# TASK-15: Сигналы S5, S7, S8, финальный ранжировщик гипотез, HTTP API `:8082` и `Dockerfile` (`./services/rca-engine`)

- **Статус:** `TODO`
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

- **Дата выполнения:** *(не заполнено)*
- **Созданные/изменённые файлы:** *(не заполнено)*
- **Результаты проверок и тестов:** *(не заполнено)*
- **Заметки по реализации:** *(не заполнено)*

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `PENDING` *(выбрать: `APPROVED` | `CHANGES_REQUESTED`)*
- **Проверенные контракты и тесты:** *(не заполнено)*
- **Найденные замечания (если есть):** *(не заполнено)*
