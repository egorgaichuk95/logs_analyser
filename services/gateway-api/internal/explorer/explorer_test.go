package explorer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/gateway-api/internal/metadb"
)

func TestExplorer_GetContextLogs(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := metadb.NewStore(filepath.Join(tmpDir, "meta.json"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	case1 := contracts.CaseMeta{
		ID:        "case-1",
		Title:     "Case 1",
		Status:    contracts.CaseStatusReady,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.SaveCase(case1); err != nil {
		t.Fatalf("save case: %v", err)
	}

	casePurged := contracts.CaseMeta{
		ID:        "case-purged",
		Title:     "Purged Case",
		Status:    contracts.CaseStatusRawPurged,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.SaveCase(casePurged); err != nil {
		t.Fatalf("save case purged: %v", err)
	}

	// Mock ClickHouse
	chMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 1024)
		n, _ := r.Body.Read(body)
		query := string(body[:n])

		if contains(query, "case-error") {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("clickhouse query failed"))
			return
		}

		if contains(query, "count()") {
			if contains(query, "case-empty") {
				w.Write([]byte(`{"cnt":0}` + "\n"))
				return
			}
			w.Write([]byte(`{"cnt":10}` + "\n"))
			return
		}

		if contains(query, "events_raw") {
			rowJSON := `{"case_id":"case-1","event_id":"00000000-0000-0000-0000-000000000001","ts":"2026-10-28 20:15:30.000000000","app_ts":"2026-10-28 20:15:30.000000000","service":"chat-com","pod":"chat-com-1","level":"ERROR","message":"test error","error_class":"OWN_DEFECT"}` + "\n"
			w.Write([]byte(rowJSON))
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer chMock.Close()

	exp := NewExplorer(chMock.URL, store, chMock.Client())

	t.Run("Context for empty caseID", func(t *testing.T) {
		res, err := exp.GetContextLogs(context.Background(), "", "pod-1", "2026-10-28T20:15:30Z", contracts.ContextModePod)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.RawPurged {
			t.Errorf("expected RawPurged true for empty caseID")
		}
	})

	t.Run("Context for raw purged case", func(t *testing.T) {
		res, err := exp.GetContextLogs(context.Background(), "case-purged", "pod-1", "2026-10-28T20:15:30Z", contracts.ContextModePod)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.RawPurged {
			t.Errorf("expected RawPurged true for purged case, got false")
		}
		if len(res.Events) != 0 {
			t.Errorf("expected 0 events, got %d", len(res.Events))
		}
	})

	t.Run("Context for empty case", func(t *testing.T) {
		res, err := exp.GetContextLogs(context.Background(), "case-empty", "pod-1", "2026-10-28T20:15:30Z", contracts.ContextModePod)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.RawPurged {
			t.Errorf("expected RawPurged true for empty events_raw, got false")
		}
	})

	t.Run("Context for case with events pod mode", func(t *testing.T) {
		res, err := exp.GetContextLogs(context.Background(), "case-1", "chat-com-1", "2026-10-28T20:15:30Z", contracts.ContextModePod)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.RawPurged {
			t.Errorf("expected RawPurged false, got true")
		}
		if len(res.Events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(res.Events))
		}
		if res.Events[0].Service != "chat-com" {
			t.Errorf("expected chat-com, got %s", res.Events[0].Service)
		}
		if res.Mode != contracts.ContextModePod {
			t.Errorf("expected mode pod, got %s", res.Mode)
		}
	})

	t.Run("Context for all services mode", func(t *testing.T) {
		res, err := exp.GetContextLogs(context.Background(), "case-1", "", "2026-10-28 20:15:30", contracts.ContextModeAllServices)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.RawPurged {
			t.Errorf("expected RawPurged false, got true")
		}
		if res.Mode != contracts.ContextModeAllServices {
			t.Errorf("expected mode all_services, got %s", res.Mode)
		}
	})

	t.Run("Context clickhouse error", func(t *testing.T) {
		_, err := exp.GetContextLogs(context.Background(), "case-error", "pod-1", "invalid-ts", contracts.ContextModePod)
		if err == nil {
			t.Fatal("expected error from clickhouse")
		}
	})

	t.Run("Context empty clickhouse URL", func(t *testing.T) {
		expEmpty := NewExplorer("", store, nil)
		_, err := expEmpty.GetContextLogs(context.Background(), "case-1", "pod-1", "2026-10-28 20:15:30", contracts.ContextModePod)
		if err == nil {
			t.Fatal("expected error when clickhouse URL is empty")
		}
	})
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || (len(s) > 0 && len(substr) > 0 && stringContains(s, substr)))
}

func stringContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
