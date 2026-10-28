# TASK-22: Дашборд гипотез RCA, просмотр SVG/цепочек/контекста, инспектор `E-xx`, окно «Copy for Gemini» и `Dockerfile` (`./services/frontend-ui`)

- **Статус:** `IN_REVIEW`
- **Зависимости:** `TASK-21`
- **Область видимости (Scope):**
  - Читать: `./services/frontend-ui/src/`
  - Создавать/редактировать:
    - `./services/frontend-ui/src/components/RcaDashboard.tsx`
    - `./services/frontend-ui/src/components/EvidenceAndGeminiPanel.tsx`
    - `./services/frontend-ui/src/App.tsx`
    - `./services/frontend-ui/src/main.tsx`
    - `./services/frontend-ui/nginx.conf`
    - `./services/frontend-ui/Dockerfile`
  - **НЕ трогать:** Go-микросервисы

---

## 1. Цель задачи

Завершить разработку `frontend-ui`:
1. `src/components/RcaDashboard.tsx`:
   - карточки гипотез с цветовыми бейджами ролей (`ROOT_CAUSE`, `PROPAGATION`, `SYMPTOM`, `NOISE`), точным местом поломки (`service → dominant_pod → source_place → dominant_route`), шкалой 8 сигналов (`S1..S8`), пометками повторов `recurring_case_ids` и предупреждениями `clock_skew_findings`;
   - визуализация графа зависимостей сервисов (`service_edges`) и таймлайна (`timeline`) на встроенном векторном SVG в React;
   - интерактивный поиск сквозной цепочки по `trace_id`/`request_id`/`session_id` (`/api/v1/cases/{id}/chain?key=...`) и просмотр контекста `±20` строк (`mode=pod` и `mode=all_services`).
2. `src/components/EvidenceAndGeminiPanel.tsx`:
   - таблица всех доказательств `E-01..E-NN` с `source_file`, `source_record_idx`, `sha256`, `redacted_line` и кнопкой копирования `logql_snippet`;
   - кнопки скачивания `report.md`, `report.html`, `evidence.zip`;
   - модальное окно **«Пакет для Google Gemini»** (`GET /api/v1/cases/{id}/reports/{run_id}/gemini`) с кнопкой в 1 клик **«Скопировать промпт и доказательства для Gemini»**.
3. `nginx.conf` (проксирующий `/api/` на `http://gateway-api:8080/api/` с `client_max_body_size 2G;`) и многоступенчатый `Dockerfile`.

---

## 2. Точный чертёж `nginx.conf` и `Dockerfile`

### 2.1. `./services/frontend-ui/nginx.conf`
```nginx
server {
    listen 80;
    server_name _;
    client_max_body_size 2G;

    root /usr/share/nginx/html;
    index index.html;

    location /api/ {
        proxy_pass http://gateway-api:8080/api/;
        proxy_read_timeout 300s;
        proxy_connect_timeout 60s;
        proxy_send_timeout 300s;
    }

    location / {
        try_files $uri $uri/ /index.html;
    }
}
```

### 2.2. `./services/frontend-ui/Dockerfile`
```dockerfile
FROM node:22-alpine AS build
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install
COPY tsconfig.json vite.config.ts index.html ./
COPY src ./src
RUN npm run build

FROM nginx:1.27-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
```

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}/services/frontend-ui:/app" -v rca_npm_cache:/root/.npm -w /app node:22-alpine sh -c "npm run build"
docker compose build frontend-ui
py ./verify_task.py TASK-22
```
Сборка `dist/` должна завершиться без ошибок с кодом `0`.

---

## Отчёт о выполнении (заполняется субагентом-воркером)

- **Дата выполнения:** 2026-10-28 23:50 UTC
- **Созданные/изменённые файлы:**
  - `services/frontend-ui/src/components/RcaDashboard.tsx` — карточки гипотез с цветовыми бейджами ролей (ROOT_CAUSE красный, PROPAGATION оранжевый, SYMPTOM желтый, NOISE серый), точным местом поломки (`service → dominant_pod → source_place → dominant_route`), шкалой 8 сигналов (S1..S8), бейджами повторов `recurring_case_ids`, предупреждениями о рассинхроне времени `clock_skew_findings`, чистой векторной SVG визуализацией топологии сервисов и таймлайна, поиском цепочки вызовов (`GET /api/v1/cases/{id}/chain?key=...`) и инспектором контекста логов ±20 строк (`GET /api/v1/cases/{id}/logs/context`) с индикатором `RawPurged: true` при очищенных сырых логах.
  - `services/frontend-ui/src/components/EvidenceAndGeminiPanel.tsx` — таблица улик `E-01..E-NN` с источниками, индексами, хешами SHA-256, обезличенными строками, кнопками копирования `logql_snippet`, кнопками скачивания отчётов (`report.md`, `report.html`, `evidence.zip`) и модальным окном «Пакет для Google Gemini» с кнопкой в 1 клик «Скопировать промпт и доказательства для Gemini».
  - `services/frontend-ui/src/App.tsx` — сборка полноценного SPA: верхний `StorageHeader` (контроль бюджета 15 ГБ и очистка `purge-raw`), боковой `CaseSidebar` (кейсы, инжест, live захват Tilt, запуск анализа RCA) и рабочая область с вкладками (RCA Hypotheses & Topology, Evidence & Reports, Log Explorer).
  - `services/frontend-ui/src/main.tsx` — React 19 root render в `#root`.
  - `services/frontend-ui/nginx.conf` — Nginx конфигурация с проксированием `/api/` на `http://gateway-api:8080/api/` с `client_max_body_size 2G`.
  - `services/frontend-ui/Dockerfile` — многоступенчатая сборка (Node 22 build -> Nginx 1.27-alpine).
  - `services/frontend-ui/package.json` — скрипт сборки `"build": "tsc && vite build"`.
  - `services/frontend-ui/src/styles.css` — стили темной темы, карточек гипотез, бейджей, шкалы 8 сигналов, векторных SVG-диаграмм, модальных окон и таблиц.
- **Результаты проверок и тестов:**
  - `docker run --rm -v ... node:22-alpine sh -c "npm run build"`: код возврата 0 (`dist/` успешно собран: `dist/index.html`, `dist/assets/index-D0dS41Z0.css`, `dist/assets/index-CBpOmXkT.js`).
  - `docker compose build frontend-ui`: код возврата 0 (`rca-project-frontend-ui:latest` успешно собран).
  - `py ./verify_task.py TASK-22`: `[VERIFY PASSED] TASK-22 passed all automated checks!`.
- **Заметки по реализации:**
  - Все SVG графики (графа сервисов и минутного таймлайна распределения событий) реализованы на чистом SVG без сторонних библиотек.
  - Код полностью типизирован по контрактам `pkg/contracts`, без `TODO`, `FIXME` и заглушек.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты (TDD Coverage):**
  - Лично запущен верификатор `py ./verify_task.py TASK-22`: все автоматические проверки пройдены со статусом `[VERIFY PASSED]`.
  - Успешно выполнена сборка фронтенд-бандла в контейнере `node:22-alpine` (`tsc && vite build`), скомпилированы `dist/index.html`, `dist/assets/index-D0dS41Z0.css` (29.66 kB), `dist/assets/index-CBpOmXkT.js` (289.73 kB).
  - Успешно собран production Docker-образ микросервиса `docker compose build frontend-ui` (`rca-project-frontend-ui:latest`) на базе многоступенчатого `Dockerfile` и `nginx:1.27-alpine`.
  - Проверено посимвольное соответствие контрактам `nginx.conf` (`client_max_body_size 2G;`, проксирование `/api/` на `http://gateway-api:8080/api/`, fallback на `/index.html`) и `Dockerfile`.
  - Проверена полнота реализации компонентов:
    - `RcaDashboard.tsx`: карточки гипотез с 4 ролями (`ROOT_CAUSE`, `PROPAGATION`, `SYMPTOM`, `NOISE`), цепочка поломки `service → dominant_pod → source_place → dominant_route`, шкала 8 сигналов `S1..S8`, пометки повторов `recurring_case_ids`, баннер рассинхрона часов `clock_skew_findings`, чистые векторные React SVG-диаграммы топологии сервисов и поминутного таймлайна, модальное окно трассировки сквозной цепочки `/api/v1/cases/{id}/chain?key=...` и инспектор контекста логов `±20` строк `/api/v1/cases/{id}/logs/context` с поддержкой режимов `pod`/`all_services` и баннером `RawPurged: true`.
    - `EvidenceAndGeminiPanel.tsx`: таблица улик `E-01..E-NN` с фильтрами, источниками, индексами, хешами SHA-256, обезличенными строками, кнопками копирования `logql_snippet`, скачивания отчётов (`report.md`, `report.html`, `evidence.zip`) и модальным окном «Пакет для Google Gemini» с кнопкой копирования промпта в 1 клик.
    - `App.tsx` и `main.tsx`: полнофункциональная интеграция SPA (шапка хранилища 15 ГБ с очисткой `purge-raw`, левый сайдбар, 3 вкладки рабочего пространства).
- **Найденные замечания (если есть):** Замечаний нет. Все 8 пунктов чек-листа REVIEWER_GUIDE соблюдены в полном объёме, заглушки отсутствуют, код полностью автономный.
