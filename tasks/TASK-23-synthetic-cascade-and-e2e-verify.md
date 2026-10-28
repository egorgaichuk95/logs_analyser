# TASK-23: Генератор синтетического каскада на 6 сервисах и сквозной E2E-тест всей системы (включая `tilt-collector`) (`./tools/synthgen`)

- **Статус:** `TODO`
- **Зависимости:** `TASK-20`, `TASK-22`
- **Область видимости (Scope):**
  - Читать: `./pkg/contracts/`, `./ARCHITECTURE_AND_PLAN.md`
  - Создавать/редактировать:
    - `./tools/synthgen/generate_incident.py`
    - `./tools/synthgen/e2e_verify.py`
  - **НЕ трогать:** код микросервисов (кроме исправления обнаруженных при E2E-тестировании дефектов)

---

## 1. Цель задачи

Создать генератор мультисервисного инцидента и сквозной автотест, проверяющий всю систему целиком в Docker Compose (включая проверку `GET /api/v1/tilt/targets` и `GET /api/v1/cases/{id}/tilt/status` от микросервиса `tilt-collector`).

---

## 2. Точные требования к скриптам (Blueprint)

### 2.1. `./tools/synthgen/generate_incident.py`
Скрипт на стандартной библиотеке Python 3 (без внешних зависимостей), генерирующий в `./import/synth_cascade/` 6 файлов в формате JSON-массива `[{ "timestamp": "...", "date": "...", "line": "{...}", "fields": {...} }]`:
1. `billing-db-proxy_part-0001.json` (**ПЕРВОПРИЧИНА / `ROOT_CAUSE`**):
   - Baseline (`08:00:00Z .. 08:19:59Z`): нормальные INFO-логи.
   - Инцидент (`08:20:00Z .. 08:25:00Z`): в pod `billing-db-proxy-7f9c` каждую секунду возникают ошибки `OWN_DEFECT` / `RESOURCE`:
     `{"time":"...","level":"error","source":{"function":"pool.Acquire","file":"/app/internal/db/pool_manager.go","line":84},"msg":"HikariPool-1 - Connection is not available, request timed out","error":"panic: runtime error: pool exhausted","trace_id":"<T>"}` + `visit-session-id=<S>`.
2. `crm-service_part-0001.json` (**РАСПРОСТРАНЕНИЕ / `PROPAGATION`**):
   - С `08:20:02Z` (отставание `+2 с` от `billing-db-proxy`) логирует `DEADLINE`:
     `{"time":"...","level":"error","source":{"function":"crm.Query","file":"/app/crm_client.go","line":52},"msg":"upstream call failed","error":"Post \"http://billing-db-proxy.chat.svc.cluster.local:5432/query\": context deadline exceeded","trace_id":"<T>"}`.
3. `chat-session_part-0001.json` (**РАСПРОСТРАНЕНИЕ / `PROPAGATION`**):
   - В baseline и инциденте пишет успешные HTTP 204 access-логи с ложным `fields.detected_level = "error"` и `"error": ""`.
   - С `08:20:04Z` пишет лог интерцептора со связкой `X-Request-Id: [<R>]` ↔ `Traceparent: [00-<T>-0123456789abcdef-01]` и ошибку `PEER_5XX` при обращении к `crm-service.chat.svc.cluster.local:8080`.
4. `auth-service_part-0001.json` (**ОТМЕНА / `SYMPTOM`**):
   - С `08:20:05Z` пишет `context canceled` (`ErrClassCanceled`).
5. `api-gateway_part-0001.json` (**КЛИЕНТСКИЙ СИМПТОМ / `SYMPTOM`**):
   - С `08:20:05Z` пишет access-логи со статусом `502` и `id=<R>`, `email=test.user@example.com`, `remote_ip=5.191.119.115`, `token=Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature1234567890`.
6. `chat-com_part-0001.json` (**ПОСТОЯННЫЙ ШУМ / `NOISE` + `CLOCK_SKEW`**):
   - И в baseline, и в инциденте с одинаковой частотой (30 раз в минуту) пишет:
     `{"msg": "Operator (operator_id=55754) was not found in cache", "time": "260930 10:20:00 +0000", "level": "ERROR", "line": "cache:98", "type": "app"}` (со сдвигом часов `+7200 с` относительно `08:20:00Z`).

### 2.2. `./tools/synthgen/e2e_verify.py`
Скрипт выполняет следующие шаги через `urllib.request` к `http://127.0.0.1:8080/api/v1`:
1. Проверяет `GET /api/v1/health` → `200 OK` и `GET /api/v1/tilt/targets` → `200 OK` (проверка связки с `tilt-collector`).
2. Создаёт кейс `POST /api/v1/cases` (`"Synthetic 6-Service Cascade"`).
3. Запускает `POST /api/v1/cases/{id}/ingest-local` для 6 сгенерированных файлов из `/import/synth_cascade/` и ждёт `status == "COMPLETED"`.
4. Запускает `POST /api/v1/cases/{id}/analyze` и проверяет:
   - Гипотеза №1 (`rank == 1`): `role == "ROOT_CAUSE"`, `service == "billing-db-proxy"`, `source_place == "pool_manager.go:84"`;
   - `crm-service` и `chat-session` имеют `role == "PROPAGATION"`;
   - `Operator (operator_id=<num>) was not found in cache` в `chat-com` имеет `role == "NOISE"`;
   - `clock_skew_findings` содержит `chat-com` со сдвигом `~7200000 ms`;
   - список `evidence_items` не пуст (`E-01`...).
5. Скачивает `GET /api/v1/cases/{id}/reports/{run_id}/gemini` и проверяет:
   - в тексте **отсутствуют** строки `test.user@example.com`, `5.191.119.115` и `eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9`;
   - в тексте **присутствуют** `[E-01]` и `billing-db-proxy`.
6. Вызывает **очистку сырых логов в 1 клик**: `POST /api/v1/storage/purge-raw` и проверяет:
   - `GET /api/v1/storage/status` возвращает `raw_bytes == 0`;
   - `GET /api/v1/cases/{id}/analysis` и `GET /api/v1/cases/{id}/reports/{run_id}/html` по-прежнему возвращают `200 OK`!
7. Печатает `ALL E2E CHECKS PASSED!`.

---

## 3. Команда проверки (Verification Gate)

```powershell
py ./verify_task.py TASK-23
py ./tools/synthgen/generate_incident.py
docker compose up -d --build
py ./tools/synthgen/e2e_verify.py
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
