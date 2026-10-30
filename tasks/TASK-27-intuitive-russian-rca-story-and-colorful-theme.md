# TASK-27: Интуитивный русскоязычный интерфейс (Часть 1) — Переводчик терминов на человеческий русский язык, наглядная дорожка «Причина → Последствия» и богатая многоцветная палитра UI (`./services/frontend-ui`)

- **Статус:** `IN_REVIEW`
- **Зависимости:** `TASK-26`
- **Область видимости (Scope):**
  - Читать: `./services/frontend-ui/src/api/client.ts`, `./services/frontend-ui/src/types/navigation.ts`, `./services/frontend-ui/src/styles.css`
  - Создавать/редактировать:
    - `./services/frontend-ui/src/utils/humanRcaTranslator.ts`
    - `./services/frontend-ui/src/components/HelpTooltip.tsx`
    - `./services/frontend-ui/src/components/SimpleRcaOverviewPage.tsx`
    - `./services/frontend-ui/src/styles.css`
  - **НЕ трогать:** Go-микросервисы, **НЕ удалять** ни один из существующих компонентов (`TopNavHeader.tsx`, `IngestAndTiltPage.tsx`, `HypothesesPage.tsx`, `TopologyAndTimelinePage.tsx`, `ChainAndContextPage.tsx`, `EvidenceAndGeminiPanel.tsx`, `StorageHeader.tsx`, `CaseSidebar.tsx`, `RcaDashboard.tsx`).

---

## 1. Цель задачи

Сделать результаты анализа **интуитивно понятными с первого взгляда для любого пользователя (даже незнакомого с внутренней математикой RCA)**, полностью избавиться от смеси английского и русского языков на главном экране и заменить монотонную синюю расцветку на **богатую, живую многоцветную палитру**:

1. **Модуль человекочитаемого русского языка и цветовой палитры сервисов (`src/utils/humanRcaTranslator.ts`):**
   - **Перевод всех ролей без англицизмов:**
     - `ROOT_CAUSE` → **`🔴 Первопричина аварии (источник сбоя)`** (коротко: `Первопричина`)
     - `PROPAGATION` → **`🟠 Следствие (цепная реакция от первопричины)`** (коротко: `Следствие по цепочке`)
     - `SYMPTOM` → **`🟡 Внешний симптом (ошибки у клиентов на входе)`** (коротко: `Внешний симптом`)
     - `NOISE` → **`🟢 Фоновый шум (было и до аварии, не влияет на сбой)`** (коротко: `Фоновый шум`)
   - **Перевод всех классов ошибок (`error_class`) на понятный русский язык:**
     - `OWN_DEFECT` → `Внутренняя ошибка в коде сервиса (паника / сбой логики)`
     - `RESOURCE` → `Исчерпание ресурсов (нехватка соединений к БД / памяти / пула)`
     - `DEADLINE` → `Превышено время ожидания ответа от другого сервиса (таймаут)`
     - `PEER_5XX` → `Соседний сервис вернул ошибку 5xx при вызове`
     - `CANCELED` → `Клиент сам оборвал запрос, не дождавшись ответа`
     - `CLIENT_4XX` → `Ошибка параметров клиентского запроса (4xx)`
     - `AUTH` → `Ошибка доступа или авторизации`
     - прочие / пустые → `Общая ошибка выполнения`
   - **Генератор понятного русского объяснения (`buildHumanRussianExplanation(h, rootCause, durationMinutes)`):**
     - Полностью заменяет сухой английский текст `Primary root cause in service...` с бэкенда на живой, понятный русский абзац:
       - **Для `ROOT_CAUSE`:**
         *«Это главный источник аварии — чинить нужно именно здесь. Сбой начался в сервисе «{service}» (файл {source_place}, контейнер {pod}) в {time} — раньше всех остальных проблем. Что произошло: {humanErrorClass}. До аварии таких ошибок было {baseline}/мин, а во время сбоя стало {incident}/мин (оценочно ~{totalErrors} проблемных логов за время сбоя). Из-за отказа этого сервиса по цепочке посыпались ошибки в соседних сервисах.»*
       - **Для `PROPAGATION`:**
         *«Это НЕ причина аварии, а её прямое последствие (цепная реакция). Сервис «{service}» ({source_place}) начал выдавать ошибки через +{lag} сек после поломки первопричины («{rootService}»), так как не смог получить от неё ответ ({humanErrorClass}). Когда вы почините «{rootService}», эта ошибка уйдёт сама.»*
       - **Для `SYMPTOM`:**
         *«Это внешний симптом, который заметили пользователи на входе в систему («{service}»). Возник из-за того, что внутренние сервисы перестали вовремя отвечать на запросы ({humanErrorClass}).»*
       - **Для `NOISE`:**
         *«Это постоянный фоновый шум в сервисе «{service}». Такие записи шли с одинаковой частотой ({incident}/мин) и до аварии, и во время неё. К текущему сбою они не имеют отношения.»*
   - **Уникальная яркая цветовая палитра для каждого микросервиса (`getServiceColor(serviceName)`):**
     - Назначает каждому сервису стабильный яркий цвет из 8 богатых оттенков (Кораллово-рубиновый `#f43f5e`, Янтарно-оранжевый `#f59e0b`, Изумрудно-зелёный `#10b981`, Аметистово-фиолетовый `#8b5cf6`, Бирюзово-циановый `#06b6d4`, Пурпурно-розовый `#ec4899`, Лаймовый `#84cc16`, Золотистый `#eab308`), чтобы графики и карточки были яркими и мгновенно различались визуально.

2. **Главный интуитивный экран: «🎯 Понятный разбор аварии: Причина → Последствия» (`src/components/SimpleRcaOverviewPage.tsx`):**
   - Создан специально для человека, который хочет за 10 секунд понять суть проблемы без изучения сложных таблиц:
   - **Блок А. Главный вердикт простыми словами (Hero-карточка с ярким рубиново-пурпурным акцентом):**
     - Крупный заголовок на русском: **«Главный вывод: в чём причина аварии и что делать»**.
     - Короткая и ясная сводка в 3 пункта:
       1. 🔴 **Где начался сбой (Что чинить):** сервис **`{root.service}`**, файл и строка **`{root.source_place}`**, контейнер `{root.dominant_pod}`, старт в `{onsetTime}`.
       2. ⚡ **Что пострадало следом (Последствия):** перечень затронутых по цепочке сервисов (`crm-service`, `chat-session`, `api-gateway` и т.д.) с указанием задержки в секундах (`+2 сек`, `+4 сек`, `+5 сек`).
       3. 📊 **Масштаб проблемы:** сколько всего проблемных логов зафиксировано за время сбоя и сколько фонового шума система автоматически отсеяла.
     - Быстрые цветные кнопки действий:
       - **«📄 Посмотреть строки логов в момент поломки»** (открывает удобное модальное окно с логами `±20 строк` вокруг момента начала сбоя на русском языке);
       - **«✨ Открыть готовый разбор для ИИ (Google Gemini)»**;
       - **«📊 Перейти к графику количества ошибок и корреляциям ↓»**.
   - **Блок Б. Наглядная визуальная дорожка «Цепочка развития аварии: Шаг за шагом» (3 разноцветных уровня со стрелками):**
     - **ШАГ 1 (Рубиново-коралловая тема `#f43f5e`): 🔥 ПЕРВОПРИЧИНА (Источник аварии — началось здесь)**
       - Карточка(и) `ROOT_CAUSE` с русским объяснением, количеством проблемных логов за время аварии, ростом ошибок (`Было 0/мин → Стало 60/мин`), русским описанием ошибки и кнопкой просмотра логов.
     - **Крупный визуальный разделитель-стрелка:**
       `⬇️ ЦЕПНАЯ РЕАКЦИЯ: через +2..4 секунды отказ перекинулся на зависимые сервисы ⬇️`
     - **ШАГ 2 (Янтарно-оранжевая тема `#f59e0b`): ⚡ ПОСЛЕДСТВИЯ В СОСЕДНИХ СЕРВИСАХ (Упали по цепочке)**
       - Карточки `PROPAGATION` в наглядной сетке. На каждой карточке крупный бейдж: **«Упал через +{lag} сек после первопричины • Связь по времени (корреляция): {corr}%»**, понятное русское объяснение, почему этот сервис — жертва, а не виновник, и сколько проблемных логов он сгенерировал.
     - **Крупный визуальный разделитель-стрелка:**
       `⬇️ РЕЗУЛЬТАТ НА ВХОДЕ: что в итоге увидели пользователи ⬇️`
     - **ШАГ 3 (Золотисто-жёлтая тема `#eab308`): 👥 ВНЕШНИЕ СИМПТОМЫ (Ошибки у клиентов)**
       - Карточки `SYMPTOM` (`api-gateway`, `auth-service` и др.) с русским пояснением.
     - **Свёрнутый аккуратный блок внизу (Изумрудно-зелёная тема `#10b981`): 🌿 «Отсеянный фоновый шум ({N} шт.) — ошибки, которые шли всегда и не связаны с аварией»** (раскрывается по клику).
   - **Кнопка «🔬 Показать технические метрики (8 сигналов S1..S8)»** на каждой карточке — по умолчанию скрыта, чтобы не перегружать экран, но открывается в 1 клик с полным русским описанием всех 8 сигналов!

3. **Универсальная кнопочка-подсказка со знаком вопроса `?` на каждом блоке UI (`src/components/HelpTooltip.tsx`):**
   - **Размер и внешний вид:** аккуратная круглая иконка-кнопочка `?` размером **`15px × 15px`** (чуть меньше стандартного курсора мыши `18–20px`, но отлично заметная благодаря контрастному контуру, мягкому свечению и знаку `?` по центру). При наведении курсора иконка плавно подсвечивается ярким бирюзовым или фиолетовым цветом.
   - **Поведение при наведении (Hover / Focus):** мгновенно и без задержки всплывает аккуратное, стилизованное русскоязычное окошко-подсказка (`Popover` шириной `280px–340px`, высокий `z-index`, мягкая тень и цветная верхняя полоска), в котором коротко и понятно для новичка написано:
     1. **Заголовок блока** (`title`);
     2. **Зачем нужен этот блок и что он показывает** (`description`);
     3. **Как им пользоваться** (`howToUse`, опционально).
   - **Обязательное размещение в `SimpleRcaOverviewPage.tsx`:**
     - На карточке «Главный вывод» (объясняет, как система автоматически нашла источник поломки и отделила его от последствий);
     - На заголовке «Шаг 1. Первопричина аварии» (объясняет, почему чинить нужно именно этот сервис);
     - На заголовке «Шаг 2. Цепная реакция (Последствия)» (объясняет, что такое задержка `+2 сек` и почему эти сервисы упали следом);
     - На заголовке «Шаг 3. Внешние симптомы» (объясняет, что эти ошибки видят конечные пользователи на входном шлюзе);
     - На блоке «Отсеянный фоновый шум» (объясняет, почему постоянные фоновые ошибки скрыты, чтобы не отвлекать);
     - Возле метрик на карточке (`Количество проблемных логов`, `Рост частоты`, `Корреляция по времени`, `8 сигналов S1..S8`).

4. **Многоцветная палитра и стили подсказок в `src/styles.css`:**
   - Добавить богатые цветовые переменные и стили карточек с градиентными акцентами:
     - Рубиново-красный (`--color-ruby`), Янтарно-оранжевый (`--color-amber`), Золотистый (`--color-gold`), Изумрудно-зелёный (`--color-emerald`), Аметистово-фиолетовый (`--color-violet`), Бирюзовый (`--color-cyan`), Розово-пурпурный (`--color-rose`).
   - Оформить визуальную дорожку шагов аварии, цветные бейджи задержек (`+2 сек`), индикаторы силы корреляции, кнопки переключения режимов и стили для `HelpTooltip` (`.help-tooltip-wrapper`, `.help-tooltip-trigger` размером `15px × 15px`, `.help-tooltip-popover`).

---

## 2. Точные TypeScript-контракты

### 2.1. `./services/frontend-ui/src/components/HelpTooltip.tsx`
```ts
import React from 'react';

export interface HelpTooltipProps {
  title: string;
  description: string;
  howToUse?: string;
  position?: 'top' | 'bottom' | 'left' | 'right';
  accentColor?: 'cyan' | 'ruby' | 'amber' | 'emerald' | 'violet' | 'gold';
}

export const HelpTooltip: React.FC<HelpTooltipProps>;
```

### 2.2. `./services/frontend-ui/src/utils/humanRcaTranslator.ts`
```ts
import { Hypothesis, TimelinePoint } from '../api/client';

export interface ServiceColorTheme {
  hex: string;
  bgSoft: string;
  border: string;
  text: string;
}

export function getRoleRussianLabel(role: Hypothesis['role']): string;
export function getRoleShortRussianLabel(role: Hypothesis['role']): string;
export function getErrorClassRussianLabel(errorClass: string): string;
export function getSignalRussianInfo(signalKey: string): { title: string; description: string };
export function buildHumanRussianExplanation(
  h: Hypothesis,
  rootCause: Hypothesis | null,
  incidentDurationMinutes: number
): string;
export function estimateHypothesisProblemLogsCount(
  h: Hypothesis,
  incidentDurationMinutes: number
): number;
export function getServiceColor(serviceName: string): ServiceColorTheme;
export function calculateIncidentDurationMinutes(startIso?: string, endIso?: string): number;
export function computeLagSecondsFromRoot(h: Hypothesis, rootCause: Hypothesis | null): number | null;
export function getBestCorrelationWithRoot(h: Hypothesis, rootCause: Hypothesis | null): number | null;
```

### 2.2. `./services/frontend-ui/src/components/SimpleRcaOverviewPage.tsx`
```ts
import { AnalyzeCaseResponse } from '../api/client';

export interface SimpleRcaOverviewPageProps {
  caseId: string;
  analysis: AnalyzeCaseResponse | null;
  loading: boolean;
  onOpenGeminiModal: () => void;
  onSwitchToEngineeringMode: (targetPage?: 'hypotheses' | 'topology' | 'chains' | 'evidence') => void;
}
```

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}/services/frontend-ui:/app" -v rca_npm_cache:/root/.npm -w /app node:22-alpine sh -c "npx tsc --noEmit"
py ./verify_task.py TASK-27
```

---

## Отчёт о выполнении (заполняется субагентом-воркером)

- **Дата выполнения:** 2026-10-30 14:40
- **Созданные/изменённые файлы:**
  - `services/frontend-ui/src/utils/humanRcaTranslator.ts` (263 строки): 100% реализация переводчика на русский язык (`getRoleRussianLabel`, `getRoleShortRussianLabel`, `getErrorClassRussianLabel`, `getSignalRussianInfo` для S1..S8, `buildHumanRussianExplanation`, `estimateHypothesisProblemLogsCount`, `getServiceColor` с 8 богатыми оттенками, `calculateIncidentDurationMinutes`, `computeLagSecondsFromRoot`, `getBestCorrelationWithRoot`).
  - `services/frontend-ui/src/components/HelpTooltip.tsx` (84 строки): компактный компонент справки `?` ровно 15px x 15px с popover (310px), подсветкой при наведении, цветной верхней полоской, заголовком, описанием и блоком «Как использовать».
  - `services/frontend-ui/src/components/SimpleRcaOverviewPage.tsx` (768 строк): полноценная страница понятного разбора аварии на русском языке с Hero-карточкой («Главный вывод: в чём причина аварии и что делать», где начался сбой, последствия с задержками, масштаб проблемы и отсеянный шум, быстрые цветные кнопки действий, включая просмотр контекстных логов ±20 строк через `api.getContextLogs`), 3-шаговой визуальной дорожкой со стрелками (Шаг 1: Первопричина, Шаг 2: Каскадные последствия, Шаг 3: Внешние симптомы у клиентов, свёрнутый блок отсеянного фонового шума), кнопкой раскрытия 8 технических сигналов S1..S8 и размещением `<HelpTooltip />` на всех блоках и метриках.
  - `services/frontend-ui/src/styles.css`: добавлены цветовые переменные (`--color-ruby`, `--color-amber`, `--color-gold`, `--color-emerald`, `--color-violet`, `--color-cyan`, `--color-rose`, `--color-lime`), стили HelpTooltip (ровно 15x15px, поповеры, акценты), визуальной дорожки, разделителей-стрелок, бейджей задержек и модального окна логов.
- **Результаты проверок и тестов:**
  - `docker run --rm -v "${PWD}/services/frontend-ui:/app" -v rca_npm_cache:/root/.npm -w /app node:22-alpine sh -c "npx tsc --noEmit"` — `PASS` (0 ошибок TypeScript).
  - `docker run --rm -v "${PWD}/services/frontend-ui:/app" -v rca_npm_cache:/root/.npm -w /app node:22-alpine sh -c "npm run build"` — `PASS` (успешная сборка Vite в production).
  - `py ./verify_task.py TASK-27` — `[VERIFY PASSED] TASK-27 passed all automated checks!`
  - Отсутствие `// TODO`, `// FIXME` или заглушек — проверено и подтверждено.
- **Заметки по реализации:**
  - Для вычисления опережения и корреляций реализованы алгоритмы, проверяющие `lead_lag_peers`, временные метки `onset_time` и сигналы `lead_lag_score`, обеспечивающие точные задержки `+N сек` и проценты корреляции.
  - Палитра микросервисов содержит детерминированный строковый хеш для любых неизвестных названий сервисов с фиксированными слотами для стандартных сервисов архитектуры.
  - Модальное окно просмотра логов позволяет фильтровать логи по уровню (ERROR, WARN, INFO), искать по тексту и копировать строки в буфер обмена.

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты:**
  - `services/frontend-ui/src/utils/humanRcaTranslator.ts`: Полная посимвольная проверка всех экспортируемых функций (`getRoleRussianLabel`, `getRoleShortRussianLabel`, `getErrorClassRussianLabel`, `getSignalRussianInfo`, `buildHumanRussianExplanation`, `estimateHypothesisProblemLogsCount`, `getServiceColor`, `calculateIncidentDurationMinutes`, `computeLagSecondsFromRoot`, `getBestCorrelationWithRoot`) и интерфейса `ServiceColorTheme`. Все контракты строго соблюдены.
  - `services/frontend-ui/src/components/HelpTooltip.tsx`: Проверен контракт `HelpTooltipProps` и реализация компонента `HelpTooltip`. Размер триггера `?` строго зафиксирован как ровно `15px × 15px` (`.help-tooltip-trigger`), попап всплывает мгновенно, снабжён заголовком, описанием и блоком «Как использовать».
  - `services/frontend-ui/src/components/SimpleRcaOverviewPage.tsx`: Проверен контракт `SimpleRcaOverviewPageProps` и реализация Hero-карточки («Главный вывод»), 3-шаговой цепочки развития аварии (Шаг 1: Первопричина `#f43f5e`, Шаг 2: Каскадные последствия `#f59e0b`, Шаг 3: Внешние симптомы `#eab308`), свёрнутого блока отсеянного шума (`#10b981`), раскрытия 8 технических сигналов S1..S8, и модального окна просмотра контекстных логов (±20 строк вокруг сбоя). Компоненты `<HelpTooltip />` размещены на всех ключевых блоках и метриках.
  - `services/frontend-ui/src/styles.css`: Проверена богатая цветовая палитра (`--color-ruby`, `--color-amber`, `--color-gold`, `--color-emerald`, `--color-violet`, `--color-cyan`, `--color-rose`, `--color-lime`), стили триггера `15px × 15px`, всплывающих подсказок и разделителей-стрелок дорожки аварии.
  - **Личный прогон верификации:**
    - `py ./verify_task.py TASK-27` — `[VERIFY PASSED] TASK-27 passed all automated checks!` (код возврата `0`).
    - `docker run --rm -v "${PWD}/services/frontend-ui:/app" -v rca_npm_cache:/root/.npm -w /app node:22-alpine sh -c "npx tsc --noEmit"` — `PASS` (0 ошибок компиляции TypeScript, код возврата `0`).
  - Полное отсутствие `TODO`, `FIXME` и заглушек. Защита от `NaN`/`Inf` и деления на 0 во всех математических вспомогательных функциях. Чужие файлы и сервисы не затронуты.
- **Найденные замечания:** Нет. Реализация полностью соответствует спецификации TASK-27 и чек-листу REVIEWER_GUIDE.
