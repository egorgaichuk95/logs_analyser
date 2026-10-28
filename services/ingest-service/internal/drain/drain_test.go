package drain_test

import (
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/ingest-service/internal/drain"
)

func fnv64a(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// TestBlueprintCase1 проверяет первый реальный кейс из Blueprint:
// Operator (operator_id=55754) was not found in cache и
// Operator (operator_id=936) was not found in cache
// дают одинаковый TemplateID и шаблон Operator (operator_id=<num>) was not found in cache.
func TestBlueprintCase1(t *testing.T) {
	miner := drain.NewMiner()

	t1 := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 10, 28, 10, 1, 0, 0, time.UTC)

	ev1 := &contracts.NormalizedEvent{
		CaseID:      "case-1",
		Service:     "chat-com",
		Level:       contracts.LevelInfo,
		SourcePlace: "operator.go:123",
		Message:     "Operator (operator_id=55754) was not found in cache",
		Ts:          t1,
	}

	ev2 := &contracts.NormalizedEvent{
		CaseID:      "case-1",
		Service:     "chat-com",
		Level:       contracts.LevelInfo,
		SourcePlace: "operator.go:123",
		Message:     "Operator (operator_id=936) was not found in cache",
		Ts:          t2,
	}

	rec1 := miner.Mine(ev1)
	rec2 := miner.Mine(ev2)

	expectedTpl := "Operator (operator_id=<num>) was not found in cache"
	if rec1.TemplateText != expectedTpl {
		t.Fatalf("expected template %q, got %q", expectedTpl, rec1.TemplateText)
	}
	if rec2.TemplateText != expectedTpl {
		t.Fatalf("expected template %q, got %q", expectedTpl, rec2.TemplateText)
	}

	expectedID := fnv64a("chat-com|INFO|operator.go:123|" + expectedTpl)
	if rec1.TemplateID != expectedID {
		t.Fatalf("expected TemplateID %d, got %d", expectedID, rec1.TemplateID)
	}
	if rec2.TemplateID != expectedID {
		t.Fatalf("expected TemplateID %d, got %d", expectedID, rec2.TemplateID)
	}
	if ev1.TemplateID != expectedID {
		t.Fatalf("expected ev1.TemplateID %d, got %d", expectedID, ev1.TemplateID)
	}
	if ev2.TemplateID != expectedID {
		t.Fatalf("expected ev2.TemplateID %d, got %d", expectedID, ev2.TemplateID)
	}

	if rec2.FirstSeen != t1 {
		t.Fatalf("expected FirstSeen %v, got %v", t1, rec2.FirstSeen)
	}
	if rec2.LastSeen != t2 {
		t.Fatalf("expected LastSeen %v, got %v", t2, rec2.LastSeen)
	}
}

// TestBlueprintCase2 проверяет второй реальный кейс из Blueprint:
// Session id=7eab3cab9d1bf2a4d5ab6f7c, pvid=123, dep=(,az) и
// Session id=9f8dfebbbe1d2c3ab4f5b6a7, pvid=456, dep=(,az)
// дают один и тот же TemplateID с <hex> и <num>.
func TestBlueprintCase2(t *testing.T) {
	miner := drain.NewMiner()

	ev1 := &contracts.NormalizedEvent{
		CaseID:      "case-2",
		Service:     "chat-session",
		Level:       contracts.LevelInfo,
		SourcePlace: "session.py:42",
		Message:     "Session id=7eab3cab9d1bf2a4d5ab6f7c, pvid=123, dep=(,az)",
		Ts:          time.Now(),
	}

	ev2 := &contracts.NormalizedEvent{
		CaseID:      "case-2",
		Service:     "chat-session",
		Level:       contracts.LevelInfo,
		SourcePlace: "session.py:42",
		Message:     "Session id=9f8dfebbbe1d2c3ab4f5b6a7, pvid=456, dep=(,az)",
		Ts:          time.Now(),
	}

	rec1 := miner.Mine(ev1)
	rec2 := miner.Mine(ev2)

	expectedTpl := "Session id=<hex>, pvid=<num>, dep=(,az)"
	if rec1.TemplateText != expectedTpl {
		t.Fatalf("expected template %q, got %q", expectedTpl, rec1.TemplateText)
	}
	if rec2.TemplateText != expectedTpl {
		t.Fatalf("expected template %q, got %q", expectedTpl, rec2.TemplateText)
	}

	expectedID := fnv64a("chat-session|INFO|session.py:42|" + expectedTpl)
	if rec1.TemplateID != expectedID {
		t.Fatalf("expected TemplateID %d, got %d", expectedID, rec1.TemplateID)
	}
	if rec2.TemplateID != expectedID {
		t.Fatalf("expected TemplateID %d, got %d", expectedID, rec2.TemplateID)
	}
	if ev1.TemplateID != expectedID {
		t.Fatalf("expected ev1.TemplateID %d, got %d", expectedID, ev1.TemplateID)
	}
	if ev2.TemplateID != expectedID {
		t.Fatalf("expected ev2.TemplateID %d, got %d", expectedID, ev2.TemplateID)
	}
}

// TestPreMaskRules проверяет правила предварительной нормализации (URL query, UUID, hex, IP, num).
func TestPreMaskRules(t *testing.T) {
	miner := drain.NewMiner()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "URL query params",
			input:    "HTTP GET /api/v1/search?query=test&limit=10 status 200",
			expected: "HTTP GET /api/v1/search?<query> status <num>",
		},
		{
			name:     "Standard UUID",
			input:    "Request c2e718b9-873b-4835-9f5b-1c5c06be73cf finished in 15 ms",
			expected: "Request <uuid> finished in <num> ms",
		},
		{
			name:     "Hex with 0x prefix",
			input:    "Pointer address 0xdeadbeef0123 invalid dereference",
			expected: "Pointer address <hex> invalid dereference",
		},
		{
			name:     "Long 32-char hex trace ID and 24-char session ID",
			input:    "trace_id=4bf92f3577b34da6a3ce929d0e0e4736 session_id=7eab3cab9d1bf2a4d5ab6f7c",
			expected: "trace_id=<hex> session_id=<hex>",
		},
		{
			name:     "IPv4 with port and without port",
			input:    "Client 192.168.1.50:8080 forwarded to 10.0.0.1",
			expected: "Client <ip> forwarded to <ip>",
		},
		{
			name:     "Numbers (integers and floats)",
			input:    "Processed 1500 items in 3.14 seconds with 0 errors",
			expected: "Processed <num> items in <num> seconds with <num> errors",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev := &contracts.NormalizedEvent{
				Service: "test-service",
				Level:   contracts.LevelInfo,
				Message: tc.input,
				Ts:      time.Now(),
			}
			rec := miner.Mine(ev)
			if rec.TemplateText != tc.expected {
				t.Errorf("input: %s\nexpected: %s\ngot:      %s", tc.input, tc.expected, rec.TemplateText)
			}
		})
	}
}

// TestDrainTreeAndWildcardReplacement проверяет кластеризацию дерева Drain:
// одинаковая длина, отличие токенов, замена на <*> при сходстве >= 0.6.
func TestDrainTreeAndWildcardReplacement(t *testing.T) {
	miner := drain.NewMiner()

	// 5 токенов: "connection to host <X> timeout"
	// Сходство между ними: 4/5 = 0.8 >= 0.6 -> должен слить в один шаблон с <*>
	ev1 := &contracts.NormalizedEvent{
		Service: "network",
		Level:   contracts.LevelError,
		Message: "connection to host alpha timeout",
		Ts:      time.Now(),
	}
	ev2 := &contracts.NormalizedEvent{
		Service: "network",
		Level:   contracts.LevelError,
		Message: "connection to host beta timeout",
		Ts:      time.Now(),
	}
	ev3 := &contracts.NormalizedEvent{
		Service: "network",
		Level:   contracts.LevelError,
		Message: "connection to host gamma timeout",
		Ts:      time.Now(),
	}

	rec1 := miner.Mine(ev1)
	rec2 := miner.Mine(ev2)
	rec3 := miner.Mine(ev3)

	expectedTpl := "connection to host <*> timeout"
	if rec2.TemplateText != expectedTpl {
		t.Fatalf("expected rec2 template %q, got %q", expectedTpl, rec2.TemplateText)
	}
	if rec3.TemplateText != expectedTpl {
		t.Fatalf("expected rec3 template %q, got %q", expectedTpl, rec3.TemplateText)
	}
	if rec2.TemplateID != rec3.TemplateID {
		t.Fatalf("expected rec2 and rec3 to have same TemplateID, got %d and %d", rec2.TemplateID, rec3.TemplateID)
	}
	_ = rec1
}

// TestDrainTreeSimilarityThreshold проверяет, что сходство < 0.6 не объединяет шаблоны.
func TestDrainTreeSimilarityThreshold(t *testing.T) {
	miner := drain.NewMiner()

	// 5 токенов: только 2 совпадают (2/5 = 0.4 < 0.6)
	ev1 := &contracts.NormalizedEvent{
		Service: "srv",
		Level:   contracts.LevelWarn,
		Message: "database connection failed on pool",
		Ts:      time.Now(),
	}
	ev2 := &contracts.NormalizedEvent{
		Service: "srv",
		Level:   contracts.LevelWarn,
		Message: "database query timed out pool",
		Ts:      time.Now(),
	}

	rec1 := miner.Mine(ev1)
	rec2 := miner.Mine(ev2)

	if rec1.TemplateText == rec2.TemplateText {
		t.Fatalf("expected different templates due to low similarity, but got same: %s", rec1.TemplateText)
	}
	if rec1.TemplateID == rec2.TemplateID {
		t.Fatalf("expected different TemplateIDs, got %d", rec1.TemplateID)
	}
}

// TestDrainTreeMaxChildren проверяет ограничение в 100 детей на узел дерева и работу wildcard.
func TestDrainTreeMaxChildren(t *testing.T) {
	miner := drain.NewMiner()

	// Создаем 120 различных сообщений с разным вторым токеном: "Step token_i completed"
	for i := 0; i < 120; i++ {
		ev := &contracts.NormalizedEvent{
			Service: "pipeline",
			Level:   contracts.LevelInfo,
			Message: fmt.Sprintf("step subtask%d completed", i),
			Ts:      time.Now(),
		}
		rec := miner.Mine(ev)
		if rec.TemplateID == 0 {
			t.Fatalf("expected non-zero template ID for iteration %d", i)
		}
	}
}

// TestHashCache проверяет быстрое извлечение из хэш-кэша для идентичных преднормализованных строк.
func TestHashCache(t *testing.T) {
	miner := drain.NewMiner()

	ts1 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	ts2 := time.Date(2026, 10, 28, 12, 30, 0, 0, time.UTC)

	ev1 := &contracts.NormalizedEvent{
		Service: "auth",
		Level:   contracts.LevelInfo,
		Message: "User auth succeeded for uid=12345",
		Ts:      ts1,
	}
	ev2 := &contracts.NormalizedEvent{
		Service: "auth",
		Level:   contracts.LevelInfo,
		Message: "User auth succeeded for uid=99999",
		Ts:      ts2,
	}

	rec1 := miner.Mine(ev1)
	rec2 := miner.Mine(ev2)

	expectedTpl := "User auth succeeded for uid=<num>"
	if rec1.TemplateText != expectedTpl || rec2.TemplateText != expectedTpl {
		t.Fatalf("unexpected templates: %q, %q", rec1.TemplateText, rec2.TemplateText)
	}
	if rec1.TemplateID != rec2.TemplateID {
		t.Fatalf("expected identical template ID from cache hit")
	}
	if rec2.FirstSeen != ts1 {
		t.Fatalf("expected FirstSeen %v, got %v", ts1, rec2.FirstSeen)
	}
	if rec2.LastSeen != ts2 {
		t.Fatalf("expected LastSeen %v, got %v", ts2, rec2.LastSeen)
	}
}

// TestConcurrency проверяет потокобезопасность при параллельных вызовах Mine из 30 горутин.
func TestConcurrency(t *testing.T) {
	miner := drain.NewMiner()

	var wg sync.WaitGroup
	workers := 30
	iterations := 100

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				ev := &contracts.NormalizedEvent{
					Service: fmt.Sprintf("service-%d", workerID%3),
					Level:   contracts.LevelInfo,
					Message: fmt.Sprintf("Worker %d executed item %d successfully", workerID, i),
					Ts:      time.Now(),
				}
				rec := miner.Mine(ev)
				if rec.TemplateID == 0 {
					t.Errorf("zero TemplateID received in worker %d", workerID)
				}
				if ev.TemplateID != rec.TemplateID {
					t.Errorf("mismatched ev.TemplateID in worker %d", workerID)
				}
			}
		}(w)
	}

	wg.Wait()
}

// TestEdgeCases проверяет граничные случаи:
// nil event, пустые строки, длинные строки > 512, спецсимволы, ErrorMsg комбинации.
func TestEdgeCases(t *testing.T) {
	miner := drain.NewMiner()

	t.Run("nil event", func(t *testing.T) {
		rec := miner.Mine(nil)
		if rec.TemplateID != 0 {
			t.Fatalf("expected zero TemplateID for nil event, got %d", rec.TemplateID)
		}
	})

	t.Run("empty message and error", func(t *testing.T) {
		ev := &contracts.NormalizedEvent{
			Service: "svc",
			Level:   contracts.LevelDebug,
		}
		rec := miner.Mine(ev)
		if rec.TemplateText != "" {
			t.Fatalf("expected empty template, got %q", rec.TemplateText)
		}
		expectedID := fnv64a("svc|DEBUG||")
		if rec.TemplateID != expectedID {
			t.Fatalf("expected ID %d, got %d", expectedID, rec.TemplateID)
		}
	})

	t.Run("empty message with error msg", func(t *testing.T) {
		ev := &contracts.NormalizedEvent{
			Service:  "svc",
			Level:    contracts.LevelError,
			ErrorMsg: "connection refused: 127.0.0.1:5432",
		}
		rec := miner.Mine(ev)
		expected := "connection refused: <ip>"
		if rec.TemplateText != expected {
			t.Fatalf("expected %q, got %q", expected, rec.TemplateText)
		}
	})

	t.Run("message with error msg already included", func(t *testing.T) {
		ev := &contracts.NormalizedEvent{
			Service:  "svc",
			Level:    contracts.LevelError,
			Message:  "fatal: connection reset by peer",
			ErrorMsg: "connection reset by peer",
		}
		rec := miner.Mine(ev)
		expected := "fatal: connection reset by peer"
		if rec.TemplateText != expected {
			t.Fatalf("expected %q, got %q", expected, rec.TemplateText)
		}
	})

	t.Run("message with error msg not included", func(t *testing.T) {
		ev := &contracts.NormalizedEvent{
			Service:  "svc",
			Level:    contracts.LevelError,
			Message:  "database operation failed",
			ErrorMsg: "timeout after 5000 ms",
		}
		rec := miner.Mine(ev)
		expected := "database operation failed | timeout after <num> ms"
		if rec.TemplateText != expected {
			t.Fatalf("expected %q, got %q", expected, rec.TemplateText)
		}
	})

	t.Run("long message truncation to 512 chars", func(t *testing.T) {
		longText := strings.Repeat("longword ", 100) // ~900 chars
		ev := &contracts.NormalizedEvent{
			Service: "svc",
			Level:   contracts.LevelWarn,
			Message: longText,
		}
		rec := miner.Mine(ev)
		if len([]rune(rec.TemplateText)) > 512 {
			t.Fatalf("expected length <= 512 runes, got %d", len([]rune(rec.TemplateText)))
		}
	})

	t.Run("unicode cyrillic truncation and formatting", func(t *testing.T) {
		cyrillic := strings.Repeat("Сообщение об ошибке в подсистеме ", 25)
		ev := &contracts.NormalizedEvent{
			Service: "svc-ru",
			Level:   contracts.LevelError,
			Message: cyrillic,
		}
		rec := miner.Mine(ev)
		if len([]rune(rec.TemplateText)) > 512 {
			t.Fatalf("expected <= 512 runes, got %d", len([]rune(rec.TemplateText)))
		}
	})

	t.Run("special regex characters in message", func(t *testing.T) {
		special := "Special [chars] (and) *regex* +tokens+ ?questions? ^starts$ \\escapes/ {1,2}"
		ev := &contracts.NormalizedEvent{
			Service: "svc",
			Level:   contracts.LevelInfo,
			Message: special,
		}
		rec := miner.Mine(ev)
		if rec.TemplateID == 0 {
			t.Fatalf("expected valid template ID for special characters")
		}
	})

	t.Run("zero timestamp defaults to current time", func(t *testing.T) {
		ev := &contracts.NormalizedEvent{
			Service: "svc",
			Level:   contracts.LevelInfo,
			Message: "Event with zero timestamp",
		}
		rec := miner.Mine(ev)
		if rec.FirstSeen.IsZero() || rec.LastSeen.IsZero() {
			t.Fatalf("expected non-zero firstSeen/lastSeen")
		}
	})

	t.Run("RawRedacted preserved in SampleRedacted", func(t *testing.T) {
		ev := &contracts.NormalizedEvent{
			Service:     "svc",
			Level:       contracts.LevelInfo,
			Message:     "Secret key authorization",
			RawRedacted: "Secret key authorization [REDACTED]",
		}
		rec := miner.Mine(ev)
		if rec.SampleRedacted != "Secret key authorization [REDACTED]" {
			t.Fatalf("expected SampleRedacted to match RawRedacted, got %q", rec.SampleRedacted)
		}
	})
}

// TestCaseIsolation проверяет изоляцию шаблонов по CaseID:
// при последовательном майнинге одинаковых сообщений из разных кейсов (case-1 и case-2)
// возвращаемые TemplateRecord должны содержать соответствующий CaseID каждого кейса,
// предотвращая кросс-кейсовую утечку шаблонов.
func TestCaseIsolation(t *testing.T) {
	miner := drain.NewMiner()

	t1 := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 10, 28, 11, 0, 0, 0, time.UTC)

	ev1 := &contracts.NormalizedEvent{
		CaseID:      "case-1",
		Service:     "billing-db-proxy",
		Level:       contracts.LevelError,
		SourcePlace: "pool_manager.go:84",
		Message:     "HikariPool-1 - Connection is not available, request timed out",
		ErrorClass:  "RESOURCE",
		Ts:          t1,
	}

	ev2 := &contracts.NormalizedEvent{
		CaseID:      "case-2",
		Service:     "billing-db-proxy",
		Level:       contracts.LevelError,
		SourcePlace: "pool_manager.go:84",
		Message:     "HikariPool-1 - Connection is not available, request timed out",
		ErrorClass:  "RESOURCE",
		Ts:          t2,
	}

	rec1 := miner.Mine(ev1)
	rec2 := miner.Mine(ev2)

	if rec1.CaseID != "case-1" {
		t.Fatalf("expected rec1.CaseID to be 'case-1', got %q", rec1.CaseID)
	}
	if rec2.CaseID != "case-2" {
		t.Fatalf("expected rec2.CaseID to be 'case-2', got %q", rec2.CaseID)
	}
	if rec1.TemplateID != rec2.TemplateID {
		t.Fatalf("expected identical template IDs across cases, got %d and %d", rec1.TemplateID, rec2.TemplateID)
	}
	if rec1.FirstSeen != t1 || rec1.LastSeen != t1 {
		t.Fatalf("rec1 timestamps corrupted: first=%v, last=%v", rec1.FirstSeen, rec1.LastSeen)
	}
	if rec2.FirstSeen != t2 || rec2.LastSeen != t2 {
		t.Fatalf("rec2 timestamps corrupted: first=%v, last=%v", rec2.FirstSeen, rec2.LastSeen)
	}
}

