# TASK-06: Автоопределение 6 схем, честный `level` и `clock_skew_ms` (`./services/ingest-service/internal/normalizer`)

- **Статус:** `TODO`
- **Зависимости:** `TASK-05`
- **Область видимости (Scope):**
  - Читать: `./pkg/contracts/`
  - Создавать/редактировать:
    - `./services/ingest-service/internal/normalizer/normalizer.go`
    - `./services/ingest-service/internal/normalizer/normalizer_test.go`
  - **НЕ трогать:** другие папки

---

## 1. Цель задачи

Реализовать пакет `normalizer`, который преобразует `contracts.RawScriptRecord` в базовый `contracts.NormalizedEvent` + распарсенную карту JSON-полей `map[string]any`:
1. Определяет `SchemaKind` (`go_slog`, `py_tornado`, `echo_access`, `otel_http`, `generic_json`, `plain`).
2. **Исправляет критический баг Loki `detected_level="error"`:** в `chat-session` 3166 записей HTTP access-логов со статусами `200`/`204` имеют `"error":""` внутри JSON, из-за чего Loki пометил их `detected_level="error"`. Нормализатор обязан поставить им `Level = "INFO"`!
3. **Вычисляет сдвиг часов `ClockSkewMs`:** в `chat-com` поле `"time": "260930 08:47:50 +0000"` опережает наносекундный `timestamp` Loki (`06:47:50Z`) ровно на `+7200000 мс` (+2 часа).

---

## 2. Точные сигнатуры и правила (Blueprint)

### 2.1. `./services/ingest-service/internal/normalizer/normalizer.go`
```go
package normalizer

import (
	"github.com/local/logs-analyzer/pkg/contracts"
)

// NormalizeRecord парсит сырую запись и возвращает нормализованное событие и внутреннюю JSON-карту (или nil для plain text).
func NormalizeRecord(caseID string, rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) (contracts.NormalizedEvent, map[string]any)
```

**Точные правила внутри `NormalizeRecord`:**
1. **Парсинг времени Loki (`Ts`):**
   - `ns, err := strconv.ParseInt(rec.Timestamp, 10, 64)` → `Ts = time.Unix(0, ns).UTC()`. Если `Timestamp` пуст — парсить `rec.Date` как `time.RFC3339Nano`.
2. **Извлечение label-полей (`rec.Fields`):**
   - `Service`: первый непустой из `fields["service_name"]`, `fields["container"]`, `fields["app"]`, иначе `"unknown"`.
   - `Environment`: `fields["deployment_environment"]` → `fields["environment"]`.
   - `Pod`: `fields["pod"]`, `Node`: `fields["node"]`, `ServiceVersion`: `fields["service_version"]`.
3. **Парсинг `rec.Line` как JSON (`map[string]any`):**
   - Если `json.Unmarshal([]byte(rec.Line), &obj)` успешен:
     - Очистить ключи от пробелов и двоеточий на концах (например, `"license: "` → `"license"`, `"status:"` → `"status"`, `"is vip:"` → `"is_vip"`), сохранив также оригинальные ключи.
     - **Определение `SchemaKind`:**
       1. Если есть ключ `"source"` (объект `map[string]any` с `"file"` / `"line"`): `SchemaKind = contracts.SchemaGoSlog`. `SourcePlace = filepath.Base(file) + ":" + fmt.Sprint(line)` (например, `"client.go:173"`).
       2. Иначе если есть `"type" == "app"` и `"line"` (строка вида `"cache:98"`): `SchemaKind = contracts.SchemaPyTornado`. `SourcePlace = str(obj["line"])`.
       3. Иначе если есть `"method"`, `"uri"`, `"status"`, `"latency"`: `SchemaKind = contracts.SchemaEchoAccess`. Заполнить `HTTPMethod`, `HTTPRoute` (путь из `uri` до `?`, с заменой UUID и чисел в сегментах пути на `<id>`), `HTTPStatus`, `LatencyNs = uint64(latency)`.
       4. Иначе если есть `"http.request.method"` или `"http.request.url"`: `SchemaKind = contracts.SchemaOTelHTTP`. Заполнить `HTTPMethod`, `HTTPRoute`, `LatencyNs` из `"http.duration"`.
       5. Иначе: `SchemaKind = contracts.SchemaGenericJSON`.
   - Если не JSON: `SchemaKind = contracts.SchemaPlainText`, `Message = rec.Line`.
4. **Честное определение `Level` и `SeverityNum`:**
   - При `SchemaEchoAccess`:
     - если `HTTPStatus >= 500` или непустая строка `obj["error"] != ""` → `LevelError`;
     - если `HTTPStatus >= 400` → `LevelWarn`;
     - иначе (`HTTPStatus < 400` и `obj["error"] == ""`) → **`LevelInfo`** (полностью игнорировать `rec.Fields["detected_level"]`!).
   - Для других JSON-схем: проверить `obj["level"]`, `obj["severity"]`, `obj["lvl"]`:
     - строки `"error"`, `"err"`, `"fatal"`, `"critical"` → `LevelError`;
     - `"warn"`, `"warning"` → `LevelWarn`;
     - `"info"` → `LevelInfo`;
     - `"debug"`, `"trace"` → `LevelDebug`.
   - Только если в JSON нет поля уровня: взять `rec.Fields["detected_level"]`, иначе `LevelInfo`.
5. **Парсинг `AppTs` и расчёт `ClockSkewMs`:**
   - Если `obj["time"]` — строка, попробовать по очереди форматы: `time.RFC3339Nano`, `time.RFC3339`, `"060102 15:04:05 -0700"`.
   - При успехе: `ClockSkewMs = (appTime.UnixNano() - ev.Ts.UnixNano()) / 1_000_000`.
6. **Извлечение `Message` и `ErrorMsg`:**
   - `Message`: первый непустой из `obj["msg"]`, `obj["message"]`; для `SchemaEchoAccess` сформировать `fmt.Sprintf("HTTP %s %s -> %d", ev.HTTPMethod, ev.HTTPRoute, ev.HTTPStatus)`.
   - `ErrorMsg`: строка из `obj["error"]` (если есть и не пуста).

### 2.2. `./services/ingest-service/internal/normalizer/normalizer_test.go`
Обязательно протестировать на двух реальных примерах:
1. Запись `chat-session` (`timestamp: "1790754654285247264"`, `fields.detected_level: "error"`, `line: "{\"time\":\"2026-09-30T07:50:54.285153537Z\",\"id\":\"d9f74895-ccc5-9ecb-9c38-8fdd487b7dc9\",\"method\":\"OPTIONS\",\"uri\":\"/api/v1/licenses/com/auth/\",\"status\":204,\"error\":\"\",\"latency\":9658}"`):
   - `assert ev.SchemaKind == contracts.SchemaEchoAccess`
   - `assert ev.Level == contracts.LevelInfo` (НЕ `ERROR`!)
   - `assert ev.HTTPStatus == 204`
2. Запись `chat-com` (`timestamp: "1790750870128800915"`, `line: "{\"msg\": \"Operator (operator_id=55754) was not found in cache\", \"time\": \"260930 08:47:50 +0000\", \"level\": \"ERROR\", \"line\": \"cache:98\", \"type\": \"app\"}"`):
   - `assert ev.SchemaKind == contracts.SchemaPyTornado`
   - `assert ev.SourcePlace == "cache:98"`
   - `assert ev.ClockSkewMs >= 7199000 && ev.ClockSkewMs <= 7201000` (+2 часа).

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}:/workspace" -v rca_go_cache:/go/pkg/mod -v rca_go_build:/root/.cache/go-build -w /workspace/services/ingest-service golang:1.23-alpine go test -v -cover ./internal/normalizer/...
py ./verify_task.py TASK-06
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
