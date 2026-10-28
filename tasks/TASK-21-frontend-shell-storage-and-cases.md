# TASK-21: Каркас `frontend-ui`, типизированный клиент, панель диска 15 ГБ с кнопкой «Очистить сырые логи», кейсы и Live-захват из Tilt (`./services/frontend-ui`)

- **Статус:** `IN_REVIEW`
- **Зависимости:** `TASK-20`
- **Область видимости (Scope):**
  - Читать: `./tasks/TASK-04-shared-contracts-rca-and-api.md`, `./tasks/TASK-20-gateway-orchestrator-and-explorer.md`
  - Создавать/редактировать:
    - `./services/frontend-ui/package.json`
    - `./services/frontend-ui/tsconfig.json`
    - `./services/frontend-ui/vite.config.ts`
    - `./services/frontend-ui/index.html`
    - `./services/frontend-ui/src/api/client.ts`
    - `./services/frontend-ui/src/components/StorageHeader.tsx`
    - `./services/frontend-ui/src/components/CaseSidebar.tsx`
    - `./services/frontend-ui/src/styles.css`
  - **НЕ трогать:** Go-микросервисы

---

## 1. Цель задачи

Создать первую часть фронтенда `services/frontend-ui` (React 19 + TypeScript + Vite, без внешних тяжеловесных чарт-библиотек, чтобы сборка проходила за секунды и весила `< 200 КБ`):
1. `src/api/client.ts` — точные TypeScript-интерфейсы, посимвольно совпадающие с `pkg/contracts` (`TASK-04`) и эндпоинтами `gateway-api` (`TASK-20`).
2. `src/components/StorageHeader.tsx` — верхняя панель контроля бюджета 15 ГБ с индикатором слоёв (`raw_bytes`, `agg_bytes`, `reports_bytes`) и **кнопкой в 1 клик «Очистить сырые логи» (`POST /api/v1/storage/purge-raw`)**.
3. `src/components/CaseSidebar.tsx` — управление кейсами, выбор локальных файлов из `/import` и `/log_example` в 1 клик, загрузка файлов, **секция «Live захват из Tilt / Local Runtime»** (просмотр обнаруженных сервисов `GET /api/v1/tilt/targets`, выбор `since_seconds`, кнопки **«Начать захват из Tilt»** / **«Остановить захват»** и счётчик захваченных строк в реальном времени) и запуск анализа RCA.

---

## 2. Точные TypeScript-интерфейсы (`./services/frontend-ui/src/api/client.ts`)

```ts
export interface StorageStatusResponse {
  raw_bytes: number;
  staging_bytes: number;
  agg_bytes: number;
  reports_bytes: number;
  total_bytes: number;
  quota_bytes: number;
  raw_quota_bytes: number;
}

export interface PurgeRawResponse {
  freed_bytes: number;
  duration_ms: number;
  status: string;
}

export interface AvailableFile {
  path: string;
  name: string;
  size_bytes: number;
  mod_time: string;
}

export interface CaseMeta {
  id: string;
  title: string;
  description: string;
  status: string;
  created_at: string;
  latest_run_id?: string;
}

export interface IngestStatusResponse {
  job_id: string;
  case_id: string;
  status: string;
  files_total: number;
  files_done: number;
  skipped_duplicate_files: number;
  rows_ingested: number;
  bytes_read: number;
  clock_skew_warnings: string[];
  error?: string;
}

export interface SignalBreakdown {
  trace_chain_score: number;
  onset_score: number;
  lead_lag_score: number;
  novelty_score: number;
  causal_dir_score: number;
  graph_root_score: number;
  localization_score: number;
  change_corr_score: number;
}

export interface LeadLagLink {
  peer_service: string;
  peer_template: number;
  lag_seconds: number;
  correlation_r: number;
}

export interface Hypothesis {
  id: string;
  rank: number;
  role: "ROOT_CAUSE" | "PROPAGATION" | "SYMPTOM" | "NOISE";
  confidence: number;
  service: string;
  template_id: number;
  template_text: string;
  error_class: string;
  source_place: string;
  dominant_pod: string;
  pod_skew_ratio: number;
  dominant_route: string;
  dominant_version: string;
  onset_time: string;
  baseline_rate_per_min: number;
  incident_rate_per_min: number;
  growth_ratio: number;
  poisson_p_value: number;
  is_constant_noise: boolean;
  recurring_case_ids?: string[];
  lead_lag_peers: LeadLagLink[];
  signals: SignalBreakdown;
  explanation: string;
  evidence_ids: string[];
}

export interface ServiceEdge {
  case_id: string;
  caller_service: string;
  callee_service: string;
  evidence_source: string;
  call_count: number;
  error_count: number;
}

export interface ClockSkewFinding {
  service: string;
  pod: string;
  skew_ms: number;
  sample_raw_ts: string;
  sample_app_ts: string;
}

export interface TimelinePoint {
  bucket: string;
  service: string;
  level: string;
  events_count: number;
  max_latency_ns: number;
}

export interface EvidenceItem {
  id: string;
  kind: string;
  service: string;
  summary: string;
  timestamp: string;
  source_file: string;
  source_record_idx: number;
  sha256: string;
  redacted_line: string;
  logql_snippet: string;
}

export interface AnalyzeCaseResponse {
  case_id: string;
  run_id: string;
  incident_start: string;
  incident_end: string;
  hypotheses: Hypothesis[];
  service_edges: ServiceEdge[];
  clock_skew_findings: ClockSkewFinding[];
  timeline: TimelinePoint[];
  evidence_items?: EvidenceItem[];
}

export interface CreateCaseRequest {
  title: string;
  description: string;
}

export interface IngestLocalRequest {
  file_paths: string[];
}

export interface AnalyzeCaseRequest {
  case_id: string;
  incident_start?: string;
  incident_end?: string;
  baseline_start?: string;
  baseline_end?: string;
  use_historical_baseline: boolean;
}

export interface NormalizedEvent {
  case_id: string;
  event_id: string;
  ts: string;
  app_ts?: string;
  clock_skew_ms: number;
  service: string;
  environment: string;
  pod: string;
  node: string;
  service_version: string;
  level: string;
  severity_num: number;
  schema_kind: string;
  template_id: number;
  message: string;
  error_class: string;
  error_msg: string;
  exception_type: string;
  source_place: string;
  stack_fingerprint: number;
  trace_id: string;
  span_id: string;
  request_id: string;
  session_id: string;
  operator_id: string;
  visitor_id: string;
  http_method: string;
  http_route: string;
  http_status: number;
  latency_ns: number;
  peer_service: string;
  source_file: string;
  source_record_idx: number;
  raw_hash: number;
  raw_redacted: string;
}

export interface ContextLogsResponse {
  raw_purged: boolean;
  mode: "pod" | "all_services";
  events: NormalizedEvent[];
}

export interface TiltTarget {
  id: string;
  runtime: string;
  service_name: string;
  pod_name: string;
  namespace: string;
  status: string;
  labels?: Record<string, string>;
}

export interface TiltStartCaptureRequest {
  case_id: string;
  target_ids?: string[];
  since_seconds: number;
}

export interface TiltStatusResponse {
  case_id?: string;
  active: boolean;
  started_at?: string;
  targets_watched: number;
  lines_captured: number;
  batches_flushed: number;
  last_error?: string;
  targets?: TiltTarget[];
}
```

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}/services/frontend-ui:/app" -v rca_npm_cache:/root/.npm -w /app node:22-alpine sh -c "npm install && npx tsc --noEmit"
py ./verify_task.py TASK-21
```

---

## Отчёт о выполнении (заполняется субагентом-воркером)

- **Дата выполнения:** 2026-10-28 23:33 UTC
- **Соблюдение цикла TDD (Contract → Tests → Code):**
  - Шаг 1 (Contract): По разделу 2 чертежа задачи полностью выписаны все TypeScript-интерфейсы и сигнатуры в `src/api/client.ts`.
  - Шаг 2 (Verification / Type Gate): Настроен строгий `tsconfig.json` (strict, noImplicitAny, ESNext, react-jsx) и команда валидации `npx tsc --noEmit`.
  - Шаг 3 (Implementation): Реализован 100% рабочий код без TODO и заглушек для `ApiClient`, `StorageHeader`, `CaseSidebar` и `styles.css`.
  - Шаг 4 (Verification Gate): Пройдена проверка `py ./verify_task.py TASK-21` со статусом `[VERIFY PASSED]`.
- **Созданные/изменённые файлы:**
  - `services/frontend-ui/package.json` (21 строка)
  - `services/frontend-ui/tsconfig.json` (20 строк)
  - `services/frontend-ui/vite.config.ts` (16 строк)
  - `services/frontend-ui/index.html` (13 строк)
  - `services/frontend-ui/src/api/client.ts` (320 строк)
  - `services/frontend-ui/src/components/StorageHeader.tsx` (228 строк)
  - `services/frontend-ui/src/components/CaseSidebar.tsx` (644 строки)
  - `services/frontend-ui/src/styles.css` (588 строк)
- **Результаты проверок и тестов:**
  - `docker run --rm -v ... node:22-alpine sh -c "npm install && npx tsc --noEmit"`: `PASS` (0 ошибок компиляции типов TypeScript, 0 уязвимостей).
  - `py ./verify_task.py TASK-21`: `[VERIFY PASSED] TASK-21 passed all automated checks!`
- **Заметки по реализации:**
  - Проект инициализирован на React 19 + TypeScript + Vite 6 без сторонних тяжеловесных библиотек с полным соблюдением offline/air-gapped режима.
  - В `src/api/client.ts` реализован типизированный `ApiClient` для всех 19 REST-эндпоинтов Gateway API (`/api/v1/*`), включая потоковую загрузку, контекст логов, Live Tilt управление и получение отчётов.
  - В `src/components/StorageHeader.tsx` реализована визуальная шкала дискового лимита 15 ГБ (`raw_bytes`, `agg_bytes`, `reports_bytes`, свободное место) и кнопка в 1 клик «Очистить сырые логи» с подтверждением и индикацией освобождённой памяти.
  - В `src/components/CaseSidebar.tsx` поддержан весь жизненный цикл расследования: создание кейса, выбор файлов из `/import` и `/log_example` в 1 клик, drag-and-drop загрузка, отслеживание инжеста, модуль захвата логов из Tilt с выбором глубины `since_seconds` и счетчиками в реальном времени, а также запуск анализа RCA.
  - Стили вынесены в `src/styles.css` в единой технологичной dark slate палитре.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты:**
  - `services/frontend-ui/src/api/client.ts`: 100% посимвольное совпадение всех 21 TypeScript-интерфейсов из Раздела 2 чертежа задачи, а также реализация типизированного клиента `ApiClient` для всех 19 REST-маршрутов `gateway-api` (`/api/v1/*`).
  - `services/frontend-ui/src/components/StorageHeader.tsx`: Корректный расчёт слоёв диска 15 ГБ, защита от деления на ноль и NaN в `formatBytes` и процентных шкалах, кнопка «Очистить сырые логи» с подтверждением и обработкой ответа `PurgeRawResponse`.
  - `services/frontend-ui/src/components/CaseSidebar.tsx`: Полная реализация управления кейсами, выбора файлов из `/import` и `/log_example`, drag-and-drop загрузки, отслеживания статуса инжеста, панели «Live захват из Tilt» со счётчиками строк и пачек в реальном времени, а также запуска анализа RCA.
  - `services/frontend-ui/src/styles.css`: Dark-slate тема без внешних зависимостей, шрифтов или CDN.
  - **Verification Gate:**
    - Команда `docker run --rm -v ... node:22-alpine sh -c "npm install && npx tsc --noEmit"`: `PASS` (0 ошибок компиляции TypeScript, строгий режим `strict`/`noImplicitAny`).
    - Скрипт `py ./verify_task.py TASK-21`: `[VERIFY PASSED] TASK-21 passed all automated checks!`.
- **Найденные замечания (если есть):** Замечаний нет. Все 8 пунктов `REVIEWER_GUIDE.md` полностью соблюдены, код автономен, без заглушек (`TODO`/`FIXME`), проверка типов TypeScript проходит без ошибок.
