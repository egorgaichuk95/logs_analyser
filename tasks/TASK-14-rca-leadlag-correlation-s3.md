# TASK-14: Сигнал S3 — Кросс-корреляция Lead-Lag с защитой от `NaN` (`./services/rca-engine/internal/timeseries/leadlag.go`)

- **Статус:** `TODO`
- **Зависимости:** `TASK-13`
- **Область видимости (Scope):**
  - Читать: `./pkg/contracts/`, `./services/rca-engine/internal/timeseries/anomaly.go`
  - Создавать/редактировать:
    - `./services/rca-engine/internal/timeseries/leadlag.go`
    - `./services/rca-engine/internal/timeseries/leadlag_test.go`
  - **НЕ трогать:** другие папки

---

## 1. Цель задачи

Реализовать расчёт сигнала **S3 (Lead-Lag Cross-Correlation)**: для каждой пары аномальных временных рядов из *разных* сервисов найти сдвиг $\tau \in [-60 .. +60]$ секунд, максимизирующий коэффициент корреляции Пирсона $r(\tau)$, со строгой защитой от деления на ноль (нулевой дисперсии ряда).

---

## 2. Точные сигнатуры и формула (Blueprint)

### 2.1. `./services/rca-engine/internal/timeseries/leadlag.go`
```go
package timeseries

import (
	"github.com/local/logs-analyzer/pkg/contracts"
)

type LeadLagResult struct {
	LinksByTemplate map[uint64][]contracts.LeadLagLink
	ScoreByTemplate map[uint64]float64 // S3 [0..1]
}

// ComputeLeadLag вычисляет сдвиговую корреляцию между нешумовыми рядами разных сервисов.
func ComputeLeadLag(stats map[uint64]TemplateStats, stepSeconds int) LeadLagResult
```

**Точный алгоритм с защитой от `NaN`:**
1. Выровнять все нешумовые ряды (`!IsConstantNoise`) на единую временную сетку `[minTs .. maxTs]` с шагом `stepSeconds` (например, `1 с` или `60 с`), получив массив `vec []float64` одинаковой длины $N$ для каждого `TemplateID`.
2. Если $N < 3$, вернуть нулевые оценки `0.0`.
3. Функция `pearsonShifted(a, b []float64, lagSteps int) float64`:
   - Для положительного `lagSteps > 0` сравнивает `a[0 : N-lagSteps]` и `b[lagSteps : N]` (ряд `a` опережает ряд `b`!).
   - Считает средние $\bar{a}, \bar{b}$ и суммы квадратов отклонений $SS_a = \sum (a_i - \bar{a})^2$, $SS_b = \sum (b_i - \bar{b})^2$.
   - **КРИТИЧЕСКАЯ ПРОВЕРКА:** `if ssA <= 1e-12 || ssB <= 1e-12 { return 0.0 }` (исключает деление на ноль и `NaN`!).
   - Возвращает `contracts.SanitizeFloat(cov / math.Sqrt(ssA * ssB))`.
4. Для каждой пары шаблонов $(A, B)$ с `serviceA != serviceB`:
   - Найти $\tau^* \in [1 .. \min(60, N/2)]$, при котором $r(\tau^*)$ максимален.
   - Если $r(\tau^*) \ge 0.6$, добавить в `LinksByTemplate[A]` запись `contracts.LeadLagLink{PeerService: serviceB, PeerTemplate: B, LagSeconds: tauStar * stepSeconds, CorrelationR: rMax}`.
5. Для каждого шаблона `A`:
   - если он опережает хотя бы один другой сервис с $r_{\max} \ge 0.6$ и сам не отстаёт от других → `ScoreByTemplate[A] = rMax`;
   - если он отстаёт от другого сервиса → `ScoreByTemplate[A] = 0.2 * rMax`;
   - иначе → `0.0`.

### 2.2. `./services/rca-engine/internal/timeseries/leadlag_test.go`
Проверить:
1. Два идентичных импульса в сервисе `A` и сервисе `B`, где импульс в `B` сдвинут на `+3` шага относительно `A`:
   - `assert res.LinksByTemplate[tplA][0].LagSeconds == 3`
   - `assert res.LinksByTemplate[tplA][0].CorrelationR > 0.95`
   - `assert res.ScoreByTemplate[tplA] > res.ScoreByTemplate[tplB]`
2. Постоянный ряд из одних нулей `[0, 0, 0, 0, 0]` не вызывает `NaN` (возвращает `0.0`).

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}:/workspace" -v rca_go_cache:/go/pkg/mod -v rca_go_build:/root/.cache/go-build -w /workspace/services/rca-engine golang:1.23-alpine go test -v -cover ./internal/timeseries/...
py ./verify_task.py TASK-14
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
