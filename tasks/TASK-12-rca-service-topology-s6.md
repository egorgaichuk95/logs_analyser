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

- **Дата выполнения:** *(не заполнено)*
- **Созданные/изменённые файлы:** *(не заполнено)*
- **Результаты проверок и тестов:** *(не заполнено)*
- **Заметки по реализации:** *(не заполнено)*

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `PENDING` *(выбрать: `APPROVED` | `CHANGES_REQUESTED`)*
- **Проверенные контракты и тесты:** *(не заполнено)*
- **Найденные замечания (если есть):** *(не заполнено)*
