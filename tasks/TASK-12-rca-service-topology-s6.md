# TASK-12: Сигнал S6 — Авто-построение топологии сервисов и `GraphRootScore` (`./services/rca-engine/internal/topology`)

- **Статус:** `TODO`
- **Зависимости:** `TASK-11`
- **Область видимости (Scope):**
  - Читать: `./pkg/contracts/`, `./services/rca-engine/internal/idgraph/`
  - Создавать/редактировать:
    - `./services/rca-engine/internal/topology/topology.go`
    - `./services/rca-engine/internal/topology/topology_test.go`
  - **НЕ трогать:** другие папки

---

## 1. Цель задачи

Реализовать пакет `topology` (Сигнал S6), который автоматически строит ориентированный граф зависимостей сервисов `Caller → Callee` (из URL/хостов в ошибках `service_edges`, из переходов в цепочках `idgraph` и из `service_profiles.yaml`) и рассчитывает для каждого сервиса оценку `GraphRootScore ∈ [0.0 .. 1.0]`.

---

## 2. Точные сигнатуры и формула (Blueprint)

### 2.1. `./services/rca-engine/internal/topology/topology.go`
```go
package topology

import (
	"github.com/local/logs-analyzer/pkg/contracts"
)

type GraphResult struct {
	Edges            []contracts.ServiceEdge
	ServiceRootScore map[string]float64 // key: service -> GraphRootScore [0..1]
}

// ComputeTopology агрегирует рёбра вызовов и рассчитывает S6 (GraphRootScore) для каждого сервиса.
// anomalousServices — множество сервисов, в которых обнаружены ошибки в окне инцидента.
func ComputeTopology(
	explicitEdges []contracts.ServiceEdge,
	chainEdges []contracts.ServiceEdge,
	anomalousServices map[string]bool,
) GraphResult
```

**Точная формула расчёта `ServiceRootScore[S]`:**
Для каждого сервиса `S`:
- Если `!anomalousServices[S]` → `0.0`.
- Пусть:
  - $D_{\text{anom}}(S)$ — число аномальных сервисов `Callee`, которые вызывает `S` (исходящие рёбра к аномальным узлам);
  - $U_{\text{anom}}(S)$ — число аномальных сервисов `Caller`, которые вызывают `S` (входящие рёбра от аномальных узлов).
- Правила оценки:
  1. Если $D_{\text{anom}}(S) == 0$ и $U_{\text{anom}}(S) > 0$ (сервис `S` никого из аномальных не вызывает, но от него зависят сбоящие соседи — классическое «дно каскада»!) → `1.0`.
  2. Если $D_{\text{anom}}(S) == 0$ и $U_{\text{anom}}(S) == 0$ (изолированный аномальный сервис) → `0.75`.
  3. Если $D_{\text{anom}}(S) > 0$ и $U_{\text{anom}}(S) > 0$ (промежуточное звено каскада `PROPAGATION`) → `0.35`.
  4. Если $D_{\text{anom}}(S) > 0$ и $U_{\text{anom}}(S) == 0$ (входной шлюз `SYMPTOM`, вызывающий сбоящий downstream) → `0.15`.
- Обязательно обернуть результат в `contracts.SanitizeFloat`.

### 2.2. `./services/rca-engine/internal/topology/topology_test.go`
Проверить на каскаде `api-gateway -> chat-session -> chat-com` (все три в `anomalousServices`):
- `assert res.ServiceRootScore["chat-com"] == 1.0`
- `assert res.ServiceRootScore["chat-session"] == 0.35`
- `assert res.ServiceRootScore["api-gateway"] == 0.15`

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}:/workspace" -v rca_go_cache:/go/pkg/mod -v rca_go_build:/root/.cache/go-build -w /workspace/services/rca-engine golang:1.23-alpine go test -v -cover ./internal/topology/...
py ./verify_task.py TASK-12
```

---

## Отчёт о выполнении (заполняется субагентом-воркером)

- **Дата выполнения:** 2026-10-28 21:05 UTC
- **Созданные/изменённые файлы:**
  - `services/rca-engine/internal/topology/topology.go` (128 строк) — реализация структуры `GraphResult` и функции `ComputeTopology` для агрегации рёбер вызовов и расчёта сигнала S6 (`GraphRootScore`);
  - `services/rca-engine/internal/topology/topology_test.go` (349 строк) — 10 модульных тестов: каскад вызовов `api-gateway -> chat-session -> chat-com`, изолированный аномальный сервис, здоровые сервисы, агрегация и дедупликация рёбер, ромбовидный граф, циклы, фильтрация петель на себя/пустых имён, защита `SanitizeFloat` и JSON-сериализация, граничные и nil-входы, объединение метаданных и детерминированная сортировка.
- **Результаты проверок и тестов:**
  - RED phase: все тесты написаны первыми и провалились на заглушке (`FAIL`).
  - GREEN phase: после реализации все тесты успешно пройдены (`PASS`).
  - Покрытие кода: `coverage: 100.0% of statements` (при минимально требуемом пороге 80.0%).
  - Автоматический гейт: `py ./verify_task.py TASK-12` завершился со статусом `[VERIFY PASSED]`.
- **Заметки по реализации:**
  - Алгоритм честно агрегирует `explicitEdges` и `chainEdges` по ключу `caller->callee`, суммируя `CallCount` и `ErrorCount`, сохраняя `CaseID` и `EvidenceSource`.
  - Отфильтрованы некорректные рёбра (пустые имена сервисов и петли вызова сервиса на самого себя).
  - Оценка `ServiceRootScore` рассчитывается строго по формуле Blueprint с разделением на $D_{\text{anom}}$ (исходящие рёбра к аномальным узлам) и $U_{\text{anom}}$ (входящие рёбра от аномальных узлов): дно каскада (1.0), изолированный узел (0.75), промежуточное звено (0.35), входной шлюз (0.15), здоровые сервисы (0.0). Все результаты защищены через `contracts.SanitizeFloat`.
  - Рёбра в `GraphResult.Edges` отсортированы детерминированно по паре `(CallerService, CalleeService)`.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты (TDD Coverage):**
  - Проверены файлы `services/rca-engine/internal/topology/topology.go` и `services/rca-engine/internal/topology/topology_test.go`.
  - Посимвольное совпадение контрактов Blueprint: структура `GraphResult`, экспортируемая функция `ComputeTopology`, сигнатуры и типы `contracts.ServiceEdge`, оборачивание в `contracts.SanitizeFloat`.
  - Полнота TDD-тестов: 10 детальных сценариев покрывают все контрактные требования (трехзвенный каскад `api-gateway -> chat-session -> chat-com`, изолированный сбоящий сервис, неаномальные сервисы, слияние и дедупликация рёбер, ромбовидные графы, циклические вызовы, фильтрация self-loops и пустых имен, защита от NaN/Inf при JSON-сериализации, пустые/nil входы, слияние метаданных и детерминированная сортировка).
  - Покрытие тестами: `coverage: 100.0% of statements` (порог >= 80.0%).
  - Автоматический верификатор `py ./verify_task.py TASK-12` успешно пройден (`[VERIFY PASSED]`, код возврата `0`).
- **Найденные замечания (если есть):** Замечаний нет. Реализация чистая, полностью автономная, без TODO/заглушек и строго соответствует формуле Blueprint.
