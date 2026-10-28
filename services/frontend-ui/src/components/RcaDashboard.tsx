import React, { useState, useMemo, useCallback } from 'react';
import {
  api,
  AnalyzeCaseResponse,
  Hypothesis,
  ServiceEdge,
  ClockSkewFinding,
  NormalizedEvent,
  ContextLogsResponse,
} from '../api/client';

export interface RcaDashboardProps {
  caseId: string;
  analysis: AnalyzeCaseResponse | null;
  loading?: boolean;
  onRefresh?: () => void;
}

export const RcaDashboard: React.FC<RcaDashboardProps> = ({
  caseId,
  analysis,
  loading = false,
  onRefresh,
}) => {
  // Selected hypothesis for deep inspection
  const [selectedHypoId, setSelectedHypoId] = useState<string | null>(null);

  // Chain Search state
  const [chainKey, setChainKey] = useState<string>('');
  const [chainLoading, setChainLoading] = useState<boolean>(false);
  const [chainEvents, setChainEvents] = useState<NormalizedEvent[] | null>(null);
  const [chainError, setChainError] = useState<string | null>(null);
  const [showChainModal, setShowChainModal] = useState<boolean>(false);

  // Context Logs state
  const [contextModalOpen, setContextModalOpen] = useState<boolean>(false);
  const [contextPod, setContextPod] = useState<string>('');
  const [contextTs, setContextTs] = useState<string>('');
  const [contextMode, setContextMode] = useState<'pod' | 'all_services'>('pod');
  const [contextLoading, setContextLoading] = useState<boolean>(false);
  const [contextData, setContextData] = useState<ContextLogsResponse | null>(null);
  const [contextError, setContextError] = useState<string | null>(null);

  // Selected node in SVG Graph
  const [selectedServiceNode, setSelectedServiceNode] = useState<string | null>(null);

  // Fallbacks if analysis is empty
  const hypotheses = analysis?.hypotheses ?? [];
  const serviceEdges = analysis?.service_edges ?? [];
  const clockSkewFindings = analysis?.clock_skew_findings ?? [];
  const timelinePoints = analysis?.timeline ?? [];

  // Active hypothesis
  const activeHypothesis = useMemo(() => {
    if (!hypotheses.length) return null;
    if (selectedHypoId) {
      return hypotheses.find((h) => h.id === selectedHypoId) || hypotheses[0];
    }
    return hypotheses[0];
  }, [hypotheses, selectedHypoId]);

  // Role color helper
  const getRoleBadgeClass = (role: Hypothesis['role']): string => {
    switch (role) {
      case 'ROOT_CAUSE':
        return 'badge-role-root-cause';
      case 'PROPAGATION':
        return 'badge-role-propagation';
      case 'SYMPTOM':
        return 'badge-role-symptom';
      case 'NOISE':
      default:
        return 'badge-role-noise';
    }
  };

  const getRoleLabel = (role: Hypothesis['role']): string => {
    switch (role) {
      case 'ROOT_CAUSE':
        return 'ROOT CAUSE';
      case 'PROPAGATION':
        return 'PROPAGATION';
      case 'SYMPTOM':
        return 'SYMPTOM';
      case 'NOISE':
      default:
        return 'NOISE';
    }
  };

  // --- 1. Chain Search Handler ---
  const handleSearchChain = useCallback(
    async (keyToSearch?: string) => {
      const key = (keyToSearch ?? chainKey).trim();
      if (!key || !caseId) return;
      setChainLoading(true);
      setChainError(null);
      setShowChainModal(true);
      try {
        const events = await api.getChain(caseId, key);
        setChainEvents(events);
      } catch (err: unknown) {
        setChainError(err instanceof Error ? err.message : 'Ошибка при поиске цепочки');
        setChainEvents([]);
      } finally {
        setChainLoading(false);
      }
    },
    [caseId, chainKey]
  );

  // --- 2. Context Logs Handler ---
  const fetchContextLogs = useCallback(
    async (pod: string, ts: string, mode: 'pod' | 'all_services') => {
      if (!caseId || !pod || !ts) return;
      setContextLoading(true);
      setContextError(null);
      try {
        const res = await api.getContextLogs(caseId, pod, ts, mode);
        setContextData(res);
      } catch (err: unknown) {
        setContextError(err instanceof Error ? err.message : 'Ошибка при загрузке контекста логов');
        setContextData(null);
      } finally {
        setContextLoading(false);
      }
    },
    [caseId]
  );

  const openContextModal = (pod: string, ts: string) => {
    setContextPod(pod);
    setContextTs(ts);
    setContextModalOpen(true);
    void fetchContextLogs(pod, ts, contextMode);
  };

  const handleModeChange = (mode: 'pod' | 'all_services') => {
    setContextMode(mode);
    if (contextPod && contextTs) {
      void fetchContextLogs(contextPod, contextTs, mode);
    }
  };

  // --- 3. Pure Vector SVG Topology Graph ---
  const topologyGraph = useMemo(() => {
    // Collect unique services
    const servicesSet = new Set<string>();
    hypotheses.forEach((h) => servicesSet.add(h.service));
    serviceEdges.forEach((e) => {
      servicesSet.add(e.caller_service);
      servicesSet.add(e.callee_service);
    });

    const services = Array.from(servicesSet);
    const n = services.length;
    const width = 640;
    const height = 360;
    const cx = width / 2;
    const cy = height / 2;
    const radius = Math.min(width, height) * 0.38;

    // Node coordinates on a circle
    const nodeCoords: Record<string, { x: number; y: number; role?: Hypothesis['role'] }> = {};
    services.forEach((svc, idx) => {
      const angle = (2 * Math.PI * idx) / (n || 1) - Math.PI / 2;
      const x = cx + radius * Math.cos(angle);
      const y = cy + radius * Math.sin(angle);
      const matchedHypo = hypotheses.find((h) => h.service === svc);
      nodeCoords[svc] = { x, y, role: matchedHypo?.role };
    });

    return { width, height, services, nodeCoords, edges: serviceEdges };
  }, [hypotheses, serviceEdges]);

  // --- 4. Pure Vector SVG Timeline ---
  const timelineChart = useMemo(() => {
    if (!timelinePoints.length) {
      return { width: 640, height: 220, bars: [], maxEvents: 0, chartH: 165, padX: 45, padBottom: 35, padTop: 20 };
    }

    // Group timeline points by bucket
    const bucketMap = new Map<string, { timeStr: string; error: number; warn: number; info: number; maxLat: number }>();

    timelinePoints.forEach((pt) => {
      const bKey = pt.bucket;
      const cur = bucketMap.get(bKey) || {
        timeStr: new Date(pt.bucket).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
        error: 0,
        warn: 0,
        info: 0,
        maxLat: 0,
      };

      const lvl = (pt.level || '').toUpperCase();
      if (lvl === 'ERROR' || lvl === 'FATAL') {
        cur.error += pt.events_count;
      } else if (lvl === 'WARN' || lvl === 'WARNING') {
        cur.warn += pt.events_count;
      } else {
        cur.info += pt.events_count;
      }
      if (pt.max_latency_ns > cur.maxLat) {
        cur.maxLat = pt.max_latency_ns;
      }
      bucketMap.set(bKey, cur);
    });

    const sortedBuckets = Array.from(bucketMap.entries()).sort(
      (a, b) => new Date(a[0]).getTime() - new Date(b[0]).getTime()
    );

    let maxTotal = 1;
    sortedBuckets.forEach(([_, data]) => {
      const sum = data.error + data.warn + data.info;
      if (sum > maxTotal) maxTotal = sum;
    });

    const width = 640;
    const height = 220;
    const padX = 45;
    const padBottom = 35;
    const padTop = 20;
    const chartW = width - padX - 15;
    const chartH = height - padBottom - padTop;
    const barWidth = Math.max(8, Math.min(28, (chartW / (sortedBuckets.length || 1)) * 0.75));

    const bars = sortedBuckets.map(([bucket, data], idx) => {
      const stepX = chartW / (sortedBuckets.length || 1);
      const x = padX + idx * stepX + (stepX - barWidth) / 2;
      const total = data.error + data.warn + data.info;
      const totalH = (total / maxTotal) * chartH;
      const errH = (data.error / maxTotal) * chartH;
      const warnH = (data.warn / maxTotal) * chartH;
      const infoH = (data.info / maxTotal) * chartH;

      return {
        bucket,
        timeStr: data.timeStr,
        x,
        y: height - padBottom - totalH,
        errH,
        warnH,
        infoH,
        totalH,
        total,
        error: data.error,
        warn: data.warn,
        info: data.info,
        maxLat: data.maxLat,
        barWidth,
      };
    });

    return { width, height, bars, maxEvents: maxTotal, chartH, padX, padBottom, padTop };
  }, [timelinePoints]);

  if (loading) {
    return (
      <div className="rca-loading-box">
        <span className="spinner-icon" style={{ fontSize: '2rem' }}>⟳</span>
        <p className="mt-1">Анализ первопричин и корреляция сигналов...</p>
      </div>
    );
  }

  if (!analysis || hypotheses.length === 0) {
    return (
      <div className="rca-empty-box">
        <h3>Анализ кейса еще не выполнен</h3>
        <p className="text-muted">
          Запустите анализ первопричин в левой боковой панели, чтобы получить ранжированные гипотезы, граф сервисов и улики.
        </p>
      </div>
    );
  }

  return (
    <div className="rca-dashboard-container">
      {/* 1. Header Toolbar: Incident info and Quick Chain Search */}
      <div className="rca-toolbar">
        <div className="rca-incident-meta">
          <span className="incident-run-badge">Run ID: <strong>{analysis.run_id}</strong></span>
          <span className="incident-time-range">
            {new Date(analysis.incident_start).toLocaleTimeString()} –{' '}
            {new Date(analysis.incident_end).toLocaleTimeString()}
          </span>
          {onRefresh && (
            <button type="button" className="btn-icon-xs" onClick={onRefresh} title="Обновить анализ">
              ⟳
            </button>
          )}
        </div>

        {/* Chain Search Input */}
        <div className="chain-search-bar">
          <input
            type="text"
            className="input-text chain-input"
            placeholder="Поиск сквозной цепочки (trace_id / request_id / session_id)..."
            value={chainKey}
            onChange={(e) => setChainKey(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') void handleSearchChain();
            }}
          />
          <button
            type="button"
            className="btn btn-secondary btn-search-chain"
            onClick={() => void handleSearchChain()}
            disabled={!chainKey.trim() || chainLoading}
          >
            {chainLoading ? 'Поиск...' : 'Найти цепочку'}
          </button>
        </div>
      </div>

      {/* 2. Clock Skew Findings Banner (if any) */}
      {clockSkewFindings.length > 0 && (
        <div className="clock-skew-alert-banner">
          <div className="skew-alert-header">
            <span className="skew-alert-icon">⚠</span>
            <strong>Обнаружен рассинхрон системных часов ({clockSkewFindings.length} подов)</strong>
            <span className="skew-alert-subtext">
              Время автоматически скорректировано по app_ts перед ранжированием
            </span>
          </div>
          <div className="skew-items-list">
            {clockSkewFindings.map((finding: ClockSkewFinding, idx: number) => (
              <span key={`${finding.pod}-${idx}`} className="skew-chip">
                <strong>{finding.service}</strong> ({finding.pod}):{' '}
                <span className={finding.skew_ms > 0 ? 'text-amber' : 'text-cyan'}>
                  {finding.skew_ms > 0 ? `+${finding.skew_ms}` : finding.skew_ms} мс
                </span>
              </span>
            ))}
          </div>
        </div>
      )}

      {/* 3. Main Dashboard Layout: Hypotheses Cards (Left) + Topology & Timeline (Right) */}
      <div className="rca-main-grid">
        {/* Left Column: Hypotheses List */}
        <div className="rca-hypotheses-column">
          <div className="column-header">
            <h3>Ранжированные гипотезы RCA ({hypotheses.length})</h3>
            <span className="text-muted text-xs">Contract-first Bayesian / Poisson</span>
          </div>

          <div className="hypotheses-cards-list">
            {hypotheses.map((h: Hypothesis) => {
              const isSelected = activeHypothesis?.id === h.id;
              const roleClass = getRoleBadgeClass(h.role);
              const confidencePct = (h.confidence * 100).toFixed(1);
              const podSkewPct = (h.pod_skew_ratio * 100).toFixed(0);

              return (
                <div
                  key={h.id}
                  className={`hypothesis-card ${isSelected ? 'hypothesis-card-selected' : ''}`}
                  onClick={() => setSelectedHypoId(h.id)}
                >
                  {/* Card Header: Rank, Role Badge, Confidence */}
                  <div className="hypo-card-top">
                    <div className="hypo-rank-and-role">
                      <span className="hypo-rank-num">#{h.rank}</span>
                      <span className={`role-badge ${roleClass}`}>{getRoleLabel(h.role)}</span>
                      {h.is_constant_noise && <span className="noise-flag-badge">Постоянный шум</span>}
                    </div>
                    <div className="hypo-confidence" title={`Оценка уверенности: ${confidencePct}%`}>
                      <span className="conf-label">Уверенность:</span>
                      <strong className="conf-value">{confidencePct}%</strong>
                    </div>
                  </div>

                  {/* Failure Location: service → dominant_pod → source_place → dominant_route */}
                  <div className="hypo-location-chain">
                    <span className="loc-segment loc-service" title="Сервис">{h.service}</span>
                    <span className="loc-arrow">→</span>
                    <span className="loc-segment loc-pod" title="Доминирующий под">
                      {h.dominant_pod || 'all-pods'}
                    </span>
                    <span className="loc-arrow">→</span>
                    <span className="loc-segment loc-source" title="Место в коде / стек">
                      {h.source_place || 'unknown'}
                    </span>
                    <span className="loc-arrow">→</span>
                    <span className="loc-segment loc-route" title="HTTP маршрут">
                      {h.dominant_route || '/'}
                    </span>
                  </div>

                  {/* Version & Pod skew metadata */}
                  <div className="hypo-meta-row text-xs">
                    {h.dominant_version && (
                      <span className="meta-tag">версия: <strong>{h.dominant_version}</strong></span>
                    )}
                    <span className="meta-tag">
                      перекос по поду: <strong>{podSkewPct}% ошибок</strong>
                    </span>
                    <span className="meta-tag">
                      старт: <strong>{new Date(h.onset_time).toLocaleTimeString()}</strong>
                    </span>
                  </div>

                  {/* Error class & Template message */}
                  <div className="hypo-message-box">
                    {h.error_class && <span className="error-class-tag">{h.error_class}</span>}
                    <code className="template-text">{h.template_text}</code>
                  </div>

                  {/* Growth ratio & Poisson p-value */}
                  <div className="hypo-metrics-bar">
                    <span title="Частота до инцидента">
                      Baseline: <strong>{h.baseline_rate_per_min.toFixed(2)}/мин</strong>
                    </span>
                    <span title="Частота в момент инцидента">
                      Инцидент: <strong>{h.incident_rate_per_min.toFixed(2)}/мин</strong>
                    </span>
                    <span title="Кратность роста частоты">
                      Рост: <strong className="text-danger">{h.growth_ratio.toFixed(1)}x</strong>
                    </span>
                    <span title="P-value статистического критерия Пуассона">
                      P-val: <strong>{h.poisson_p_value < 0.001 ? '<0.001' : h.poisson_p_value.toFixed(4)}</strong>
                    </span>
                  </div>

                  {/* Recurring Incidents Badge */}
                  {h.recurring_case_ids && h.recurring_case_ids.length > 0 && (
                    <div className="recurring-cases-box">
                      <span className="recurring-icon">🔁</span>
                      <span>
                        Повтор инцидента (найден в кейсах:{' '}
                        <strong>{h.recurring_case_ids.join(', ')}</strong>)
                      </span>
                    </div>
                  )}

                  {/* Explanation text */}
                  {h.explanation && <p className="hypo-explanation">{h.explanation}</p>}

                  {/* Lead-Lag Peers (if any) */}
                  {h.lead_lag_peers && h.lead_lag_peers.length > 0 && (
                    <div className="lead-lag-box text-xs">
                      <span className="font-semibold">Причинно-следственная связь (Lead-Lag):</span>
                      <div className="lead-lag-list">
                        {h.lead_lag_peers.map((peer, pIdx) => (
                          <span key={pIdx} className="lead-lag-chip">
                            {peer.lag_seconds > 0 ? 'опережает' : 'отстает от'}{' '}
                            <strong>{peer.peer_service}</strong> на {Math.abs(peer.lag_seconds)}с (r=
                            {peer.correlation_r.toFixed(2)})
                          </span>
                        ))}
                      </div>
                    </div>
                  )}

                  {/* Scale of 8 Signals (S1..S8) */}
                  <div className="signals-breakdown-section">
                    <div className="signals-title">8 Сигналов первопричины (S1..S8):</div>
                    <div className="signals-grid">
                      <SignalBar label="S1 Trace Chain" value={h.signals.trace_chain_score} tooltip="Трассировка сквозной цепочки вызовов" />
                      <SignalBar label="S2 Onset" value={h.signals.onset_score} tooltip="Опережение времени старта инцидента" />
                      <SignalBar label="S3 Lead-Lag" value={h.signals.lead_lag_score} tooltip="Временное опережение кросс-корреляции" />
                      <SignalBar label="S4 Novelty" value={h.signals.novelty_score} tooltip="Новизна шаблона ошибки относительно базы" />
                      <SignalBar label="S5 Causal Dir" value={h.signals.causal_dir_score} tooltip="Причинное направление топологии" />
                      <SignalBar label="S6 Graph Root" value={h.signals.graph_root_score} tooltip="Корень направленного ациклического графа" />
                      <SignalBar label="S7 Localization" value={h.signals.localization_score} tooltip="Локализация на конкретном поде / маршруте" />
                      <SignalBar label="S8 Change Corr" value={h.signals.change_corr_score} tooltip="Корреляция с изменениями и деплоями" />
                    </div>
                  </div>

                  {/* Card Actions */}
                  <div className="hypo-card-actions">
                    <button
                      type="button"
                      className="btn btn-secondary-sm"
                      onClick={(e) => {
                        e.stopPropagation();
                        openContextModal(h.dominant_pod, h.onset_time);
                      }}
                      title="Просмотреть ±20 строк логов вокруг момента сбоя"
                    >
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                        <line x1="8" y1="6" x2="21" y2="6" />
                        <line x1="8" y1="12" x2="21" y2="12" />
                        <line x1="8" y1="18" x2="21" y2="18" />
                        <line x1="3" y1="6" x2="3.01" y2="6" />
                        <line x1="3" y1="12" x2="3.01" y2="12" />
                        <line x1="3" y1="18" x2="3.01" y2="18" />
                      </svg>
                      Контекст логов (±20 строк)
                    </button>
                    {h.dominant_route && (
                      <button
                        type="button"
                        className="btn btn-ghost-sm"
                        onClick={(e) => {
                          e.stopPropagation();
                          void handleSearchChain(h.dominant_route);
                        }}
                      >
                        Цепочка маршрута
                      </button>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        </div>

        {/* Right Column: Visualizations (SVG Topology Graph & SVG Timeline) */}
        <div className="rca-visualizations-column">
          {/* Section A: Pure Vector SVG Topology Graph */}
          <div className="viz-card topology-card">
            <div className="viz-card-header">
              <div className="viz-title-group">
                <h4>Граф зависимостей и вызовов сервисов</h4>
                <span className="text-xs text-muted">
                  {topologyGraph.services.length} сервисов, {topologyGraph.edges.length} связей (чистый векторный SVG)
                </span>
              </div>
              {selectedServiceNode && (
                <button
                  type="button"
                  className="btn-ghost-sm text-xs"
                  onClick={() => setSelectedServiceNode(null)}
                >
                  Сбросить фокус: {selectedServiceNode}
                </button>
              )}
            </div>

            <div className="svg-container">
              <svg
                viewBox={`0 0 ${topologyGraph.width} ${topologyGraph.height}`}
                className="rca-svg-graph"
                preserveAspectRatio="xMidYMid meet"
              >
                <defs>
                  <marker
                    id="arrow-normal"
                    viewBox="0 0 10 10"
                    refX="22"
                    refY="5"
                    markerWidth="6"
                    markerHeight="6"
                    orient="auto-start-reverse"
                  >
                    <path d="M 0 0 L 10 5 L 0 10 z" fill="#3b82f6" />
                  </marker>
                  <marker
                    id="arrow-error"
                    viewBox="0 0 10 10"
                    refX="22"
                    refY="5"
                    markerWidth="6"
                    markerHeight="6"
                    orient="auto-start-reverse"
                  >
                    <path d="M 0 0 L 10 5 L 0 10 z" fill="#ef4444" />
                  </marker>
                </defs>

                {/* Render Edges */}
                {topologyGraph.edges.map((edge: ServiceEdge, idx: number) => {
                  const src = topologyGraph.nodeCoords[edge.caller_service];
                  const dst = topologyGraph.nodeCoords[edge.callee_service];
                  if (!src || !dst) return null;

                  const hasErrors = edge.error_count > 0;
                  const isHighlighted =
                    selectedServiceNode === edge.caller_service || selectedServiceNode === edge.callee_service;

                  const midX = (src.x + dst.x) / 2;
                  const midY = (src.y + dst.y) / 2;

                  return (
                    <g key={`edge-${idx}`} className={`graph-edge ${isHighlighted ? 'edge-highlighted' : ''}`}>
                      <line
                        x1={src.x}
                        y1={src.y}
                        x2={dst.x}
                        y2={dst.y}
                        stroke={hasErrors ? '#ef4444' : '#3b82f6'}
                        strokeWidth={hasErrors ? 2.5 : 1.5}
                        strokeDasharray={hasErrors ? undefined : undefined}
                        opacity={selectedServiceNode && !isHighlighted ? 0.25 : 0.85}
                        markerEnd={hasErrors ? 'url(#arrow-error)' : 'url(#arrow-normal)'}
                      />
                      {/* Edge Label (Calls / Errors) */}
                      <text
                        x={midX}
                        y={midY - 4}
                        fill={hasErrors ? '#fca5a5' : '#94a3b8'}
                        fontSize="9"
                        textAnchor="middle"
                        className="edge-label"
                      >
                        {edge.call_count} вызовов{hasErrors ? ` (${edge.error_count} err)` : ''}
                      </text>
                    </g>
                  );
                })}

                {/* Render Nodes */}
                {topologyGraph.services.map((svc: string) => {
                  const node = topologyGraph.nodeCoords[svc];
                  if (!node) return null;
                  const isRoot = node.role === 'ROOT_CAUSE';
                  const isPropagation = node.role === 'PROPAGATION';
                  const isSymptom = node.role === 'SYMPTOM';
                  const isSelected = selectedServiceNode === svc;

                  let fill = '#1e293b';
                  let stroke = '#3b82f6';
                  if (isRoot) {
                    fill = '#450a0a';
                    stroke = '#ef4444';
                  } else if (isPropagation) {
                    fill = '#431407';
                    stroke = '#f97316';
                  } else if (isSymptom) {
                    fill = '#422006';
                    stroke = '#eab308';
                  }

                  return (
                    <g
                      key={`node-${svc}`}
                      className={`graph-node ${isSelected ? 'node-selected' : ''}`}
                      onClick={() => setSelectedServiceNode((prev) => (prev === svc ? null : svc))}
                      style={{ cursor: 'pointer' }}
                    >
                      {/* Pulse halo for ROOT_CAUSE */}
                      {isRoot && (
                        <circle
                          cx={node.x}
                          cy={node.y}
                          r="28"
                          fill="none"
                          stroke="#ef4444"
                          strokeWidth="2"
                          opacity="0.4"
                          className="pulse-halo"
                        />
                      )}
                      <circle
                        cx={node.x}
                        cy={node.y}
                        r={isRoot ? 22 : 18}
                        fill={fill}
                        stroke={stroke}
                        strokeWidth={isSelected ? 3 : 2}
                      />
                      <text
                        x={node.x}
                        y={node.y + 4}
                        fill="#f8fafc"
                        fontSize={isRoot ? '11' : '10'}
                        fontWeight="600"
                        textAnchor="middle"
                      >
                        {svc.length > 12 ? `${svc.slice(0, 10)}…` : svc}
                      </text>
                      {node.role && (
                        <text
                          x={node.x}
                          y={node.y + (isRoot ? 34 : 30)}
                          fill={stroke}
                          fontSize="8"
                          fontWeight="700"
                          textAnchor="middle"
                        >
                          {node.role}
                        </text>
                      )}
                    </g>
                  );
                })}
              </svg>
            </div>
          </div>

          {/* Section B: Pure Vector SVG Timeline Histogram */}
          <div className="viz-card timeline-card">
            <div className="viz-card-header">
              <div className="viz-title-group">
                <h4>Динамика инцидента во времени (Timeline)</h4>
                <span className="text-xs text-muted">
                  Распределение логов по минутам (макс. {timelineChart.maxEvents} сообщ/мин)
                </span>
              </div>
              <div className="timeline-legend">
                <span className="legend-chip chip-error">● Ошибки (ERROR)</span>
                <span className="legend-chip chip-warn">● Предупреждения (WARN)</span>
                <span className="legend-chip chip-info">● Информация (INFO)</span>
              </div>
            </div>

            <div className="svg-container">
              {timelineChart.bars.length > 0 ? (
                <svg
                  viewBox={`0 0 ${timelineChart.width} ${timelineChart.height}`}
                  className="rca-svg-timeline"
                  preserveAspectRatio="xMidYMid meet"
                >
                  {/* Grid Lines */}
                  <line
                    x1={timelineChart.padX}
                    y1={timelineChart.height - timelineChart.padBottom}
                    x2={timelineChart.width - 15}
                    y2={timelineChart.height - timelineChart.padBottom}
                    stroke="#334155"
                    strokeWidth="1"
                  />
                  <line
                    x1={timelineChart.padX}
                    y1={timelineChart.padTop}
                    x2={timelineChart.width - 15}
                    y2={timelineChart.padTop}
                    stroke="#1e293b"
                    strokeWidth="1"
                    strokeDasharray="4 4"
                  />

                  {/* Y Axis Label */}
                  <text
                    x={timelineChart.padX - 8}
                    y={timelineChart.padTop + 10}
                    fill="#64748b"
                    fontSize="9"
                    textAnchor="end"
                  >
                    {timelineChart.maxEvents}
                  </text>
                  <text
                    x={timelineChart.padX - 8}
                    y={timelineChart.height - timelineChart.padBottom}
                    fill="#64748b"
                    fontSize="9"
                    textAnchor="end"
                  >
                    0
                  </text>

                  {/* Bars */}
                  {timelineChart.bars.map((bar, bIdx) => {
                    let curY = timelineChart.height - timelineChart.padBottom;

                    return (
                      <g key={`bar-${bIdx}`} className="timeline-bar-group">
                        <title>
                          {`${bar.timeStr}\nВсего: ${bar.total}\nОшибок: ${bar.error}\nПредупреждений: ${bar.warn}\nМакс. задержка: ${(bar.maxLat / 1e6).toFixed(1)} мс`}
                        </title>

                        {/* INFO segment */}
                        {bar.infoH > 0 && (
                          <rect
                            x={bar.x}
                            y={(curY -= bar.infoH)}
                            width={bar.barWidth}
                            height={bar.infoH}
                            fill="#3b82f6"
                            rx="1"
                          />
                        )}

                        {/* WARN segment */}
                        {bar.warnH > 0 && (
                          <rect
                            x={bar.x}
                            y={(curY -= bar.warnH)}
                            width={bar.barWidth}
                            height={bar.warnH}
                            fill="#f59e0b"
                            rx="1"
                          />
                        )}

                        {/* ERROR segment */}
                        {bar.errH > 0 && (
                          <rect
                            x={bar.x}
                            y={(curY -= bar.errH)}
                            width={bar.barWidth}
                            height={bar.errH}
                            fill="#ef4444"
                            rx="1"
                          />
                        )}

                        {/* X-Axis Time Label (show every alternate or so) */}
                        {bIdx % Math.ceil(timelineChart.bars.length / 8) === 0 && (
                          <text
                            x={bar.x + bar.barWidth / 2}
                            y={timelineChart.height - timelineChart.padBottom + 16}
                            fill="#94a3b8"
                            fontSize="8.5"
                            textAnchor="middle"
                          >
                            {bar.timeStr}
                          </text>
                        )}
                      </g>
                    );
                  })}
                </svg>
              ) : (
                <div className="empty-hint p-4">Нет данных таймлайна для отображения</div>
              )}
            </div>
          </div>
        </div>
      </div>

      {/* --- Modal 1: Interactive Trace Chain Inspector --- */}
      {showChainModal && (
        <div className="modal-backdrop" onClick={() => setShowChainModal(false)}>
          <div className="modal-content modal-content-wide" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <div>
                <h3>Сквозная цепочка вызовов (Distributed Trace)</h3>
                <span className="text-xs text-muted">Ключ поиска: <strong>{chainKey}</strong></span>
              </div>
              <button type="button" className="btn-close-sm" onClick={() => setShowChainModal(false)}>
                ✕
              </button>
            </div>

            {chainLoading ? (
              <div className="rca-loading-box">
                <span className="spinner-icon">⟳</span> Загрузка событий цепочки...
              </div>
            ) : chainError ? (
              <div className="sidebar-error-toast">{chainError}</div>
            ) : chainEvents && chainEvents.length > 0 ? (
              <div className="chain-events-table-wrapper">
                <table className="chain-table">
                  <thead>
                    <tr>
                      <th>Время</th>
                      <th>Сервис</th>
                      <th>Под</th>
                      <th>Метод / Маршрут</th>
                      <th>Статус</th>
                      <th>Задержка</th>
                      <th>Сообщение / Ошибка</th>
                      <th>Действие</th>
                    </tr>
                  </thead>
                  <tbody>
                    {chainEvents.map((evt: NormalizedEvent, idx: number) => {
                      const isErr = evt.http_status >= 500 || evt.level === 'ERROR';
                      return (
                        <tr key={evt.event_id || idx} className={isErr ? 'row-chain-error' : ''}>
                          <td className="font-mono text-xs">
                            {new Date(evt.ts).toLocaleTimeString([], {
                              hour: '2-digit',
                              minute: '2-digit',
                              second: '2-digit',
                              fractionalSecondDigits: 3,
                            })}
                          </td>
                          <td className="font-semibold text-xs">{evt.service}</td>
                          <td className="font-mono text-xs text-muted">{evt.pod}</td>
                          <td className="text-xs">
                            {evt.http_method && <strong>{evt.http_method} </strong>}
                            <code>{evt.http_route || evt.source_place || '-'}</code>
                          </td>
                          <td>
                            {evt.http_status > 0 ? (
                              <span className={`status-badge ${evt.http_status >= 400 ? 'badge-err' : 'badge-ok'}`}>
                                {evt.http_status}
                              </span>
                            ) : (
                              <span className={`level-badge level-${evt.level?.toLowerCase()}`}>{evt.level}</span>
                            )}
                          </td>
                          <td className="font-mono text-xs">
                            {evt.latency_ns > 0 ? `${(evt.latency_ns / 1e6).toFixed(1)} мс` : '-'}
                          </td>
                          <td className="text-xs message-cell">
                            {evt.error_class && <span className="error-class-tag">{evt.error_class}</span>}
                            <span>{evt.message || evt.raw_redacted}</span>
                          </td>
                          <td>
                            <button
                              type="button"
                              className="btn-icon-xs text-accent"
                              title="Просмотреть контекст ±20 строк"
                              onClick={() => openContextModal(evt.pod, evt.ts)}
                            >
                              контекст
                            </button>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            ) : (
              <div className="empty-hint p-4">По данному ключу события трассировки не найдены</div>
            )}
          </div>
        </div>
      )}

      {/* --- Modal 2: Log Context Inspector (±20 lines) with RawPurged Indication --- */}
      {contextModalOpen && (
        <div className="modal-backdrop" onClick={() => setContextModalOpen(false)}>
          <div className="modal-content modal-content-wide" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <div>
                <h3>Контекст логов (±20 строк)</h3>
                <span className="text-xs text-muted">
                  Под: <strong>{contextPod}</strong> | Время события:{' '}
                  <strong>{new Date(contextTs).toLocaleTimeString()}</strong>
                </span>
              </div>
              <div className="context-mode-switch">
                <button
                  type="button"
                  className={`btn-chip ${contextMode === 'pod' ? 'chip-active' : ''}`}
                  onClick={() => handleModeChange('pod')}
                >
                  Только этот под (pod)
                </button>
                <button
                  type="button"
                  className={`btn-chip ${contextMode === 'all_services' ? 'chip-active' : ''}`}
                  onClick={() => handleModeChange('all_services')}
                >
                  Все сервисы (all_services)
                </button>
                <button type="button" className="btn-close-sm ml-2" onClick={() => setContextModalOpen(false)}>
                  ✕
                </button>
              </div>
            </div>

            {/* RawPurged Banner Indication */}
            {contextData?.raw_purged && (
              <div className="raw-purged-banner">
                <span className="raw-purged-icon">⚠</span>
                <div>
                  <strong>Сырые логи были очищены (RawPurged = true)</strong>
                  <p className="text-xs">
                    Хранилище сырых строк логов очищено для экономии дискового бюджета 15 ГБ. Отображаются сохраненные нормализованные события из агрегатов и отчёта.
                  </p>
                </div>
              </div>
            )}

            {contextLoading ? (
              <div className="rca-loading-box">
                <span className="spinner-icon">⟳</span> Загрузка контекста логов...
              </div>
            ) : contextError ? (
              <div className="sidebar-error-toast">{contextError}</div>
            ) : contextData && contextData.events && contextData.events.length > 0 ? (
              <div className="context-log-terminal">
                <table className="terminal-table">
                  <thead>
                    <tr>
                      <th style={{ width: '45px' }}>#</th>
                      <th style={{ width: '95px' }}>Время</th>
                      <th style={{ width: '110px' }}>Сервис</th>
                      <th style={{ width: '120px' }}>Под</th>
                      <th style={{ width: '60px' }}>Уровень</th>
                      <th>Сообщение / Сырая строка</th>
                    </tr>
                  </thead>
                  <tbody>
                    {contextData.events.map((evt: NormalizedEvent, idx: number) => {
                      const isTarget = evt.ts === contextTs;
                      const lineOffset = idx - Math.floor(contextData.events.length / 2);

                      return (
                        <tr
                          key={evt.event_id || idx}
                          className={`terminal-row ${isTarget ? 'terminal-row-target' : ''} ${evt.level === 'ERROR' ? 'terminal-row-err' : ''}`}
                        >
                          <td className="line-num font-mono">
                            {lineOffset === 0 ? '▶ 0' : lineOffset > 0 ? `+${lineOffset}` : lineOffset}
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
                            <span className={`level-pill level-${evt.level?.toLowerCase()}`}>{evt.level}</span>
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
              <div className="empty-hint p-4">В указанном интервале события контекста не найдены</div>
            )}
          </div>
        </div>
      )}
    </div>
  );
};

// --- Sub-component: Signal Progress Bar ---
interface SignalBarProps {
  label: string;
  value: number;
  tooltip: string;
}

const SignalBar: React.FC<SignalBarProps> = ({ label, value, tooltip }) => {
  const safeVal = Number.isFinite(value) ? Math.max(0, Math.min(1, value)) : 0;
  const pct = (safeVal * 100).toFixed(0);

  return (
    <div className="signal-item" title={`${tooltip}: ${safeVal.toFixed(2)}`}>
      <div className="signal-label-row">
        <span className="signal-name">{label}</span>
        <span className="signal-val font-mono">{safeVal.toFixed(2)}</span>
      </div>
      <div className="signal-track">
        <div
          className="signal-fill"
          style={{
            width: `${pct}%`,
            backgroundColor: safeVal > 0.6 ? '#ef4444' : safeVal > 0.3 ? '#f59e0b' : '#3b82f6',
          }}
        />
      </div>
    </div>
  );
};
