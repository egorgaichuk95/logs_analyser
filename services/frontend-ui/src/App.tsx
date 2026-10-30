import React, { useState, useEffect, useCallback } from 'react';
import { api, CaseMeta, AnalyzeCaseResponse, CreateCaseRequest } from './api/client';
import { AppPageId } from './types/navigation';
import { TopNavHeader } from './components/TopNavHeader';
import { SimpleRcaOverviewPage } from './components/SimpleRcaOverviewPage';
import { IngestAndTiltPage } from './components/IngestAndTiltPage';
import { HypothesesPage } from './components/HypothesesPage';
import { TopologyAndTimelinePage } from './components/TopologyAndTimelinePage';
import { ChainAndContextPage } from './components/ChainAndContextPage';
import { EvidenceAndGeminiPanel } from './components/EvidenceAndGeminiPanel';
import { StorageHeader } from './components/StorageHeader';
import { CaseSidebar } from './components/CaseSidebar';
import { RcaDashboard } from './components/RcaDashboard';
import { HelpTooltip } from './components/HelpTooltip';

export const App: React.FC = () => {
  // Cases state
  const [cases, setCases] = useState<CaseMeta[]>([]);
  const [activeCase, setActiveCase] = useState<CaseMeta | null>(null);
  const [loadingCases, setLoadingCases] = useState<boolean>(true);

  // Analysis state
  const [analysis, setAnalysis] = useState<AnalyzeCaseResponse | null>(null);
  const [loadingAnalysis, setLoadingAnalysis] = useState<boolean>(false);

  // Active page state: 'overview' | 'ingest' | 'hypotheses' | 'topology' | 'chains' | 'evidence' | 'classic_v1'
  const [activePage, setActivePage] = useState<AppPageId>('overview');

  // Deep-link state across pages
  const [initialChainKey, setInitialChainKey] = useState<string | undefined>(undefined);
  const [highlightEvidenceId, setHighlightEvidenceId] = useState<string | undefined>(undefined);

  // Welcome screen creation modal state
  const [showCreateModal, setShowCreateModal] = useState<boolean>(false);
  const [newCaseTitle, setNewCaseTitle] = useState<string>('');
  const [newCaseDesc, setNewCaseDesc] = useState<string>('');
  const [creatingCase, setCreatingCase] = useState<boolean>(false);
  const [createCaseError, setCreateCaseError] = useState<string | null>(null);

  // 1. Fetch analysis for a specific case
  const loadCaseAnalysis = useCallback(async (caseId: string): Promise<AnalyzeCaseResponse | null> => {
    setLoadingAnalysis(true);
    try {
      const data = await api.getAnalysis(caseId);
      setAnalysis(data);
      return data;
    } catch {
      setAnalysis(null);
      return null;
    } finally {
      setLoadingAnalysis(false);
    }
  }, []);

  // 2. Initial load: fetch cases & smart auto-select latest
  useEffect(() => {
    let isMounted = true;

    const initApp = async () => {
      setLoadingCases(true);
      try {
        const caseList = await api.getCases();
        if (!isMounted) return;
        setCases(caseList);

        if (caseList.length > 0) {
          const latestCase = caseList[0];
          setActiveCase(latestCase);

          // Check if latest case already has an analysis
          const existingAnalysis = await loadCaseAnalysis(latestCase.id);
          if (!isMounted) return;

          if (existingAnalysis && existingAnalysis.hypotheses && existingAnalysis.hypotheses.length > 0) {
            setActivePage('overview');
          } else {
            setActivePage('ingest');
          }
        } else {
          setActiveCase(null);
          setActivePage('ingest');
        }
      } catch {
        if (!isMounted) return;
        setCases([]);
        setActiveCase(null);
      } finally {
        if (isMounted) setLoadingCases(false);
      }
    };

    void initApp();

    return () => {
      isMounted = false;
    };
  }, [loadCaseAnalysis]);

  // 3. Navigation handlers
  const handleSelectCase = (caseMeta: CaseMeta) => {
    setActiveCase(caseMeta);
    setInitialChainKey(undefined);
    setHighlightEvidenceId(undefined);
    void (async () => {
      const existing = await loadCaseAnalysis(caseMeta.id);
      if (existing && existing.hypotheses && existing.hypotheses.length > 0) {
        if (activePage === 'ingest') {
          setActivePage('overview');
        }
      } else {
        setActivePage('ingest');
      }
    })();
  };

  const handleCaseCreated = (created: CaseMeta) => {
    setCases((prev) => [created, ...prev]);
    setActiveCase(created);
    setAnalysis(null);
    setInitialChainKey(undefined);
    setHighlightEvidenceId(undefined);
    setActivePage('ingest');
  };

  const handleCaseDeleted = (deletedCaseId: string) => {
    setCases((prev) => prev.filter((c) => c.id !== deletedCaseId));
    if (activeCase?.id === deletedCaseId) {
      const remaining = cases.filter((c) => c.id !== deletedCaseId);
      if (remaining.length > 0) {
        const nextCase = remaining[0];
        setActiveCase(nextCase);
        void loadCaseAnalysis(nextCase.id);
      } else {
        setActiveCase(null);
        setAnalysis(null);
      }
    }
  };

  const handleAnalyzeSuccess = (resp: AnalyzeCaseResponse) => {
    setAnalysis(resp);
    setActivePage('overview');
  };

  const handlePurgeSuccess = () => {
    if (activeCase?.id) {
      void loadCaseAnalysis(activeCase.id);
    }
  };

  // Welcome screen create case submission
  const handleWelcomeCreateCase = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newCaseTitle.trim()) return;

    setCreatingCase(true);
    setCreateCaseError(null);

    try {
      const payload: CreateCaseRequest = {
        title: newCaseTitle.trim(),
        description: newCaseDesc.trim(),
      };
      const created = await api.createCase(payload);
      handleCaseCreated(created);
      setNewCaseTitle('');
      setNewCaseDesc('');
      setShowCreateModal(false);
    } catch (err: unknown) {
      setCreateCaseError(err instanceof Error ? err.message : 'Ошибка при создании кейса');
    } finally {
      setCreatingCase(false);
    }
  };

  return (
    <div className="v2-app-root">
      {/* 1. Global Navigation Top Bar */}
      <TopNavHeader
        activeCase={activeCase}
        activePage={activePage}
        analysis={analysis}
        onSelectPage={(page) => setActivePage(page)}
        onSelectCase={handleSelectCase}
        onCaseCreated={handleCaseCreated}
        onCaseDeleted={handleCaseDeleted}
        onAnalyzeSuccess={handleAnalyzeSuccess}
        onPurgeSuccess={handlePurgeSuccess}
      />

      {/* 2. Main Multi-Page Full-Width Viewport */}
      <main className="v2-app-main">
        {loadingCases ? (
          <div className="v2-loading-box">
            <span className="spinner-icon">⟳</span> Загрузка окружения RCA Analyzer...
          </div>
        ) : !activeCase ? (
          /* Welcome Screen when no cases exist */
          <div className="v2-welcome-screen">
            <div className="v2-welcome-card">
              <div className="v2-welcome-icon-box">
                <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75">
                  <path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20" />
                  <path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z" />
                </svg>
              </div>

              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: '0.5rem' }}>
                <h2>Добро пожаловать в RCA Analyzer</h2>
                <HelpTooltip
                  title="О системе RCA Analyzer"
                  description="Интеллектуальная система локализации первопричин сбоев в микросервисах, сквозного поиска распределенных цепочек и автоматической генерации доказательств для Google Gemini."
                  howToUse="Создайте свой первый кейс расследования или загрузите логи для автоматического анализа."
                  accentColor="cyan"
                />
              </div>
              <p className="v2-welcome-desc">
                Интеллектуальная система локализации первопричин сбоев в микросервисах, сквозного поиска распределенных
                цепочек и автоматической генерации доказательств для Google Gemini.
              </p>

              <div className="v2-welcome-features">
                <div className="v2-welcome-feature-item">
                  <span className="f-icon">📥</span>
                  <div>
                    <strong>Быстрый импорт и Tilt Live</strong>
                    <p className="text-xs text-muted">Потоковый парсинг логов с контролем квоты диска 15 ГБ</p>
                  </div>
                </div>

                <div className="v2-welcome-feature-item">
                  <span className="f-icon">🎯</span>
                  <div>
                    <strong>Математический RCA-движок</strong>
                    <p className="text-xs text-muted">Ранжирование гипотез, компенсация Clock Skew и фильтрация шума</p>
                  </div>
                </div>

                <div className="v2-welcome-feature-item">
                  <span className="f-icon">✨</span>
                  <div>
                    <strong>Пакет для Google Gemini</strong>
                    <p className="text-xs text-muted">1-клик экспорт проверенных улик E-01..E-NN без PII</p>
                  </div>
                </div>
              </div>

              <div className="v2-welcome-actions">
                <button
                  type="button"
                  className="btn btn-primary v2-btn-welcome-create"
                  onClick={() => setShowCreateModal(true)}
                >
                  + Создать первый кейс расследования
                </button>
              </div>
            </div>
          </div>
        ) : (
          /* Active Case: 7 Pages */
          <div className="v2-page-viewport">
            {/* Page 0: Overview (✨ Понятный разбор аварии) */}
            {activePage === 'overview' && (
              <SimpleRcaOverviewPage
                caseId={activeCase.id}
                analysis={analysis}
                loading={loadingAnalysis}
                onOpenGeminiModal={() => setActivePage('evidence')}
                onSwitchToEngineeringMode={(target) =>
                  setActivePage(target || 'hypotheses')
                }
              />
            )}

            {/* Page 1: Ingest & Tilt */}
            {activePage === 'ingest' && (
              <IngestAndTiltPage
                activeCase={activeCase}
                onIngestComplete={() => {
                  if (activeCase?.id) {
                    void loadCaseAnalysis(activeCase.id);
                  }
                }}
                onAnalyzeSuccess={handleAnalyzeSuccess}
                onNavigateToHypotheses={() => setActivePage('overview')}
              />
            )}

            {/* Page 2: Hypotheses */}
            {activePage === 'hypotheses' && (
              <HypothesesPage
                caseId={activeCase.id}
                analysis={analysis}
                loading={loadingAnalysis}
                onNavigateToTopology={() => setActivePage('topology')}
                onNavigateToChains={(key) => {
                  setInitialChainKey(key);
                  setActivePage('chains');
                }}
                onNavigateToEvidence={(evId) => {
                  setHighlightEvidenceId(evId);
                  setActivePage('evidence');
                }}
              />
            )}

            {/* Page 3: Topology & Timeline */}
            {activePage === 'topology' && (
              <TopologyAndTimelinePage
                caseId={activeCase.id}
                analysis={analysis}
                onSelectServiceForLogs={() => {
                  setActivePage('chains');
                }}
              />
            )}

            {/* Page 4: Trace Chains & Logs Context */}
            {activePage === 'chains' && (
              <ChainAndContextPage
                caseId={activeCase.id}
                analysis={analysis}
                initialChainKey={initialChainKey}
              />
            )}

            {/* Page 5: Evidence & Gemini Reports */}
            {activePage === 'evidence' && (
              <EvidenceAndGeminiPanel
                caseId={activeCase.id}
                runId={analysis?.run_id || 'latest'}
                evidenceItems={analysis?.evidence_items || []}
                highlightEvidenceId={highlightEvidenceId}
                loading={loadingAnalysis}
              />
            )}

            {/* Page 6: Classic v1 Layout (All Panels on 1 Screen) */}
            {activePage === 'classic_v1' && (
              <div className="v1-classic-root">
                <div className="v1-classic-banner">
                  <span className="v1-classic-tag">🏛️ Классический режим v1</span>
                  <span className="v1-classic-desc">Все панели инструментов на одном экране</span>
                  <HelpTooltip
                    title="Классический режим v1"
                    description="Оригинальный интерфейс, где хранилище, сайдбар кейсов, детальный дэшборд гипотез и панель улик открыты одновременно."
                    howToUse="Используйте этот режим, если вам привычнее работать в компактном одноэкранном виде."
                    accentColor="violet"
                  />
                </div>
                <StorageHeader
                  onRefresh={() => {
                    if (activeCase?.id) void loadCaseAnalysis(activeCase.id);
                  }}
                  onPurgeSuccess={handlePurgeSuccess}
                />
                <div className="app-body">
                  <CaseSidebar
                    selectedCaseId={activeCase.id}
                    onSelectCase={handleSelectCase}
                    onAnalyzeSuccess={handleAnalyzeSuccess}
                    onIngestComplete={() => {
                      if (activeCase?.id) void loadCaseAnalysis(activeCase.id);
                    }}
                  />
                  <div className="main-content">
                    <RcaDashboard
                      caseId={activeCase.id}
                      analysis={analysis}
                      loading={loadingAnalysis}
                      onRefresh={() => {
                        if (activeCase?.id) void loadCaseAnalysis(activeCase.id);
                      }}
                    />
                    <EvidenceAndGeminiPanel
                      caseId={activeCase.id}
                      runId={analysis?.run_id || 'latest'}
                      evidenceItems={analysis?.evidence_items || []}
                      highlightEvidenceId={highlightEvidenceId}
                      loading={loadingAnalysis}
                    />
                  </div>
                </div>
              </div>
            )}
          </div>
        )}
      </main>

      {/* --- Modal: Create Case (from Welcome Screen) --- */}
      {showCreateModal && (
        <div className="modal-backdrop" onClick={() => setShowCreateModal(false)}>
          <div className="modal-content v2-create-case-modal" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <h3>Создать новый кейс расследования</h3>
              <button
                type="button"
                className="btn-close-sm"
                onClick={() => setShowCreateModal(false)}
              >
                ✕
              </button>
            </div>

            <form onSubmit={(e) => void handleWelcomeCreateCase(e)}>
              {createCaseError && <div className="sidebar-error-toast mb-3">{createCaseError}</div>}

              <div className="form-group mb-3">
                <label className="text-xs font-semibold">Название инцидента / кейса *:</label>
                <input
                  type="text"
                  className="input-text"
                  placeholder="Например: Сбой каскада заказов 30 октября"
                  value={newCaseTitle}
                  onChange={(e) => setNewCaseTitle(e.target.value)}
                  autoFocus
                  required
                />
              </div>

              <div className="form-group mb-4">
                <label className="text-xs font-semibold">Описание (контекст, симптомы):</label>
                <textarea
                  className="input-textarea"
                  rows={3}
                  placeholder="Опишите наблюдаемые аномалии, затронутые сервисы или ссылки на алерты..."
                  value={newCaseDesc}
                  onChange={(e) => setNewCaseDesc(e.target.value)}
                />
              </div>

              <div className="modal-footer">
                <button
                  type="button"
                  className="btn btn-secondary"
                  onClick={() => setShowCreateModal(false)}
                >
                  Отмена
                </button>
                <button
                  type="submit"
                  className="btn btn-primary"
                  disabled={creatingCase || !newCaseTitle.trim()}
                >
                  {creatingCase ? 'Создание...' : 'Создать кейс'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
};

export default App;

