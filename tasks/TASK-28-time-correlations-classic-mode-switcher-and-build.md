# TASK-28: Интуитивный русскоязычный интерфейс (Часть 2) — Интерактивная панель «📊 Количество проблемных логов за время и Корреляции», кнопка перехода к старому/инженерному режиму, сборка Docker и E2E-проверка (`./services/frontend-ui`)

- **Статус:** `TODO`
- **Зависимости:** `TASK-27`
- **Область видимости (Scope):**
  - Читать: `./services/frontend-ui/src/`
  - Создавать/редактировать:
    - `./services/frontend-ui/src/components/ProblemLogsAndCorrelationPanel.tsx`
    - `./services/frontend-ui/src/components/SimpleRcaOverviewPage.tsx`
    - `./services/frontend-ui/src/components/TopNavHeader.tsx`
    - `./services/frontend-ui/src/types/navigation.ts`
    - `./services/frontend-ui/src/App.tsx`
    - `./services/frontend-ui/src/styles.css`
  - **НЕ трогать:** Go-микросервисы, **НЕ удалять** существующие компоненты (`HypothesesPage.tsx`, `TopologyAndTimelinePage.tsx`, `ChainAndContextPage.tsx`, `EvidenceAndGeminiPanel.tsx`, `IngestAndTiltPage.tsx`, `StorageHeader.tsx`, `CaseSidebar.tsx`, `RcaDashboard.tsx`).

---

## 1. Цель задачи

1. **Реализовать интерактивную панель «📊 Количество проблемных логов за выбранное время и Корреляция сервисов» (`src/components/ProblemLogsAndCorrelationPanel.tsx`):**
   - Отвечает на главный вопрос пользователя: *«Сколько проблемных логов произошло за определённое время в каждом сервисе и как ошибки в разных сервисах связаны между собой?»*
   - **Интерактивный выбор отрезка времени:**
     - Быстрые цветные кнопки-пресеты:
       - `🔥 Только время аварии (пик сбоя)`
       - `📅 Весь доступный период логов`
       - `⚡ Первые минуты начала сбоя`
       - `🎛️ Свой интервал времени` (два удобных выпадающих списка / ползунка `Начало интервала` и `Конец интервала` по временным корзинам `timeline`).
   - **4 крупные разноцветные сводные карточки за выбранный отрезок времени:**
     1. **Рубиновая карточка (`--color-ruby`):** `Всего проблемных логов (Ошибок)` за выбранное время (крупное число + сколько это в среднем в минуту).
     2. **Янтарная карточка (`--color-amber`):** `Ошибок в первопричине` (имя главного сервиса + сколько ошибок дал именно он).
     3. **Фиолетовая карточка (`--color-violet`):** `Каскадных ошибок в зависимых сервисах` (сколько ошибок вызвано цепной реакцией).
     4. **Изумрудная карточка (`--color-emerald`):** `Отсеяно фонового шума` (сколько логов не относятся к аварии).
   - **Многоцветный интерактивный график количества проблемных логов по времени (SVG):**
     - Столбчатый или слоистый график по временным интервалам (`bucket` из `analysis.timeline`, а при пустом `timeline` — автоматическая реконструкция распределения по `onset_time` и `incident_rate_per_min` / `baseline_rate_per_min` гипотез, чтобы график ВСЕГДА был наполненным и наглядным!).
     - Каждый сервис окрашен в свой уникальный яркий цвет из `getServiceColor(service)`.
     - При клике по столбцу времени или наведении показывается точная разбивка: сколько проблемных логов дал каждый сервис в эту минуту.
   - **Понятная таблица и визуальная карта «Корреляция ошибок и сдвиг по времени (Кто от кого упал)»:**
     - Полностью на русском языке, с цветными индикаторами для каждого сервиса/проблемы:
       - **Колонка 1: Сервис и его роль в аварии** (цветной кружок сервиса + русский бейдж: `🔴 Первопричина`, `🟠 Следствие`, `🟡 Симптом`, `🟢 Фоновый шум`).
       - **Колонка 2: Количество проблемных логов за выбранное время** (крупная цифра + цветная полоска доли от общего числа ошибок).
       - **Колонка 3: Изменение частоты («Было до аварии → Стало»)** (например: `0 / мин → 60 / мин`, бейдж `Рост в 60 раз`).
       - **Колонка 4: Сдвиг по времени от начала аварии** (`Начался первым (0 сек)`, `Через +2 сек`, `Через +4 сек`, `Шёл постоянно`).
       - **Колонка 5: Корреляция (Сила связи с первопричиной в % и с каким сервисом связан):**
         - Наглядная цветная шкала `0% .. 100%` (фиолетовый/рубиновый для высокой связи `>75%`, янтарный для средней `40..75%`, изумрудно-серый для шума `<30%`).
         - Человеческое пояснение на русском:
           - *«Сильная связь (96%) с billing-db-proxy: волна ошибок повторяет первопричину с задержкой +2 сек»*;
           - *«Связи с аварией нет (0%): частота ошибок не менялась»*.

2. **Сохранение всего старого функционала по отдельной кнопке + полная русификация навигации (`TopNavHeader.tsx`, `App.tsx`, `navigation.ts`):**
   - В `navigation.ts` добавить страницу `'overview'` (понятный русскоязычный обзор по умолчанию) в `AppPageId`:
     `export type AppPageId = 'overview' | 'ingest' | 'hypotheses' | 'topology' | 'chains' | 'evidence' | 'classic_v1';`
   - В верхней панели `TopNavHeader.tsx`:
     - **Основное понятное меню на русском языке (с разноцветными иконками/акцентами):**
       1. **`✨ Понятный разбор аварии`** (`'overview'`) — **открывается по умолчанию**, включает цепочку «Причина → Последствия» (`SimpleRcaOverviewPage`) и панель «Количество проблемных логов и Корреляции» (`ProblemLogsAndCorrelationPanel`)!
       2. **`📥 Загрузка логов и Tilt`** (`'ingest'`) — добавление файлов и живой захват.
       3. **`🕸️ Граф связей и Таймлайн`** (`'topology'`) — крупная карта вызовов сервисов.
       4. **`📑 Доказательства и Отчёт для ИИ`** (`'evidence'`) — скачивание отчётов и копирование для Google Gemini.
     - **Отдельная заметная кнопка-переключатель «⚙️ Профессиональные / Старые инструменты»** в правой части шапки:
       - По нажатию открывает удобное меню или переключает режим, где доступны **все прежние детальные экраны без единого удаления**:
         - `🔬 Детальная таблица гипотез и 8 сигналов (S1..S8)` (`'hypotheses'`)
         - `🔍 Трассировка цепочек и Терминал логов` (`'chains'`)
         - `🏛️ Классический одностраничный экран (Все панели на 1 экране v1)` (`'classic_v1'` — рендерит `StorageHeader` + `CaseSidebar` + `RcaDashboard` + `EvidenceAndGeminiPanel` в классическом виде).
     - Перевести все оставшиеся английские статусы и бейджи (`RAW_PURGED` → `Сырые логи очищены`, `READY` → `Готов`, `ANALYZED` → `Проанализирован`).

---

## 2. Точные TypeScript-контракты

### 2.1. `./services/frontend-ui/src/components/ProblemLogsAndCorrelationPanel.tsx`
```ts
import { AnalyzeCaseResponse } from '../api/client';

export interface ProblemLogsAndCorrelationPanelProps {
  analysis: AnalyzeCaseResponse;
  onSelectServiceLogs?: (pod: string, onsetTime: string) => void;
}
```

### 2.2. Обновлённый `./services/frontend-ui/src/types/navigation.ts`
```ts
import { CaseMeta, AnalyzeCaseResponse } from '../api/client';

export type AppPageId =
  | 'overview'
  | 'ingest'
  | 'hypotheses'
  | 'topology'
  | 'chains'
  | 'evidence'
  | 'classic_v1';

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
```

---

## 3. Команда проверки (Verification Gate)

> ⚠️ **ВАЖНОЕ ПРАВИЛО ДЛЯ WINDOWS POWERSHELL:** Никогда НЕ вызывай команду `curl` без расширения `.exe` в PowerShell (так как в PowerShell `curl` — это интерактивный алиас `Invoke-WebRequest`, который зависает в фоновом режиме!). Используй только `py ./verify_task.py TASK-28` и `py ./tools/synthgen/e2e_verify.py`.

```powershell
docker run --rm -v "${PWD}/services/frontend-ui:/app" -v rca_npm_cache:/root/.npm -w /app node:22-alpine sh -c "npm run build"
docker compose build frontend-ui
docker compose up -d frontend-ui
py ./verify_task.py TASK-28
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
