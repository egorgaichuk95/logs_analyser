import React, { useState, useEffect, useMemo, useCallback } from 'react';
import {
  api,
  AnalyzeCaseResponse,
  NormalizedEvent,
  ContextLogsResponse,
  Hypothesis,
  EvidenceItem,
  ClockSkewFinding,
} from '../api/client';

export interface ChainAndContextPageProps {
  caseId: string;
  analysis: AnalyzeCaseResponse | null;
  initialChainKey?: string;
}

// Math and safety helpers
function safeNumber(v: unknown, fallback: number = 0): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : fallback;
}

function formatLatency(ns: number): string {
  const safeNs = safeNumber(ns, 0);
  if (safeNs <= 0) return '-';
  const ms = safeNs / 1_000_000;
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(2)} s`;
  }
  return `${ms.toFixed(1)} ms`;
}

function formatTimestamp(isoStr?: string): string {
  if (!isoStr) return '-';
  try {
    const d = new Date(isoStr);
    if (Number.isNaN(d.getTime())) return isoStr;
    const timePart = d.toLocaleTimeString([], { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' });
    const ms = String(d.getMilliseconds()).padStart(3, '0');
    return `${timePart}.${ms}`;
  } catch {
    return isoStr;
  }
}

type PageSection = 'chain' | 'context';
type TerminalLevelFilter = 'ALL' | 'ERROR' | 'WARN' | 'INFO';

export const ChainAndContextPage: React.FC<ChainAndContextPageProps> = ({
  caseId,
  analysis,
  initialChainKey = '',
}) => {
  // Mode selection: Section A (Chain Trace) or Section B (Logs Context)
  const [activeSection, setActiveSection] = useState<PageSection>('chain');

  // ==========================================
  // Section A: Chain Trace State
  // ==========================================
  const [searchKey, setSearchKey] = useState<string>(initialChainKey);
  const [searchedKey, setSearchedKey] = useState<string | null>(initialChainKey || null);
  const [chainEvents, setChainEvents] = useState<NormalizedEvent[]>([]);
  const [loadingChain, setLoadingChain] = useState<boolean>(false);
  const [chainError, setChainError] = useState<string | null>(null);
  const [chainFilterText, setChainFilterText] = useState<string>('');

  // ==========================================
  // Section B: Context Logs State
  // ==========================================
  const [selectedPod, setSelectedPod] = useState<string>('');
  const [customPodInput, setCustomPodInput] = useState<string>('');
  const [contextTs, setContextTs] = useState<string>('');
  const [contextMode, setContextMode] = useState<'pod' | 'all_services'>('pod');
  const [contextData, setContextData] = useState<ContextLogsResponse | null>(null);
  const [loadingContext, setLoadingContext] = useState<boolean>(false);
  const [contextError, setContextError] = useState<string | null>(null);
  const [terminalFilter, setTerminalFilter] = useState<string>('');
  const [terminalLevel, setTerminalLevel] = useState<TerminalLevelFilter>('ALL');
  const [terminalCopied, setTerminalCopied] = useState<boolean>(false);

  // 1. Collect all known pods from analysis
  const availablePods = useMemo(() => {
    const podSet = new Set<string>();
    if (analysis?.hypotheses) {
      analysis.hypotheses.forEach((h: Hypothesis) => {
        if (h.dominant_pod && h.dominant_pod.trim()) {
          podSet.add(h.dominant_pod.trim());
        }
      });
    }
    if (analysis?.clock_skew_findings) {
      analysis.clock_skew_findings.forEach((f: ClockSkewFinding) => {
        if (f.pod && f.pod.trim()) {
          podSet.add(f.pod.trim());
        }
      });
    }
    chainEvents.forEach((ev: NormalizedEvent) => {
      if (ev.pod && ev.pod.trim()) {
        podSet.add(ev.pod.trim());
      }
    });
    return Array.from(podSet).sort();
  }, [analysis, chainEvents]);

  // Set default pod and ts on mount or analysis change
  useEffect(() => {
    if (!selectedPod && availablePods.length > 0) {
      setSelectedPod(availablePods[0]);
    }
  }, [availablePods, selectedPod]);

  useEffect(() => {
    if (!contextTs && analysis) {
      const rootCause = analysis.hypotheses?.find((h) => h.role === 'ROOT_CAUSE');
      if (rootCause?.onset_time) {
        setContextTs(rootCause.onset_time);
      } else if (analysis.incident_start) {
        setContextTs(analysis.incident_start);
      } else if (analysis.evidence_items && analysis.evidence_items.length > 0) {
        setContextTs(analysis.evidence_items[0].timestamp);
      }
    }
  }, [analysis, contextTs]);

  // 2. Extract potential trace / request / session IDs from analysis
  const exampleKeys = useMemo(() => {
    const keys = new Set<string>();
    const uuidRegex = /\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi;
    const reqRegex = /\b(?:req|trace|sess|txn|corr)-[0-9a-zA-Z_-]{4,32}\b/gi;
    const hexTraceRegex = /\b[0-9a-f]{16,32}\b/gi;

    const scanString = (str?: string) => {
      if (!str) return;
      let match: RegExpExecArray | null;
      while ((match = uuidRegex.exec(str)) !== null) {
        keys.add(match[0]);
      }
      while ((match = reqRegex.exec(str)) !== null) {
        keys.add(match[0]);
      }
      while ((match = hexTraceRegex.exec(str)) !== null) {
        if (!/^\d+$/.test(match[0])) { // skip purely numeric strings
          keys.add(match[0]);
        }
      }
    };

    if (analysis?.evidence_items) {
      analysis.evidence_items.forEach((item: EvidenceItem) => {
        scanString(item.redacted_line);
        scanString(item.summary);
        scanString(item.logql_snippet);
      });
    }

    if (analysis?.hypotheses) {
      analysis.hypotheses.forEach((h: Hypothesis) => {
        scanString(h.template_text);
        scanString(h.explanation);
      });
    }

    // Always ensure at least a couple of typical demo keys if empty
    if (keys.size === 0) {
      keys.add('c7a19e8b-4f90-4e2b-a821-39c0f92b71d4');
      keys.add('req-cascade-9821');
    }

    return Array.from(keys).slice(0, 8);
  }, [analysis]);

  // 3. Search Chain handler
  const handleSearchChain = useCallback(
    async (keyToSearch: string) => {
      const trimmed = keyToSearch.trim();
      if (!trimmed || !caseId) return;

      setLoadingChain(true);
      setChainError(null);
      setSearchedKey(trimmed);

      try {
        const events = await api.getChain(caseId, trimmed);
        setChainEvents(Array.isArray(events) ? events : []);
      } catch (err: unknown) {
        setChainError(err instanceof Error ? err.message : 'Ошибка при получении цепочки запроса');
        setChainEvents([]);
      } finally {
        setLoadingChain(false);
      }
    },
    [caseId]
  );

  // Auto-search if initialChainKey is passed
  useEffect(() => {
    if (initialChainKey && initialChainKey.trim()) {
      setSearchKey(initialChainKey);
      void handleSearchChain(initialChainKey);
    }
  }, [initialChainKey, handleSearchChain]);

  // 4. Fetch Context Logs handler
  const handleFetchContext = useCallback(
    async (podToFetch?: string, tsToFetch?: string, modeToFetch?: 'pod' | 'all_services') => {
      const activePod = podToFetch !== undefined ? podToFetch : selectedPod === '__custom__' ? customPodInput : selectedPod;
      const activeTs = tsToFetch !== undefined ? tsToFetch : contextTs;
      const activeMode = modeToFetch !== undefined ? modeToFetch : contextMode;

      if (!caseId) return;
      if (activeMode === 'pod' && !activePod) {
        setContextError('Выберите или укажите Pod для режима "pod"');
        return;
      }
      if (!activeTs) {
        setContextError('Укажите временную метку (ts) для получения контекста');
        return;
      }

      setLoadingContext(true);
      setContextError(null);

      try {
        const resp = await api.getContextLogs(caseId, activePod, activeTs, activeMode);
        setContextData(resp);
      } catch (err: unknown) {
        setContextError(err instanceof Error ? err.message : 'Ошибка при загрузке контекста логов');
        setContextData(null);
      } finally {
        setLoadingContext(false);
      }
    },
    [caseId, selectedPod, customPodInput, contextTs, contextMode]
  );

  // 5. Jump from Chain Step to Context Logs
  const handleOpenContextForStep = (step: NormalizedEvent) => {
    const targetPod = step.pod || availablePods[0] || '';
    const targetTs = step.ts || step.app_ts || contextTs;

    setSelectedPod(targetPod);
    setContextTs(targetTs);
    setContextMode('pod');
    setActiveSection('context');

    void handleFetchContext(targetPod, targetTs, 'pod');
  };

  // 6. Chain events filtering and metrics
  const filteredChainEvents = useMemo(() => {
    if (!chainFilterText.trim()) return chainEvents;
    const q = chainFilterText.toLowerCase();
    return chainEvents.filter((ev) => {
      return (
        (ev.service && ev.service.toLowerCase().includes(q)) ||
        (ev.pod && ev.pod.toLowerCase().includes(q)) ||
        (ev.level && ev.level.toLowerCase().includes(q)) ||
        (ev.message && ev.message.toLowerCase().includes(q)) ||
        (ev.http_route && ev.http_route.toLowerCase().includes(q)) ||
        (ev.error_class && ev.error_class.toLowerCase().includes(q))
      );
    });
  }, [chainEvents, chainFilterText]);

  const chainMetrics = useMemo(() => {
    if (chainEvents.length === 0) return null;
    const services = new Set<string>();
    let errorCount = 0;
    let minTime = Infinity;
    let maxTime = -Infinity;

    chainEvents.forEach((ev) => {
      if (ev.service) services.add(ev.service);
      const isErr = (ev.http_status && ev.http_status >= 400) || ev.level === 'ERROR' || Boolean(ev.error_class);
      if (isErr) errorCount += 1;
      const t = new Date(ev.ts).getTime();
      if (Number.isFinite(t)) {
        if (t < minTime) minTime = t;
        if (t > maxTime) maxTime = t;
      }
    });

    const totalDurationMs = minTime < Infinity && maxTime > -Infinity ? Math.max(0, maxTime - minTime) : 0;

    return {
      totalSteps: chainEvents.length,
      distinctServices: Array.from(services),
      errorCount,
      durationMs: totalDurationMs,
    };
  }, [chainEvents]);

  // 7. Context Terminal filtering & Target row calculation
  const terminalEvents = useMemo(() => {
    if (!contextData?.events) return [];
    return contextData.events.filter((ev) => {
      if (terminalLevel !== 'ALL' && ev.level !== terminalLevel) {
        return false;
      }
      if (terminalFilter.trim()) {
        const q = terminalFilter.toLowerCase();
        const matchesMsg = (ev.message || '').toLowerCase().includes(q);
        const matchesSvc = (ev.service || '').toLowerCase().includes(q);
        const matchesPod = (ev.pod || '').toLowerCase().includes(q);
        const matchesRaw = (ev.raw_redacted || '').toLowerCase().includes(q);
        return matchesMsg || matchesSvc || matchesPod || matchesRaw;
      }
      return true;
    });
  }, [contextData?.events, terminalLevel, terminalFilter]);

  // Identify index of target row closest to contextTs
  const targetRowEventId = useMemo(() => {
    if (!contextData?.events || contextData.events.length === 0 || !contextTs) return null;
    const targetMs = new Date(contextTs).getTime();
    if (!Number.isFinite(targetMs)) return null;

    let closestId: string | null = null;
    let minDiff = Infinity;

    contextData.events.forEach((ev) => {
      const evMs = new Date(ev.ts).getTime();
      if (Number.isFinite(evMs)) {
        const diff = Math.abs(evMs - targetMs);
        if (diff < minDiff) {
          minDiff = diff;
          closestId = ev.event_id || `${ev.ts}_${ev.service}_${ev.pod}`;
        }
      }
    });

    return closestId;
  }, [contextData?.events, contextTs]);

  // Copy all terminal lines
  const handleCopyTerminal = async () => {
    if (terminalEvents.length === 0) return;
    const text = terminalEvents
      .map((ev, idx) => {
        const ts = formatTimestamp(ev.ts);
        const lvl = (ev.level || 'INFO').padEnd(5);
        const svc = (ev.service || '-').padEnd(16);
        const pod = (ev.pod || '-').padEnd(20);
        const msg = ev.raw_redacted || ev.message || '';
        return `[${String(idx + 1).padStart(2, '0')}] ${ts} | ${lvl} | ${svc} | ${pod} | ${msg}`;
      })
      .join('\n');

    try {
      await navigator.clipboard.writeText(text);
      setTerminalCopied(true);
      setTimeout(() => setTerminalCopied(false), 2500);
    } catch {
      // Fallback
    }
  };

  return (
    <div className="v2-chains-page">
      {/* 1. Header with Mode Tabs */}
      <div className="v2-chains-header">
        <div className="v2-chains-header-info">
          <h2>🔍 Цепочки запросов и Контекст логов ClickHouse</h2>
          <p className="text-xs text-muted">
            Сквозная распределенная трассировка вызовов через сервисы и глубокий инспектор окрестности строк ±20
          </p>
        </div>

        <div className="v2-chains-mode-tabs">
          <button
            type="button"
            className={`v2-chains-mode-tab ${activeSection === 'chain' ? 'active' : ''}`}
            onClick={() => setActiveSection('chain')}
          >
            <span className="mode-tab-icon">🔗</span>
            <span className="mode-tab-label">Сквозная трассировка (Trace Chain)</span>
            {chainEvents.length > 0 && (
              <span className="mode-tab-badge badge-primary">{chainEvents.length}</span>
            )}
          </button>

          <button
            type="button"
            className={`v2-chains-mode-tab ${activeSection === 'context' ? 'active' : ''}`}
            onClick={() => setActiveSection('context')}
          >
            <span className="mode-tab-icon">📜</span>
            <span className="mode-tab-label">Контекст логов ±20 строк</span>
            {contextData?.events && contextData.events.length > 0 && (
              <span className="mode-tab-badge badge-accent">{contextData.events.length}</span>
            )}
          </button>
        </div>
      </div>

      {/* ===================================================================== */}
      {/* SECTION A: Distributed Request Trace (GET /api/v1/cases/{id}/chain) */}
      {/* ===================================================================== */}
      {activeSection === 'chain' && (
        <div className="v2-chain-section-container">
          {/* Search Box Card */}
          <div className="v2-chain-search-card">
            <div className="v2-chain-search-input-row">
              <div className="v2-chain-input-wrapper">
                <span className="v2-chain-input-icon">🔍</span>
                <input
                  type="text"
                  className="v2-chain-search-input"
                  placeholder="Введите trace_id, request_id, session_id или visitor_id..."
                  value={searchKey}
                  onChange={(e) => setSearchKey(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') {
                      void handleSearchChain(searchKey);
                    }
                  }}
                />
                {searchKey && (
                  <button
                    type="button"
                    className="v2-chain-input-clear"
                    onClick={() => setSearchKey('')}
                    title="Очистить"
                  >
                    ✕
                  </button>
                )}
              </div>

              <button
                type="button"
                className="btn btn-primary v2-btn-search-chain"
                disabled={loadingChain || !searchKey.trim()}
                onClick={() => void handleSearchChain(searchKey)}
              >
                {loadingChain ? (
                  <>
                    <span className="spinner-icon">⟳</span> Поиск...
                  </>
                ) : (
                  'Найти цепочку'
                )}
              </button>
            </div>

            {/* Quick Example Key Chips from Analysis */}
            {exampleKeys.length > 0 && (
              <div className="v2-example-keys-row">
                <span className="text-xs text-muted font-semibold">Быстрые примеры из анализа:</span>
                <div className="v2-example-chips-list">
                  {exampleKeys.map((key) => (
                    <button
                      key={key}
                      type="button"
                      className={`v2-example-key-chip ${searchedKey === key ? 'active' : ''}`}
                      onClick={() => {
                        setSearchKey(key);
                        void handleSearchChain(key);
                      }}
                      title={`Искать по ключу ${key}`}
                    >
                      <span className="chip-key-icon">🔑</span>
                      <span className="chip-key-text">{key}</span>
                    </button>
                  ))}
                </div>
              </div>
            )}
          </div>

          {/* Chain Error Message */}
          {chainError && (
            <div className="sidebar-error-toast my-3">
              <span>⚠️ {chainError}</span>
            </div>
          )}

          {/* Results Area */}
          {loadingChain ? (
            <div className="v2-loading-box">
              <span className="spinner-icon">⟳</span> Поиск распределенной цепочки вызовов в ClickHouse...
            </div>
          ) : searchedKey && chainEvents.length === 0 && !chainError ? (
            <div className="v2-empty-state-card">
              <span className="v2-empty-icon">🔎</span>
              <h4>По ключу «{searchedKey}» событий не найдено</h4>
              <p className="v2-empty-hint">
                Убедитесь, что логи содержат <code>trace_id</code>, <code>request_id</code> или <code>session_id</code> с
                данным значением, либо выберите один из быстрых примеров выше.
              </p>
            </div>
          ) : chainEvents.length > 0 ? (
            <div className="v2-chain-results-wrapper">
              {/* Summary Metrics Bar */}
              {chainMetrics && (
                <div className="v2-chain-summary-bar">
                  <div className="v2-summary-stat">
                    <span className="text-xs text-muted">Ключ поиска:</span>
                    <code className="font-mono text-accent font-semibold">{searchedKey}</code>
                  </div>

                  <div className="v2-summary-stat">
                    <span className="text-xs text-muted">Всего шагов:</span>
                    <strong className="text-main">{chainMetrics.totalSteps}</strong>
                  </div>

                  <div className="v2-summary-stat">
                    <span className="text-xs text-muted">Сервисы:</span>
                    <div className="v2-summary-service-pills">
                      {chainMetrics.distinctServices.map((svc) => (
                        <span key={svc} className="v2-mini-service-pill">
                          {svc}
                        </span>
                      ))}
                    </div>
                  </div>

                  <div className="v2-summary-stat">
                    <span className="text-xs text-muted">Ошибок в цепочке:</span>
                    <strong className={chainMetrics.errorCount > 0 ? 'text-danger' : 'text-success'}>
                      {chainMetrics.errorCount}
                    </strong>
                  </div>

                  <div className="v2-summary-stat">
                    <span className="text-xs text-muted">Длительность цепочки:</span>
                    <strong className="text-main">{chainMetrics.durationMs} ms</strong>
                  </div>

                  {/* Filter inside chain */}
                  <div className="v2-chain-filter-box">
                    <input
                      type="text"
                      className="input-text text-xs"
                      placeholder="Фильтр шагов цепочки..."
                      value={chainFilterText}
                      onChange={(e) => setChainFilterText(e.target.value)}
                    />
                  </div>
                </div>
              )}

              {/* Step-by-Step Visual Flow */}
              <div className="v2-chain-steps-flow">
                {filteredChainEvents.map((step, idx) => {
                  const isError =
                    (step.http_status && step.http_status >= 400) ||
                    step.level === 'ERROR' ||
                    Boolean(step.error_class);
                  const isWarn = step.level === 'WARN' || (step.http_status && step.http_status >= 300 && step.http_status < 400);

                  return (
                    <div
                      key={step.event_id || `${step.ts}_${idx}`}
                      className={`v2-chain-step-card ${isError ? 'step-error' : isWarn ? 'step-warn' : 'step-ok'}`}
                    >
                      {/* Step Indicator Connector */}
                      <div className="v2-chain-step-indicator">
                        <div className="v2-step-node">{idx + 1}</div>
                        {idx < filteredChainEvents.length - 1 && <div className="v2-step-line" />}
                      </div>

                      {/* Step Content */}
                      <div className="v2-chain-step-body">
                        {/* Top Line: Service, Pod, Time, Status, Latency, Level */}
                        <div className="v2-chain-step-header">
                          <div className="v2-step-service-group">
                            <span className="v2-step-service-badge">{step.service}</span>
                            <span className="v2-step-pod-badge" title={step.pod}>
                              {step.pod}
                            </span>
                          </div>

                          <div className="v2-step-meta-group">
                            <span className="v2-step-time" title={step.ts}>
                              🕒 {formatTimestamp(step.ts)}
                            </span>

                            {step.http_status > 0 && (
                              <span
                                className={`v2-step-status-badge ${
                                  step.http_status >= 400 ? 'status-err' : 'status-ok'
                                }`}
                              >
                                {step.http_status}
                              </span>
                            )}

                            {step.latency_ns > 0 && (
                              <span className="v2-step-latency" title={`${step.latency_ns} ns`}>
                                ⏱️ {formatLatency(step.latency_ns)}
                              </span>
                            )}

                            <span className={`v2-level-badge level-${(step.level || 'info').toLowerCase()}`}>
                              {step.level || 'INFO'}
                            </span>
                          </div>
                        </div>

                        {/* Middle Line: HTTP Method / Route, Peer Service, Error Class */}
                        {(step.http_route || step.error_class || step.peer_service) && (
                          <div className="v2-chain-step-route-row">
                            {step.http_route && (
                              <div className="v2-step-route-tag">
                                <span className="method-label">{step.http_method || 'HTTP'}</span>
                                <code className="route-path">{step.http_route}</code>
                              </div>
                            )}

                            {step.peer_service && (
                              <span className="v2-peer-tag">
                                → Вызов: <strong>{step.peer_service}</strong>
                              </span>
                            )}

                            {step.error_class && (
                              <span className="v2-error-class-badge" title={step.error_msg}>
                                💥 {step.error_class}
                              </span>
                            )}
                          </div>
                        )}

                        {/* Message / Log text */}
                        <div className="v2-chain-step-message-box">
                          <pre className="v2-chain-step-message">
                            {step.raw_redacted || step.message}
                          </pre>
                        </div>

                        {/* Footer: Jump to Context button */}
                        <div className="v2-chain-step-footer">
                          <div className="v2-step-ids text-xs text-muted">
                            {step.trace_id && <span>Trace: <code>{step.trace_id.slice(0, 12)}…</code></span>}
                            {step.request_id && <span>Req: <code>{step.request_id.slice(0, 12)}…</code></span>}
                            {step.session_id && <span>Sess: <code>{step.session_id.slice(0, 12)}…</code></span>}
                          </div>

                          <button
                            type="button"
                            className="btn btn-secondary-sm v2-btn-open-context"
                            onClick={() => handleOpenContextForStep(step)}
                            title={`Открыть контекст ±20 строк для пода ${step.pod} в момент ${step.ts}`}
                          >
                            📜 Открыть контекст ±20 строк вокруг этого шага →
                          </button>
                        </div>
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          ) : (
            <div className="v2-empty-state-card">
              <span className="v2-empty-icon">🔗</span>
              <h4>Сквозная трассировка запроса (Trace Chain)</h4>
              <p className="v2-empty-hint">
                Введите сквозной идентификатор (например, <code>trace_id</code> или <code>request_id</code>) в поисковую
                строку выше или нажмите на один из предложенных идентификаторов для построения графа прохождения запроса.
              </p>
            </div>
          )}
        </div>
      )}

      {/* ===================================================================== */}
      {/* SECTION B: Context Logs (±20 rows) (GET /api/v1/cases/{id}/logs/context) */}
      {/* ===================================================================== */}
      {activeSection === 'context' && (
        <div className="v2-context-section-container">
          {/* Controls Bar */}
          <div className="v2-context-controls-card">
            <div className="v2-context-controls-grid">
              {/* 1. Pod Selector */}
              <div className="v2-ctrl-field">
                <label className="text-xs font-semibold">Целевой Pod:</label>
                <div className="v2-pod-select-group">
                  <select
                    className="select-input"
                    value={selectedPod}
                    onChange={(e) => setSelectedPod(e.target.value)}
                  >
                    {availablePods.map((p) => (
                      <option key={p} value={p}>
                        {p}
                      </option>
                    ))}
                    <option value="__custom__">✍️ Ввести имя пода вручную...</option>
                  </select>

                  {selectedPod === '__custom__' && (
                    <input
                      type="text"
                      className="input-text text-xs"
                      placeholder="Например: chat-session-7d84bf9-xkl2"
                      value={customPodInput}
                      onChange={(e) => setCustomPodInput(e.target.value)}
                    />
                  )}
                </div>
              </div>

              {/* 2. Timestamp input */}
              <div className="v2-ctrl-field">
                <label className="text-xs font-semibold">Временная метка (ts):</label>
                <div className="v2-ts-input-group">
                  <input
                    type="text"
                    className="input-text font-mono text-xs"
                    placeholder="2026-10-28T14:22:00.000Z"
                    value={contextTs}
                    onChange={(e) => setContextTs(e.target.value)}
                  />
                </div>
              </div>

              {/* 3. Mode Toggle */}
              <div className="v2-ctrl-field">
                <label className="text-xs font-semibold">Режим выборки:</label>
                <div className="v2-mode-segmented">
                  <button
                    type="button"
                    className={`v2-segmented-btn ${contextMode === 'pod' ? 'active' : ''}`}
                    onClick={() => setContextMode('pod')}
                  >
                    📦 Только Pod
                  </button>
                  <button
                    type="button"
                    className={`v2-segmented-btn ${contextMode === 'all_services' ? 'active' : ''}`}
                    onClick={() => setContextMode('all_services')}
                  >
                    🌐 Все сервисы
                  </button>
                </div>
              </div>

              {/* 4. Action Trigger */}
              <div className="v2-ctrl-field v2-ctrl-action">
                <button
                  type="button"
                  className="btn btn-primary v2-btn-fetch-context"
                  disabled={loadingContext}
                  onClick={() => void handleFetchContext()}
                >
                  {loadingContext ? (
                    <>
                      <span className="spinner-icon">⟳</span> Загрузка...
                    </>
                  ) : (
                    '🔍 Загрузить контекст ±20 строк'
                  )}
                </button>
              </div>
            </div>

            {/* Quick Presets Row (OnsetTime & Evidence) */}
            <div className="v2-context-presets-row">
              <span className="text-xs text-muted font-semibold">Быстрые пресеты времени:</span>
              <div className="v2-presets-chips">
                {analysis?.hypotheses
                  ?.filter((h) => Boolean(h.onset_time))
                  .slice(0, 4)
                  .map((h) => (
                    <button
                      key={h.id}
                      type="button"
                      className="v2-preset-chip"
                      onClick={() => {
                        setContextTs(h.onset_time);
                        if (h.dominant_pod) setSelectedPod(h.dominant_pod);
                        void handleFetchContext(h.dominant_pod, h.onset_time);
                      }}
                      title={`Установить время Onset для ${h.service}: ${h.onset_time}`}
                    >
                      <span className="chip-badge">{h.role === 'ROOT_CAUSE' ? '💥 Root' : '📍 Onset'}</span>
                      <span className="chip-svc">{h.service}:</span>
                      <span className="chip-time">{formatTimestamp(h.onset_time)}</span>
                    </button>
                  ))}

                {analysis?.evidence_items?.slice(0, 4).map((e) => (
                  <button
                    key={e.id}
                    type="button"
                    className="v2-preset-chip"
                    onClick={() => {
                      setContextTs(e.timestamp);
                      void handleFetchContext(undefined, e.timestamp);
                    }}
                    title={`Установить время улики ${e.id}: ${e.timestamp}`}
                  >
                    <span className="chip-badge chip-ev">{e.id}</span>
                    <span className="chip-svc">{e.service}:</span>
                    <span className="chip-time">{formatTimestamp(e.timestamp)}</span>
                  </button>
                ))}
              </div>
            </div>
          </div>

          {/* Context Error Toast */}
          {contextError && (
            <div className="sidebar-error-toast my-3">
              <span>⚠️ {contextError}</span>
            </div>
          )}

          {/* Raw Purged Banner */}
          {contextData?.raw_purged && (
            <div className="raw-purged-banner">
              <span className="banner-icon">⚠️</span>
              <div>
                <strong>Сырые логи были очищены (Purge Raw).</strong>
                <p className="text-xs mt-1">
                  Текстовые сырые логи удалены для соблюдения дисковой квоты (15 ГБ). Ниже отображаются сохраненные
                  агрегированные и сэмплированные события из ClickHouse.
                </p>
              </div>
            </div>
          )}

          {/* Terminal Window */}
          <div className="v2-terminal-wrapper">
            {/* Terminal Header Toolbar */}
            <div className="v2-terminal-header">
              <div className="v2-terminal-dots">
                <span className="dot dot-red" />
                <span className="dot dot-yellow" />
                <span className="dot dot-green" />
              </div>

              <div className="v2-terminal-title">
                <span className="term-icon">📟</span>
                <span>
                  Log Terminal:{' '}
                  <strong>{contextMode === 'all_services' ? 'Все сервисы' : selectedPod || 'Pod'}</strong> @{' '}
                  <code className="text-accent">{formatTimestamp(contextTs)}</code>
                </span>
              </div>

              <div className="v2-terminal-toolbar-right">
                {/* Search Filter input */}
                <input
                  type="text"
                  className="v2-terminal-search"
                  placeholder="Фильтр строк по тексту..."
                  value={terminalFilter}
                  onChange={(e) => setTerminalFilter(e.target.value)}
                />

                {/* Level pills */}
                <div className="v2-terminal-level-group">
                  {(['ALL', 'ERROR', 'WARN', 'INFO'] as TerminalLevelFilter[]).map((lvl) => (
                    <button
                      key={lvl}
                      type="button"
                      className={`v2-term-lvl-btn ${terminalLevel === lvl ? 'active' : ''}`}
                      onClick={() => setTerminalLevel(lvl)}
                    >
                      {lvl}
                    </button>
                  ))}
                </div>

                {/* Counter */}
                <span className="text-xs text-muted">
                  Строк: <strong>{terminalEvents.length}</strong>
                </span>

                {/* Copy Terminal Button */}
                <button
                  type="button"
                  className={`btn btn-secondary-sm ${terminalCopied ? 'btn-copied' : ''}`}
                  onClick={() => void handleCopyTerminal()}
                  disabled={terminalEvents.length === 0}
                  title="Скопировать все отображаемые строки терминала"
                >
                  {terminalCopied ? '✓ Скопировано' : '📋 Скопировать'}
                </button>
              </div>
            </div>

            {/* Terminal Body */}
            <div className="v2-terminal-body">
              {loadingContext ? (
                <div className="v2-loading-box">
                  <span className="spinner-icon">⟳</span> Чтение окрестности строк из ClickHouse...
                </div>
              ) : terminalEvents.length === 0 ? (
                <div className="v2-terminal-empty">
                  {contextData ? (
                    <span>В окрестности ±20 строк нет событий, соответствующих заданному фильтру.</span>
                  ) : (
                    <span>
                      Выберите Pod и время события и нажмите кнопку «Загрузить контекст ±20 строк» выше.
                    </span>
                  )}
                </div>
              ) : (
                <div className="v2-terminal-lines-list">
                  {terminalEvents.map((ev, idx) => {
                    const rowKey = ev.event_id || `${ev.ts}_${ev.service}_${ev.pod}_${idx}`;
                    const isTarget = targetRowEventId === rowKey || targetRowEventId === ev.event_id;
                    const isErr = ev.level === 'ERROR' || (ev.http_status && ev.http_status >= 400);
                    const isWarn = ev.level === 'WARN';

                    return (
                      <div
                        key={rowKey}
                        className={`v2-terminal-line ${isTarget ? 'is-target-line' : ''} ${
                          isErr ? 'line-err' : isWarn ? 'line-warn' : ''
                        }`}
                      >
                        {/* Target Marker */}
                        {isTarget && (
                          <div className="v2-target-marker-badge">
                            🎯 ЦЕЛЕВАЯ СТРОКА (TARGET TS)
                          </div>
                        )}

                        <div className="v2-term-line-content">
                          <span className="v2-term-line-num">{String(idx + 1).padStart(2, '0')}</span>
                          <span className="v2-term-time">{formatTimestamp(ev.ts)}</span>

                          <span
                            className={`v2-term-lvl-badge lvl-${(ev.level || 'info').toLowerCase()}`}
                          >
                            {(ev.level || 'INFO').slice(0, 5)}
                          </span>

                          <span className="v2-term-service" title={ev.service}>
                            {ev.service}
                          </span>

                          <span className="v2-term-pod" title={ev.pod}>
                            {ev.pod}
                          </span>

                          {ev.http_status > 0 && (
                            <span
                              className={`v2-term-status ${ev.http_status >= 400 ? 'status-err' : ''}`}
                            >
                              {ev.http_status}
                            </span>
                          )}

                          <span className="v2-term-msg" title={ev.raw_redacted || ev.message}>
                            <code>{ev.raw_redacted || ev.message}</code>
                          </span>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
