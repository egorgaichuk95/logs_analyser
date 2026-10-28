# TASK-15: Сигналы S5, S7, S8, финальный ранжировщик гипотез, HTTP API `:8082` и `Dockerfile` (`./services/rca-engine`)

- **Статус:** `DONE`
- **Зависимости:** `TASK-14`
- **Область видимости (Scope):**
  - Читать: `./pkg/contracts/`, `./services/rca-engine/internal/`
  - Создавать/редактировать:
    - `./services/rca-engine/internal/ranker/ranker.go`
    - `./services/rca-engine/internal/ranker/ranker_test.go`
    - `./services/rca-engine/internal/server/http.go`
    - `./services/rca-engine/cmd/rca/main.go`
    - `./services/rca-engine/Dockerfile`
  - **НЕ трогать:** другие микросервисы

---

## 1. Цель задачи

Завершить микросервис `rca-engine` (`:8082`):
1. Реализовать расчёт сигналов **S5 (`CausalDirScore`)**, **S7 (`LocalizationScore`)**, **S8 (`ChangeCorrScore`)** и итоговую взвешенную формулу ранжирования гипотез (`ROOT_CAUSE`, `PROPAGATION`, `SYMPTOM`, `NOISE`) в `ranker.go`.
2. Реализовать чтение данных из ClickHouse (из `events_raw`, а если сырые логи были очищены кнопкой «Очистить сырые логи» — из `events_agg_1m` + `templates_dict` + `service_edges`) и HTTP-сервер `:8082`.

---

## 2. Точные формулы и сигнатуры (Blueprint)

### 2.1. `./services/rca-engine/internal/ranker/ranker.go`
```go
package ranker

import (
	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/rca-engine/internal/idgraph"
	"github.com/local/logs-analyzer/services/rca-engine/internal/timeseries"
	"github.com/local/logs-analyzer/services/rca-engine/internal/topology"
)

type CandidateInput struct {
	Template        contracts.TemplateRecord
	Stats           timeseries.TemplateStats
	DominantPod     string
	PodSkewRatio    float64
	DominantRoute   string
	DominantVersion string
	PeerService     string
	VersionChanged  bool
}

// RankHypotheses объединяет сигналы S1..S8, назначает роли ROOT_CAUSE / PROPAGATION / SYMPTOM / NOISE и формирует Explanation.
func RankHypotheses(
	candidates []CandidateInput,
	idRes idgraph.Result,
	topoRes topology.GraphResult,
	llRes timeseries.LeadLagResult,
) []contracts.Hypothesis
```

**Точные правила расчёта сигналов S5, S7, S8 и итогового `Confidence`:**
1. **Сигнал S5 (`CausalDirScore`) по `ErrorClass`:**
   - `ErrClassOwnDefect` → `1.0`
   - `ErrClassResource` → `0.95`
   - `ErrClassDeadline`, `ErrClassUnavailable`, `ErrClassPeer5xx` → если у целевого `PeerService` в `candidates` есть собственный `OWN_DEFECT`/`RESOURCE`, то `0.25` (это звено распространения `PROPAGATION`), иначе `0.75`.
   - `ErrClassPeer4xx` → `0.15`
   - `ErrClassCanceled` → **`0.10`** (`context canceled` никогда не должен получать высокую оценку первопричины!).
2. **Сигнал S7 (`LocalizationScore`):**
   - Если `SourcePlace != ""` → `+0.5`.
   - Если `PodSkewRatio >= 0.8` (проблема локализована в одном pod) или `DominantRoute != ""` → `+0.5`.
3. **Сигнал S8 (`ChangeCorrScore`):**
   - `1.0`, если `VersionChanged == true`, иначе `0.0`.
4. **Итоговый балл `Confidence` и назначение ролей:**
   - Если `Stats.IsConstantNoise == true` → `Role = contracts.RoleNoise`, `Confidence = 0.05`.
   - Иначе:
     ```go
     score := 0.22*s.TraceChainScore +
              0.20*s.OnsetScore +
              0.12*s.LeadLagScore +
              0.14*s.NoveltyScore +
              0.16*s.CausalDirScore +
              0.10*s.GraphRootScore +
              0.06*s.ChangeCorrScore
     score = contracts.SanitizeFloat(math.Min(0.99, math.Max(0.05, score)))
     ```
   - Отсортировать все нешумовые гипотезы по убыванию `Confidence`:
     - Гипотеза №1 (с максимальным `Confidence` и `ErrorClass != ErrClassCanceled`) получает `Role = contracts.RoleRootCause`.
     - Остальные нешумовые гипотезы:
       - если `ErrorClass == ErrClassCanceled` или (`HTTPStatus >= 500` на входном сервисе с `GraphRootScore <= 0.2`) → `Role = contracts.RoleSymptom`;
       - иначе → `Role = contracts.RolePropagation`.

### 2.2. `./services/rca-engine/internal/server/http.go` и `Dockerfile`
- `GET /health` → `200 OK` `{"status":"ok"}`.
- `POST /internal/rca/analyze` (принимает `contracts.AnalyzeCaseRequest`, возвращает `contracts.AnalyzeCaseResponse`):
  - **Чтение из ClickHouse (с поддержкой работы после очистки сырых логов `purge-raw`):**
    1. Сначала выполнить проверку наличия сырых строк кейса:
       `SELECT count() AS cnt FROM logs_rca.events_raw WHERE case_id = '<case_id>' FORMAT JSONEachRow`.
    2. Если `cnt > 0`:
       - загрузить события кейса из `logs_rca.events_raw` и рёбра идентификаторов из `logs_rca.identity_edges` → запустить `idgraph.BuildChainsAndScores(events, idEdges)` для расчёта `S1`;
    3. Загрузить минутные ряды и метаданные из `logs_rca.events_agg_1m` (которые **сохраняются 180 дней даже после `purge-raw`**):
       `SELECT bucket_1m, service, pod, service_version, level, error_class, template_id, source_place, peer_service, sum(events_count) AS events_count, max(latency_max_ns) AS max_latency_ns, max(clock_skew_abs_ms) AS max_skew_ms FROM logs_rca.events_agg_1m WHERE case_id = '<case_id>' GROUP BY bucket_1m, service, pod, service_version, level, error_class, template_id, source_place, peer_service ORDER BY bucket_1m ASC FORMAT JSONEachRow`;
    4. Загрузить тексты шаблонов из `logs_rca.templates_dict` и рёбра сервисов из `logs_rca.service_edges`;
    5. Если `req.UseHistoricalBaseline == true`, загрузить среднюю историческую частоту шаблонов по остальным кейсам (`WHERE case_id != '<case_id>'`) из `logs_rca.events_agg_1m`;
    6. Запустить `topology.ComputeTopology`, `timeseries.AnalyzeSeries`, `timeseries.ComputeLeadLag` и `ranker.RankHypotheses` и вернуть полный `contracts.AnalyzeCaseResponse`.
- `GET /internal/rca/chain/{case_id}?key=...` → возвращает JSON-массив `[]contracts.NormalizedEvent` из `idgraph.FindChainByKey`.
- `./services/rca-engine/Dockerfile`: многоступенчатая сборка `golang:1.23-alpine` → `alpine:3.20` (копирует `go.work`, `pkg/contracts`, `services/rca-engine` и `go.mod` остальных 4 сервисов, собирает `./cmd/rca` в `/app/rca` на порту `8082`).

### 2.3. `./services/rca-engine/internal/ranker/ranker_test.go`
Проверить на каскаде из 4 кандидатов:
1. `chat-com`: `ErrClassOwnDefect` (`AttributeError`, `request_processing.py:173`, `OnsetScore=1.0`, `GraphRootScore=1.0`) → получает `Rank == 1` и `Role == contracts.RoleRootCause`.
2. `chat-session`: `ErrClassDeadline` (`client.go:173`, `PeerService="chat-com"`, `OnsetScore=0.8`, `GraphRootScore=0.35`) → получает `Role == contracts.RolePropagation`.
3. `api-gateway`: `ErrClassCanceled` (`context canceled`, `GraphRootScore=0.15`) → получает `Role == contracts.RoleSymptom`.
4. `chat-com` (`cache:98`, `IsConstantNoise=true`) → получает `Role == contracts.RoleNoise`.

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}:/workspace" -v rca_go_cache:/go/pkg/mod -v rca_go_build:/root/.cache/go-build -w /workspace/services/rca-engine golang:1.23-alpine go test -v -cover ./...
docker compose build rca-engine
py ./verify_task.py TASK-15
```

---

## Отчёт о выполнении (заполняется субагентом-воркером)

- **Дата выполнения:** 2026-10-28 21:48 UTC
- **Соблюдение цикла TDD (Contract -> Tests -> Code):**
  - **Шаг 1 (Contract):** Объявлены структуры `CandidateInput`, сигнатура `RankHypotheses`, `Server` и HTTP маршруты (`/health`, `/internal/rca/analyze`, `/internal/rca/chain/{case_id}`).
  - **Шаг 2 (RED tests first):** Написаны исчерпывающие тесты в `ranker_test.go` и `http_test.go`:
    - Каскад из 4 кандидатов (`chat-com` OwnDefect -> Rank 1 RootCause; `chat-session` Deadline -> Propagation; `api-gateway` Canceled -> Symptom; `chat-com` cache:98 -> Noise).
    - Расчёт всех сигналов S1..S8, формулы взвешенного Confidence с защитой `SanitizeFloat` от `NaN`/`Inf`.
    - Проверка граничных случаев (пустой вход, все ошибки canceled, zero denominators).
    - HTTP API тесты с моком ClickHouse: проверка `/health`, обработка ошибок валидации (400 Bad Request), полный цикл `/internal/rca/analyze` с сырыми логами, а также режим после `purge-raw` (чтение из `events_agg_1m`, `templates_dict`, `service_edges` при `count(events_raw) == 0`), поддержка `use_historical_baseline`, эндпоинт `/internal/rca/chain/{case_id}` с ленивой загрузкой и фильтрацией по ключу.
  - **Шаг 3 (GREEN code):** Реализована 100% рабочая логика в `ranker.go`, `server/http.go`, `cmd/rca/main.go` и `Dockerfile` без `TODO`, `FIXME` и заглушек.
  - **Шаг 4 (Coverage Gate):** Все тесты прошли успешно (`PASS`). Покрытие пакетов:
    - `internal/ranker`: **98.0%**
    - `internal/server`: **86.4%**
    - `internal/idgraph`: **94.4%**
    - `internal/timeseries`: **93.2%**
    - `internal/topology`: **100.0%**
- **Созданные/изменённые файлы:**
  - `services/rca-engine/internal/ranker/ranker.go` (230 строк)
  - `services/rca-engine/internal/ranker/ranker_test.go` (375 строк)
  - `services/rca-engine/internal/server/http.go` (630 строк)
  - `services/rca-engine/internal/server/http_test.go` (364 строки)
  - `services/rca-engine/cmd/rca/main.go` (58 строк)
  - `services/rca-engine/Dockerfile` (16 строк)
- **Результаты проверок и тестов:**
  - `docker run ... go test -v -cover ./...` -> `PASS`, покрытие всех пакетов от 86.4% до 100.0%.
  - `docker compose build rca-engine` -> `Image rca-project-rca-engine Built`.
  - `py ./verify_task.py TASK-15` -> `[VERIFY PASSED] TASK-15 passed all automated checks!`.
- **Заметки по реализации:**
  - Поддержана бесшовная работа анализа инцидента даже после вызова `purge-raw`: при отсутствии сырых строк события реконструируются из 180-дневного минуточного агрегата `events_agg_1m`, справочника `templates_dict` и топологических связей `service_edges`.
  - Все вещественные значения защищены от деления на 0 и невалидных чисел с помощью `contracts.SanitizeFloat`.
  - Построены детальные человекочитаемые пояснения (`Explanation`) для каждого ранга гипотез.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты (TDD Coverage):**
  - **Проверенные файлы:**
    - `services/rca-engine/internal/ranker/ranker.go`
    - `services/rca-engine/internal/ranker/ranker_test.go`
    - `services/rca-engine/internal/server/http.go`
    - `services/rca-engine/internal/server/http_test.go`
    - `services/rca-engine/cmd/rca/main.go`
    - `services/rca-engine/Dockerfile`
  - **Полнота контрактных тестов:**
    - Каскад из 4 кандидатов (`chat-com` OwnDefect -> `ROOT_CAUSE`, `chat-session` Deadline -> `PROPAGATION`, `api-gateway` Canceled -> `SYMPTOM`, `chat-com` cache:98 -> `NOISE`).
    - Полная матрица для S5 (`CausalDirScore`) по всем классам ошибок с проверкой влияния `PeerService` и блокировкой `ErrClassCanceled` как `ROOT_CAUSE`.
    - Сигналы S7 (`LocalizationScore`) и S8 (`ChangeCorrScore`).
    - Взвешенная формула `Confidence` и назначение ролей.
    - Обработка краевых случаев: пустой вход, только `ErrClassCanceled`, входной шлюз с 5xx ошибкой и низким `GraphRootScore`.
    - HTTP API эндпоинты `GET /health`, `POST /internal/rca/analyze`, `GET /internal/rca/chain/{case_id}` с валидацией входных данных, моком ClickHouse, проверкой анализа с сырыми логами и после очистки `purge-raw` (из 180-дневных агрегатов `events_agg_1m`).
  - **Покрытие тестами (`go test -v -cover ./...`):**
    - `internal/ranker`: **98.0%** of statements (порог >= 80% соблюдён)
    - `internal/server`: **86.4%** of statements (порог >= 80% соблюдён)
    - `internal/idgraph`: **94.4%** of statements
    - `internal/timeseries`: **93.2%** of statements
    - `internal/topology`: **100.0%** of statements
  - **Автоматический верификатор:**
    - `py ./verify_task.py TASK-15` -> `[VERIFY PASSED] TASK-15 passed all automated checks!`.
    - `docker compose build rca-engine` -> Docker-образ успешно собран.
  - **Проверка чек-листа (8/8):**
    - Scope соблюдён, чужие микросервисы не модифицировались.
    - Контракты и JSON-теги посимвольно совпадают с `pkg/contracts` и `TASK-15.md`.
    - Отсутствуют заглушки (`TODO`, `FIXME`, пустые функции).
    - `contracts.SanitizeFloat` применён во всех формулах, деление на ноль защищено.
    - Отсутствует чтение файлов целиком в память (`os.ReadFile`/`io.ReadAll` для логов нет, стриминг через `json.NewDecoder`).
- **Найденные замечания (если есть):** Замечаний нет. Реализация полностью готова к продакшену.
