# TASK-14: Сигнал S3 — Кросс-корреляция Lead-Lag с защитой от `NaN` (`./services/rca-engine/internal/timeseries/leadlag.go`)

- **Статус:** `DONE`
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

- **Дата выполнения:** 2026-10-28 21:30 UTC
- **Соблюдение цикла TDD (Contract → Tests → Code):**
  1. **CONTRACT:** В `./services/rca-engine/internal/timeseries/leadlag.go` объявлены структура `LeadLagResult`, вспомогательная функция `pearsonShifted` и экспортируемая функция `ComputeLeadLag` со строгим соответствием Blueprint.
  2. **RED (TESTS FIRST):** В `./services/rca-engine/internal/timeseries/leadlag_test.go` написаны исчерпывающие тесты: сдвинутые на +3 шага импульсы межсервисной задержки (`chat-com` vs `chat-session`), защита от нулевой дисперсии и деления на 0/NaN, изоляция рядов одного сервиса (корреляция только межсервисная), граничные случаи ($N < 3$, пустые ряды, `IsConstantNoise == true`, порог $r \ge 0.6$, неположительный `stepSeconds`), все вариации `pearsonShifted` (положительные, нулевые, отрицательные задержки, граничные длины), каскад распространения A -> B -> C с пенализацией отстающих сервисов ($0.2 \times r_{\max}$), JSON-сериализация и `Hypothesis.Sanitize()`. Первичный запуск завершился с `FAIL` (красная фаза).
  3. **GREEN (CODE):** Реализован полный честный алгоритм в `leadlag.go` без TODO, заглушек и хардкода: выравнивание на временную сетку, поиск $\tau^* \in [1 .. \min(60, N/2)]$, проверка `ssA <= 1e-12 || ssB <= 1e-12`, оборачивание в `contracts.SanitizeFloat`, расчет оценок $S3$ для лидирующих и отстающих сервисов. Все тесты перешли в статус `PASS`.
  4. **VERIFY GATE:** Успешно запущен автоматический верификатор `py ./verify_task.py TASK-14` с вердиктом `[VERIFY PASSED]`.
- **Созданные/изменённые файлы:**
  - `services/rca-engine/internal/timeseries/leadlag.go` (276 строк) — реализация кросс-корреляции Пирсона с временным сдвигом, расчёта ссылок `LeadLagLink` и оценок S3 (`ScoreByTemplate`);
  - `services/rca-engine/internal/timeseries/leadlag_test.go` (543 строки) — исчерпывающие TDD-тесты контрактов, граничных условий и математической безопасности.
- **Результаты проверок и тестов:**
  - `go test -v -cover ./internal/timeseries/...` — все тесты пройдены (`PASS`).
  - Покрытие кода пакета: `coverage: 93.2% of statements` (минимально требуемый порог: 80.0%).
  - Автоматический скрипт: `py ./verify_task.py TASK-14` завершился со статусом `[VERIFY PASSED]`.
- **Заметки по реализации:**
  - Точное посимвольное соответствие контрактам `LeadLagResult`, `LeadLagLink` и `ComputeLeadLag`.
  - Защита от деления на 0 при нулевой дисперсии (`ssA <= 1e-12 || ssB <= 1e-12`) возвращает `0.0`.
  - Все вещественные значения защищены через `contracts.SanitizeFloat`.
  - Шаблоны одного сервиса пропускаются; корреляция рассчитывается исключительно между разными сервисами.
  - Отрицательные задержки поддержаны в `pearsonShifted`, обеспечивая симметрию `pearsonShifted(b, a, -tau) == pearsonShifted(a, b, tau)`.
  - Ссылки `LinksByTemplate` детерминированно сортируются по убыванию `CorrelationR` и возрастанию `LagSeconds`.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты (TDD Coverage):**
  - Файлы реализации и тестов:
    - `services/rca-engine/internal/timeseries/leadlag.go` (277 строк)
    - `services/rca-engine/internal/timeseries/leadlag_test.go` (544 строки)
  - Контракты: посимвольное совпадение `LeadLagResult`, `contracts.LeadLagLink` (`PeerService`, `PeerTemplate`, `LagSeconds`, `CorrelationR`), сигнатуры `ComputeLeadLag(stats map[uint64]TemplateStats, stepSeconds int) LeadLagResult`.
  - Математическая безопасность: строгая защита от нулевой дисперсии и деления на ноль (`ssA <= 1e-12 || ssB <= 1e-12`, `denom <= 1e-12`), ограничение коэффициента в интервале `[-1.0, 1.0]`, оборачивание всех float-значений в `contracts.SanitizeFloat`.
  - Полнота TDD-тестов: 7 комплексных тест-кейсов, покрывающих сдвиг импульсов (+3 шага), нулевую дисперсию, изоляцию рядов одного сервиса, граничные условия ($N < 3$, пустые ряды, $r < 0.6$), функцию `pearsonShifted` (симметрия, перекрытия), цепочки распространения ($A \to B \to C$) и JSON-сериализацию с `Hypothesis.Sanitize()`.
  - Измеренное покрытие:
    - Пакет `internal/timeseries`: `coverage: 93.2% of statements` (порог: >= 80%).
    - Покрытие функций `leadlag.go`: `pearsonShifted` — 93.2%, `ComputeLeadLag` — 96.1%.
  - Автоматический верификатор `py ./verify_task.py TASK-14` завершился со статусом `[VERIFY PASSED]` (код 0).
- **Найденные замечания (если есть):** Замечаний нет. Реализация полностью соответствует Blueprint, архитектурным требованиям и чек-листу REVIEWER_GUIDE.md.
