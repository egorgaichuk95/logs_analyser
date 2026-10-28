# TASK-22: Дашборд гипотез RCA, просмотр SVG/цепочек/контекста, инспектор `E-xx`, окно «Copy for Gemini» и `Dockerfile` (`./services/frontend-ui`)

- **Статус:** `TODO`
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

- **Дата выполнения:** *(не заполнено)*
- **Созданные/изменённые файлы:** *(не заполнено)*
- **Результаты проверок и тестов:** *(не заполнено)*
- **Заметки по реализации:** *(не заполнено)*

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `PENDING` *(выбрать: `APPROVED` | `CHANGES_REQUESTED`)*
- **Проверенные контракты и тесты:** *(не заполнено)*
- **Найденные замечания (если есть):** *(не заполнено)*
