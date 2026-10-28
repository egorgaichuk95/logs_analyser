# TASK-11: Сигнал S1 — Граф идентификаторов и цепочки запросов (`./services/rca-engine/internal/idgraph`)

- **Статус:** `IN_REVIEW`
- **Зависимости:** `TASK-04`
- **Область видимости (Scope):**
  - Читать: `./pkg/contracts/`
  - Создавать/редактировать:
    - `./services/rca-engine/internal/idgraph/graph.go`
    - `./services/rca-engine/internal/idgraph/graph_test.go`
  - **НЕ трогать:** другие папки

---

## 1. Цель задачи

Реализовать в `rca-engine` пакет `idgraph` (Сигнал S1), который:
1. Отфильтровывает «длинные» фоновые трейсы (например, циклы NATS-консьюмера с $>30$ событиями или длительностью $>60\text{ с}$).
2. Объединяет события в сквозные цепочки запросов (`Chain`) через структуру непересекающихся множеств (**Union-Find**) по всем доступным ключам (`request_id`, `trace_id`, `session_id`, `visitor_id`) и рёбрам `IdentityEdge`.
3. При отсутствии сквозного ID связывает ошибочные события внутри одного `pod` по миллисекундной близости (`|Δt| ≤ 50 мс`).
4. В каждой цепочке с ошибкой находит первоисточник (`OriginError`) и рассчитывает для каждого шаблона `(service, template_id)` оценку `TraceChainScore ∈ [0.0 .. 1.0]`.

---

## 2. Точные сигнатуры и алгоритм (Blueprint)

### 2.1. `./services/rca-engine/internal/idgraph/graph.go`
```go
package idgraph

import (
	"github.com/local/logs-analyzer/pkg/contracts"
)

type Chain struct {
	ChainID     string                      `json:"chain_id"`
	Events      []contracts.NormalizedEvent `json:"events"`
	OriginEvent *contracts.NormalizedEvent  `json:"origin_event,omitempty"`
}

type Result struct {
	Chains           []Chain
	TemplateS1Scores map[uint64]float64 // key: TemplateID -> TraceChainScore [0..1]
	DerivedEdges     []contracts.ServiceEdge
}

// BuildChainsAndScores строит сквозные цепочки из событий и рёбер идентификаторов,
// вычисляет S1 (TraceChainScore) и выводит межсервисные переходы внутри цепочек.
func BuildChainsAndScores(
	events []contracts.NormalizedEvent,
	edges []contracts.IdentityEdge,
) Result

// FindChainByKey возвращает упорядоченную по времени цепочку для заданного trace_id / request_id / session_id
func FindChainByKey(events []contracts.NormalizedEvent, edges []contracts.IdentityEdge, key string) []contracts.NormalizedEvent
```

**Точные правила внутри `BuildChainsAndScores`:**
1. **Фильтр фоновых циклов (`isBackgroundTrace`):**
   - Подсчитать для каждого `trace_id` число событий и `span = max(ts) - min(ts)`.
   - Если `count > 30` или `span > 60s` или `strings.Contains(ev.SourcePlace, "consumer")` / `ev.Message == "fetched"` → игнорировать этот `trace_id` при склейке!
2. **Union-Find по узлам идентификаторов:**
   - Каждый ключ кодируется префиксом: `"req:" + RequestID`, `"tr:" + TraceID`, `"sess:" + SessionID`, `"vis:" + VisitorID`.
   - Объединить (`union`) все ключи из `edges` и все ключи, встречающиеся вместе в одном `NormalizedEvent`.
3. **Fallback по близости внутри `pod` (`±50 мс`):**
   - Отсортировать события каждого `pod` по `Ts`. Если два соседних ошибочных/предупреждающих события в одном `pod` отстоят друг от друга на `<= 50 ms`, объединить их в одну компоненту.
4. **Определение `OriginEvent` и расчёт `TemplateS1Scores`:**
   - В каждой цепочке `Chain`, где есть хотя бы одно событие с `Level == ERROR` или `HTTPStatus >= 500`, отсортировать `Events` по `(Ts, SourceRecordIdx)`.
   - Найти **первую ошибку в цепочке**, у которой `ErrorClass != contracts.ErrClassCanceled` (если в цепочке есть только `ErrClassCanceled`, взять её с коэффициентом `0.2`).
   - Пусть $N_{\text{err\_chains}}$ — общее число цепочек с ошибками (при $N_{\text{err\_chains}} == 0$ все оценки равны `0.0`).
   - Для каждого `TemplateID`: `score = float64(originCount[templateID]) / float64(N_err_chains)`, пропустить через `contracts.SanitizeFloat(score)`.

### 2.2. `./services/rca-engine/internal/idgraph/graph_test.go`
Проверить:
1. Транзитивную склейку 4 событий: `access (req=R1)` + `interceptor (req=R1, trace=T1)` + `chat-session app (trace=T1, sess=S1)` + `chat-com err (sess=S1, OWN_DEFECT)` → все 4 попадают в одну цепочку, `OriginEvent` указывает на `chat-com err`, а его `TemplateS1Scores == 1.0`.
2. Склейку по `pod ±50ms` на 3 событиях без общих ID.
3. Отсев фонового трейса с 35 событиями.

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}:/workspace" -v rca_go_cache:/go/pkg/mod -v rca_go_build:/root/.cache/go-build -w /workspace/services/rca-engine golang:1.23-alpine go test -v -cover ./internal/idgraph/...
py ./verify_task.py TASK-11
```

---

## Отчёт о выполнении (заполняется субагентом-воркером)

- **Дата выполнения:** 2026-10-28
- **Соблюдение цикла TDD (Contract → Tests → Code):**
  - Шаг 1 (Contract): зафиксированы структуры `Chain`, `Result` и сигнатуры `BuildChainsAndScores`, `FindChainByKey` по Blueprint.
  - Шаг 2 (RED Tests First): написаны исчерпывающие тесты в `graph_test.go` (транзитивная склейка 4 событий, pod ±50ms, отсев длинных фоновых трейсов, логика с `CANCELED`, защита от деления на 0 и пустых данных, `IdentityEdge`, `FindChainByKey`, сериализация JSON). Тесты падали в фазе RED.
  - Шаг 3 (GREEN Implementation): реализован 100% алгоритм без TODO и заглушек.
  - Шаг 4 (Coverage & Verification Gate): запущены тесты и `verify_task.py TASK-11`.
- **Созданные/изменённые файлы:**
  - `./services/rca-engine/internal/idgraph/graph.go` (366 строк)
  - `./services/rca-engine/internal/idgraph/graph_test.go` (584 строки)
  - `./tasks/TASK-11-rca-identity-graph-s1.md`
- **Результаты проверок и тестов:**
  - `go test -v -cover ./internal/idgraph/...` — все 9 тестов пройдены (`PASS`).
  - Покрытие: `coverage: 94.4% of statements` (требуемый минимум: `>= 80.0%`).
  - Автоматический скрипт: `py ./verify_task.py TASK-11` выдал `[VERIFY PASSED]`.
- **Заметки по реализации:**
  - Алгоритм Union-Find (Disjoint Set Union с эвристиками сжатия пути и объединения по рангу) группирует события по составным ключам идентификаторов (`req:`, `tr:`, `sess:`, `vis:`) и рёбрам `IdentityEdge`.
  - Реализован фильтр длинных/фоновых циклов (`count > 30`, `span > 60s`, `consumer` в `SourcePlace`, сообщение `"fetched"`). Фоновые трейсы игнорируются при склейке и отсеиваются из цепочек инцидента.
  - Реализован fallback по близости внутри pod (`|Δt| <= 50ms`) для ошибочных/предупреждающих событий.
  - Определение `OriginEvent` пропускает `contracts.ErrClassCanceled`, если в цепочке есть другие ошибки. При наличии только `CANCELED` назначается коэффициент `0.2`.
  - Все оценки $S1 \in [0.0..1.0]$ защищены от деления на ноль при $N_{\text{err\_chains}} == 0$ и нормализуются через `contracts.SanitizeFloat`.
  - Автоматически выводятся межсервисные переходы `DerivedEdges` внутри цепочек с источником доказательства `trace_propagation` и подсчётом `CallCount`/`ErrorCount`.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты (TDD Coverage):**
  - Файлы в Scope: `./services/rca-engine/internal/idgraph/graph.go`, `./services/rca-engine/internal/idgraph/graph_test.go`. Чужие сервисы не затронуты.
  - Посимвольный аудит контрактов: структуры `Chain`, `Result`, экспортируемые функции `BuildChainsAndScores`, `FindChainByKey`, JSON-теги (`chain_id`, `events`, `origin_event`), интеграция с типами `./pkg/contracts` (`NormalizedEvent`, `IdentityEdge`, `ServiceEdge`) полностью соответствуют Blueprint.
  - TDD-аудит и граничные сценарии: проверены 9 комплексных тестов (транзитивная склейка по нескольким идентификаторам, pod proximity fallback ±50ms, отсев длинных/фоновых трейсов >30 событий / >60с / "fetched" / consumer, приоритет выбора ошибок над `ErrClassCanceled` и пониженный вес 0.2 при единственной `ErrClassCanceled`, защита от деления на ноль при $N_{\text{err\_chains}} == 0$, `contracts.SanitizeFloat`, связывание через `IdentityEdge`, поиск по ключу `FindChainByKey`, сериализация JSON).
  - Покрытие тестами: `coverage: 94.4% of statements` (минимально требуемый порог 80.0% уверенно превышен).
  - Заглушки: `TODO`, `FIXME`, пустые тела функций отсутствуют.
  - Верификатор: `py ./verify_task.py TASK-11` завершён успешно (`[VERIFY PASSED]`, exit code 0).
- **Найденные замечания (если есть):** Замечаний нет. Код чистый, безопасный, полностью детерминированный и соответствует всем требованиям спецификации.
