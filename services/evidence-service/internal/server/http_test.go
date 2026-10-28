package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/evidence-service/internal/server"
)

func TestServer_Health(t *testing.T) {
	tmpDir := t.TempDir()
	srv := server.NewServer("http://127.0.0.1:8123", tmpDir)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode json body: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status 'ok', got %q", body["status"])
	}
}

func TestServer_BuildEvidence_Success(t *testing.T) {
	tmpDir := t.TempDir()
	srv := server.NewServer("http://127.0.0.1:8123", tmpDir)

	now := time.Now().UTC()
	buildReq := contracts.BuildEvidenceRequest{
		CaseID: "case-srv-01",
		RunID:  "run-001",
		Analysis: contracts.AnalyzeCaseResponse{
			CaseID:        "case-srv-01",
			RunID:         "run-001",
			IncidentStart: now.Add(-10 * time.Minute),
			IncidentEnd:   now,
			Hypotheses: []contracts.Hypothesis{
				{
					ID:         "H-01",
					Rank:       1,
					Role:       contracts.RoleRootCause,
					Confidence: 0.9,
					Service:    "chat-session",
				},
			},
			EvidenceItems: []contracts.EvidenceItem{
				{
					ID:           "E-01",
					Kind:         contracts.EvidenceKindRawLog,
					Service:      "chat-session",
					Summary:      "Timeout log",
					Timestamp:    now,
					RedactedLine: "timeout connecting to user ak.odilov@talkwus.com",
				},
			},
		},
	}

	bodyBytes, err := json.Marshal(buildReq)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/internal/evidence/build", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp contracts.BuildEvidenceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.CaseID != buildReq.CaseID {
		t.Errorf("expected CaseID %s, got %s", buildReq.CaseID, resp.CaseID)
	}
	if resp.RunID != buildReq.RunID {
		t.Errorf("expected RunID %s, got %s", buildReq.RunID, resp.RunID)
	}
	if len(resp.EvidenceItems) != 1 {
		t.Fatalf("expected 1 evidence item in response, got %d", len(resp.EvidenceItems))
	}

	// Verify report directory exists on disk
	if _, err := os.Stat(filepath.Join(tmpDir, buildReq.CaseID, buildReq.RunID)); err != nil {
		t.Fatalf("expected report dir to exist: %v", err)
	}
}

func TestServer_BuildEvidence_InvalidRequests(t *testing.T) {
	tmpDir := t.TempDir()
	srv := server.NewServer("http://127.0.0.1:8123", tmpDir)

	// 1. Invalid JSON body
	req1 := httptest.NewRequest(http.MethodPost, "/internal/evidence/build", bytes.NewReader([]byte("{invalid-json")))
	rec1 := httptest.NewRecorder()
	srv.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad json, got %d", rec1.Code)
	}

	// 2. Empty CaseID
	emptyCaseReq := contracts.BuildEvidenceRequest{CaseID: ""}
	b, _ := json.Marshal(emptyCaseReq)
	req2 := httptest.NewRequest(http.MethodPost, "/internal/evidence/build", bytes.NewReader(b))
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty case_id, got %d", rec2.Code)
	}
}

func TestServer_BuildEvidence_ClickHouseFallback(t *testing.T) {
	// Mock ClickHouse server returning empty rows
	chServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "{}")
	}))
	defer chServer.Close()

	tmpDir := t.TempDir()
	srv := server.NewServer(chServer.URL, tmpDir)

	buildReq := contracts.BuildEvidenceRequest{
		CaseID: "case-ch-01",
		RunID:  "run-001",
		Analysis: contracts.AnalyzeCaseResponse{
			CaseID: "case-ch-01",
			RunID:  "run-001",
			Hypotheses: []contracts.Hypothesis{
				{
					ID:      "H-01",
					Role:    contracts.RoleRootCause,
					Service: "chat-session",
				},
			},
		},
	}

	b, _ := json.Marshal(buildReq)
	req := httptest.NewRequest(http.MethodPost, "/internal/evidence/build", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from clickhouse fallback build, got %d: %s", rec.Code, rec.Body.String())
	}
}
