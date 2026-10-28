# TASK-19: Менеджер живого захвата и батч-отправки логов в `ingest-service`, HTTP API `:8084` и `Dockerfile` (`./services/tilt-collector`)

- **Статус:** `TODO`
- **Зависимости:** `TASK-09`, `TASK-18`
- **Область видимости (Scope):**
  - Читать: `./pkg/contracts/`, `./services/tilt-collector/internal/discovery/`
  - Создавать/редактировать:
    - `./services/tilt-collector/internal/collector/manager.go`
    - `./services/tilt-collector/internal/server/http.go`
    - `./services/tilt-collector/internal/server/http_test.go`
    - `./services/tilt-collector/cmd/tiltcollector/main.go`
    - `./services/tilt-collector/Dockerfile`
  - **НЕ трогать:** другие микросервисы

---

## 1. Цель задачи

Завершить микросервис `tilt-collector` (`:8084`):
1. Реализовать `collector.Manager`, который по команде `StartCapture(req contracts.TiltStartCaptureRequest)` запускает параллельные горутины чтения живых логов из всех выбранных сервисов локального кластера Tilt, буферизует записи в канал и сбрасывает батчи в `POST http://ingest-service:8081/internal/ingest/batch` каждые **`500` строк** или каждые **`1500 мс`**.
2. Поднять внутренний HTTP-сервер `:8084` (`/health`, `/internal/tilt/targets`, `/internal/tilt/start`, `/internal/tilt/stop`, `/internal/tilt/status`) и собрать `Dockerfile`.

---

## 2. Посимвольный чертёж кода и контрактов (Blueprint)

### 2.1. `./services/tilt-collector/internal/collector/manager.go`
```go
package collector

import (
	"context"
	"sync"
	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/tilt-collector/internal/discovery"
)

type Manager struct {
	mu        sync.RWMutex
	runtime   *discovery.RuntimeClient
	ingestURL string
	// состояние активной сессии захвата, cancel-функция и счётчики
}

func NewManager(runtime *discovery.RuntimeClient, ingestURL string) *Manager

// StartCapture останавливает предыдущую сессию (если была активна), находит целевые контейнеры/поды Tilt
// и запускает фоновый стриминг логов в ingest-service (/internal/ingest/batch) для указанного req.CaseID.
func (m *Manager) StartCapture(ctx context.Context, req contracts.TiltStartCaptureRequest) (contracts.TiltStatusResponse, error)

// StopCapture останавливает все горутины чтения логов, досылает остаток буфера в ingest-service и переводит Active в false.
func (m *Manager) StopCapture() contracts.TiltStatusResponse

// GetStatus возвращает текущее состояние захвата (Active, TargetsWatched, LinesCaptured, BatchesFlushed, Targets).
func (m *Manager) GetStatus() contracts.TiltStatusResponse
```

### 2.2. `./services/tilt-collector/internal/server/http.go`
Маршруты HTTP-сервера `:8084`:
- `GET /health` → `200 OK` `{"status":"ok"}`.
- `GET /internal/tilt/targets` → вызывает `runtime.DiscoverTargets(r.Context())` и возвращает JSON-массив `[]contracts.TiltTarget`.
- `POST /internal/tilt/start` (принимает `contracts.TiltStartCaptureRequest`) → вызывает `manager.StartCapture`, возвращает `200 OK` с `contracts.TiltStatusResponse`.
- `POST /internal/tilt/stop` → вызывает `manager.StopCapture()`, возвращает `200 OK` с `contracts.TiltStatusResponse`.
- `GET /internal/tilt/status` → вызывает `manager.GetStatus()`, возвращает `200 OK` с `contracts.TiltStatusResponse`.

### 2.3. `./services/tilt-collector/Dockerfile`
```dockerfile
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.work ./
COPY pkg/contracts ./pkg/contracts
COPY services/tilt-collector ./services/tilt-collector
COPY services/ingest-service/go.mod ./services/ingest-service/go.mod
COPY services/rca-engine/go.mod ./services/rca-engine/go.mod
COPY services/evidence-service/go.mod ./services/evidence-service/go.mod
COPY services/gateway-api/go.mod ./services/gateway-api/go.mod
RUN cd services/tilt-collector && CGO_ENABLED=0 go build -o /out/tiltcollector ./cmd/tiltcollector

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/tiltcollector /app/tiltcollector
EXPOSE 8084
CMD ["/app/tiltcollector"]
```

### 2.4. `./services/tilt-collector/internal/server/http_test.go`
Написать интеграционный тест по TDD:
1. Поднять фейковый Docker Engine `httptest.NewServer`, отдающий 2 контейнера Tilt (`chat-com` и `chat-session`, плюс системный `rca-clickhouse`) и поток логов при вызове `/containers/{id}/logs`.
2. Поднять фейковый `ingest-service` `httptest.NewServer`, принимающий `POST /internal/ingest/batch` (`contracts.IngestBatchRequest`).
3. Вызвать `GET /internal/tilt/targets` → убедиться, что возвращаются ровно 2 сервиса (`chat-com` и `chat-session`).
4. Вызвать `POST /internal/tilt/start` с `CaseID: "case-tilt-1"` → подождать сброса батча → вызвать `POST /internal/tilt/stop`.
5. Проверить через `GET /internal/tilt/status`, что `LinesCaptured >= 2`, `BatchesFlushed >= 1`, а на фейковый `ingest-service` пришли записи с корректными `service_name` и `case_id == "case-tilt-1"`.

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}:/workspace" -v rca_go_cache:/go/pkg/mod -v rca_go_build:/root/.cache/go-build -w /workspace/services/tilt-collector golang:1.23-alpine go test -v -cover ./...
docker compose build tilt-collector
py ./verify_task.py TASK-19
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
