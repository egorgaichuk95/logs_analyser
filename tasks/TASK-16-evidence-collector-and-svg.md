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

- **Дата выполнения:** 2026-10-28 22:04 UTC
- **Соблюдение цикла TDD (Contract → Tests → Code):**
  1. CONTRACT: Созданы файлы пакетов `collector` и `svgchart` с точными сигнатурами функций `CollectEvidence`, `RenderTimelineSVG`, `RenderTopologySVG` по Blueprint.
  2. RED (TESTS FIRST): Написаны комплексные тесты в `charts_test.go` и `collector_test.go`:
     - Проверка валидности XML (`xml.Unmarshal`) для SVG 900x320 (`RenderTimelineSVG`) и 800x280 (`RenderTopologySVG`), проверка наличия стрелок, маркеров Onset и подсветки `ROOT_CAUSE`.
     - Проверка формирования проверяемых доказательств `E-01..E-NN` через `httptest.NewServer` с эмуляцией ClickHouse, включая fallback на `logs_rca.templates_dict` при очищенной `events_raw` (после `purge-raw`), fallback на шаблон гипотезы, корректное вычисление SHA-256 хеша, генерацию `LogQLSnippet` вида `{service_name=%q, pod=%q} |= %q` и мутацию поля `EvidenceIDs` в `analysis.Hypotheses`.
     - Запуск `go test` в RED фазе зафиксировал ожидаемые падения тестов при компиляции контрактов.
  3. GREEN (CODE): Реализован полный алгоритм сбора доказательств и генерации чистых векторных автономных SVG без TODO и заглушек.
  4. VERIFY: Запущены тесты и верификатор `py ./verify_task.py TASK-16`. Статус: `[VERIFY PASSED]`.
- **Созданные/изменённые файлы:**
  - `services/evidence-service/internal/collector/collector.go` (302 строки)
  - `services/evidence-service/internal/collector/collector_test.go` (380 строк)
  - `services/evidence-service/internal/svgchart/charts.go` (394 строки)
  - `services/evidence-service/internal/svgchart/charts_test.go` (260 строк)
- **Результаты проверок и тестов:**
  - `github.com/local/logs-analyzer/services/evidence-service/internal/collector`: `coverage: 87.3% of statements` (PASS)
  - `github.com/local/logs-analyzer/services/evidence-service/internal/svgchart`: `coverage: 92.7% of statements` (PASS)
  - `verify_task.py TASK-16`: `[VERIFY PASSED]`
- **Заметки по реализации:**
  - Полная поддержка автономного SVG без внешних CSS/JS/CDN библиотек.
  - Строгая последовательная нумерация доказательств (`E-01`, `E-02`...) с привязкой ID к гипотезам.
  - Многоуровневый fallback для сырых логов: `events_raw` -> `templates_dict` -> `TemplateText`.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты (TDD Coverage):**
  - Пакеты и файлы реализации:
    - `services/evidence-service/internal/collector/collector.go`
    - `services/evidence-service/internal/collector/collector_test.go`
    - `services/evidence-service/internal/svgchart/charts.go`
    - `services/evidence-service/internal/svgchart/charts_test.go`
  - Контракты: посимвольное совпадение экспортируемых функций `CollectEvidence`, `RenderTimelineSVG`, `RenderTopologySVG`, структур `EvidenceItem`, `AnalyzeCaseResponse`, констант `contracts.EvidenceKind*` и `contracts.Role*`.
  - Покрытие тестами statements:
    - `github.com/local/logs-analyzer/services/evidence-service/internal/collector`: `coverage: 87.3% of statements` (PASS)
    - `github.com/local/logs-analyzer/services/evidence-service/internal/svgchart`: `coverage: 92.7% of statements` (PASS)
  - Тестирование граничных и специфичных случаев:
    - `TestCollectEvidence_FullFlow`: полный цикл формирования доказательств видов `stat_test`, `raw_log`, `clock_skew`, `topology_edge`, валидация SHA-256, формата LogQLSnippet и заполнения `Hypothesis.EvidenceIDs`.
    - `TestCollectEvidence_PurgedRawFallbackToTemplatesDict`: корректный fallback на `logs_rca.templates_dict` (`sample_redacted`) при очистке сырых логов `events_raw` (`purge-raw`).
    - `TestCollectEvidence_BothPurgedFallbackToHypothesisTemplate`: fallback на `Hypothesis.TemplateText` при отсутствии данных в обеих таблицах ClickHouse.
    - `TestCollectEvidence_NilAndEmptyInput`, `TestCollectEvidence_ContextCanceled`, `TestCollectEvidence_ClickHouseError`, `TestCollectEvidence_EmptyURLAndZeroErrors`: проверка отмены контекста, сетевых сбоев, пустых параметров.
    - `TestRenderTimelineSVG_ValidXML`, `TestRenderTimelineSVG_Empty`: генерация автономного SVG 900x320 с валидацией XML-парсером `xml.Unmarshal`, проверка меток Onset и легенды.
    - `TestRenderTopologySVG_ValidXML`, `TestRenderTopologySVG_Empty`, `TestRenderTopologySVG_HypothesesOnly`, `TestRenderTopologySVG_MultiNodeTwoRows`: генерация автономного SVG 800x280 с подсветкой `ROOT_CAUSE`, ребрами ошибок, маркерами стрелок и раскладкой в 1 или 2 ряда.
  - Автоматическая верификация: `py ./verify_task.py TASK-16` завершился успешно с кодом возврата 0 (`[VERIFY PASSED] TASK-16 passed all automated checks!`).
- **Найденные замечания (если есть):** Нет. Реализация полностью автономна, без внешних зависимостей/CDN/TODO, защищена от NaN через `SanitizeFloat` и деления на ноль, потоково декодирует JSON из ClickHouse и строго соблюдает межсервисные контракты.
