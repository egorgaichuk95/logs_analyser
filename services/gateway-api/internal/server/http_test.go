package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/gateway-api/internal/explorer"
	"github.com/local/logs-analyzer/services/gateway-api/internal/metadb"
	"github.com/local/logs-analyzer/services/gateway-api/internal/storage"
)

type mockBackends struct {
	clickhouse    *httptest.Server
	ingest        *httptest.Server
	rca           *httptest.Server
	evidence      *httptest.Server
	tiltCollector *httptest.Server

	chRawPurged atomic.Bool
}

func setupMockBackends(t *testing.T, reportsDir string) *mockBackends {
	b := &mockBackends{}

	// Mock ClickHouse
	b.clickhouse = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		query := string(body)

		if strings.Contains(query, "system.parts") {
			// ClickHouse parts query
			if b.chRawPurged.Load() {
				w.Write([]byte(`{"table":"events_agg_1m","bytes":1000}` + "\n" +
					`{"table":"templates_dict","bytes":200}` + "\n"))
			} else {
				w.Write([]byte(`{"table":"events_raw","bytes":5000}` + "\n" +
					`{"table":"identity_edges","bytes":500}` + "\n" +
					`{"table":"events_agg_1m","bytes":1000}` + "\n" +
					`{"table":"templates_dict","bytes":200}` + "\n"))
			}
			return
		}

		if strings.Contains(query, "TRUNCATE TABLE") {
			b.chRawPurged.Store(true)
			w.WriteHeader(http.StatusOK)
			return
		}

		if strings.Contains(query, "count()") {
			if b.chRawPurged.Load() || strings.Contains(query, "case-empty") {
				w.Write([]byte(`{"cnt":0}` + "\n"))
			} else {
				w.Write([]byte(`{"cnt":42}` + "\n"))
			}
			return
		}

		if strings.Contains(query, "events_raw") {
			if b.chRawPurged.Load() {
				// return 0 rows
				w.WriteHeader(http.StatusOK)
				return
			}
			row := `{"case_id":"case-test","event_id":"00000000-0000-0000-0000-000000000001","ts":"2026-10-28 20:15:30.000000000","service":"chat-com","pod":"chat-com-1","level":"ERROR","message":"DB error"}` + "\n"
			w.Write([]byte(row))
			return
		}

		if strings.Contains(query, "DELETE WHERE") || strings.Contains(query, "DROP PARTITION") {
			w.WriteHeader(http.StatusOK)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))

	// Mock Ingest Service
	b.ingest = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/health" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
			return
		}

		if path == "/internal/ingest/browse" {
			files := []contracts.AvailableFile{
				{
					Path:      "/import/app.log",
					Name:      "app.log",
					SizeBytes: 1024,
					ModTime:   time.Now().UTC(),
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(files)
			return
		}

		if path == "/internal/ingest/start" && r.Method == http.MethodPost {
			var req contracts.StartIngestRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			resp := contracts.IngestStatusResponse{
				JobID:      req.JobID,
				CaseID:     req.CaseID,
				Status:     contracts.IngestStatusRunning,
				FilesTotal: len(req.FilePaths),
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.HasPrefix(path, "/internal/ingest/status/") {
			jobID := strings.TrimPrefix(path, "/internal/ingest/status/")
			resp := contracts.IngestStatusResponse{
				JobID:        jobID,
				CaseID:       "case-1",
				Status:       contracts.IngestStatusCompleted,
				FilesTotal:   1,
				FilesDone:    1,
				RowsIngested: 100,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		http.NotFound(w, r)
	}))

	// Mock RCA Engine
	b.rca = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/health" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
			return
		}

		if path == "/internal/rca/analyze" && r.Method == http.MethodPost {
			var req contracts.AnalyzeCaseRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			now := time.Now().UTC()
			resp := contracts.AnalyzeCaseResponse{
				CaseID:        req.CaseID,
				RunID:         "run-test-1",
				IncidentStart: now.Add(-10 * time.Minute),
				IncidentEnd:   now,
				Hypotheses: []contracts.Hypothesis{
					{
						ID:          "H1",
						Rank:        1,
						Role:        contracts.RoleRootCause,
						Confidence:  0.95,
						Service:     "chat-com",
						TemplateID:  12345,
						SourcePlace: "server.py:100",
					},
				},
				ServiceEdges: []contracts.ServiceEdge{
					{
						CaseID:        req.CaseID,
						CallerService: "chat-com",
						CalleeService: "chat-session",
						CallCount:     50,
						ErrorCount:    5,
					},
				},
				Timeline: []contracts.TimelinePoint{
					{
						Bucket:      now,
						Service:     "chat-com",
						Level:       contracts.LevelError,
						EventsCount: 5,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.HasPrefix(path, "/internal/rca/chain/") {
			events := []contracts.NormalizedEvent{
				{
					CaseID:  "case-test",
					EventID: "00000000-0000-0000-0000-000000000001",
					Service: "chat-com",
					Level:   contracts.LevelError,
					Message: "Chain root event",
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(events)
			return
		}

		http.NotFound(w, r)
	}))

	// Mock Evidence Service
	b.evidence = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/health" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
			return
		}

		if path == "/internal/evidence/build" && r.Method == http.MethodPost {
			var req contracts.BuildEvidenceRequest
			_ = json.NewDecoder(r.Body).Decode(&req)

			// Create report files in reportsDir
			runDir := filepath.Join(reportsDir, req.CaseID, req.RunID)
			_ = os.MkdirAll(runDir, 0755)
			mdPath := filepath.Join(runDir, "report.md")
			htmlPath := filepath.Join(runDir, "report.html")
			geminiPath := filepath.Join(runDir, "gemini_bundle.md")
			zipPath := filepath.Join(runDir, fmt.Sprintf("evidence_%s.zip", req.CaseID))

			_ = os.WriteFile(mdPath, []byte("# RCA Report\nRoot cause chat-com"), 0644)
			_ = os.WriteFile(htmlPath, []byte("<html><body>RCA Report</body></html>"), 0644)
			_ = os.WriteFile(geminiPath, []byte("# GEMINI PROMPT\nAnalyze root cause"), 0644)
			_ = os.WriteFile(zipPath, []byte("PK\x03\x04mockzip"), 0644)

			resp := contracts.BuildEvidenceResponse{
				CaseID:       req.CaseID,
				RunID:        req.RunID,
				ReportDir:    runDir,
				MarkdownPath: mdPath,
				HTMLPath:     htmlPath,
				GeminiPath:   geminiPath,
				ZipPath:      zipPath,
				SizeBytes:    1024,
				EvidenceItems: []contracts.EvidenceItem{
					{
						ID:      "E-01",
						Kind:    contracts.EvidenceKindRawLog,
						Service: "chat-com",
						Summary: "Database connection timeout",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		http.NotFound(w, r)
	}))

	// Mock Tilt Collector
	b.tiltCollector = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/health" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
			return
		}

		if path == "/internal/tilt/targets" {
			targets := []contracts.TiltTarget{
				{
					ID:          "target-1",
					Runtime:     contracts.RuntimeDocker,
					ServiceName: "chat-com",
					PodName:     "chat-com-container",
					Status:      "running",
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(targets)
			return
		}

		if path == "/internal/tilt/start" && r.Method == http.MethodPost {
			var req contracts.TiltStartCaptureRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			now := time.Now().UTC()
			resp := contracts.TiltStatusResponse{
				CaseID:         req.CaseID,
				Active:         true,
				StartedAt:      &now,
				TargetsWatched: 1,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if path == "/internal/tilt/stop" && r.Method == http.MethodPost {
			resp := contracts.TiltStatusResponse{
				Active: false,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if path == "/internal/tilt/status" {
			resp := contracts.TiltStatusResponse{
				Active:         true,
				TargetsWatched: 1,
				LinesCaptured:  50,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		http.NotFound(w, r)
	}))

	return b
}

func (b *mockBackends) Close() {
	b.clickhouse.Close()
	b.ingest.Close()
	b.rca.Close()
	b.evidence.Close()
	b.tiltCollector.Close()
}

func setupTestGateway(t *testing.T) (*Server, *httptest.Server, *mockBackends, string, string) {
	tmpDir := t.TempDir()
	stagingDir := filepath.Join(tmpDir, "staging")
	reportsDir := filepath.Join(tmpDir, "reports")
	metaPath := filepath.Join(tmpDir, "meta", "state.json")

	_ = os.MkdirAll(stagingDir, 0755)
	_ = os.MkdirAll(reportsDir, 0755)

	backends := setupMockBackends(t, reportsDir)

	metaStore, err := metadb.NewStore(metaPath)
	if err != nil {
		t.Fatalf("failed to create metadb store: %v", err)
	}

	storageMgr := storage.NewManager(
		backends.clickhouse.URL,
		stagingDir,
		reportsDir,
		16106127360,
		10737418240,
		metaStore,
	)

	exp := explorer.NewExplorer(
		backends.clickhouse.URL,
		metaStore,
		backends.clickhouse.Client(),
	)

	cfg := Config{
		CHHTTPURL:        backends.clickhouse.URL,
		IngestURL:        backends.ingest.URL,
		RCAURL:           backends.rca.URL,
		EvidenceURL:      backends.evidence.URL,
		TiltCollectorURL: backends.tiltCollector.URL,
		StagingDir:       stagingDir,
		ReportsDir:       reportsDir,
		QuotaBytes:       16106127360,
		RawQuotaBytes:    10737418240,
		MetaStore:        metaStore,
		StorageMgr:       storageMgr,
		Explorer:         exp,
		HTTPClient:       http.DefaultClient,
	}

	srv := NewServer(cfg)
	ts := httptest.NewServer(srv.Routes())

	return srv, ts, backends, stagingDir, reportsDir
}

func TestGateway_CORSAndHealth(t *testing.T) {
	_, ts, backends, _, _ := setupTestGateway(t)
	defer ts.Close()
	defer backends.Close()

	// 1. OPTIONS preflight
	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/api/v1/health", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("options request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected CORS origin *, got %s", resp.Header.Get("Access-Control-Allow-Origin"))
	}
	if !strings.Contains(resp.Header.Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("expected CORS allow methods to contain POST, got %s", resp.Header.Get("Access-Control-Allow-Methods"))
	}

	// 2. GET /api/v1/health
	resp, err = http.Get(ts.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var h map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&h)
	if h["status"] != "ok" {
		t.Errorf("expected status ok, got %v", h)
	}
}

func TestGateway_EndToEndScenario(t *testing.T) {
	// Full end-to-end integration scenario:
	// POST /api/v1/cases
	// -> GET /api/v1/tilt/targets
	// -> POST /api/v1/cases/{id}/tilt/start
	// -> POST /api/v1/cases/{id}/analyze
	// -> POST /api/v1/storage/purge-raw
	// -> GET /api/v1/cases/{id}/analysis (200 OK after purge)
	// -> GET /api/v1/cases/{id}/reports/{run_id}/gemini (200 OK after purge)
	// -> GET /api/v1/cases/{id}/logs/context (RawPurged: true)
	_, ts, backends, _, _ := setupTestGateway(t)
	defer ts.Close()
	defer backends.Close()

	// Step 1: POST /api/v1/cases
	createReq := contracts.CreateCaseRequest{
		Title:       "Production Outage Chat Service",
		Description: "Timeout cascade across chat-com and chat-session",
	}
	body, _ := json.Marshal(createReq)
	resp, err := http.Post(ts.URL+"/api/v1/cases", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create case failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 200/201 on create case, got %d", resp.StatusCode)
	}
	var caseMeta contracts.CaseMeta
	if err := json.NewDecoder(resp.Body).Decode(&caseMeta); err != nil {
		t.Fatalf("decode case meta: %v", err)
	}
	resp.Body.Close()

	if caseMeta.ID == "" {
		t.Fatal("expected non-empty case ID")
	}
	caseID := caseMeta.ID

	// Step 2: GET /api/v1/tilt/targets
	resp, err = http.Get(ts.URL + "/api/v1/tilt/targets")
	if err != nil {
		t.Fatalf("get tilt targets: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on tilt targets, got %d", resp.StatusCode)
	}
	var targets []contracts.TiltTarget
	_ = json.NewDecoder(resp.Body).Decode(&targets)
	resp.Body.Close()
	if len(targets) == 0 {
		t.Fatal("expected at least 1 tilt target")
	}

	// Step 3: POST /api/v1/cases/{id}/tilt/start
	tiltStartReq := contracts.TiltStartCaptureRequest{
		SinceSeconds: 300,
	}
	tStartBody, _ := json.Marshal(tiltStartReq)
	resp, err = http.Post(ts.URL+"/api/v1/cases/"+caseID+"/tilt/start", "application/json", bytes.NewReader(tStartBody))
	if err != nil {
		t.Fatalf("tilt start: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on tilt start, got %d", resp.StatusCode)
	}
	var tiltStatus contracts.TiltStatusResponse
	_ = json.NewDecoder(resp.Body).Decode(&tiltStatus)
	resp.Body.Close()
	if !tiltStatus.Active {
		t.Error("expected tilt active true")
	}
	if tiltStatus.CaseID != caseID {
		t.Errorf("expected caseID %s, got %s", caseID, tiltStatus.CaseID)
	}

	// Step 4: POST /api/v1/cases/{id}/analyze
	analyzeReq := contracts.AnalyzeCaseRequest{
		CaseID: caseID,
	}
	aBody, _ := json.Marshal(analyzeReq)
	resp, err = http.Post(ts.URL+"/api/v1/cases/"+caseID+"/analyze", "application/json", bytes.NewReader(aBody))
	if err != nil {
		t.Fatalf("analyze case: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 on analyze, got %d: %s", resp.StatusCode, string(b))
	}
	var analyzeResp contracts.AnalyzeCaseResponse
	_ = json.NewDecoder(resp.Body).Decode(&analyzeResp)
	resp.Body.Close()

	if analyzeResp.RunID == "" {
		t.Fatal("expected non-empty runID")
	}
	if len(analyzeResp.EvidenceItems) == 0 {
		t.Fatal("expected non-empty EvidenceItems returned after analysis orchestration")
	}
	runID := analyzeResp.RunID

	// Step 5: Verify Context Logs BEFORE purge
	resp, err = http.Get(ts.URL + "/api/v1/cases/" + caseID + "/logs/context?pod=chat-com-1&ts=2026-10-28T20:15:30Z&mode=pod")
	if err != nil {
		t.Fatalf("get context before purge: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on context before purge, got %d", resp.StatusCode)
	}
	var ctxRespBefore contracts.ContextLogsResponse
	_ = json.NewDecoder(resp.Body).Decode(&ctxRespBefore)
	resp.Body.Close()
	if ctxRespBefore.RawPurged {
		t.Errorf("expected RawPurged: false before purge, got true")
	}
	if len(ctxRespBefore.Events) == 0 {
		t.Errorf("expected events > 0 before purge")
	}

	// Step 6: POST /api/v1/storage/purge-raw
	resp, err = http.Post(ts.URL+"/api/v1/storage/purge-raw", "application/json", nil)
	if err != nil {
		t.Fatalf("purge raw failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on purge-raw, got %d", resp.StatusCode)
	}
	var purgeResp contracts.PurgeRawResponse
	_ = json.NewDecoder(resp.Body).Decode(&purgeResp)
	resp.Body.Close()
	if purgeResp.Status != "OK" {
		t.Errorf("expected OK purge status, got %s", purgeResp.Status)
	}

	// Step 7: GET /api/v1/cases/{id}/analysis AFTER purge-raw -> MUST RETURN 200 OK!
	resp, err = http.Get(ts.URL + "/api/v1/cases/" + caseID + "/analysis")
	if err != nil {
		t.Fatalf("get analysis after purge failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on get analysis after purge, got %d", resp.StatusCode)
	}
	var cachedAnalysis contracts.AnalyzeCaseResponse
	_ = json.NewDecoder(resp.Body).Decode(&cachedAnalysis)
	resp.Body.Close()
	if cachedAnalysis.RunID != runID {
		t.Errorf("expected runID %s, got %s", runID, cachedAnalysis.RunID)
	}

	// Step 8: GET /api/v1/cases/{id}/reports/{run_id}/gemini AFTER purge-raw -> MUST RETURN 200 OK!
	resp, err = http.Get(ts.URL + "/api/v1/cases/" + caseID + "/reports/" + runID + "/gemini")
	if err != nil {
		t.Fatalf("get gemini report after purge failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on get gemini report after purge, got %d", resp.StatusCode)
	}
	geminiContent, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(geminiContent), "GEMINI PROMPT") {
		t.Errorf("unexpected gemini report content: %s", string(geminiContent))
	}

	// Step 9: GET /api/v1/cases/{id}/logs/context AFTER purge-raw -> MUST RETURN RawPurged: true!
	resp, err = http.Get(ts.URL + "/api/v1/cases/" + caseID + "/logs/context?pod=chat-com-1&ts=2026-10-28T20:15:30Z&mode=pod")
	if err != nil {
		t.Fatalf("get context after purge: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on context after purge, got %d", resp.StatusCode)
	}
	var ctxRespAfter contracts.ContextLogsResponse
	_ = json.NewDecoder(resp.Body).Decode(&ctxRespAfter)
	resp.Body.Close()
	if !ctxRespAfter.RawPurged {
		t.Errorf("expected RawPurged: true after purge, got false")
	}
}

func TestGateway_StorageEndpoints(t *testing.T) {
	_, ts, backends, _, _ := setupTestGateway(t)
	defer ts.Close()
	defer backends.Close()

	// GET /api/v1/storage/status
	resp, err := http.Get(ts.URL + "/api/v1/storage/status")
	if err != nil {
		t.Fatalf("storage status: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var st contracts.StorageStatusResponse
	_ = json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()

	if st.QuotaBytes != 16106127360 {
		t.Errorf("expected quota %d, got %d", 16106127360, st.QuotaBytes)
	}
}

func TestGateway_CasesCRUDAndIngest(t *testing.T) {
	_, ts, backends, _, _ := setupTestGateway(t)
	defer ts.Close()
	defer backends.Close()

	// 1. Create case
	cBody, _ := json.Marshal(contracts.CreateCaseRequest{Title: "Case CRUD", Description: "Test"})
	resp, err := http.Post(ts.URL+"/api/v1/cases", "application/json", bytes.NewReader(cBody))
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	var meta contracts.CaseMeta
	_ = json.NewDecoder(resp.Body).Decode(&meta)
	resp.Body.Close()
	caseID := meta.ID

	// 2. List cases
	resp, err = http.Get(ts.URL + "/api/v1/cases")
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	var cases []contracts.CaseMeta
	_ = json.NewDecoder(resp.Body).Decode(&cases)
	resp.Body.Close()
	if len(cases) != 1 || cases[0].ID != caseID {
		t.Errorf("expected case in list, got %+v", cases)
	}

	// 3. GET /api/v1/files/available
	resp, err = http.Get(ts.URL + "/api/v1/files/available")
	if err != nil {
		t.Fatalf("get files available: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var files []contracts.AvailableFile
	_ = json.NewDecoder(resp.Body).Decode(&files)
	resp.Body.Close()
	if len(files) == 0 {
		t.Error("expected files from ingest browse")
	}

	// 4. Ingest local
	ingLocalBody, _ := json.Marshal(contracts.IngestLocalRequest{FilePaths: []string{"/import/app.log"}})
	resp, err = http.Post(ts.URL+"/api/v1/cases/"+caseID+"/ingest-local", "application/json", bytes.NewReader(ingLocalBody))
	if err != nil {
		t.Fatalf("ingest local: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on ingest-local, got %d", resp.StatusCode)
	}
	var ingStatus contracts.IngestStatusResponse
	_ = json.NewDecoder(resp.Body).Decode(&ingStatus)
	resp.Body.Close()
	if ingStatus.Status != contracts.IngestStatusRunning {
		t.Errorf("expected RUNNING, got %s", ingStatus.Status)
	}

	// 5. Check job status
	resp, err = http.Get(ts.URL + "/api/v1/jobs/" + ingStatus.JobID)
	if err != nil {
		t.Fatalf("get job status: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var jobStatus contracts.IngestStatusResponse
	_ = json.NewDecoder(resp.Body).Decode(&jobStatus)
	resp.Body.Close()
	if jobStatus.Status != contracts.IngestStatusCompleted {
		t.Errorf("expected COMPLETED, got %s", jobStatus.Status)
	}

	// 6. Upload file multipart
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	part, _ := w.CreateFormFile("files", "test_upload.json")
	part.Write([]byte(`{"test":"log line"}`))
	w.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/cases/"+caseID+"/upload", &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on upload, got %d", resp.StatusCode)
	}
	var upStatus contracts.IngestStatusResponse
	_ = json.NewDecoder(resp.Body).Decode(&upStatus)
	resp.Body.Close()
	if upStatus.JobID == "" {
		t.Error("expected non-empty jobID from upload ingest start")
	}

	// 7. Chain proxy
	resp, err = http.Get(ts.URL + "/api/v1/cases/" + caseID + "/chain?key=req-123")
	if err != nil {
		t.Fatalf("get chain: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on chain, got %d", resp.StatusCode)
	}
	var chain []contracts.NormalizedEvent
	_ = json.NewDecoder(resp.Body).Decode(&chain)
	resp.Body.Close()
	if len(chain) == 0 {
		t.Error("expected chain events")
	}

	// 8. Delete case
	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/cases/"+caseID, nil)
	resp, err = http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("delete case: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on delete, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify case is deleted
	resp, err = http.Get(ts.URL + "/api/v1/cases/" + caseID + "/analysis")
	if err != nil {
		t.Fatalf("get analysis: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after case deletion, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestGateway_TiltProxies(t *testing.T) {
	_, ts, backends, _, _ := setupTestGateway(t)
	defer ts.Close()
	defer backends.Close()

	// GET /api/v1/cases/case-123/tilt/status
	resp, err := http.Get(ts.URL + "/api/v1/cases/case-123/tilt/status")
	if err != nil {
		t.Fatalf("tilt status: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var status contracts.TiltStatusResponse
	_ = json.NewDecoder(resp.Body).Decode(&status)
	resp.Body.Close()
	if !status.Active {
		t.Error("expected active true")
	}

	// POST /api/v1/cases/case-123/tilt/stop
	resp, err = http.Post(ts.URL+"/api/v1/cases/case-123/tilt/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("tilt stop: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var stopStatus contracts.TiltStatusResponse
	_ = json.NewDecoder(resp.Body).Decode(&stopStatus)
	resp.Body.Close()
	if stopStatus.Active {
		t.Error("expected active false after stop")
	}
}

func TestGateway_ReportsEndpoints(t *testing.T) {
	_, ts, backends, _, reportsDir := setupTestGateway(t)
	defer ts.Close()
	defer backends.Close()

	caseID := "case-rep-1"
	runID := "run-1"
	runDir := filepath.Join(reportsDir, caseID, runID)
	_ = os.MkdirAll(runDir, 0755)

	_ = os.WriteFile(filepath.Join(runDir, "report.md"), []byte("# Report MD"), 0644)
	_ = os.WriteFile(filepath.Join(runDir, "report.html"), []byte("<h1>Report HTML</h1>"), 0644)
	_ = os.WriteFile(filepath.Join(runDir, "gemini_bundle.md"), []byte("# Gemini Bundle"), 0644)
	_ = os.WriteFile(filepath.Join(runDir, fmt.Sprintf("evidence_%s.zip", caseID)), []byte("PK\x03\x04zipdata"), 0644)

	// Markdown
	resp, err := http.Get(ts.URL + "/api/v1/cases/" + caseID + "/reports/" + runID + "/markdown")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for markdown report, got status: %v, err: %v", resp.StatusCode, err)
	}
	resp.Body.Close()

	// HTML
	resp, err = http.Get(ts.URL + "/api/v1/cases/" + caseID + "/reports/" + runID + "/html")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for html report, got status: %v, err: %v", resp.StatusCode, err)
	}
	resp.Body.Close()

	// Zip
	resp, err = http.Get(ts.URL + "/api/v1/cases/" + caseID + "/reports/" + runID + "/zip")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for zip report, got status: %v, err: %v", resp.StatusCode, err)
	}
	resp.Body.Close()

	// Unknown kind
	resp, err = http.Get(ts.URL + "/api/v1/cases/" + caseID + "/reports/" + runID + "/unknown")
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown report kind, got: %v", resp.StatusCode)
	}
	resp.Body.Close()

	// Missing report run
	resp, err = http.Get(ts.URL + "/api/v1/cases/" + caseID + "/reports/missing-run/markdown")
	if err != nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for missing run, got: %v", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestGateway_RecurringCasesOrchestration(t *testing.T) {
	_, ts, backends, _, _ := setupTestGateway(t)
	defer ts.Close()
	defer backends.Close()

	// 1. Create Case 1 and analyze it
	cBody1, _ := json.Marshal(contracts.CreateCaseRequest{Title: "Case 1", Description: "First"})
	resp1, _ := http.Post(ts.URL+"/api/v1/cases", "application/json", bytes.NewReader(cBody1))
	var meta1 contracts.CaseMeta
	_ = json.NewDecoder(resp1.Body).Decode(&meta1)
	resp1.Body.Close()

	aBody1, _ := json.Marshal(contracts.AnalyzeCaseRequest{CaseID: meta1.ID})
	respA1, _ := http.Post(ts.URL+"/api/v1/cases/"+meta1.ID+"/analyze", "application/json", bytes.NewReader(aBody1))
	respA1.Body.Close()

	// 2. Create Case 2 and analyze it -> RecurringCaseIDs should find Case 1!
	cBody2, _ := json.Marshal(contracts.CreateCaseRequest{Title: "Case 2", Description: "Second"})
	resp2, _ := http.Post(ts.URL+"/api/v1/cases", "application/json", bytes.NewReader(cBody2))
	var meta2 contracts.CaseMeta
	_ = json.NewDecoder(resp2.Body).Decode(&meta2)
	resp2.Body.Close()

	aBody2, _ := json.Marshal(contracts.AnalyzeCaseRequest{CaseID: meta2.ID})
	respA2, _ := http.Post(ts.URL+"/api/v1/cases/"+meta2.ID+"/analyze", "application/json", bytes.NewReader(aBody2))
	var analysis2 contracts.AnalyzeCaseResponse
	_ = json.NewDecoder(respA2.Body).Decode(&analysis2)
	respA2.Body.Close()

	if len(analysis2.Hypotheses) == 0 {
		t.Fatal("expected hypotheses in analysis 2")
	}
	if len(analysis2.Hypotheses[0].RecurringCaseIDs) == 0 {
		t.Errorf("expected RecurringCaseIDs to contain %s, got %+v", meta1.ID, analysis2.Hypotheses[0].RecurringCaseIDs)
	} else if analysis2.Hypotheses[0].RecurringCaseIDs[0] != meta1.ID {
		t.Errorf("expected recurring case %s, got %s", meta1.ID, analysis2.Hypotheses[0].RecurringCaseIDs[0])
	}
}

func TestGateway_ErrorAndEdgeCases(t *testing.T) {
	_, ts, backends, _, _ := setupTestGateway(t)
	defer ts.Close()
	defer backends.Close()

	// 1. Create case with empty title -> 400
	resp, _ := http.Post(ts.URL+"/api/v1/cases", "application/json", strings.NewReader(`{"title":""}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for empty title, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 2. Create case invalid JSON -> 400
	resp, _ = http.Post(ts.URL+"/api/v1/cases", "application/json", strings.NewReader(`invalid json`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid json, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 3. Delete non-existent case -> 404
	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/cases/non-existent", nil)
	resp, _ = http.DefaultClient.Do(delReq)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for deleting non-existent case, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 4. Ingest local on non-existent case -> 404
	resp, _ = http.Post(ts.URL+"/api/v1/cases/non-existent/ingest-local", "application/json", strings.NewReader(`{"file_paths":["/a.log"]}`))
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent case ingest, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 5. Ingest local empty file paths -> 400
	// Create case first
	cBody, _ := json.Marshal(contracts.CreateCaseRequest{Title: "Case Valid", Description: ""})
	resp, _ = http.Post(ts.URL+"/api/v1/cases", "application/json", bytes.NewReader(cBody))
	var meta contracts.CaseMeta
	_ = json.NewDecoder(resp.Body).Decode(&meta)
	resp.Body.Close()

	resp, _ = http.Post(ts.URL+"/api/v1/cases/"+meta.ID+"/ingest-local", "application/json", strings.NewReader(`{"file_paths":[]}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for empty file_paths, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 6. Upload on non-existent case -> 404
	resp, _ = http.Post(ts.URL+"/api/v1/cases/non-existent/upload", "multipart/form-data; boundary=xyz", strings.NewReader("--xyz--"))
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for upload to non-existent case, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 7. Upload without files -> 400
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	w.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/cases/"+meta.ID+"/upload", &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for upload with no files, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestGateway_AdditionalBranchesAndCoverage(t *testing.T) {
	// Test nil dependencies
	srvNil := NewServer(Config{})
	tsNil := httptest.NewServer(srvNil)
	defer tsNil.Close()

	// 1. Storage endpoints with nil storage
	resp, _ := http.Get(tsNil.URL + "/api/v1/storage/status")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 on nil storage, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp, _ = http.Post(tsNil.URL+"/api/v1/storage/purge-raw", "application/json", nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 on nil storage purge, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 2. Files available with empty IngestURL
	resp, _ = http.Get(tsNil.URL + "/api/v1/files/available")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 on empty IngestURL, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 3. Tilt targets with empty TiltCollectorURL
	resp, _ = http.Get(tsNil.URL + "/api/v1/tilt/targets")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 on empty TiltCollectorURL, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 4. List cases with nil meta
	resp, _ = http.Get(tsNil.URL + "/api/v1/cases")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 on nil meta list cases, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 5. Get analysis with nil meta
	resp, _ = http.Get(tsNil.URL + "/api/v1/cases/case-123/analysis")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 on nil meta get analysis, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 6. Context logs with nil explorer
	resp, _ = http.Get(tsNil.URL + "/api/v1/cases/case-123/logs/context")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 on nil explorer get context logs, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 7. Error backends
	errBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("backend internal error"))
	}))
	defer errBackend.Close()

	tmpDir := t.TempDir()
	metaStore, _ := metadb.NewStore(filepath.Join(tmpDir, "meta.json"))
	_ = metaStore.SaveCase(contracts.CaseMeta{
		ID:        "case-err",
		Title:     "Case Error",
		Status:    contracts.CaseStatusReady,
		CreatedAt: time.Now().UTC(),
	})

	srvErr := NewServer(Config{
		IngestURL:        errBackend.URL,
		RCAURL:           errBackend.URL,
		TiltCollectorURL: errBackend.URL,
		EvidenceURL:      errBackend.URL,
		MetaStore:        metaStore,
		StagingDir:       tmpDir,
		ReportsDir:       tmpDir,
	})
	tsErr := httptest.NewServer(srvErr)
	defer tsErr.Close()

	// Ingest error
	iBody, _ := json.Marshal(contracts.IngestLocalRequest{FilePaths: []string{"/test"}})
	resp, _ = http.Post(tsErr.URL+"/api/v1/cases/case-err/ingest-local", "application/json", bytes.NewReader(iBody))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 on ingest backend error, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Analyze error from RCA
	aBody, _ := json.Marshal(contracts.AnalyzeCaseRequest{CaseID: "case-err"})
	resp, _ = http.Post(tsErr.URL+"/api/v1/cases/case-err/analyze", "application/json", bytes.NewReader(aBody))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 on rca backend error, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Tilt start error
	tStartBody, _ := json.Marshal(contracts.TiltStartCaptureRequest{CaseID: "case-err"})
	resp, _ = http.Post(tsErr.URL+"/api/v1/cases/case-err/tilt/start", "application/json", bytes.NewReader(tStartBody))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 on tilt start backend error, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Tilt stop error
	resp, _ = http.Post(tsErr.URL+"/api/v1/cases/case-err/tilt/stop", "application/json", nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 on tilt stop backend error, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Tilt status error
	resp, _ = http.Get(tsErr.URL + "/api/v1/cases/case-err/tilt/status")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 on tilt status backend error, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Chain error
	resp, _ = http.Get(tsErr.URL + "/api/v1/cases/case-err/chain?key=test")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 on chain backend error, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Browse error
	resp, _ = http.Get(tsErr.URL + "/api/v1/files/available")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 on files available error, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Tilt targets error
	resp, _ = http.Get(tsErr.URL + "/api/v1/tilt/targets")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 on tilt targets error, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Reports "latest" resolution and hasRep paths
	repCaseID := "case-rep-latest"
	repRunID := "run-99"
	_ = metaStore.SaveCase(contracts.CaseMeta{
		ID:          repCaseID,
		Title:       "Rep Case",
		Status:      contracts.CaseStatusAnalyzed,
		LatestRunID: repRunID,
		CreatedAt:   time.Now().UTC(),
	})
	fMd := filepath.Join(tmpDir, "custom.md")
	fHtml := filepath.Join(tmpDir, "custom.html")
	fGem := filepath.Join(tmpDir, "custom_gemini.md")
	fZip := filepath.Join(tmpDir, "custom.zip")
	_ = os.WriteFile(fMd, []byte("custom md"), 0644)
	_ = os.WriteFile(fHtml, []byte("custom html"), 0644)
	_ = os.WriteFile(fGem, []byte("custom gemini"), 0644)
	_ = os.WriteFile(fZip, []byte("PK\x03\x04customzip"), 0644)

	_ = metaStore.SaveReport(contracts.BuildEvidenceResponse{
		CaseID:       repCaseID,
		RunID:        repRunID,
		MarkdownPath: fMd,
		HTMLPath:     fHtml,
		GeminiPath:   fGem,
		ZipPath:      fZip,
	})

	for _, k := range []string{"markdown", "html", "gemini", "zip"} {
		resp, err := http.Get(tsErr.URL + "/api/v1/cases/" + repCaseID + "/reports/latest/" + k)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 for latest %s report, got status: %v", k, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// 8. Test ServeHTTP directly and more branches
	rec := httptest.NewRecorder()
	reqHealth := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	srvErr.ServeHTTP(rec, reqHealth)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 from ServeHTTP, got %d", rec.Code)
	}

	// Ingest upload on err backend
	var bUp bytes.Buffer
	wUp := multipart.NewWriter(&bUp)
	pUp, _ := wUp.CreateFormFile("files", "dummy.json")
	pUp.Write([]byte(`{"hello":"world"}`))
	wUp.Close()
	reqUp, _ := http.NewRequest(http.MethodPost, tsErr.URL+"/api/v1/cases/case-err/upload", &bUp)
	reqUp.Header.Set("Content-Type", wUp.FormDataContentType())
	respUp, err := http.DefaultClient.Do(reqUp)
	if err == nil {
		if respUp.StatusCode != http.StatusInternalServerError {
			t.Errorf("expected 500 on upload backend error, got %d", respUp.StatusCode)
		}
		respUp.Body.Close()
	}

	// Ingest upload with non-file field
	var bPart bytes.Buffer
	wPart := multipart.NewWriter(&bPart)
	_ = wPart.WriteField("text", "value")
	pFile, _ := wPart.CreateFormFile("files", "data.json")
	pFile.Write([]byte(`{"ok":true}`))
	wPart.Close()
	reqPart, _ := http.NewRequest(http.MethodPost, tsErr.URL+"/api/v1/cases/case-err/upload", &bPart)
	reqPart.Header.Set("Content-Type", wPart.FormDataContentType())
	respPart, err := http.DefaultClient.Do(reqPart)
	if err == nil {
		respPart.Body.Close()
	}

	// Job status on error backend
	respJob, _ := http.Get(tsErr.URL + "/api/v1/jobs/job-err-1")
	if respJob.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 on job backend error, got %d", respJob.StatusCode)
	}
	respJob.Body.Close()

	// Reports fallback to gemini_prompt.md and zip glob
	promptDir := filepath.Join(tmpDir, "case-prompt", "run-prompt")
	_ = os.MkdirAll(promptDir, 0755)
	_ = os.WriteFile(filepath.Join(promptDir, "gemini_prompt.md"), []byte("prompt fallback"), 0644)
	_ = os.WriteFile(filepath.Join(promptDir, "anything.zip"), []byte("PK\x03\x04zipfallback"), 0644)

	respP, err := http.Get(tsErr.URL + "/api/v1/cases/case-prompt/reports/run-prompt/gemini")
	if err != nil || respP.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for gemini prompt fallback, got: %v", respP.StatusCode)
	}
	respP.Body.Close()

	respZ, err := http.Get(tsErr.URL + "/api/v1/cases/case-prompt/reports/run-prompt/zip")
	if err != nil || respZ.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for zip glob fallback, got: %v", respZ.StatusCode)
	}
	respZ.Body.Close()

	// Delete case with CHHTTPURL set
	chDelMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer chDelMock.Close()

	srvDel := NewServer(Config{
		CHHTTPURL:  chDelMock.URL,
		MetaStore:  metaStore,
		StagingDir: tmpDir,
		ReportsDir: tmpDir,
	})
	tsDel := httptest.NewServer(srvDel)
	defer tsDel.Close()

	delReq, _ := http.NewRequest(http.MethodDelete, tsDel.URL+"/api/v1/cases/case-err", nil)
	respDel, _ := http.DefaultClient.Do(delReq)
	if respDel.StatusCode != http.StatusOK {
		t.Errorf("expected 200 on delete with CH, got %d", respDel.StatusCode)
	}
	respDel.Body.Close()

	// 9. Additional edge checks on handlers directly with mock recorder
	recDirect := httptest.NewRecorder()
	srvNil.handleDeleteCase(recDirect, httptest.NewRequest(http.MethodDelete, "/api/v1/cases/", nil))
	srvNil.handleIngestLocal(recDirect, httptest.NewRequest(http.MethodPost, "/api/v1/cases//ingest-local", nil))
	srvNil.handleUpload(recDirect, httptest.NewRequest(http.MethodPost, "/api/v1/cases//upload", nil))
	srvNil.handleJobStatus(recDirect, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/", nil))
	srvNil.handleAnalyzeCase(recDirect, httptest.NewRequest(http.MethodPost, "/api/v1/cases//analyze", nil))
	srvNil.handleGetAnalysis(recDirect, httptest.NewRequest(http.MethodGet, "/api/v1/cases//analysis", nil))
	srvNil.handleGetChain(recDirect, httptest.NewRequest(http.MethodGet, "/api/v1/cases//chain", nil))
	srvNil.handleGetContextLogs(recDirect, httptest.NewRequest(http.MethodGet, "/api/v1/cases//logs/context", nil))
	srvNil.handleGetReportFile(recDirect, httptest.NewRequest(http.MethodGet, "/api/v1/cases//reports//", nil))
	srvNil.handleTiltStart(recDirect, httptest.NewRequest(http.MethodPost, "/api/v1/cases//tilt/start", nil))
	srvNil.handleTiltStatus(recDirect, httptest.NewRequest(http.MethodGet, "/api/v1/cases//tilt/status", nil))
}
