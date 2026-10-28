# TASK-20: Публичный REST API `gateway-api` (`:8080`), проксирование `tilt-collector`, поиск/контекст логов и `Dockerfile` (`./services/gateway-api`)

- **Статус:** `TODO`
- **Зависимости:** `TASK-09`, `TASK-10`, `TASK-15`, `TASK-17`, `TASK-19`
- **Область видимости (Scope):**
  - Читать: `./pkg/contracts/`, `./services/gateway-api/internal/`
  - Создавать/редактировать:
    - `./services/gateway-api/internal/explorer/explorer.go`
    - `./services/gateway-api/internal/server/http.go`
    - `./services/gateway-api/internal/server/http_test.go`
    - `./services/gateway-api/cmd/gateway/main.go`
    - `./services/gateway-api/Dockerfile`
  - **НЕ трогать:** другие микросервисы

---

## 1. Цель задачи

Собрать финальный фасад `gateway-api` (`:8080`), предоставляющий фронтенду единый REST API `/api/v1/*` (с заголовками CORS `*`), оркестрирующий вызовы к `ingest-service`, `rca-engine`, `evidence-service` и `tilt-collector` (`:8084`) и поддерживающий работу с отчётами и агрегатами даже после нажатия кнопки **«Очистить сырые логи»**.

---

## 2. Посимвольная таблица эндпоинтов `/api/v1/*` (Контракт с `frontend-ui`)

| Метод и путь | Тело запроса | Тип ответа (`pkg/contracts`) | Поведение |
|---|---|---|---|
| `GET /api/v1/health` | — | `{"status":"ok"}` | Проверка доступности |
| `GET /api/v1/storage/status` | — | `contracts.StorageStatusResponse` | Вызывает `storage.Manager.GetStatus` |
| `POST /api/v1/storage/purge-raw` | — | `contracts.PurgeRawResponse` | **Очистка сырых логов в 1 клик** (`storage.Manager.PurgeAllRawLogs`) |
| `GET /api/v1/files/available` | — | `[]contracts.AvailableFile` | Проксирует `GET http://ingest-service:8081/internal/ingest/browse` |
| `GET /api/v1/cases` | — | `[]contracts.CaseMeta` | Список всех кейсов из `metadb.Store.ListCases()` |
| `POST /api/v1/cases` | `contracts.CreateCaseRequest` (`{"title":"...","description":"..."}`) | `contracts.CaseMeta` | Создаёт новый кейс с UUID |
| `DELETE /api/v1/cases/{id}` | — | `{"status":"deleted"}` | Удаляет кейс, его партиции и папку отчётов |
| `POST /api/v1/cases/{id}/ingest-local` | `contracts.IngestLocalRequest` (`{"file_paths":["..."]}`) | `contracts.IngestStatusResponse` | Запускает `POST http://ingest-service:8081/internal/ingest/start` |
| `POST /api/v1/cases/{id}/upload` | `multipart/form-data` (`files`) | `contracts.IngestStatusResponse` | Потоково сохраняет файлы в `/data/staging/<case_id>/` и вызывает `ingest-service` |
| `GET /api/v1/jobs/{job_id}` | — | `contracts.IngestStatusResponse` | Проксирует `GET http://ingest-service:8081/internal/ingest/status/{job_id}` |
| `POST /api/v1/cases/{id}/analyze` | `contracts.AnalyzeCaseRequest` | `contracts.AnalyzeCaseResponse` | 1) Вызывает `rca-engine` (`/internal/rca/analyze`); 2) Проставляет `RecurringCaseIDs` через `metadb.Store.FindRecurringCases`; 3) Вызывает `evidence-service` (`/internal/evidence/build`); 4) Сохраняет `AnalysisRun` и `Report` в `metadb.Store` и возвращает `AnalyzeCaseResponse` с заполненным `EvidenceItems` |
| `GET /api/v1/cases/{id}/analysis` | — | `contracts.AnalyzeCaseResponse` | Возвращает последний сохранённый `AnalyzeCaseResponse` из `metadb.Store` (работает даже после `purge-raw`!) |
| `GET /api/v1/cases/{id}/chain?key=...` | — | `[]contracts.NormalizedEvent` | Проксирует `GET http://rca-engine:8082/internal/rca/chain/{id}?key=...` |
| `GET /api/v1/cases/{id}/logs/context?pod=...&ts=...&mode=pod\|all_services` | — | `contracts.ContextLogsResponse` | Запрашивает `±20` соседних строк из `logs_rca.events_raw` (если `events_raw` пуста после очистки, возвращает `RawPurged: true`) |
| `GET /api/v1/cases/{id}/reports/{run_id}/{kind}` | `kind` ∈ `markdown`, `html`, `gemini`, `zip` | Файл (`text/markdown`, `text/html`, `application/zip`) | Отдаёт соответствующий файл отчёта с диска `/data/reports/<case_id>/<run_id>/` |
| `GET /api/v1/tilt/targets` | — | `[]contracts.TiltTarget` | Проксирует `GET http://tilt-collector:8084/internal/tilt/targets` (список запущенных сервисов в локальном Tilt / k8s / Docker) |
| `POST /api/v1/cases/{id}/tilt/start` | `contracts.TiltStartCaptureRequest` | `contracts.TiltStatusResponse` | Проставляет `req.CaseID = id` и вызывает `POST http://tilt-collector:8084/internal/tilt/start` |
| `POST /api/v1/cases/{id}/tilt/stop` | — | `contracts.TiltStatusResponse` | Вызывает `POST http://tilt-collector:8084/internal/tilt/stop` |
| `GET /api/v1/cases/{id}/tilt/status` | — | `contracts.TiltStatusResponse` | Проксирует `GET http://tilt-collector:8084/internal/tilt/status` |

**Переменные окружения `gateway-api` (`cmd/gateway/main.go`):**
- `CLICKHOUSE_HTTP_URL` (по умолчанию `"http://clickhouse:8123"`)
- `INGEST_URL` (по умолчанию `"http://ingest-service:8081"`)
- `RCA_URL` (по умолчанию `"http://rca-engine:8082"`)
- `EVIDENCE_URL` (по умолчанию `"http://evidence-service:8083"`)
- `TILT_COLLECTOR_URL` (по умолчанию `"http://tilt-collector:8084"`)
- `META_STATE_PATH` (по умолчанию `"/data/meta/state.json"`, с fallback на `SQLITE_PATH`, если задан)
- `STAGING_DIR` (по умолчанию `"/data/staging"`)
- `REPORTS_DIR` (по умолчанию `"/data/reports"`)
- `DISK_QUOTA_BYTES` (`16106127360`) и `RAW_QUOTA_BYTES` (`10737418240`)
- `PORT` (`"8080"`)

**SQL-запросы контекста в `./services/gateway-api/internal/explorer/explorer.go`:**
- При `mode == "pod"` (по умолчанию):
  `SELECT * FROM logs_rca.events_raw WHERE case_id = '<case_id>' AND pod = '<pod>' AND ts BETWEEN toDateTime64('<ts>', 9, 'UTC') - INTERVAL 30 SECOND AND toDateTime64('<ts>', 9, 'UTC') + INTERVAL 30 SECOND ORDER BY ts ASC LIMIT 40 FORMAT JSONEachRow`
- При `mode == "all_services"`:
  тот же запрос без фильтра `AND pod = '<pod>'` (показывает хронологический срез всех сервисов вокруг момента ошибки).
- Если таблица `events_raw` пуста для данного `case_id`, возвращать `contracts.ContextLogsResponse{RawPurged: true, Mode: mode, Events: []contracts.NormalizedEvent{}}`.

---

## 3. Команда проверки (Verification Gate)

В `http_test.go` протестировать полный сценарий с тестовыми `httptest.NewServer` для `ingest`, `rca`, `evidence`, `tilt-collector` и `clickhouse`:
- `POST /api/v1/cases` → `GET /api/v1/tilt/targets` → `POST /api/v1/cases/{id}/tilt/start` → `POST /api/v1/cases/{id}/analyze` → `POST /api/v1/storage/purge-raw` → `GET /api/v1/cases/{id}/analysis` и `GET /api/v1/cases/{id}/reports/{run_id}/gemini` (оба возвращают `200 OK` после очистки сырых логов!).

```powershell
docker run --rm -v "${PWD}:/workspace" -v rca_go_cache:/go/pkg/mod -v rca_go_build:/root/.cache/go-build -w /workspace/services/gateway-api golang:1.23-alpine go test -v -cover ./...
docker compose build gateway-api
py ./verify_task.py TASK-20
```

---

## Отчёт о выполнении (заполняется субагентом-воркером)

- **Дата выполнения:** 2026-10-28 23:17 UTC
- **Соблюдение цикла TDD (Contract → Tests → Code):**
  - Шаг 1 (Contract): Зафиксированы структуры и методы в `explorer.go` и `http.go`.
  - Шаг 2 (Red): Написаны контрактные тесты в `explorer_test.go` и `http_test.go` с моками ClickHouse, Ingest, RCA, Evidence и Tilt, зафиксирован FAIL тестов до реализации.
  - Шаг 3 (Green): Реализован 100% рабочий код без TODO и заглушек в `explorer.go`, `http.go`, `main.go`, `Dockerfile`.
  - Шаг 4 (Coverage & Verification Gate): Все тесты прошли со статусом PASS, покрытие пакетов `server` (83.4%), `explorer` (89.0%), `storage` (92.5%).
- **Созданные/изменённые файлы:**
  - `services/gateway-api/internal/explorer/explorer.go` (215 строк)
  - `services/gateway-api/internal/explorer/explorer_test.go` (172 строки)
  - `services/gateway-api/internal/server/http.go` (632 строки)
  - `services/gateway-api/internal/server/http_test.go` (1226 строк)
  - `services/gateway-api/cmd/gateway/main.go` (114 строк)
  - `services/gateway-api/Dockerfile` (17 строк)
- **Результаты проверок и тестов:**
  - `go test -v -cover ./internal/...`:
    - `internal/explorer`: `PASS` (coverage: 89.0% of statements)
    - `internal/server`: `PASS` (coverage: 83.4% of statements)
    - `internal/storage`: `PASS` (coverage: 92.5% of statements)
  - `docker compose build gateway-api`: `[OK] Image rca-project-gateway-api Built`
  - `py ./verify_task.py TASK-20`: `[VERIFY PASSED] TASK-20 passed all automated checks!`
- **Заметки по реализации:**
  - Реализован фасад REST API `/api/v1/*` с CORS заголовками `*` и обработкой preflight OPTIONS.
  - Реализована оркестрация анализа: запрос к `rca-engine`, поиск повторяющихся инцидентов `metadb.Store.FindRecurringCases`, генерация доказательств `evidence-service`, сохранение метаданных и выдача `AnalyzeCaseResponse` с `EvidenceItems`.
  - Поддержана очистка сырых логов в 1 клик (`/storage/purge-raw`) с гарантией сохранения доступности отчётов (`markdown`, `html`, `gemini`, `zip`) и последнего сохранённого анализа `/cases/{id}/analysis` (200 OK).
  - В `explorer.go` реализован контекстный срез `±20` строк (в окне 30 секунд) с автоматическим переключением в режим `RawPurged: true` при очистке `events_raw`.
  - Загрузка файлов через `/upload` реализована потоково в `/data/staging/<case_id>/` чанками по 256 КБ с соблюдением бюджета памяти.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты (TDD Coverage):**
  - Аудит всех 8 пунктов чек-листа `REVIEWER_GUIDE.md` успешно пройден.
  - Посимвольно проверен REST API `/api/v1/*` (19 эндпоинтов) на соответствие чертежу и пакетам `pkg/contracts`.
  - Полнота тестов: протестирован полный end-to-end цикл (`cases` → `tilt` → `analyze` → `purge-raw` → `analysis` & `reports/gemini` с 200 OK после очистки сырых логов), граничные сценарии, таймауты, пустые входы, streaming upload чанками по 256 КБ, CORS preflight.
  - Покрытие пакетов:
    - `internal/explorer`: **89.0%** of statements
    - `internal/server`: **83.4%** of statements
    - `internal/storage`: **92.5%** of statements
  - Личный запуск `py ./verify_task.py TASK-20`: `[VERIFY PASSED] TASK-20 passed all automated checks!`, сборка `docker compose build gateway-api` успешна.
- **Найденные замечания (если есть):** Замечаний нет. Реализация чистая, без TODO/заглушек, с соблюдением ресурсных ограничений и автономности.
