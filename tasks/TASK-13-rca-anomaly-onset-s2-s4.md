# TASK-13: Сигналы S2 и S4 — Сравнение с Baseline, отсев шума и поиск точки начала Onset (`./services/rca-engine/internal/timeseries`)

- **Статус:** `TODO`
- **Зависимости:** `TASK-12`
- **Область видимости (Scope):**
  - Читать: `./pkg/contracts/`
  - Создавать/редактировать:
    - `./services/rca-engine/internal/timeseries/anomaly.go`
    - `./services/rca-engine/internal/timeseries/anomaly_test.go`
  - **НЕ трогать:** другие папки

---

## 1. Цель задачи

Реализовать расчёт сигналов **S4 (Novelty & Noise Separation)** и **S2 (Onset Detection)**:
1. Сравнить частоту каждого шаблона `(service, template_id)` в окне инцидента с окном базовой линии (`baseline`).
2. Отфильтровать постоянный фоновый шум (например, `Operator was not found in cache` в `chat-com` с частотой 190/с).
3. Определить точный момент начала аномалии `OnsetTime` и рассчитать `OnsetScore ∈ [0.0 .. 1.0]` и `NoveltyScore ∈ [0.0 .. 1.0]` со строгой защитой от `NaN` / `Inf`.

---

## 2. Точные сигнатуры и математические формулы (Blueprint)

### 2.1. `./services/rca-engine/internal/timeseries/anomaly.go`
```go
package timeseries

import (
	"math"
	"time"
	"github.com/local/logs-analyzer/pkg/contracts"
)

type BucketPoint struct {
	Ts    time.Time
	Count uint64
}

type TemplateStats struct {
	Service            string
	TemplateID         uint64
	BaselineRatePerMin float64
	IncidentRatePerMin float64
	GrowthRatio        float64
	PoissonPValue      float64
	IsConstantNoise    bool
	OnsetTime          time.Time
	OnsetScore         float64 // S2 [0..1]
	NoveltyScore       float64 // S4 [0..1]
	Buckets            []BucketPoint
}

// AnalyzeSeries рассчитывает S2 и S4 для набора временных рядов шаблонов.
// Если baselineDur <= 0, автоматически берёт первые 20% интервала до первого скачка как baseline.
func AnalyzeSeries(
	series map[uint64][]BucketPoint,
	serviceByTpl map[uint64]string,
	historicalBaseRatePerMin map[uint64]float64,
	baselineEnd time.Time,
) map[uint64]TemplateStats
```

**Точные математические формулы (обязательно использовать `contracts.SanitizeFloat`):**
1. **Частоты `BaselineRatePerMin` ($\lambda_{\text{base}}$) и `IncidentRatePerMin` ($\lambda_{\text{inc}}$):**
   - Если в текущем кейсе есть точки с `Ts < baselineEnd`, считать $\lambda_{\text{base}} = \frac{\text{sum\_base}}{\max(\text{base\_minutes}, 0.5)}$.
   - Если точек до `baselineEnd` нет, но в `historicalBaseRatePerMin[tpl]` есть значение $> 0$, взять его как $\lambda_{\text{base}}$.
   - $\lambda_{\text{inc}} = \frac{\text{sum\_inc}}{\max(\text{inc\_minutes}, 0.5)}$.
2. **Классификация фонового шума (`IsConstantNoise`) и расчёт `NoveltyScore` (S4):**
   - Если $\lambda_{\text{base}} == 0$ и $\text{sum\_inc} > 0$:
     - шаблон **новый**! `GrowthRatio = math.Min(lambdaInc*10.0, 100.0)`, `NoveltyScore = 1.0`, `IsConstantNoise = false`.
   - Иначе если $\lambda_{\text{base}} > 0$:
     - `GrowthRatio = contracts.SanitizeFloat(lambdaInc / lambdaBase)`.
     - Если $\lambda_{\text{base}} \ge 5.0$ и `GrowthRatio < 1.5` → **`IsConstantNoise = true`**, `NoveltyScore = 0.0` (постоянный шум вроде `cache:98`!).
     - Иначе: `NoveltyScore = contracts.SanitizeFloat(math.Min(1.0, math.Log10(math.Max(1.0, GrowthRatio))/1.5))`.
3. **Аппроксимация $p$-value Пуассона (`PoissonPValue`):**
   - Вычислить $z$-оценку: $z = \frac{\lambda_{\text{inc}} - \lambda_{\text{base}}}{\sqrt{\max(\lambda_{\text{base}}, 0.1)}}$.
   - Если $z \le 0$ → `PoissonPValue = 1.0`. Иначе `PoissonPValue = contracts.SanitizeFloat(0.5 * math.Erfc(z / math.Sqrt2))`.
4. **Определение `OnsetTime` и `OnsetScore` (S2):**
   - Для каждого нешумового ряда (`!IsConstantNoise`) отсортировать `Buckets` по `Ts`.
   - Порог срабатывания бакета: `threshold = math.Max(1.0, meanBasePerBucket + 3.0*stdBasePerBucket)`.
   - Найти первый бакет в окне инцидента с `float64(b.Count) >= threshold` — его `b.Ts` становится `OnsetTime`.
   - Среди всех нешумовых кандидатов найти минимальное время начала $t_{\min} = \min(\text{OnsetTime})$.
   - Для каждого нешумового кандидата:
     $$\Delta t_{\text{sec}} = \max(0.0, \text{OnsetTime.Sub}(t_{\min}).\text{Seconds}())$$
     $$\text{OnsetScore} = \text{SanitizeFloat}\left(\exp\left(-\frac{\Delta t_{\text{sec}}}{15.0}\right)\right)$$
   - Для шумовых кандидатов (`IsConstantNoise == true`) → `OnsetScore = 0.0`.

### 2.2. `./services/rca-engine/internal/timeseries/anomaly_test.go`
Проверить:
1. Ряд `cache:98` (`190/мин` в baseline и `195/мин` в инциденте) получает `IsConstantNoise == true`, `NoveltyScore == 0.0`, `OnsetScore == 0.0`.
2. Ряд `pool_exhausted` (`0` в baseline, всплеск в `08:20:00Z`) и ряд `upstream_timeout` (`0` в baseline, всплеск в `08:20:15Z`):
   - `pool_exhausted`: `NoveltyScore == 1.0`, `OnsetScore == 1.0`;
   - `upstream_timeout`: `OnsetScore ≈ 0.3678` ($\exp(-1)$).

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}:/workspace" -v rca_go_cache:/go/pkg/mod -v rca_go_build:/root/.cache/go-build -w /workspace/services/rca-engine golang:1.23-alpine go test -v -cover ./internal/timeseries/...
py ./verify_task.py TASK-13
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
