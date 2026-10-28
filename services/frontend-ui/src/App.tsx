import React, { useState, useEffect, useCallback } from 'react';
import { api, CaseMeta, AnalyzeCaseResponse } from './api/client';
import { StorageHeader } from './components/StorageHeader';
import { CaseSidebar } from './components/CaseSidebar';
import { RcaDashboard } from './components/RcaDashboard';
import { EvidenceAndGeminiPanel } from './components/EvidenceAndGeminiPanel';

export const App: React.FC = () => {
  // Selected Case
  const [activeCase, setActiveCase] = useState<CaseMeta | null>(null);

  // Analysis result
  const [analysis, setAnalysis] = useState<AnalyzeCaseResponse | null>(null);
  const [loadingAnalysis, setLoadingAnalysis] = useState<boolean>(false);
  const [analysisError, setAnalysisError] = useState<string | null>(null);

  // Active Tab: 'rca' | 'evidence' | 'explorer'
  const [activeTab, setActiveTab] = useState<'rca' | 'evidence' | 'explorer'>('rca');

  // Log Explorer Search state (tab 3)
  const [explorerQuery, setExplorerQuery] = useState<string>('');
  const [explorerKey, setExplorerKey] = useState<string>('');

  // 1. Load latest analysis when active case changes
  const loadCaseAnalysis = useCallback(async (caseId: string) => {
    setLoadingAnalysis(true);
    setAnalysisError(null);
    try {
      const data = await api.getAnalysis(caseId);
      setAnalysis(data);
    } catch {
      // If analysis not found yet, set null gracefully
      setAnalysis(null);
    } finally {
      setLoadingAnalysis(false);
    }
  }, []);

  useEffect(() => {
    if (activeCase?.id) {
      void loadCaseAnalysis(activeCase.id);
    } else {
      setAnalysis(null);
    }
  }, [activeCase?.id, loadCaseAnalysis]);

  // Handlers from sidebar
  const handleSelectCase = (c: CaseMeta) => {
    setActiveCase(c);
  };

  const handleAnalyzeSuccess = (resp: AnalyzeCaseResponse) => {
    setAnalysis(resp);
    setActiveTab('rca');
  };

  return (
    <div className="app-container">
      {/* 1. Global Storage Header (15 GB budget bar, Purge Raw logs) */}
      <StorageHeader />

      {/* 2. Main Layout: Sidebar + Central Workspace */}
      <div className="app-body">
        {/* Left Sidebar: Cases, Local Ingest, Tilt live capture, RCA Analyze button */}
        <CaseSidebar
          selectedCaseId={activeCase?.id}
          onSelectCase={handleSelectCase}
          onAnalyzeSuccess={handleAnalyzeSuccess}
          onIngestComplete={() => {
            if (activeCase?.id) {
              void loadCaseAnalysis(activeCase.id);
            }
          }}
        />

        {/* Central Workspace */}
        <main className="app-main">
          {activeCase ? (
            <div className="case-workspace">
              {/* Case Title & Tabs Navigation Bar */}
              <div className="case-header-bar">
                <div className="case-meta-group">
                  <div className="case-title-row">
                    <h2>{activeCase.title}</h2>
                    <span className="case-status-badge status-ready">{activeCase.status}</span>
                  </div>
                  {activeCase.description && (
                    <p className="case-desc-text">{activeCase.description}</p>
                  )}
                  <div className="case-submeta text-xs text-muted">
                    <span>ID: <code className="font-mono">{activeCase.id}</code></span>
                    <span>Создан: {new Date(activeCase.created_at).toLocaleString()}</span>
                    {analysis?.run_id && (
                      <span>Последний анализ: <strong className="text-accent">{analysis.run_id}</strong></span>
                    )}
                  </div>
                </div>

                {/* Tabs Switcher */}
                <div className="tabs-bar">
                  <button
                    type="button"
                    className={`tab-btn ${activeTab === 'rca' ? 'tab-btn-active' : ''}`}
                    onClick={() => setActiveTab('rca')}
                  >
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                      <line x1="18" y1="20" x2="18" y2="10" />
                      <line x1="12" y1="20" x2="12" y2="4" />
                      <line x1="6" y1="20" x2="6" y2="14" />
                    </svg>
                    RCA Hypotheses & Topology
                  </button>

                  <button
                    type="button"
                    className={`tab-btn ${activeTab === 'evidence' ? 'tab-btn-active' : ''}`}
                    onClick={() => setActiveTab('evidence')}
                  >
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                      <polyline points="14 2 14 8 20 8" />
                      <line x1="16" y1="13" x2="8" y2="13" />
                      <line x1="16" y1="17" x2="8" y2="17" />
                    </svg>
                    Evidence & Reports
                    {analysis?.evidence_items && analysis.evidence_items.length > 0 && (
                      <span className="tab-counter-badge">{analysis.evidence_items.length}</span>
                    )}
                  </button>

                  <button
                    type="button"
                    className={`tab-btn ${activeTab === 'explorer' ? 'tab-btn-active' : ''}`}
                    onClick={() => setActiveTab('explorer')}
                  >
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                      <circle cx="11" cy="11" r="8" />
                      <line x1="21" y1="21" x2="16.65" y2="16.65" />
                    </svg>
                    Log Explorer
                  </button>
                </div>
              </div>

              {/* Tab Content */}
              <div className="tab-content-area">
                {activeTab === 'rca' && (
                  <RcaDashboard
                    caseId={activeCase.id}
                    analysis={analysis}
                    loading={loadingAnalysis}
                    onRefresh={() => void loadCaseAnalysis(activeCase.id)}
                  />
                )}

                {activeTab === 'evidence' && (
                  <EvidenceAndGeminiPanel
                    caseId={activeCase.id}
                    runId={analysis?.run_id || 'latest'}
                    evidenceItems={analysis?.evidence_items || []}
                    loading={loadingAnalysis}
                  />
                )}

                {activeTab === 'explorer' && (
                  <div className="log-explorer-view">
                    <div className="explorer-card">
                      <div className="explorer-card-header">
                        <h3>Инспектор логов и цепочек ClickHouse</h3>
                        <span className="text-xs text-muted">
                          Быстрый переход к контексту подов и поиск распределенных трасс
                        </span>
                      </div>

                      <div className="explorer-controls-grid">
                        <div className="explorer-field">
                          <label className="text-xs">Фильтр по тексту / шаблону ошибки:</label>
                          <input
                            type="text"
                            className="input-text"
                            placeholder="Например: connection refused, 503, out of memory..."
                            value={explorerQuery}
                            onChange={(e) => setExplorerQuery(e.target.value)}
                          />
                        </div>

                        <div className="explorer-field">
                          <label className="text-xs">Сквозной ID (trace_id / request_id / session_id):</label>
                          <div className="input-with-button">
                            <input
                              type="text"
                              className="input-text"
                              placeholder="c7a19e8b-..."
                              value={explorerKey}
                              onChange={(e) => setExplorerKey(e.target.value)}
                            />
                            <button
                              type="button"
                              className="btn btn-primary-sm"
                              onClick={() => {
                                setActiveTab('rca');
                              }}
                            >
                              Искать цепочку
                            </button>
                          </div>
                        </div>
                      </div>

                      {analysis?.hypotheses && analysis.hypotheses.length > 0 ? (
                        <div className="explorer-quick-access">
                          <span className="text-xs font-semibold text-muted">
                            Быстрый просмотр контекста подов с инцидентами:
                          </span>
                          <div className="quick-pods-chips">
                            {analysis.hypotheses.map((h) => (
                              <button
                                key={h.id}
                                type="button"
                                className="btn-chip"
                                onClick={() => {
                                  setActiveTab('rca');
                                }}
                              >
                                {h.service} ({h.dominant_pod}): {h.error_class || h.template_text.slice(0, 30)}
                              </button>
                            ))}
                          </div>
                        </div>
                      ) : (
                        <div className="empty-hint p-4">
                          Запустите анализ RCA для автоматического определения проблемных подов и формирования контекста.
                        </div>
                      )}
                    </div>
                  </div>
                )}
              </div>
            </div>
          ) : (
            <div className="app-no-case-selected">
              <div className="no-case-illustration">
                <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
                  <path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20" />
                  <path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z" />
                </svg>
              </div>
              <h3>Кейс расследования не выбран</h3>
              <p className="text-muted">
                Создайте новый кейс или выберите существующий в левой боковой панели для импорта логов и анализа первопричин.
              </p>
            </div>
          )}

          {analysisError && (
            <div className="global-error-toast">
              <span>⚠ {analysisError}</span>
              <button type="button" className="btn-close-sm" onClick={() => setAnalysisError(null)}>
                ✕
              </button>
            </div>
          )}
        </main>
      </div>
    </div>
  );
};

export default App;
