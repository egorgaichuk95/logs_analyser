# TASK-08: Быстрый шаблонизатор сообщений Drain с хэш-кэшем (`./services/ingest-service/internal/drain`)

- **Статус:** `DONE`
- **Зависимости:** `TASK-07`
- **Область видимости (Scope):**
  - Читать: `./pkg/contracts/`
  - Создавать/редактировать:
    - `./services/ingest-service/internal/drain/drain.go`
    - `./services/ingest-service/internal/drain/drain_test.go`
  - **НЕ трогать:** другие папки

---

## 1. Цель задачи

Реализовать потокобезопасный шаблонизатор логов `drain.Miner` (`package drain`), сочетающий:
1. Предварительную нормализацию переменных токенов (UUID, 24–64-символьные hex-идентификаторы сессий/трейсов, числа, IP-адреса, строковые литералы в кавычках и URL query-строки).
2. Быстрый хэш-кэш по преднормализованной строке (так как в `chat-com` 90% строк — одно и то же сообщение `Operator (operator_id=55754) was not found in cache`, кэш обрабатывает их за $O(1)$ без прохода по дереву).
3. Дерево кластеризации Drain (глубина `4`, порог сходства токенов `0.6`, максимум `100` дочерних узлов), заменяющее отличающиеся токены одной длины на `<*>`.

---

## 2. Точные сигнатуры и алгоритм (Blueprint)

### 2.1. `./services/ingest-service/internal/drain/drain.go`
```go
package drain

import (
	"sync"
	"time"
	"github.com/local/logs-analyzer/pkg/contracts"
)

type Miner struct {
	mu       sync.Mutex
	// внутренние структуры дерева и кэша
}

func NewMiner() *Miner

// Mine принимает событие ev, вычисляет для него TemplateID и шаблон текста,
// записывает ev.TemplateID и возвращает TemplateRecord для сохранения в справочник templates_dict.
func (m *Miner) Mine(ev *contracts.NormalizedEvent) contracts.TemplateRecord
```

**Точные правила нормализации перед деревом Drain (`preMask`):**
Применять в указанном порядке к `baseText` (где `baseText = ev.Message`; если `ev.ErrorMsg != ""` и не входит в `ev.Message`, то `baseText = ev.Message + " | " + ev.ErrorMsg`):
1. Заменить query-параметры в URL `\?[^\s"']+` → `?<query>`.
2. Заменить UUID `[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}` → `<uuid>`.
3. Заменить длинные hex-строки (20..64 символов `[0-9a-fA-F]`, содержащие хотя бы одну цифру и букву, либо `0x[0-9a-fA-F]+`) → `<hex>`.
4. Заменить IPv4 `\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b` → `<ip>`.
5. Заменить числа `\b\d+(?:\.\d+)?\b` → `<num>`.
6. Обрезать длину шаблона до `512` символов.
7. Вычислить `TemplateID = fnv64a(ev.Service + "|" + ev.Level + "|" + ev.SourcePlace + "|" + finalTemplate)`.

### 2.2. `./services/ingest-service/internal/drain/drain_test.go`
Проверить на реальных примерах из `chat-com`:
1. `Operator (operator_id=55754) was not found in cache` и `Operator (operator_id=936) was not found in cache` дают одинаковый `TemplateID` и шаблон `Operator (operator_id=<num>) was not found in cache`.
2. `Session id=7eab3cab9d1bf2a4d5ab6f7c, pvid=123, dep=(,az)` и `Session id=9f8dfebbbe1d2c3ab4f5b6a7, pvid=456, dep=(,az)` дают один и тот же `TemplateID` с `<hex>` и `<num>`.

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}:/workspace" -v rca_go_cache:/go/pkg/mod -v rca_go_build:/root/.cache/go-build -w /workspace/services/ingest-service golang:1.23-alpine go test -v -cover ./internal/drain/...
py ./verify_task.py TASK-08
```

---

## Отчёт о выполнении (заполняется субагентом-воркером)

- **Дата выполнения:** 2026-10-28 20:02 UTC
- **Соблюдение цикла TDD (Contract -> Tests -> Code):**
  1. CONTRACT: Создана структура `Miner`, конструктор `NewMiner() *Miner` и метод `Mine(ev *contracts.NormalizedEvent) contracts.TemplateRecord` по Blueprint.
  2. RED: Написаны полные тесты в `drain_test.go` (Blueprint кейсы chat-com `operator_id=<num>` и Session `id=<hex>`, правила preMask, дерево Drain глубиной 4 с порогом 0.6 и лимитом 100 детей, O(1) хэш-кэш, параллельные вызовы из 30 горутин, граничные случаи). Первичный запуск тестов подтвердил падение (RED).
  3. GREEN: Полностью реализован модуль `drain.go` без TODO/заглушек: точные 5 правил преднормализации, дерево кластеризации Drain с заменой вариативных токенов на `<*>`, потокобезопасный хэш-кэш и FNV64a хэширование `TemplateID`.
  4. VERIFY: Пройден гейт `verify_task.py TASK-08`.
- **Созданные/изменённые файлы:**
  - `./services/ingest-service/internal/drain/drain.go` (272 строки)
  - `./services/ingest-service/internal/drain/drain_test.go` (496 строк)
- **Результаты проверок и тестов:**
  - `go test -v -cover ./internal/drain/...`: `PASS`, покрытие `94.1% of statements` (порог `>= 80%`).
  - `py ./verify_task.py TASK-08`: `[VERIFY PASSED] TASK-08 passed all automated checks!`.
- **Заметки по реализации:**
  - `preMask` выполняет в точном порядке замену query-параметров (`?<query>`), UUID (`<uuid>`), hex-идентификаторов 20..64 символов и 0x... (`<hex>`), IPv4 с портами (`<ip>`), чисел (`<num>`), а также обрезку до 512 рун с сохранением UTF-8.
  - Дерево кластеризации Drain использует глубину 4 и лимит 100 детей на узел с fallback на `*`, корректно вычисляет сходство и обновляет токены кластера на `<*>`.
  - Хэш-кэш гарантирует O(1) для повторяющихся преднормализованных сообщений (типично для `chat-com`).
  - Метод `Mine` потокобезопасен (`sync.Mutex`), заполняет `ev.TemplateID` и возвращает `contracts.TemplateRecord` с обновлением `FirstSeen` и `LastSeen`.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты (TDD Coverage):**
  - Файлы: `./services/ingest-service/internal/drain/drain.go`, `./services/ingest-service/internal/drain/drain_test.go`.
  - Полнота контрактов: Структура `Miner`, методы `NewMiner()` и `Mine(ev *contracts.NormalizedEvent) contracts.TemplateRecord` точно соответствуют Blueprint и `pkg/contracts`. Реализованы все 5 правил `preMask`, FNV64a хэширование `TemplateID`, ограничение длины шаблона 512 рун (UTF-8 safe), дерево кластеризации Drain глубиной 4 с порогом 0.6 и лимитом 100 детей, O(1) хэш-кэш для повторяющихся сообщений, потокобезопасность `sync.Mutex`.
  - Результаты тестов: Запуск `go test -v -cover ./internal/drain/...` успешен, утверждены Blueprint кейсы для `chat-com` (`operator_id=<num>`), `chat-session` (`id=<hex>`), subtests preMask (URL query, UUID, 0x/длинный hex, IPv4, num), дерево Drain и слияние в `<*>`, граница сходства < 0.6, ограничение maxChildren, O(1) кэш, параллельное исполнение 30 горутин и граничные кейсы (nil event, truncation, Cyrillic, zero timestamp, RawRedacted fallback).
  - Покрытие кода: `94.1% of statements` (без кэша `94.9% of statements`, порог `>= 80%`).
  - `py ./verify_task.py TASK-08`: `[VERIFY PASSED] TASK-08 passed all automated checks!`.
- **Найденные замечания (если есть):** Нет. Замечаний и заглушек не обнаружено, код полностью готов к интеграции.
