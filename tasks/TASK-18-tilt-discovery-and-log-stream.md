# TASK-18: Автообнаружение сервисов и парсер живых потоков логов из локального Tilt / k8s / Docker (`./services/tilt-collector`)

- **Статус:** `DONE`
- **Зависимости:** `TASK-04`
- **Область видимости (Scope):**
  - Читать: `./pkg/contracts/`
  - Создавать/редактировать:
    - `./services/tilt-collector/internal/discovery/discovery.go`
    - `./services/tilt-collector/internal/discovery/discovery_test.go`
  - **НЕ трогать:** другие микросервисы

---

## 1. Цель задачи

Реализовать первую половину микросервиса `tilt-collector` (`:8084`) — пакет `discovery`, отвечающий за:
1. **Автоматическое обнаружение (`DiscoverTargets`)** всех локально запущенных в **Tilt** (через Docker Engine / локальный Kubernetes: `kind`, `k3d`, `minikube`, `docker-desktop`) контейнеров и подов приложения с извлечением канонического `ServiceName`, `PodName`, `Namespace` и фильтрацией системных контейнеров.
2. **Потоковый демультиплексор и парсер живых логов (`StreamContainerLogs`)**, который читает `stdout/stderr` контейнера с `timestamps=1`, корректно снимает 8-байтовые заголовки мультиплексирования Docker (`[stream_type, 0, 0, 0, size_be32]`), а также CRI/k8s префиксы (`2026-09-30T08:20:00.123456789Z stdout F {...}`) и преобразует каждую строку в стандартный `contracts.RawScriptRecord` (наносекундный `Timestamp`, `Date`, `Line`, `Fields{"service_name": ..., "pod": ..., "namespace": ...}`), полностью совместимый с `ingest-service`!

---

## 2. Посимвольный чертёж кода и контрактов (Blueprint)

### 2.1. `./services/tilt-collector/internal/discovery/discovery.go`
```go
package discovery

import (
	"context"
	"io"
	"net/http"
	"github.com/local/logs-analyzer/pkg/contracts"
)

type RuntimeClient struct {
	dockerBaseURL string // по умолчанию "http://unix/v1.43" через unix-сокет /var/run/docker.sock или тестовый HTTP URL
	k8sAPIURL     string // опционально K8S_API_URL
	httpClient    *http.Client
}

// NewRuntimeClient создаёт клиент к локальному Docker Daemon (через unix-сокет или http URL для тестов) и опциональному K8s API.
func NewRuntimeClient(dockerSocketOrURL string, k8sAPIURL string) *RuntimeClient

// NormalizeTarget преобразует метаданные контейнера/пода (имя, labels) в contracts.TiltTarget.
// Возвращает (target, true), если это прикладной сервис, или (_, false), если это системный контейнер.
func NormalizeTarget(id string, rawName string, status string, labels map[string]string) (contracts.TiltTarget, bool)

// DiscoverTargets опрашивает локальный runtime и возвращает список всех активных сервисов приложения.
func (c *RuntimeClient) DiscoverTargets(ctx context.Context) ([]contracts.TiltTarget, error)

// ParseLogLine разбирает одну строку из потока Docker/Kubernetes logs (с RFC3339Nano timestamp) в contracts.RawScriptRecord.
func ParseLogLine(rawLine string, target contracts.TiltTarget) (contracts.RawScriptRecord, bool)

// StreamReader читает поток логов контейнера (поддерживая как plain-поток, так и 8-байтовые заголовки Docker attach/logs stream)
// и вызывает onRecord для каждой непустой строки.
func StreamReader(ctx context.Context, r io.Reader, target contracts.TiltTarget, onRecord func(contracts.RawScriptRecord)) error
```

**Точные правила внутри `NormalizeTarget`:**
1. **Фильтрация системных контейнеров (возвращать `false`):**
   - Если `rawName` (без ведущего `/`) начинается с `"rca-"` (контейнеры самого нашего анализатора: `rca-clickhouse`, `rca-ingest`, `rca-engine`, `rca-evidence`, `rca-gateway`, `rca-frontend`, `rca-tilt-collector`).
   - Если `labels["io.kubernetes.pod.namespace"]` равен `"kube-system"`, `"local-path-storage"`, `"ingress-nginx"`, `"metallb-system"` или `labels["io.kubernetes.docker.type"] == "podsandbox"` (пустой `pause`-контейнер пода!).
2. **Определение `ServiceName`, `PodName`, `Namespace`:**
   - `ServiceName`: первый непустой из:
     - `labels["dev.tilt.resource"]` или `labels["tilt.dev/resource"]` (нативная метка Tilt!);
     - `labels["app.kubernetes.io/name"]` или `labels["app"]` или `labels["service_name"]`;
     - `labels["io.kubernetes.container.name"]`;
     - `labels["com.docker.compose.service"]`;
     - иначе — очищенное имя контейнера `strings.TrimPrefix(rawName, "/")` без суффиксов хэша пода.
   - `PodName`: первый непустой из `labels["io.kubernetes.pod.name"]`, `strings.TrimPrefix(rawName, "/")`, `ServiceName`.
   - `Namespace`: первый непустой из `labels["io.kubernetes.pod.namespace"]`, `labels["com.docker.compose.project"]`, `"tilt-local"`.

**Точные правила внутри `ParseLogLine` и `StreamReader`:**
1. **Демультиплексирование 8-байтового заголовка Docker (`StreamReader`):**
   - В Docker Engine API `/containers/{id}/logs?stdout=1&stderr=1&timestamps=1` при `tty=false` каждый фрейм начинается с 8 байт: `header[0] ∈ {1 (stdout), 2 (stderr)}, header[1..3] == 0, header[4..7] = uint32 big-endian payload length`.
   - Если первые 4 байта строки/фрейма соответствуют `[1|2, 0, 0, 0]`, аккуратно срезать 8-байтовый заголовок, чтобы остался чистый текст строки!
2. **Разбор CRI / Docker префикса времени в `ParseLogLine`:**
   - Формат CRI (Kubernetes containerd): `2026-09-30T08:20:00.123456789Z stdout F {"level":"error",...}`
   - Формат Docker `timestamps=1`: `2026-09-30T08:20:00.123456789Z {"level":"error",...}`
   - Извлечь первый токен до пробела и распарсить через `time.Parse(time.RFC3339Nano, token)` (если нет префикса времени — использовать `time.Now().UTC()`).
   - Если после timestamp идут токены CRI (`stdout`/`stderr` и `F`/`P`), пропустить их, оставив чистый `payload` в `rec.Line`.
   - Заполнить:
     - `rec.Timestamp = strconv.FormatInt(ts.UTC().UnixNano(), 10)`
     - `rec.Date = ts.UTC().Format(time.RFC3339Nano)`
     - `rec.Line = payload`
     - `rec.Fields = map[string]string{"service_name": target.ServiceName, "pod": target.PodName, "namespace": target.Namespace, "source": "tilt"}`

### 2.2. `./services/tilt-collector/internal/discovery/discovery_test.go`
Написать тесты по TDD:
1. `NormalizeTarget`:
   - контейнер с `labels: {"dev.tilt.resource": "chat-session", "io.kubernetes.pod.name": "chat-session-7f9c", "io.kubernetes.pod.namespace": "chat"}` → `ServiceName == "chat-session"`, `PodName == "chat-session-7f9c"`, `ok == true`;
   - контейнер `rca-clickhouse` и контейнер в `kube-system` → `ok == false`.
2. `ParseLogLine` и `StreamReader`:
   - строка CRI `2026-09-30T08:20:00.500000000Z stderr F {"level":"error","msg":"db timeout"}` → `rec.Line == "{\"level\":\"error\",\"msg\":\"db timeout\"}"`, `rec.Fields["service_name"] == "chat-session"`;
   - поток с 8-байтовым бинарным заголовком Docker `[1, 0, 0, 0, 0, 0, 0, N]` корректно очищается от бинарного префикса.
3. `DiscoverTargets` с `httptest.NewServer` (имитирующим `/containers/json` Docker API) возвращает только контейнеры приложения Tilt, отсеивая `rca-*` и `kube-system`.

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}:/workspace" -v rca_go_cache:/go/pkg/mod -v rca_go_build:/root/.cache/go-build -w /workspace/services/tilt-collector golang:1.23-alpine go test -v -cover ./internal/discovery/...
py ./verify_task.py TASK-18
```

---

## Отчёт о выполнении (заполняется субагентом-воркером)

- **Дата выполнения:** 2026-10-28 22:35 UTC
- **Соблюдение цикла TDD (Contract → Tests → Code):**
  1. *Contract:* Сформирован контракт `RuntimeClient`, `NewRuntimeClient`, `NormalizeTarget`, `DiscoverTargets`, `ParseLogLine`, `StreamReader` по Blueprint.
  2. *RED (Tests First):* Написаны исчерпывающие тесты в `discovery_test.go` (18 тестов: нормализация Tilt/K8s/Docker compose меток, фильтрация rca-* и системных неймспейсов, CRI и Docker timestamp форматы, plain-строки, демультиплексирование 8-байтового заголовка Docker logs, мокирование Docker API и K8s API через `httptest.NewServer`, обработка ошибок и отмена контекста). Зафиксировано начальное падение тестов (RED).
  3. *GREEN (Code):* Реализована 100% рабочая логика в `discovery.go` без TODO, FIXME и заглушек. Потоковый демультиплексор поддерживает как фреймы Docker с 8-байтными заголовками, так и plain/CRI потоки.
  4. *Coverage & Gate:* Достигнуто покрытие **89.5%** statement coverage (минимальный порог 80.0%). Автоматический верификатор `py ./verify_task.py TASK-18` завершился со статусом `[VERIFY PASSED]`.
- **Созданные/изменённые файлы:**
  - `./services/tilt-collector/internal/discovery/discovery.go` (460 строк)
  - `./services/tilt-collector/internal/discovery/discovery_test.go` (780 строк)
  - `./tasks/TASK-18-tilt-discovery-and-log-stream.md`
- **Результаты проверок и тестов:**
  - `go test -v -cover ./internal/discovery/...`: **PASS** (18/18 тестов passed)
  - `coverage: 89.5% of statements`
  - `py ./verify_task.py TASK-18`: `[VERIFY PASSED]`
- **Заметки по реализации:**
  - `NormalizeTarget`: фильтрует системные контейнеры (`rca-*`, `kube-system`, `local-path-storage`, `ingress-nginx`, `metallb-system`, `podsandbox`), извлекает канонический `ServiceName` с приоритетом Tilt (`dev.tilt.resource`, `tilt.dev/resource`) -> K8s (`app.kubernetes.io/name`, `app`, `service_name`, `io.kubernetes.container.name`) -> Docker compose -> container name с обрезанием хэшей подов.
  - `ParseLogLine`: демультиплексирует бинарный заголовок при наличии, парсит RFC3339/RFC3339Nano (fallback на UTC now), корректно извлекает CRI payload с тегами `F`/`P` для `stdout`/`stderr`, формирует `contracts.RawScriptRecord` со стандартными полями (`service_name`, `pod`, `namespace`, `source: tilt`).
  - `StreamReader`: потоково читает лог контейнера, распознает 8-байтовые заголовки фреймов Docker (`uint32` payload length), а также plain-text потоки с безопасным использованием буфера (без `os.ReadFile`/`io.ReadAll`), корректно обрабатывая context cancellation и EOF.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты (TDD Coverage):**
  - Файлы в области видимости:
    - `services/tilt-collector/internal/discovery/discovery.go` (12 790 байт) — реализация `RuntimeClient`, `NewRuntimeClient`, `NormalizeTarget`, `DiscoverTargets`, `ParseLogLine`, `StreamReader`.
    - `services/tilt-collector/internal/discovery/discovery_test.go` (24 340 байт) — 18 исчерпывающих TDD-тестов, покрывающих все нормализации Tilt/K8s/Docker compose, демультиплексирование бинарных фреймов Docker (8-byte header `[stream_type, 0, 0, 0, uint32_be]`), парсинг CRI `stdout/stderr` с тегами `F`/`P`, fallback RFC3339/RFC3339Nano, мокирование Docker/K8s API через `httptest.NewServer`, проверку отмены контекста и коротких строк.
  - Покрытие тестами: `coverage: 89.5% of statements` (порог: 80.0%). Все тесты успешно пройдены (`PASS`).
  - Верификатор `py ./verify_task.py TASK-18` успешно пройден с кодом 0 (`[VERIFY PASSED] TASK-18 passed all automated checks!`).
  - Контракты и JSON-теги посимвольно соответствуют чертежу задачи и `pkg/contracts` (`TiltTarget`, `RawScriptRecord`, `RuntimeDocker`, `RuntimeK8s`).
  - Заглушки, `TODO`, `FIXME` и `panic` отсутствуют.
  - Чтение потоковое через буферы фиксированного размера (без `os.ReadFile`/`io.ReadAll`).
  - Фильтрация системных контейнеров (`rca-*`, `kube-system`, `local-path-storage`, `ingress-nginx`, `metallb-system`, `podsandbox`) реализована и протестирована.
- **Найденные замечания (если есть):** Замечаний нет. Решение полностью готово и удовлетворяет всем 8 пунктам чек-листа.
