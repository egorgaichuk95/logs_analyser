package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

func TestHealthHandler(t *testing.T) {
	srv := NewServer("http://localhost:8123")
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status ok, got %v", body["status"])
	}
}

func TestAnalyze_ValidationErrors(t *testing.T) {
	srv := NewServer("http://localhost:8123")

	// 1. Invalid JSON
	reqBadJSON := httptest.NewRequest(http.MethodPost, "/internal/rca/analyze", strings.NewReader("invalid-json"))
	recBadJSON := httptest.NewRecorder()
	srv.ServeHTTP(recBadJSON, reqBadJSON)
	if recBadJSON.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad json, got %d", recBadJSON.Code)
	}

	// 2. Empty CaseID
	reqEmptyCase := httptest.NewRequest(http.MethodPost, "/internal/rca/analyze", strings.NewReader(`{"case_id":""}`))
	recEmptyCase := httptest.NewRecorder()
	srv.ServeHTTP(recEmptyCase, reqEmptyCase)
	if recEmptyCase.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty case_id, got %d", recEmptyCase.Code)
	}
}

func TestAnalyze_WithRawEvents(t *testing.T) {
	// Mock ClickHouse HTTP server
	mockCH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes := make([]byte, 2048)
		n, _ := r.Body.Read(bodyBytes)
		query := string(bodyBytes[:n])

		switch {
		case strings.Contains(query, "SELECT count() AS cnt FROM logs_rca.events_raw"):
			// Return cnt = 2
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"cnt":2}` + "\n"))

		case strings.Contains(query, "FROM logs_rca.events_raw"):
			// Return raw events
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			ev1 := contracts.NormalizedEvent{
				CaseID:         "case-raw-1",
				EventID:        "00000000-0000-0000-0000-000000000001",
				Ts:             time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC),
				Service:        "chat-com",
				Pod:            "chat-com-pod-1",
				Level:          contracts.LevelError,
				TemplateID:     2001,
				ErrorClass:     contracts.ErrClassOwnDefect,
				SourcePlace:    "main.py:42",
				TraceID:        "tr-100",
				ClockSkewMs:    150,
			}
			ev2 := contracts.NormalizedEvent{
				CaseID:         "case-raw-1",
				EventID:        "00000000-0000-0000-0000-000000000002",
				Ts:             time.Date(2026, 10, 28, 12, 0, 1, 0, time.UTC),
				Service:        "chat-session",
				Pod:            "chat-session-pod-1",
				Level:          contracts.LevelError,
				TemplateID:     2002,
				ErrorClass:     contracts.ErrClassDeadline,
				SourcePlace:    "client.go:88",
				TraceID:        "tr-100",
				PeerService:    "chat-com",
				ClockSkewMs:    0,
			}
			b1, _ := json.Marshal(ev1)
			b2, _ := json.Marshal(ev2)
			_, _ = w.Write(append(b1, '\n'))
			_, _ = w.Write(append(b2, '\n'))

		case strings.Contains(query, "FROM logs_rca.identity_edges"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// No extra identity edges needed for single trace
			_, _ = w.Write([]byte(""))

		case strings.Contains(query, "FROM logs_rca.events_agg_1m") && strings.Contains(query, "WHERE case_id = 'case-raw-1'"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// Return aggregated buckets
			row1 := `{"bucket_1m":"2026-10-28 12:00:00","service":"chat-com","pod":"chat-com-pod-1","service_version":"v1","level":"ERROR","error_class":"OWN_DEFECT","template_id":2001,"source_place":"main.py:42","peer_service":"","events_count":10,"max_latency_ns":5000000,"max_skew_ms":150}`
			row2 := `{"bucket_1m":"2026-10-28 12:00:00","service":"chat-session","pod":"chat-session-pod-1","service_version":"v1","level":"ERROR","error_class":"DEADLINE","template_id":2002,"source_place":"client.go:88","peer_service":"chat-com","events_count":8,"max_latency_ns":12000000,"max_skew_ms":0}`
			_, _ = w.Write([]byte(row1 + "\n" + row2 + "\n"))

		case strings.Contains(query, "FROM logs_rca.templates_dict"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			t1 := `{"case_id":"case-raw-1","service":"chat-com","template_id":2001,"template_text":"panic: runtime error","level":"ERROR","error_class":"OWN_DEFECT","source_place":"main.py:42"}`
			t2 := `{"case_id":"case-raw-1","service":"chat-session","template_id":2002,"template_text":"context deadline exceeded","level":"ERROR","error_class":"DEADLINE","source_place":"client.go:88"}`
			_, _ = w.Write([]byte(t1 + "\n" + t2 + "\n"))

		case strings.Contains(query, "FROM logs_rca.service_edges"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			e1 := `{"case_id":"case-raw-1","caller_service":"chat-session","callee_service":"chat-com","evidence_source":"trace_propagation","call_count":100,"error_count":10}`
			_, _ = w.Write([]byte(e1 + "\n"))

		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer mockCH.Close()

	srv := NewServer(mockCH.URL)

	reqPayload := contracts.AnalyzeCaseRequest{
		CaseID: "case-raw-1",
	}
	body, _ := json.Marshal(reqPayload)
	req := httptest.NewRequest(http.MethodPost, "/internal/rca/analyze", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp contracts.AnalyzeCaseResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode analyze response: %v", err)
	}

	if resp.CaseID != "case-raw-1" {
		t.Errorf("expected CaseID case-raw-1, got %s", resp.CaseID)
	}
	if len(resp.Hypotheses) == 0 {
		t.Fatalf("expected hypotheses to be non-empty")
	}

	// First hypothesis must be RootCause
	if resp.Hypotheses[0].Role != contracts.RoleRootCause {
		t.Errorf("expected first hypothesis Role ROOT_CAUSE, got %s", resp.Hypotheses[0].Role)
	}

	// Test GET /internal/rca/chain/case-raw-1?key=tr-100
	reqChain := httptest.NewRequest(http.MethodGet, "/internal/rca/chain/case-raw-1?key=tr-100", nil)
	recChain := httptest.NewRecorder()
	srv.ServeHTTP(recChain, reqChain)

	if recChain.Code != http.StatusOK {
		t.Fatalf("expected chain status 200, got %d", recChain.Code)
	}

	var chainEvents []contracts.NormalizedEvent
	if err := json.NewDecoder(recChain.Body).Decode(&chainEvents); err != nil {
		t.Fatalf("decode chain events: %v", err)
	}
	if len(chainEvents) != 2 {
		t.Errorf("expected 2 chain events, got %d", len(chainEvents))
	}
}

func TestAnalyze_AfterPurgeRaw(t *testing.T) {
	// Mock ClickHouse where events_raw is purged (count == 0)
	mockCH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes := make([]byte, 2048)
		n, _ := r.Body.Read(bodyBytes)
		query := string(bodyBytes[:n])

		switch {
		case strings.Contains(query, "SELECT count() AS cnt FROM logs_rca.events_raw"):
			// Return cnt = 0 (PURGED)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"cnt":0}` + "\n"))

		case strings.Contains(query, "FROM logs_rca.events_agg_1m") && strings.Contains(query, "WHERE case_id = 'case-purged-1'"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			row1 := `{"bucket_1m":"2026-10-28 12:00:00","service":"chat-com","pod":"chat-com-pod-1","service_version":"v1","level":"ERROR","error_class":"OWN_DEFECT","template_id":3001,"source_place":"main.py:42","peer_service":"","events_count":15,"max_latency_ns":5000000,"max_skew_ms":0}`
			_, _ = w.Write([]byte(row1 + "\n"))

		case strings.Contains(query, "FROM logs_rca.events_agg_1m") && strings.Contains(query, "WHERE case_id != 'case-purged-1'"):
			// Historical baseline query
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			histRow := `{"template_id":3001,"total_events":60,"total_minutes":60}`
			_, _ = w.Write([]byte(histRow + "\n"))

		case strings.Contains(query, "FROM logs_rca.templates_dict"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			t1 := `{"case_id":"case-purged-1","service":"chat-com","template_id":3001,"template_text":"runtime error: memory out","level":"ERROR","error_class":"OWN_DEFECT","source_place":"main.py:42"}`
			_, _ = w.Write([]byte(t1 + "\n"))

		case strings.Contains(query, "FROM logs_rca.service_edges"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(""))

		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer mockCH.Close()

	srv := NewServer(mockCH.URL)

	reqPayload := contracts.AnalyzeCaseRequest{
		CaseID:                "case-purged-1",
		UseHistoricalBaseline: true,
	}
	body, _ := json.Marshal(reqPayload)
	req := httptest.NewRequest(http.MethodPost, "/internal/rca/analyze", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK after purge raw, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp contracts.AnalyzeCaseResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode analyze response: %v", err)
	}

	if len(resp.Hypotheses) == 0 {
		t.Fatalf("expected hypotheses even after purge-raw, got 0")
	}
	if resp.Hypotheses[0].TemplateID != 3001 {
		t.Errorf("expected TemplateID 3001, got %d", resp.Hypotheses[0].TemplateID)
	}

	// GET chain after purge-raw returns empty list without error
	reqChain := httptest.NewRequest(http.MethodGet, "/internal/rca/chain/case-purged-1?key=tr-none", nil)
	recChain := httptest.NewRecorder()
	srv.ServeHTTP(recChain, reqChain)

	if recChain.Code != http.StatusOK {
		t.Fatalf("expected chain 200 OK after purge raw, got %d", recChain.Code)
	}
	var chainEvents []contracts.NormalizedEvent
	if err := json.NewDecoder(recChain.Body).Decode(&chainEvents); err != nil {
		t.Fatalf("decode empty chain: %v", err)
	}
	if len(chainEvents) != 0 {
		t.Errorf("expected 0 events in chain after purge-raw, got %d", len(chainEvents))
	}
}

func TestAnalyze_ClickHouseError(t *testing.T) {
	// Mock ClickHouse that returns 500 error
	mockCH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("DB::Exception: Memory limit exceeded"))
	}))
	defer mockCH.Close()

	srv := NewServer(mockCH.URL)
	reqPayload := contracts.AnalyzeCaseRequest{CaseID: "case-err"}
	body, _ := json.Marshal(reqPayload)

	req := httptest.NewRequest(http.MethodPost, "/internal/rca/analyze", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for clickhouse error, got %d", rec.Code)
	}
}

func TestChain_DirectLoadAndEmptyKey(t *testing.T) {
	mockCH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes := make([]byte, 2048)
		n, _ := r.Body.Read(bodyBytes)
		query := string(bodyBytes[:n])

		if strings.Contains(query, "SELECT count() AS cnt FROM logs_rca.events_raw") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"cnt":1}` + "\n"))
			return
		}

		if strings.Contains(query, "FROM logs_rca.events_raw") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			ev := contracts.NormalizedEvent{
				CaseID:     "case-direct",
				EventID:    "00000000-0000-0000-0000-000000000010",
				Ts:         time.Now().UTC(),
				Service:    "chat-com",
				RequestID:  "req-12345",
				TemplateID: 10,
			}
			b, _ := json.Marshal(ev)
			_, _ = w.Write(append(b, '\n'))
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer mockCH.Close()

	srv := NewServer(mockCH.URL)

	// Call chain endpoint without prior analyze call (lazy loading)
	req := httptest.NewRequest(http.MethodGet, "/internal/rca/chain/case-direct?key=req-12345", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var events []contracts.NormalizedEvent
	if err := json.NewDecoder(rec.Body).Decode(&events); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if len(events) != 1 || events[0].RequestID != "req-12345" {
		t.Errorf("expected 1 event with req-12345, got %+v", events)
	}

	// Empty key should return empty slice
	reqEmptyKey := httptest.NewRequest(http.MethodGet, "/internal/rca/chain/case-direct?key=", nil)
	recEmptyKey := httptest.NewRecorder()
	srv.ServeHTTP(recEmptyKey, reqEmptyKey)
	if recEmptyKey.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for empty key, got %d", recEmptyKey.Code)
	}
	var emptyEvents []contracts.NormalizedEvent
	_ = json.NewDecoder(recEmptyKey.Body).Decode(&emptyEvents)
	if len(emptyEvents) != 0 {
		t.Errorf("expected 0 events for empty key, got %d", len(emptyEvents))
	}
}
