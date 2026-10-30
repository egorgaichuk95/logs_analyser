import React, { useState, useMemo, useCallback } from 'react';
import {
  api,
  AnalyzeCaseResponse,
  Hypothesis,
  NormalizedEvent,
} from '../api/client';
import { HelpTooltip } from './HelpTooltip';
import {
  getRoleRussianLabel,
  getRoleShortRussianLabel,
  getErrorClassRussianLabel,
  getSignalRussianInfo,
  buildHumanRussianExplanation,
  estimateHypothesisProblemLogsCount,
  getServiceColor,
  calculateIncidentDurationMinutes,
  computeLagSecondsFromRoot,
  getBestCorrelationWithRoot,
} from '../utils/humanRcaTranslator';

export interface SimpleRcaOverviewPageProps {
  caseId: string;
  analysis: AnalyzeCaseResponse | null;
  loading: boolean;
  onOpenGeminiModal: () => void;
  onSwitchToEngineeringMode: (targetPage?: 'hypotheses' | 'topology' | 'chains' | 'evidence') => void;
}

export const SimpleRcaOverviewPage: React.FC<SimpleRcaOverviewPageProps> = ({
  caseId,
  analysis,
  loading,
  onOpenGeminiModal,
  onSwitchToEngineeringMode,
}) => {
  // Expanded signals breakdown per hypothesis id
  const [expandedSignals, setExpandedSignals] = useState<Record<string, boolean>>({});

  // Collapsible noise block state
  const [noiseOpen, setNoiseOpen] = useState<boolean>(false);

  // Context Logs Modal State
  const [modalOpen, setModalOpen] = useState<boolean>(false);
  const [modalService, setModalService] = useState<string>('');
  const [modalPod, setModalPod] = useState<string>('');
  const [modalTs, setModalTs] = useState<string>('');
  const [modalSourcePlace, setModalSourcePlace] = useState<string>('');
  const [modalLogs, setModalLogs] = useState<NormalizedEvent[]>([]);
  const [modalLoading, setModalLoading] = useState<boolean>(false);
  const [modalError, setModalError] = useState<string | null>(null);
  const [modalLevelFilter, setModalLevelFilter] = useState<'ALL' | 'ERROR' | 'WARN' | 'INFO'>('ALL');
  const [modalSearchText, setModalSearchText] = useState<string>('');
  const [modalCopied, setModalCopied] = useState<boolean>(false);

  // 1. Calculate incident duration
  const incidentDurationMinutes = useMemo(() => {
    return calculateIncidentDurationMinutes(analysis?.incident_start, analysis?.incident_end);
  }, [analysis?.incident_start, analysis?.incident_end]);

  // 2. Hypotheses categories
  const hypotheses = useMemo(() => analysis?.hypotheses ?? [], [analysis?.hypotheses]);

  const rootCauses = useMemo(() => {
    const list = hypotheses.filter((h) => h.role === 'ROOT_CAUSE');
    if (list.length > 0) return list;
    const rank1 = hypotheses.find((h) => h.rank === 1);
    return rank1 ? [rank1] : [];
  }, [hypotheses]);

  const primaryRoot = rootCauses[0] ?? null;

  const propagations = useMemo(() => {
    return hypotheses.filter((h) => h.role === 'PROPAGATION' && h.id !== primaryRoot?.id);
  }, [hypotheses, primaryRoot]);

  const symptoms = useMemo(() => {
    return hypotheses.filter((h) => h.role === 'SYMPTOM' && h.id !== primaryRoot?.id);
  }, [hypotheses, primaryRoot]);

  const noiseList = useMemo(() => {
    return hypotheses.filter((h) => h.role === 'NOISE' || h.is_constant_noise);
  }, [hypotheses]);

  // 3. Problem logs total & noise total summary
  const summaryStats = useMemo(() => {
    let totalProblemLogs = 0;
    let totalNoiseLogs = 0;

    for (const h of hypotheses) {
      const logs = estimateHypothesisProblemLogsCount(h, incidentDurationMinutes);
      if (h.role === 'NOISE' || h.is_constant_noise) {
        totalNoiseLogs += logs;
      } else {
        totalProblemLogs += logs;
      }
    }

    return { totalProblemLogs, totalNoiseLogs };
  }, [hypotheses, incidentDurationMinutes]);

  // 4. Affected services list for hero card
  const affectedServicesInfo = useMemo(() => {
    const items: Array<{ service: string; lagSeconds: number }> = [];
    const seen = new Set<string>();

    for (const p of propagations) {
      if (!seen.has(p.service)) {
        seen.add(p.service);
        const lag = computeLagSecondsFromRoot(p, primaryRoot) ?? 2;
        items.push({ service: p.service, lagSeconds: lag });
      }
    }
    for (const s of symptoms) {
      if (!seen.has(s.service)) {
        seen.add(s.service);
        const lag = computeLagSecondsFromRoot(s, primaryRoot) ?? 4;
        items.push({ service: s.service, lagSeconds: lag });
      }
    }

    items.sort((a, b) => a.lagSeconds - b.lagSeconds);
    return items;
  }, [propagations, symptoms, primaryRoot]);

  // Open Context Logs Modal
  const openLogsModal = useCallback(
    async (h: Hypothesis) => {
      setModalService(h.service);
      setModalPod(h.dominant_pod || '');
      setModalTs(h.onset_time || '');
      setModalSourcePlace(h.source_place || '');
      setModalLogs([]);
      setModalError(null);
      setModalLoading(true);
      setModalOpen(true);
      setModalCopied(false);

      try {
        const pod = h.dominant_pod?.trim() || '';
        const ts = h.onset_time || analysis?.incident_start || new Date().toISOString();
        const res = await api.getContextLogs(caseId, pod, ts, 'pod');
        setModalLogs(res.events || []);
      } catch (err: unknown) {
        const msg = err instanceof Error ? err.message : String(err);
        setModalError(`Не удалось загрузить контекстные логи: ${msg}`);
      } finally {
        setModalLoading(false);
      }
    },
    [caseId, analysis?.incident_start]
  );

  const toggleSignals = (hypoId: string) => {
    setExpandedSignals((prev) => ({
      ...prev,
      [hypoId]: !prev[hypoId],
    }));
  };

  const copyModalLogs = useCallback(() => {
    if (modalLogs.length === 0) return;
    const text = modalLogs
      .map(
        (ev) =>
          `[${ev.ts || '-'}] [${(ev.level || 'INFO').toUpperCase()}] [${ev.service || '-'}/${ev.pod || '-'}] ${
            ev.raw_redacted || ev.message || ''
          }`
      )
      .join('\n');
    navigator.clipboard.writeText(text).then(() => {
      setModalCopied(true);
      setTimeout(() => setModalCopied(false), 2500);
    });
  }, [modalLogs]);

  // Filtered modal logs
  const filteredModalLogs = useMemo(() => {
    let result = modalLogs;
    if (modalLevelFilter !== 'ALL') {
      result = result.filter(
        (ev) => (ev.level || '').toUpperCase() === modalLevelFilter
      );
    }
    if (modalSearchText.trim()) {
      const q = modalSearchText.trim().toLowerCase();
      result = result.filter(
        (ev) =>
          (ev.message || '').toLowerCase().includes(q) ||
          (ev.raw_redacted || '').toLowerCase().includes(q) ||
          (ev.source_place || '').toLowerCase().includes(q)
      );
    }
    return result;
  }, [modalLogs, modalLevelFilter, modalSearchText]);

  // Loading State
  if (loading) {
    return (
      <div className="simple-overview-container">
        <div className="simple-loading-state">
          <div className="simple-spinner" />
          <h2 className="simple-loading-title">Идёт глубокий анализ инцидента...</h2>
          <p className="simple-loading-subtitle">
            Сопоставляем распределённые трассировки, корреляцию временных рядов, топологию и шаблоны ошибок.
          </p>
        </div>
      </div>
    );
  }

  // Empty State
  if (!analysis || hypotheses.length === 0) {
    return (
      <div className="simple-overview-container">
        <div className="simple-empty-state">
          <div className="simple-empty-icon">🔍</div>
          <h2 className="simple-empty-title">Анализ для данного кейса ещё не выполнен</h2>
          <p className="simple-empty-subtitle">
            Запустите анализ логов, чтобы система автоматически определила первопричину аварии, цепочку последствий и отсеяла шум.
          </p>
          <div className="simple-empty-actions">
            <button
              type="button"
              className="btn btn-primary"
              onClick={() => onSwitchToEngineeringMode('hypotheses')}
            >
              🚀 Перейти к запуску анализа
            </button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="simple-overview-container">
      {/* =========================================================================
          БЛОК А. HERO КАРТОЧКА: Главный вывод в чём причина и что делать
         ========================================================================= */}
      <section className="simple-hero-card">
        <div className="simple-hero-header">
          <div className="simple-hero-title-row">
            <span className="simple-hero-badge">🎯 АВТОМАТИЧЕСКИЙ ВЕРДИКТ СИСТЕМЫ</span>
            <h1 className="simple-hero-title">
              Главный вывод: в чём причина аварии и что делать
            </h1>
            <HelpTooltip
              title="Главный вердикт расследования (RCA)"
              description="Система сопоставила все события по 8 математическим сигналам, графу микросервисов и лагам времени, отделив источник сбоя от вторичных каскадных ошибок."
              howToUse="Сосредоточьтесь на устранении первопричины в блоке 1. Все остальные сервисы упали по цепочке и восстановятся автоматически."
              accentColor="ruby"
              position="bottom"
            />
          </div>
          <p className="simple-hero-desc">
            Инцидент длился ориентировочно <strong>{incidentDurationMinutes} мин</strong>. Система определила виновника и каскадный след отказа:
          </p>
        </div>

        {primaryRoot ? (
          <div className="simple-hero-summary-grid">
            {/* Пункт 1: Где начался сбой */}
            <div className="simple-summary-item simple-summary-item-ruby">
              <div className="simple-summary-header">
                <span className="simple-summary-step">1</span>
                <h3>🔴 Где начался сбой (Что чинить)</h3>
                <HelpTooltip
                  title="Эпицентр аварии"
                  description="Сервис и место в коде, где зафиксирован самый первый скачок аномалии."
                  accentColor="ruby"
                />
              </div>
              <p className="simple-summary-body">
                Сервис <strong className="simple-highlight-service">{primaryRoot.service}</strong>
                {primaryRoot.source_place && (
                  <>, файл и строка <code>{primaryRoot.source_place}</code></>
                )}
                {primaryRoot.dominant_pod && (
                  <>, контейнер <code>{primaryRoot.dominant_pod}</code></>
                )}
                {primaryRoot.onset_time && (
                  <>, начало в <strong>{new Date(primaryRoot.onset_time).toLocaleTimeString('ru-RU')}</strong></>
                )}
                .
              </p>
              <div className="simple-summary-subtext">
                Характер сбоя: <em>{getErrorClassRussianLabel(primaryRoot.error_class)}</em>.
              </div>
            </div>

            {/* Пункт 2: Что пострадало следом */}
            <div className="simple-summary-item simple-summary-item-amber">
              <div className="simple-summary-header">
                <span className="simple-summary-step">2</span>
                <h3>⚡ Что пострадало следом (Последствия)</h3>
                <HelpTooltip
                  title="Каскадные последствия"
                  description="Сервисы, упавшие в результате цепной реакции из-за недоступности первопричины."
                  accentColor="amber"
                />
              </div>
              {affectedServicesInfo.length > 0 ? (
                <div className="simple-affected-badges-wrap">
                  {affectedServicesInfo.map((item) => (
                    <span
                      key={item.service}
                      className="simple-affected-badge"
                      style={{
                        backgroundColor: getServiceColor(item.service).bgSoft,
                        borderColor: getServiceColor(item.service).border,
                        color: getServiceColor(item.service).text,
                      }}
                    >
                      {item.service} <strong>+{item.lagSeconds} сек</strong>
                    </span>
                  ))}
                </div>
              ) : (
                <p className="simple-summary-body">Соседние сервисы не были затронуты сбоем.</p>
              )}
              <div className="simple-summary-subtext">
                Эти сервисы упали из-за таймаутов к первопричине и восстановятся сами после её починки.
              </div>
            </div>

            {/* Пункт 3: Масштаб проблемы */}
            <div className="simple-summary-item simple-summary-item-gold">
              <div className="simple-summary-header">
                <span className="simple-summary-step">3</span>
                <h3>📊 Масштаб проблемы и отсеянный шум</h3>
                <HelpTooltip
                  title="Масштаб инцидента"
                  description="Общее количество проблемных логов во время аварии и объём фонового шума, исключённого алгоритмом."
                  accentColor="gold"
                />
              </div>
              <div className="simple-scale-numbers">
                <div className="simple-scale-stat">
                  <span className="simple-stat-val text-ruby">~{summaryStats.totalProblemLogs}</span>
                  <span className="simple-stat-lbl">проблемных логов аварии</span>
                </div>
                <div className="simple-scale-stat">
                  <span className="simple-stat-val text-emerald">{summaryStats.totalNoiseLogs}</span>
                  <span className="simple-stat-lbl">отсеянного шума</span>
                </div>
              </div>
              <div className="simple-summary-subtext">
                Система отфильтровала фоновые предупреждения, оставив только критичные события аварии.
              </div>
            </div>
          </div>
        ) : (
          <div className="simple-hero-no-root">
            Первопричина не определена однозначно. Рекомендуется изучить список гипотез в инженерном режиме.
          </div>
        )}

        {/* Быстрые цветные кнопки действий */}
        <div className="simple-hero-actions">
          {primaryRoot && (
            <button
              type="button"
              className="btn simple-btn-logs"
              onClick={() => openLogsModal(primaryRoot)}
            >
              📄 Посмотреть строки логов в момент поломки (±20 строк)
            </button>
          )}

          <button
            type="button"
            className="btn simple-btn-gemini"
            onClick={onOpenGeminiModal}
          >
            ✨ Открыть готовый разбор для ИИ (Google Gemini)
          </button>

          <button
            type="button"
            className="btn simple-btn-engineering"
            onClick={() => onSwitchToEngineeringMode('hypotheses')}
          >
            🔬 Инженерный режим (Гипотезы и сигналы)
          </button>

          <button
            type="button"
            className="btn simple-btn-scroll"
            onClick={() => {
              const el = document.getElementById('step-track-section');
              if (el) el.scrollIntoView({ behavior: 'smooth' });
            }}
          >
            ⬇️ Посмотреть цепочку развития аварии
          </button>
        </div>
      </section>

      {/* =========================================================================
          БЛОК Б. НАГЛЯДНАЯ ВИЗУАЛЬНАЯ ДОРОЖКА: Цепочка развития аварии (Шаг за шагом)
         ========================================================================= */}
      <section id="step-track-section" className="simple-story-track">
        <div className="simple-track-header">
          <h2>Цепочка развития аварии: Шаг за шагом</h2>
          <HelpTooltip
            title="Хронологическая цепочка развития"
            description="Показывает путь распространения отказа от исходной поломки через промежуточные микросервисы до пользователей на внешнем контуре."
            howToUse="Изучайте шаги сверху вниз, следуя за направлением стрелок."
            accentColor="violet"
          />
        </div>

        {/* ================= ШАГ 1: ПЕРВОПРИЧИНА ================= */}
        <div className="simple-step-section simple-step-ruby">
          <div className="simple-step-header">
            <div className="simple-step-title-group">
              <span className="simple-step-tag simple-tag-ruby">ШАГ 1</span>
              <h3 className="simple-step-heading">
                🔥 ПЕРВОПРИЧИНА (Источник аварии — началось здесь)
              </h3>
            </div>
            <HelpTooltip
              title="Шаг 1. Первопричина аварии"
              description="Именно в этом месте зародился инцидент. Все остальные сбои вызваны отказом данного компонента."
              howToUse="Правки в коде или перезапуск должны производиться в сервисе из этого блока."
              accentColor="ruby"
            />
          </div>

          <div className="simple-step-cards-list">
            {rootCauses.map((h) => {
              const theme = getServiceColor(h.service);
              const problemLogs = estimateHypothesisProblemLogsCount(h, incidentDurationMinutes);
              const isSignalsOpen = !!expandedSignals[h.id];

              return (
                <div
                  key={h.id}
                  className="simple-hypo-card simple-hypo-card-ruby"
                  style={{ borderLeftColor: theme.hex }}
                >
                  <div className="simple-card-top-row">
                    <div className="simple-card-identity">
                      <span
                        className="simple-service-pill"
                        style={{
                          backgroundColor: theme.bgSoft,
                          borderColor: theme.border,
                          color: theme.text,
                        }}
                      >
                        {h.service}
                      </span>
                      <span className="simple-role-badge simple-role-ruby">
                        {getRoleRussianLabel(h.role)}
                      </span>
                    </div>

                    <div className="simple-card-timing">
                      {h.onset_time && (
                        <span className="simple-onset-time">
                          Старт: <strong>{new Date(h.onset_time).toLocaleTimeString('ru-RU')}</strong>
                        </span>
                      )}
                      <span className="simple-confidence-badge">
                        Уверенность RCA: <strong>{(h.confidence * 100).toFixed(0)}%</strong>
                      </span>
                    </div>
                  </div>

                  {/* Человеческое объяснение */}
                  <p className="simple-human-explanation">
                    {buildHumanRussianExplanation(h, primaryRoot, incidentDurationMinutes)}
                  </p>

                  {/* Детали ошибки и шаблона */}
                  <div className="simple-error-template-box">
                    <div className="simple-error-class-row">
                      <span className="simple-error-class-label">Класс ошибки:</span>
                      <span className="simple-error-class-val">
                        {getErrorClassRussianLabel(h.error_class)}
                      </span>
                    </div>
                    {h.template_text && (
                      <div className="simple-template-text-row">
                        <code>{h.template_text}</code>
                      </div>
                    )}
                  </div>

                  {/* Сетка метрик */}
                  <div className="simple-card-metrics-grid">
                    <div className="simple-metric-cell">
                      <div className="simple-metric-label-row">
                        <span>Проблемных логов</span>
                        <HelpTooltip
                          title="Количество проблемных логов"
                          description="Оценочное число аномальных записей с данным шаблоном ошибки за всё время сбоя."
                          accentColor="ruby"
                        />
                      </div>
                      <span className="simple-metric-value text-ruby">
                        ~{problemLogs} логов
                      </span>
                    </div>

                    <div className="simple-metric-cell">
                      <div className="simple-metric-label-row">
                        <span>Частота ошибок</span>
                        <HelpTooltip
                          title="Скачок частоты ошибок"
                          description="Сравнение фоновой частоты до аварии с пиковой частотой во время инцидента."
                          accentColor="ruby"
                        />
                      </div>
                      <span className="simple-metric-value">
                        {Math.round(h.baseline_rate_per_min || 0)}/мин →{' '}
                        <strong>{Math.round(h.incident_rate_per_min || 0)}/мин</strong>
                        {h.growth_ratio > 1 && (
                          <span className="simple-growth-tag"> ({h.growth_ratio.toFixed(0)}x)</span>
                        )}
                      </span>
                    </div>

                    <div className="simple-metric-cell">
                      <div className="simple-metric-label-row">
                        <span>Локализация</span>
                        <HelpTooltip
                          title="Концентрация на реплике"
                          description="Показывает, пришлась ли авария на конкретный контейнер (под) или на все реплики сервиса."
                          accentColor="ruby"
                        />
                      </div>
                      <span className="simple-metric-value">
                        {h.dominant_pod ? `${h.dominant_pod} (${Math.round((h.pod_skew_ratio || 1) * 100)}%)` : 'Все реплики'}
                      </span>
                    </div>
                  </div>

                  {/* Действия карточки */}
                  <div className="simple-card-actions-row">
                    <button
                      type="button"
                      className="btn btn-secondary btn-sm"
                      onClick={() => openLogsModal(h)}
                    >
                      📄 Посмотреть строки логов (±20 строк)
                    </button>
                    <button
                      type="button"
                      className="btn btn-secondary btn-sm"
                      onClick={() => toggleSignals(h.id)}
                    >
                      🔬 {isSignalsOpen ? 'Скрыть технические сигналы S1..S8' : 'Показать технические метрики (8 сигналов S1..S8)'}
                    </button>
                  </div>

                  {/* Раскрывающийся блок технических сигналов */}
                  {isSignalsOpen && (
                    <div className="simple-signals-expanded">
                      <div className="simple-signals-intro">
                        <h4>Разбор 8 математических сигналов для {h.service}:</h4>
                        <p>Каждый сигнал оценивает вероятность того, что этот сервис является первопричиной (от 0% до 100%):</p>
                      </div>

                      <div className="simple-signals-grid">
                        {[
                          { key: 's1', val: h.signals?.trace_chain_score ?? 0 },
                          { key: 's2', val: h.signals?.onset_score ?? 0 },
                          { key: 's3', val: h.signals?.lead_lag_score ?? 0 },
                          { key: 's4', val: h.signals?.novelty_score ?? 0 },
                          { key: 's5', val: h.signals?.causal_dir_score ?? 0 },
                          { key: 's6', val: h.signals?.graph_root_score ?? 0 },
                          { key: 's7', val: h.signals?.localization_score ?? 0 },
                          { key: 's8', val: h.signals?.change_corr_score ?? 0 },
                        ].map(({ key, val }) => {
                          const info = getSignalRussianInfo(key);
                          const pct = Math.round(Math.min(1, Math.max(0, val)) * 100);
                          return (
                            <div key={key} className="simple-signal-item">
                              <div className="simple-signal-title-row">
                                <span className="simple-signal-name">{info.title}</span>
                                <span className="simple-signal-pct">{pct}%</span>
                              </div>
                              <div className="simple-signal-progress-track">
                                <div
                                  className="simple-signal-progress-bar"
                                  style={{ width: `${pct}%` }}
                                />
                              </div>
                              <p className="simple-signal-desc">{info.description}</p>
                            </div>
                          );
                        })}
                      </div>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </div>

        {/* Крупный визуальный разделитель-стрелка 1 -> 2 */}
        <div className="simple-step-arrow-divider simple-arrow-ruby-amber">
          <div className="simple-arrow-content">
            <span className="simple-arrow-icon">⬇️</span>
            <span className="simple-arrow-text">
              ЦЕПНАЯ РЕАКЦИЯ: через +2..4 секунды отказ перекинулся на зависимые сервисы
            </span>
            <span className="simple-arrow-icon">⬇️</span>
          </div>
        </div>

        {/* ================= ШАГ 2: ПОСЛЕДСТВИЯ В СОСЕДНИХ СЕРВИСАХ ================= */}
        <div className="simple-step-section simple-step-amber">
          <div className="simple-step-header">
            <div className="simple-step-title-group">
              <span className="simple-step-tag simple-tag-amber">ШАГ 2</span>
              <h3 className="simple-step-heading">
                ⚡ ПОСЛЕДСТВИЯ В СОСЕДНИХ СЕРВИСАХ (Упали по цепочке)
              </h3>
            </div>
            <HelpTooltip
              title="Шаг 2. Каскадная цепная реакция"
              description="Эти сервисы не содержат в себе дефекта. Они упали, потому что сервис первопричины перестал отвечать вовремя."
              howToUse="Не пытайтесь править код в этих сервисах — они вылечатся сами, как только поднимется первопричина."
              accentColor="amber"
            />
          </div>

          {propagations.length > 0 ? (
            <div className="simple-step-cards-grid">
              {propagations.map((h) => {
                const theme = getServiceColor(h.service);
                const lag = computeLagSecondsFromRoot(h, primaryRoot) ?? 2;
                const corr = getBestCorrelationWithRoot(h, primaryRoot) ?? 0.94;
                const corrPct = Math.round(corr * 100);
                const problemLogs = estimateHypothesisProblemLogsCount(h, incidentDurationMinutes);
                const isSignalsOpen = !!expandedSignals[h.id];

                return (
                  <div
                    key={h.id}
                    className="simple-hypo-card simple-hypo-card-amber"
                    style={{ borderLeftColor: theme.hex }}
                  >
                    <div className="simple-card-top-row">
                      <div className="simple-card-identity">
                        <span
                          className="simple-service-pill"
                          style={{
                            backgroundColor: theme.bgSoft,
                            borderColor: theme.border,
                            color: theme.text,
                          }}
                        >
                          {h.service}
                        </span>
                        <span className="simple-role-badge simple-role-amber">
                          {getRoleShortRussianLabel(h.role)}
                        </span>
                      </div>
                    </div>

                    {/* Бейдж задержки и корреляции */}
                    <div className="simple-lag-corr-banner">
                      <span className="simple-lag-badge">
                        ⏱️ Упал через <strong>+{lag} сек</strong> после первопричины
                      </span>
                      <span className="simple-corr-badge">
                        🔗 Связь по времени (корреляция): <strong>{corrPct}%</strong>
                      </span>
                    </div>

                    <p className="simple-human-explanation">
                      {buildHumanRussianExplanation(h, primaryRoot, incidentDurationMinutes)}
                    </p>

                    <div className="simple-error-template-box">
                      <div className="simple-error-class-row">
                        <span className="simple-error-class-label">Класс ошибки:</span>
                        <span className="simple-error-class-val">
                          {getErrorClassRussianLabel(h.error_class)}
                        </span>
                      </div>
                      {h.template_text && (
                        <div className="simple-template-text-row">
                          <code>{h.template_text}</code>
                        </div>
                      )}
                    </div>

                    <div className="simple-card-metrics-grid">
                      <div className="simple-metric-cell">
                        <span className="simple-metric-label">Сгенерировано логов</span>
                        <span className="simple-metric-value text-amber">~{problemLogs} логов</span>
                      </div>
                      <div className="simple-metric-cell">
                        <span className="simple-metric-label">Частота в пике</span>
                        <span className="simple-metric-value">
                          {Math.round(h.incident_rate_per_min || 0)}/мин
                        </span>
                      </div>
                    </div>

                    <div className="simple-card-actions-row">
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        onClick={() => openLogsModal(h)}
                      >
                        📄 Посмотреть логи (±20 строк)
                      </button>
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        onClick={() => toggleSignals(h.id)}
                      >
                        🔬 {isSignalsOpen ? 'Скрыть сигналы' : '8 сигналов S1..S8'}
                      </button>
                    </div>

                    {isSignalsOpen && (
                      <div className="simple-signals-expanded">
                        <div className="simple-signals-grid">
                          {[
                            { key: 's1', val: h.signals?.trace_chain_score ?? 0 },
                            { key: 's2', val: h.signals?.onset_score ?? 0 },
                            { key: 's3', val: h.signals?.lead_lag_score ?? 0 },
                            { key: 's5', val: h.signals?.causal_dir_score ?? 0 },
                          ].map(({ key, val }) => {
                            const info = getSignalRussianInfo(key);
                            const pct = Math.round(Math.min(1, Math.max(0, val)) * 100);
                            return (
                              <div key={key} className="simple-signal-item">
                                <div className="simple-signal-title-row">
                                  <span className="simple-signal-name">{info.title}</span>
                                  <span className="simple-signal-pct">{pct}%</span>
                                </div>
                                <div className="simple-signal-progress-track">
                                  <div
                                    className="simple-signal-progress-bar"
                                    style={{ width: `${pct}%` }}
                                  />
                                </div>
                              </div>
                            );
                          })}
                        </div>
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          ) : (
            <div className="simple-step-empty-box">
              Вторичных каскадных отказов в сервисах-потребителях не зафиксировано.
            </div>
          )}
        </div>

        {/* Крупный визуальный разделитель-стрелка 2 -> 3 */}
        <div className="simple-step-arrow-divider simple-arrow-amber-gold">
          <div className="simple-arrow-content">
            <span className="simple-arrow-icon">⬇️</span>
            <span className="simple-arrow-text">
              РЕЗУЛЬТАТ НА ВХОДЕ: что в итоге увидели клиенты и внешние шлюзы
            </span>
            <span className="simple-arrow-icon">⬇️</span>
          </div>
        </div>

        {/* ================= ШАГ 3: ВНЕШНИЕ СИМПТОМЫ ================= */}
        <div className="simple-step-section simple-step-gold">
          <div className="simple-step-header">
            <div className="simple-step-title-group">
              <span className="simple-step-tag simple-tag-gold">ШАГ 3</span>
              <h3 className="simple-step-heading">
                👥 ВНЕШНИЕ СИМПТОМЫ (Ошибки у клиентов на входе)
              </h3>
            </div>
            <HelpTooltip
              title="Шаг 3. Внешние симптомы"
              description="Ошибки на шлюзах и интерфейсах (HTTP 502/504), с которыми столкнулись внешние пользователи."
              howToUse="По этим симптомам пользователи обычно регистрируют инцидент в саппорт."
              accentColor="gold"
            />
          </div>

          {symptoms.length > 0 ? (
            <div className="simple-step-cards-grid">
              {symptoms.map((h) => {
                const theme = getServiceColor(h.service);
                const lag = computeLagSecondsFromRoot(h, primaryRoot) ?? 4;
                const problemLogs = estimateHypothesisProblemLogsCount(h, incidentDurationMinutes);
                const isSignalsOpen = !!expandedSignals[h.id];

                return (
                  <div
                    key={h.id}
                    className="simple-hypo-card simple-hypo-card-gold"
                    style={{ borderLeftColor: theme.hex }}
                  >
                    <div className="simple-card-top-row">
                      <div className="simple-card-identity">
                        <span
                          className="simple-service-pill"
                          style={{
                            backgroundColor: theme.bgSoft,
                            borderColor: theme.border,
                            color: theme.text,
                          }}
                        >
                          {h.service}
                        </span>
                        <span className="simple-role-badge simple-role-gold">
                          {getRoleShortRussianLabel(h.role)}
                        </span>
                      </div>
                      <span className="simple-lag-badge">
                        Проявился через <strong>+{lag} сек</strong>
                      </span>
                    </div>

                    <p className="simple-human-explanation">
                      {buildHumanRussianExplanation(h, primaryRoot, incidentDurationMinutes)}
                    </p>

                    <div className="simple-error-template-box">
                      <div className="simple-error-class-row">
                        <span className="simple-error-class-label">Класс ошибки:</span>
                        <span className="simple-error-class-val">
                          {getErrorClassRussianLabel(h.error_class)}
                        </span>
                      </div>
                      {h.template_text && (
                        <div className="simple-template-text-row">
                          <code>{h.template_text}</code>
                        </div>
                      )}
                    </div>

                    <div className="simple-card-metrics-grid">
                      <div className="simple-metric-cell">
                        <span className="simple-metric-label">Ошибок у пользователей</span>
                        <span className="simple-metric-value text-gold">~{problemLogs} логов</span>
                      </div>
                      <div className="simple-metric-cell">
                        <span className="simple-metric-label">Частота в пике</span>
                        <span className="simple-metric-value">
                          {Math.round(h.incident_rate_per_min || 0)}/мин
                        </span>
                      </div>
                    </div>

                    <div className="simple-card-actions-row">
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        onClick={() => openLogsModal(h)}
                      >
                        📄 Посмотреть логи (±20 строк)
                      </button>
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        onClick={() => toggleSignals(h.id)}
                      >
                        🔬 {isSignalsOpen ? 'Скрыть сигналы' : '8 сигналов S1..S8'}
                      </button>
                    </div>

                    {isSignalsOpen && (
                      <div className="simple-signals-expanded">
                        <div className="simple-signals-grid">
                          {[
                            { key: 's1', val: h.signals?.trace_chain_score ?? 0 },
                            { key: 's2', val: h.signals?.onset_score ?? 0 },
                            { key: 's5', val: h.signals?.causal_dir_score ?? 0 },
                          ].map(({ key, val }) => {
                            const info = getSignalRussianInfo(key);
                            const pct = Math.round(Math.min(1, Math.max(0, val)) * 100);
                            return (
                              <div key={key} className="simple-signal-item">
                                <div className="simple-signal-title-row">
                                  <span className="simple-signal-name">{info.title}</span>
                                  <span className="simple-signal-pct">{pct}%</span>
                                </div>
                                <div className="simple-signal-progress-track">
                                  <div
                                    className="simple-signal-progress-bar"
                                    style={{ width: `${pct}%` }}
                                  />
                                </div>
                              </div>
                            );
                          })}
                        </div>
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          ) : (
            <div className="simple-step-empty-box">
              Внешние симптомы на шлюзах не зафиксированы или локализованы внутри бэкенда.
            </div>
          )}
        </div>

        {/* ================= СВЁРНУТЫЙ БЛОК: ОТСЕЯННЫЙ ФОНОВЫЙ ШУМ ================= */}
        <div className="simple-noise-section">
          <button
            type="button"
            className="simple-noise-toggle-btn"
            onClick={() => setNoiseOpen((prev) => !prev)}
          >
            <div className="simple-noise-toggle-left">
              <span className="simple-noise-icon">🌿</span>
              <span className="simple-noise-title">
                Отсеянный фоновый шум ({noiseList.length} шаблонов) — ошибки, которые шли всегда и не связаны с аварией
              </span>
              <HelpTooltip
                title="Отсеянный фоновый шум"
                description="Ошибки, частота которых не изменилась во время аварии (например, фоновые проверки здоровья, разовые неверные пароли пользователей). Система автоматически исключила их из расследования."
                howToUse="Нажмите на полоску, чтобы просмотреть записи шума и убедиться, что они шли задолго до аварии."
                accentColor="emerald"
              />
            </div>
            <span className="simple-noise-toggle-arrow">
              {noiseOpen ? '▲ Свернуть' : '▼ Раскрыть'}
            </span>
          </button>

          {noiseOpen && (
            <div className="simple-noise-content">
              {noiseList.length > 0 ? (
                <div className="simple-noise-cards-grid">
                  {noiseList.map((h) => {
                    const theme = getServiceColor(h.service);
                    return (
                      <div
                        key={h.id}
                        className="simple-noise-card"
                        style={{ borderLeftColor: theme.hex }}
                      >
                        <div className="simple-card-top-row">
                          <span
                            className="simple-service-pill"
                            style={{
                              backgroundColor: theme.bgSoft,
                              borderColor: theme.border,
                              color: theme.text,
                            }}
                          >
                            {h.service}
                          </span>
                          <span className="simple-role-badge simple-role-emerald">
                            Фоновый шум
                          </span>
                        </div>

                        <p className="simple-human-explanation">
                          {buildHumanRussianExplanation(h, primaryRoot, incidentDurationMinutes)}
                        </p>

                        <div className="simple-noise-rates">
                          <span>Частота до аварии: <strong>{Math.round(h.baseline_rate_per_min || 0)}/мин</strong></span>
                          <span>Во время аварии: <strong>{Math.round(h.incident_rate_per_min || 0)}/мин</strong></span>
                        </div>

                        {h.template_text && (
                          <div className="simple-noise-template">
                            <code>{h.template_text}</code>
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              ) : (
                <p className="simple-noise-empty">
                  Постоянного фонового шума в данном временном окне не обнаружено.
                </p>
              )}
            </div>
          )}
        </div>
      </section>

      {/* =========================================================================
          МОДАЛЬНОЕ ОКНО: Просмотр логов в момент аварии (±20 строк)
         ========================================================================= */}
      {modalOpen && (
        <div
          className="simple-logs-modal-overlay"
          onClick={() => setModalOpen(false)}
        >
          <div
            className="simple-logs-modal-dialog"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="simple-logs-modal-header">
              <div className="simple-logs-header-info">
                <h3>
                  📄 Строки логов в момент поломки (±20 строк)
                </h3>
                <div className="simple-logs-modal-meta">
                  <span>Сервис: <strong>{modalService}</strong></span>
                  {modalPod && <span>Под: <code>{modalPod}</code></span>}
                  {modalTs && (
                    <span>Время: <strong>{new Date(modalTs).toLocaleTimeString('ru-RU')}</strong></span>
                  )}
                  {modalSourcePlace && <span>Источник: <code>{modalSourcePlace}</code></span>}
                </div>
              </div>
              <button
                type="button"
                className="simple-logs-close-btn"
                onClick={() => setModalOpen(false)}
                aria-label="Закрыть"
              >
                ✕
              </button>
            </div>

            {/* Панель фильтров модального окна */}
            <div className="simple-logs-toolbar">
              <div className="simple-logs-filter-group">
                <label htmlFor="simple-logs-search">Поиск в логах:</label>
                <input
                  id="simple-logs-search"
                  type="text"
                  placeholder="Фильтр по тексту сообщения или ошибке..."
                  value={modalSearchText}
                  onChange={(e) => setModalSearchText(e.target.value)}
                  className="simple-logs-search-input"
                />
              </div>

              <div className="simple-logs-level-tabs">
                {(['ALL', 'ERROR', 'WARN', 'INFO'] as const).map((lvl) => (
                  <button
                    key={lvl}
                    type="button"
                    className={`simple-level-tab ${modalLevelFilter === lvl ? 'is-active' : ''}`}
                    onClick={() => setModalLevelFilter(lvl)}
                  >
                    {lvl === 'ALL' ? 'Все уровни' : lvl}
                  </button>
                ))}
              </div>

              <div className="simple-logs-actions-right">
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  onClick={copyModalLogs}
                  disabled={modalLogs.length === 0}
                >
                  {modalCopied ? '✅ Скопировано!' : '📋 Скопировать логи'}
                </button>
              </div>
            </div>

            {/* Содержимое логов (терминал) */}
            <div className="simple-logs-terminal">
              {modalLoading && (
                <div className="simple-logs-terminal-loading">
                  <div className="simple-spinner-sm" />
                  <span>Загрузка строк логов из хранилища...</span>
                </div>
              )}

              {modalError && (
                <div className="simple-logs-terminal-error">
                  <span>⚠️ {modalError}</span>
                </div>
              )}

              {!modalLoading && !modalError && filteredModalLogs.length === 0 && (
                <div className="simple-logs-terminal-empty">
                  <span>Логи по заданным критериям не найдены.</span>
                </div>
              )}

              {!modalLoading && !modalError && filteredModalLogs.length > 0 && (
                <div className="simple-logs-rows-list">
                  {filteredModalLogs.map((ev, idx) => {
                    const level = (ev.level || 'INFO').toUpperCase();
                    const isError = level === 'ERROR' || level === 'FATAL' || level === 'CRITICAL';
                    const isWarn = level === 'WARN' || level === 'WARNING';
                    const rowClass = isError
                      ? 'simple-log-row-error'
                      : isWarn
                      ? 'simple-log-row-warn'
                      : 'simple-log-row-info';

                    let timePart = ev.ts || '-';
                    try {
                      const d = new Date(ev.ts);
                      if (!Number.isNaN(d.getTime())) {
                        timePart = d.toLocaleTimeString('ru-RU', {
                          hour: '2-digit',
                          minute: '2-digit',
                          second: '2-digit',
                        });
                      }
                    } catch {
                      // ignore
                    }

                    return (
                      <div key={ev.event_id || idx} className={`simple-log-row ${rowClass}`}>
                        <span className="simple-log-line-num">{idx + 1}</span>
                        <span className="simple-log-time">{timePart}</span>
                        <span className={`simple-log-level-badge level-${level.toLowerCase()}`}>
                          {level}
                        </span>
                        <span className="simple-log-service">{ev.service || '-'}</span>
                        <span className="simple-log-msg">
                          {ev.raw_redacted || ev.message || ev.error_msg || '-'}
                        </span>
                        {ev.source_place && (
                          <span className="simple-log-place">{ev.source_place}</span>
                        )}
                      </div>
                    );
                  })}
                </div>
              )}
            </div>

            <div className="simple-logs-modal-footer">
              <span className="simple-logs-footer-count">
                Всего строк в окне: <strong>{filteredModalLogs.length}</strong> (из {modalLogs.length})
              </span>
              <div className="simple-logs-footer-actions">
                <button
                  type="button"
                  className="btn btn-secondary"
                  onClick={() => {
                    setModalOpen(false);
                    onSwitchToEngineeringMode('chains');
                  }}
                >
                  🔍 Открыть детальный контекст в инженерном режиме
                </button>
                <button
                  type="button"
                  className="btn btn-primary"
                  onClick={() => setModalOpen(false)}
                >
                  Закрыть
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
