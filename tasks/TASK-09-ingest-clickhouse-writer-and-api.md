# TASK-09: Батч-писатель в ClickHouse, HTTP API `:8081` и `Dockerfile` (`./services/ingest-service`)

- **Статус:** `TODO`
- **Зависимости:** `TASK-02`, `TASK-08`
- **Область видимости (Scope):**
  - Читать: `./pkg/contracts/`, `./services/ingest-service/internal/`
  - Создавать/редактировать:
    - `./services/ingest-service/internal/chwriter/writer.go`
    - `./services/ingest-service/internal/server/http.go`
    - `./services/ingest-service/internal/server/http_test.go`
    - `./services/ingest-service/cmd/ingest/main.go`
    - `./services/ingest-service/Dockerfile`
  - **НЕ трогать:** другие микросервисы

---

## 1. Цель задачи

Собрать полный конвейер микросервиса `ingest-service` (`:8081`):
`reader.StreamPath` → `normalizer.NormalizeRecord` → `classifier.EnrichAndClassify` → `drain.Miner.Mine` → батчевая отправка по `5000` записей в ClickHouse (`events_raw`, `templates_dict`, `identity_edges`, `service_edges`, `ingested_files`) с проверкой дубликатов файлов по `file_sha256`.

> **Важно для надёжности и отсутствия лишних зависимостей:** взаимодействовать с ClickHouse через его штатный HTTP-интерфейс (`CLICKHOUSE_HTTP_URL`, по умолчанию `http://clickhouse:8123`) и формат `JSONEachRow` на стандартном `net/http` Go!

---

## 2. Точные сигнатуры и формат ClickHouse HTTP (Blueprint)

### 2.1. `./services/ingest-service/internal/chwriter/writer.go`
```go
package chwriter

import (
	"context"
	"net/http"
	"github.com/local/logs-analyzer/pkg/contracts"
)

type Writer struct {
	baseURL string
	client  *http.Client
}

func NewWriter(baseURL string) *Writer

// IsFileAlreadyIngested проверяет, есть ли (case_id, file_sha256) в logs_rca.ingested_files
func (w *Writer) IsFileAlreadyIngested(ctx context.Context, caseID, fileSHA256 string) (bool, error)

// RecordIngestedFile записывает факт загрузки файла в logs_rca.ingested_files
func (w *Writer) RecordIngestedFile(ctx context.Context, caseID, fileSHA256, fileName string, rows uint64) error

// FlushBatch отправляет накопленный батч (до 5000 строк) в logs_rca.events_raw, templates_dict, identity_edges, service_edges
func (w *Writer) FlushBatch(
	ctx context.Context,
	events []contracts.NormalizedEvent,
	templates []contracts.TemplateRecord,
	idEdges []contracts.IdentityEdge,
	srvEdges []contracts.ServiceEdge,
) error
```

**Формат `DateTime64(9, 'UTC')` для ClickHouse `JSONEachRow`:**
При формировании JSON-строки для ClickHouse форматировать `time.Time` как `t.UTC().Format("2006-01-02 15:04:05.000000000")` и передавать параметр URL `date_time_input_format=best_effort` (`POST http://clickhouse:8123/?date_time_input_format=best_effort&query=INSERT+INTO+logs_rca.events_raw+FORMAT+JSONEachRow`).

### 2.2. `./services/ingest-service/internal/server/http.go`
Реализовать HTTP-обработчики:
1. `GET /health` → `200 OK` `{"status":"ok"}`.
2. `GET /internal/ingest/browse` → сканирует папки `IMPORT_DIR` (`/import`) и `EXAMPLE_DIR` (`/log_example`) и возвращает JSON-массив `[]contracts.AvailableFile` (все файлы `.json`, `.jsonl`, `.gz`, `.zip`).
3. `POST /internal/ingest/start` (принимает `contracts.StartIngestRequest`):
   - Создаёт задачу со статусом `RUNNING` в `sync.Map` / `map[string]*contracts.IngestStatusResponse` под мьютексом и запускает фоновую горутину обработки файлов.
   - Для каждого файла из `req.FilePaths`:
     - считает `sha256` через `reader.ComputeFileSHA256(path)`;
     - проверяет `writer.IsFileAlreadyIngested(ctx, req.CaseID, sha256)`: если `true` — увеличивает `SkippedDuplicateFiles++` и переходит к следующему файлу;
     - иначе запускает `reader.StreamPath`, каждые `5000` записей вызывает `writer.FlushBatch`, собирает предупреждения о `ClockSkewMs` (если `abs(ev.ClockSkewMs) > 60000`, добавляет предупреждение `"service <S> has clock skew <X>ms"`), а в конце файла вызывает `writer.RecordIngestedFile`.
   - Возвращает `202 Accepted` с начальным `contracts.IngestStatusResponse`.
4. `GET /internal/ingest/status/{job_id}` → возвращает текущий `contracts.IngestStatusResponse`.
5. `POST /internal/ingest/batch` (принимает `contracts.IngestBatchRequest`, возвращает `contracts.IngestBatchResponse`):
   - **Стриминговый приём батчей логов напрямую из `tilt-collector` (`:8084`)!**
   - Прогоняет каждую запись из `req.Records` через тот же самый конвейер (`normalizer.NormalizeRecord(req.CaseID, rec, req.SourceName, idx)` → `classifier.EnrichAndClassify` → `drain.Miner.Mine`), вызывает `writer.FlushBatch` и возвращает `200 OK` с `contracts.IngestBatchResponse{RowsIngested: uint64(len(req.Records)), ClockSkewWarnings: warnings}`.

### 2.3. `./services/ingest-service/Dockerfile`
```dockerfile
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.work ./
COPY pkg/contracts ./pkg/contracts
COPY services/ingest-service ./services/ingest-service
COPY services/rca-engine/go.mod ./services/rca-engine/go.mod
COPY services/evidence-service/go.mod ./services/evidence-service/go.mod
COPY services/gateway-api/go.mod ./services/gateway-api/go.mod
COPY services/tilt-collector/go.mod ./services/tilt-collector/go.mod
RUN cd services/ingest-service && CGO_ENABLED=0 go build -o /out/ingest ./cmd/ingest

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/ingest /app/ingest
EXPOSE 8081
CMD ["/app/ingest"]
```

### 2.4. `./services/ingest-service/internal/server/http_test.go`
Написать интеграционный тест с `httptest.NewServer` (имитирующим ClickHouse `:8123`), проверяющий:
1. Запуск `POST /internal/ingest/start` на временном `.json` файле из 2 записей (`chat-com` со сдвигом часов `+7200 с` и `chat-session` с `context canceled`);
2. Ожидание статуса `COMPLETED` через `GET /internal/ingest/status/{job_id}`;
3. Проверку `RowsIngested == 2` и непустого `ClockSkewWarnings`;
4. Повторный запуск на том же файле и проверку `SkippedDuplicateFiles == 1`;
5. Вызов `POST /internal/ingest/batch` с `contracts.IngestBatchRequest` (2 живые записи от `tilt:chat-session`) и проверку `RowsIngested == 2`.
6. **Эталонный «Золотой тест» (`TestGoldenRealLogExamples`) на реальных файлах из `../../log_example/`:**
   - Если в `../../log_example/` присутствуют реальные файлы `chat-com_*.json` и `chat-session_*.json`, прогнать первые `500` записей каждого файла через полный конвейер (`reader.StreamPath` → `normalizer.NormalizeRecord` → `classifier.EnrichAndClassify` → `drain.Miner.Mine`) и убедиться:
     - ни одна реальная строка не вызывает ошибку парсинга или панику;
     - в `chat-session` ложные `detected_level="error"` на `HTTP 200/204` нормализованы в `INFO`, а все `eyJ...` JWT-токены замаскированы (`!strings.Contains(ev.RawRedacted, "eyJhbGci")`);
     - в `chat-com` обнаружен сдвиг часов `ClockSkewMs > 7000000` (+2 часа).

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}:/workspace" -v rca_go_cache:/go/pkg/mod -v rca_go_build:/root/.cache/go-build -w /workspace/services/ingest-service golang:1.23-alpine go test -v -cover ./...
docker compose build ingest-service
py ./verify_task.py TASK-09
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
