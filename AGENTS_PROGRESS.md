# Входная точка для ИИ-агентов и трекер прогресса (AGENTS_PROGRESS)

> **ОБЯЗАТЕЛЬНО К ПРОЧТЕНИЮ ДЛЯ ОРКЕСТРАТОРА, ВОРКЕРОВ И РЕВЬЮЕРА (Gemini 3.8 Flash High):**
> Этот файл — **единая таблица прогресса** при разработке проекта **RCA Logs Analyzer**.
> Машиночитаемый чекпоинт для восстановления после прерывания сессии хранится в [`./ORCHESTRATOR_STATE.json`](./ORCHESTRATOR_STATE.json).

---

## 1. Иерархия субагентов и строго последовательный протокол (`STRICT_SEQUENTIAL`)

Разработка ведётся **строго последовательно (по одной задаче за раз, без параллельного запуска воркеров)** тремя ролями агентов:

1. **Главный агент-Оркестратор ([`./ORCHESTRATOR.md`](./ORCHESTRATOR.md)):**
   - Читает [`./ORCHESTRATOR_STATE.json`](./ORCHESTRATOR_STATE.json) и этот файл (`./AGENTS_PROGRESS.md`).
   - Выбирает **одну** текущую задачу `TASK-XX` по порядку (`TASK-01` → `TASK-02` → ... → `TASK-23`).
   - Записывает чекпоинт на диск **перед** и **после** каждого вызова субагента (если работа прервётся из-за лимита токенов или перезапуска, следующий Оркестратор продолжит с того же самого шага!).
   - Сначала запускает **одного Субагента-Воркера (`tdd-worker`)**, ждёт его завершения, затем запускает **одного Субагента-Ревьюера (`strict-reviewer`)**.
   - Переходит к следующей задаче только после получения вердикта `APPROVED` от Ревьюера и создания Git-коммита точки отката.
2. **Субагент-Воркер ([`./WORKER_GUIDE.md`](./WORKER_GUIDE.md)) — работает строго по Contract-First TDD:**
   - **Шаг 1 (Контракт):** фиксирует структуры, JSON-теги и сигнатуры функций строго по чертежу `./tasks/TASK-XX-...md` и `./pkg/contracts`.
   - **Шаг 2 (Тесты ДО кода — фаза `RED`):** пишет исчерпывающие модульные и контрактные тесты в `*_test.go` с максимальным покрытием (`>= 80–85%` statement coverage), включая все сценарии из чертежа, граничные случаи и защиту от `NaN`.
   - **Шаг 3 (Код — фаза `GREEN`):** пишет 100% рабочей реализации (**0 заглушек / 0 `TODO`**) до полного прохождения тестов (`go test -v -cover ./...`) и автопроверки `py ./verify_task.py TASK-XX`, после чего заполняет блок `## Отчёт о выполнении`.
3. **Субагент-Ревьюер ([`./REVIEWER_GUIDE.md`](./REVIEWER_GUIDE.md)):**
   - Проверяет код и тесты Воркера по 8 пунктам чек-листа (посимвольное совпадение контрактов, полнота TDD-тестов и процент покрытия `>= 80–85%`, отсутствие `TODO`, потоковое чтение без `os.ReadFile`, защита от `NaN`, маскирование секретов/PII, кнопка очистки сырых логов в 1 клик) и лично запускает `py ./verify_task.py TASK-XX`.
   - Записывает вердикт (`APPROVED` или `CHANGES_REQUESTED`) в блок `## Вердикт ревьюера (заполняется субагентом-ревьюером)` в конце файла задачи.

---

## 2. Доска задач (Master Task Board — 23 атомарные задачи в последовательном порядке)

Статусы задачи: `TODO` | `IN_PROGRESS` | `IN_REVIEW` | `CHANGES_REQUESTED` | `DONE` | `BLOCKED`
Статусы ревью: `PENDING` | `APPROVED` | `CHANGES_REQUESTED`

| Порядок / ID | Статус | Статус ревью | Зависимости | Микросервис / Модуль | Файл-чертёж | Краткое описание (1–2 файла кода + тест по TDD) | Результат / Отчёт воркера и ревьюера |
|---|---|---|---|---|---|---|---|
| **01. TASK-01** | `DONE` | `APPROVED` | — | `./deploy/clickhouse`, `go.work` | [TASK-01](./tasks/TASK-01-workspace-and-clickhouse-ddl.md) | Инициализация `go.work` (6 модулей), стартовых `go.mod`, профилей памяти ClickHouse (`768MB`/`384MB`) и `init_schema.sql` | Все 11 файлов созданы, go work sync PASS, verify_task PASS |
| **02. TASK-02** | `DONE` | `APPROVED` | TASK-01 | `./docker-compose.yml`, `./config` | [TASK-02](./tasks/TASK-02-docker-compose-and-profiles.md) | `docker-compose.yml` (7 контейнеров, `internal: true`, потолок `1648MB RAM`), `service_profiles.yaml` и проверка конфигурации | docker compose config PASS, verify_task PASS |
| **03. TASK-03** | `DONE` | `APPROVED` | TASK-01 | `./pkg/contracts` | [TASK-03](./tasks/TASK-03-shared-contracts-events.md) | Контракты событий: `events.go` (`NormalizedEvent`, `RawScriptRecord`, `ErrorClass`, `IdentityEdge`, `ServiceEdge`, `SanitizeFloat`) + тест | events.go, coverage: 100.0%, verify_task PASS |
| **04. TASK-04** | `DONE` | `APPROVED` | TASK-03 | `./pkg/contracts` | [TASK-04](./tasks/TASK-04-shared-contracts-rca-and-api.md) | Контракты RCA, Live Tilt Capture (`TiltTarget`, `TiltStatusResponse`, `IngestBatchRequest`) и всех HTTP DTO (`rca.go`, `api.go`) + тест | rca.go, api.go, coverage: 100.0%, verify_task PASS |
| **05. TASK-05** | `DONE` | `APPROVED` | TASK-04 | `./services/ingest-service` | [TASK-05](./tasks/TASK-05-ingest-stream-reader.md) | Потоковый чтец `stream_reader.go` (`.json` массивы `*_part-*.json`, `.jsonl`, `.gz`, `.zip`) + расчёт `file_sha256` + тест | stream_reader.go, coverage: 86.9%, verify_task PASS |
| **06. TASK-06** | `DONE` | `APPROVED` | TASK-05 | `./services/ingest-service` | [TASK-06](./tasks/TASK-06-ingest-schema-normalizer.md) | Нормализатор `normalizer.go`: автоопределение 6 схем, исправление ложного `detected_level="error"`, расчёт `clock_skew_ms` + тест | normalizer.go, coverage: 89.4%, verify_task PASS |
| **07. TASK-07** | `DONE` | `APPROVED` | TASK-06 | `./services/ingest-service` | [TASK-07](./tasks/TASK-07-ingest-redactor-and-classifier.md) | Маскировщик секретов `redactor.go` (JWT `eyJ...`) + классификатор ошибок и ID/peer `classifier.go` (`CANCELED` и др.) + тест | redactor.go, classifier.go, coverage: 96.2%, verify_task PASS |
| **08. TASK-08** | `DONE` | `APPROVED` | TASK-07 | `./services/ingest-service` | [TASK-08](./tasks/TASK-08-ingest-drain-templater.md) | Быстрый шаблонизатор `drain.go` (маскирование переменных токенов + хэш-кэш + дерево Drain глубины 4) + тест | drain.go, coverage: 94.1%, verify_task PASS |
| **09. TASK-09** | `DONE` | `APPROVED` | TASK-02, TASK-08 | `./services/ingest-service` | [TASK-09](./tasks/TASK-09-ingest-clickhouse-writer-and-api.md) | Батч-писатель в ClickHouse `writer.go`, HTTP API `:8081` (включая `/internal/ingest/batch` для Tilt), Golden-тест + `Dockerfile` | writer.go, http.go, main.go, Dockerfile, coverage: 82.2%, verify_task PASS |
| **10. TASK-10** | `DONE` | `APPROVED` | TASK-04 | `./services/gateway-api` | [TASK-10](./tasks/TASK-10-gateway-sqlite-and-storage-purge.md) | Персистентное хранилище `store.go` + менеджер квоты 15 ГБ и очистки сырых логов в 1 клик `manager.go` (`purge-raw`) + тест | store.go, manager.go, coverage: 92.5%, verify_task PASS |
| **11. TASK-11** | `DONE` | `APPROVED` | TASK-04 | `./services/rca-engine` | [TASK-11](./tasks/TASK-11-rca-identity-graph-s1.md) | Сигнал S1 (`idgraph/graph.go`): Union-Find по `request_id`/`trace_id`/`session_id`/`visitor_id`, фильтр фоновых трейсов, fallback `pod ±50ms` + тест | idgraph/graph.go, coverage: 94.4%, verify_task PASS |
| **12. TASK-12** | `DONE` | `APPROVED` | TASK-11 | `./services/rca-engine` | [TASK-12](./tasks/TASK-12-rca-service-topology-s6.md) | Сигнал S6 (`topology/topology.go`): авто-построение ориентированного графа сервисов и расчёт `GraphRootScore` + тест | topology/topology.go, coverage: 100.0%, verify_task PASS |
| **13. TASK-13** | `DONE` | `APPROVED` | TASK-12 | `./services/rca-engine` | [TASK-13](./tasks/TASK-13-rca-anomaly-onset-s2-s4.md) | Сигналы S2 и S4 (`timeseries/anomaly.go`): сравнение с baseline (и 180-дн. историей), отсев шума, латентность, `OnsetTime` + тест | anomaly.go, coverage: 91.8%, verify_task PASS |
| **14. TASK-14** | `DONE` | `APPROVED` | TASK-13 | `./services/rca-engine` | [TASK-14](./tasks/TASK-14-rca-leadlag-correlation-s3.md) | Сигнал S3 (`timeseries/leadlag.go`): кросс-корреляция Пирсона $r(\tau)$ на сдвигах `[-60..+60 с]` с защитой от `NaN` + тест | leadlag.go, coverage: 93.2%, verify_task PASS |
| **15. TASK-15** | `DONE` | `APPROVED` | TASK-14 | `./services/rca-engine` | [TASK-15](./tasks/TASK-15-rca-ranker-and-api-s5-s7-s8.md) | Сигналы S5, S7, S8, ранжировщик `ranker.go` (`ROOT_CAUSE`/`PROPAGATION`/`SYMPTOM`/`NOISE`), HTTP API `:8082`, `Dockerfile` + тест | ranker.go, http.go, main.go, Dockerfile, coverage: 98.0%/86.4%, verify_task PASS |
| **16. TASK-16** | `TODO` | `PENDING` | TASK-04 | `./services/evidence-service` | [TASK-16](./tasks/TASK-16-evidence-collector-and-svg.md) | Сборщик доказательств `E-01..E-NN` (`collector.go`) и векторные генераторы `timeline.svg` и `topology.svg` (`charts.go`) + тест | — |
| **17. TASK-17** | `TODO` | `PENDING` | TASK-16 | `./services/evidence-service` | [TASK-17](./tasks/TASK-17-evidence-reports-gemini-and-zip.md) | Маскировщик PII (`pii.go`), генераторы `report.md`, `report.html`, `gemini_bundle.md`, `evidence.zip`, HTTP API `:8083`, `Dockerfile` + тест | — |
| **18. TASK-18** | `TODO` | `PENDING` | TASK-04 | `./services/tilt-collector` | [TASK-18](./tasks/TASK-18-tilt-discovery-and-log-stream.md) | Автообнаружение сервисов локального Tilt (`discovery.go` по меткам `dev.tilt.resource`/k8s/Docker) и демультиплексор потока логов + тест | — |
| **19. TASK-19** | `TODO` | `PENDING` | TASK-09, TASK-18 | `./services/tilt-collector` | [TASK-19](./tasks/TASK-19-tilt-stream-collector-and-api.md) | Менеджер живого захвата и батч-отправки в `ingest-service` (`collector/manager.go`), HTTP API `:8084`, `Dockerfile` + тест | — |
| **20. TASK-20** | `TODO` | `PENDING` | TASK-09, TASK-10, TASK-15, TASK-17, TASK-19 | `./services/gateway-api` | [TASK-20](./tasks/TASK-20-gateway-orchestrator-and-explorer.md) | Полный публичный REST API `:8080` (включая `/api/v1/tilt/*`), поиск/контекст логов, `RecurringCaseIDs`, `Dockerfile` + тест | — |
| **21. TASK-21** | `TODO` | `PENDING` | TASK-20 | `./services/frontend-ui` | [TASK-21](./tasks/TASK-21-frontend-shell-storage-and-cases.md) | Каркас React/TS, `client.ts`, шкала 15 ГБ с кнопкой **«Очистить сырые логи»**, управление кейсами и **панель Live-захвата из Tilt** | — |
| **22. TASK-22** | `TODO` | `PENDING` | TASK-21 | `./services/frontend-ui` | [TASK-22](./tasks/TASK-22-frontend-rca-evidence-gemini-and-e2e.md) | Панель гипотез RCA, просмотр SVG/цепочек/контекста, инспектор `E-xx`, модальное окно **«Copy for Gemini»**, Nginx `Dockerfile` | — |
| **23. TASK-23** | `TODO` | `PENDING` | TASK-20, TASK-22 | `./tools/synthgen` | [TASK-23](./tasks/TASK-23-synthetic-cascade-and-e2e-verify.md) | Генератор синтетического каскада на 6 сервисах (`generate_incident.py`) и сквозной автотест всей системы (`e2e_verify.py`) | — |

---

## 3. Журнал выполненных работ (Execution Log)

> Формат записи: `- [YYYY-MM-DD HH:MM] **TASK-XX** — Воркер: <итог, файлы, coverage> | Ревьюер: APPROVED (<результат verify_task.py>).`

- [2026-10-28 18:48] **TASK-01** — Воркер: go.work (6 модулей), 6 go.mod, ClickHouse memory profiles & DDL schema | Ревьюер: APPROVED (py ./verify_task.py TASK-01 PASS).
- [2026-10-28 18:53] **TASK-02** — Воркер: docker-compose.yml (7 сервисов, 1648MB RAM), service_profiles.yaml | Ревьюер: APPROVED (docker compose config -q & py ./verify_task.py TASK-02 PASS).
- [2026-10-28 18:59] **TASK-03** — Воркер: pkg/contracts/events.go, coverage: 100.0% | Ревьюер: APPROVED (py ./verify_task.py TASK-03 PASS).
- [2026-10-28 19:07] **TASK-04** — Воркер: pkg/contracts/rca.go, api.go, coverage: 100.0% | Ревьюер: APPROVED (py ./verify_task.py TASK-04 PASS).
- [2026-10-28 19:19] **TASK-05** — Воркер: stream_reader.go (.json array, .jsonl, .gz, .zip, SHA-256), coverage: 86.9% | Ревьюер: APPROVED (py ./verify_task.py TASK-05 PASS).
- [2026-10-28 19:35] **TASK-06** — Воркер: normalizer.go (6 schemas, Loki fix, clock_skew), coverage: 89.4% | Ревьюер: APPROVED (py ./verify_task.py TASK-06 PASS).
- [2026-10-28 19:51] **TASK-07** — Воркер: redactor.go (JWT/Bearer mask), classifier.go (ID extraction, 8 error classes, edges), coverage: 96.2% | Ревьюер: APPROVED (py ./verify_task.py TASK-07 PASS).
- [2026-10-28 20:05] **TASK-08** — Воркер: drain.go (preMask regexes, Drain tree depth 4, O(1) hash cache), coverage: 94.1% | Ревьюер: APPROVED (py ./verify_task.py TASK-08 PASS).
- [2026-10-28 20:23] **TASK-09** — Воркер: chwriter/writer.go (ClickHouse HTTP 5k batch), server/http.go (:8081, /batch), Dockerfile, coverage: 82.2% | Ревьюер: APPROVED (docker build + py ./verify_task.py TASK-09 PASS).
- [2026-10-28 20:43] **TASK-10** — Воркер: metadb/store.go, storage/manager.go (15GB quota, 1-click purge-raw), coverage: 92.5% | Ревьюер: APPROVED (py ./verify_task.py TASK-10 PASS).
- [2026-10-28 20:58] **TASK-11** — Воркер: idgraph/graph.go (S1 Union-Find trace chains, pod proximity fallback), coverage: 94.4% | Ревьюер: APPROVED (py ./verify_task.py TASK-11 PASS).
- [2026-10-28 21:08] **TASK-12** — Воркер: topology/topology.go (S6 service topology DAG, GraphRootScore), coverage: 100.0% | Ревьюер: APPROVED (py ./verify_task.py TASK-12 PASS).
- [2026-10-28 21:21] **TASK-13** — Воркер: timeseries/anomaly.go (S2 Onset, S4 Novelty & noise filter, Poisson p-value), coverage: 91.8% | Ревьюер: APPROVED (py ./verify_task.py TASK-13 PASS).
- [2026-10-28 21:34] **TASK-14** — Воркер: timeseries/leadlag.go (S3 Pearson cross-correlation, shift [-60..+60s], NaN-safe), coverage: 93.2% | Ревьюер: APPROVED (py ./verify_task.py TASK-14 PASS).
- [2026-10-28 21:53] **TASK-15** — Воркер: ranker.go (S5/S7/S8, RootCause/Prop/Symp/Noise), server/http.go (:8082, post-purge agg fallback), Dockerfile, coverage: 98%/86% | Ревьюер: APPROVED (docker build + py ./verify_task.py TASK-15 PASS).
