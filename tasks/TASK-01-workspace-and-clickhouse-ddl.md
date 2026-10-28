# TASK-01: Инициализация Go Workspace, модулей и DDL-схемы ClickHouse (`low_memory`)

- **Статус:** `IN_REVIEW`
- **Зависимости:** нет (первая задача)
- **Область видимости (Scope):**
  - Создавать/редактировать:
    - `./go.work`
    - `./pkg/contracts/go.mod`
    - `./services/ingest-service/go.mod`
    - `./services/rca-engine/go.mod`
    - `./services/evidence-service/go.mod`
    - `./services/gateway-api/go.mod`
    - `./services/tilt-collector/go.mod`
    - `./deploy/clickhouse/low_memory.xml`
    - `./deploy/clickhouse/low_memory_profile.xml`
    - `./deploy/clickhouse/init_schema.sql`
    - `./import/.gitkeep`
    - `./log_example/.gitkeep`
  - **НЕ трогать:** другие папки

---

## 1. Цель задачи

Создать базовую структуру Go-монорепозитория из 6 модулей (чтобы `go test` работал на любом шаге без ошибок отсутствующих `go.mod`) и конфигурационные файлы ClickHouse с жёстким ограничением памяти (**768 МБ на сервер**, **384 МБ на запрос**) и полным набором из 6 таблиц + `MATERIALIZED VIEW`.

---

## 2. Точное содержимое создаваемых файлов (Blueprint)

### 2.1. `./go.work`
```go
go 1.23

use (
	./pkg/contracts
	./services/evidence-service
	./services/gateway-api
	./services/ingest-service
	./services/rca-engine
	./services/tilt-collector
)
```

### 2.2. Стартовые `go.mod` для всех 6 модулей
1. `./pkg/contracts/go.mod`:
   ```go
   module github.com/local/logs-analyzer/pkg/contracts

   go 1.23
   ```
2. Для каждого из 5 сервисов (`ingest-service`, `rca-engine`, `evidence-service`, `gateway-api`, `tilt-collector`) создать `./services/<name>/go.mod`:
   ```go
   module github.com/local/logs-analyzer/services/<name>

   go 1.23

   require github.com/local/logs-analyzer/pkg/contracts v0.0.0

   replace github.com/local/logs-analyzer/pkg/contracts => ../../pkg/contracts
   ```

### 2.3. `./deploy/clickhouse/low_memory.xml`
```xml
<clickhouse>
    <max_server_memory_usage>805306368</max_server_memory_usage>
    <mark_cache_size>67108864</mark_cache_size>
    <uncompressed_cache_size>0</uncompressed_cache_size>
    <compiled_expression_cache_size>16777216</compiled_expression_cache_size>
    <mlock_executable>false</mlock_executable>
    <query_log remove="remove"/>
    <trace_log remove="remove"/>
    <text_log remove="remove"/>
    <metric_log remove="remove"/>
    <asynchronous_metric_log remove="remove"/>
    <part_log remove="remove"/>
    <processors_profile_log remove="remove"/>
</clickhouse>
```

### 2.4. `./deploy/clickhouse/low_memory_profile.xml`
```xml
<clickhouse>
    <profiles>
        <default>
            <max_memory_usage>402653184</max_memory_usage>
            <max_threads>2</max_threads>
            <max_insert_threads>1</max_insert_threads>
            <max_block_size>8192</max_block_size>
            <async_insert>0</async_insert>
        </default>
    </profiles>
</clickhouse>
```

### 2.5. `./deploy/clickhouse/init_schema.sql`
Скопировать **посимвольно** следующий SQL-скрипт (база `logs_rca`, таблицы `events_raw`, `events_agg_1m`, `mv_events_agg_1m`, `templates_dict`, `identity_edges`, `service_edges`, `ingested_files`):

```sql
CREATE DATABASE IF NOT EXISTS logs_rca;

CREATE TABLE IF NOT EXISTS logs_rca.events_raw (
    case_id           LowCardinality(String),
    event_id          UUID,
    ts                DateTime64(9, 'UTC'),
    app_ts            Nullable(DateTime64(9, 'UTC')),
    clock_skew_ms     Int64 DEFAULT 0,
    service           LowCardinality(String),
    environment       LowCardinality(String),
    pod               LowCardinality(String),
    node              LowCardinality(String),
    service_version   LowCardinality(String),
    level             LowCardinality(String),
    severity_num      UInt8,
    schema_kind       LowCardinality(String),
    template_id       UInt64,
    message           String CODEC(ZSTD(3)),
    error_class       LowCardinality(String),
    error_msg         String CODEC(ZSTD(3)),
    exception_type    LowCardinality(String),
    source_place      LowCardinality(String),
    stack_fingerprint UInt64 DEFAULT 0,
    trace_id          String CODEC(ZSTD(1)),
    span_id           String CODEC(ZSTD(1)),
    request_id        String CODEC(ZSTD(1)),
    session_id        String CODEC(ZSTD(1)),
    operator_id       String CODEC(ZSTD(1)),
    visitor_id        String CODEC(ZSTD(1)),
    http_method       LowCardinality(String),
    http_route        LowCardinality(String),
    http_status       UInt16 DEFAULT 0,
    latency_ns        UInt64 DEFAULT 0,
    peer_service      LowCardinality(String),
    source_file       LowCardinality(String),
    source_record_idx UInt32,
    raw_hash          UInt64,
    raw_redacted      String CODEC(ZSTD(5))
)
ENGINE = ReplacingMergeTree(ts)
PARTITION BY (case_id, toYYYYMMDD(ts))
ORDER BY (case_id, service, ts, pod, raw_hash)
TTL toDateTime(ts) + INTERVAL 3 DAY DELETE
SETTINGS index_granularity = 8192;

CREATE TABLE IF NOT EXISTS logs_rca.events_agg_1m (
    case_id           LowCardinality(String),
    bucket_1m         DateTime('UTC'),
    service           LowCardinality(String),
    pod               LowCardinality(String),
    service_version   LowCardinality(String),
    level             LowCardinality(String),
    error_class       LowCardinality(String),
    template_id       UInt64,
    source_place      LowCardinality(String),
    peer_service      LowCardinality(String),
    http_status_class UInt8,
    events_count      UInt64,
    latency_sum_ns    UInt64,
    latency_max_ns    SimpleAggregateFunction(max, UInt64),
    clock_skew_abs_ms SimpleAggregateFunction(max, UInt64)
)
ENGINE = SummingMergeTree()
PARTITION BY toYYYYMM(bucket_1m)
ORDER BY (case_id, service, bucket_1m, level, error_class, template_id, pod, source_place, peer_service, http_status_class)
TTL bucket_1m + INTERVAL 180 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS logs_rca.mv_events_agg_1m
TO logs_rca.events_agg_1m
AS SELECT
    case_id,
    toStartOfMinute(ts) AS bucket_1m,
    service,
    pod,
    service_version,
    level,
    error_class,
    template_id,
    source_place,
    peer_service,
    toUInt8(intDiv(http_status, 100)) AS http_status_class,
    count() AS events_count,
    sum(latency_ns) AS latency_sum_ns,
    max(latency_ns) AS latency_max_ns,
    max(toUInt64(abs(clock_skew_ms))) AS clock_skew_abs_ms
FROM logs_rca.events_raw
GROUP BY
    case_id, bucket_1m, service, pod, service_version,
    level, error_class, template_id, source_place, peer_service, http_status_class;

CREATE TABLE IF NOT EXISTS logs_rca.templates_dict (
    case_id           LowCardinality(String),
    service           LowCardinality(String),
    template_id       UInt64,
    template_text     String,
    level             LowCardinality(String),
    error_class       LowCardinality(String),
    source_place      LowCardinality(String),
    sample_redacted   String CODEC(ZSTD(3)),
    first_seen        DateTime64(9, 'UTC'),
    last_seen         DateTime64(9, 'UTC')
)
ENGINE = ReplacingMergeTree(last_seen)
ORDER BY (case_id, service, template_id);

CREATE TABLE IF NOT EXISTS logs_rca.identity_edges (
    case_id           LowCardinality(String),
    ts                DateTime64(9, 'UTC'),
    service           LowCardinality(String),
    left_type         LowCardinality(String),
    left_val          String,
    right_type        LowCardinality(String),
    right_val         String
)
ENGINE = ReplacingMergeTree(ts)
PARTITION BY case_id
ORDER BY (case_id, left_type, left_val, right_type, right_val);

CREATE TABLE IF NOT EXISTS logs_rca.service_edges (
    case_id           LowCardinality(String),
    caller_service    LowCardinality(String),
    callee_service    LowCardinality(String),
    evidence_source   LowCardinality(String),
    call_count        UInt64,
    error_count       UInt64
)
ENGINE = SummingMergeTree()
ORDER BY (case_id, caller_service, callee_service, evidence_source);

CREATE TABLE IF NOT EXISTS logs_rca.ingested_files (
    case_id           LowCardinality(String),
    file_sha256       FixedString(64),
    file_name         String,
    rows_ingested     UInt64,
    ingested_at       DateTime('UTC') DEFAULT now()
)
ENGINE = ReplacingMergeTree(ingested_at)
ORDER BY (case_id, file_sha256);
```

---

## 3. Команда проверки (Verification Gate)

Проверить, что все файлы созданы и не содержат синтаксических ошибок:
```powershell
Get-Item ./go.work, ./pkg/contracts/go.mod, ./services/*/go.mod, ./deploy/clickhouse/*.xml, ./deploy/clickhouse/init_schema.sql | Select-Object Name, Length
py ./verify_task.py TASK-01
```
Все 11 файлов должны существовать и быть непустыми.

---

## Отчёт о выполнении (заполняется субагентом-воркером)

- **Дата выполнения:** 2026-10-28
- **Соблюдение цикла TDD (Contract → Tests → Code):** Задача инициализации инфраструктуры репозитория. Созданы контракты конфигураций ClickHouse (ограничения памяти 768 МБ на сервер / 384 МБ на запрос, 6 таблиц + 1 Materialized View) и Go Workspace (модули contracts + 5 сервисов с директивами replace).
- **Созданные/изменённые файлы:**
  - `./go.work` (164 байт)
  - `./pkg/contracts/go.mod` (61 байт)
  - `./services/ingest-service/go.mod` (209 байт)
  - `./services/rca-engine/go.mod` (205 байт)
  - `./services/evidence-service/go.mod` (211 байт)
  - `./services/gateway-api/go.mod` (206 байт)
  - `./services/tilt-collector/go.mod` (209 байт)
  - `./deploy/clickhouse/low_memory.xml` (579 байт)
  - `./deploy/clickhouse/low_memory_profile.xml` (343 байт)
  - `./deploy/clickhouse/init_schema.sql` (5129 байт)
  - `./import/.gitkeep` (7 байт)
  - `./log_example/.gitkeep` (7 байт)
- **Результаты проверок и тестов:**
  - `py ./verify_task.py TASK-01`: `[VERIFY PASSED]`
  - `go work sync` (через Docker `golang:1.23-alpine`): завершился с кодом 0 без ошибок
  - Покрытие тестами: N/A (для TASK-01 тесты модулей начинаются с TASK-03, порог `min_cov: 0.0`)
- **Заметки по реализации:** Все файлы созданы посимвольно в строгом соответствии с Blueprint. Замечаний от ревьюера не поступало.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты (TDD Coverage):**
  - Посимвольно проверены `go.work` и 6 файлов `go.mod` (pkg/contracts + 5 микросервисов с директивами `replace`, Go 1.23).
  - Проверена схема ClickHouse DDL (`deploy/clickhouse/init_schema.sql`): база `logs_rca`, 6 таблиц (`events_raw`, `events_agg_1m`, `templates_dict`, `identity_edges`, `service_edges`, `ingested_files`) и Materialized View (`mv_events_agg_1m`).
  - Проверены профили памяти ClickHouse (`deploy/clickhouse/low_memory.xml` с лимитом 768 МБ и отключением системных логов, `deploy/clickhouse/low_memory_profile.xml` с лимитом 384 МБ на запрос).
  - Проверены маркерные файлы `./import/.gitkeep` и `./log_example/.gitkeep`.
  - Выполнена проверка синхронизации модулей: `docker run ... golang:1.23-alpine go work sync` (код 0).
  - Лично запущен автоматический верификатор: `py ./verify_task.py TASK-01` -> `[VERIFY PASSED] TASK-01 passed all automated checks!`.
  - Покрытие тестами: N/A (в TASK-01 нет Go-кода, модульные TDD-тесты стартуют с TASK-03, `min_cov: 0.0`).
- **Найденные замечания (если есть):** Замечаний нет. Все 8 пунктов чек-листа соблюдены в полном объёме.
