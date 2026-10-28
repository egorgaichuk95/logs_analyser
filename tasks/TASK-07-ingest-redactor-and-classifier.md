# TASK-07: Маскировщик секретов и классификатор ошибок/связей (`./services/ingest-service/internal/classifier`)

- **Статус:** `TODO`
- **Зависимости:** `TASK-06`
- **Область видимости (Scope):**
  - Читать: `./pkg/contracts/`, `./services/ingest-service/internal/normalizer/`
  - Создавать/редактировать:
    - `./services/ingest-service/internal/classifier/redactor.go`
    - `./services/ingest-service/internal/classifier/classifier.go`
    - `./services/ingest-service/internal/classifier/classifier_test.go`
  - **НЕ трогать:** другие папки

---

## 1. Цель задачи

Реализовать в пакете `classifier`:
1. `RedactSecrets(raw string) string` — обязательное маскирование JWT (`eyJ...`), `Bearer` и `auth-token` с сохранением 8-символьного хэша `<REDACTED_JWT:a1b2c3d4>`.
2. `EnrichAndClassify(ev *contracts.NormalizedEvent, parsedJSON map[string]any, rawLine string) ([]contracts.IdentityEdge, *contracts.ServiceEdge)` — определение семантического класса ошибки `ErrorClass` (`CANCELED`, `DEADLINE`, `UNAVAILABLE`, `PEER_5XX`, `PEER_4XX`, `OWN_DEFECT`, `RESOURCE`), извлечение `TraceID`, `SpanID`, `RequestID`, `SessionID`, `OperatorID`, `VisitorID`, `PeerService` и генерация рёбер `IdentityEdge` и `ServiceEdge`.

---

## 2. Точные сигнатуры и регулярные выражения (Blueprint)

### 2.1. `./services/ingest-service/internal/classifier/redactor.go`
```go
package classifier

import (
	"fmt"
	"hash/fnv"
	"regexp"
)

var (
	jwtRe       = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)
	authParamRe = regexp.MustCompile(`(?i)(auth-token|access_token|token|bearer)(%20|=|\s+)([A-Za-z0-9._~+/-]{20,})`)
)

func shortHash8(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%08x", uint32(h.Sum64()))
}

// RedactSecrets заменяет JWT и Bearer/auth-token на <REDACTED_JWT:hash8> / <REDACTED_TOKEN:hash8>
func RedactSecrets(input string) string
```

### 2.2. `./services/ingest-service/internal/classifier/classifier.go`
```go
package classifier

import (
	"github.com/local/logs-analyzer/pkg/contracts"
)

// EnrichAndClassify заполняет в ev поля ErrorClass, ExceptionType, SourcePlace (из traceback),
// TraceID, SpanID, RequestID, SessionID, OperatorID, VisitorID, PeerService, RawHash, RawRedacted
// и возвращает найденные рёбра IdentityEdge и ServiceEdge.
func EnrichAndClassify(
	ev *contracts.NormalizedEvent,
	parsedJSON map[string]any,
	rawLine string,
) ([]contracts.IdentityEdge, *contracts.ServiceEdge)
```

**Точные регулярные выражения и правила для `EnrichAndClassify`:**
1. **Извлечение ID:**
   - `TraceID`: из `parsedJSON["trace_id"]` или из заголовка `parsedJSON["http.request.headers"]["Traceparent"]` (регулярка `00-([0-9a-f]{32})-([0-9a-f]{16})-[0-9a-f]{2}` → группа 1 в `TraceID`, группа 2 в `SpanID`).
   - `RequestID`: из `parsedJSON["id"]` (строка UUID) или из `parsedJSON["http.request.headers"]["X-Request-Id"]` (первый элемент массива или строка).
   - `SessionID`: regex по `rawLine`: `(?:visit-session-id|visit_session_id|session_id|Session id|request session)[=:\s]+([0-9a-fA-F]{24,36})`.
   - `OperatorID`: regex `operator_id[=:\s]+(\d+)`.
   - `VisitorID`: из `parsedJSON["ID"]` (в `chat-session` это число `34219180`) или regex `(?:visitor_id|provided_visitor_id)[=:\s]+([0-9a-fA-F]{8,36}|\d+)`.
2. **Генерация `[]contracts.IdentityEdge`:**
   - Собрать все непустые пары `(type, val)` у текущего события из списка: `("request_id", ev.RequestID)`, `("trace_id", ev.TraceID)`, `("session_id", ev.SessionID)`, `("visitor_id", ev.VisitorID)`.
   - Если непустых идентификаторов $\ge 2$, соединить первый со всеми остальными в `[]contracts.IdentityEdge`.
3. **Извлечение `PeerService` и генерация `*contracts.ServiceEdge`:**
   - Regex по `rawLine`: `([a-z0-9-]+)\.[a-z0-9-]+\.svc\.cluster\.local` → группа 1 записывается в `ev.PeerService` (например, `chat-com`).
   - Если `ev.PeerService != ""` и `ev.PeerService != ev.Service`, вернуть `&contracts.ServiceEdge{CaseID: ev.CaseID, CallerService: ev.Service, CalleeService: ev.PeerService, EvidenceSource: "peer_url", CallCount: 1, ErrorCount: isErr}`.
4. **Классификация `ev.ErrorClass` (проверять `combined = ev.Message + " " + ev.ErrorMsg + " " + traceStr`):**
   - Если `ev.Level != contracts.LevelError && ev.Level != contracts.LevelWarn && ev.HTTPStatus < 400 && ev.ErrorMsg == ""` → `contracts.ErrClassNone`.
   - Иначе проверять в точном порядке приоритета:
     1. Содержит `context canceled` или `client disconnected` → `contracts.ErrClassCanceled`.
     2. Содержит `Traceback (most recent call last)` или `AttributeError` или `TypeError` или `KeyError` или `ValueError` или `NullPointerException` или `panic:` или `runtime error:` → `contracts.ErrClassOwnDefect`.
        - Извлечь `ExceptionType` регуляркой `([A-Za-z]+Error|[A-Za-z]+Exception|panic)`.
        - Если в `parsedJSON["trace"]` есть строки `File '...', line (\d+)`, взять последний фрейм прикладного кода (не из `site-packages`/`/opt/pypy/`) и обновить `ev.SourcePlace = filepath.Base(file) + ":" + line` (например, `"request_processing.py:173"`).
     3. Содержит `OutOfMemory` или `OOM` или `Connection is not available` или `pool exhausted` или `too many connections` или `disk full` → `contracts.ErrClassResource`.
     4. Содержит `context deadline exceeded` или `timed out` или `timeout` или `i/o timeout` → `contracts.ErrClassDeadline`.
     5. Содержит `connection refused` или `connection reset` или `no such host` или `broken pipe` или `unexpected EOF` → `contracts.ErrClassUnavailable`.
     6. Содержит `status 500` или `status 502` или `status 503` или `status 504` или `ev.HTTPStatus >= 500` → `contracts.ErrClassPeer5xx`.
     7. Содержит `status 400` или `status 404` или `not found` или `provided hash is empty` или `ev.HTTPStatus >= 400` → `contracts.ErrClassPeer4xx`.
     8. По умолчанию для остальных ошибок → `contracts.ErrClassOwnDefect`.

### 2.3. `./services/ingest-service/internal/classifier/classifier_test.go`
Написать тесты на 3 реальных строках из `chat-session` и `chat-com`:
1. Строка с `Get "http://chat-com.chat.svc.cluster.local:8260/l/v/init?action=init&auth-token=eyJhbGciOiJSUzI1NiIsInR5cCIgOiAiSldUIiwia2lkIiA6ICJXdVVwd2M3...&visit-session-id=18cb9e028be84b0d8378e56447909e12": context canceled`:
   - `assert !strings.Contains(ev.RawRedacted, "eyJhbGci")`
   - `assert ev.ErrorClass == contracts.ErrClassCanceled`
   - `assert ev.PeerService == "chat-com"`
   - `assert ev.SessionID == "18cb9e028be84b0d8378e56447909e12"`
   - `assert serviceEdge != nil && serviceEdge.CalleeService == "chat-com"`
2. Строка интерцептора с `"http.request.headers":{"Traceparent":["00-169fef323de3db49a3643f9a10a558aa-f34c60ee2a4c87e0-01"],"X-Request-Id":["8a52fbd7-c4e4-9b46-a8a8-45b29ee6dce2"]}`:
   - `assert ev.TraceID == "169fef323de3db49a3643f9a10a558aa"`
   - `assert ev.RequestID == "8a52fbd7-c4e4-9b46-a8a8-45b29ee6dce2"`
   - `assert len(edges) >= 1`
3. Строка `chat-com` с `Traceback` и `File '/var/www/supportchat/python/source/supportchat/operator/request_processing.py', line 173, in on_message` и `AttributeError`:
   - `assert ev.ErrorClass == contracts.ErrClassOwnDefect`
   - `assert ev.ExceptionType == "AttributeError"`
   - `assert ev.SourcePlace == "request_processing.py:173"`

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}:/workspace" -v rca_go_cache:/go/pkg/mod -v rca_go_build:/root/.cache/go-build -w /workspace/services/ingest-service golang:1.23-alpine go test -v -cover ./internal/classifier/...
py ./verify_task.py TASK-07
```

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
