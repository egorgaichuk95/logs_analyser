# TASK-05: Потоковый чтец файлов логов (`./services/ingest-service/internal/reader`)

- **Статус:** `IN_REVIEW`
- **Зависимости:** `TASK-04`
- **Область видимости (Scope):**
  - Читать: `./pkg/contracts/`
  - Создавать/редактировать:
    - `./services/ingest-service/internal/reader/stream_reader.go`
    - `./services/ingest-service/internal/reader/stream_reader_test.go`
  - **НЕ трогать:** другие папки

---

## 1. Цель задачи

Реализовать потоковый чтец файлов логов (`package reader`) без загрузки файлов целиком в оперативную память (строго запрещены `os.ReadFile` и `io.ReadAll` на входном потоке логов). Поддержать форматы:
1. Корневой JSON-массив объектов `[{ "timestamp": "...", "date": "...", "line": "...", "fields": {...} }]` (основной формат скрипта пользователя `*_part-0024.json`).
2. Построчный JSONL / NDJSON (`.jsonl`, `.ndjson` — как в формате `RawScriptRecord`, так и в формате `{"ts":"...","ts_ns":123,"labels":{...},"line":"..."}`).
3. Архивы `.gz` (`gzip.NewReader`) и `.zip` (`archive/zip` — поочерёдное потоковое чтение всех `.json` и `.jsonl` внутри архива).
4. Расчёт `SHA-256` файла (`ComputeFileSHA256(path string) (string, int64, error)`) для защиты от повторного импорта.

---

## 2. Точные сигнатуры и алгоритм (Blueprint)

### 2.1. `./services/ingest-service/internal/reader/stream_reader.go`
```go
package reader

import (
	"context"
	"github.com/local/logs-analyzer/pkg/contracts"
)

// RecordCallback вызывается для каждой прочитанной записи.
// sourceFile — базовое имя файла, recordIdx — порядковый индекс записи в файле (начиная с 0).
type RecordCallback func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error

// ComputeFileSHA256 потоково считает SHA-256 файла (64 hex символа) и его размер в байтах через io.Copy.
func ComputeFileSHA256(path string) (string, int64, error)

// StreamPath потоково читает файл по пути path (.json, .jsonl, .gz, .zip) и вызывает emit для каждой записи.
func StreamPath(ctx context.Context, path string, emit RecordCallback) error
```

**Детали реализации `StreamPath`:**
- Для `.json` (или `.json.gz`): обернуть `io.Reader` в `bufio.NewReaderSize(r, 64*1024)`. Пропустить пробельные символы, посмотреть первый байт (`Peek(1)`):
  - если первый байт `'['`: использовать `dec := json.NewDecoder(br)`, прочитать открывающий токен `dec.Token()`, затем в цикле `for dec.More() { var rec contracts.RawScriptRecord; if err := dec.Decode(&rec); err != nil { return err }; emit(rec, baseName, idx); idx++ }`;
  - если первый байт `'{'`: читать построчно через `bufio.Scanner` с буфером `4 * 1024 * 1024` (4 МБ на одну строку, так как строки с JWT и стектрейсами могут превышать стандартные 64 КБ!).
- Для `.jsonl`: если в строке вместо `timestamp`/`fields` присутствуют `ts_ns` и `labels`, сконвертировать `ts_ns` в строковый `Timestamp: strconv.FormatInt(tsNs, 10)` и `labels` в `Fields`.

### 2.2. `./services/ingest-service/internal/reader/stream_reader_test.go`
Написать тесты:
1. Потоковое чтение JSON-массива из 3 элементов `RawScriptRecord` во временном файле `.json`.
2. Потоковое чтение того же массива, сжатого в `.json.gz` и упакованного в `.zip`.
3. Чтение строки длиной `200 КБ` (проверка увеличенного буфера).
4. Проверка `ComputeFileSHA256` (возвращает ровно 64 hex-символа).

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}:/workspace" -v rca_go_cache:/go/pkg/mod -v rca_go_build:/root/.cache/go-build -w /workspace/services/ingest-service golang:1.23-alpine go test -v -cover ./internal/reader/...
py ./verify_task.py TASK-05
```

---

## Отчёт о выполнении (заполняется субагентом-воркером)

- **Дата выполнения:** 2026-10-28 19:16 UTC
- **Соблюдение цикла TDD (Contract → Tests → Code):**
  - **Шаг 1 (Contract):** Создан файл `stream_reader.go` с точным соответствием Blueprint (`RecordCallback`, `ComputeFileSHA256`, `StreamPath`).
  - **Шаг 2 (RED Tests First):** Написан исчерпывающий набор тестов в `stream_reader_test.go` (15 тестов): JSON-массив, JSONL в форматах `RawScriptRecord` и `ts_ns`/`labels`, сжатие `.gz` и `.zip`, вложенные `.gz` внутри `.zip`, строки >200 КБ, SHA-256 (64 hex-символа), пустые и пробельные файлы, отмена контекста, прерывание при ошибке в `emit`, невалидный JSON, неструктурированные plain text строки. Тесты упали на фазе RED с кодом 1.
  - **Шаг 3 (GREEN Implementation):** Реализована потоковая обработка логов без `os.ReadFile` и `io.ReadAll`. Использованы `io.Copy(hasher, f)`, `bufio.NewReaderSize`, `json.NewDecoder` с `UseNumber()`, `bufio.Scanner` с буфером 4 МБ, `archive/zip` и `compress/gzip`. Все 15 тестов перешли в PASS.
- **Созданные/изменённые файлы:**
  - `services/ingest-service/internal/reader/stream_reader.go` (331 строк)
  - `services/ingest-service/internal/reader/stream_reader_test.go` (677 строк)
- **Результаты проверок и тестов:**
  - `go test -v -cover ./internal/reader/...`: `PASS`, покрытие `86.9% of statements` (требовалось `>= 80.0%`).
  - `py ./verify_task.py TASK-05`: `[VERIFY PASSED] TASK-05 passed all automated checks!`.
- **Заметки по реализации:**
  - Строго соблюден запрет на чтение файлов целиком в память.
  - `ComputeFileSHA256` производит потоковый расчёт через `io.Copy`.
  - В `StreamPath` поддержаны все контрактные форматы: JSON-массивы, JSONL/NDJSON, архивы `.gz` и `.zip` (включая вложенные `.gz`), а также конвертация `ts_ns` и `labels` в поля `contracts.RawScriptRecord`.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты (TDD Coverage):**
  - Файлы реализации и тестов: [`stream_reader.go`](file:///e:/%D0%BF%D1%80%D0%BE%D0%B3%D0%B0/logs-analyzer/logs-analyzer/rca-project/services/ingest-service/internal/reader/stream_reader.go) (332 строки), [`stream_reader_test.go`](file:///e:/%D0%BF%D1%80%D0%BE%D0%B3%D0%B0/logs-analyzer/logs-analyzer/rca-project/services/ingest-service/internal/reader/stream_reader_test.go) (693 строки).
  - Контракты: посимвольно соблюдены сигнатуры `RecordCallback`, `ComputeFileSHA256(path string) (string, int64, error)`, `StreamPath(ctx context.Context, path string, emit RecordCallback) error`, привязка к `contracts.RawScriptRecord` (`Timestamp`, `Date`, `Line`, `Fields`).
  - Полнота TDD-тестов: 15 тестов покрывают JSON-массивы, JSONL/NDJSON (с конвертацией `ts_ns` и `labels`), сжатие `.gz`, архивы `.zip` (включая вложенные `.gz`), длинные строки >200 КБ (буфер до 4 МБ), SHA-256 (64 hex-символа), пустые и пробельные файлы, отмену контекста, прерывание при ошибке emit, невалидный JSON, plain text логи.
  - Покрытие тестами: `coverage: 86.9% of statements` (порог `>= 80.0%`).
  - Личный запуск верификатора: `py ./verify_task.py TASK-05` завершился с кодом 0 (`[VERIFY PASSED] TASK-05 passed all automated checks!`).
  - Ограничения ресурсов: отсутствуют `os.ReadFile` и `io.ReadAll` при потоковом чтении; заглушек (`TODO`/`FIXME`/`panic`) не обнаружено.
- **Найденные замечания (если есть):** Замечаний нет. Реализация полностью соответствует контрактам и требованиям безопасности/бюджета ресурсов.
