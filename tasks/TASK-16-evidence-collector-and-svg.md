# TASK-16: Сборщик доказательств `E-01..E-NN` и генератор SVG-графиков (`./services/evidence-service`)

- **Статус:** `TODO`
- **Зависимости:** `TASK-04`
- **Область видимости (Scope):**
  - Читать: `./pkg/contracts/`
  - Создавать/редактировать:
    - `./services/evidence-service/internal/collector/collector.go`
    - `./services/evidence-service/internal/svgchart/charts.go`
    - `./services/evidence-service/internal/svgchart/charts_test.go`
  - **НЕ трогать:** другие папки

---

## 1. Цель задачи

Реализовать в `evidence-service`:
1. Пакет `collector` — извлечение проверяемых доказательств `E-01 .. E-NN` (`contracts.EvidenceItem`) из ClickHouse для каждой гипотезы и аномалии (`raw_log` с `SHA-256`, `source_file` и `source_record_idx`, `stat_test`, `clock_skew`, `topology_edge`), которые сохраняются в отчёт и позволяют проверять выводы даже после удаления сырых логов.
2. Пакет `svgchart` — генерацию чистых векторных SVG-диаграмм (`timeline.svg` и `topology.svg`) без внешних C/JS зависимостей.

---

## 2. Точные сигнатуры и реализация (Blueprint)

### 2.1. `./services/evidence-service/internal/collector/collector.go`
```go
package collector

import (
	"context"
	"github.com/local/logs-analyzer/pkg/contracts"
)

// CollectEvidence собирает доказательства E-01..E-NN для гипотез и системных находок кейса
// и прописывает собранные ID в поле EvidenceIDs каждой гипотезы в analysis.Hypotheses.
func CollectEvidence(ctx context.Context, chHTTPURL string, analysis *contracts.AnalyzeCaseResponse) ([]contracts.EvidenceItem, error)
```

**Правила формирования `EvidenceItem`:**
- Нумерация строго последовательная: `fmt.Sprintf("E-%02d", idx)` (`"E-01"`, `"E-02"`, ...).
- Для каждой гипотезы:
  1. Элемент `Kind = "stat_test"`: `Summary = fmt.Sprintf("Статистика шаблона %s: baseline=%.2f/мин, инцидент=%.2f/мин, рост=%.1fx, p-value=%.4g", h.Service, h.BaselineRatePerMin, h.IncidentRatePerMin, h.GrowthRatio, h.PoissonPValue)`.
  2. Элемент(ы) `Kind = "raw_log"`: запрос до 3 сырых строк этого `(case_id, service, template_id)` из `logs_rca.events_raw` (а если `events_raw` уже очищена — взятие `sample_redacted` из `logs_rca.templates_dict`!), вычисление `sha256.Sum256([]byte(rawRedacted))` и формирование `LogQLSnippet = fmt.Sprintf("{service_name=%q, pod=%q} |= %q", h.Service, h.DominantPod, shortToken)`.
- Для каждого `ClockSkewFinding`: элемент `Kind = "clock_skew"` с примером расхождения времени.

### 2.2. `./services/evidence-service/internal/svgchart/charts.go`
```go
package svgchart

import "github.com/local/logs-analyzer/pkg/contracts"

// RenderTimelineSVG генерирует валидный автономный SVG (900x320) таймлайна событий с отметками Onset
func RenderTimelineSVG(points []contracts.TimelinePoint, hypotheses []contracts.Hypothesis) string

// RenderTopologySVG генерирует валидный автономный SVG (800x280) графа сервисов с подсветкой ROOT_CAUSE
func RenderTopologySVG(edges []contracts.ServiceEdge, hypotheses []contracts.Hypothesis) string
```

### 2.3. `./services/evidence-service/internal/svgchart/charts_test.go`
Проверить, что `RenderTimelineSVG` и `RenderTopologySVG` возвращают валидный XML (`strings.HasPrefix(svg, "<svg")` и `strings.HasSuffix(strings.TrimSpace(svg), "</svg>")`), содержащий названия сервисов и роли.

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}:/workspace" -v rca_go_cache:/go/pkg/mod -v rca_go_build:/root/.cache/go-build -w /workspace/services/evidence-service golang:1.23-alpine go test -v -cover ./...
py ./verify_task.py TASK-16
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
