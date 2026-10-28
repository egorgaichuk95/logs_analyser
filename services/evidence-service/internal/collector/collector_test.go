package collector

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

func TestCollectEvidence_FullFlow(t *testing.T) {
	now := time.Date(2026, 10, 28, 14, 0, 0, 0, time.UTC)

	// Mock ClickHouse server returning raw events
	mockCH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sql := string(body)

		if strings.Contains(sql, "FROM logs_rca.events_raw") {
			row1 := map[string]any{
				"case_id":           "case-101",
				"event_id":          "11111111-1111-1111-1111-111111111111",
				"ts":                now.Format("2006-01-02 15:04:05.000000000"),
				"service":           "chat-session",
				"pod":               "chat-session-xyz-1",
				"template_id":       uint64(42),
				"source_file":       "/logs/chat-session_part-0001.json",
				"source_record_idx": uint32(10),
				"raw_redacted":      `{"level":"ERROR","msg":"pool exhausted after 5000ms"}`,
			}
			row2 := map[string]any{
				"case_id":           "case-101",
				"event_id":          "22222222-2222-2222-2222-222222222222",
				"ts":                now.Add(time.Second).Format("2006-01-02 15:04:05.000000000"),
				"service":           "chat-session",
				"pod":               "chat-session-xyz-1",
				"template_id":       uint64(42),
				"source_file":       "/logs/chat-session_part-0001.json",
				"source_record_idx": uint32(11),
				"raw_redacted":      `{"level":"ERROR","msg":"pool exhausted after 5001ms"}`,
			}
			b1, _ := json.Marshal(row1)
			b2, _ := json.Marshal(row2)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(append(b1, '\n'))
			_, _ = w.Write(append(b2, '\n'))
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer mockCH.Close()

	analysis := &contracts.AnalyzeCaseResponse{
		CaseID:        "case-101",
		RunID:         "run-001",
		IncidentStart: now,
		IncidentEnd:   now.Add(30 * time.Minute),
		Hypotheses: []contracts.Hypothesis{
			{
				ID:                 "H-01",
				Rank:               1,
				Role:               contracts.RoleRootCause,
				Confidence:         0.98,
				Service:            "chat-session",
				TemplateID:         42,
				TemplateText:       "pool exhausted after <*>ms",
				ErrorClass:         "RESOURCE",
				DominantPod:        "chat-session-xyz-1",
				OnsetTime:          now,
				BaselineRatePerMin: 0.1,
				IncidentRatePerMin: 55.4,
				GrowthRatio:        554.0,
				PoissonPValue:      1.2e-15,
			},
		},
		ClockSkewFindings: []contracts.ClockSkewFinding{
			{
				Service:     "chat-com",
				Pod:         "chat-com-abc-2",
				SkewMs:      1240,
				SampleRawTs: now.Format(time.RFC3339),
				SampleAppTs: now.Add(1240 * time.Millisecond).Format(time.RFC3339),
			},
		},
		ServiceEdges: []contracts.ServiceEdge{
			{
				CaseID:         "case-101",
				CallerService:  "chat-com",
				CalleeService:  "chat-session",
				EvidenceSource: "peer_url",
				CallCount:      1000,
				ErrorCount:     450,
			},
		},
	}

	items, err := CollectEvidence(context.Background(), mockCH.URL, analysis)
	if err != nil {
		t.Fatalf("CollectEvidence returned error: %v", err)
	}

	// Should have: E-01 (stat_test), E-02 (raw_log), E-03 (raw_log), E-04 (clock_skew), E-05 (topology_edge)
	if len(items) != 5 {
		t.Fatalf("expected 5 evidence items, got %d", len(items))
	}

	// Verify sequential numbering
	for i, item := range items {
		expectedID := fmt.Sprintf("E-%02d", i+1)
		if item.ID != expectedID {
			t.Errorf("expected item[%d].ID=%s, got %s", i, expectedID, item.ID)
		}
	}

	// Check stat_test item
	eStat := items[0]
	if eStat.Kind != contracts.EvidenceKindStatTest {
		t.Errorf("expected Kind=%s, got %s", contracts.EvidenceKindStatTest, eStat.Kind)
	}
	if !strings.Contains(eStat.Summary, "baseline=0.10/мин") || !strings.Contains(eStat.Summary, "инцидент=55.40/мин") {
		t.Errorf("stat_test summary mismatch: %s", eStat.Summary)
	}

	// Check raw_log items
	eRaw1 := items[1]
	if eRaw1.Kind != contracts.EvidenceKindRawLog {
		t.Errorf("expected Kind=%s, got %s", contracts.EvidenceKindRawLog, eRaw1.Kind)
	}
	expectedSHA := fmt.Sprintf("%x", sha256.Sum256([]byte(`{"level":"ERROR","msg":"pool exhausted after 5000ms"}`)))
	if eRaw1.SHA256 != expectedSHA {
		t.Errorf("expected sha256=%s, got %s", expectedSHA, eRaw1.SHA256)
	}
	if eRaw1.SourceFile != "/logs/chat-session_part-0001.json" || eRaw1.SourceRecordIdx != 10 {
		t.Errorf("unexpected source location: %s:%d", eRaw1.SourceFile, eRaw1.SourceRecordIdx)
	}
	if !strings.HasPrefix(eRaw1.LogQLSnippet, `{service_name="chat-session", pod="chat-session-xyz-1"} |=`) {
		t.Errorf("invalid LogQL snippet format: %s", eRaw1.LogQLSnippet)
	}

	// Check clock_skew item
	eSkew := items[3]
	if eSkew.Kind != contracts.EvidenceKindClockSkew {
		t.Errorf("expected Kind=%s, got %s", contracts.EvidenceKindClockSkew, eSkew.Kind)
	}
	if !strings.Contains(eSkew.Summary, "1240 мс") {
		t.Errorf("expected 1240 ms in clock skew summary, got: %s", eSkew.Summary)
	}

	// Check topology_edge item
	eTopo := items[4]
	if eTopo.Kind != contracts.EvidenceKindTopologyEdge {
		t.Errorf("expected Kind=%s, got %s", contracts.EvidenceKindTopologyEdge, eTopo.Kind)
	}
	if !strings.Contains(eTopo.Summary, "chat-com -> chat-session") {
		t.Errorf("topology summary mismatch: %s", eTopo.Summary)
	}

	// Check hypothesis EvidenceIDs populated
	if len(analysis.Hypotheses[0].EvidenceIDs) != 3 {
		t.Errorf("expected 3 EvidenceIDs in H-01, got %v", analysis.Hypotheses[0].EvidenceIDs)
	}
	expectedH1IDs := []string{"E-01", "E-02", "E-03"}
	for i, id := range expectedH1IDs {
		if i < len(analysis.Hypotheses[0].EvidenceIDs) && analysis.Hypotheses[0].EvidenceIDs[i] != id {
			t.Errorf("expected EvidenceIDs[%d]=%s, got %s", i, id, analysis.Hypotheses[0].EvidenceIDs[i])
		}
	}
}

func TestCollectEvidence_PurgedRawFallbackToTemplatesDict(t *testing.T) {
	now := time.Date(2026, 10, 28, 14, 0, 0, 0, time.UTC)

	// Mock ClickHouse server returning empty for events_raw, but a row for templates_dict
	mockCH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sql := string(body)

		if strings.Contains(sql, "FROM logs_rca.events_raw") {
			// events_raw is purged!
			w.WriteHeader(http.StatusOK)
			return
		}

		if strings.Contains(sql, "FROM logs_rca.templates_dict") {
			row := map[string]any{
				"case_id":         "case-202",
				"service":         "chat-session",
				"template_id":     uint64(99),
				"template_text":   "redis connection refused <*>",
				"sample_redacted": `{"level":"FATAL","msg":"redis connection refused [REDACTED_IP]"}`,
				"source_place":    "db/redis.go:128",
			}
			b, _ := json.Marshal(row)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(append(b, '\n'))
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer mockCH.Close()

	analysis := &contracts.AnalyzeCaseResponse{
		CaseID:        "case-202",
		IncidentStart: now,
		Hypotheses: []contracts.Hypothesis{
			{
				ID:                 "H-01",
				Rank:               1,
				Role:               contracts.RoleRootCause,
				Confidence:         0.92,
				Service:            "chat-session",
				TemplateID:         99,
				TemplateText:       "redis connection refused <*>",
				ErrorClass:         "UNAVAILABLE",
				DominantPod:        "chat-session-pod-0",
				OnsetTime:          now,
				BaselineRatePerMin: 0.0,
				IncidentRatePerMin: 120.0,
				GrowthRatio:        1200.0,
				PoissonPValue:      1e-20,
			},
		},
	}

	items, err := CollectEvidence(context.Background(), mockCH.URL, analysis)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have E-01 (stat_test) and E-02 (raw_log from templates_dict fallback)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0].Kind != contracts.EvidenceKindStatTest {
		t.Errorf("expected Kind=%s, got %s", contracts.EvidenceKindStatTest, items[0].Kind)
	}

	rawItem := items[1]
	if rawItem.Kind != contracts.EvidenceKindRawLog {
		t.Errorf("expected Kind=%s, got %s", contracts.EvidenceKindRawLog, rawItem.Kind)
	}
	if rawItem.RedactedLine != `{"level":"FATAL","msg":"redis connection refused [REDACTED_IP]"}` {
		t.Errorf("expected sample_redacted in RedactedLine, got: %s", rawItem.RedactedLine)
	}
	expectedSHA := fmt.Sprintf("%x", sha256.Sum256([]byte(rawItem.RedactedLine)))
	if rawItem.SHA256 != expectedSHA {
		t.Errorf("expected SHA %s, got %s", expectedSHA, rawItem.SHA256)
	}
	if !strings.HasPrefix(rawItem.LogQLSnippet, `{service_name="chat-session", pod="chat-session-pod-0"} |=`) {
		t.Errorf("expected LogQL snippet, got: %s", rawItem.LogQLSnippet)
	}
	if len(analysis.Hypotheses[0].EvidenceIDs) != 2 {
		t.Errorf("expected 2 EvidenceIDs in hypothesis, got %v", analysis.Hypotheses[0].EvidenceIDs)
	}
}

func TestCollectEvidence_BothPurgedFallbackToHypothesisTemplate(t *testing.T) {
	now := time.Date(2026, 10, 28, 14, 0, 0, 0, time.UTC)

	// Mock ClickHouse returning empty for all queries
	mockCH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mockCH.Close()

	analysis := &contracts.AnalyzeCaseResponse{
		CaseID:        "case-303",
		IncidentStart: now,
		Hypotheses: []contracts.Hypothesis{
			{
				ID:           "H-01",
				Service:      "auth-service",
				TemplateID:   77,
				TemplateText: "token decryption failed: invalid signature",
				ErrorClass:   "PEER_4XX",
				DominantPod:  "auth-pod-1",
			},
		},
	}

	items, err := CollectEvidence(context.Background(), mockCH.URL, analysis)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	rawItem := items[1]
	if rawItem.Kind != contracts.EvidenceKindRawLog {
		t.Errorf("expected Kind=%s, got %s", contracts.EvidenceKindRawLog, rawItem.Kind)
	}
	if rawItem.RedactedLine != "token decryption failed: invalid signature" {
		t.Errorf("expected TemplateText as RedactedLine fallback, got: %s", rawItem.RedactedLine)
	}
}

func TestCollectEvidence_NilAndEmptyInput(t *testing.T) {
	// Nil analysis
	items, err := CollectEvidence(context.Background(), "http://127.0.0.1:8123", nil)
	if err != nil {
		t.Errorf("expected nil error on nil analysis, got %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items on nil analysis, got %d", len(items))
	}

	// Empty analysis
	emptyResp := &contracts.AnalyzeCaseResponse{}
	items, err = CollectEvidence(context.Background(), "http://127.0.0.1:8123", emptyResp)
	if err != nil {
		t.Errorf("expected nil error on empty analysis, got %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items on empty analysis, got %d", len(items))
	}
}

func TestCollectEvidence_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	analysis := &contracts.AnalyzeCaseResponse{
		CaseID: "case-999",
		Hypotheses: []contracts.Hypothesis{
			{ID: "H-01", Service: "svc", TemplateID: 1},
		},
	}

	_, err := CollectEvidence(ctx, "http://127.0.0.1:8123", analysis)
	if err == nil {
		t.Fatal("expected error on canceled context, got nil")
	}
}

func TestCollectEvidence_ClickHouseError(t *testing.T) {
	mockCH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal clickhouse error", http.StatusInternalServerError)
	}))
	defer mockCH.Close()

	analysis := &contracts.AnalyzeCaseResponse{
		CaseID: "case-err",
		Hypotheses: []contracts.Hypothesis{
			{
				ID:           "H-01",
				Service:      "svc",
				TemplateID:   55,
				TemplateText: "fallback template",
			},
		},
	}

	// Should fallback gracefully without crashing
	items, err := CollectEvidence(context.Background(), mockCH.URL, analysis)
	if err != nil {
		t.Fatalf("did not expect fatal error on CH 500 when template fallback exists, got: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items with fallback, got %d", len(items))
	}
}

func TestCollectEvidence_EmptyURLAndZeroErrors(t *testing.T) {
	analysis := &contracts.AnalyzeCaseResponse{
		CaseID: "case-empty-url",
		Hypotheses: []contracts.Hypothesis{
			{
				ID:           "H-01",
				Service:      "billing",
				TemplateID:   12,
				TemplateText: "",
				ErrorClass:   "OWN_DEFECT",
			},
		},
		ServiceEdges: []contracts.ServiceEdge{
			{
				CallerService: "api",
				CalleeService: "billing",
				CallCount:     50,
				ErrorCount:    0, // Should NOT create topology_edge evidence
			},
		},
	}

	items, err := CollectEvidence(context.Background(), "", analysis)
	if err != nil {
		t.Fatalf("unexpected error on empty CH URL: %v", err)
	}
	// Expect only 1 item: E-01 (stat_test) since template is empty and CH url is empty, and edge has 0 errors
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Kind != contracts.EvidenceKindStatTest {
		t.Errorf("expected Kind=%s, got %s", contracts.EvidenceKindStatTest, items[0].Kind)
	}
}
