package storage

import (
	"context"
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
	"github.com/local/logs-analyzer/services/gateway-api/internal/metadb"
)

func TestStore_SaveGetListDeleteCase(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "meta", "state.json")

	store, err := metadb.NewStore(statePath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// Empty store should return empty list
	cases := store.ListCases()
	if len(cases) != 0 {
		t.Fatalf("expected 0 cases, got %d", len(cases))
	}

	// Empty case ID should fail
	if err := store.SaveCase(contracts.CaseMeta{}); err != contracts.ErrEmptyCaseID {
		t.Fatalf("expected ErrEmptyCaseID, got %v", err)
	}

	// Save Case 1
	now := time.Now().UTC().Truncate(time.Second)
	c1 := contracts.CaseMeta{
		ID:          "case-01",
		Title:       "Payment Failure Incident",
		Description: "High latency and 500 errors in payments",
		Status:      contracts.CaseStatusCreated,
		CreatedAt:   now.Add(-10 * time.Minute),
	}
	if err := store.SaveCase(c1); err != nil {
		t.Fatalf("SaveCase c1 failed: %v", err)
	}

	// Save Case 2
	c2 := contracts.CaseMeta{
		ID:          "case-02",
		Title:       "Chat Outage",
		Description: "WebSocket disconnects",
		Status:      contracts.CaseStatusReady,
		CreatedAt:   now,
	}
	if err := store.SaveCase(c2); err != nil {
		t.Fatalf("SaveCase c2 failed: %v", err)
	}

	// GetCase
	got1, ok := store.GetCase("case-01")
	if !ok || got1.ID != "case-01" || got1.Title != "Payment Failure Incident" {
		t.Fatalf("GetCase case-01 returned unexpected: ok=%v, case=%+v", ok, got1)
	}

	// Get non-existent
	_, ok = store.GetCase("non-existent")
	if ok {
		t.Fatal("expected GetCase non-existent to return false")
	}

	// ListCases should be sorted by CreatedAt descending (case-02 then case-01)
	list := store.ListCases()
	if len(list) != 2 {
		t.Fatalf("expected 2 cases, got %d", len(list))
	}
	if list[0].ID != "case-02" || list[1].ID != "case-01" {
		t.Fatalf("unexpected order: [0]=%s, [1]=%s", list[0].ID, list[1].ID)
	}

	// DeleteCase
	if err := store.DeleteCase("case-01"); err != nil {
		t.Fatalf("DeleteCase case-01 failed: %v", err)
	}
	_, ok = store.GetCase("case-01")
	if ok {
		t.Fatal("case-01 should be deleted")
	}
	listAfter := store.ListCases()
	if len(listAfter) != 1 || listAfter[0].ID != "case-02" {
		t.Fatalf("expected only case-02, got %+v", listAfter)
	}

	// Delete non-existent case should return error
	if err := store.DeleteCase("case-01"); err == nil {
		t.Fatal("expected error when deleting non-existent case, got nil")
	}
	if err := store.DeleteCase(""); err != contracts.ErrEmptyCaseID {
		t.Fatalf("expected ErrEmptyCaseID, got %v", err)
	}
}

func TestStore_PersistenceAndReload(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "meta", "state.json")

	store1, err := metadb.NewStore(statePath)
	if err != nil {
		t.Fatalf("NewStore 1 failed: %v", err)
	}

	c := contracts.CaseMeta{
		ID:        "case-persist",
		Title:     "Persistent Case",
		Status:    contracts.CaseStatusReady,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := store1.SaveCase(c); err != nil {
		t.Fatalf("SaveCase failed: %v", err)
	}

	run := contracts.AnalyzeCaseResponse{
		CaseID:        "case-persist",
		RunID:         "run-001",
		IncidentStart: time.Now().Add(-1 * time.Hour),
		IncidentEnd:   time.Now(),
		Hypotheses: []contracts.Hypothesis{
			{
				ID:           "hyp-1",
				Rank:         1,
				Role:         contracts.RoleRootCause,
				Confidence:   0.95,
				Service:      "auth-service",
				TemplateID:   42,
				TemplateText: "auth error: connection refused",
				SourcePlace:  "auth/handler.go:88",
			},
		},
	}
	if err := store1.SaveAnalysisRun(run); err != nil {
		t.Fatalf("SaveAnalysisRun failed: %v", err)
	}

	rep := contracts.BuildEvidenceResponse{
		CaseID:       "case-persist",
		RunID:        "run-001",
		ReportDir:    "/reports/case-persist/run-001",
		MarkdownPath: "/reports/case-persist/run-001/report.md",
		HTMLPath:     "/reports/case-persist/run-001/report.html",
		SizeBytes:    1024,
	}
	if err := store1.SaveReport(rep); err != nil {
		t.Fatalf("SaveReport failed: %v", err)
	}

	// Verify temporary file is cleaned up after atomic rename
	tmpFile := statePath + ".tmp"
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Fatalf("expected temp file to not exist after atomic rename, err: %v", err)
	}

	// Reload from the same file in a brand new Store instance
	store2, err := metadb.NewStore(statePath)
	if err != nil {
		t.Fatalf("NewStore 2 failed: %v", err)
	}

	// Verify case loaded
	loadedCase, ok := store2.GetCase("case-persist")
	if !ok || loadedCase.Title != "Persistent Case" || loadedCase.LatestRunID != "run-001" {
		t.Fatalf("reloaded case mismatch: ok=%v, case=%+v", ok, loadedCase)
	}
	if loadedCase.Status != contracts.CaseStatusAnalyzed {
		t.Fatalf("expected case status ANALYZED, got %s", loadedCase.Status)
	}

	// Verify run loaded
	loadedRun, ok := store2.GetAnalysisRun("case-persist", "run-001")
	if !ok || len(loadedRun.Hypotheses) != 1 || loadedRun.Hypotheses[0].TemplateID != 42 {
		t.Fatalf("reloaded run mismatch: ok=%v, run=%+v", ok, loadedRun)
	}

	// Verify latest run resolution
	latestRun, ok := store2.GetAnalysisRun("case-persist", "latest")
	if !ok || latestRun.RunID != "run-001" {
		t.Fatalf("latest run resolution failed: ok=%v, run=%+v", ok, latestRun)
	}

	// Verify report loaded
	loadedRep, ok := store2.GetReport("case-persist", "run-001")
	if !ok || loadedRep.SizeBytes != 1024 || loadedRep.MarkdownPath != "/reports/case-persist/run-001/report.md" {
		t.Fatalf("reloaded report mismatch: ok=%v, report=%+v", ok, loadedRep)
	}

	// Verify latest report resolution
	latestRep, ok := store2.GetReport("case-persist", "")
	if !ok || latestRep.RunID != "run-001" {
		t.Fatalf("latest report resolution with empty runID failed: ok=%v, rep=%+v", ok, latestRep)
	}
}

func TestStore_FindRecurringCases(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "meta", "state.json")

	store, err := metadb.NewStore(statePath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// Populate cases
	_ = store.SaveCase(contracts.CaseMeta{ID: "c-current", Title: "Current Incident"})
	_ = store.SaveCase(contracts.CaseMeta{ID: "c-old-1", Title: "Incident 1"})
	_ = store.SaveCase(contracts.CaseMeta{ID: "c-old-2", Title: "Incident 2"})
	_ = store.SaveCase(contracts.CaseMeta{ID: "c-old-3", Title: "Incident 3"})
	_ = store.SaveCase(contracts.CaseMeta{ID: "c-old-4", Title: "Incident 4"})

	// Run for c-current
	_ = store.SaveAnalysisRun(contracts.AnalyzeCaseResponse{
		CaseID: "c-current",
		RunID:  "r0",
		Hypotheses: []contracts.Hypothesis{
			{TemplateID: 100, SourcePlace: "auth/login.go:50"},
		},
	})

	// Run for c-old-1 (matches TemplateID 100)
	_ = store.SaveAnalysisRun(contracts.AnalyzeCaseResponse{
		CaseID: "c-old-1",
		RunID:  "r1",
		Hypotheses: []contracts.Hypothesis{
			{TemplateID: 100, SourcePlace: "different/file.go:10"},
		},
	})

	// Run for c-old-2 (matches SourcePlace auth/login.go:50)
	_ = store.SaveAnalysisRun(contracts.AnalyzeCaseResponse{
		CaseID: "c-old-2",
		RunID:  "r2",
		Hypotheses: []contracts.Hypothesis{
			{TemplateID: 999, SourcePlace: "auth/login.go:50"},
		},
	})

	// Run for c-old-3 (matches both)
	_ = store.SaveAnalysisRun(contracts.AnalyzeCaseResponse{
		CaseID: "c-old-3",
		RunID:  "r3",
		Hypotheses: []contracts.Hypothesis{
			{TemplateID: 100, SourcePlace: "auth/login.go:50"},
		},
	})

	// Run for c-old-4 (matches neither)
	_ = store.SaveAnalysisRun(contracts.AnalyzeCaseResponse{
		CaseID: "c-old-4",
		RunID:  "r4",
		Hypotheses: []contracts.Hypothesis{
			{TemplateID: 777, SourcePlace: "unrelated/file.go:1"},
		},
	})

	// Search matching templateID 100 only (should exclude c-current, should find c-old-1 and c-old-3)
	res1 := store.FindRecurringCases("c-current", 100, "")
	if strings.Join(res1, ",") != "c-old-1,c-old-3" {
		t.Fatalf("expected c-old-1,c-old-3, got %v", res1)
	}

	// Search matching sourcePlace only (should find c-old-2 and c-old-3)
	res2 := store.FindRecurringCases("c-current", 0, "auth/login.go:50")
	if strings.Join(res2, ",") != "c-old-2,c-old-3" {
		t.Fatalf("expected c-old-2,c-old-3, got %v", res2)
	}

	// Search matching either templateID 100 or sourcePlace auth/login.go:50 (finds 1, 2, 3)
	res3 := store.FindRecurringCases("c-current", 100, "auth/login.go:50")
	if strings.Join(res3, ",") != "c-old-1,c-old-2,c-old-3" {
		t.Fatalf("expected c-old-1,c-old-2,c-old-3, got %v", res3)
	}

	// Search non-matching
	res4 := store.FindRecurringCases("c-current", 555, "nonexistent.go:1")
	if len(res4) != 0 {
		t.Fatalf("expected empty slice, got %v", res4)
	}

	// Empty query
	res5 := store.FindRecurringCases("c-current", 0, "")
	if len(res5) != 0 {
		t.Fatalf("expected empty slice, got %v", res5)
	}
}

func TestStore_MarkAllRawPurged(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "meta", "state.json")

	store, err := metadb.NewStore(statePath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	_ = store.SaveCase(contracts.CaseMeta{ID: "c1", Status: contracts.CaseStatusCreated})
	_ = store.SaveCase(contracts.CaseMeta{ID: "c2", Status: contracts.CaseStatusReady})
	_ = store.SaveCase(contracts.CaseMeta{ID: "c3", Status: contracts.CaseStatusAnalyzed})

	if err := store.MarkAllRawPurged(); err != nil {
		t.Fatalf("MarkAllRawPurged failed: %v", err)
	}

	for _, id := range []string{"c1", "c2", "c3"} {
		c, ok := store.GetCase(id)
		if !ok || c.Status != contracts.CaseStatusRawPurged {
			t.Fatalf("expected case %s to have RAW_PURGED status, got ok=%v, status=%s", id, ok, c.Status)
		}
	}

	// Verify persistence
	storeReloaded, err := metadb.NewStore(statePath)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	c1, _ := storeReloaded.GetCase("c1")
	if c1.Status != contracts.CaseStatusRawPurged {
		t.Fatalf("reloaded case status is not RAW_PURGED: %s", c1.Status)
	}
}

func TestManager_GetStatus_Success(t *testing.T) {
	// Setup mock ClickHouse
	mockCH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		query := string(body)
		if !strings.Contains(query, "system.parts") {
			t.Errorf("unexpected ClickHouse query: %s", query)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Return ClickHouse JSONEachRow format
		// Test both numeric bytes and string-encoded bytes (from JSONEachRow quote integers setting)
		resp := `{"table":"events_raw","bytes":10000000}
{"table":"identity_edges","bytes":"2000000"}
{"table":"ingested_files","bytes":500000}
{"table":"events_agg_1m","bytes":150000}
{"table":"templates_dict","bytes":"50000"}
{"table":"service_edges","bytes":25000}
{"table":"system_log_ignored","bytes":99999}
`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(resp))
	}))
	defer mockCH.Close()

	// Setup dirs with known sizes
	stagingDir := t.TempDir()
	reportsDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(stagingDir, "chunk-1.json"), []byte(strings.Repeat("a", 3000)), 0644)
	_ = os.WriteFile(filepath.Join(stagingDir, "chunk-2.json"), []byte(strings.Repeat("b", 2000)), 0644)

	_ = os.WriteFile(filepath.Join(reportsDir, "report.md"), []byte(strings.Repeat("c", 1500)), 0644)

	mgr := NewManager(mockCH.URL, stagingDir, reportsDir, 16106127360, 10737418240, nil)

	status, err := mgr.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}

	expectedRaw := int64(10000000 + 2000000 + 500000)
	expectedAgg := int64(150000 + 50000 + 25000)
	expectedStaging := int64(5000)
	expectedReports := int64(1500)
	expectedTotal := expectedRaw + expectedAgg + expectedStaging + expectedReports

	if status.RawBytes != expectedRaw {
		t.Fatalf("expected RawBytes=%d, got %d", expectedRaw, status.RawBytes)
	}
	if status.AggBytes != expectedAgg {
		t.Fatalf("expected AggBytes=%d, got %d", expectedAgg, status.AggBytes)
	}
	if status.StagingBytes != expectedStaging {
		t.Fatalf("expected StagingBytes=%d, got %d", expectedStaging, status.StagingBytes)
	}
	if status.ReportsBytes != expectedReports {
		t.Fatalf("expected ReportsBytes=%d, got %d", expectedReports, status.ReportsBytes)
	}
	if status.TotalBytes != expectedTotal {
		t.Fatalf("expected TotalBytes=%d, got %d", expectedTotal, status.TotalBytes)
	}
	if status.QuotaBytes != 16106127360 {
		t.Fatalf("expected QuotaBytes=16106127360, got %d", status.QuotaBytes)
	}
	if status.RawQuotaBytes != 10737418240 {
		t.Fatalf("expected RawQuotaBytes=10737418240, got %d", status.RawQuotaBytes)
	}
}

func TestManager_GetStatus_Errors(t *testing.T) {
	// 1. ClickHouse returns 500
	mockErrCH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("ClickHouse internal error"))
	}))
	defer mockErrCH.Close()

	mgr := NewManager(mockErrCH.URL, "", "", 0, 0, nil)
	_, err := mgr.GetStatus(context.Background())
	if err == nil {
		t.Fatal("expected error on ClickHouse 500, got nil")
	}

	// 2. Empty ClickHouse URL
	mgrEmpty := NewManager("", "", "", 0, 0, nil)
	_, err = mgrEmpty.GetStatus(context.Background())
	if err == nil {
		t.Fatal("expected error on empty ClickHouse URL, got nil")
	}
}

func TestManager_PurgeAllRawLogs_Success(t *testing.T) {
	var mu sync.Mutex
	queriesReceived := make([]string, 0)

	mockCH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		query := strings.TrimSpace(string(body))

		mu.Lock()
		queriesReceived = append(queriesReceived, query)
		mu.Unlock()

		if strings.Contains(query, "system.parts") {
			// Return initial parts before purge
			resp := `{"table":"events_raw","bytes":5000000}
{"table":"identity_edges","bytes":1000000}
{"table":"ingested_files","bytes":200000}
{"table":"events_agg_1m","bytes":100000}
{"table":"templates_dict","bytes":20000}
{"table":"service_edges","bytes":10000}
`
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(resp))
			return
		}

		if strings.HasPrefix(query, "TRUNCATE TABLE") {
			w.WriteHeader(http.StatusOK)
			return
		}

		w.WriteHeader(http.StatusBadRequest)
	}))
	defer mockCH.Close()

	// Dirs
	stagingDir := t.TempDir()
	reportsDir := t.TempDir()

	file1 := filepath.Join(stagingDir, "file1.log")
	file2 := filepath.Join(stagingDir, "file2.log")
	_ = os.WriteFile(file1, []byte("content1"), 0644)
	_ = os.WriteFile(file2, []byte("content2"), 0644)

	repFile := filepath.Join(reportsDir, "case-01-report.md")
	_ = os.WriteFile(repFile, []byte("# Report for Case 01"), 0644)

	// Meta store
	metaDir := t.TempDir()
	store, err := metadb.NewStore(filepath.Join(metaDir, "state.json"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	_ = store.SaveCase(contracts.CaseMeta{
		ID:     "case-01",
		Title:  "Incident 1",
		Status: contracts.CaseStatusAnalyzed,
	})
	_ = store.SaveReport(contracts.BuildEvidenceResponse{
		CaseID:       "case-01",
		RunID:        "run-1",
		MarkdownPath: repFile,
	})

	mgr := NewManager(mockCH.URL, stagingDir, reportsDir, 0, 0, store)

	res, err := mgr.PurgeAllRawLogs(context.Background())
	if err != nil {
		t.Fatalf("PurgeAllRawLogs failed: %v", err)
	}

	// 1. Verify freed bytes = rawBytes (5000000 + 1000000 + 200000) + stagingBytes (8 + 8 = 16)
	expectedFreed := int64(6200000 + 16)
	if res.FreedBytes != expectedFreed {
		t.Fatalf("expected FreedBytes=%d, got %d", expectedFreed, res.FreedBytes)
	}
	if res.Status != "OK" {
		t.Fatalf("expected Status='OK', got %s", res.Status)
	}
	if res.DurationMs < 0 {
		t.Fatalf("expected non-negative duration, got %d", res.DurationMs)
	}

	// 2. Verify ClickHouse truncate queries
	mu.Lock()
	truncates := make([]string, 0)
	for _, q := range queriesReceived {
		if strings.HasPrefix(q, "TRUNCATE TABLE") {
			truncates = append(truncates, q)
		}
	}
	mu.Unlock()

	expectedTruncates := []string{
		"TRUNCATE TABLE logs_rca.events_raw",
		"TRUNCATE TABLE logs_rca.identity_edges",
		"TRUNCATE TABLE logs_rca.ingested_files",
	}

	if len(truncates) != len(expectedTruncates) {
		t.Fatalf("expected %d TRUNCATE queries, got %d: %v", len(expectedTruncates), len(truncates), truncates)
	}
	for i, exp := range expectedTruncates {
		if truncates[i] != exp {
			t.Errorf("truncate[%d]: expected %q, got %q", i, exp, truncates[i])
		}
	}

	// Verify aggregate tables were NEVER truncated!
	for _, q := range queriesReceived {
		if strings.Contains(q, "events_agg_1m") || strings.Contains(q, "templates_dict") || strings.Contains(q, "service_edges") {
			if strings.HasPrefix(q, "TRUNCATE") {
				t.Fatalf("forbidden truncate on aggregate table: %s", q)
			}
		}
	}

	// 3. Verify stagingDir is completely cleared
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		t.Fatalf("ReadDir stagingDir failed: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected stagingDir to be empty after purge, got %d entries", len(entries))
	}

	// 4. Verify reportsDir is INTACT!
	if _, err := os.Stat(repFile); os.IsNotExist(err) {
		t.Fatal("report file in reportsDir was deleted during purge! It must be preserved.")
	}

	// 5. Verify case in Store is marked RAW_PURGED
	c, ok := store.GetCase("case-01")
	if !ok || c.Status != contracts.CaseStatusRawPurged {
		t.Fatalf("expected case-01 to have RAW_PURGED status, got ok=%v, status=%s", ok, c.Status)
	}

	// 6. Verify report in Store is still accessible
	rep, ok := store.GetReport("case-01", "run-1")
	if !ok || rep.MarkdownPath != repFile {
		t.Fatalf("expected report in Store to be preserved, got ok=%v, rep=%+v", ok, rep)
	}
}

func TestManager_PurgeAllRawLogs_Errors(t *testing.T) {
	// 1. Truncate failure in ClickHouse
	mockCH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		query := string(body)
		if strings.Contains(query, "system.parts") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"table":"events_raw","bytes":100}` + "\n"))
			return
		}
		if strings.HasPrefix(query, "TRUNCATE TABLE") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("truncate permission denied"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer mockCH.Close()

	mgr := NewManager(mockCH.URL, "", "", 0, 0, nil)
	_, err := mgr.PurgeAllRawLogs(context.Background())
	if err == nil {
		t.Fatal("expected error on truncate failure, got nil")
	}

	// 2. Pre-status query failure
	mockCHDown := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer mockCHDown.Close()

	mgrDown := NewManager(mockCHDown.URL, "", "", 0, 0, nil)
	_, err = mgrDown.PurgeAllRawLogs(context.Background())
	if err == nil {
		t.Fatal("expected error on pre-status failure, got nil")
	}
}

func TestManager_EdgeCases(t *testing.T) {
	// 1. Default quotas
	mgr := NewManager("http://localhost:8123", "", "", 0, 0, nil)
	if mgr.quotaBytes != 16106127360 {
		t.Fatalf("expected default quota 16106127360, got %d", mgr.quotaBytes)
	}
	if mgr.rawQuotaBytes != 10737418240 {
		t.Fatalf("expected default raw quota 10737418240, got %d", mgr.rawQuotaBytes)
	}

	// 2. Non-existent staging and reports dirs do not fail GetStatus
	mockCH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"table":"events_raw","bytes":100}` + "\n"))
	}))
	defer mockCH.Close()

	mgrNonExistent := NewManager(mockCH.URL, "/non/existent/staging/path", "/non/existent/reports/path", 100, 50, nil)
	status, err := mgrNonExistent.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus with non-existent dirs failed: %v", err)
	}
	if status.StagingBytes != 0 || status.ReportsBytes != 0 {
		t.Fatalf("expected 0 for non-existent dirs, got staging=%d, reports=%d", status.StagingBytes, status.ReportsBytes)
	}

	// 3. PurgeAllRawLogs with nil Store succeeds without panic
	mockCHPurge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mockCHPurge.Close()

	mgrNilStore := NewManager(mockCHPurge.URL, "", "", 0, 0, nil)
	res, err := mgrNilStore.PurgeAllRawLogs(context.Background())
	if err != nil {
		t.Fatalf("PurgeAllRawLogs with nil Store failed: %v", err)
	}
	if res.Status != "OK" {
		t.Fatalf("expected OK, got %s", res.Status)
	}

	// 4. GetStatus with malformed lines and large uint64 strings
	mockCHMalformed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("\n{bad-json\n{\"table\":\"events_raw\",\"bytes\":\"18446744073709551615\"}\n"))
	}))
	defer mockCHMalformed.Close()

	mgrMalformed := NewManager(mockCHMalformed.URL, "", "", 0, 0, nil)
	statusM, err := mgrMalformed.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus with malformed lines failed: %v", err)
	}
	if statusM.RawBytes == 0 {
		t.Fatal("expected non-zero RawBytes from uint64 parsing")
	}

	// 5. Store validation errors
	tmpDir := t.TempDir()
	st, _ := metadb.NewStore(filepath.Join(tmpDir, "st.json"))
	if err := st.SaveAnalysisRun(contracts.AnalyzeCaseResponse{}); err != contracts.ErrEmptyCaseID {
		t.Fatalf("expected ErrEmptyCaseID, got %v", err)
	}
	if err := st.SaveAnalysisRun(contracts.AnalyzeCaseResponse{CaseID: "c"}); err == nil {
		t.Fatal("expected error on empty runID, got nil")
	}
	if err := st.SaveReport(contracts.BuildEvidenceResponse{}); err != contracts.ErrEmptyCaseID {
		t.Fatalf("expected ErrEmptyCaseID, got %v", err)
	}
	if err := st.SaveReport(contracts.BuildEvidenceResponse{CaseID: "c"}); err == nil {
		t.Fatal("expected error on empty runID, got nil")
	}
	if _, ok := st.GetAnalysisRun("", ""); ok {
		t.Fatal("expected false for empty caseID")
	}
	if _, ok := st.GetReport("", ""); ok {
		t.Fatal("expected false for empty caseID")
	}
	if _, ok := st.GetCase(""); ok {
		t.Fatal("expected false for empty caseID")
	}
	if _, err := metadb.NewStore(""); err == nil {
		t.Fatal("expected error on empty store path")
	}
}
