# TASK-03: Контракты событий и графов (`./pkg/contracts/events.go`)

- **Статус:** `TODO`
- **Зависимости:** `TASK-01`
- **Область видимости (Scope):**
  - Создавать/редактировать:
    - `./pkg/contracts/events.go`
    - `./pkg/contracts/events_test.go`
  - **НЕ трогать:** другие папки

---

## 1. Цель задачи

Создать первую часть общего пакета `github.com/local/logs-analyzer/pkg/contracts` — константы уровней, типов схем, семантических классов ошибок и структуры `RawScriptRecord`, `NormalizedEvent`, `IdentityEdge`, `ServiceEdge`, `TemplateRecord`.

---

## 2. Посимвольный чертёж кода (Blueprint)

### 2.1. `./pkg/contracts/events.go`
```go
package contracts

import (
	"math"
	"time"
)

const (
	LevelError = "ERROR"
	LevelWarn  = "WARN"
	LevelInfo  = "INFO"
	LevelDebug = "DEBUG"

	SeverityError uint8 = 40
	SeverityWarn  uint8 = 30
	SeverityInfo  uint8 = 20
	SeverityDebug uint8 = 10
)

const (
	SchemaGoSlog      = "go_slog"
	SchemaPyTornado   = "py_tornado"
	SchemaEchoAccess  = "echo_access"
	SchemaOTelHTTP    = "otel_http"
	SchemaGenericJSON = "generic_json"
	SchemaPlainText   = "plain"
)

const (
	ErrClassNone        = "NONE"
	ErrClassCanceled    = "CANCELED"    // context canceled — указывает вверх на вызывающего
	ErrClassDeadline    = "DEADLINE"    // deadline exceeded / timeout — указывает вниз на peer
	ErrClassUnavailable = "UNAVAILABLE" // connection refused / reset / EOF — указывает вниз на peer
	ErrClassPeer5xx     = "PEER_5XX"    // status 500/502/503/504 от peer — указывает вниз
	ErrClassPeer4xx     = "PEER_4XX"    // status 400/404 / not found — бизнес-ошибка (низкий вес)
	ErrClassOwnDefect   = "OWN_DEFECT"  // AttributeError / panic / NPE — дефект кода внутри сервиса
	ErrClassResource    = "RESOURCE"    // OOM / pool exhausted / disk full — ресурс внутри сервиса
)

// RawScriptRecord — входная запись из JSON-файлов скрипта (*_part-XXXX.json)
type RawScriptRecord struct {
	Timestamp string            `json:"timestamp"`
	Date      string            `json:"date"`
	Line      string            `json:"line"`
	Fields    map[string]string `json:"fields"`
}

// NormalizedEvent — единая модель нормализованного события (соответствует logs_rca.events_raw)
type NormalizedEvent struct {
	CaseID           string     `json:"case_id"`
	EventID          string     `json:"event_id"`
	Ts               time.Time  `json:"ts"`
	AppTs            *time.Time `json:"app_ts,omitempty"`
	ClockSkewMs      int64      `json:"clock_skew_ms"`
	Service          string     `json:"service"`
	Environment      string     `json:"environment"`
	Pod              string     `json:"pod"`
	Node             string     `json:"node"`
	ServiceVersion   string     `json:"service_version"`
	Level            string     `json:"level"`
	SeverityNum      uint8      `json:"severity_num"`
	SchemaKind       string     `json:"schema_kind"`
	TemplateID       uint64     `json:"template_id"`
	Message          string     `json:"message"`
	ErrorClass       string     `json:"error_class"`
	ErrorMsg         string     `json:"error_msg"`
	ExceptionType    string     `json:"exception_type"`
	SourcePlace      string     `json:"source_place"`
	StackFingerprint uint64     `json:"stack_fingerprint"`
	TraceID          string     `json:"trace_id"`
	SpanID           string     `json:"span_id"`
	RequestID        string     `json:"request_id"`
	SessionID        string     `json:"session_id"`
	OperatorID       string     `json:"operator_id"`
	VisitorID        string     `json:"visitor_id"`
	HTTPMethod       string     `json:"http_method"`
	HTTPRoute        string     `json:"http_route"`
	HTTPStatus       uint16     `json:"http_status"`
	LatencyNs        uint64     `json:"latency_ns"`
	PeerService      string     `json:"peer_service"`
	SourceFile       string     `json:"source_file"`
	SourceRecordIdx  uint32     `json:"source_record_idx"`
	RawHash          uint64     `json:"raw_hash"`
	RawRedacted      string     `json:"raw_redacted"`
}

// IdentityEdge — связь между двумя идентификаторами (logs_rca.identity_edges)
type IdentityEdge struct {
	CaseID    string    `json:"case_id"`
	Ts        time.Time `json:"ts"`
	Service   string    `json:"service"`
	LeftType  string    `json:"left_type"`  // request_id, trace_id, session_id, visitor_id
	LeftVal   string    `json:"left_val"`
	RightType string    `json:"right_type"`
	RightVal  string    `json:"right_val"`
}

// ServiceEdge — ориентированное ребро вызова Caller -> Callee (logs_rca.service_edges)
type ServiceEdge struct {
	CaseID         string `json:"case_id"`
	CallerService  string `json:"caller_service"`
	CalleeService  string `json:"callee_service"`
	EvidenceSource string `json:"evidence_source"` // peer_url, trace_propagation, session_bridge, static_config
	CallCount      uint64 `json:"call_count"`
	ErrorCount     uint64 `json:"error_count"`
}

// TemplateRecord — запись справочника шаблонов (logs_rca.templates_dict)
type TemplateRecord struct {
	CaseID         string    `json:"case_id"`
	Service        string    `json:"service"`
	TemplateID     uint64    `json:"template_id"`
	TemplateText   string    `json:"template_text"`
	Level          string    `json:"level"`
	ErrorClass     string    `json:"error_class"`
	SourcePlace    string    `json:"source_place"`
	SampleRedacted string    `json:"sample_redacted"`
	FirstSeen      time.Time `json:"first_seen"`
	LastSeen       time.Time `json:"last_seen"`
}

// SanitizeFloat защищает JSON-сериализацию от NaN и Inf во всех математических расчётах
func SanitizeFloat(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0.0
	}
	return v
}

// SeverityFromLevel возвращает числовой код уровня
func SeverityFromLevel(lvl string) uint8 {
	switch lvl {
	case LevelError:
		return SeverityError
	case LevelWarn:
		return SeverityWarn
	case LevelInfo:
		return SeverityInfo
	default:
		return SeverityDebug
	}
}
```

### 2.2. `./pkg/contracts/events_test.go`
Написать unit-тест, проверяющий:
- JSON-сериализацию `RawScriptRecord` и `NormalizedEvent`;
- что `SanitizeFloat(math.NaN()) == 0.0` и `SanitizeFloat(math.Inf(1)) == 0.0`;
- что `SeverityFromLevel(LevelError) == 40`.

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}:/workspace" -v rca_go_cache:/go/pkg/mod -v rca_go_build:/root/.cache/go-build -w /workspace/pkg/contracts golang:1.23-alpine go test -v -cover ./...
py ./verify_task.py TASK-03
```
Все тесты должны завершиться со статусом `PASS`.

---

## Отчёт о выполнении (заполняется субагентом-воркером)

- **Дата выполнения:** *(не заполнено)*
- **Созданные/изменённые файлы:** *(не заполнено)*
- **Результаты проверок и тестов:** *(не заполнено)*
- **Заметки по реализации:** *(не заполнено)*

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `PENDING` *(выбрать: `APPROVED` | `CHANGES_REQUESTED`)*
- **Проверенные контракты и тесты:** *(не заполнено)*
- **Найденные замечания (если есть):** *(не заполнено)*
