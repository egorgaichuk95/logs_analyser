# TASK-10: Хранилище метаданных и менеджер квоты 15 ГБ с очисткой сырых логов в 1 клик (`./services/gateway-api`)

- **Статус:** `TODO`
- **Зависимости:** `TASK-04`
- **Область видимости (Scope):**
  - Читать: `./pkg/contracts/`
  - Создавать/редактировать:
    - `./services/gateway-api/internal/metadb/store.go`
    - `./services/gateway-api/internal/storage/manager.go`
    - `./services/gateway-api/internal/storage/manager_test.go`
  - **НЕ трогать:** другие микросервисы

---

## 1. Цель задачи

Реализовать в `gateway-api`:
1. Персистентное файловое хранилище метаданных (`package metadb`) в `/data/meta/state.json` (атомарная запись через временный файл `state.json.tmp` + `os.Rename` под `sync.RWMutex` — не требует внешних C/SQLite драйверов, работает мгновенно при объёме в сотни кейсов и потребляет `< 2 МБ RAM`!).
2. Менеджер дисковой квоты **15 ГБ** и **очистки сырых логов в 1 клик (`PurgeAllRawLogs`)** (`package storage`), который выполняет `TRUNCATE TABLE logs_rca.events_raw`, `TRUNCATE TABLE logs_rca.identity_edges`, `TRUNCATE TABLE logs_rca.ingested_files` и очищает `/data/staging`, сохраняя все агрегаты (`events_agg_1m`, `templates_dict`, `service_edges`), кейсы и отчёты!

---

## 2. Точные сигнатуры и логика (Blueprint)

### 2.1. `./services/gateway-api/internal/metadb/store.go`
```go
package metadb

import (
	"sync"
	"github.com/local/logs-analyzer/pkg/contracts"
)

type Store struct {
	mu   sync.RWMutex
	path string
	// хранит Cases, Jobs, AnalysisRuns (AnalyzeCaseResponse), Reports (BuildEvidenceResponse)
}

func NewStore(path string) (*Store, error)

func (s *Store) SaveCase(c contracts.CaseMeta) error
func (s *Store) GetCase(id string) (contracts.CaseMeta, bool)
func (s *Store) ListCases() []contracts.CaseMeta
func (s *Store) DeleteCase(id string) error
func (s *Store) MarkAllRawPurged() error

func (s *Store) SaveAnalysisRun(res contracts.AnalyzeCaseResponse) error
func (s *Store) GetAnalysisRun(caseID, runID string) (contracts.AnalyzeCaseResponse, bool)
func (s *Store) FindRecurringCases(currentCaseID string, templateID uint64, sourcePlace string) []string

func (s *Store) SaveReport(rep contracts.BuildEvidenceResponse) error
func (s *Store) GetReport(caseID, runID string) (contracts.BuildEvidenceResponse, bool)
```

### 2.2. `./services/gateway-api/internal/storage/manager.go`
```go
package storage

import (
	"context"
	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/gateway-api/internal/metadb"
)

type Manager struct {
	chHTTPURL     string
	stagingDir    string
	reportsDir    string
	quotaBytes    int64 // 15 GB = 16106127360
	rawQuotaBytes int64 // 10 GB = 10737418240
	meta          *metadb.Store
}

func NewManager(chHTTPURL, stagingDir, reportsDir string, quotaBytes, rawQuotaBytes int64, meta *metadb.Store) *Manager

// GetStatus запрашивает размер таблиц ClickHouse и папок staging/reports
func (m *Manager) GetStatus(ctx context.Context) (contracts.StorageStatusResponse, error)

// PurgeAllRawLogs — очистка сырых логов в 1 клик!
// Выполняет TRUNCATE TABLE logs_rca.events_raw, identity_edges, ingested_files и удаляет файлы в stagingDir.
// Таблицы events_agg_1m, templates_dict, service_edges и папка reportsDir НЕ затрагиваются!
func (m *Manager) PurgeAllRawLogs(ctx context.Context) (contracts.PurgeRawResponse, error)
```

**Запрос размера таблиц в ClickHouse (`GetStatus`):**
`SELECT table, sum(bytes_on_disk) AS bytes FROM system.parts WHERE database = 'logs_rca' AND active = 1 GROUP BY table FORMAT JSONEachRow`:
- `RawBytes` = сумма `bytes` для `events_raw` + `identity_edges` + `ingested_files`;
- `AggBytes` = сумма `bytes` для `events_agg_1m` + `templates_dict` + `service_edges`.

### 2.3. `./services/gateway-api/internal/storage/manager_test.go`
Проверить:
1. Сохранение кейса, результата анализа и отчёта в `metadb.Store`, перезагрузку с диска и поиск повторов через `FindRecurringCases`.
2. Вызов `PurgeAllRawLogs` с `httptest.NewServer`: убедиться, что отправлены запросы `TRUNCATE TABLE logs_rca.events_raw`, `TRUNCATE TABLE logs_rca.identity_edges`, `TRUNCATE TABLE logs_rca.ingested_files`, папка `staging` очищена, статус кейса переведён в `RAW_PURGED`, а отчёт в `reportsDir` и `GetReport` остались на месте.

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}:/workspace" -v rca_go_cache:/go/pkg/mod -v rca_go_build:/root/.cache/go-build -w /workspace/services/gateway-api golang:1.23-alpine go test -v -cover ./...
py ./verify_task.py TASK-10
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
