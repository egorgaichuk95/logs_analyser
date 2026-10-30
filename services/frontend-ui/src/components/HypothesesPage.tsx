import React, { useState, useMemo, useCallback, useEffect } from 'react';
import {
  api,
  AnalyzeCaseResponse,
  type EvidenceItem,
  Hypothesis,
  ClockSkewFinding,
  NormalizedEvent,
  ContextLogsResponse,
} from '../api/client';

export interface HypothesesPageProps {
  caseId: string;
  analysis: AnalyzeCaseResponse | null;
  loading: boolean;
  onNavigateToTopology: () => void;
  onNavigateToChains: (initialKey?: string) => void;
  onNavigateToEvidence: (highlightEvidenceId?: string) => void;
}

// Math and number safety helpers
function safeNumber(v: unknown, fallback: number = 0): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : fallback;
}

function clamp(v: number, min: number = 0, max: number = 1): number {
  const safe = safeNumber(v, min);
  return Math.max(min, Math.min(max, safe));
}

function safePercent(v: unknown): string {
  const num = safeNumber(v, 0);
  return `${(clamp(num, 0, 1) * 100).toFixed(1)}%`;
}

type RoleFilter = 'ALL' | 'ROOT_CAUSE' | 'PROPAGATION' | 'SYMPTOM' | 'NOISE';
type SortOrder = 'RANK_ASC' | 'CONFIDENCE_DESC' | 'GROWTH_DESC' | 'ONSET_ASC';
type DrawerTab = 'signals' | 'context' | 'evidence';

export const HypothesesPage: React.FC<HypothesesPageProps> = ({
  caseId,
  analysis,
  loading,
  onNavigateToTopology,
  onNavigateToChains,
  onNavigateToEvidence,
}) => {
  // Filtering & Sorting
  const [roleFilter, setRoleFilter] = useState<RoleFilter>('ALL');
  const [searchQuery, setSearchQuery] = useState<string>('');
  const [sortOrder, setSortOrder] = useState<SortOrder>('RANK_ASC');

  // Clock Skew Modal
  const [showClockSkewModal, setShowClockSkewModal] = useState<boolean>(false);

  // Drawer state
  const [drawerOpen, setDrawerOpen] = useState<boolean>(false);
  const [selectedHypoId, setSelectedHypoId] = useState<string | null>(null);
  const [drawerActiveTab, setDrawerActiveTab] = useState<DrawerTab>('signals');

  // Context Logs in Drawer
  const [contextMode, setContextMode] = useState<'pod' | 'all_services'>('pod');
  const [contextLoading, setContextLoading] = useState<boolean>(false);
  const [contextData, setContextData] = useState<ContextLogsResponse | null>(null);
  const [contextError, setContextError] = useState<string | null>(null);

  // Copy feedback state
  const [copiedTemplateId, setCopiedTemplateId] = useState<number | null>(null);

  const hypotheses = useMemo(() => analysis?.hypotheses ?? [], [analysis?.hypotheses]);
  const clockSkewFindings = useMemo(
    () => analysis?.clock_skew_findings ?? [],
    [analysis?.clock_skew_findings]
  );
  const evidenceItems: EvidenceItem[] = useMemo(
    () => analysis?.evidence_items ?? [],
    [analysis?.evidence_items]
  );

  // Active Hypothesis for Drawer
  const activeHypothesis = useMemo(() => {
    if (!selectedHypoId) return hypotheses[0] ?? null;
    return hypotheses.find((h) => h.id === selectedHypoId) ?? hypotheses[0] ?? null;
  }, [hypotheses, selectedHypoId]);

  // Root cause hypothesis (rank 1 or role ROOT_CAUSE)
  const rootCauseHypo = useMemo(() => {
    return (
      hypotheses.find((h) => h.role === 'ROOT_CAUSE') ??
      hypotheses.find((h) => h.rank === 1) ??
      null
    );
  }, [hypotheses]);

  // Role distribution counters
  const roleCounts = useMemo(() => {
    let root = 0;
    let prop = 0;
    let symp = 0;
    let noise = 0;
    for (const h of hypotheses) {
      if (h.role === 'ROOT_CAUSE') root++;
      else if (h.role === 'PROPAGATION') prop++;
      else if (h.role === 'SYMPTOM') symp++;
      else if (h.role === 'NOISE') noise++;
    }
    return {
      all: hypotheses.length,
      ROOT_CAUSE: root,
      PROPAGATION: prop,
      SYMPTOM: symp,
      NOISE: noise,
    };
  }, [hypotheses]);

  // Filtered and Sorted hypotheses
  const filteredHypotheses = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    return hypotheses
      .filter((h) => {
        if (roleFilter !== 'ALL' && h.role !== roleFilter) {
          return false;
        }
        if (q) {
          const matchService = (h.service || '').toLowerCase().includes(q);
          const matchTemplate = (h.template_text || '').toLowerCase().includes(q);
          const matchSource = (h.source_place || '').toLowerCase().includes(q);
          const matchErrorClass = (h.error_class || '').toLowerCase().includes(q);
          const matchPod = (h.dominant_pod || '').toLowerCase().includes(q);
          const matchRoute = (h.dominant_route || '').toLowerCase().includes(q);
          const matchExplanation = (h.explanation || '').toLowerCase().includes(q);
          if (
            !matchService &&
            !matchTemplate &&
            !matchSource &&
            !matchErrorClass &&
            !matchPod &&
            !matchRoute &&
            !matchExplanation
          ) {
            return false;
          }
        }
        return true;
      })
      .sort((a, b) => {
        switch (sortOrder) {
          case 'CONFIDENCE_DESC':
            return safeNumber(b.confidence) - safeNumber(a.confidence);
          case 'GROWTH_DESC':
            return safeNumber(b.growth_ratio) - safeNumber(a.growth_ratio);
          case 'ONSET_ASC':
            return new Date(a.onset_time).getTime() - new Date(b.onset_time).getTime();
          case 'RANK_ASC':
          default:
            return safeNumber(a.rank) - safeNumber(b.rank);
        }
      });
  }, [hypotheses, roleFilter, searchQuery, sortOrder]);

  // Fetch context logs
  const fetchContextLogs = useCallback(
    async (pod: string, ts: string, mode: 'pod' | 'all_services') => {
      if (!caseId || !pod || !ts) return;
      setContextLoading(true);
      setContextError(null);
      try {
        const res = await api.getContextLogs(caseId, pod, ts, mode);
        setContextData(res);
      } catch (err: unknown) {
        setContextError(
          err instanceof Error ? err.message : 'Не удалось загрузить контекст логов'
        );
        setContextData(null);
      } finally {
        setContextLoading(false);
      }
    },
    [caseId]
  );

  // Open drawer helper
  const handleOpenDrawer = (h: Hypothesis, tab: DrawerTab = 'signals') => {
    setSelectedHypoId(h.id);
    setDrawerActiveTab(tab);
    setDrawerOpen(true);
    if (tab === 'context') {
      void fetchContextLogs(h.dominant_pod, h.onset_time, contextMode);
    }
  };

  // Close drawer
  const handleCloseDrawer = () => {
    setDrawerOpen(false);
  };

  // Switch context mode inside drawer
  const handleContextModeChange = (newMode: 'pod' | 'all_services') => {
    setContextMode(newMode);
    if (activeHypothesis) {
      void fetchContextLogs(
        activeHypothesis.dominant_pod,
        activeHypothesis.onset_time,
        newMode
      );
    }
  };

  // Copy template text to clipboard
  const handleCopyTemplate = async (templateId: number, text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopiedTemplateId(templateId);
      setTimeout(() => setCopiedTemplateId(null), 2000);
    } catch {
      // ignore clipboard error
    }
  };

  // Close drawer on ESC
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        if (showClockSkewModal) setShowClockSkewModal(false);
        else if (drawerOpen) setDrawerOpen(false);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [drawerOpen, showClockSkewModal]);

  // Loading state
  if (loading) {
    return (
      <div className="v2-page-container">
        <div className="v2-empty-state-card">
          <div className="storage-spinner" style={{ fontSize: '2.5rem' }}>⟳</div>
          <h3 className="mt-2">Анализ причин сбоя и ранжирование гипотез...</h3>
          <p className="text-muted text-sm mt-1">
            Обработка DRAIN-шаблонов, расчет опережений Пирсона и кросс-корреляция сигналов.
          </p>
        </div>
      </div>
    );
  }

  // Empty state
  if (!analysis || hypotheses.length === 0) {
    return (
      <div className="v2-page-container">
        <div className="v2-empty-state-card">
          <div className="v2-empty-icon">🎯</div>
          <h3>Гипотезы RCA еще не рассчитаны</h3>
          <p className="text-muted text-sm mt-1" style={{ maxWidth: '480px' }}>
            Для выбранного кейса анализ еще не проводился. Импортируйте логи или захватите поток
            Tilt, после чего запустите анализ RCA.
          </p>
          <div className="mt-2">
            <button
              type="button"
              className="btn btn-primary"
              onClick={onNavigateToTopology}
            >
              🕸️ Открыть топологию сервисов
            </button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="v2-page-container">
      {/* 1. Верхняя панель сводки инцидента (KPI Cards) */}
      <div className="v2-kpi-summary-grid">
        {/* KPI 1: Первопричина */}
        <div
          className={`v2-kpi-card v2-kpi-card-root ${rootCauseHypo ? 'cursor-pointer' : ''}`}
          onClick={() => {
            if (rootCauseHypo) handleOpenDrawer(rootCauseHypo, 'signals');
          }}
          title={rootCauseHypo ? 'Нажмите для подробного разбора первопричины' : undefined}
        >
          <div className="v2-kpi-card-header">
            <span className="v2-kpi-card-label">🎯 Главная первопричина</span>
            {rootCauseHypo && (
              <span className="badge-role-root-cause text-xs font-semibold">
                #{rootCauseHypo.rank} ROOT_CAUSE
              </span>
            )}
          </div>
          {rootCauseHypo ? (
            <div className="v2-kpi-card-content">
              <div className="v2-kpi-main-val text-truncate" title={rootCauseHypo.service}>
                {rootCauseHypo.service}
              </div>
              <div className="v2-kpi-sub-val font-mono text-xs text-truncate" title={rootCauseHypo.source_place}>
                {rootCauseHypo.source_place || 'код не локализован'}
              </div>
              <div className="v2-kpi-meta-row mt-1">
                <span className="v2-kpi-tag text-xs">
                  Уверенность: <strong>{safePercent(rootCauseHypo.confidence)}</strong>
                </span>
                {rootCauseHypo.error_class && (
                  <span className="v2-kpi-tag text-xs text-danger">
                    {rootCauseHypo.error_class}
                  </span>
                )}
              </div>
            </div>
          ) : (
            <div className="v2-kpi-card-content">
              <div className="v2-kpi-main-val text-muted">Не определена</div>
              <div className="v2-kpi-sub-val text-xs text-muted">Недостаточно данных</div>
            </div>
          )}
        </div>

        {/* KPI 2: Окно инцидента */}
        <div className="v2-kpi-card">
          <div className="v2-kpi-card-header">
            <span className="v2-kpi-card-label">⏱️ Окно инцидента</span>
            <span className="v2-kpi-run-badge font-mono text-xs" title={analysis.run_id}>
              {analysis.run_id.slice(0, 10)}…
            </span>
          </div>
          <div className="v2-kpi-card-content">
            <div className="v2-kpi-time-range font-mono">
              <div className="v2-time-point">
                <span className="text-muted text-xs">Старт: </span>
                <strong>{new Date(analysis.incident_start).toLocaleTimeString()}</strong>
              </div>
              <div className="v2-time-point">
                <span className="text-muted text-xs">Конец: </span>
                <strong>{new Date(analysis.incident_end).toLocaleTimeString()}</strong>
              </div>
            </div>
            <div className="v2-kpi-sub-val text-xs text-muted mt-1">
              Дата: {new Date(analysis.incident_start).toLocaleDateString()}
            </div>
          </div>
        </div>

        {/* KPI 3: Распределение гипотез */}
        <div className="v2-kpi-card">
          <div className="v2-kpi-card-header">
            <span className="v2-kpi-card-label">📊 Распределение гипотез</span>
            <span className="text-xs text-muted">Всего: {roleCounts.all}</span>
          </div>
          <div className="v2-kpi-card-content">
            <div className="v2-role-distribution-pills">
              <span className="v2-role-pill pill-root" title="Первопричины">
                🔴 {roleCounts.ROOT_CAUSE} Root
              </span>
              <span className="v2-role-pill pill-prop" title="Каскадное распространение">
                🟠 {roleCounts.PROPAGATION} Prop
              </span>
              <span className="v2-role-pill pill-symp" title="Симптомы">
                🟡 {roleCounts.SYMPTOM} Symp
              </span>
              <span className="v2-role-pill pill-noise" title="Фоновый шум">
                ⚪ {roleCounts.NOISE} Noise
              </span>
            </div>
            <div className="v2-role-distribution-bar mt-2">
              <div
                className="seg-root"
                style={{ width: `${safePercent(roleCounts.ROOT_CAUSE / (roleCounts.all || 1))}` }}
                title={`ROOT_CAUSE: ${roleCounts.ROOT_CAUSE}`}
              />
              <div
                className="seg-prop"
                style={{ width: `${safePercent(roleCounts.PROPAGATION / (roleCounts.all || 1))}` }}
                title={`PROPAGATION: ${roleCounts.PROPAGATION}`}
              />
              <div
                className="seg-symp"
                style={{ width: `${safePercent(roleCounts.SYMPTOM / (roleCounts.all || 1))}` }}
                title={`SYMPTOM: ${roleCounts.SYMPTOM}`}
              />
              <div
                className="seg-noise"
                style={{ width: `${safePercent(roleCounts.NOISE / (roleCounts.all || 1))}` }}
                title={`NOISE: ${roleCounts.NOISE}`}
              />
            </div>
          </div>
        </div>

        {/* KPI 4: Рассинхрон системных часов */}
        <div
          className={`v2-kpi-card ${clockSkewFindings.length > 0 ? 'v2-kpi-card-skew cursor-pointer' : ''}`}
          onClick={() => {
            if (clockSkewFindings.length > 0) setShowClockSkewModal(true);
          }}
          title={clockSkewFindings.length > 0 ? 'Нажмите для просмотра сдвигов по подам' : undefined}
        >
          <div className="v2-kpi-card-header">
            <span className="v2-kpi-card-label">🕰️ Рассинхрон часов (Clock Skew)</span>
            <span
              className={`text-xs font-semibold ${clockSkewFindings.length > 0 ? 'text-amber' : 'text-emerald'}`}
            >
              {clockSkewFindings.length > 0 ? '⚠ Сдвиг обнаружен' : '✓ В норме'}
            </span>
          </div>
          <div className="v2-kpi-card-content">
            {clockSkewFindings.length > 0 ? (
              <>
                <div className="v2-kpi-main-val text-amber">
                  {clockSkewFindings.length}{' '}
                  <span className="text-sm font-normal text-muted">
                    {clockSkewFindings.length === 1 ? 'под' : 'подов'}
                  </span>
                </div>
                <div className="v2-kpi-sub-val text-xs text-muted mt-1">
                  Скорректировано по <code className="font-mono">app_ts</code>
                </div>
                <div className="mt-1">
                  <span className="text-xs text-accent">Показать детали подов →</span>
                </div>
              </>
            ) : (
              <>
                <div className="v2-kpi-main-val text-emerald">0 мс</div>
                <div className="v2-kpi-sub-val text-xs text-muted mt-1">
                  Все поды кластера синхронизированы
                </div>
              </>
            )}
          </div>
        </div>
      </div>

      {/* 2. Панель фильтрации, поиска и сортировки */}
      <div className="v2-filter-toolbar">
        {/* Фильтр-табы по ролям */}
        <div className="v2-role-tabs">
          <button
            type="button"
            className={`v2-role-tab ${roleFilter === 'ALL' ? 'active' : ''}`}
            onClick={() => setRoleFilter('ALL')}
          >
            Все гипотезы
            <span className="v2-tab-badge">{roleCounts.all}</span>
          </button>
          <button
            type="button"
            className={`v2-role-tab role-tab-root ${roleFilter === 'ROOT_CAUSE' ? 'active' : ''}`}
            onClick={() => setRoleFilter('ROOT_CAUSE')}
          >
            🔴 Первопричины
            <span className="v2-tab-badge">{roleCounts.ROOT_CAUSE}</span>
          </button>
          <button
            type="button"
            className={`v2-role-tab role-tab-prop ${roleFilter === 'PROPAGATION' ? 'active' : ''}`}
            onClick={() => setRoleFilter('PROPAGATION')}
          >
            🟠 Каскад
            <span className="v2-tab-badge">{roleCounts.PROPAGATION}</span>
          </button>
          <button
            type="button"
            className={`v2-role-tab role-tab-symp ${roleFilter === 'SYMPTOM' ? 'active' : ''}`}
            onClick={() => setRoleFilter('SYMPTOM')}
          >
            🟡 Симптомы
            <span className="v2-tab-badge">{roleCounts.SYMPTOM}</span>
          </button>
          <button
            type="button"
            className={`v2-role-tab role-tab-noise ${roleFilter === 'NOISE' ? 'active' : ''}`}
            onClick={() => setRoleFilter('NOISE')}
          >
            ⚪ Шум
            <span className="v2-tab-badge">{roleCounts.NOISE}</span>
          </button>
        </div>

        {/* Текстовый поиск и сортировка */}
        <div className="v2-filter-actions-row">
          <div className="v2-search-input-box">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <circle cx="11" cy="11" r="8" />
              <line x1="21" y1="21" x2="16.65" y2="16.65" />
            </svg>
            <input
              type="text"
              className="v2-search-input"
              placeholder="Поиск по сервису, шаблону, файлу, поду, классу ошибки..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
            />
            {searchQuery && (
              <button
                type="button"
                className="btn-icon-xs text-muted"
                onClick={() => setSearchQuery('')}
                title="Очистить"
              >
                ✕
              </button>
            )}
          </div>

          <div className="v2-sort-box">
            <label className="text-xs text-muted">Сортировка:</label>
            <select
              className="v2-select text-xs"
              value={sortOrder}
              onChange={(e) => setSortOrder(e.target.value as SortOrder)}
            >
              <option value="RANK_ASC">По рангу (#1..#N)</option>
              <option value="CONFIDENCE_DESC">По уверенности (max)</option>
              <option value="GROWTH_DESC">По кратности роста (max)</option>
              <option value="ONSET_ASC">По времени старта (раньше)</option>
            </select>
          </div>

          <button
            type="button"
            className="btn btn-secondary text-xs"
            onClick={onNavigateToTopology}
            title="Открыть визуальный SVG-граф вызовов сервисов"
          >
            🕸️ Граф топологии
          </button>
        </div>
      </div>

      {/* 3. Список широких карточек гипотез (на 100% ширины) */}
      <div className="v2-hypotheses-list-container">
        {filteredHypotheses.length === 0 ? (
          <div className="v2-empty-state-card mt-2">
            <div className="text-muted" style={{ fontSize: '1.8rem' }}>🔍</div>
            <p className="mt-1 font-semibold">По заданному фильтру гипотезы не найдены</p>
            <p className="text-muted text-xs mt-1">
              Попробуйте сбросить текстовый поиск или выбрать другой таб роли.
            </p>
            <button
              type="button"
              className="btn btn-secondary-sm mt-2"
              onClick={() => {
                setRoleFilter('ALL');
                setSearchQuery('');
              }}
            >
              Сбросить фильтры
            </button>
          </div>
        ) : (
          filteredHypotheses.map((h: Hypothesis) => {
            const confidencePct = safePercent(h.confidence);
            const podSkewPct = (clamp(h.pod_skew_ratio, 0, 1) * 100).toFixed(0);
            const roleBadgeClass =
              h.role === 'ROOT_CAUSE'
                ? 'badge-role-root-cause'
                : h.role === 'PROPAGATION'
                ? 'badge-role-propagation'
                : h.role === 'SYMPTOM'
                ? 'badge-role-symptom'
                : 'badge-role-noise';

            return (
              <div
                key={h.id}
                className={`v2-hypo-card-full ${h.role === 'ROOT_CAUSE' ? 'v2-hypo-card-root-highlight' : ''}`}
              >
                {/* Карточка: Верхняя строка — Ранг, Бейдж роли, Уверенность, Класс ошибки */}
                <div className="v2-hypo-header-row">
                  <div className="v2-hypo-rank-and-tags">
                    <span className="v2-hypo-rank-badge">#{h.rank}</span>
                    <span className={`role-badge ${roleBadgeClass}`}>
                      {h.role === 'ROOT_CAUSE'
                        ? '🔴 ROOT_CAUSE'
                        : h.role === 'PROPAGATION'
                        ? '🟠 PROPAGATION'
                        : h.role === 'SYMPTOM'
                        ? '🟡 SYMPTOM'
                        : '⚪ NOISE'}
                    </span>
                    {h.error_class && (
                      <span className="error-class-tag" title="Класс ошибки">
                        {h.error_class}
                      </span>
                    )}
                    {h.is_constant_noise && (
                      <span className="noise-flag-badge">Постоянный шум</span>
                    )}
                  </div>

                  <div className="v2-hypo-confidence-box">
                    <span className="v2-conf-label text-xs text-muted">Уверенность:</span>
                    <strong className="v2-conf-value">{confidencePct}</strong>
                    <div className="v2-conf-mini-bar">
                      <div
                        className="v2-conf-mini-fill"
                        style={{
                          width: confidencePct,
                          backgroundColor:
                            h.confidence > 0.7
                              ? 'var(--accent-red)'
                              : h.confidence > 0.4
                              ? 'var(--accent-amber)'
                              : 'var(--accent-blue)',
                        }}
                      />
                    </div>
                  </div>
                </div>

                {/* Наглядная цепочка локализации сбоя: service -> dominant_pod -> source_place -> dominant_route */}
                <div className="v2-loc-chain-box">
                  <div className="v2-loc-step" title="Сервис">
                    <span className="v2-loc-step-lbl">Сервис:</span>
                    <strong className="v2-loc-step-val text-accent">{h.service}</strong>
                  </div>
                  <span className="v2-loc-arrow">→</span>

                  <div className="v2-loc-step" title="Доминирующий под и перекос">
                    <span className="v2-loc-step-lbl">Pod:</span>
                    <span className="v2-loc-step-val font-mono">
                      {h.dominant_pod || 'all-pods'}
                    </span>
                    <span className="v2-loc-skew-badge" title="Перекос ошибок по данному поду">
                      {podSkewPct}% перекос
                    </span>
                  </div>
                  <span className="v2-loc-arrow">→</span>

                  <div className="v2-loc-step" title="Файл и строка в коде">
                    <span className="v2-loc-step-lbl">Файл / стек:</span>
                    <span className="v2-loc-step-val font-mono text-cyan">
                      {h.source_place || 'код не локализован'}
                    </span>
                  </div>
                  <span className="v2-loc-arrow">→</span>

                  <div className="v2-loc-step" title="HTTP маршрут">
                    <span className="v2-loc-step-lbl">Маршрут:</span>
                    <code className="v2-loc-step-val font-mono">
                      {h.dominant_route || '/'}
                    </code>
                  </div>

                  {h.dominant_version && (
                    <div className="v2-loc-version-tag">
                      версия: <strong>{h.dominant_version}</strong>
                    </div>
                  )}
                </div>

                {/* Читабельный блок шаблона ошибки (template_text) + Объяснение */}
                <div className="v2-template-code-box">
                  <div className="v2-template-header">
                    <span className="text-xs text-muted">
                      DRAIN Template #{h.template_id}
                    </span>
                    <button
                      type="button"
                      className="btn-ghost-sm text-xs"
                      onClick={() => handleCopyTemplate(h.template_id, h.template_text)}
                      title="Скопировать шаблон"
                    >
                      {copiedTemplateId === h.template_id ? '✓ Скопировано' : 'Копировать шаблон'}
                    </button>
                  </div>
                  <pre className="v2-template-pre">
                    <code>{h.template_text}</code>
                  </pre>
                </div>

                {h.explanation && (
                  <div className="v2-hypo-explanation">
                    <span className="v2-explanation-icon">💡</span>
                    <p className="v2-explanation-text">{h.explanation}</p>
                  </div>
                )}

                {/* Метрики в чистую строку */}
                <div className="v2-metrics-row">
                  <div className="v2-metric-chip" title="Время первого проявления аномалии">
                    <span className="v2-m-lbl">Onset Time:</span>
                    <strong className="v2-m-val font-mono">
                      {new Date(h.onset_time).toLocaleTimeString()}
                    </strong>
                  </div>
                  <div className="v2-metric-chip" title="Базовая частота до инцидента">
                    <span className="v2-m-lbl">Baseline Rate:</span>
                    <strong className="v2-m-val font-mono">
                      {safeNumber(h.baseline_rate_per_min).toFixed(2)} / мин
                    </strong>
                  </div>
                  <div className="v2-metric-chip" title="Частота во время инцидента">
                    <span className="v2-m-lbl">Incident Rate:</span>
                    <strong className="v2-m-val font-mono">
                      {safeNumber(h.incident_rate_per_min).toFixed(2)} / мин
                    </strong>
                  </div>
                  <div className="v2-metric-chip" title="Кратность роста частоты">
                    <span className="v2-m-lbl">Кратность роста:</span>
                    <strong className="v2-m-val text-danger font-mono">
                      ×{safeNumber(h.growth_ratio).toFixed(1)}
                    </strong>
                  </div>
                  <div className="v2-metric-chip" title="Статистическая значимость Пуассона">
                    <span className="v2-m-lbl">Poisson p-value:</span>
                    <strong className="v2-m-val font-mono">
                      {safeNumber(h.poisson_p_value) < 0.001
                        ? '<0.001'
                        : safeNumber(h.poisson_p_value).toFixed(4)}
                    </strong>
                  </div>

                  {h.recurring_case_ids && h.recurring_case_ids.length > 0 && (
                    <div className="v2-recurring-badge" title="Повторение инцидента в предыдущих кейсах">
                      🔁 Повтор: <strong>{h.recurring_case_ids.join(', ')}</strong>
                    </div>
                  )}
                </div>

                {/* Кнопки действий карточки */}
                <div className="v2-hypo-actions-bar">
                  <button
                    type="button"
                    className="btn btn-primary-sm"
                    onClick={() => handleOpenDrawer(h, 'signals')}
                  >
                    📊 Открыть разбор 8 сигналов (S1..S8) и связей
                  </button>

                  <button
                    type="button"
                    className="btn btn-secondary-sm"
                    onClick={() => handleOpenDrawer(h, 'context')}
                  >
                    📜 Смотреть контекст логов (±20 строк)
                  </button>

                  {h.evidence_ids && h.evidence_ids.length > 0 && (
                    <button
                      type="button"
                      className="btn btn-ghost-sm"
                      onClick={() => handleOpenDrawer(h, 'evidence')}
                    >
                      📑 Улики ({h.evidence_ids.length})
                    </button>
                  )}

                  {h.dominant_route && (
                    <button
                      type="button"
                      className="btn btn-ghost-sm"
                      onClick={() => onNavigateToChains(h.dominant_route)}
                      title="Найти сквозную цепочку по маршруту"
                    >
                      🔍 Трассировка маршрута
                    </button>
                  )}
                </div>
              </div>
            );
          })
        )}
      </div>

      {/* 4. Выдвижное окно деталей гипотезы (HypothesisDetailDrawer, ~820px) */}
      {drawerOpen && activeHypothesis && (
        <div className="v2-drawer-overlay" onClick={handleCloseDrawer}>
          <div
            className="v2-drawer-panel v2-drawer-panel-wide"
            onClick={(e) => e.stopPropagation()}
            style={{ maxWidth: '820px' }}
          >
            {/* Drawer Header */}
            <div className="v2-drawer-header">
              <div className="v2-drawer-title-group">
                <div className="v2-drawer-title-row">
                  <span className="v2-drawer-rank">#{activeHypothesis.rank}</span>
                  <h3>{activeHypothesis.service}</h3>
                  <span
                    className={`role-badge ${
                      activeHypothesis.role === 'ROOT_CAUSE'
                        ? 'badge-role-root-cause'
                        : activeHypothesis.role === 'PROPAGATION'
                        ? 'badge-role-propagation'
                        : activeHypothesis.role === 'SYMPTOM'
                        ? 'badge-role-symptom'
                        : 'badge-role-noise'
                    }`}
                  >
                    {activeHypothesis.role}
                  </span>
                  <span className="v2-drawer-conf-tag">
                    Уверенность: <strong>{safePercent(activeHypothesis.confidence)}</strong>
                  </span>
                </div>
                <div className="v2-drawer-subtitle font-mono text-xs text-muted">
                  Pod: {activeHypothesis.dominant_pod || 'all-pods'} | Файл:{' '}
                  {activeHypothesis.source_place || 'код не локализован'}
                </div>
              </div>

              <button
                type="button"
                className="btn-close-sm"
                onClick={handleCloseDrawer}
                title="Закрыть окно (Esc)"
              >
                ✕
              </button>
            </div>

            {/* Drawer Tabs Bar */}
            <div className="v2-drawer-tabs">
              <button
                type="button"
                className={`v2-drawer-tab ${drawerActiveTab === 'signals' ? 'active' : ''}`}
                onClick={() => setDrawerActiveTab('signals')}
              >
                📊 Сигналы S1..S8 и Lead-Lag
              </button>
              <button
                type="button"
                className={`v2-drawer-tab ${drawerActiveTab === 'context' ? 'active' : ''}`}
                onClick={() => {
                  setDrawerActiveTab('context');
                  void fetchContextLogs(
                    activeHypothesis.dominant_pod,
                    activeHypothesis.onset_time,
                    contextMode
                  );
                }}
              >
                📜 Контекст логов (±20 строк)
              </button>
              <button
                type="button"
                className={`v2-drawer-tab ${drawerActiveTab === 'evidence' ? 'active' : ''}`}
                onClick={() => setDrawerActiveTab('evidence')}
              >
                📑 Связанные улики ({activeHypothesis.evidence_ids?.length || 0})
              </button>
            </div>

            {/* Drawer Body Content */}
            <div className="v2-drawer-body">
              {/* Tab 1: Сигналы S1..S8 и Lead-Lag */}
              {drawerActiveTab === 'signals' && (
                <div className="v2-drawer-section">
                  <div className="v2-section-lead">
                    <h4>8 мультимодальных сигналов первопричины (S1..S8)</h4>
                    <p className="text-xs text-muted">
                      Каждый сигнал нормирован в диапазон [0.00 .. 1.00] и взвешивается в байесовской оценке
                      уверенности.
                    </p>
                  </div>

                  <div className="v2-signals-breakdown-list">
                    <SignalDetailRow
                      code="S1"
                      title="Трассировка сквозной цепочки (Trace Chain)"
                      value={activeHypothesis.signals.trace_chain_score}
                      description="Определяет участие сервиса в цепочке распределенной трассировки инцидента."
                    />
                    <SignalDetailRow
                      code="S2"
                      title="Опережение времени старта (Onset Priority)"
                      value={activeHypothesis.signals.onset_score}
                      description="Временной приоритет первого появления аномалии относительно других сервисов."
                    />
                    <SignalDetailRow
                      code="S3"
                      title="Временное опережение кросс-корреляции (Lead-Lag)"
                      value={activeHypothesis.signals.lead_lag_score}
                      description="Временной сдвиг (смещение пиков кросс-корреляции Пирсона с опережением других сервисов)."
                    />
                    <SignalDetailRow
                      code="S4"
                      title="Новизна шаблона ошибки (Novelty Score)"
                      value={activeHypothesis.signals.novelty_score}
                      description="Степень новизны шаблона ошибки (отсутствие или единичные случаи в базовом периоде)."
                    />
                    <SignalDetailRow
                      code="S5"
                      title="Причинное направление топологии (Causal Direction)"
                      value={activeHypothesis.signals.causal_dir_score}
                      description="Направление графа вызовов: возникновение ошибки в конечном узле-исполнителе."
                    />
                    <SignalDetailRow
                      code="S6"
                      title="Корень ориентированного графа (Graph Root)"
                      value={activeHypothesis.signals.graph_root_score}
                      description="Близость сервиса к источнику сбоя в направленном ациклическом графе связей."
                    />
                    <SignalDetailRow
                      code="S7"
                      title="Локализация по поду / маршруту (Localization)"
                      value={activeHypothesis.signals.localization_score}
                      description="Концентрация ошибок на конкретном поде (перекос реплик) или маршруте API."
                    />
                    <SignalDetailRow
                      code="S8"
                      title="Корреляция с изменениями (Change Correlation)"
                      value={activeHypothesis.signals.change_corr_score}
                      description="Совпадение по времени с выкаткой новой версии или перезапуском пода."
                    />
                  </div>

                  {/* Таблица Lead-Lag связей */}
                  <div className="v2-drawer-subblock mt-3">
                    <div className="v2-subblock-header">
                      <h4>Причинно-следственные опережения (Lead-Lag Peers)</h4>
                      <span className="text-xs text-muted">
                        Корреляция временных рядов Пирсона $r$ и лаг в секундах
                      </span>
                    </div>

                    {activeHypothesis.lead_lag_peers &&
                    activeHypothesis.lead_lag_peers.length > 0 ? (
                      <div className="v2-table-wrapper mt-1">
                        <table className="v2-table">
                          <thead>
                            <tr>
                              <th>Сервис-партнёр</th>
                              <th>Шаблон DRAIN</th>
                              <th>Временной лаг</th>
                              <th>Коэффициент Пирсона $r$</th>
                            </tr>
                          </thead>
                          <tbody>
                            {activeHypothesis.lead_lag_peers.map((peer, idx) => (
                              <tr key={idx}>
                                <td className="font-semibold text-xs">{peer.peer_service}</td>
                                <td className="font-mono text-xs">#{peer.peer_template}</td>
                                <td className="text-xs">
                                  {peer.lag_seconds > 0 ? (
                                    <span className="text-danger font-semibold">
                                      Опережает на +{peer.lag_seconds} с
                                    </span>
                                  ) : peer.lag_seconds < 0 ? (
                                    <span className="text-cyan font-semibold">
                                      Отстает на {peer.lag_seconds} с
                                    </span>
                                  ) : (
                                    <span className="text-muted">Одновременно (0 с)</span>
                                  )}
                                </td>
                                <td className="font-mono text-xs">
                                  r = {safeNumber(peer.correlation_r).toFixed(2)}
                                </td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    ) : (
                      <div className="v2-empty-hint p-2">
                        Кросс-корреляционные опережения с другими сервисами не зафиксированы.
                      </div>
                    )}
                  </div>
                </div>
              )}

              {/* Tab 2: Контекст логов (±20 строк) */}
              {drawerActiveTab === 'context' && (
                <div className="v2-drawer-section">
                  {/* Переключатель режимов: pod / all_services */}
                  <div className="v2-context-toolbar">
                    <div className="v2-context-meta">
                      <span className="text-xs text-muted">
                        Под: <strong>{activeHypothesis.dominant_pod || 'all'}</strong> | Время:{' '}
                        <strong>
                          {new Date(activeHypothesis.onset_time).toLocaleTimeString()}
                        </strong>
                      </span>
                    </div>

                    <div className="v2-context-mode-switch">
                      <button
                        type="button"
                        className={`btn-chip ${contextMode === 'pod' ? 'btn-chip-active' : ''}`}
                        onClick={() => handleContextModeChange('pod')}
                      >
                        Только Pod
                      </button>
                      <button
                        type="button"
                        className={`btn-chip ${contextMode === 'all_services' ? 'btn-chip-active' : ''}`}
                        onClick={() => handleContextModeChange('all_services')}
                      >
                        Все сервисы
                      </button>
                      <button
                        type="button"
                        className="btn-icon-xs ml-1"
                        onClick={() =>
                          void fetchContextLogs(
                            activeHypothesis.dominant_pod,
                            activeHypothesis.onset_time,
                            contextMode
                          )
                        }
                        title="Обновить контекст"
                      >
                        ⟳
                      </button>
                    </div>
                  </div>

                  {/* Предупреждение об очищенных сырых логах */}
                  {contextData?.raw_purged && (
                    <div className="raw-purged-banner mt-2">
                      <span className="raw-purged-icon">⚠</span>
                      <div>
                        <strong>Сырые строки логов очищены (raw_purged: true)</strong>
                        <p className="text-xs">
                          Для соблюдения дискового лимита 15 ГБ исходный файл логов удален.
                          Отображаются сохраненные нормализованные события из ClickHouse.
                        </p>
                      </div>
                    </div>
                  )}

                  {contextLoading ? (
                    <div className="v2-loading-box mt-3">
                      <div className="storage-spinner">⟳</div>
                      <span className="ml-2 text-xs">Загрузка контекста логов (±20 строк)...</span>
                    </div>
                  ) : contextError ? (
                    <div className="sidebar-error-toast mt-2">{contextError}</div>
                  ) : contextData && contextData.events && contextData.events.length > 0 ? (
                    <div className="v2-context-terminal mt-2">
                      <table className="terminal-table">
                        <thead>
                          <tr>
                            <th style={{ width: '42px' }}>#</th>
                            <th style={{ width: '90px' }}>Время</th>
                            <th style={{ width: '100px' }}>Сервис</th>
                            <th style={{ width: '110px' }}>Под</th>
                            <th style={{ width: '55px' }}>Уровень</th>
                            <th>Сообщение / Сырая строка</th>
                          </tr>
                        </thead>
                        <tbody>
                          {contextData.events.map((evt: NormalizedEvent, idx: number) => {
                            const isTarget = evt.ts === activeHypothesis.onset_time;
                            const centerIdx = Math.floor(contextData.events.length / 2);
                            const lineOffset = idx - centerIdx;

                            return (
                              <tr
                                key={evt.event_id || idx}
                                className={`terminal-row ${isTarget ? 'terminal-row-target' : ''} ${
                                  evt.level === 'ERROR' ? 'terminal-row-err' : ''
                                }`}
                              >
                                <td className="line-num font-mono">
                                  {isTarget
                                    ? '▶ 0'
                                    : lineOffset > 0
                                    ? `+${lineOffset}`
                                    : lineOffset}
                                </td>
                                <td className="font-mono text-xs">
                                  {new Date(evt.ts).toLocaleTimeString([], {
                                    hour: '2-digit',
                                    minute: '2-digit',
                                    second: '2-digit',
                                    fractionalSecondDigits: 3,
                                  })}
                                </td>
                                <td className="text-xs font-semibold">{evt.service}</td>
                                <td className="font-mono text-xs text-muted">{evt.pod}</td>
                                <td>
                                  <span className={`level-pill level-${(evt.level || 'info').toLowerCase()}`}>
                                    {evt.level}
                                  </span>
                                </td>
                                <td className="font-mono text-xs log-line-cell">
                                  {evt.raw_redacted || evt.message}
                                </td>
                              </tr>
                            );
                          })}
                        </tbody>
                      </table>
                    </div>
                  ) : (
                    <div className="v2-empty-hint p-4 mt-2">
                      В окне ±20 строк события не найдены или лог был полностью удален.
                    </div>
                  )}
                </div>
              )}

              {/* Tab 3: Связанные улики (Evidence) */}
              {drawerActiveTab === 'evidence' && (
                <div className="v2-drawer-section">
                  <div className="v2-section-lead">
                    <h4>Связанные доказательства и улики инцидента</h4>
                    <p className="text-xs text-muted">
                      Улики подтверждают обоснованность гипотезы и экспортируются в отчёт Gemini.
                    </p>
                  </div>

                  {activeHypothesis.evidence_ids && activeHypothesis.evidence_ids.length > 0 ? (
                    <div className="v2-evidence-cards-list mt-2">
                      {activeHypothesis.evidence_ids.map((evId: string) => {
                        const fullItem = evidenceItems.find((e) => e.id === evId);

                        return (
                          <div key={evId} className="v2-evidence-item-card">
                            <div className="v2-ev-card-top">
                              <span className="v2-ev-id-badge font-mono">{evId}</span>
                              {fullItem?.kind && (
                                <span className={`kind-badge kind-${fullItem.kind}`}>
                                  {fullItem.kind}
                                </span>
                              )}
                              <button
                                type="button"
                                className="btn-primary-sm text-xs ml-auto"
                                onClick={() => {
                                  handleCloseDrawer();
                                  onNavigateToEvidence(evId);
                                }}
                              >
                                Перейти в отчёт улик →
                              </button>
                            </div>

                            {fullItem ? (
                              <div className="v2-ev-card-body mt-1">
                                <div className="font-semibold text-xs">{fullItem.summary}</div>
                                <div className="text-xs text-muted mt-1">
                                  Файл: <code className="font-mono">{fullItem.source_file}</code> (строка {fullItem.source_record_idx})
                                </div>
                                {fullItem.redacted_line && (
                                  <pre className="v2-ev-code-snippet mt-1">
                                    <code>{fullItem.redacted_line}</code>
                                  </pre>
                                )}
                              </div>
                            ) : (
                              <div className="text-xs text-muted mt-1">
                                Улика привязана к данной гипотезе по результатам статистического анализа.
                              </div>
                            )}
                          </div>
                        );
                      })}
                    </div>
                  ) : (
                    <div className="v2-empty-hint p-4 mt-2">
                      К данной гипотезе не привязано конкретных улик.
                    </div>
                  )}
                </div>
              )}
            </div>

            {/* Drawer Footer */}
            <div className="v2-drawer-footer">
              <button
                type="button"
                className="btn btn-secondary text-xs"
                onClick={handleCloseDrawer}
              >
                Закрыть
              </button>

              <button
                type="button"
                className="btn btn-primary text-xs"
                onClick={onNavigateToTopology}
              >
                🕸️ Посмотреть на графе топологии
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 5. Модальное окно рассинхрона часов (Clock Skew Details Modal) */}
      {showClockSkewModal && (
        <div
          className="modal-backdrop"
          onClick={() => setShowClockSkewModal(false)}
        >
          <div
            className="modal-content modal-content-wide"
            onClick={(e) => e.stopPropagation()}
            style={{ maxWidth: '680px' }}
          >
            <div className="modal-header">
              <div>
                <h3>Рассинхрон системных часов ({clockSkewFindings.length} подов)</h3>
                <span className="text-xs text-muted">
                  Сдвиги часов автоматически скорректированы перед расчетом временных задержек
                </span>
              </div>
              <button
                type="button"
                className="btn-close-sm"
                onClick={() => setShowClockSkewModal(false)}
              >
                ✕
              </button>
            </div>

            <div className="p-3">
              <div className="v2-table-wrapper">
                <table className="v2-table">
                  <thead>
                    <tr>
                      <th>Сервис</th>
                      <th>Под</th>
                      <th>Сдвиг часов (мс)</th>
                      <th>Время хоста (raw_ts)</th>
                      <th>Время приложения (app_ts)</th>
                    </tr>
                  </thead>
                  <tbody>
                    {clockSkewFindings.map((f: ClockSkewFinding, idx: number) => (
                      <tr key={`${f.pod}-${idx}`}>
                        <td className="font-semibold text-xs">{f.service}</td>
                        <td className="font-mono text-xs text-muted">{f.pod}</td>
                        <td className="font-mono text-xs">
                          <span
                            className={
                              f.skew_ms > 0
                                ? 'text-amber font-semibold'
                                : 'text-cyan font-semibold'
                            }
                          >
                            {f.skew_ms > 0 ? `+${f.skew_ms}` : f.skew_ms} мс
                          </span>
                        </td>
                        <td className="font-mono text-xs text-muted">
                          {new Date(f.sample_raw_ts).toLocaleTimeString()}
                        </td>
                        <td className="font-mono text-xs text-muted">
                          {new Date(f.sample_app_ts).toLocaleTimeString()}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>

            <div className="modal-footer">
              <button
                type="button"
                className="btn btn-secondary text-xs"
                onClick={() => setShowClockSkewModal(false)}
              >
                Закрыть
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};

// Sub-component for 8 Signals Breakdown Row
interface SignalDetailRowProps {
  code: string;
  title: string;
  value: number;
  description: string;
}

const SignalDetailRow: React.FC<SignalDetailRowProps> = ({
  code,
  title,
  value,
  description,
}) => {
  const safeVal = clamp(value, 0, 1);
  const pct = (safeVal * 100).toFixed(0);

  let fillColor = '#3b82f6';
  if (safeVal >= 0.7) fillColor = '#ef4444';
  else if (safeVal >= 0.4) fillColor = '#f59e0b';

  return (
    <div className="v2-signal-row">
      <div className="v2-signal-row-top">
        <span className="v2-signal-code font-mono">{code}</span>
        <span className="v2-signal-title font-semibold text-xs">{title}</span>
        <span className="v2-signal-val font-mono">{safeVal.toFixed(2)}</span>
      </div>

      <div className="v2-signal-progress-track">
        <div
          className="v2-signal-progress-fill"
          style={{ width: `${pct}%`, backgroundColor: fillColor }}
        />
      </div>

      <p className="v2-signal-desc text-xs text-muted">{description}</p>
    </div>
  );
};

export default HypothesesPage;
