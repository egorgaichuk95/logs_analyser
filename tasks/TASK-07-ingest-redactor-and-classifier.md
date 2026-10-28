# TASK-07: Маскировщик секретов и классификатор ошибок/связей (`./services/ingest-service/internal/classifier`)

- **Статус:** `READY_FOR_REVIEW`
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

- **Дата выполнения:** 2026-10-28 19:47 UTC
- **Соблюдение цикла TDD (Contract → Tests → Code):**
  1. **CONTRACT Phase:** Зафиксированы точные сигнатуры функций `RedactSecrets(input string) string` и `EnrichAndClassify(ev *contracts.NormalizedEvent, parsedJSON map[string]any, rawLine string) ([]contracts.IdentityEdge, *contracts.ServiceEdge)` строго по Blueprint.
  2. **RED Phase (Tests First):** Написан всесторонний набор тестов в `classifier_test.go` ДО реализации бизнес-логики:
     - Все 3 контрактных сценария из Blueprint (маскирование JWT в URL с context canceled и PeerService chat-com; Traceparent и X-Request-Id в http.request.headers с генерацией IdentityEdge; traceback chat-com с извлечением последнего прикладного фрейма request_processing.py:173 и AttributeError).
     - Все классы ошибок (`CANCELED`, `OWN_DEFECT`, `RESOURCE`, `DEADLINE`, `UNAVAILABLE`, `PEER_5XX`, `PEER_4XX`, `NONE`).
     - Проверка строгого порядка приоритетов классификации ошибок.
     - Граничные случаи (nil parsedJSON, пустые строки, отсутствие идентификаторов, комбинации 1/2/3/4 идентификаторов, Bearer токены, access_token).
     - Первоначальный запуск тестов подтвердил фазу RED (компиляция и падение тестов).
  3. **GREEN Phase (Implementation):** Реализована 100% честная бизнес-логика в `redactor.go` и `classifier.go` без заглушек, без `// TODO`, без подгонки.
  4. **Coverage & Verification Gate:** Тесты полностью прошли (`PASS`), достигнуто покрытие statement coverage `96.2%` (порог `>= 80.0%`). Автоматический верификатор `py ./verify_task.py TASK-07` вернул `[VERIFY PASSED]`.
- **Созданные/изменённые файлы:**
  - `services/ingest-service/internal/classifier/redactor.go` (41 строк)
  - `services/ingest-service/internal/classifier/classifier.go` (526 строк)
  - `services/ingest-service/internal/classifier/classifier_test.go` (968 строк)
- **Результаты проверок и тестов:**
  - `go test -v -cover ./internal/classifier/...`: `PASS`, покрытие `96.2% of statements` (требовалось `>= 80.0%`).
  - `py ./verify_task.py TASK-07`: `[VERIFY PASSED] TASK-07 passed all automated checks!`.
- **Заметки по реализации:**
  - `RedactSecrets` последовательно обрабатывает JWT-токены (`eyJ...`) и параметры авторизации (`Bearer`, `auth-token`, `access_token`, `token`), заменяя их на детерминированный 8-символьный хэш `<REDACTED_JWT:hash8>` и `<REDACTED_TOKEN:hash8>`.
  - Извлечение `TraceID` и `SpanID` поддерживает поля JSON и заголовок `Traceparent` (в формате `00-<32hex>-<16hex>-<2hex>`), включая форматы среза строк и одиночной строки.
  - Извлечение `RequestID` поддерживает `id` (UUID), `request_id` и заголовок `X-Request-Id`.
  - Извлечение `VisitorID` поддерживает числовые форматы (float64, int, int64 в `parsedJSON["ID"]`), строковые значения и regex по сырой строке.
  - Извлечение `SessionID` и `OperatorID` поддерживает regex и JSON-поля.
  - Извлечение `PeerService` выделяет имя сервиса из FQDN `...svc.cluster.local` и формирует ориентированное ребро `contracts.ServiceEdge` с `EvidenceSource="peer_url"` и `ErrorCount` в зависимости от наличия ошибок (исключая вызовы сервиса к самому себе).
  - Генерация `contracts.IdentityEdge` строит рёбра связности между парами идентификаторов `(request_id, trace_id, session_id, visitor_id)` при наличии $\ge 2$ идентификаторов.
  - Классификация ошибок строго соблюдает порядок приоритетов: `CANCELED` > `OWN_DEFECT` > `RESOURCE` > `DEADLINE` > `UNAVAILABLE` > `PEER_5XX` > `PEER_4XX` > `OWN_DEFECT` (default) / `NONE`.
  - При наличии трассировки стека извлекается последний фрейм прикладного кода (с фильтрацией `site-packages` и `/opt/pypy/`), формируя `SourcePlace` в формате `file:line`.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты (TDD Coverage):**
  - **Файлы реализации:**
    - `services/ingest-service/internal/classifier/redactor.go`
    - `services/ingest-service/internal/classifier/classifier.go`
    - `services/ingest-service/internal/classifier/classifier_test.go`
  - **Аудит контрактов и Blueprint:**
    - Сигнатуры `RedactSecrets(input string) string` и `EnrichAndClassify(ev *contracts.NormalizedEvent, parsedJSON map[string]any, rawLine string) ([]contracts.IdentityEdge, *contracts.ServiceEdge)` строго соответствуют спецификации задачи и `pkg/contracts`.
    - Все поля `NormalizedEvent`, `IdentityEdge`, `ServiceEdge` и константы `contracts.ErrClass*` заполняются по стандарту модели данных.
  - **Полнота тестов и граничные сценарии:**
    - Проверены 3 обязательных контракта из Blueprint: (1) JWT маскирование + context canceled + chat-com ServiceEdge; (2) Traceparent/X-Request-Id + IdentityEdge; (3) traceback chat-com с извлечением SourcePlace (`request_processing.py:173`) и фильтрацией фреймов `site-packages`/`/opt/pypy/`.
    - Проверены все 8 семантических классов ошибок (`CANCELED`, `OWN_DEFECT`, `RESOURCE`, `DEADLINE`, `UNAVAILABLE`, `PEER_5XX`, `PEER_4XX`, `NONE`), их граничные условия и строгий приоритет разрешения конфликтов.
    - Проверены детерминированность хэширования в `RedactSecrets`, сохранение FQDN k8s, исключение self-call для `ServiceEdge`, комбинации идентификаторов для `IdentityEdge`.
  - **Автоматическая верификация и покрытие:**
    - Команда `py ./verify_task.py TASK-07` выполнена лично: `[VERIFY PASSED] TASK-07 passed all automated checks!`.
    - Покрытие пакета `services/ingest-service/internal/classifier`: **96.2% of statements** (порог `>= 80.0%` перевыполнен).
  - **Чек-лист качества:** Заглушек (`TODO`, `FIXME`, пустых функций) нет, утечек памяти и `os.ReadFile` нет, внешних сетевых вызовов нет.
- **Найденные замечания:** Замечаний нет. Код и тесты выполнены на высоком профессиональном уровне.
