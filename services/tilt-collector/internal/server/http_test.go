package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/tilt-collector/internal/collector"
	"github.com/local/logs-analyzer/services/tilt-collector/internal/discovery"
)

func TestServer_Health(t *testing.T) {
	runtime := discovery.NewRuntimeClient("http://127.0.0.1:9999", "")
	mgr := collector.NewManager(runtime, "http://127.0.0.1:9999")
	srv := NewServer(runtime, mgr)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var res map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if res["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", res["status"])
	}
}

func TestServer_TiltEndToEndIntegration(t *testing.T) {
	// 1. Mock Docker Engine with 2 Tilt containers + 1 rca system container
	dockerContainers := []map[string]interface{}{
		{
			"Id":     "id-chat-com",
			"Names":  []string{"/chat-com-7b649fd7b4-z5w2k"},
			"State":  "running",
			"Status": "Up 2 hours",
			"Labels": map[string]string{
				"dev.tilt.resource":         "chat-com",
				"io.kubernetes.pod.name":    "chat-com-7b649fd7b4-z5w2k",
				"io.kubernetes.pod.namespace": "default",
			},
		},
		{
			"Id":     "id-chat-session",
			"Names":  []string{"/chat-session-7f9c"},
			"State":  "running",
			"Status": "Up 2 hours",
			"Labels": map[string]string{
				"dev.tilt.resource":         "chat-session",
				"io.kubernetes.pod.name":    "chat-session-7f9c",
				"io.kubernetes.pod.namespace": "chat",
			},
		},
		{
			"Id":     "id-rca-clickhouse",
			"Names":  []string{"/rca-clickhouse"},
			"State":  "running",
			"Status": "Up 2 hours",
			"Labels": map[string]string{
				"dev.tilt.resource": "clickhouse",
			},
		},
	}

	tsDocker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/containers/json") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(dockerContainers)
			return
		}

		if strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/logs") {
			w.Header().Set("Content-Type", "text/plain")
			flusher, canFlush := w.(http.Flusher)
			if strings.Contains(r.URL.Path, "id-chat-com") {
				_, _ = w.Write([]byte("2026-09-30T08:20:00.100000000Z {\"level\":\"info\",\"msg\":\"chat-com initialized\"}\n"))
				if canFlush {
					flusher.Flush()
				}
				// Keep connection open until context cancelled
				<-r.Context().Done()
				return
			}
			if strings.Contains(r.URL.Path, "id-chat-session") {
				_, _ = w.Write([]byte("2026-09-30T08:20:00.200000000Z {\"level\":\"info\",\"msg\":\"chat-session ready\"}\n"))
				if canFlush {
					flusher.Flush()
				}
				<-r.Context().Done()
				return
			}
			http.NotFound(w, r)
			return
		}

		http.NotFound(w, r)
	}))
	defer tsDocker.Close()

	// 2. Mock ingest-service receiving POST /internal/ingest/batch
	var ingestBatchesMu sync.Mutex
	var receivedBatches []contracts.IngestBatchRequest
	var totalIngestedRows uint64

	tsIngest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/ingest/batch" && r.Method == http.MethodPost {
			var batchReq contracts.IngestBatchRequest
			if err := json.NewDecoder(r.Body).Decode(&batchReq); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			ingestBatchesMu.Lock()
			receivedBatches = append(receivedBatches, batchReq)
			atomic.AddUint64(&totalIngestedRows, uint64(len(batchReq.Records)))
			ingestBatchesMu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(contracts.IngestBatchResponse{
				RowsIngested: uint64(len(batchReq.Records)),
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer tsIngest.Close()

	runtime := discovery.NewRuntimeClient(tsDocker.URL, "")
	mgr := collector.NewManager(runtime, tsIngest.URL)
	srv := NewServer(runtime, mgr)

	// 3. GET /internal/tilt/targets -> check only 2 services returned (system rca-clickhouse filtered)
	reqTargets := httptest.NewRequest(http.MethodGet, "/internal/tilt/targets", nil)
	recTargets := httptest.NewRecorder()
	srv.ServeHTTP(recTargets, reqTargets)

	if recTargets.Code != http.StatusOK {
		t.Fatalf("expected GET /internal/tilt/targets status 200, got %d: %s", recTargets.Code, recTargets.Body.String())
	}

	var targets []contracts.TiltTarget
	if err := json.Unmarshal(recTargets.Body.Bytes(), &targets); err != nil {
		t.Fatalf("failed to decode targets: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d: %+v", len(targets), targets)
	}

	targetNames := map[string]bool{
		targets[0].ServiceName: true,
		targets[1].ServiceName: true,
	}
	if !targetNames["chat-com"] || !targetNames["chat-session"] {
		t.Errorf("expected targets to contain chat-com and chat-session, got %+v", targets)
	}

	// 4. POST /internal/tilt/start with CaseID: "case-tilt-1"
	startBody, _ := json.Marshal(contracts.TiltStartCaptureRequest{
		CaseID:       "case-tilt-1",
		SinceSeconds: 300,
	})
	reqStart := httptest.NewRequest(http.MethodPost, "/internal/tilt/start", bytes.NewReader(startBody))
	reqStart.Header.Set("Content-Type", "application/json")
	recStart := httptest.NewRecorder()
	srv.ServeHTTP(recStart, reqStart)

	if recStart.Code != http.StatusOK {
		t.Fatalf("expected POST /internal/tilt/start status 200, got %d: %s", recStart.Code, recStart.Body.String())
	}

	var startResp contracts.TiltStatusResponse
	if err := json.Unmarshal(recStart.Body.Bytes(), &startResp); err != nil {
		t.Fatalf("failed to decode start response: %v", err)
	}
	if !startResp.Active {
		t.Errorf("expected Active=true in start response")
	}
	if startResp.CaseID != "case-tilt-1" {
		t.Errorf("expected CaseID 'case-tilt-1', got %q", startResp.CaseID)
	}
	if startResp.TargetsWatched != 2 {
		t.Errorf("expected TargetsWatched=2, got %d", startResp.TargetsWatched)
	}

	// Wait up to 2 seconds for logs to be captured and flushed
	deadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(deadline) {
		st := mgr.GetStatus()
		if st.LinesCaptured >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// POST /internal/tilt/stop -> flush remainder and mark active=false
	reqStop := httptest.NewRequest(http.MethodPost, "/internal/tilt/stop", nil)
	recStop := httptest.NewRecorder()
	srv.ServeHTTP(recStop, reqStop)

	if recStop.Code != http.StatusOK {
		t.Fatalf("expected POST /internal/tilt/stop status 200, got %d: %s", recStop.Code, recStop.Body.String())
	}

	var stopResp contracts.TiltStatusResponse
	if err := json.Unmarshal(recStop.Body.Bytes(), &stopResp); err != nil {
		t.Fatalf("failed to decode stop response: %v", err)
	}
	if stopResp.Active {
		t.Errorf("expected Active=false after stop, got true")
	}

	// 5. GET /internal/tilt/status
	reqStatus := httptest.NewRequest(http.MethodGet, "/internal/tilt/status", nil)
	recStatus := httptest.NewRecorder()
	srv.ServeHTTP(recStatus, reqStatus)

	if recStatus.Code != http.StatusOK {
		t.Fatalf("expected GET /internal/tilt/status status 200, got %d: %s", recStatus.Code, recStatus.Body.String())
	}

	var finalStatus contracts.TiltStatusResponse
	if err := json.Unmarshal(recStatus.Body.Bytes(), &finalStatus); err != nil {
		t.Fatalf("failed to decode final status: %v", err)
	}
	if finalStatus.LinesCaptured < 2 {
		t.Errorf("expected LinesCaptured >= 2, got %d", finalStatus.LinesCaptured)
	}
	if finalStatus.BatchesFlushed < 1 {
		t.Errorf("expected BatchesFlushed >= 1, got %d", finalStatus.BatchesFlushed)
	}
	if finalStatus.Active {
		t.Errorf("expected Active=false in final status")
	}

	// Verify that batches reached the mock ingest-service with correct case_id and service_name
	ingestBatchesMu.Lock()
	defer ingestBatchesMu.Unlock()
	if len(receivedBatches) == 0 {
		t.Fatalf("expected at least 1 batch received by ingest-service")
	}

	var foundChatCom, foundChatSession bool
	for _, b := range receivedBatches {
		if b.CaseID != "case-tilt-1" {
			t.Errorf("expected batch CaseID 'case-tilt-1', got %q", b.CaseID)
		}
		for _, rec := range b.Records {
			if rec.Fields["service_name"] == "chat-com" {
				foundChatCom = true
			}
			if rec.Fields["service_name"] == "chat-session" {
				foundChatSession = true
			}
		}
	}
	if !foundChatCom {
		t.Errorf("expected records with service_name 'chat-com' in ingest batch")
	}
	if !foundChatSession {
		t.Errorf("expected records with service_name 'chat-session' in ingest batch")
	}
}

func TestServer_StartValidationAndErrors(t *testing.T) {
	runtime := discovery.NewRuntimeClient("http://127.0.0.1:9999", "")
	mgr := collector.NewManager(runtime, "http://127.0.0.1:9999")
	srv := NewServer(runtime, mgr)

	// 1. Invalid JSON body
	reqBadJSON := httptest.NewRequest(http.MethodPost, "/internal/tilt/start", strings.NewReader("{invalid"))
	recBadJSON := httptest.NewRecorder()
	srv.ServeHTTP(recBadJSON, reqBadJSON)
	if recBadJSON.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad JSON, got %d", recBadJSON.Code)
	}

	// 2. Empty CaseID
	bodyEmptyCase, _ := json.Marshal(contracts.TiltStartCaptureRequest{CaseID: ""})
	reqEmptyCase := httptest.NewRequest(http.MethodPost, "/internal/tilt/start", bytes.NewReader(bodyEmptyCase))
	recEmptyCase := httptest.NewRecorder()
	srv.ServeHTTP(recEmptyCase, reqEmptyCase)
	if recEmptyCase.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty case_id, got %d", recEmptyCase.Code)
	}
}

func TestServer_DiscoverTargetsError(t *testing.T) {
	tsErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer tsErr.Close()

	runtime := discovery.NewRuntimeClient(tsErr.URL, "")
	mgr := collector.NewManager(runtime, "http://127.0.0.1:9999")
	srv := NewServer(runtime, mgr)

	req := httptest.NewRequest(http.MethodGet, "/internal/tilt/targets", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when discovery fails, got %d", rec.Code)
	}
}

func TestCollector_500RowsBatchThreshold(t *testing.T) {
	// Create mock docker engine generating 520 lines
	tsDocker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/containers/json") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{
					"Id":     "id-burst",
					"Names":  []string{"/burst-service"},
					"State":  "running",
					"Labels": map[string]string{"dev.tilt.resource": "burst-service"},
				},
			})
			return
		}
		if strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/logs") {
			w.Header().Set("Content-Type", "text/plain")
			for i := 1; i <= 520; i++ {
				_, _ = fmt.Fprintf(w, "2026-09-30T08:20:00.000Z burst line %d\n", i)
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer tsDocker.Close()

	var batchesCount uint64
	var totalRecords uint64
	tsIngest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req contracts.IngestBatchRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		atomic.AddUint64(&batchesCount, 1)
		atomic.AddUint64(&totalRecords, uint64(len(req.Records)))
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(contracts.IngestBatchResponse{RowsIngested: uint64(len(req.Records))})
	}))
	defer tsIngest.Close()

	runtime := discovery.NewRuntimeClient(tsDocker.URL, "")
	mgr := collector.NewManager(runtime, tsIngest.URL)

	ctx := context.Background()
	_, err := mgr.StartCapture(ctx, contracts.TiltStartCaptureRequest{CaseID: "burst-case"})
	if err != nil {
		t.Fatalf("StartCapture failed: %v", err)
	}

	// Wait up to 1.5 seconds for burst flush (should flush 500 rows immediately without waiting full 1500ms ticker)
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if atomic.LoadUint64(&batchesCount) >= 1 && atomic.LoadUint64(&totalRecords) >= 500 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	stopStatus := mgr.StopCapture()
	if stopStatus.LinesCaptured != 520 {
		t.Errorf("expected LinesCaptured == 520, got %d", stopStatus.LinesCaptured)
	}
	if stopStatus.BatchesFlushed < 2 {
		// 500 lines in batch 1 + 20 lines remainder flushed on stop -> at least 2 batches
		t.Errorf("expected at least 2 batches (500 + 20), got %d", stopStatus.BatchesFlushed)
	}
}

func TestCollector_TargetIDsFilteringAndRestart(t *testing.T) {
	tsDocker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/containers/json") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{
					"Id":     "id-service-a",
					"Names":  []string{"/svc-a"},
					"Labels": map[string]string{"dev.tilt.resource": "svc-a"},
				},
				{
					"Id":     "id-service-b",
					"Names":  []string{"/svc-b"},
					"Labels": map[string]string{"dev.tilt.resource": "svc-b"},
				},
			})
			return
		}
		if strings.Contains(r.URL.Path, "id-service-a") && strings.HasSuffix(r.URL.Path, "/logs") {
			_, _ = w.Write([]byte("2026-09-30T08:20:00.000Z message from A\n"))
			return
		}
		if strings.Contains(r.URL.Path, "id-service-b") && strings.HasSuffix(r.URL.Path, "/logs") {
			_, _ = w.Write([]byte("2026-09-30T08:20:00.000Z message from B\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer tsDocker.Close()

	tsIngest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(contracts.IngestBatchResponse{RowsIngested: 1})
	}))
	defer tsIngest.Close()

	runtime := discovery.NewRuntimeClient(tsDocker.URL, "")
	mgr := collector.NewManager(runtime, tsIngest.URL)

	// Filter only id-service-a
	st1, err := mgr.StartCapture(context.Background(), contracts.TiltStartCaptureRequest{
		CaseID:    "filter-case",
		TargetIDs: []string{"id-service-a"},
	})
	if err != nil {
		t.Fatalf("StartCapture failed: %v", err)
	}
	if st1.TargetsWatched != 1 || len(st1.Targets) != 1 || st1.Targets[0].ServiceName != "svc-a" {
		t.Errorf("expected 1 watched target svc-a, got %+v", st1)
	}

	// Restart session with CaseID "new-case" while st1 is active (tests clean restart)
	st2, err := mgr.StartCapture(context.Background(), contracts.TiltStartCaptureRequest{
		CaseID: "new-case",
	})
	if err != nil {
		t.Fatalf("StartCapture restart failed: %v", err)
	}
	if st2.CaseID != "new-case" || st2.TargetsWatched != 2 {
		t.Errorf("expected 2 targets watched after restart, got %+v", st2)
	}

	mgr.StopCapture()
	// Stop again (idempotent stop)
	stStopAgain := mgr.StopCapture()
	if stStopAgain.Active {
		t.Errorf("expected Active=false on repeated StopCapture")
	}
}

func TestCollector_IngestServiceError(t *testing.T) {
	tsDocker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/containers/json") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"Id": "id-err", "Names": []string{"/err-svc"}, "Labels": map[string]string{"dev.tilt.resource": "err-svc"}},
			})
			return
		}
		if strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/logs") {
			_, _ = w.Write([]byte("2026-09-30T08:20:00.000Z err log line\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer tsDocker.Close()

	// Ingest returns 500
	tsIngestErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal ingest failure", http.StatusInternalServerError)
	}))
	defer tsIngestErr.Close()

	runtime := discovery.NewRuntimeClient(tsDocker.URL, "")
	mgr := collector.NewManager(runtime, tsIngestErr.URL)

	_, err := mgr.StartCapture(context.Background(), contracts.TiltStartCaptureRequest{CaseID: "case-err"})
	if err != nil {
		t.Fatalf("StartCapture unexpected error: %v", err)
	}

	time.Sleep(300 * time.Millisecond)
	st := mgr.StopCapture()
	if st.LastError == "" {
		t.Errorf("expected LastError to be populated when ingest fails, got empty")
	}
}
