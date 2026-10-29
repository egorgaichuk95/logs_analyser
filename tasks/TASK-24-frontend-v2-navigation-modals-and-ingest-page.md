# TASK-24: Редизайн UI v2 (Часть 1) — Компактная навигация, модальные окна Кейсов и Диска (15 ГБ), отдельная страница «📥 Источники и Загрузка логов» (`./services/frontend-ui`)

- **Статус:** `TODO`
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

- **Дата выполнения:** *(не заполнено)*
- **Созданные/изменённые файлы:** *(не заполнено)*
- **Результаты проверок и тестов:** *(не заполнено)*
- **Заметки по реализации:** *(не заполнено)*

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `PENDING` *(выбрать: `APPROVED` | `CHANGES_REQUESTED`)*
- **Проверенные контракты и тесты:** *(не заполнено)*
- **Найденные замечания (если есть):** *(не заполнено)*
