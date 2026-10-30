import React, { useState, useMemo } from 'react';
import {
  AnalyzeCaseResponse,
  Hypothesis,
  ServiceEdge,
} from '../api/client';

export interface TopologyAndTimelinePageProps {
  caseId: string;
  analysis: AnalyzeCaseResponse | null;
  onSelectServiceForLogs?: (serviceName: string) => void;
}

// Math safety helpers
function safeNumber(v: unknown, fallback: number = 0): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : fallback;
}

type EdgeFilter = 'ALL' | 'ERRORS_ONLY';

export const TopologyAndTimelinePage: React.FC<TopologyAndTimelinePageProps> = ({
  caseId: _caseId,
  analysis,
  onSelectServiceForLogs,
}) => {
  // Graph controls
  const [zoom, setZoom] = useState<number>(1.0);
  const [pan, setPan] = useState<{ x: number; y: number }>({ x: 0, y: 0 });
  const [edgeFilter, setEdgeFilter] = useState<EdgeFilter>('ALL');
  const [selectedService, setSelectedService] = useState<string | null>(null);

  // Timeline controls
  const [timelineServiceFilter, setTimelineServiceFilter] = useState<string>('ALL');
  const [selectedBucketKey, setSelectedBucketKey] = useState<string | null>(null);

  const hypotheses = useMemo(() => analysis?.hypotheses ?? [], [analysis?.hypotheses]);
  const serviceEdges = useMemo(() => analysis?.service_edges ?? [], [analysis?.service_edges]);
  const timelinePoints = useMemo(() => analysis?.timeline ?? [], [analysis?.timeline]);

  // Service role map (takes highest role if multiple hypotheses)
  const serviceRoleMap = useMemo(() => {
    const map = new Map<string, Hypothesis['role']>();
    const priority: Record<Hypothesis['role'], number> = {
      ROOT_CAUSE: 4,
      PROPAGATION: 3,
      SYMPTOM: 2,
      NOISE: 1,
    };

    hypotheses.forEach((h) => {
      const cur = map.get(h.service);
      if (!cur || priority[h.role] > priority[cur]) {
        map.set(h.service, h.role);
      }
    });
    return map;
  }, [hypotheses]);

  // Extract all unique service names across hypotheses and edges
  const allServices = useMemo(() => {
    const set = new Set<string>();
    hypotheses.forEach((h) => set.add(h.service));
    serviceEdges.forEach((e) => {
      if (e.caller_service) set.add(e.caller_service);
      if (e.callee_service) set.add(e.callee_service);
    });
    timelinePoints.forEach((pt) => {
      if (pt.service) set.add(pt.service);
    });
    return Array.from(set).sort();
  }, [hypotheses, serviceEdges, timelinePoints]);

  // Filtered edges
  const filteredEdges = useMemo(() => {
    if (edgeFilter === 'ERRORS_ONLY') {
      return serviceEdges.filter((e) => safeNumber(e.error_count) > 0);
    }
    return serviceEdges;
  }, [serviceEdges, edgeFilter]);

  // Topology layout calculation
  const topologyLayout = useMemo(() => {
    const width = 960;
    const height = 480;
    const cx = width / 2;
    const cy = height / 2;
    const n = allServices.length;

    // Radius adapted to number of services
    const radius = Math.min(cx, cy) * (n > 8 ? 0.76 : n > 4 ? 0.68 : 0.55);

    const coords: Record<
      string,
      { x: number; y: number; role?: Hypothesis['role']; isRoot: boolean }
    > = {};

    allServices.forEach((svc, idx) => {
      if (n === 1) {
        coords[svc] = {
          x: cx,
          y: cy,
          role: serviceRoleMap.get(svc),
          isRoot: serviceRoleMap.get(svc) === 'ROOT_CAUSE',
        };
        return;
      }
      const angle = (2 * Math.PI * idx) / (n || 1) - Math.PI / 2;
      const x = cx + radius * Math.cos(angle);
      const y = cy + radius * Math.sin(angle);
      const role = serviceRoleMap.get(svc);
      coords[svc] = {
        x: Number.isFinite(x) ? x : cx,
        y: Number.isFinite(y) ? y : cy,
        role,
        isRoot: role === 'ROOT_CAUSE',
      };
    });

    return { width, height, coords };
  }, [allServices, serviceRoleMap]);

  // Inspector details for selected service
  const selectedServiceDetails = useMemo(() => {
    if (!selectedService) return null;

    const role = serviceRoleMap.get(selectedService) ?? 'NOISE';
    const relatedHypotheses = hypotheses.filter((h) => h.service === selectedService);

    // Incoming edges (where this service is callee)
    const incoming = serviceEdges.filter((e) => e.callee_service === selectedService);
    // Outgoing edges (where this service is caller)
    const outgoing = serviceEdges.filter((e) => e.caller_service === selectedService);

    let totalCalls = 0;
    let totalErrors = 0;
    incoming.forEach((e) => {
      totalCalls += safeNumber(e.call_count);
      totalErrors += safeNumber(e.error_count);
    });
    outgoing.forEach((e) => {
      totalCalls += safeNumber(e.call_count);
      totalErrors += safeNumber(e.error_count);
    });

    return {
      name: selectedService,
      role,
      relatedHypotheses,
      incoming,
      outgoing,
      totalCalls,
      totalErrors,
    };
  }, [selectedService, serviceRoleMap, hypotheses, serviceEdges]);

  // Timeline data processing
  const timelineData = useMemo(() => {
    const width = 960;
    const height = 340;
    const padLeft = 60;
    const padRight = 30;
    const padTop = 25;
    const padBottom = 45;
    const chartW = width - padLeft - padRight;
    const chartH = height - padTop - padBottom;

    const filteredPoints =
      timelineServiceFilter === 'ALL'
        ? timelinePoints
        : timelinePoints.filter((pt) => pt.service === timelineServiceFilter);

    if (filteredPoints.length === 0) {
      return {
        width,
        height,
        padLeft,
        padRight,
        padTop,
        padBottom,
        chartW,
        chartH,
        bars: [],
        maxEvents: 0,
        maxLatencyNs: 0,
        uniqueServices: Array.from(new Set(timelinePoints.map((p) => p.service))).sort(),
      };
    }

    // Group by bucket timestamp
    const bucketMap = new Map<
      string,
      {
        bucket: string;
        timeStr: string;
        dateStr: string;
        error: number;
        warn: number;
        info: number;
        total: number;
        maxLat: number;
      }
    >();

    filteredPoints.forEach((pt) => {
      const bKey = pt.bucket;
      const existing = bucketMap.get(bKey) ?? {
        bucket: bKey,
        timeStr: new Date(bKey).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
        dateStr: new Date(bKey).toLocaleDateString(),
        error: 0,
        warn: 0,
        info: 0,
        total: 0,
        maxLat: 0,
      };

      const count = safeNumber(pt.events_count);
      const lvl = (pt.level || '').toUpperCase();

      if (lvl === 'ERROR' || lvl === 'FATAL') {
        existing.error += count;
      } else if (lvl === 'WARN' || lvl === 'WARNING') {
        existing.warn += count;
      } else {
        existing.info += count;
      }
      existing.total += count;

      const lat = safeNumber(pt.max_latency_ns);
      if (lat > existing.maxLat) {
        existing.maxLat = lat;
      }

      bucketMap.set(bKey, existing);
    });

    const sortedBuckets = Array.from(bucketMap.values()).sort(
      (a, b) => new Date(a.bucket).getTime() - new Date(b.bucket).getTime()
    );

    let maxEvents = 1;
    let maxLatencyNs = 1;
    sortedBuckets.forEach((b) => {
      if (b.total > maxEvents) maxEvents = b.total;
      if (b.maxLat > maxLatencyNs) maxLatencyNs = b.maxLat;
    });

    const nBuckets = Math.max(1, sortedBuckets.length);
    const stepX = chartW / nBuckets;
    const barWidth = Math.max(6, Math.min(32, stepX * 0.72));

    const bars = sortedBuckets.map((b, idx) => {
      const x = padLeft + idx * stepX + (stepX - barWidth) / 2;
      const totalH = (b.total / maxEvents) * chartH;
      const errH = (b.error / maxEvents) * chartH;
      const warnH = (b.warn / maxEvents) * chartH;
      const infoH = (b.info / maxEvents) * chartH;

      // Latency point coordinate (normalized to chart height)
      const latNormalized = (b.maxLat / maxLatencyNs) * chartH;
      const latY = height - padBottom - latNormalized;

      return {
        ...b,
        x,
        barWidth,
        totalH,
        errH,
        warnH,
        infoH,
        latY,
        centerX: x + barWidth / 2,
      };
    });

    return {
      width,
      height,
      padLeft,
      padRight,
      padTop,
      padBottom,
      chartW,
      chartH,
      bars,
      maxEvents,
      maxLatencyNs,
      uniqueServices: Array.from(new Set(timelinePoints.map((p) => p.service))).sort(),
    };
  }, [timelinePoints, timelineServiceFilter]);

  // Selected bucket details
  const selectedBucket = useMemo(() => {
    if (!selectedBucketKey) return null;
    return timelineData.bars.find((b) => b.bucket === selectedBucketKey) ?? null;
  }, [selectedBucketKey, timelineData.bars]);

  // Zoom handlers
  const handleZoomIn = () => setZoom((z) => Math.min(3.0, z + 0.2));
  const handleZoomOut = () => setZoom((z) => Math.max(0.4, z - 0.2));
  const handleResetZoom = () => {
    setZoom(1.0);
    setPan({ x: 0, y: 0 });
    setSelectedService(null);
  };

  // Node click handler
  const handleNodeClick = (svc: string) => {
    setSelectedService((prev) => (prev === svc ? null : svc));
  };

  return (
    <div className="v2-topology-page-container">
      {/* =========================================================================
          ВЕРХНИЙ БЛОК (460px+): Интерактивный векторный SVG-граф вызовов сервисов
          ========================================================================= */}
      <div className="v2-topology-block">
        <div className="v2-topo-header">
          <div className="v2-topo-title-group">
            <h3>🕸️ Граф зависимостей и вызовов сервисов</h3>
            <span className="text-xs text-muted">
              Узлы: <strong>{allServices.length}</strong> сервисов | Связи:{' '}
              <strong>{filteredEdges.length}</strong> вызовов (векторный SVG)
            </span>
          </div>

          <div className="v2-topo-toolbar">
            {/* Фильтр рёбер */}
            <div className="v2-topo-edge-filters">
              <button
                type="button"
                className={`btn-chip ${edgeFilter === 'ALL' ? 'btn-chip-active' : ''}`}
                onClick={() => setEdgeFilter('ALL')}
              >
                Все вызовы ({serviceEdges.length})
              </button>
              <button
                type="button"
                className={`btn-chip ${edgeFilter === 'ERRORS_ONLY' ? 'btn-chip-active' : ''}`}
                onClick={() => setEdgeFilter('ERRORS_ONLY')}
              >
                Только с ошибками ({serviceEdges.filter((e) => e.error_count > 0).length})
              </button>
            </div>

            {/* Зум контролы */}
            <div className="v2-zoom-controls">
              <button
                type="button"
                className="btn-icon"
                onClick={handleZoomIn}
                title="Увеличить масштаб (Zoom In)"
              >
                🔍+
              </button>
              <button
                type="button"
                className="btn-icon"
                onClick={handleZoomOut}
                title="Уменьшить масштаб (Zoom Out)"
              >
                🔍-
              </button>
              <button
                type="button"
                className="btn-icon text-xs"
                onClick={handleResetZoom}
                title="Сбросить масштаб и позицию"
              >
                🔄 {Math.round(zoom * 100)}%
              </button>
            </div>
          </div>
        </div>

        {/* Рабочая область графа с боковым инспектором */}
        <div className="v2-topo-canvas-wrapper">
          <div className="v2-svg-graph-container">
            {allServices.length === 0 ? (
              <div className="v2-empty-state-card m-4">
                <div className="text-muted" style={{ fontSize: '2rem' }}>🕸️</div>
                <p className="mt-1 font-semibold">Нет данных о связях между сервисами</p>
                <p className="text-muted text-xs mt-1">
                  Запустите анализ логов для построения топологии сетевых вызовов.
                </p>
              </div>
            ) : (
              <svg
                viewBox={`0 0 ${topologyLayout.width} ${topologyLayout.height}`}
                className="v2-svg-graph"
                preserveAspectRatio="xMidYMid meet"
              >
                <defs>
                  {/* Стрелка для обычных вызовов */}
                  <marker
                    id="v2-arrow-normal"
                    viewBox="0 0 10 10"
                    refX="22"
                    refY="5"
                    markerWidth="6"
                    markerHeight="6"
                    orient="auto-start-reverse"
                  >
                    <path d="M 0 0 L 10 5 L 0 10 z" fill="#3b82f6" />
                  </marker>

                  {/* Стрелка для вызовов с ошибками */}
                  <marker
                    id="v2-arrow-error"
                    viewBox="0 0 10 10"
                    refX="22"
                    refY="5"
                    markerWidth="7"
                    markerHeight="7"
                    orient="auto-start-reverse"
                  >
                    <path d="M 0 0 L 10 5 L 0 10 z" fill="#ef4444" />
                  </marker>
                </defs>

                <g transform={`translate(${pan.x}, ${pan.y}) scale(${zoom})`}>
                  {/* Рёбра графа (Service Edges) */}
                  {filteredEdges.map((edge: ServiceEdge, idx: number) => {
                    const src = topologyLayout.coords[edge.caller_service];
                    const dst = topologyLayout.coords[edge.callee_service];
                    if (!src || !dst) return null;

                    const hasErrors = safeNumber(edge.error_count) > 0;
                    const isRelated =
                      selectedService === edge.caller_service ||
                      selectedService === edge.callee_service;
                    const isDimmed = selectedService && !isRelated;

                    // Calculate quadratic bezier curve offset to prevent bidirectional overlap
                    const dx = dst.x - src.x;
                    const dy = dst.y - src.y;
                    const dist = Math.sqrt(dx * dx + dy * dy) || 1;
                    const normalX = -dy / dist;
                    const normalY = dx / dist;

                    // Curved offset
                    const offset = 22;
                    const midX = (src.x + dst.x) / 2 + normalX * offset;
                    const midY = (src.y + dst.y) / 2 + normalY * offset;

                    return (
                      <g
                        key={`edge-${idx}`}
                        className={`v2-graph-edge ${isRelated ? 'edge-highlighted' : ''}`}
                        opacity={isDimmed ? 0.18 : 0.9}
                      >
                        <path
                          d={`M ${src.x} ${src.y} Q ${midX} ${midY} ${dst.x} ${dst.y}`}
                          fill="none"
                          stroke={hasErrors ? '#ef4444' : '#3b82f6'}
                          strokeWidth={hasErrors ? 2.8 : 1.6}
                          markerEnd={
                            hasErrors ? 'url(#v2-arrow-error)' : 'url(#v2-arrow-normal)'
                          }
                        />
                        <text
                          x={midX}
                          y={midY - 4}
                          fill={hasErrors ? '#fca5a5' : '#94a3b8'}
                          fontSize="9"
                          fontWeight={hasErrors ? '700' : '500'}
                          textAnchor="middle"
                          className="v2-edge-label"
                        >
                          {edge.call_count}
                          {hasErrors ? ` (${edge.error_count} err)` : ''}
                        </text>
                      </g>
                    );
                  })}

                  {/* Узлы графа (Service Nodes) */}
                  {allServices.map((svc: string) => {
                    const node = topologyLayout.coords[svc];
                    if (!node) return null;

                    const isRoot = node.role === 'ROOT_CAUSE';
                    const isProp = node.role === 'PROPAGATION';
                    const isSymp = node.role === 'SYMPTOM';
                    const isSelected = selectedService === svc;
                    const isDimmed = selectedService && !isSelected;

                    let fill = '#1e293b';
                    let stroke = '#3b82f6';
                    if (isRoot) {
                      fill = '#450a0a';
                      stroke = '#ef4444';
                    } else if (isProp) {
                      fill = '#431407';
                      stroke = '#f97316';
                    } else if (isSymp) {
                      fill = '#422006';
                      stroke = '#eab308';
                    }

                    const nodeRadius = isRoot ? 26 : 22;

                    return (
                      <g
                        key={`node-${svc}`}
                        className={`v2-graph-node ${isSelected ? 'node-selected' : ''}`}
                        onClick={() => handleNodeClick(svc)}
                        opacity={isDimmed ? 0.35 : 1.0}
                        style={{ cursor: 'pointer' }}
                      >
                        {/* Анимированный ореол для первопричины (ROOT_CAUSE) */}
                        {isRoot && (
                          <circle
                            cx={node.x}
                            cy={node.y}
                            r={nodeRadius + 10}
                            fill="none"
                            stroke="#ef4444"
                            strokeWidth="2.5"
                            opacity="0.5"
                            className="pulse-halo"
                          />
                        )}

                        {/* Основной круг узла */}
                        <circle
                          cx={node.x}
                          cy={node.y}
                          r={nodeRadius}
                          fill={fill}
                          stroke={stroke}
                          strokeWidth={isSelected ? 3.5 : 2}
                        />

                        {/* Название сервиса */}
                        <text
                          x={node.x}
                          y={node.y + 4}
                          fill="#f8fafc"
                          fontSize={isRoot ? '11' : '10'}
                          fontWeight="700"
                          textAnchor="middle"
                        >
                          {svc.length > 14 ? `${svc.slice(0, 12)}…` : svc}
                        </text>

                        {/* Метка роли под кругом */}
                        {node.role && (
                          <text
                            x={node.x}
                            y={node.y + nodeRadius + 14}
                            fill={stroke}
                            fontSize="8.5"
                            fontWeight="700"
                            textAnchor="middle"
                          >
                            {node.role}
                          </text>
                        )}
                      </g>
                    );
                  })}
                </g>
              </svg>
            )}
          </div>

          {/* Боковой инспектор выбранного сервиса */}
          {selectedServiceDetails && (
            <div className="v2-service-inspector-panel">
              <div className="v2-inspector-header">
                <div>
                  <span className="text-xs text-muted">Инспектор сервиса</span>
                  <h4>{selectedServiceDetails.name}</h4>
                </div>
                <button
                  type="button"
                  className="btn-close-sm"
                  onClick={() => setSelectedService(null)}
                  title="Закрыть инспектор"
                >
                  ✕
                </button>
              </div>

              <div className="v2-inspector-body">
                {/* Роль и метрики */}
                <div className="v2-inspector-role-row">
                  <span
                    className={`role-badge ${
                      selectedServiceDetails.role === 'ROOT_CAUSE'
                        ? 'badge-role-root-cause'
                        : selectedServiceDetails.role === 'PROPAGATION'
                        ? 'badge-role-propagation'
                        : selectedServiceDetails.role === 'SYMPTOM'
                        ? 'badge-role-symptom'
                        : 'badge-role-noise'
                    }`}
                  >
                    {selectedServiceDetails.role}
                  </span>
                  <span className="text-xs text-muted">
                    Всего вызовов: <strong>{selectedServiceDetails.totalCalls}</strong> | Ошибок:{' '}
                    <strong className="text-danger">{selectedServiceDetails.totalErrors}</strong>
                  </span>
                </div>

                {/* Связанные гипотезы */}
                <div className="v2-inspector-section">
                  <span className="v2-inspector-sec-title">
                    Гипотезы RCA ({selectedServiceDetails.relatedHypotheses.length})
                  </span>
                  {selectedServiceDetails.relatedHypotheses.length > 0 ? (
                    <div className="v2-inspector-hypo-list">
                      {selectedServiceDetails.relatedHypotheses.map((h) => (
                        <div key={h.id} className="v2-inspector-hypo-item">
                          <div className="v2-hypo-item-top">
                            <span className="font-semibold text-xs">#{h.rank}</span>
                            <span className="text-xs text-muted">
                              Уверенность: {(h.confidence * 100).toFixed(1)}%
                            </span>
                          </div>
                          <div className="font-mono text-xs text-truncate text-cyan" title={h.source_place}>
                            {h.source_place}
                          </div>
                          <div className="text-xs text-muted text-truncate" title={h.template_text}>
                            {h.template_text}
                          </div>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <div className="text-xs text-muted">Прямых аномалий по сервису не обнаружено</div>
                  )}
                </div>

                {/* Входящие вызовы */}
                <div className="v2-inspector-section">
                  <span className="v2-inspector-sec-title">
                    Входящие вызовы ({selectedServiceDetails.incoming.length})
                  </span>
                  {selectedServiceDetails.incoming.length > 0 ? (
                    <div className="v2-edges-list">
                      {selectedServiceDetails.incoming.map((e, idx) => (
                        <div key={idx} className="v2-edge-item">
                          <span className="text-xs font-semibold">{e.caller_service}</span>
                          <span className="v2-edge-arrow">→</span>
                          <span className="text-xs text-muted">
                            {e.call_count} вызовов
                            {e.error_count > 0 && (
                              <strong className="text-danger"> ({e.error_count} err)</strong>
                            )}
                          </span>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <div className="text-xs text-muted">Входящие вызовы отсутствуют</div>
                  )}
                </div>

                {/* Исходящие вызовы */}
                <div className="v2-inspector-section">
                  <span className="v2-inspector-sec-title">
                    Исходящие вызовы ({selectedServiceDetails.outgoing.length})
                  </span>
                  {selectedServiceDetails.outgoing.length > 0 ? (
                    <div className="v2-edges-list">
                      {selectedServiceDetails.outgoing.map((e, idx) => (
                        <div key={idx} className="v2-edge-item">
                          <span className="text-xs font-semibold">{e.callee_service}</span>
                          <span className="v2-edge-arrow">←</span>
                          <span className="text-xs text-muted">
                            {e.call_count} вызовов
                            {e.error_count > 0 && (
                              <strong className="text-danger"> ({e.error_count} err)</strong>
                            )}
                          </span>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <div className="text-xs text-muted">Исходящие вызовы отсутствуют</div>
                  )}
                </div>

                {/* Кнопка перехода к логам сервиса */}
                {onSelectServiceForLogs && (
                  <div className="mt-2">
                    <button
                      type="button"
                      className="btn btn-primary-sm w-full"
                      onClick={() => onSelectServiceForLogs(selectedServiceDetails.name)}
                    >
                      📜 Смотреть логи {selectedServiceDetails.name}
                    </button>
                  </div>
                )}
              </div>
            </div>
          )}
        </div>
      </div>

      {/* =========================================================================
          НИЖНИЙ БЛОК (340px+): Векторный SVG-таймлайн интенсивности событий (Timeline)
          ========================================================================= */}
      <div className="v2-timeline-block">
        <div className="v2-timeline-header">
          <div className="v2-timeline-title-group">
            <h3>⏱️ Векторный таймлайн интенсивности событий</h3>
            <span className="text-xs text-muted">
              Распределение ошибок и предупреждений во времени (макс.{' '}
              <strong>{timelineData.maxEvents}</strong> соб/корзина)
            </span>
          </div>

          <div className="v2-timeline-toolbar">
            {/* Фильтр по сервисам */}
            <div className="v2-timeline-filter-box">
              <label className="text-xs text-muted">Фильтр по сервису:</label>
              <select
                className="v2-select text-xs"
                value={timelineServiceFilter}
                onChange={(e) => setTimelineServiceFilter(e.target.value)}
              >
                <option value="ALL">Все сервисы ({allServices.length})</option>
                {timelineData.uniqueServices.map((svc) => (
                  <option key={svc} value={svc}>
                    {svc}
                  </option>
                ))}
              </select>
            </div>

            {/* Легенда уровней */}
            <div className="v2-timeline-legend">
              <span className="legend-chip chip-error">● Ошибки (ERROR)</span>
              <span className="legend-chip chip-warn">● Предупреждения (WARN)</span>
              <span className="legend-chip chip-info">● Информация (INFO)</span>
              <span className="legend-chip text-cyan">━ Пиковая задержка</span>
            </div>
          </div>
        </div>

        {/* SVG График таймлайна */}
        <div className="v2-timeline-svg-wrapper">
          {timelineData.bars.length === 0 ? (
            <div className="v2-empty-state-card m-4">
              <div className="text-muted" style={{ fontSize: '2rem' }}>⏱️</div>
              <p className="mt-1 font-semibold">Нет событий таймлайна для выбранного сервиса</p>
              <p className="text-muted text-xs mt-1">
                Выберите другой сервис или сбросьте фильтр на «Все сервисы».
              </p>
            </div>
          ) : (
            <svg
              viewBox={`0 0 ${timelineData.width} ${timelineData.height}`}
              className="v2-svg-timeline"
              preserveAspectRatio="xMidYMid meet"
            >
              {/* Сетка координат */}
              <line
                x1={timelineData.padLeft}
                y1={timelineData.height - timelineData.padBottom}
                x2={timelineData.width - timelineData.padRight}
                y2={timelineData.height - timelineData.padBottom}
                stroke="#334155"
                strokeWidth="1"
              />
              <line
                x1={timelineData.padLeft}
                y1={timelineData.padTop}
                x2={timelineData.width - timelineData.padRight}
                y2={timelineData.padTop}
                stroke="#1e293b"
                strokeWidth="1"
                strokeDasharray="4 4"
              />
              <line
                x1={timelineData.padLeft}
                y1={timelineData.padTop + timelineData.chartH / 2}
                x2={timelineData.width - timelineData.padRight}
                y2={timelineData.padTop + timelineData.chartH / 2}
                stroke="#1e293b"
                strokeWidth="1"
                strokeDasharray="4 4"
              />

              {/* Метки оси Y (события) */}
              <text
                x={timelineData.padLeft - 8}
                y={timelineData.padTop + 8}
                fill="#64748b"
                fontSize="9"
                textAnchor="end"
              >
                {timelineData.maxEvents}
              </text>
              <text
                x={timelineData.padLeft - 8}
                y={timelineData.padTop + timelineData.chartH / 2 + 4}
                fill="#64748b"
                fontSize="9"
                textAnchor="end"
              >
                {Math.round(timelineData.maxEvents / 2)}
              </text>
              <text
                x={timelineData.padLeft - 8}
                y={timelineData.height - timelineData.padBottom}
                fill="#64748b"
                fontSize="9"
                textAnchor="end"
              >
                0
              </text>

              {/* Столбчатая гистограмма (Stacked Bars) */}
              {timelineData.bars.map((bar, idx) => {
                let curY = timelineData.height - timelineData.padBottom;
                const isSelected = selectedBucketKey === bar.bucket;

                return (
                  <g
                    key={`bar-${idx}`}
                    className={`v2-timeline-bar-group ${isSelected ? 'bar-selected' : ''}`}
                    onClick={() =>
                      setSelectedBucketKey((prev) => (prev === bar.bucket ? null : bar.bucket))
                    }
                    style={{ cursor: 'pointer' }}
                  >
                    <title>
                      {`${bar.dateStr} ${bar.timeStr}\nВсего: ${bar.total}\nОшибок: ${bar.error}\nПредупреждений: ${bar.warn}\nИнфо: ${bar.info}\nМакс. задержка: ${(bar.maxLat / 1e6).toFixed(1)} мс`}
                    </title>

                    {/* Фоновая область клика */}
                    <rect
                      x={bar.x - 2}
                      y={timelineData.padTop}
                      width={bar.barWidth + 4}
                      height={timelineData.chartH}
                      fill={isSelected ? 'rgba(59, 130, 246, 0.15)' : 'transparent'}
                      rx="2"
                    />

                    {/* INFO сегмент */}
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

                    {/* WARN сегмент */}
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

                    {/* ERROR сегмент */}
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

                    {/* Подпись времени по оси X */}
                    {idx % Math.ceil(timelineData.bars.length / 9) === 0 && (
                      <text
                        x={bar.centerX}
                        y={timelineData.height - timelineData.padBottom + 16}
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

              {/* Линия тренда пиковой латентности */}
              {timelineData.bars.length > 1 && (
                <path
                  d={timelineData.bars
                    .map((b, i) => `${i === 0 ? 'M' : 'L'} ${b.centerX} ${b.latY}`)
                    .join(' ')}
                  fill="none"
                  stroke="#06b6d4"
                  strokeWidth="1.8"
                  strokeDasharray="3 2"
                  opacity="0.8"
                />
              )}

              {/* Точки пиковой латентности */}
              {timelineData.bars.map((b, i) => (
                <circle
                  key={`lat-dot-${i}`}
                  cx={b.centerX}
                  cy={b.latY}
                  r="2.5"
                  fill="#06b6d4"
                  opacity="0.9"
                />
              ))}
            </svg>
          )}
        </div>

        {/* Информационная карточка выбранной временной корзины */}
        {selectedBucket && (
          <div className="v2-bucket-detail-banner">
            <div className="v2-b-meta">
              <span className="text-xs text-muted">Выбранная корзина:</span>
              <strong>
                {selectedBucket.dateStr} {selectedBucket.timeStr}
              </strong>
            </div>

            <div className="v2-b-stats">
              <span className="v2-b-stat">
                Всего событий: <strong>{selectedBucket.total}</strong>
              </span>
              <span className="v2-b-stat text-danger">
                Ошибок: <strong>{selectedBucket.error}</strong>
              </span>
              <span className="v2-b-stat text-amber">
                Предупреждений: <strong>{selectedBucket.warn}</strong>
              </span>
              <span className="v2-b-stat text-cyan">
                Макс. задержка: <strong>{(selectedBucket.maxLat / 1e6).toFixed(1)} мс</strong>
              </span>
            </div>

            <button
              type="button"
              className="btn-close-sm ml-auto"
              onClick={() => setSelectedBucketKey(null)}
              title="Закрыть детали"
            >
              ✕
            </button>
          </div>
        )}
      </div>
    </div>
  );
};

export default TopologyAndTimelinePage;
