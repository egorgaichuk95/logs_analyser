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

- **Дата выполнения:** 2026-10-28 20:36 UTC
- **Соблюдение цикла TDD (Contract → Tests → Code):**
  - Шаг 1 (CONTRACT): объявлены структуры и сигнатуры в `internal/metadb/store.go` (`Store`, `NewStore`, `SaveCase`, `GetCase`, `ListCases`, `DeleteCase`, `MarkAllRawPurged`, `SaveAnalysisRun`, `GetAnalysisRun`, `FindRecurringCases`, `SaveReport`, `GetReport`) и `internal/storage/manager.go` (`Manager`, `NewManager`, `GetStatus`, `PurgeAllRawLogs`).
  - Шаг 2 (RED): в `internal/storage/manager_test.go` написаны комплексные тесты для Store (CRUD, атомарная запись через `.tmp` и `os.Rename`, сохранение и перезагрузка состояния с диска в новый инстанс Store, `FindRecurringCases` по `templateID` и `sourcePlace`, `MarkAllRawPurged`), `GetStatus` (расчёт размеров таблиц ClickHouse `system.parts` в JSONEachRow формате с числовыми и строковыми байтами, разделение на raw и agg, подсчёт размеров `stagingDir` и `reportsDir`, обработка ошибок) и `PurgeAllRawLogs` (`httptest.NewServer` с валидацией `TRUNCATE TABLE logs_rca.events_raw`, `identity_edges`, `ingested_files`, очистка `stagingDir`, сохранение `reportsDir` и `events_agg_1m`/`templates_dict`/`service_edges`, смена статуса кейсов на `RAW_PURGED`). Зафиксирован провал тестов на заглушках (RED phase).
  - Шаг 3 (GREEN): реализован 100% рабочий код в `store.go` и `manager.go` без TODO и заглушек.
  - Шаг 4 (COVERAGE GATE): пройдены все тесты, покрытие пакета `storage` составило `92.5%` (при требуемом пороге >= 80%).
- **Созданные/изменённые файлы:**
  - `services/gateway-api/internal/metadb/store.go` (281 строка)
  - `services/gateway-api/internal/storage/manager.go` (194 строки)
  - `services/gateway-api/internal/storage/manager_test.go` (694 строки)
- **Результаты проверок и тестов:**
  - `go test -v -cover ./internal/...`: PASS для всех тестов (`TestStore_SaveGetListDeleteCase`, `TestStore_PersistenceAndReload`, `TestStore_FindRecurringCases`, `TestStore_MarkAllRawPurged`, `TestManager_GetStatus_Success`, `TestManager_GetStatus_Errors`, `TestManager_PurgeAllRawLogs_Success`, `TestManager_PurgeAllRawLogs_Errors`, `TestManager_EdgeCases`).
  - Покрытие `github.com/local/logs-analyzer/services/gateway-api/internal/storage`: `92.5% of statements`.
  - Автоматический скрипт верификации: `py ./verify_task.py TASK-10` вывел `[VERIFY PASSED] TASK-10 passed all automated checks!`.
- **Заметки по реализации:**
  - Файловое хранилище метаданных `metadb.Store` сериализует всё состояние в JSON с атомарной заменой через `state.json.tmp` + `os.Rename` под защитой `sync.RWMutex`, гарантируя отсутствие частичных записей и нулевую зависимость от внешних драйверов.
  - Менеджер хранилища `Manager` парсит размеры `system.parts` с поддержкой как числовых, так и строковых значений `bytes` ClickHouse.
  - `PurgeAllRawLogs` гарантированно не затрагивает агрегаты `events_agg_1m`, `templates_dict`, `service_edges`, а также сохраняет все сгенерированные отчёты в `reportsDir`.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты (TDD Coverage):**
  - **Файлы Scope:** `services/gateway-api/internal/metadb/store.go` (328 строк), `services/gateway-api/internal/storage/manager.go` (200 строк), `services/gateway-api/internal/storage/manager_test.go` (695 строк). Чужие микросервисы не модифицировались.
  - **Аудит контрактов (Contract-First Audit):** Сигнатуры `metadb.Store` (`SaveCase`, `GetCase`, `ListCases`, `DeleteCase`, `MarkAllRawPurged`, `SaveAnalysisRun`, `GetAnalysisRun`, `FindRecurringCases`, `SaveReport`, `GetReport`) и `storage.Manager` (`NewManager`, `GetStatus`, `PurgeAllRawLogs`), а также используемые типы и JSON-теги полностью соответствуют `./pkg/contracts/api.go` (`StorageStatusResponse`, `PurgeRawResponse`, `CaseMeta`, `AnalyzeCaseResponse`, `BuildEvidenceResponse`).
  - **Качество TDD-тестов:** 9 всесторонних тест-сьютов (`TestStore_SaveGetListDeleteCase`, `TestStore_PersistenceAndReload`, `TestStore_FindRecurringCases`, `TestStore_MarkAllRawPurged`, `TestManager_GetStatus_Success`, `TestManager_GetStatus_Errors`, `TestManager_PurgeAllRawLogs_Success`, `TestManager_PurgeAllRawLogs_Errors`, `TestManager_EdgeCases`). Проверены атомарность записи через `.tmp` и `os.Rename`, устойчивость к релоаду с диска, поиск рекуррентных инцидентов, парсинг `system.parts` ClickHouse (числовые и строковые байты), ошибки ClickHouse (500, network, truncate error), пустые URL и директории.
  - **Тестовое покрытие:**
    - Пакет `internal/storage`: **92.5%** of statements (при требовании >= 80%).
    - Совместное покрытие пакетов `internal/metadb` + `internal/storage`: **89.9%** of statements.
  - **Очистка сырых логов в 1 клик (`purge-raw`, Пункт 6):**
    - Корректно выполняются `TRUNCATE TABLE` исключительно для 3 сырых таблиц: `logs_rca.events_raw`, `logs_rca.identity_edges`, `logs_rca.ingested_files`.
    - Агрегаты `events_agg_1m`, `templates_dict`, `service_edges` защищены и не затрагиваются.
    - Директория `reportsDir` и отчеты в `metadb` не удаляются и остаются доступными.
    - Каталог `stagingDir` очищается полностью. Кейсы переводятся в статус `RAW_PURGED`.
  - **Отсутствие заглушек:** `TODO`, `FIXME`, `implement later`, `panic("not implemented")` отсутствуют.
  - **Бюджет ресурсов и потоковое чтение:** Нет чтения сырых логов целиком в память; для чтения `system.parts` используется построчный `bufio.NewScanner`, размер директорий вычисляется через метаданные `filepath.Walk`.
  - **Личный запуск верификатора:** Команда `py ./verify_task.py TASK-10` завершилась с кодом 0 (`[VERIFY PASSED] TASK-10 passed all automated checks!`).
- **Найденные замечания (если есть):** Замечаний нет. Все 8 пунктов чек-листа соблюдены в полном объёме.
