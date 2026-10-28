# TASK-02: Конфигурация `docker-compose.yml` (лимит 1.55 ГБ RAM, `internal: true`) и `service_profiles.yaml`

- **Статус:** `IN_REVIEW`
- **Зависимости:** `TASK-01`
- **Область видимости (Scope):**
  - Создавать/редактировать:
    - `./docker-compose.yml`
    - `./config/service_profiles.yaml`
  - **НЕ трогать:** другие папки

---

## 1. Цель задачи

Создать корневой `./docker-compose.yml`, обеспечивающий работу всего стека из 7 контейнеров (включая `tilt-collector` для живого захвата логов из локально поднятого через Tilt приложения) в рамках бюджета **1–2 ГБ RAM** (суммарный `mem_limit` всех 7 контейнеров = **1648 МБ ≈ 1.61 ГБ**), с изолированной внутренней сетью `internal: true`, и декларативный конфиг `./config/service_profiles.yaml`.

---

## 2. Точное содержимое создаваемых файлов (Blueprint)

### 2.1. `./config/service_profiles.yaml`
```yaml
version: 1
global:
  # Дополнительные пути для извлечения идентификаторов из произвольных JSON-логов
  trace_id_fields: ["trace_id", "traceId", "otel.trace_id"]
  span_id_fields: ["span_id", "spanId", "otel.span_id"]
  request_id_fields: ["id", "request_id", "req_id", "x_request_id"]
  session_id_fields: ["session_id", "visit_session_id", "visit-session-id"]
  visitor_id_fields: ["visitor_id", "provided_visitor_id", "ID", "player_id"]
  operator_id_fields: ["operator_id", "operatorId"]

services:
  chat-session:
    static_upstreams:
      - "chat-com"
  chat-com:
    expected_noise_templates:
      - "Operator (operator_id=<num>) was not found in cache"
```

### 2.2. `./docker-compose.yml`
```yaml
services:
  clickhouse:
    image: clickhouse/clickhouse-server:24.8-alpine
    container_name: rca-clickhouse
    mem_limit: 896m
    environment:
      CLICKHOUSE_DB: logs_rca
      CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT: "1"
    volumes:
      - ./deploy/clickhouse/low_memory.xml:/etc/clickhouse-server/config.d/low_memory.xml:ro
      - ./deploy/clickhouse/low_memory_profile.xml:/etc/clickhouse-server/users.d/low_memory_profile.xml:ro
      - ./deploy/clickhouse/init_schema.sql:/docker-entrypoint-initdb.d/init_schema.sql:ro
      - rca_ch_data:/var/lib/clickhouse
    networks:
      - rca_internal
    healthcheck:
      test: ["CMD", "wget", "--spider", "-q", "http://127.0.0.1:8123/ping"]
      interval: 5s
      timeout: 3s
      retries: 10

  ingest-service:
    build:
      context: .
      dockerfile: services/ingest-service/Dockerfile
    container_name: rca-ingest
    mem_limit: 224m
    environment:
      CLICKHOUSE_HTTP_URL: "http://clickhouse:8123"
      PROFILES_PATH: "/config/service_profiles.yaml"
      IMPORT_DIR: "/import"
      EXAMPLE_DIR: "/log_example"
      STAGING_DIR: "/data/staging"
      PORT: "8081"
    volumes:
      - ./config:/config:ro
      - ./import:/import:ro
      - ./log_example:/log_example:ro
      - rca_app_data:/data
    depends_on:
      clickhouse:
        condition: service_healthy
    networks:
      - rca_internal

  rca-engine:
    build:
      context: .
      dockerfile: services/rca-engine/Dockerfile
    container_name: rca-engine
    mem_limit: 192m
    environment:
      CLICKHOUSE_HTTP_URL: "http://clickhouse:8123"
      PROFILES_PATH: "/config/service_profiles.yaml"
      PORT: "8082"
    volumes:
      - ./config:/config:ro
    depends_on:
      clickhouse:
        condition: service_healthy
    networks:
      - rca_internal

  evidence-service:
    build:
      context: .
      dockerfile: services/evidence-service/Dockerfile
    container_name: rca-evidence
    mem_limit: 128m
    environment:
      CLICKHOUSE_HTTP_URL: "http://clickhouse:8123"
      REPORTS_DIR: "/data/reports"
      PORT: "8083"
    volumes:
      - rca_app_data:/data
    depends_on:
      clickhouse:
        condition: service_healthy
    networks:
      - rca_internal

  tilt-collector:
    build:
      context: .
      dockerfile: services/tilt-collector/Dockerfile
    container_name: rca-tilt-collector
    mem_limit: 64m
    environment:
      INGEST_URL: "http://ingest-service:8081"
      DOCKER_SOCKET_PATH: "/var/run/docker.sock"
      K8S_API_URL: ""
      PORT: "8084"
    extra_hosts:
      - "host.docker.internal:host-gateway"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    depends_on:
      - ingest-service
    networks:
      - rca_internal
      - rca_host_bridge

  gateway-api:
    build:
      context: .
      dockerfile: services/gateway-api/Dockerfile
    container_name: rca-gateway
    mem_limit: 96m
    environment:
      CLICKHOUSE_HTTP_URL: "http://clickhouse:8123"
      INGEST_URL: "http://ingest-service:8081"
      RCA_URL: "http://rca-engine:8082"
      EVIDENCE_URL: "http://evidence-service:8083"
      TILT_COLLECTOR_URL: "http://tilt-collector:8084"
      META_STATE_PATH: "/data/meta/state.json"
      STAGING_DIR: "/data/staging"
      REPORTS_DIR: "/data/reports"
      DISK_QUOTA_BYTES: "16106127360"
      RAW_QUOTA_BYTES: "10737418240"
      PORT: "8080"
    ports:
      - "127.0.0.1:8080:8080"
    volumes:
      - rca_app_data:/data
    depends_on:
      clickhouse:
        condition: service_healthy
    networks:
      - rca_internal
      - rca_host_bridge

  frontend-ui:
    build:
      context: ./services/frontend-ui
      dockerfile: Dockerfile
    container_name: rca-frontend
    mem_limit: 48m
    ports:
      - "127.0.0.1:4300:80"
    depends_on:
      - gateway-api
    networks:
      - rca_internal
      - rca_host_bridge

networks:
  rca_internal:
    driver: bridge
    internal: true
  rca_host_bridge:
    driver: bridge

volumes:
  rca_ch_data:
  rca_app_data:
```

---

## 3. Команда проверки (Verification Gate)

Проверить синтаксис `docker-compose.yml`:
```powershell
docker compose config -q
py ./verify_task.py TASK-02
```
Команда должна завершиться с кодом `0` (без ошибок).

---

## Отчёт о выполнении (заполняется субагентом-воркером)

- **Дата выполнения:** 2026-10-28 18:50 UTC
- **Соблюдение цикла TDD (Contract → Tests → Code):**
  - Шаг А (Contract): зафиксированы точные спецификации сервисов, ограничений по памяти, томов, портов и переменных окружения для `docker-compose.yml`, а также структура и поля для `config/service_profiles.yaml` по Blueprint.
  - Шаг Б (RED - Tests First): запущен `py ./verify_task.py TASK-02` до создания файлов, подтверждён ожидаемый отказ верификатора (`Missing required file in Scope: docker-compose.yml`).
  - Шаг В (GREEN - Code): созданы файлы `./config/service_profiles.yaml` и `./docker-compose.yml` в точном соответствии с Blueprint.
  - Шаг Г (Automated Verify Gate): запущен `docker compose config -q` (код 0) и `py ./verify_task.py TASK-02` (`[VERIFY PASSED]`).
- **Созданные/изменённые файлы:**
  - `./config/service_profiles.yaml` (18 строк, 728 байт)
  - `./docker-compose.yml` (156 строк, 4022 байт)
- **Результаты проверок и тестов:**
  - `docker compose config -q`: exit code 0
  - `py ./verify_task.py TASK-02`: `[VERIFY PASSED] TASK-02 passed all automated checks!`
- **Заметки по реализации:**
  - Лимиты памяти (`mem_limit`): clickhouse: 896m, ingest-service: 224m, rca-engine: 192m, evidence-service: 128m, tilt-collector: 64m, gateway-api: 96m, frontend-ui: 48m. Суммарный объём: 1648 МБ (1.61 ГБ) — укладывается в бюджет 1–2 ГБ RAM.
  - Внутренняя сеть `rca_internal` изолирована (`internal: true`), внешняя сеть `rca_host_bridge` для связи с хостом/Tilt и портами `8080` (gateway-api) и `4300` (frontend-ui).
  - Конфигурация проверена, заглушки и TODO отсутствуют.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты:**
  - Границы Scope: созданы `./docker-compose.yml` (4022 байт) и `./config/service_profiles.yaml` (728 байт), файлы вне Scope не затронуты.
  - Посимвольный аудит контрактов: конфигурация `docker-compose.yml` (7 сервисов, mem_limit суммарно 1648 МБ / 1.61 ГБ, сеть `rca_internal` с `internal: true`, привязка портов на `127.0.0.1`, монтирование томов `:ro`, квоты `DISK_QUOTA_BYTES` и `RAW_QUOTA_BYTES`) и `config/service_profiles.yaml` (поля идентификаторов trace/span/req/session/visitor/operator и статические апстримы/шумовые шаблоны) полностью идентичны Blueprint.
  - Синтаксическая валидация: `docker compose config -q` завершился успешно с кодом `0`.
  - Отсутствие заглушек: TODO, FIXME и плейсхолдеры отсутствуют.
  - Автоматический верификатор: `py ./verify_task.py TASK-02` успешно пройден (`[VERIFY PASSED] TASK-02 passed all automated checks!`).
- **Найденные замечания (если есть):** Замечаний нет. Конфигурация полностью соответствует требованиям спецификации и готова к использованию следующими задачами.
