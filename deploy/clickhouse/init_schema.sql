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
