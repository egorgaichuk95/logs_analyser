# TASK-25: Редизайн UI v2 (Часть 2) — Страница 2 «🎯 Гипотезы RCA» во всю ширину с детальным окном (Slide-over Drawer) и Страница 3 «🕸️ Граф и Таймлайн» (`./services/frontend-ui`)

- **Статус:** `DONE`
- **Зависимости:** `TASK-24`
- **Область видимости (Scope):**
  - Читать: `./services/frontend-ui/src/api/client.ts`, `./services/frontend-ui/src/types/navigation.ts`, `./services/frontend-ui/src/styles.css`
  - Создавать/редактировать:
    - `./services/frontend-ui/src/components/HypothesesPage.tsx`
    - `./services/frontend-ui/src/components/TopologyAndTimelinePage.tsx`
    - `./services/frontend-ui/src/styles.css`
  - **НЕ трогать:** Go-микросервисы, не удалять `RcaDashboard.tsx`.

---

## 1. Цель задачи

Разделить перегруженный дашборд RCA (где раньше карточки гипотез и граф сжимали друг друга в двух узких колонках) на **две отдельные полноэкранные страницы** с открытием глубоких деталей в удобном широком выдвижном окне (`Drawer` / `Modal`):

1. **Страница 2: «🎯 Результаты RCA (Гипотезы)» (`src/components/HypothesesPage.tsx`):**
   - **Верхняя панель сводки инцидента (KPI Cards):**
     - 4 информативные плашки в ряд:
       1. `Первопричина (Root Cause)`: имя главного сервиса + `source_place` (`pool_manager.go:84`);
       2. `Окно инцидента`: `incident_start` — `incident_end` + `run_id`;
       3. `Распределение гипотез`: счётчики по ролям (`🔴 ROOT_CAUSE`, `🟠 PROPAGATION`, `🟡 SYMPTOM`, `⚪ NOISE`);
       4. `Рассинхрон часов (Clock Skew)`: статус (или кнопка открытия подробного списка подов со сдвигом `clock_skew_findings`).
   - **Панель фильтрации и поиска:**
     - Фильтр-табы по ролям: `Все гипотезы` | `🔴 Первопричины (ROOT_CAUSE)` | `🟠 Каскад (PROPAGATION)` | `🟡 Симптомы (SYMPTOM)` | `⚪ Шум (NOISE)`.
     - Поле быстрого текстового поиска по сервису, тексту шаблона или файлу (`source_place`).
   - **Широкие читабельные карточки гипотез (на 100% ширины контейнера):**
     - Крупный заголовок: ранг `#1`, цветовой бейдж роли (`ROOT_CAUSE` / `PROPAGATION` / `SYMPTOM` / `NOISE`), крупный индикатор уверенности (`Confidence: XX.X%`), класс ошибки (`error_class`).
     - Крупная наглядная цепочка локализации сбоя:
       `Сервис (service)` → `Pod (dominant_pod, перекос XX%)` → `Файл и строка (source_place)` → `Маршрут (dominant_route)`.
     - Полный читабельный текст шаблона ошибки (`template_text`) в аккуратном кодовом блоке (без тесного обрезания) и человекочитаемое объяснение (`explanation`).
     - Метрики в одну чистую строку: `Onset Time`, `Baseline Rate / мин`, `Incident Rate / мин`, `Кратность роста (×N)`, `Poisson p-value`, бейдж повторов в прошлых кейсах (`recurring_case_ids`).
     - **Кнопки действий на карточке (открывают просторное окно деталей):**
       - **«📊 Открыть разбор 8 сигналов (S1..S8) и связей»** — открывает широкое выдвижное окно (`HypothesisDetailDrawer`) с вкладкой *«Сигналы S1..S8 и Lead-Lag»*.
       - **«📜 Смотреть контекст логов (±20 строк)»** — открывает то же окно сразу на вкладке *«Контекст логов (±20 строк)»* и автоматически подгружает `GET /api/v1/cases/{id}/logs/context`.
       - **«📑 Улики (E-xx)»** — показывает привязанные к этой гипотезе доказательства `evidence_ids`.
   - **Выдвижное окно деталей гипотезы (`HypothesisDetailDrawer` / Модальное окно шириной `820px`):**
     - Позволяет комфортно и крупно изучить выбранную гипотезу:
       - Подробная расшифровка всех 8 сигналов (`trace_chain_score`, `onset_score`, `lead_lag_score`, `novelty_score`, `causal_dir_score`, `graph_root_score`, `localization_score`, `change_corr_score`) с названиями, описанием смысла каждого сигнала и широкими прогресс-барами `[0.00 .. 1.00]`.
       - Таблица опережающих/запаздывающих корреляций Пирсона (`lead_lag_peers`: сервис, лаг в секундах, коэффициент $r$).
       - Встроенный интерактивный просмотрщик контекста логов `±20 строк` с переключателем `Только Pod` (`mode="pod"`) / `Все сервисы` (`mode="all_services"`), подсветкой целевой строки и обработкой статуса `raw_purged: true`.

2. **Страница 3: «🕸️ Граф зависимостей и Таймлайн» (`src/components/TopologyAndTimelinePage.tsx`):**
   - Отдельная просторная страница для визуальной аналитики на весь экран:
     - **Верхний блок (на всю ширину, высота `460px+`): Интерактивный векторный SVG-граф вызовов сервисов (`service_edges`):**
       - Кнопки масштабирования (`🔍+ Увеличить`, `🔍- Уменьшить`, `🔄 Сбросить масштаб`).
       - Фильтр рёбер (`Все вызовы` | `Только с ошибками`).
       - Узлы сервисов раскрашены по их высшей роли в инциденте (`ROOT_CAUSE` — красный, `PROPAGATION` — оранжевый, `SYMPTOM` — жёлтый, `NOISE`/норма — синий/серый).
       - **Интерактивность:** при клике по любому узлу сервиса на графе справа или в модальном окне открывается карточка этого сервиса (входящие и исходящие вызовы, список гипотез этого сервиса и кнопка перехода к его логам).
     - **Нижний блок (на всю ширину, высота `340px+`): Векторный SVG-таймлайн интенсивности событий (`timeline`):**
       - Фильтр по конкретному сервису или всем сервисам сразу.
       - Наглядные столбцы по временным корзинам (`bucket`) с цветовой разбивкой по уровням (`ERROR`, `WARN`, `INFO`) и отображением пиковой латентности (`max_latency_ns`), с тултипом/деталями при выборе корзины.

---

## 2. Точные TypeScript-контракты компонентов

```ts
import { AnalyzeCaseResponse, EvidenceItem } from '../api/client';

export interface HypothesesPageProps {
  caseId: string;
  analysis: AnalyzeCaseResponse | null;
  loading: boolean;
  onNavigateToTopology: () => void;
  onNavigateToChains: (initialKey?: string) => void;
  onNavigateToEvidence: (highlightEvidenceId?: string) => void;
}

export interface TopologyAndTimelinePageProps {
  caseId: string;
  analysis: AnalyzeCaseResponse | null;
  onSelectServiceForLogs?: (serviceName: string) => void;
}
```

> **Защита от NaN / деления на ноль:** Все числовые значения сигналов, процентов и координат SVG должны проходить проверку `Number.isFinite(v) ? v : 0` и безопасное ограничение диапазона `Math.max(0, Math.min(1, v))`.

---

## 3. Команда проверки (Verification Gate)

```powershell
docker run --rm -v "${PWD}/services/frontend-ui:/app" -v rca_npm_cache:/root/.npm -w /app node:22-alpine sh -c "npx tsc --noEmit"
py ./verify_task.py TASK-25
```

---

## Отчёт о выполнении (заполняется субагентом-воркером)

- **Дата выполнения:** 2026-10-30 10:40 UTC
- **Соблюдение цикла TDD (Contract → Tests → Code):**
  - Шаг 1 (Контракты): строго реализованы интерфейсы `HypothesesPageProps` и `TopologyAndTimelinePageProps` с импортом DTO-типов из `api/client.ts`.
  - Шаг 2 (Тесты/Гейты): проверка типов и компиляции `tsc --noEmit` и автоматический верификатор `verify_task.py TASK-25`.
  - Шаг 3 (Реализация): 100% рабочего кода без TODO, FIXME или заглушек. Защита от деления на ноль и NaN (`Number.isFinite`, `safeNumber`, безопасные радиусы и шкалы).
- **Созданные/изменённые файлы:**
  - `services/frontend-ui/src/components/HypothesesPage.tsx` (+694 строк, 58.3 КБ)
  - `services/frontend-ui/src/components/TopologyAndTimelinePage.tsx` (+815 строк, 37.5 КБ)
  - `services/frontend-ui/src/styles.css` (+685 строк)
- **Результаты проверок и тестов:**
  - `docker run --rm -v "${PWD}/services/frontend-ui:/app" -v rca_npm_cache:/root/.npm -w /app node:22-alpine sh -c "npx tsc --noEmit"`: `PASS` (0 ошибок, 0 предупреждений)
  - `py ./verify_task.py TASK-25`: `[VERIFY PASSED] TASK-25 passed all automated checks!`
- **Заметки по реализации:**
  - Реализован `HypothesesPage.tsx`:
    - Верхняя сводная KPI-панель (Первопричина с быстрым переходом, окно инцидента с Run ID, сегментный бар распределения ролей ROOT_CAUSE/PROPAGATION/SYMPTOM/NOISE, статус рассинхрона часов с модальным окном детального списка сдвигов по подам).
    - Фильтр-табы по ролям со счётчиками, текстовый поиск по всем ключевым полям и селектор сортировки (ранг, confidence, рост, onset).
    - Карточки гипотез на 100% ширины контейнера: крупный ранг, бейдж роли, процент уверенности с мини-индикатором, класс ошибки, наглядная цепочка локализации `service -> pod -> source_place -> route`, полный код шаблона DRAIN с кнопкой копирования, объяснение причины, метрики onset/rates/growth/p-value/recurring.
    - Выдвижное окно деталей гипотезы (`HypothesisDetailDrawer`, ширина 820px) с вкладками: разбор 8 сигналов S1..S8 с широкими прогресс-барами и описанием, таблица lead_lag_peers, терминал логов ±20 строк (с переключением mode="pod" / mode="all_services", подсветкой строки сбоя и баннером raw_purged: true), вкладка связанных улик с переходом в отчёт.
  - Реализован `TopologyAndTimelinePage.tsx`:
    - Полноэкранный верхний блок (высота 480px) — векторный SVG-граф топологии вызовов (`service_edges`): зум (+, -, сброс), фильтр рёбер (все / только с ошибками), узлы раскрашены по ролям в инциденте с пульс-ореолом для ROOT_CAUSE, квадратичные кривые Безье для предотвращения наложения двунаправленных связей, боковой инспектор сервиса с деталями входящих/исходящих вызовов, гипотез и логов.
    - Полноэкранный нижний блок (высота 340px) — векторный SVG-таймлайн событий (`timeline`): фильтрация по сервису, стековые столбцы по уровням ERROR/WARN/INFO, кривая пиковой задержки, карточка деталей выбранной корзины.
  - Добавлены стили `v2-` в `styles.css`.
  - `RcaDashboard.tsx` не удалялся (обратная совместимость сохранена).

---

## Вердикт ревьюера (заполняется субагентом-ревьюером)

- **Статус ревью:** `APPROVED`
- **Проверенные контракты и тесты (TDD Coverage):**
  - Контракты интерфейсов `HypothesesPageProps` и `TopologyAndTimelinePageProps` посимвольно совпадают с разделом 2 задачи и экспортируются из `HypothesesPage.tsx` и `TopologyAndTimelinePage.tsx`.
  - Все исходные файлы созданы и валидны:
    - `services/frontend-ui/src/components/HypothesesPage.tsx` (1312 строк, 58.3 КБ)
    - `services/frontend-ui/src/components/TopologyAndTimelinePage.tsx` (969 строк, 37.5 КБ)
    - `services/frontend-ui/src/styles.css` (+685 строк стилей `v2-`, всего 4069 строк, 78.2 КБ)
    - `services/frontend-ui/src/components/RcaDashboard.tsx` сохранён для обратной совместимости (1050 строк).
  - TypeScript компиляция без ошибок: `docker run --rm -v "${PWD}/services/frontend-ui:/app" -v rca_npm_cache:/root/.npm -w /app node:22-alpine sh -c "npx tsc --noEmit"` — `PASS` (код возврата `0`, 0 ошибок, 0 предупреждений).
  - Автоматический верификатор `py ./verify_task.py TASK-25` лично запущен: `[VERIFY PASSED] TASK-25 passed all automated checks!` (код возврата `0`).
  - Полное отсутствие `TODO`, `FIXME`, `implement later` или заглушек.
  - Математическая безопасность: везде используется `safeNumber`, `clamp`, `safePercent`, `Number.isFinite()`, деление с ненулевым защитным знаменателем `(roleCounts.all || 1)`, `(n || 1)`, `Math.sqrt(...) || 1`.
  - Полнота интерфейса: 4 KPI плашки инцидента, фильтр-табы по ролям со счётчиками, быстрый поиск и сортировка, карточки гипотез на 100% ширины с цепочкой локализации `service -> pod -> source_place -> route`, DRAIN шаблоном, метриками и кнопками действий; `HypothesisDetailDrawer` (820px, S1..S8, `lead_lag_peers`, контекст логов ±20 строк с `raw_purged` баннером и переключением режимов, улики); масштабируемый SVG-граф с раскраской узлов по ролям, кривыми Безье и боковым инспектором сервиса; векторный SVG-таймлайн со стековыми столбцами, кривой латентности и карточкой корзины.
- **Найденные замечания (если есть):** Замечаний нет. Реализация выполнена в строгом соответствии с контрактами, архитектурным чертежом и требованиями надежности.
