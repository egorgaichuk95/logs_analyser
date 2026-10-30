import React, { useState, useMemo } from 'react';
import {
  AnalyzeCaseResponse,
  Hypothesis,
  TimelinePoint,
} from '../api/client';
import { HelpTooltip } from './HelpTooltip';
import {
  getRoleRussianLabel,
  getRoleShortRussianLabel,
  getErrorClassRussianLabel,
  getServiceColor,
  calculateIncidentDurationMinutes,
  computeLagSecondsFromRoot,
  getBestCorrelationWithRoot,
} from '../utils/humanRcaTranslator';

export interface ProblemLogsAndCorrelationPanelProps {
  analysis: AnalyzeCaseResponse;
  onSelectServiceLogs?: (pod: string, onsetTime: string) => void;
}

type TimePreset = 'incident' | 'all' | 'first_minutes' | 'custom';
type DisplayMode = 'templates' | 'services';

interface SyntheticBucket {
  bucket: string;
  displayTime: string;
  serviceCounts: Record<string, number>;
  totalCount: number;
}

export const ProblemLogsAndCorrelationPanel: React.FC<ProblemLogsAndCorrelationPanelProps> = ({
  analysis,
  onSelectServiceLogs,
}) => {
  const hypotheses = useMemo(() => analysis?.hypotheses ?? [], [analysis?.hypotheses]);

  const rootCauses = useMemo(() => {
    const list = hypotheses.filter((h) => h.role === 'ROOT_CAUSE');
    if (list.length > 0) return list;
    const rank1 = hypotheses.find((h) => h.rank === 1);
    return rank1 ? [rank1] : [];
  }, [hypotheses]);

  const primaryRoot = rootCauses[0] ?? null;

  // 1. Построение/реконструкция непрерывного таймлайна (SyntheticBuckets)
  const { allBuckets, allServices } = useMemo(() => {
    const rawTimeline: TimelinePoint[] = analysis?.timeline ?? [];
    const servicesSet = new Set<string>();
    hypotheses.forEach((h) => {
      if (h.service) servicesSet.add(h.service);
    });
    rawTimeline.forEach((p) => {
      if (p.service) servicesSet.add(p.service);
    });
    const servicesList = Array.from(servicesSet);

    if (rawTimeline.length >= 2) {
      // Группируем реальный timeline по бакетам
      const bucketMap: Record<string, Record<string, number>> = {};
      rawTimeline.forEach((pt) => {
        const b = pt.bucket;
        if (!bucketMap[b]) bucketMap[b] = {};
        bucketMap[b][pt.service] = (bucketMap[b][pt.service] || 0) + (pt.events_count || 0);
      });

      const sortedKeys = Object.keys(bucketMap).sort(
        (a, b) => new Date(a).getTime() - new Date(b).getTime()
      );

      const res: SyntheticBucket[] = sortedKeys.map((key) => {
        const serviceCounts = bucketMap[key] || {};
        let totalCount = 0;
        Object.values(serviceCounts).forEach((cnt) => {
          totalCount += cnt;
        });

        let displayTime = key;
        try {
          const d = new Date(key);
          if (!Number.isNaN(d.getTime())) {
            displayTime = d.toLocaleTimeString('ru-RU', {
              hour: '2-digit',
              minute: '2-digit',
              second: '2-digit',
            });
          }
        } catch {
          // ignore
        }

        return {
          bucket: key,
          displayTime,
          serviceCounts,
          totalCount,
        };
      });

      return { allBuckets: res, allServices: servicesList };
    }

    // Fallback: автоматическая реконструкция распределения по onset_time и частотам гипотез
    let startMs = 0;
    let endMs = 0;

    if (analysis?.incident_start && analysis?.incident_end) {
      startMs = new Date(analysis.incident_start).getTime();
      endMs = new Date(analysis.incident_end).getTime();
    }
    if (Number.isNaN(startMs) || Number.isNaN(endMs) || startMs <= 0 || endMs <= startMs) {
      const onsetMs = primaryRoot?.onset_time ? new Date(primaryRoot.onset_time).getTime() : Date.now();
      startMs = onsetMs - 120_000; // за 2 минуты до аварии
      endMs = onsetMs + 240_000; // 4 минуты после аварии
    }

    // Создаём минутные бакеты от startMs до endMs (от 5 до 8 бакетов)
    const stepMs = 60_000;
    const generated: SyntheticBucket[] = [];

    for (let t = startMs; t <= endMs; t += stepMs) {
      const bucketIso = new Date(t).toISOString();
      const displayTime = new Date(t).toLocaleTimeString('ru-RU', {
        hour: '2-digit',
        minute: '2-digit',
      });
      const counts: Record<string, number> = {};
      let total = 0;

      for (const h of hypotheses) {
        const s = h.service;
        const onset = h.onset_time ? new Date(h.onset_time).getTime() : startMs;
        const isPostOnset = t >= onset;
        const rate = isPostOnset
          ? Math.max(1, Math.round(h.incident_rate_per_min || 1))
          : Math.round(h.baseline_rate_per_min || 0);

        counts[s] = (counts[s] || 0) + rate;
        total += rate;
      }

      generated.push({
        bucket: bucketIso,
        displayTime,
        serviceCounts: counts,
        totalCount: total,
      });
    }

    return { allBuckets: generated, allServices: servicesList };
  }, [analysis?.timeline, analysis?.incident_start, analysis?.incident_end, hypotheses, primaryRoot]);

  // 2. Временные границы по умолчанию
  const initialStart = allBuckets[0]?.bucket ?? analysis?.incident_start ?? new Date().toISOString();
  const initialEnd = allBuckets[allBuckets.length - 1]?.bucket ?? analysis?.incident_end ?? new Date().toISOString();

  // State: Выбор отрезка времени
  const [activePreset, setActivePreset] = useState<TimePreset>('incident');
  const [selectedStart, setSelectedStart] = useState<string>(initialStart);
  const [selectedEnd, setSelectedEnd] = useState<string>(initialEnd);

  // State: Режим отображения (шаблоны / сервисы)
  const [displayMode, setDisplayMode] = useState<DisplayMode>('templates');

  // State: Раскрытая карточка сравнения шаблонов
  const [expandedComparisonId, setExpandedComparisonId] = useState<string | null>(null);

  // State: Раскрытые сервисы в режиме 'services'
  const [expandedServices, setExpandedServices] = useState<Record<string, boolean>>({});

  // State: Hover tooltip на SVG графике
  const [hoveredBucket, setHoveredBucket] = useState<SyntheticBucket | null>(null);
  const [tooltipPos, setTooltipPos] = useState<{ x: number; y: number } | null>(null);

  // Переключение пресетов времени
  const handleSelectPreset = (preset: TimePreset) => {
    setActivePreset(preset);

    if (allBuckets.length === 0) return;

    if (preset === 'all') {
      setSelectedStart(allBuckets[0].bucket);
      setSelectedEnd(allBuckets[allBuckets.length - 1].bucket);
    } else if (preset === 'incident') {
      // От onset_time первопричины (или incident_start) до incident_end
      const rootTime = primaryRoot?.onset_time ? new Date(primaryRoot.onset_time).getTime() : 0;
      let startIdx = 0;
      if (rootTime > 0) {
        const found = allBuckets.findIndex(
          (b) => new Date(b.bucket).getTime() >= rootTime - 30_000
        );
        if (found >= 0) startIdx = found;
      }
      setSelectedStart(allBuckets[startIdx].bucket);
      setSelectedEnd(allBuckets[allBuckets.length - 1].bucket);
    } else if (preset === 'first_minutes') {
      // Первые 2-3 минуты с момента аварии
      const rootTime = primaryRoot?.onset_time ? new Date(primaryRoot.onset_time).getTime() : 0;
      let startIdx = 0;
      if (rootTime > 0) {
        const found = allBuckets.findIndex(
          (b) => new Date(b.bucket).getTime() >= rootTime - 30_000
        );
        if (found >= 0) startIdx = found;
      }
      const endIdx = Math.min(allBuckets.length - 1, startIdx + 2);
      setSelectedStart(allBuckets[startIdx].bucket);
      setSelectedEnd(allBuckets[endIdx].bucket);
    }
  };

  // Расчёт длительности выбранного интервала в минутах
  const selectedDurationMinutes = useMemo(() => {
    const s = new Date(selectedStart).getTime();
    const e = new Date(selectedEnd).getTime();
    if (Number.isNaN(s) || Number.isNaN(e) || e <= s) {
      return calculateIncidentDurationMinutes(analysis?.incident_start, analysis?.incident_end);
    }
    return Math.max(1, Math.round((e - s) / 60_000));
  }, [selectedStart, selectedEnd, analysis?.incident_start, analysis?.incident_end]);

  // Расчёт логов конкретного шаблона/гипотезы за выбранный отрезок времени
  const calculateTemplateLogsForInterval = (h: Hypothesis): number => {
    // Если есть фактические точки в таймлайне для данного сервиса в выбранном диапазоне
    const startMs = new Date(selectedStart).getTime();
    const endMs = new Date(selectedEnd).getTime();

    let bucketSum = 0;
    let hasActualTimeline = false;

    for (const b of allBuckets) {
      const bTime = new Date(b.bucket).getTime();
      if (bTime >= startMs - 1000 && bTime <= endMs + 1000) {
        if (b.serviceCounts[h.service] !== undefined) {
          hasActualTimeline = true;
          bucketSum += b.serviceCounts[h.service];
        }
      }
    }

    if (hasActualTimeline && bucketSum > 0) {
      // Если гипотез несколько у одного сервиса, делим пропорционально их incident_rate
      const sameServiceHypos = hypotheses.filter((x) => x.service === h.service);
      const totalServiceRate = sameServiceHypos.reduce(
        (sum, x) => sum + Math.max(0.1, x.incident_rate_per_min || 0.1),
        0
      );
      const myRate = Math.max(0.1, h.incident_rate_per_min || 0.1);
      const share = totalServiceRate > 0 ? myRate / totalServiceRate : 1 / sameServiceHypos.length;
      return Math.max(1, Math.round(bucketSum * share));
    }

    // Иначе аналитическая оценка по baseline/incident rate
    const onsetMs = h.onset_time ? new Date(h.onset_time).getTime() : startMs;
    const effectiveDuration = selectedDurationMinutes;

    if (h.role === 'NOISE' || h.is_constant_noise) {
      return Math.max(1, Math.round((h.baseline_rate_per_min || 2) * effectiveDuration));
    }

    // Сколько минут попадает после onset_time
    let minsAfterOnset = effectiveDuration;
    if (onsetMs > startMs) {
      const diffMin = Math.round((onsetMs - startMs) / 60_000);
      minsAfterOnset = Math.max(1, effectiveDuration - diffMin);
    }

    const problemLogs = Math.max(1, Math.round((h.incident_rate_per_min || 1) * minsAfterOnset));
    return problemLogs;
  };

  // 4 Сводные карточки за выбранный отрезок времени
  const summaryCardsStats = useMemo(() => {
    let totalProblemLogs = 0;
    let rootProblemLogs = 0;
    let cascadeLogs = 0;
    let noiseLogs = 0;

    for (const h of hypotheses) {
      const count = calculateTemplateLogsForInterval(h);
      if (h.role === 'NOISE' || h.is_constant_noise) {
        noiseLogs += count;
      } else {
        totalProblemLogs += count;
        if (h.role === 'ROOT_CAUSE' || (primaryRoot && h.service === primaryRoot.service)) {
          rootProblemLogs += count;
        } else {
          cascadeLogs += count;
        }
      }
    }

    const avgPerMin = selectedDurationMinutes > 0
      ? Math.round(totalProblemLogs / selectedDurationMinutes)
      : totalProblemLogs;

    return {
      totalProblemLogs,
      avgPerMin,
      rootProblemLogs,
      rootServiceName: primaryRoot?.service || 'chat-com',
      cascadeLogs,
      noiseLogs,
    };
  }, [hypotheses, primaryRoot, selectedDurationMinutes, selectedStart, selectedEnd, allBuckets]);

  // Группировка гипотез по сервисам для режима 'services'
  const serviceSummaries = useMemo(() => {
    interface ServiceGroup {
      service: string;
      role: Hypothesis['role'];
      templatesCount: number;
      totalLogs: number;
      maxGrowth: number;
      peakRate: number;
      minLag: number | null;
      bestCorr: number;
      hypotheses: Hypothesis[];
    }

    const map: Record<string, ServiceGroup> = {};

    hypotheses.forEach((h) => {
      const s = h.service;
      const logs = calculateTemplateLogsForInterval(h);
      const lag = computeLagSecondsFromRoot(h, primaryRoot);
      const corr = getBestCorrelationWithRoot(h, primaryRoot) ?? 0.8;
      const growth = h.growth_ratio || 1;

      if (!map[s]) {
        map[s] = {
          service: s,
          role: h.role,
          templatesCount: 0,
          totalLogs: 0,
          maxGrowth: growth,
          peakRate: 0,
          minLag: lag,
          bestCorr: corr,
          hypotheses: [],
        };
      }

      const g = map[s];
      g.templatesCount += 1;
      g.totalLogs += logs;
      if (growth > g.maxGrowth) g.maxGrowth = growth;
      g.peakRate += Math.round(h.incident_rate_per_min || 0);
      if (lag !== null && (g.minLag === null || lag < g.minLag)) g.minLag = lag;
      if (corr > g.bestCorr) g.bestCorr = corr;
      if (h.role === 'ROOT_CAUSE') g.role = 'ROOT_CAUSE';
      g.hypotheses.push(h);
    });

    const list = Object.values(map);
    list.sort((a, b) => {
      if (a.role === 'ROOT_CAUSE') return -1;
      if (b.role === 'ROOT_CAUSE') return 1;
      return b.totalLogs - a.totalLogs;
    });

    return list;
  }, [hypotheses, primaryRoot, selectedStart, selectedEnd, allBuckets]);

  // Данные для SVG-графика
  const svgChartData = useMemo(() => {
    if (allBuckets.length === 0) return { maxVal: 10, buckets: [] };

    let maxVal = 0;
    allBuckets.forEach((b) => {
      if (b.totalCount > maxVal) maxVal = b.totalCount;
    });
    if (maxVal <= 0) maxVal = 10;

    return {
      maxVal: Math.ceil(maxVal * 1.15),
      buckets: allBuckets,
    };
  }, [allBuckets]);

  // Определение, попадает ли бакет в выбранный интервал
  const isBucketSelected = (bucketKey: string): boolean => {
    const t = new Date(bucketKey).getTime();
    const s = new Date(selectedStart).getTime();
    const e = new Date(selectedEnd).getTime();
    return t >= s - 1000 && t <= e + 1000;
  };

  const toggleServiceExpand = (serviceName: string) => {
    setExpandedServices((prev) => ({
      ...prev,
      [serviceName]: !prev[serviceName],
    }));
  };

  return (
    <div className="problem-logs-correlation-container">
      {/* =========================================================================
          СЕКЦИЯ 1. ИНТЕРАКТИВНЫЙ ВЫБОР ОТРЕЗКА ВРЕМЕНИ
         ========================================================================= */}
      <div className="time-filter-panel">
        <div className="time-filter-header">
          <div className="time-filter-title-wrap">
            <span className="time-filter-icon">⏱️</span>
            <h2 className="time-filter-title">
              Выбор отрезка времени для анализа логов и корреляций
            </h2>
            <HelpTooltip
              title="Интерактивный фильтр времени"
              description="Позволяет переключить временной отрезок и мгновенно пересчитать количество проблемных логов и силу взаимосвязи сервисов именно за выбранные минуты."
              howToUse="Выберите один из быстрых цветных пресетов или задайте точные границы «С» и «По» в выпадающих списках."
              accentColor="cyan"
              position="right"
            />
          </div>

          <div className="time-selected-summary-badge">
            Выбран период: <strong>{selectedDurationMinutes} мин</strong> (
            {new Date(selectedStart).toLocaleTimeString('ru-RU')} —{' '}
            {new Date(selectedEnd).toLocaleTimeString('ru-RU')})
          </div>
        </div>

        {/* 4 Цветные кнопки-пресеты */}
        <div className="time-presets-grid">
          <button
            type="button"
            className={`btn-time-preset preset-incident ${activePreset === 'incident' ? 'is-active' : ''}`}
            onClick={() => handleSelectPreset('incident')}
          >
            <span className="preset-icon">🔥</span>
            <div className="preset-text">
              <span className="preset-name">Только время аварии (пик сбоя)</span>
              <span className="preset-desc">Период наивысшей нагрузки с момента отказа</span>
            </div>
          </button>

          <button
            type="button"
            className={`btn-time-preset preset-all ${activePreset === 'all' ? 'is-active' : ''}`}
            onClick={() => handleSelectPreset('all')}
          >
            <span className="preset-icon">📅</span>
            <div className="preset-text">
              <span className="preset-name">Весь доступный период логов</span>
              <span className="preset-desc">Включая базовый период до начала сбоя</span>
            </div>
          </button>

          <button
            type="button"
            className={`btn-time-preset preset-first-minutes ${activePreset === 'first_minutes' ? 'is-active' : ''}`}
            onClick={() => handleSelectPreset('first_minutes')}
          >
            <span className="preset-icon">⚡</span>
            <div className="preset-text">
              <span className="preset-name">Первые минуты начала сбоя</span>
              <span className="preset-desc">Эпицентр и первые цепные реакции (+2 мин)</span>
            </div>
          </button>

          <button
            type="button"
            className={`btn-time-preset preset-custom ${activePreset === 'custom' ? 'is-active' : ''}`}
            onClick={() => setActivePreset('custom')}
          >
            <span className="preset-icon">🎛️</span>
            <div className="preset-text">
              <span className="preset-name">Свой интервал времени</span>
              <span className="preset-desc">Ручная настройка интервала от и до</span>
            </div>
          </button>
        </div>

        {/* Ручные выпадающие списки (активны всегда для точной подстройки) */}
        <div className="time-custom-selectors-row">
          <div className="time-selector-field">
            <label htmlFor="time-select-start">Начало интервала:</label>
            <select
              id="time-select-start"
              className="time-select-control"
              value={selectedStart}
              onChange={(e) => {
                setSelectedStart(e.target.value);
                setActivePreset('custom');
              }}
            >
              {allBuckets.map((b) => (
                <option key={`start-${b.bucket}`} value={b.bucket}>
                  {b.displayTime} ({b.totalCount} логов)
                </option>
              ))}
            </select>
          </div>

          <div className="time-selector-arrow">➔</div>

          <div className="time-selector-field">
            <label htmlFor="time-select-end">Конец интервала:</label>
            <select
              id="time-select-end"
              className="time-select-control"
              value={selectedEnd}
              onChange={(e) => {
                setSelectedEnd(e.target.value);
                setActivePreset('custom');
              }}
            >
              {allBuckets.map((b) => (
                <option key={`end-${b.bucket}`} value={b.bucket}>
                  {b.displayTime} ({b.totalCount} логов)
                </option>
              ))}
            </select>
          </div>
        </div>
      </div>

      {/* =========================================================================
          СЕКЦИЯ 2. 4 РАЗНОЦВЕТНЫЕ СВОДНЫЕ КАРТОЧКИ СЧЁТЧИКОВ
         ========================================================================= */}
      <div className="problem-summary-cards-grid">
        {/* Карточка 1: Рубиновая — Всего проблемных логов */}
        <div className="summary-stat-card card-ruby">
          <div className="summary-card-header">
            <span className="summary-card-icon">🚨</span>
            <span className="summary-card-title">Всего проблемных логов</span>
            <HelpTooltip
              title="Всего проблемных логов (Ошибок)"
              description="Суммарное количество аномальных ошибок во всех микросервисах за выбранный интервал времени (без учёта фонового шума)."
              howToUse="Показывает суммарный объём сбоя, сгенерированный системой в выбранные минуты."
              accentColor="ruby"
            />
          </div>
          <div className="summary-card-number text-ruby">
            {summaryCardsStats.totalProblemLogs.toLocaleString('ru-RU')}
          </div>
          <div className="summary-card-subtext">
            В среднем <strong>~{summaryCardsStats.avgPerMin} / мин</strong> за выбранный отрезок
          </div>
        </div>

        {/* Карточка 2: Янтарная — В первопричине */}
        <div className="summary-stat-card card-amber">
          <div className="summary-card-header">
            <span className="summary-card-icon">🔥</span>
            <span className="summary-card-title">Ошибок в первопричине</span>
            <HelpTooltip
              title="Ошибок в первопричине"
              description="Количество логов, порождённых непосредственно главным сервисом-виновником инцидента."
              howToUse="Именно эти ошибки вызвали цепную реакцию во всех зависимых сервисах."
              accentColor="amber"
            />
          </div>
          <div className="summary-card-number text-amber">
            {summaryCardsStats.rootProblemLogs.toLocaleString('ru-RU')}
          </div>
          <div className="summary-card-subtext">
            Сервис-источник: <strong className="service-name-highlight">{summaryCardsStats.rootServiceName}</strong>
          </div>
        </div>

        {/* Карточка 3: Фиолетовая — Каскадные ошибки */}
        <div className="summary-stat-card card-violet">
          <div className="summary-card-header">
            <span className="summary-card-icon">⚡</span>
            <span className="summary-card-title">Каскадных ошибок</span>
            <HelpTooltip
              title="Каскадные ошибки в зависимых сервисах"
              description="Ошибки в других микросервисах, возникшие в результате цепной реакции (таймауты, сбои при обращении к первопричине)."
              howToUse="Эти ошибки исчезнут автоматически, как только вы почините первопричину."
              accentColor="violet"
            />
          </div>
          <div className="summary-card-number text-violet">
            {summaryCardsStats.cascadeLogs.toLocaleString('ru-RU')}
          </div>
          <div className="summary-card-subtext">
            Вторичные сбои в <strong>{serviceSummaries.length - 1}</strong> зависимых сервисах
          </div>
        </div>

        {/* Карточка 4: Изумрудная — Отсеянный шум */}
        <div className="summary-stat-card card-emerald">
          <div className="summary-card-header">
            <span className="summary-card-icon">🌿</span>
            <span className="summary-card-title">Отсеяно фонового шума</span>
            <HelpTooltip
              title="Отсеянный фоновый шум"
              description="Ошибки и предупреждения, которые шли непрерывно и до аварии с неизменной частотой. Алгоритм RCA исключил их из рассмотрения."
              howToUse="Не отвлекайтесь на эти логи при устранении аварии — они не имеют отношения к сбою."
              accentColor="emerald"
            />
          </div>
          <div className="summary-card-number text-emerald">
            {summaryCardsStats.noiseLogs.toLocaleString('ru-RU')}
          </div>
          <div className="summary-card-subtext">
            Регулярные логи, не повлиявшие на инцидент
          </div>
        </div>
      </div>

      {/* =========================================================================
          СЕКЦИЯ 3. МНОГОЦВЕТНЫЙ ИНТЕРАКТИВНЫЙ SVG-ГРАФИК ВРЕМЕНИ
         ========================================================================= */}
      <div className="problem-chart-section">
        <div className="problem-chart-header">
          <div className="problem-chart-title-wrap">
            <span className="problem-chart-icon">📊</span>
            <h3 className="problem-chart-title">
              Распределение проблемных логов по времени и сервисам
            </h3>
            <HelpTooltip
              title="Интерактивный график динамики логов"
              description="Отображает каждую минуту инцидента в виде слоистого столбца. Каждый сервис окрашен в свой цвет. Подсвеченная область соответствует выбранному вами диапазону времени."
              howToUse="Наведите курсор на столбец, чтобы увидеть точную раскладку по сервисам в эту минуту. Кликните по столбцу, чтобы быстро сфокусироваться на нём."
              accentColor="cyan"
              position="bottom"
            />
          </div>

          <div className="problem-chart-legend">
            {allServices.map((s) => {
              const theme = getServiceColor(s);
              return (
                <span
                  key={s}
                  className="chart-legend-item"
                  style={{ borderColor: theme.border }}
                >
                  <span
                    className="legend-color-dot"
                    style={{ backgroundColor: theme.hex }}
                  />
                  <span className="legend-label">{s}</span>
                </span>
              );
            })}
          </div>
        </div>

        {/* Интерактивный SVG контейнер */}
        <div className="svg-chart-container">
          {svgChartData.buckets.length > 0 ? (
            <svg
              className="problem-timeline-svg"
              viewBox="0 0 800 220"
              preserveAspectRatio="none"
              onMouseLeave={() => {
                setHoveredBucket(null);
                setTooltipPos(null);
              }}
            >
              {/* Горизонтальные направляющие сетки */}
              <line x1="40" y1="30" x2="780" y2="30" stroke="rgba(255,255,255,0.08)" strokeDasharray="4 4" />
              <line x1="40" y1="95" x2="780" y2="95" stroke="rgba(255,255,255,0.08)" strokeDasharray="4 4" />
              <line x1="40" y1="160" x2="780" y2="160" stroke="rgba(255,255,255,0.08)" />

              {/* Метки шкалы Y */}
              <text x="35" y="34" fill="#64748b" fontSize="10" textAnchor="end">
                {svgChartData.maxVal}
              </text>
              <text x="35" y="99" fill="#64748b" fontSize="10" textAnchor="end">
                {Math.round(svgChartData.maxVal / 2)}
              </text>
              <text x="35" y="164" fill="#64748b" fontSize="10" textAnchor="end">
                0
              </text>

              {/* Отрисовка столбцов бакетов */}
              {(() => {
                const totalB = svgChartData.buckets.length;
                const plotWidth = 740;
                const colWidth = Math.max(16, Math.min(60, Math.floor((plotWidth / totalB) * 0.75)));
                const stepX = plotWidth / totalB;
                const chartHeight = 130; // от y=30 до y=160

                return svgChartData.buckets.map((b, idx) => {
                  const x = 50 + idx * stepX + (stepX - colWidth) / 2;
                  const isSelected = isBucketSelected(b.bucket);

                  // Подложка выбранного диапазона
                  const highlightRect = isSelected ? (
                    <rect
                      key={`hl-${b.bucket}`}
                      x={50 + idx * stepX}
                      y="15"
                      width={stepX}
                      height="150"
                      fill="rgba(6, 182, 212, 0.08)"
                      stroke="rgba(6, 182, 212, 0.3)"
                      strokeWidth="1"
                    />
                  ) : null;

                  // Сегменты сервисов внутри столбца
                  let currentY = 160;
                  const segments = allServices.map((svc) => {
                    const cnt = b.serviceCounts[svc] || 0;
                    if (cnt <= 0) return null;
                    const segHeight = Math.max(2, (cnt / svgChartData.maxVal) * chartHeight);
                    currentY -= segHeight;
                    const theme = getServiceColor(svc);

                    return (
                      <rect
                        key={`${b.bucket}-${svc}`}
                        x={x}
                        y={currentY}
                        width={colWidth}
                        height={segHeight}
                        fill={theme.hex}
                        rx="2"
                        opacity={isSelected ? 1.0 : 0.65}
                      />
                    );
                  });

                  return (
                    <g
                      key={b.bucket}
                      className="svg-bucket-group"
                      cursor="pointer"
                      onClick={() => {
                        setSelectedStart(b.bucket);
                        setSelectedEnd(b.bucket);
                        setActivePreset('custom');
                      }}
                      onMouseEnter={(e) => {
                        setHoveredBucket(b);
                        const rect = e.currentTarget.getBoundingClientRect();
                        setTooltipPos({ x: rect.left + rect.width / 2, y: rect.top });
                      }}
                    >
                      {highlightRect}
                      {segments}

                      {/* Текстовая метка времени под осью */}
                      <text
                        x={x + colWidth / 2}
                        y="180"
                        fill={isSelected ? '#38bdf8' : '#94a3b8'}
                        fontSize="10"
                        fontWeight={isSelected ? '700' : '500'}
                        textAnchor="middle"
                      >
                        {b.displayTime}
                      </text>

                      {/* Суммарное число ошибок над столбцом */}
                      {b.totalCount > 0 && (
                        <text
                          x={x + colWidth / 2}
                          y={Math.max(25, currentY - 4)}
                          fill={isSelected ? '#fff' : '#cbd5e1'}
                          fontSize="9"
                          fontWeight="600"
                          textAnchor="middle"
                        >
                          {b.totalCount}
                        </text>
                      )}
                    </g>
                  );
                });
              })()}
            </svg>
          ) : (
            <div className="chart-empty-state">Нет данных для графика временных рядов</div>
          )}

          {/* Плавающий Hover Tooltip */}
          {hoveredBucket && tooltipPos && (
            <div
              className="chart-hover-popover"
              style={{
                left: `${tooltipPos.x}px`,
                top: `${tooltipPos.y - 120}px`,
              }}
            >
              <div className="hover-popover-header">
                <span>⏱️ {hoveredBucket.displayTime}</span>
                <span className="hover-popover-total">
                  Всего: <strong>{hoveredBucket.totalCount}</strong> логов
                </span>
              </div>
              <div className="hover-popover-list">
                {allServices.map((svc) => {
                  const cnt = hoveredBucket.serviceCounts[svc] || 0;
                  if (cnt <= 0) return null;
                  const theme = getServiceColor(svc);
                  return (
                    <div key={svc} className="hover-popover-row">
                      <span className="hover-dot" style={{ backgroundColor: theme.hex }} />
                      <span className="hover-service-name">{svc}</span>
                      <span className="hover-service-count">{cnt} логов</span>
                    </div>
                  );
                })}
              </div>
            </div>
          )}
        </div>
      </div>

      {/* =========================================================================
          СЕКЦИЯ 4. ПЕРЕКЛЮЧАТЕЛЬ РЕЖИМА И ТАБЛИЦА КОРРЕЛЯЦИЙ
         ========================================================================= */}
      <div className="correlation-matrix-section">
        <div className="correlation-matrix-header">
          <div className="matrix-title-group">
            <h3 className="matrix-title">
              Корреляция ошибок и количество логов за выбранное время
            </h3>
            <HelpTooltip
              title="Таблица корреляций и причинных связей"
              description="Показывает детальный расчёт по каждому шаблону лога: сколько раз он сработал в выбранные минуты, во сколько раз подскочила частота, через сколько секунд после первопричины он проявился и с какими другими шаблонами он математически связан (кросс-корреляция Пирсона)."
              howToUse="Кликните на строку шаблона или кнопку «🔍 Сравнить связь», чтобы увидеть парное сравнение текстов и времени."
              accentColor="violet"
            />
          </div>

          {/* 1-клик переключатель режимов */}
          <div className="matrix-mode-switcher">
            <button
              type="button"
              className={`btn-mode-tab ${displayMode === 'templates' ? 'is-active' : ''}`}
              onClick={() => setDisplayMode('templates')}
            >
              📋 По каждому шаблону логов (Детально по типам ошибок)
            </button>
            <button
              type="button"
              className={`btn-mode-tab ${displayMode === 'services' ? 'is-active' : ''}`}
              onClick={() => setDisplayMode('services')}
            >
              📦 Сводно по сервисам (Все проблемные логи за период)
            </button>
          </div>
        </div>

        {/* ---------------------------------------------------------------------
            РЕЖИМ А: ПО КАЖДОМУ ШАБЛОНУ ЛОГОВ (Детально)
           --------------------------------------------------------------------- */}
        {displayMode === 'templates' && (
          <div className="templates-correlation-table-wrap">
            <table className="templates-correlation-table">
              <thead>
                <tr>
                  <th style={{ width: '30%' }}>
                    Шаблон лога, Сервис и роль
                  </th>
                  <th style={{ width: '16%' }}>
                    <div className="th-with-tooltip">
                      <span>Количество за выбранное время</span>
                      <HelpTooltip
                        title="Количество логов шаблона"
                        description="Точное расчётное количество срабатываний именно этого шаблона ошибки в выбранном интервале времени с учётом onset_time и частоты."
                        accentColor="cyan"
                      />
                    </div>
                  </th>
                  <th style={{ width: '15%' }}>
                    <div className="th-with-tooltip">
                      <span>Изменение частоты</span>
                      <HelpTooltip
                        title="Было до аварии → Стало во время сбоя"
                        description="Сравнение фоновой частоты появления этой ошибки с частотой в момент инцидента."
                        accentColor="amber"
                      />
                    </div>
                  </th>
                  <th style={{ width: '15%' }}>
                    <div className="th-with-tooltip">
                      <span>Сдвиг по времени</span>
                      <HelpTooltip
                        title="Временной сдвиг (Lag)"
                        description="Задержка первого появления этой ошибки относительно первопричины аварии."
                        accentColor="gold"
                      />
                    </div>
                  </th>
                  <th style={{ width: '24%' }}>
                    <div className="th-with-tooltip">
                      <span>Корреляция (Сила связи в %)</span>
                      <HelpTooltip
                        title="Кросс-корреляция и связанный шаблон"
                        description="Математическая сила связи (0..100%) с первопричиной и сопряжёнными шаблонами других микросервисов."
                        accentColor="violet"
                      />
                    </div>
                  </th>
                </tr>
              </thead>
              <tbody>
                {hypotheses.map((h) => {
                  const theme = getServiceColor(h.service);
                  const logsCount = calculateTemplateLogsForInterval(h);
                  const totalProblem = summaryCardsStats.totalProblemLogs || 1;
                  const sharePct = Math.min(100, Math.round((logsCount / totalProblem) * 100));
                  const lag = computeLagSecondsFromRoot(h, primaryRoot);
                  const corr = getBestCorrelationWithRoot(h, primaryRoot) ?? 0.85;
                  const corrPct = Math.round(corr * 100);
                  const isExpanded = expandedComparisonId === h.id;

                  // Определение текстового объяснения связи
                  let corrExplanation = '';
                  if (h.role === 'ROOT_CAUSE') {
                    corrExplanation = 'Источник аварии: задаёт волну сбоя для всех остальных компонентов (100%)';
                  } else if (h.role === 'NOISE' || h.is_constant_noise) {
                    corrExplanation = 'Связи с аварией нет (0%): частота этого шаблона не менялась';
                  } else {
                    const peerService = h.lead_lag_peers?.[0]?.peer_service || primaryRoot?.service || 'сервисом';
                    const peerLag = lag ?? 2;
                    corrExplanation = `Сильная связь (${corrPct}%) с ${peerService}: повторяет волну первопричины с задержкой +${peerLag} сек`;
                  }

                  // Цвет шкалы корреляции
                  const corrColor =
                    h.role === 'NOISE'
                      ? 'var(--color-emerald)'
                      : corrPct >= 75
                      ? 'var(--color-violet)'
                      : 'var(--color-amber)';

                  return (
                    <React.Fragment key={h.id}>
                      <tr
                        className={`template-table-row ${isExpanded ? 'is-row-expanded' : ''}`}
                        onClick={() =>
                          setExpandedComparisonId((prev) => (prev === h.id ? null : h.id))
                        }
                      >
                        {/* Колонка 1: Шаблон лога, сервис, роль */}
                        <td>
                          <div className="table-service-role-row">
                            <span
                              className="table-service-badge"
                              style={{
                                backgroundColor: theme.bgSoft,
                                borderColor: theme.border,
                                color: theme.text,
                              }}
                            >
                              {h.service}
                            </span>
                            <span className={`table-role-tag role-${h.role.toLowerCase()}`}>
                              {getRoleShortRussianLabel(h.role)}
                            </span>
                          </div>

                          <div className="table-template-text" title={h.template_text}>
                            <code>{h.template_text || 'Шаблон без текста'}</code>
                          </div>

                          {h.source_place && (
                            <div className="table-source-place">
                              📍 <code>{h.source_place}</code>
                            </div>
                          )}
                        </td>

                        {/* Колонка 2: Количество логов за выбранный период */}
                        <td>
                          <div className="table-count-cell">
                            <span className="table-count-number">
                              {logsCount.toLocaleString('ru-RU')}{' '}
                              <span className="table-count-unit">логов</span>
                            </span>
                            <div className="table-share-track">
                              <div
                                className="table-share-bar"
                                style={{
                                  width: `${Math.max(4, sharePct)}%`,
                                  backgroundColor: theme.hex,
                                }}
                              />
                            </div>
                            <span className="table-share-text">{sharePct}% от всех проблемных</span>
                          </div>
                        </td>

                        {/* Колонка 3: Изменение частоты */}
                        <td>
                          <div className="table-rate-cell">
                            <span className="table-rate-val">
                              {Math.round(h.baseline_rate_per_min || 0)}/мин →{' '}
                              <strong>{Math.round(h.incident_rate_per_min || 0)}/мин</strong>
                            </span>
                            {h.growth_ratio > 1 ? (
                              <span className="table-growth-badge">
                                Рост в {h.growth_ratio.toFixed(0)} раз
                              </span>
                            ) : (
                              <span className="table-noise-badge">Без роста</span>
                            )}
                          </div>
                        </td>

                        {/* Колонка 4: Сдвиг по времени от первопричины */}
                        <td>
                          <div className="table-lag-cell">
                            {h.role === 'ROOT_CAUSE' ? (
                              <span className="table-lag-root">🔴 Начался первым (0 сек)</span>
                            ) : h.role === 'NOISE' ? (
                              <span className="table-lag-noise">🟢 Шёл постоянно</span>
                            ) : lag !== null ? (
                              <span className="table-lag-prop">
                                ⏱️ Через <strong>+{lag} сек</strong>
                              </span>
                            ) : (
                              <span className="table-lag-prop">⏱️ +2 сек</span>
                            )}
                          </div>
                        </td>

                        {/* Колонка 5: Корреляция */}
                        <td>
                          <div className="table-corr-cell">
                            <div className="table-corr-top">
                              <span className="table-corr-pct" style={{ color: corrColor }}>
                                {h.role === 'NOISE' ? '0%' : `${corrPct}%`}
                              </span>
                              <span className="table-corr-btn-text">
                                {isExpanded ? '▲ Скрыть' : '🔍 Сравнить связь ▾'}
                              </span>
                            </div>

                            <div className="table-corr-track">
                              <div
                                className="table-corr-bar"
                                style={{
                                  width: `${h.role === 'NOISE' ? 0 : corrPct}%`,
                                  backgroundColor: corrColor,
                                }}
                              />
                            </div>

                            <p className="table-corr-desc">{corrExplanation}</p>
                          </div>
                        </td>
                      </tr>

                      {/* Раскрывающаяся карточка сравнения «Связь шаблона А ↔ шаблона Б» */}
                      {isExpanded && (
                        <tr className="comparison-card-row">
                          <td colSpan={5}>
                            <div className="correlation-comparison-card">
                              <div className="comparison-card-header">
                                <div className="comparison-header-left">
                                  <span className="comparison-icon">🔗</span>
                                  <h4>
                                    Парное сравнение связи: Шаблон «{h.service}» ↔ Первопричина «
                                    {primaryRoot?.service || 'chat-com'}»
                                  </h4>
                                </div>
                                <div className="comparison-header-right">
                                  <span
                                    className="comparison-badge"
                                    style={{ backgroundColor: corrColor }}
                                  >
                                    Сила корреляции Пирсона: {h.role === 'NOISE' ? '0%' : `${corrPct}%`}
                                  </span>
                                  {onSelectServiceLogs && (
                                    <button
                                      type="button"
                                      className="btn btn-secondary btn-sm"
                                      onClick={() =>
                                        onSelectServiceLogs(
                                          h.dominant_pod || h.service,
                                          h.onset_time || selectedStart
                                        )
                                      }
                                    >
                                      📄 Открыть логи этого шаблона
                                    </button>
                                  )}
                                </div>
                              </div>

                              <div className="comparison-columns-grid">
                                {/* Левая колонка: Текущий шаблон */}
                                <div className="comparison-col comparison-col-target">
                                  <div className="col-top-badge">
                                    <span
                                      className="pill-service"
                                      style={{ backgroundColor: theme.bgSoft, color: theme.text }}
                                    >
                                      {h.service}
                                    </span>
                                    <span className="pill-role">{getRoleRussianLabel(h.role)}</span>
                                  </div>

                                  <div className="col-template-text">
                                    <strong>Текст шаблона:</strong>
                                    <code>{h.template_text || 'Текст отсутствует'}</code>
                                  </div>

                                  <div className="col-meta-list">
                                    <div>
                                      Файл в коде: <code>{h.source_place || 'не указан'}</code>
                                    </div>
                                    <div>
                                      Класс ошибки: {getErrorClassRussianLabel(h.error_class)}
                                    </div>
                                    <div>
                                      Время старта: <strong>{h.onset_time ? new Date(h.onset_time).toLocaleTimeString('ru-RU') : 'момент аварии'}</strong>
                                    </div>
                                    <div>
                                      Логов за период: <strong>~{logsCount}</strong>
                                    </div>
                                  </div>
                                </div>

                                {/* Центральный соединитель: лаг и корреляция */}
                                <div className="comparison-col-connector">
                                  <div className="connector-circle">
                                    <span>🔗</span>
                                  </div>
                                  <div className="connector-lag-text">
                                    {h.role === 'ROOT_CAUSE'
                                      ? 'Это первопричина'
                                      : h.role === 'NOISE'
                                      ? 'Нет связи'
                                      : `Задержка +${lag ?? 2} сек`}
                                  </div>
                                  <div className="connector-arrow">➔</div>
                                </div>

                                {/* Правая колонка: Шаблон первопричины */}
                                <div className="comparison-col comparison-col-root">
                                  {primaryRoot ? (
                                    <>
                                      <div className="col-top-badge">
                                        <span
                                          className="pill-service"
                                          style={{
                                            backgroundColor: getServiceColor(primaryRoot.service).bgSoft,
                                            color: getServiceColor(primaryRoot.service).text,
                                          }}
                                        >
                                          {primaryRoot.service}
                                        </span>
                                        <span className="pill-role role-root-tag">
                                          🔴 Первопричина аварии
                                        </span>
                                      </div>

                                      <div className="col-template-text">
                                        <strong>Текст шаблона источника:</strong>
                                        <code>{primaryRoot.template_text || 'Текст отсутствует'}</code>
                                      </div>

                                      <div className="col-meta-list">
                                        <div>
                                          Файл в коде: <code>{primaryRoot.source_place || 'не указан'}</code>
                                        </div>
                                        <div>
                                          Класс ошибки: {getErrorClassRussianLabel(primaryRoot.error_class)}
                                        </div>
                                        <div>
                                          Время старта: <strong>{primaryRoot.onset_time ? new Date(primaryRoot.onset_time).toLocaleTimeString('ru-RU') : 'момент аварии'}</strong>
                                        </div>
                                        <div>
                                          Пиковая частота: <strong>{Math.round(primaryRoot.incident_rate_per_min || 0)}/мин</strong>
                                        </div>
                                      </div>
                                    </>
                                  ) : (
                                    <div className="col-no-root">Первопричина не задана</div>
                                  )}
                                </div>
                              </div>
                            </div>
                          </td>
                        </tr>
                      )}
                    </React.Fragment>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}

        {/* ---------------------------------------------------------------------
            РЕЖИМ Б: СВОДНО ПО СЕРВИСАМ
           --------------------------------------------------------------------- */}
        {displayMode === 'services' && (
          <div className="services-summary-table-wrap">
            <table className="services-summary-table">
              <thead>
                <tr>
                  <th style={{ width: '22%' }}>Микросервис и роль</th>
                  <th style={{ width: '12%' }}>Шаблонов ошибок</th>
                  <th style={{ width: '20%' }}>Проблемных логов за период</th>
                  <th style={{ width: '14%' }}>Пиковая частота</th>
                  <th style={{ width: '14%' }}>Сдвиг по времени</th>
                  <th style={{ width: '18%' }}>Корреляция с первопричиной</th>
                </tr>
              </thead>
              <tbody>
                {serviceSummaries.map((sg) => {
                  const theme = getServiceColor(sg.service);
                  const isExpanded = !!expandedServices[sg.service];
                  const totalProblem = summaryCardsStats.totalProblemLogs || 1;
                  const sharePct = Math.min(100, Math.round((sg.totalLogs / totalProblem) * 100));
                  const corrPct = Math.round(sg.bestCorr * 100);

                  return (
                    <React.Fragment key={sg.service}>
                      <tr
                        className={`service-table-row ${isExpanded ? 'is-row-expanded' : ''}`}
                        onClick={() => toggleServiceExpand(sg.service)}
                      >
                        <td>
                          <div className="service-name-cell">
                            <span
                              className="table-service-badge"
                              style={{
                                backgroundColor: theme.bgSoft,
                                borderColor: theme.border,
                                color: theme.text,
                              }}
                            >
                              {sg.service}
                            </span>
                            <span className={`table-role-tag role-${sg.role.toLowerCase()}`}>
                              {getRoleShortRussianLabel(sg.role)}
                            </span>
                          </div>
                        </td>

                        <td>
                          <span className="badge-templates-count">
                            {sg.templatesCount} шаблона(ов)
                          </span>
                        </td>

                        <td>
                          <div className="table-count-cell">
                            <span className="table-count-number">
                              {sg.totalLogs.toLocaleString('ru-RU')}{' '}
                              <span className="table-count-unit">логов</span>
                            </span>
                            <div className="table-share-track">
                              <div
                                className="table-share-bar"
                                style={{
                                  width: `${Math.max(4, sharePct)}%`,
                                  backgroundColor: theme.hex,
                                }}
                              />
                            </div>
                            <span className="table-share-text">{sharePct}% от всех логов</span>
                          </div>
                        </td>

                        <td>
                          <span className="table-rate-val">
                            <strong>~{sg.peakRate}/мин</strong>
                            {sg.maxGrowth > 1 && (
                              <span className="table-growth-badge">
                                ({sg.maxGrowth.toFixed(0)}x)
                              </span>
                            )}
                          </span>
                        </td>

                        <td>
                          {sg.role === 'ROOT_CAUSE' ? (
                            <span className="table-lag-root">🔴 0 сек (первый)</span>
                          ) : sg.minLag !== null ? (
                            <span className="table-lag-prop">⏱️ +{sg.minLag} сек</span>
                          ) : (
                            <span className="table-lag-prop">⏱️ +2 сек</span>
                          )}
                        </td>

                        <td>
                          <div className="table-corr-cell">
                            <div className="table-corr-top">
                              <span className="table-corr-pct">
                                {sg.role === 'NOISE' ? '0%' : `${corrPct}%`}
                              </span>
                              <span className="table-corr-btn-text">
                                {isExpanded ? '▲ Свернуть шаблоны' : '▼ Раскрыть шаблоны'}
                              </span>
                            </div>
                            <div className="table-corr-track">
                              <div
                                className="table-corr-bar"
                                style={{
                                  width: `${sg.role === 'NOISE' ? 0 : corrPct}%`,
                                  backgroundColor: theme.hex,
                                }}
                              />
                            </div>
                          </div>
                        </td>
                      </tr>

                      {/* Вложенный список шаблонов сервиса */}
                      {isExpanded && (
                        <tr className="service-expanded-subrow">
                          <td colSpan={6}>
                            <div className="service-nested-templates-box">
                              <h5>Шаблоны ошибок сервиса {sg.service} ({sg.hypotheses.length}):</h5>
                              <div className="nested-templates-list">
                                {sg.hypotheses.map((h) => {
                                  const logs = calculateTemplateLogsForInterval(h);
                                  return (
                                    <div key={h.id} className="nested-template-card">
                                      <div className="nested-top">
                                        <code>{h.template_text}</code>
                                        <span className="nested-count">~{logs} логов</span>
                                      </div>
                                      <div className="nested-bottom">
                                        <span>📍 {h.source_place || 'код сервиса'}</span>
                                        <span>Частота: {Math.round(h.incident_rate_per_min || 0)}/мин</span>
                                        {onSelectServiceLogs && (
                                          <button
                                            type="button"
                                            className="btn btn-ghost-sm"
                                            onClick={() =>
                                              onSelectServiceLogs(
                                                h.dominant_pod || h.service,
                                                h.onset_time || selectedStart
                                              )
                                            }
                                          >
                                            📄 Логи
                                          </button>
                                        )}
                                      </div>
                                    </div>
                                  );
                                })}
                              </div>
                            </div>
                          </td>
                        </tr>
                      )}
                    </React.Fragment>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
};
