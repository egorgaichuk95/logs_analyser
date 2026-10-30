# TASK-24: Редизайн UI v2 (Часть 1) — Компактная навигация, модальные окна Кейсов и Диска (15 ГБ), отдельная страница «📥 Источники и Загрузка логов» (`./services/frontend-ui`)

- **Статус:** `READY_FOR_REVIEW`
- **Зависимости:** `TASK-22`
- **Область видимости (Scope):**
  - Читать: `./services/frontend-ui/src/api/client.ts`, `./services/frontend-ui/src/styles.css`
  - Создавать/редактировать:
    - `./services/frontend-ui/src/types/navigation.ts`
    - `./services/frontend-ui/src/components/TopNavHeader.tsx`
    - `./services/frontend-ui/src/components/IngestAndTiltPage.tsx`
    - `./services/frontend-ui/src/styles.css`
  - **НЕ трогать:** Go-микросервисы, не удалять существующие файлы `StorageHeader.tsx` и `CaseSidebar.tsx` (для обратной совместимости с проверками `TASK-21`).

---

## 1. Цель задачи

Устранить визуальную перегруженность и тесный левый сайдбар с тройными вложенными скроллбарами. Перевести интерфейс на **чистую многостраничную архитектуру во всю ширину экрана (100% viewport width)** с удобными модальными окнами:

1. **Контракты навигации (`src/types/navigation.ts`):**
   - Строгая типизация 5 основных страниц приложения (`PageId`: `'ingest' | 'hypotheses' | 'topology' | 'chains' | 'evidence'`) и модальных окон.
2. **Компактная верхняя панель навигации (`src/components/TopNavHeader.tsx`):**
   - Высота в одну аккуратную строку (`60px`), без громоздкой постоянной плашки диска.
   - **Слева:** бренд `RCA Logs Analyzer` + интерактивная кнопка-селектор текущего кейса (показывает название активного кейса, его статус и кнопку **«📁 Сменить / Новый кейс»**). По нажатию открывает **просторное модальное окно управления кейсами** (поиск по кейсам, таблица/карточки всех кейсов без мелких скроллов, создание нового кейса, удаление кейса).
   - **По центру:** переключатель 5 полноэкранных страниц с бейджами счётчиков:
     1. `📥 Загрузка и Tilt` (`'ingest'`)
     2. `🎯 Гипотезы RCA` (`'hypotheses'`, с бейджем кол-ва найденных гипотез)
     3. `🕸️ Граф и Таймлайн` (`'topology'`)
     4. `🔍 Цепочки и Логи` (`'chains'`)
     5. `📑 Улики и Gemini` (`'evidence'`, с бейджем кол-ва улик `E-xx`)
   - **Справа:**
     - Кнопка **«🚀 Запустить анализ RCA»** (открывает удобное модальное окно настроек запуска анализа или запускает анализ сразу).
     - Компактный индикатор-кнопка диска **`💾 X.X MB / 15.0 GB`** с цветовой точкой заполнения. При клике на него открывается **модальное окно «Управление дисковой квотой 15 ГБ»** с крупной наглядной шкалой слоёв (`Сырые логи`, `Агрегаты и шаблоны`, `Отчёты RCA`, `Свободно`), детальной статистикой в байтах/мегабайтах и кнопкой в 1 клик **«🗑️ Очистить сырые логи»** (`POST /api/v1/storage/purge-raw`).
3. **Страница 1: «📥 Источники и Загрузка логов» (`src/components/IngestAndTiltPage.tsx`):**
   - Просторная страница на всю ширину экрана (сетка из 3 крупных читабельных карточек без зажатых областей):
     - **Блок 1: Локальные файлы из `/import` и `/log_example`** — широкая таблица доступных файлов с поиском по имени файла, выбором всех/отдельных файлов, отображением полного имени файла (без обрезки до нечитаемых огрызков), размера и кнопкой **«Импортировать выбранные файлы»**.
     - **Блок 2: Потоковая загрузка через браузер (Drag & Drop)** — крупная область перетаскивания файлов `.json`, `.jsonl`, `.gz`, `.zip`, список выбранных файлов и прогресс-бар выполнения задачи инжеста (`rows_ingested`, `bytes_read`, статус джоба, предупреждения `clock_skew_warnings`).
     - **Блок 3: Живой захват из локального Tilt (`Live Capture`)** — полноценная панель со списком всех обнаруженных контейнеров/подов (`GET /api/v1/tilt/targets`), фильтром сервисов, пресетами глубины захвата (`1 мин`, `5 мин`, `15 мин`, произвольно в секундах), кнопками **«▶️ Начать захват из Tilt»** / **«⏹️ Остановить захват»** и крупным дашбордом живой статистики (`lines_captured`, `batches_flushed`).
4. **Обновлённая дизайн-система (`src/styles.css`):**
   - Добавить чистые CSS-классы для полноширинного макета (`v2-layout`, `v2-top-nav`, `v2-page-container`, `v2-grid-2col`, `v2-grid-3col`), крупных модальных окон (`v2-modal-overlay`, `v2-modal-lg`, `v2-modal-xl`) и выдвижных боковых панелей (`v2-drawer-overlay`, `v2-drawer-panel` шириной `680px–860px`) с комфортной типографикой (`14px–16px` основной текст, увеличенный межстрочный интервал `1.55`).

---

## 2. Точные TypeScript-контракты компонентов

### 2.1. `./services/frontend-ui/src/types/navigation.ts`
```ts
import { CaseMeta, AnalyzeCaseResponse } from '../api/client';

export type AppPageId = 'ingest' | 'hypotheses' | 'topology' | 'chains' | 'evidence';

export interface TopNavHeaderProps {
  activeCase: CaseMeta | null;
  activePage: AppPageId;
  analysis: AnalyzeCaseResponse | null;
  onSelectPage: (page: AppPageId) => void;
  onSelectCase: (caseMeta: CaseMeta) => void;
  onCaseCreated: (caseMeta: CaseMeta) => void;
  onCaseDeleted: (caseId: string) => void;
  onAnalyzeSuccess: (result: AnalyzeCaseResponse) => void;
  onPurgeSuccess: () => void;
}

export interface IngestAndTiltPageProps {
  activeCase: CaseMeta;
  onIngestComplete: () => void;
  onAnalyzeSuccess: (result: AnalyzeCaseResponse) => void;
  onNavigateToHypotheses: () => void;
}
```

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}/services/frontend-ui:/app" -v rca_npm_cache:/root/.npm -w /app node:22-alpine sh -c "npx tsc --noEmit"
py ./verify_task.py TASK-24
```

---

## Отчёт о выполнении (заполняется субагентом-воркером)

- **Дата выполнения:** 2026-10-30 10:22 UTC
- **Соблюдение цикла Contract-First TDD:**
  - Шаг А (CONTRACT): Зафиксированы TypeScript-контракты в `./services/frontend-ui/src/types/navigation.ts` (`AppPageId`, `TopNavHeaderProps`, `IngestAndTiltPageProps`).
  - Шаг Б (CODE): Реализованы компоненты `TopNavHeader.tsx` и `IngestAndTiltPage.tsx`, а также добавлены стили v2 в `styles.css`.
  - Шаг В (VERIFY GATE): Проверка типов через Docker Node 22 (`tsc --noEmit`) завершена успешно с кодом 0; запущен автоматический верификатор `py ./verify_task.py TASK-24`.
- **Созданные/изменённые файлы:**
  - `./services/frontend-ui/src/types/navigation.ts` (23 строки) — типы навигации и пропсов компонентов v2;
  - `./services/frontend-ui/src/components/TopNavHeader.tsx` (563 строки) — компактная шапка 60px с селектором кейса, модальным окном управления кейсами (поиск, создание, удаление), 5 табами страниц со счётчиками, модалкой запуска анализа RCA и компактным индикатором диска с модалкой управления дисковой квотой 15 ГБ и 1-click purge-raw;
  - `./services/frontend-ui/src/components/IngestAndTiltPage.tsx` (581 строка) — полноэкранная страница источников и загрузки с 3 блоками: локальные файлы (/import и /log_example), потоковая загрузка Drag & Drop (.json, .jsonl, .gz, .zip) и живой захват Tilt с авто-поллингом и дашбордом метрик;
  - `./services/frontend-ui/src/styles.css` (+644 строки) — добавлены классы дизайн-системы v2: полноширинный макет (`v2-layout`, `v2-page-container`), сетки (`v2-grid-2col`, `v2-grid-3col`), модальные окна (`v2-modal-overlay`, `v2-modal-md`, `v2-modal-lg`, `v2-modal-xl`), дроверы (`v2-drawer-overlay`, `v2-drawer-panel`), дашборды и дропзона.
- **Результаты проверок и тестов:**
  - `docker run ... npx tsc --noEmit` — 0 ошибок (PASS);
  - `py ./verify_task.py TASK-24` — `[VERIFY PASSED] TASK-24 passed all automated checks!`;
  - Файлы обратной совместимости (`StorageHeader.tsx`, `CaseSidebar.tsx`) сохранены без изменений.
- **Заметки по реализации:**
  - Во всех расчётах процентов использования диска и прогресса задач инжеста используется защитная функция `safePercent`, гарантирующая отсутствие деления на ноль, `NaN` и `Infinity`.
  - При очистке сырых логов через `api.purgeRaw()` вызывается колбэк `onPurgeSuccess`, обновляется статистика и выводится бейдж с объёмом освобождённой памяти и временем операции.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты (TDD Coverage):**
  - Контракты в `./services/frontend-ui/src/types/navigation.ts` (`AppPageId`, `TopNavHeaderProps`, `IngestAndTiltPageProps`) посимвольно соответствуют спецификации задачи (раздел 2).
  - Компонент `./services/frontend-ui/src/components/TopNavHeader.tsx`: однострочная шапка высотой ровно 60px, интерактивный селектор кейсов с модальным окном (поиск, карточки, создание, удаление), 5 вкладок с бейджами, модалка запуска RCA, компактный индикатор квоты 15 ГБ и модалка управления диском со шкалой слоёв хранения и кнопкой 1-click `purge-raw`.
  - Компонент `./services/frontend-ui/src/components/IngestAndTiltPage.tsx`: полноэкранная страница на всю ширину с 3 блоками источников (локальные файлы `/import` и `/log_example` с таблицей и поиском, браузерный Drag & Drop с валидацией расширений, живой захват Tilt с таймером поллинга, пресетами глубины и живой статистикой строк/батчей).
  - Дизайн-система `./services/frontend-ui/src/styles.css`: добавлены классы v2 для полноширинного макета, модалок (`v2-modal-md/lg/xl`), сеток и дропзоны.
  - Обратная совместимость сохранена: файлы `StorageHeader.tsx` и `CaseSidebar.tsx` сохранены без изменений.
  - Защита от деления на ноль и `NaN`: реализована и протестирована функция `safePercent`, а также безопасная нормализация байтов `formatBytes`.
  - Полное отсутствие `TODO`/`FIXME`/заглушек.
  - Личный запуск `py ./verify_task.py TASK-24`: код возврата 0, `[VERIFY PASSED] TASK-24 passed all automated checks!`, проверка типов TypeScript (`npx tsc --noEmit`) через Docker завершилась без ошибок (0 vulnerabilities, 0 tsc errors).
- **Найденные замечания (если есть):** Замечаний нет. Решение полностью соответствует контрактам и готово к переходу на `TASK-25`.
