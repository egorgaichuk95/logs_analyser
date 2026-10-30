# TASK-26: Редизайн UI v2 (Часть 3) — Страница 4 «🔍 Цепочки и Логи», обновлённая Страница 5 «📑 Улики и Gemini», интеграция в `App.tsx`, пересборка Docker и E2E-проверка (`./services/frontend-ui`)

- **Статус:** `DONE`
- **Зависимости:** `TASK-25`
- **Область видимости (Scope):**
  - Читать: `./services/frontend-ui/src/`
  - Создавать/редактировать:
    - `./services/frontend-ui/src/components/ChainAndContextPage.tsx`
    - `./services/frontend-ui/src/components/EvidenceAndGeminiPanel.tsx`
    - `./services/frontend-ui/src/App.tsx`
    - `./services/frontend-ui/src/styles.css`
  - **НЕ трогать:** Go-микросервисы, не удалять старые компоненты `StorageHeader.tsx`, `CaseSidebar.tsx`, `RcaDashboard.tsx`.

---

## 1. Цель задачи

Завершить редизайн фронтенда (UI v2), создав отдельные просторные страницы для трассировки цепочек, инспектора улик и отчётов, объединить все 5 страниц в `App.tsx`, пересобрать и перезапустить контейнер `frontend-ui` в Docker Compose и подтвердить работоспособность всей системы сквозным E2E-тестом.

1. **Страница 4: «🔍 Поиск цепочек и Контекст логов» (`src/components/ChainAndContextPage.tsx`):**
   - Два удобных полноразмерных режима (переключаемых крупными табами вверху страницы или расположенных просторными секциями):
     - **Секция А: Сквозная трассировка запроса (`GET /api/v1/cases/{id}/chain?key=...`):**
       - Крупная поисковая строка по `trace_id`, `request_id`, `session_id` или `visitor_id` + быстрые кнопки-примеры из найденных в анализе идентификаторов или улик.
       - Пошаговая визуальная карта прохождения запроса через сервисы (время с точностью до миллисекунд, сервис, pod, уровень, HTTP-статус, латентность, класс ошибки и полный текст сообщения).
       - Кнопка **«📜 Открыть контекст ±20 строк вокруг этого шага»** на каждой строке цепочки (автоматически открывает контекст для данного `pod` и `ts`).
     - **Секция Б: Просмотрщик контекста логов `±20 строк` (`GET /api/v1/cases/{id}/logs/context`):**
       - Удобная панель выбора `pod` (выпадающий список всех подов из анализа/гипотез или ввод вручную), времени `ts` (с быстрыми пресетами из `OnsetTime` гипотез и таймстампов улик `E-xx`) и переключателя режима: **`Только выбранный Pod (mode=pod)`** | **`Все сервисы синхронно (mode=all_services)`**.
       - Широкоформатный терминал логов с моноширинным шрифтом, фильтром по подстроке и уровням (`ERROR`, `WARN`, `INFO`), подсветкой целевого момента времени и чётким информационным баннером при `raw_purged: true`.

2. **Страница 5: «📑 Улики (E-01..E-NN) и Отчёты (Google Gemini)» (`src/components/EvidenceAndGeminiPanel.tsx`):**
   - **Верхний блок экспорта (4 крупные карточки в ряд):**
     1. **✨ Пакет для Google Gemini (`gemini_bundle.md`)** — акцентная карточка с кнопкой **«Открыть окно Gemini и скопировать»** (открывает большое модальное окно с предпросмотром промпта и копированием в 1 клик);
     2. **🌐 Интерактивный HTML-отчёт (`report.html`)** — кнопка скачивания автономного отчёта со встроенными SVG-диаграммами;
     3. **📝 Текстовый отчёт (`report.md`)** — кнопка скачивания Markdown;
     4. **📦 Полный архив доказательств (`evidence.zip`)** — кнопка скачивания ZIP-архива.
   - **Панель поиска и фильтрации улик:**
     - Поиск по ID улики (`E-01`), сервису, тексту строки или файлу источника.
   - **Широкая читабельная таблица доказательств `E-01..E-NN`:**
     - Колонки: `ID`, `Сервис / Тип`, `Время`, `Источник (файл : запись)`, `Краткое описание`, `Действия`.
     - По клику на кнопку **«🔍 Подробнее / LogQL»** (или на строку таблицы) открывается **просторное модальное окно инспектора улики (`EvidenceInspectorModal`)**, где без каких-либо обрезаний отображаются:
       - Полный обезличенный текст лога (`redacted_line`) с кнопкой копирования;
       - Полный путь к исходному файлу (`source_file`), номер записи (`source_record_idx`) и криптографический хэш `SHA-256`;
       - Готовый запрос `LogQL` (`logql_snippet`) в крупном блоке с кнопкой **«📋 Скопировать LogQL в 1 клик»**.

3. **Финальная сборка многостраничного приложения (`src/App.tsx`):**
   - Использует `TopNavHeader` вверху и отображает активную страницу (`AppPageId`) во всю ширину экрана без боковой тесноты:
     - `'ingest'` → `<IngestAndTiltPage />`
     - `'hypotheses'` → `<HypothesesPage />`
     - `'topology'` → `<TopologyAndTimelinePage />`
     - `'chains'` → `<ChainAndContextPage />`
     - `'evidence'` → `<EvidenceAndGeminiPanel />`
   - Умная навигация:
     - При первом открытии приложения автоматически выбирается последний созданный кейс; если у него уже есть результаты анализа — открывается страница `'hypotheses'`, если анализа ещё нет — открывается страница `'ingest'`.
     - После нажатия **«Запустить анализ RCA»** и получения ответа автоматически открывается страница `'hypotheses'`.
     - Если кейсов ещё нет совсем — на экране отображается чистый приветственный блок с кнопкой **«+ Создать первый кейс расследования»**.

---

## 2. Точные TypeScript-контракты компонентов

```ts
import { AnalyzeCaseResponse, EvidenceItem } from '../api/client';

export interface ChainAndContextPageProps {
  caseId: string;
  analysis: AnalyzeCaseResponse | null;
  initialChainKey?: string;
}

export interface EvidenceAndGeminiPanelProps {
  caseId: string;
  runId: string;
  evidenceItems: EvidenceItem[];
  highlightEvidenceId?: string;
}
```

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}/services/frontend-ui:/app" -v rca_npm_cache:/root/.npm -w /app node:22-alpine sh -c "npm run build"
docker compose build frontend-ui
docker compose up -d frontend-ui
py ./verify_task.py TASK-26
py ./tools/synthgen/e2e_verify.py
```
Все команды должны завершиться с кодом `0`, контейнер `rca-frontend` должен обновиться и запуститься на `http://127.0.0.1:4300`, а `e2e_verify.py` должен вывести `ALL E2E CHECKS PASSED!`.

---

## Отчёт о выполнении (заполняется субагентом-воркером)

- **Дата выполнения:** 2026-10-30 11:04
- **Созданные/изменённые файлы:** `ChainAndContextPage.tsx` (40 308 байт), `EvidenceAndGeminiPanel.tsx` (31 151 байт), `App.tsx` (14 140 байт), `styles.css` (103 583 байт)
- **Результаты проверок и тестов:** `npm run build` PASS, `docker compose build frontend-ui` PASS, `py ./verify_task.py TASK-26` PASS (`[VERIFY PASSED]`), `py ./tools/synthgen/e2e_verify.py` PASS (`ALL E2E CHECKS PASSED!`).
- **Заметки по реализации:** Все 5 страниц инженерного режима интегрированы и собраны в Docker.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты:** `ChainAndContextPageProps`, `EvidenceAndGeminiPanelProps`, `verify_task.py TASK-26` PASS, `e2e_verify.py` PASS.
- **Найденные замечания (если есть):** Нет.
