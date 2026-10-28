package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/ingest-service/internal/chwriter"
	"github.com/local/logs-analyzer/services/ingest-service/internal/classifier"
	"github.com/local/logs-analyzer/services/ingest-service/internal/drain"
	"github.com/local/logs-analyzer/services/ingest-service/internal/normalizer"
	"github.com/local/logs-analyzer/services/ingest-service/internal/reader"
	"github.com/local/logs-analyzer/services/ingest-service/internal/server"
)

type mockClickHouse struct {
	mu            sync.Mutex
	ingestedFiles map[string]bool // key: caseID + ":" + sha256
	events        []string
	templates     []string
	idEdges       []string
	srvEdges      []string
	shouldFail    bool
}

func newMockClickHouse() *mockClickHouse {
	return &mockClickHouse{
		ingestedFiles: make(map[string]bool),
		events:        make([]string, 0),
		templates:     make([]string, 0),
		idEdges:       make([]string, 0),
		srvEdges:      make([]string, 0),
	}
}

func (m *mockClickHouse) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.shouldFail {
		http.Error(w, "mock clickhouse internal error", http.StatusInternalServerError)
		return
	}

	query := r.URL.Query().Get("query")
	bodyBytes, _ := io.ReadAll(r.Body)
	bodyStr := string(bodyBytes)

	if query == "" && strings.HasPrefix(bodyStr, "SELECT") {
		query = bodyStr
	}

	if strings.Contains(query, "SELECT count()") && strings.Contains(query, "ingested_files") {
		count := 0
		for key := range m.ingestedFiles {
			parts := strings.SplitN(key, ":", 2)
			if len(parts) == 2 {
				caseID := parts[0]
				sha := parts[1]
				if strings.Contains(query, caseID) && strings.Contains(query, sha) {
					count = 1
					break
				}
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "%d\n", count)
		return
	}

	if strings.Contains(query, "INSERT INTO logs_rca.ingested_files") {
		lines := strings.Split(strings.TrimSpace(bodyStr), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			var f struct {
				CaseID     string `json:"case_id"`
				FileSHA256 string `json:"file_sha256"`
			}
			if err := json.Unmarshal([]byte(line), &f); err == nil && f.CaseID != "" {
				m.ingestedFiles[f.CaseID+":"+f.FileSHA256] = true
			}
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	if strings.Contains(query, "INSERT INTO logs_rca.events_raw") {
		lines := strings.Split(strings.TrimSpace(bodyStr), "\n")
		for _, line := range lines {
			if line != "" {
				m.events = append(m.events, line)
			}
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	if strings.Contains(query, "INSERT INTO logs_rca.templates_dict") {
		lines := strings.Split(strings.TrimSpace(bodyStr), "\n")
		for _, line := range lines {
			if line != "" {
				m.templates = append(m.templates, line)
			}
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	if strings.Contains(query, "INSERT INTO logs_rca.identity_edges") {
		lines := strings.Split(strings.TrimSpace(bodyStr), "\n")
		for _, line := range lines {
			if line != "" {
				m.idEdges = append(m.idEdges, line)
			}
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	if strings.Contains(query, "INSERT INTO logs_rca.service_edges") {
		lines := strings.Split(strings.TrimSpace(bodyStr), "\n")
		for _, line := range lines {
			if line != "" {
				m.srvEdges = append(m.srvEdges, line)
			}
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func findLogExampleDir() string {
	candidates := []string{
		"../../log_example",
		"../../../log_example",
		"/workspace/log_example",
		"/log_example",
		"../../../../log_example",
		"../log_example",
		"./log_example",
		"../../log example",
		"../../../log example",
	}
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

func TestHealthEndpoint(t *testing.T) {
	mockCH := newMockClickHouse()
	chServer := httptest.NewServer(mockCH)
	defer chServer.Close()

	writer := chwriter.NewWriter(chServer.URL)
	srv := server.NewServer(writer, t.TempDir(), t.TempDir())
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var res map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode health resp: %v", err)
	}
	if res["status"] != "ok" {
		t.Fatalf("expected status=ok, got %v", res["status"])
	}
}

func TestBrowseEndpoint(t *testing.T) {
	importDir := t.TempDir()
	exampleDir := t.TempDir()

	f1 := filepath.Join(importDir, "access.jsonl")
	f2 := filepath.Join(exampleDir, "service.json.gz")
	fIgnore := filepath.Join(importDir, "notes.txt")

	if err := os.WriteFile(f1, []byte("{\"line\":\"1\"}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("dummy gz content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fIgnore, []byte("text file"), 0644); err != nil {
		t.Fatal(err)
	}

	mockCH := newMockClickHouse()
	chServer := httptest.NewServer(mockCH)
	defer chServer.Close()

	writer := chwriter.NewWriter(chServer.URL)
	srv := server.NewServer(writer, importDir, exampleDir)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/internal/ingest/browse")
	if err != nil {
		t.Fatalf("GET /internal/ingest/browse: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var files []contracts.AvailableFile
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		t.Fatalf("decode browse files: %v", err)
	}

	if len(files) < 2 {
		t.Fatalf("expected at least 2 files, got %d", len(files))
	}
}

func TestIngestStart_And_Status_And_Deduplication(t *testing.T) {
	mockCH := newMockClickHouse()
	chServer := httptest.NewServer(mockCH)
	defer chServer.Close()

	writer := chwriter.NewWriter(chServer.URL)
	srv := server.NewServer(writer, t.TempDir(), t.TempDir())
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	// 1. Create temporary JSON log file with 2 records:
	// - chat-com with 2 hour clock skew
	// - chat-session with context canceled
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test_log_sample.json")

	logData := `[
  {
    "timestamp": "1790750870128800915",
    "date": "2026-09-30T06:47:50.128800Z",
    "fields": {
      "service_name": "chat-com",
      "pod": "chat-com-6d65457f75-jcbph",
      "deployment_environment": "production"
    },
    "line": "{\"msg\": \"Operator (operator_id=55754) was not found in cache\", \"time\": \"260930 08:47:50 +0000\", \"level\": \"ERROR\", \"line\": \"cache:98\", \"type\": \"app\"}"
  },
  {
    "timestamp": "1790754654285247264",
    "date": "2026-09-30T07:50:54.285247Z",
    "fields": {
      "service_name": "chat-session",
      "pod": "chat-session-5755b46d79-2sw6b",
      "deployment_environment": "production",
      "detected_level": "error"
    },
    "line": "{\"time\":\"2026-09-30T07:50:54.285153537Z\",\"id\":\"d9f74895-ccc5-9ecb-9c38-8fdd487b7dc9\",\"method\":\"OPTIONS\",\"uri\":\"/api/v1/licenses/com/auth/\",\"status\":204,\"error\":\"\",\"latency\":9658}"
  }
]`
	if err := os.WriteFile(logPath, []byte(logData), 0644); err != nil {
		t.Fatalf("write log sample: %v", err)
	}

	caseID := "case-tdd-test-1"
	jobID := "job-101"

	// 2. Start Ingest
	startReq := contracts.StartIngestRequest{
		JobID:     jobID,
		CaseID:    caseID,
		FilePaths: []string{logPath},
	}
	body, _ := json.Marshal(startReq)

	resp, err := http.Post(ts.URL+"/internal/ingest/start", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /internal/ingest/start: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d", resp.StatusCode)
	}

	var initStatus contracts.IngestStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&initStatus); err != nil {
		t.Fatalf("decode init status: %v", err)
	}
	if initStatus.JobID != jobID || initStatus.Status != contracts.IngestStatusRunning {
		t.Fatalf("unexpected init status: %+v", initStatus)
	}

	// 3. Poll for completion
	var finalStatus contracts.IngestStatusResponse
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		statusResp, err := http.Get(ts.URL + "/internal/ingest/status/" + jobID)
		if err != nil {
			t.Fatalf("GET /internal/ingest/status: %v", err)
		}
		if statusResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 for status, got %d", statusResp.StatusCode)
		}
		if err := json.NewDecoder(statusResp.Body).Decode(&finalStatus); err != nil {
			_ = statusResp.Body.Close()
			t.Fatalf("decode status: %v", err)
		}
		_ = statusResp.Body.Close()

		if finalStatus.Status == contracts.IngestStatusCompleted || finalStatus.Status == contracts.IngestStatusFailed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if finalStatus.Status != contracts.IngestStatusCompleted {
		t.Fatalf("expected job status COMPLETED, got %s (error: %s)", finalStatus.Status, finalStatus.Error)
	}

	// 4. Validate RowsIngested and ClockSkewWarnings
	if finalStatus.RowsIngested != 2 {
		t.Fatalf("expected RowsIngested == 2, got %d", finalStatus.RowsIngested)
	}
	if finalStatus.FilesDone != 1 {
		t.Fatalf("expected FilesDone == 1, got %d", finalStatus.FilesDone)
	}
	if finalStatus.SkippedDuplicateFiles != 0 {
		t.Fatalf("expected SkippedDuplicateFiles == 0, got %d", finalStatus.SkippedDuplicateFiles)
	}
	if len(finalStatus.ClockSkewWarnings) == 0 {
		t.Fatalf("expected non-empty ClockSkewWarnings for chat-com clock skew")
	}

	foundClockSkewWarning := false
	for _, w := range finalStatus.ClockSkewWarnings {
		if strings.Contains(w, "chat-com") && strings.Contains(w, "clock skew") {
			foundClockSkewWarning = true
			break
		}
	}
	if !foundClockSkewWarning {
		t.Fatalf("expected chat-com clock skew warning, got: %v", finalStatus.ClockSkewWarnings)
	}

	// 5. Test Deduplication: second run with same file & same case_id
	jobID2 := "job-102"
	startReq2 := contracts.StartIngestRequest{
		JobID:     jobID2,
		CaseID:    caseID,
		FilePaths: []string{logPath},
	}
	body2, _ := json.Marshal(startReq2)

	resp2, err := http.Post(ts.URL+"/internal/ingest/start", "application/json", bytes.NewReader(body2))
	if err != nil {
		t.Fatalf("POST second start: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted on second run, got %d", resp2.StatusCode)
	}

	deadline2 := time.Now().Add(5 * time.Second)
	var finalStatus2 contracts.IngestStatusResponse
	for time.Now().Before(deadline2) {
		statusResp, err := http.Get(ts.URL + "/internal/ingest/status/" + jobID2)
		if err != nil {
			t.Fatalf("GET status 2: %v", err)
		}
		if err := json.NewDecoder(statusResp.Body).Decode(&finalStatus2); err != nil {
			_ = statusResp.Body.Close()
			t.Fatalf("decode status 2: %v", err)
		}
		_ = statusResp.Body.Close()

		if finalStatus2.Status == contracts.IngestStatusCompleted || finalStatus2.Status == contracts.IngestStatusFailed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if finalStatus2.Status != contracts.IngestStatusCompleted {
		t.Fatalf("expected job 2 COMPLETED, got %s (error: %s)", finalStatus2.Status, finalStatus2.Error)
	}
	if finalStatus2.SkippedDuplicateFiles != 1 {
		t.Fatalf("expected SkippedDuplicateFiles == 1 on second run, got %d", finalStatus2.SkippedDuplicateFiles)
	}
	if finalStatus2.RowsIngested != 0 {
		t.Fatalf("expected RowsIngested == 0 on duplicate skip, got %d", finalStatus2.RowsIngested)
	}
}

func TestIngestBatchEndpoint(t *testing.T) {
	mockCH := newMockClickHouse()
	chServer := httptest.NewServer(mockCH)
	defer chServer.Close()

	writer := chwriter.NewWriter(chServer.URL)
	srv := server.NewServer(writer, t.TempDir(), t.TempDir())
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	// Call POST /internal/ingest/batch with 2 records from tilt:chat-session
	batchReq := contracts.IngestBatchRequest{
		CaseID:     "case-live-tilt",
		SourceName: "tilt:chat-session-7f9c",
		Records: []contracts.RawScriptRecord{
			{
				Timestamp: "1790754654285247264",
				Date:      "2026-09-30T07:50:54.285247Z",
				Line:      `{"time":"2026-09-30T07:50:54.285153537Z","id":"d9f74895-ccc5-9ecb-9c38-8fdd487b7dc9","method":"OPTIONS","uri":"/api/v1/licenses/com/auth/","status":204,"latency":9658}`,
			},
			{
				Timestamp: "1790750870128800915",
				Date:      "2026-09-30T06:47:50.128800Z",
				Line:      `{"msg":"Operator (operator_id=55754) not found","time":"260930 08:47:50 +0000","level":"ERROR"}`,
			},
		},
	}

	body, _ := json.Marshal(batchReq)
	resp, err := http.Post(ts.URL+"/internal/ingest/batch", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /internal/ingest/batch: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(b))
	}

	var batchResp contracts.IngestBatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&batchResp); err != nil {
		t.Fatalf("decode batch response: %v", err)
	}

	if batchResp.RowsIngested != 2 {
		t.Fatalf("expected RowsIngested == 2, got %d", batchResp.RowsIngested)
	}
	if len(batchResp.ClockSkewWarnings) == 0 {
		t.Fatalf("expected ClockSkewWarnings for skewed record")
	}

	mockCH.mu.Lock()
	eventsCount := len(mockCH.events)
	mockCH.mu.Unlock()
	if eventsCount != 2 {
		t.Fatalf("expected mock ClickHouse to receive 2 events, got %d", eventsCount)
	}
}

func TestValidationAndErrors(t *testing.T) {
	mockCH := newMockClickHouse()
	chServer := httptest.NewServer(mockCH)
	defer chServer.Close()

	writer := chwriter.NewWriter(chServer.URL)
	srv := server.NewServer(writer, t.TempDir(), t.TempDir())
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	// 1. Start with empty request
	resp, err := http.Post(ts.URL+"/internal/ingest/start", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty start request, got %d", resp.StatusCode)
	}

	// 2. Status for non-existent job
	resp2, err := http.Get(ts.URL + "/internal/ingest/status/non-existent-job")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown job, got %d", resp2.StatusCode)
	}

	// 3. Batch with invalid request
	resp3, err := http.Post(ts.URL+"/internal/ingest/batch", "application/json", strings.NewReader(`{"case_id":""}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty batch request, got %d", resp3.StatusCode)
	}

	// 4. Batch failure when ClickHouse returns error
	mockCH.mu.Lock()
	mockCH.shouldFail = true
	mockCH.mu.Unlock()

	validBatch := contracts.IngestBatchRequest{
		CaseID:     "c1",
		SourceName: "test",
		Records: []contracts.RawScriptRecord{
			{Line: "test"},
		},
	}
	b, _ := json.Marshal(validBatch)
	resp4, err := http.Post(ts.URL+"/internal/ingest/batch", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp4.Body.Close()
	if resp4.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 when CH fails, got %d", resp4.StatusCode)
	}
}

func TestCHWriter_DirectUnitTests(t *testing.T) {
	mockCH := newMockClickHouse()
	chServer := httptest.NewServer(mockCH)
	defer chServer.Close()

	writer := chwriter.NewWriter(chServer.URL)
	ctx := context.Background()

	// 1. IsFileAlreadyIngested returns false
	ingested, err := writer.IsFileAlreadyIngested(ctx, "case-1", "sha123")
	if err != nil {
		t.Fatalf("IsFileAlreadyIngested failed: %v", err)
	}
	if ingested {
		t.Fatalf("expected false, got true")
	}

	// 2. RecordIngestedFile
	err = writer.RecordIngestedFile(ctx, "case-1", "sha123", "test.json", 100)
	if err != nil {
		t.Fatalf("RecordIngestedFile failed: %v", err)
	}

	// 3. IsFileAlreadyIngested returns true now
	ingested, err = writer.IsFileAlreadyIngested(ctx, "case-1", "sha123")
	if err != nil {
		t.Fatalf("IsFileAlreadyIngested failed: %v", err)
	}
	if !ingested {
		t.Fatalf("expected true after recording")
	}

	// 4. FlushBatch with data
	now := time.Now().UTC()
	events := []contracts.NormalizedEvent{
		{
			CaseID:      "case-1",
			EventID:     "00000000-0000-0000-0000-000000000001",
			Ts:          now,
			AppTs:       &now,
			ClockSkewMs: 100,
			Service:     "svc-a",
			Level:       "INFO",
			Message:     "hello",
		},
	}
	tpls := []contracts.TemplateRecord{
		{
			CaseID:       "case-1",
			Service:      "svc-a",
			TemplateID:   12345,
			TemplateText: "hello",
			FirstSeen:    now,
			LastSeen:     now,
		},
	}
	idEdges := []contracts.IdentityEdge{
		{
			CaseID:    "case-1",
			Ts:        now,
			Service:   "svc-a",
			LeftType:  "request_id",
			LeftVal:   "req-1",
			RightType: "trace_id",
			RightVal:  "trace-1",
		},
	}
	srvEdges := []contracts.ServiceEdge{
		{
			CaseID:         "case-1",
			CallerService:  "svc-a",
			CalleeService:  "svc-b",
			EvidenceSource: "peer_url",
			CallCount:      1,
		},
	}

	err = writer.FlushBatch(ctx, events, tpls, idEdges, srvEdges)
	if err != nil {
		t.Fatalf("FlushBatch failed: %v", err)
	}

	// 5. ClickHouse error handling
	mockCH.mu.Lock()
	mockCH.shouldFail = true
	mockCH.mu.Unlock()

	err = writer.RecordIngestedFile(ctx, "case-1", "sha456", "test.json", 50)
	if err == nil {
		t.Fatalf("expected error when CH fails")
	}

	_, err = writer.IsFileAlreadyIngested(ctx, "case-1", "sha456")
	if err == nil {
		t.Fatalf("expected error from IsFileAlreadyIngested when CH fails")
	}

	err = writer.FlushBatch(ctx, events, nil, nil, nil)
	if err == nil {
		t.Fatalf("expected error from FlushBatch when CH fails")
	}
}

// TestGoldenRealLogExamples: эталонный «Золотой тест» на реальных файлах
func TestGoldenRealLogExamples(t *testing.T) {
	exampleDir := findLogExampleDir()
	if exampleDir == "" {
		t.Skip("log_example directory not found, skipping golden test")
	}

	entries, err := os.ReadDir(exampleDir)
	if err != nil {
		t.Skipf("cannot read log_example dir %s: %v", exampleDir, err)
	}

	var chatComPath, chatSessionPath string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "chat-com_") && strings.HasSuffix(name, ".json") {
			chatComPath = filepath.Join(exampleDir, name)
		}
		if strings.HasPrefix(name, "chat-session_") && strings.HasSuffix(name, ".json") {
			chatSessionPath = filepath.Join(exampleDir, name)
		}
	}

	if chatComPath == "" || chatSessionPath == "" {
		t.Skipf("real log files not found in %s (chatCom=%s, chatSession=%s)", exampleDir, chatComPath, chatSessionPath)
	}

	ctx := context.Background()

	// 1. Test chat-session: 500 records
	t.Run("GoldenChatSession", func(t *testing.T) {
		miner := drain.NewMiner()
		var count int
		var normalizedCount int

		err := reader.StreamPath(ctx, chatSessionPath, func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
			if count >= 500 {
				return io.EOF // Stop early after 500
			}
			count++

			ev, parsedJSON := normalizer.NormalizeRecord("case-golden", rec, sourceFile, recordIdx)
			idEdges, srvEdge := classifier.EnrichAndClassify(&ev, parsedJSON, rec.Line)
			_ = idEdges
			_ = srvEdge
			tpl := miner.Mine(&ev)

			if ev.TemplateID == 0 {
				t.Fatalf("record %d: expected non-zero template ID", recordIdx)
			}
			if tpl.TemplateID != ev.TemplateID {
				t.Fatalf("record %d: template ID mismatch: %d vs %d", recordIdx, tpl.TemplateID, ev.TemplateID)
			}

			// JWT mask check
			if strings.Contains(ev.RawRedacted, "eyJhbGci") {
				t.Fatalf("record %d: unmasked JWT token found in RawRedacted: %s", recordIdx, ev.RawRedacted)
			}

			// False positive error check: HTTP 200/204 with detected_level=error should be normalized to INFO
			if ev.SchemaKind == contracts.SchemaEchoAccess && (ev.HTTPStatus == 200 || ev.HTTPStatus == 204) {
				if ev.Level != contracts.LevelInfo {
					t.Fatalf("record %d: HTTP %d expected INFO level, got %s", recordIdx, ev.HTTPStatus, ev.Level)
				}
				normalizedCount++
			}

			return nil
		})

		if err != nil && err != io.EOF {
			t.Fatalf("StreamPath failed on chat-session: %v", err)
		}
		if count < 500 {
			t.Fatalf("expected at least 500 records processed, got %d", count)
		}
		if normalizedCount == 0 {
			t.Fatalf("expected at least some HTTP 200/204 normalized events in chat-session")
		}
	})

	// 2. Test chat-com: 500 records
	t.Run("GoldenChatCom", func(t *testing.T) {
		miner := drain.NewMiner()
		var count int
		var skewDetectedCount int

		err := reader.StreamPath(ctx, chatComPath, func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
			if count >= 500 {
				return io.EOF // Stop early after 500
			}
			count++

			ev, parsedJSON := normalizer.NormalizeRecord("case-golden", rec, sourceFile, recordIdx)
			idEdges, srvEdge := classifier.EnrichAndClassify(&ev, parsedJSON, rec.Line)
			_ = idEdges
			_ = srvEdge
			_ = miner.Mine(&ev)

			if ev.ClockSkewMs > 7000000 {
				skewDetectedCount++
			}

			return nil
		})

		if err != nil && err != io.EOF {
			t.Fatalf("StreamPath failed on chat-com: %v", err)
		}
		if count < 500 {
			t.Fatalf("expected at least 500 records processed, got %d", count)
		}
		if skewDetectedCount == 0 {
			t.Fatalf("expected chat-com to exhibit ClockSkewMs > 7000000 (+2 hours)")
		}
	})
}
