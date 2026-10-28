import React, { useState, useEffect, useCallback, useRef } from 'react';
import {
  api,
  CaseMeta,
  AvailableFile,
  IngestStatusResponse,
  TiltTarget,
  TiltStatusResponse,
  AnalyzeCaseResponse,
  CreateCaseRequest,
} from '../api/client';
import { formatBytes } from './StorageHeader';

export interface CaseSidebarProps {
  selectedCaseId?: string | null;
  onSelectCase?: (c: CaseMeta) => void;
  onAnalyzeSuccess?: (resp: AnalyzeCaseResponse) => void;
  onIngestComplete?: (job: IngestStatusResponse) => void;
}

export const CaseSidebar: React.FC<CaseSidebarProps> = ({
  selectedCaseId,
  onSelectCase,
  onAnalyzeSuccess,
  onIngestComplete,
}) => {
  // Cases state
  const [cases, setCases] = useState<CaseMeta[]>([]);
  const [activeCase, setActiveCase] = useState<CaseMeta | null>(null);
  const [loadingCases, setLoadingCases] = useState<boolean>(false);
  const [showCreateModal, setShowCreateModal] = useState<boolean>(false);
  const [newTitle, setNewTitle] = useState<string>('');
  const [newDesc, setNewDesc] = useState<string>('');
  const [creatingCase, setCreatingCase] = useState<boolean>(false);

  // Available files & Ingest state
  const [availableFiles, setAvailableFiles] = useState<AvailableFile[]>([]);
  const [selectedFilePaths, setSelectedFilePaths] = useState<string[]>([]);
  const [loadingFiles, setLoadingFiles] = useState<boolean>(false);
  const [ingesting, setIngesting] = useState<boolean>(false);
  const [activeJob, setActiveJob] = useState<IngestStatusResponse | null>(null);
  const [isDragging, setIsDragging] = useState<boolean>(false);

  // Tilt live capture state
  const [tiltTargets, setTiltTargets] = useState<TiltTarget[]>([]);
  const [selectedTargetIds, setSelectedTargetIds] = useState<string[]>([]);
  const [sinceSeconds, setSinceSeconds] = useState<number>(300);
  const [tiltStatus, setTiltStatus] = useState<TiltStatusResponse | null>(null);
  const [loadingTilt, setLoadingTilt] = useState<boolean>(false);
  const [tiltActionInProgress, setTiltActionInProgress] = useState<boolean>(false);

  // Analyze state
  const [analyzing, setAnalyzing] = useState<boolean>(false);
  const [useHistoricalBaseline, setUseHistoricalBaseline] = useState<boolean>(true);
  const [incidentStart, setIncidentStart] = useState<string>('');
  const [incidentEnd, setIncidentEnd] = useState<string>('');

  // General error banner
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  // Refs for polling
  const tiltTimerRef = useRef<number | null>(null);
  const ingestTimerRef = useRef<number | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  // 1. Load cases
  const loadCases = useCallback(async () => {
    setLoadingCases(true);
    try {
      const list = await api.getCases();
      setCases(list);
      if (list.length > 0) {
        if (selectedCaseId) {
          const found = list.find((c) => c.id === selectedCaseId);
          if (found) {
            setActiveCase(found);
            return;
          }
        }
        setActiveCase((prev) => (prev ? list.find((c) => c.id === prev.id) || list[0] : list[0]));
      } else {
        setActiveCase(null);
      }
    } catch (err: unknown) {
      setErrorMessage(err instanceof Error ? err.message : 'Не удалось загрузить список кейсов');
    } finally {
      setLoadingCases(false);
    }
  }, [selectedCaseId]);

  useEffect(() => {
    void loadCases();
  }, [loadCases]);

  // Sync external selectedCaseId
  useEffect(() => {
    if (selectedCaseId && cases.length > 0) {
      const matched = cases.find((c) => c.id === selectedCaseId);
      if (matched) setActiveCase(matched);
    }
  }, [selectedCaseId, cases]);

  // Inform parent when active case changes
  useEffect(() => {
    if (activeCase && onSelectCase) {
      onSelectCase(activeCase);
    }
  }, [activeCase, onSelectCase]);

  // 2. Load available files
  const loadAvailableFiles = useCallback(async () => {
    setLoadingFiles(true);
    try {
      const files = await api.getAvailableFiles();
      setAvailableFiles(files);
      // Select all by default for convenient 1-click ingest
      setSelectedFilePaths(files.map((f) => f.path));
    } catch {
      // Non-fatal if browse is empty
      setAvailableFiles([]);
    } finally {
      setLoadingFiles(false);
    }
  }, []);

  useEffect(() => {
    void loadAvailableFiles();
  }, [loadAvailableFiles]);

  // 3. Load Tilt targets & status
  const loadTiltInfo = useCallback(async (caseId: string) => {
    setLoadingTilt(true);
    try {
      const [targets, status] = await Promise.all([
        api.getTiltTargets().catch(() => []),
        api.getTiltStatus(caseId).catch(() => ({
          active: false,
          targets_watched: 0,
          lines_captured: 0,
          batches_flushed: 0,
        })),
      ]);
      setTiltTargets(targets);
      setSelectedTargetIds(targets.map((t) => t.id));
      setTiltStatus(status);
    } catch {
      // Tilt might be offline
    } finally {
      setLoadingTilt(false);
    }
  }, []);

  useEffect(() => {
    if (activeCase?.id) {
      void loadTiltInfo(activeCase.id);
    }
  }, [activeCase?.id, loadTiltInfo]);

  // Tilt live polling when capture is active
  useEffect(() => {
    if (tiltStatus?.active && activeCase?.id) {
      const caseId = activeCase.id;
      tiltTimerRef.current = window.setInterval(async () => {
        try {
          const s = await api.getTiltStatus(caseId);
          setTiltStatus(s);
          if (!s.active && tiltTimerRef.current) {
            clearInterval(tiltTimerRef.current);
          }
        } catch {
          // ignore transient poll error
        }
      }, 2000);
    } else if (tiltTimerRef.current) {
      clearInterval(tiltTimerRef.current);
    }

    return () => {
      if (tiltTimerRef.current) clearInterval(tiltTimerRef.current);
    };
  }, [tiltStatus?.active, activeCase?.id]);

  // Ingest job polling
  const startJobPolling = useCallback(
    (jobId: string) => {
      if (ingestTimerRef.current) clearInterval(ingestTimerRef.current);

      ingestTimerRef.current = window.setInterval(async () => {
        try {
          const job = await api.getJobStatus(jobId);
          setActiveJob(job);
          if (job.status === 'COMPLETED' || job.status === 'FAILED') {
            if (ingestTimerRef.current) clearInterval(ingestTimerRef.current);
            setIngesting(false);
            if (job.status === 'COMPLETED' && onIngestComplete) {
              onIngestComplete(job);
            }
          }
        } catch {
          // keep polling or break
        }
      }, 1500);
    },
    [onIngestComplete]
  );

  useEffect(() => {
    return () => {
      if (ingestTimerRef.current) clearInterval(ingestTimerRef.current);
    };
  }, []);

  // --- Handlers ---

  const handleCreateCase = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newTitle.trim()) return;
    setCreatingCase(true);
    setErrorMessage(null);
    try {
      const req: CreateCaseRequest = {
        title: newTitle.trim(),
        description: newDesc.trim(),
      };
      const created = await api.createCase(req);
      setCases((prev) => [created, ...prev]);
      setActiveCase(created);
      setShowCreateModal(false);
      setNewTitle('');
      setNewDesc('');
    } catch (err: unknown) {
      setErrorMessage(err instanceof Error ? err.message : 'Ошибка при создании кейса');
    } finally {
      setCreatingCase(false);
    }
  };

  const handleDeleteCase = async (caseId: string) => {
    if (!window.confirm('Вы действительно хотите удалить этот кейс со всеми данными и отчётами?')) {
      return;
    }
    try {
      await api.deleteCase(caseId);
      setCases((prev) => prev.filter((c) => c.id !== caseId));
      if (activeCase?.id === caseId) {
        const remaining = cases.filter((c) => c.id !== caseId);
        setActiveCase(remaining.length > 0 ? remaining[0] : null);
      }
    } catch (err: unknown) {
      setErrorMessage(err instanceof Error ? err.message : 'Ошибка при удалении кейса');
    }
  };

  const handleIngestLocal = async () => {
    if (!activeCase?.id || selectedFilePaths.length === 0) return;
    setIngesting(true);
    setErrorMessage(null);
    try {
      const res = await api.ingestLocal(activeCase.id, { file_paths: selectedFilePaths });
      setActiveJob(res);
      startJobPolling(res.job_id);
    } catch (err: unknown) {
      setErrorMessage(err instanceof Error ? err.message : 'Ошибка при запуске локального инжеста');
      setIngesting(false);
    }
  };

  const handleFileUpload = async (files: FileList | null) => {
    if (!activeCase?.id || !files || files.length === 0) return;
    setIngesting(true);
    setErrorMessage(null);
    try {
      const res = await api.uploadFile(activeCase.id, files);
      setActiveJob(res);
      startJobPolling(res.job_id);
    } catch (err: unknown) {
      setErrorMessage(err instanceof Error ? err.message : 'Ошибка при загрузке файлов');
      setIngesting(false);
    }
  };

  const handleStartTilt = async () => {
    if (!activeCase?.id) return;
    setTiltActionInProgress(true);
    setErrorMessage(null);
    try {
      const res = await api.startTiltCapture(
        activeCase.id,
        sinceSeconds,
        selectedTargetIds.length > 0 ? selectedTargetIds : undefined
      );
      setTiltStatus(res);
    } catch (err: unknown) {
      setErrorMessage(err instanceof Error ? err.message : 'Не удалось начать захват из Tilt');
    } finally {
      setTiltActionInProgress(false);
    }
  };

  const handleStopTilt = async () => {
    if (!activeCase?.id) return;
    setTiltActionInProgress(true);
    setErrorMessage(null);
    try {
      const res = await api.stopTiltCapture(activeCase.id);
      setTiltStatus(res);
    } catch (err: unknown) {
      setErrorMessage(err instanceof Error ? err.message : 'Не удалось остановить захват Tilt');
    } finally {
      setTiltActionInProgress(false);
    }
  };

  const handleAnalyze = async () => {
    if (!activeCase?.id) return;
    setAnalyzing(true);
    setErrorMessage(null);
    try {
      const res = await api.analyzeCase(activeCase.id, {
        case_id: activeCase.id,
        use_historical_baseline: useHistoricalBaseline,
        incident_start: incidentStart ? new Date(incidentStart).toISOString() : undefined,
        incident_end: incidentEnd ? new Date(incidentEnd).toISOString() : undefined,
      });
      if (onAnalyzeSuccess) {
        onAnalyzeSuccess(res);
      }
      // Update case run id
      setActiveCase((prev) => (prev ? { ...prev, latest_run_id: res.run_id } : null));
    } catch (err: unknown) {
      setErrorMessage(err instanceof Error ? err.message : 'Ошибка при выполнении анализа RCA');
    } finally {
      setAnalyzing(false);
    }
  };

  const toggleSelectAllFiles = () => {
    if (selectedFilePaths.length === availableFiles.length) {
      setSelectedFilePaths([]);
    } else {
      setSelectedFilePaths(availableFiles.map((f) => f.path));
    }
  };

  const toggleSelectFile = (p: string) => {
    setSelectedFilePaths((prev) =>
      prev.includes(p) ? prev.filter((item) => item !== p) : [...prev, p]
    );
  };

  const toggleSelectTarget = (id: string) => {
    setSelectedTargetIds((prev) =>
      prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id]
    );
  };

  return (
    <aside className="case-sidebar">
      {/* Sidebar Header: Case selector and New Case Button */}
      <div className="sidebar-section case-selection-section">
        <div className="section-header">
          <span className="section-title">Кейсы расследования</span>
          <button
            type="button"
            className="btn btn-primary-sm"
            onClick={() => setShowCreateModal(true)}
            title="Создать новый кейс"
          >
            + Новый кейс
          </button>
        </div>

        {loadingCases && <div className="text-muted text-sm">Загрузка кейсов...</div>}

        <div className="cases-list">
          {cases.map((c) => {
            const isActive = activeCase?.id === c.id;
            return (
              <div
                key={c.id}
                className={`case-card ${isActive ? 'case-card-active' : ''}`}
                onClick={() => setActiveCase(c)}
              >
                <div className="case-card-header">
                  <span className="case-title" title={c.title}>
                    {c.title}
                  </span>
                  <button
                    type="button"
                    className="btn-icon-xs text-danger"
                    title="Удалить кейс"
                    onClick={(e) => {
                      e.stopPropagation();
                      void handleDeleteCase(c.id);
                    }}
                  >
                    ✕
                  </button>
                </div>
                {c.description && <p className="case-desc">{c.description}</p>}
                <div className="case-card-footer">
                  <span className="case-id">ID: {c.id.slice(0, 8)}...</span>
                  <span className="case-date">
                    {new Date(c.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
                  </span>
                </div>
              </div>
            );
          })}
          {cases.length === 0 && !loadingCases && (
            <div className="empty-hint">Нет кейсов. Создайте первый кейс для начала анализа.</div>
          )}
        </div>
      </div>

      {activeCase ? (
        <>
          {/* Section: File Ingestion (/import & Upload) */}
          <div className="sidebar-section ingest-section">
            <div className="section-header">
              <span className="section-title">Инжест логов</span>
              <button
                type="button"
                className="btn-icon-xs"
                onClick={() => void loadAvailableFiles()}
                title="Обновить файлы"
                disabled={loadingFiles}
              >
                ⟳
              </button>
            </div>

            {/* Local files list from /import */}
            <div className="files-picker">
              <div className="files-header">
                <label className="checkbox-label">
                  <input
                    type="checkbox"
                    checked={availableFiles.length > 0 && selectedFilePaths.length === availableFiles.length}
                    onChange={toggleSelectAllFiles}
                    disabled={availableFiles.length === 0}
                  />
                  <span>Доступные файлы ({availableFiles.length})</span>
                </label>
              </div>

              <div className="available-files-list">
                {availableFiles.map((file) => (
                  <label key={file.path} className="file-row checkbox-label" title={file.path}>
                    <input
                      type="checkbox"
                      checked={selectedFilePaths.includes(file.path)}
                      onChange={() => toggleSelectFile(file.path)}
                    />
                    <span className="file-name">{file.name}</span>
                    <span className="file-size">{formatBytes(file.size_bytes)}</span>
                  </label>
                ))}
                {availableFiles.length === 0 && !loadingFiles && (
                  <div className="empty-hint">В папках /import и /log_example файлы не найдены</div>
                )}
              </div>

              <button
                type="button"
                className="btn btn-secondary w-full"
                onClick={() => void handleIngestLocal()}
                disabled={ingesting || selectedFilePaths.length === 0}
                title="Запустить инжест выбранных локальных файлов в 1 клик"
              >
                {ingesting ? 'Идет инжест...' : `Инжест файлов (${selectedFilePaths.length})`}
              </button>
            </div>

            {/* Drag & Drop File Upload */}
            <div
              className={`dropzone ${isDragging ? 'dropzone-active' : ''}`}
              onDragOver={(e) => {
                e.preventDefault();
                setIsDragging(true);
              }}
              onDragLeave={() => setIsDragging(false)}
              onDrop={(e) => {
                e.preventDefault();
                setIsDragging(false);
                if (e.dataTransfer.files) {
                  void handleFileUpload(e.dataTransfer.files);
                }
              }}
              onClick={() => fileInputRef.current?.click()}
            >
              <input
                ref={fileInputRef}
                type="file"
                multiple
                style={{ display: 'none' }}
                onChange={(e) => {
                  if (e.target.files) void handleFileUpload(e.target.files);
                }}
              />
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                <polyline points="17 8 12 3 7 8" />
                <line x1="12" y1="3" x2="12" y2="15" />
              </svg>
              <span>Перетащите файлы сюда или выберите</span>
            </div>

            {/* Ingest Job progress */}
            {activeJob && (
              <div className={`job-status-box job-${activeJob.status.toLowerCase()}`}>
                <div className="job-status-title">
                  <span>Статус инжеста: {activeJob.status}</span>
                  {activeJob.status === 'RUNNING' && <span className="pulse-indicator" />}
                </div>
                <div className="job-metrics">
                  <div>Строк: <strong>{activeJob.rows_ingested.toLocaleString()}</strong></div>
                  <div>Прочитано: <strong>{formatBytes(activeJob.bytes_read)}</strong></div>
                  <div>Файлы: <strong>{activeJob.files_done} / {activeJob.files_total}</strong></div>
                  {activeJob.skipped_duplicate_files > 0 && (
                    <div>Пропущено дублей: {activeJob.skipped_duplicate_files}</div>
                  )}
                </div>
                {activeJob.clock_skew_warnings && activeJob.clock_skew_warnings.length > 0 && (
                  <div className="skew-warn-text">
                    ⚠ Рассинхрон часов ({activeJob.clock_skew_warnings.length}): {activeJob.clock_skew_warnings[0]}
                  </div>
                )}
                {activeJob.error && <div className="text-danger text-sm">{activeJob.error}</div>}
              </div>
            )}
          </div>

          {/* Section: Live захват из Tilt / Local Runtime */}
          <div className="sidebar-section tilt-section">
            <div className="section-header">
              <span className="section-title">Live захват из Tilt</span>
              <button
                type="button"
                className="btn-icon-xs"
                onClick={() => void loadTiltInfo(activeCase.id)}
                disabled={loadingTilt}
                title="Обновить список сервисов Tilt"
              >
                ⟳
              </button>
            </div>

            {/* Discovered targets */}
            <div className="tilt-targets-box">
              <div className="text-sm font-semibold mb-1">
                Обнаруженные сервисы ({tiltTargets.length}):
              </div>
              <div className="targets-list">
                {tiltTargets.map((t) => (
                  <label key={t.id} className="target-row checkbox-label">
                    <input
                      type="checkbox"
                      checked={selectedTargetIds.includes(t.id)}
                      onChange={() => toggleSelectTarget(t.id)}
                      disabled={tiltStatus?.active}
                    />
                    <span className="target-name" title={`${t.service_name} (${t.pod_name})`}>
                      {t.service_name}
                    </span>
                    <span className="target-pod text-muted">{t.pod_name.slice(-6)}</span>
                  </label>
                ))}
                {tiltTargets.length === 0 && !loadingTilt && (
                  <div className="empty-hint">Tilt runtime не обнаружен (автономный режим)</div>
                )}
              </div>
            </div>

            {/* since_seconds input */}
            <div className="tilt-since-row">
              <label htmlFor="since-input" className="text-sm">Глубина (сек):</label>
              <input
                id="since-input"
                type="number"
                min="10"
                max="86400"
                step="30"
                value={sinceSeconds}
                onChange={(e) => setSinceSeconds(parseInt(e.target.value, 10) || 300)}
                disabled={tiltStatus?.active}
                className="input-sm"
              />
              <div className="since-presets">
                <button
                  type="button"
                  className="btn-chip"
                  onClick={() => setSinceSeconds(60)}
                  disabled={tiltStatus?.active}
                >
                  1м
                </button>
                <button
                  type="button"
                  className="btn-chip"
                  onClick={() => setSinceSeconds(300)}
                  disabled={tiltStatus?.active}
                >
                  5м
                </button>
                <button
                  type="button"
                  className="btn-chip"
                  onClick={() => setSinceSeconds(900)}
                  disabled={tiltStatus?.active}
                >
                  15м
                </button>
              </div>
            </div>

            {/* Tilt status indicators and control buttons */}
            {tiltStatus?.active ? (
              <div className="tilt-active-box">
                <div className="tilt-live-header">
                  <span className="pulse-indicator active-pulse" />
                  <strong>ЗАХВАТ АКТИВЕН</strong>
                </div>
                <div className="tilt-counters">
                  <div className="counter-item">
                    <span className="counter-val">{tiltStatus.lines_captured.toLocaleString()}</span>
                    <span className="counter-lbl">строк захвачено</span>
                  </div>
                  <div className="counter-item">
                    <span className="counter-val">{tiltStatus.batches_flushed}</span>
                    <span className="counter-lbl">пачек сброшено</span>
                  </div>
                </div>
                <button
                  type="button"
                  className="btn btn-danger w-full"
                  onClick={() => void handleStopTilt()}
                  disabled={tiltActionInProgress}
                >
                  {tiltActionInProgress ? 'Остановка...' : 'Остановить захват'}
                </button>
              </div>
            ) : (
              <button
                type="button"
                className="btn btn-tilt-start w-full"
                onClick={() => void handleStartTilt()}
                disabled={tiltActionInProgress}
              >
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <polygon points="5 3 19 12 5 21 5 3" />
                </svg>
                {tiltActionInProgress ? 'Запуск...' : 'Начать захват из Tilt'}
              </button>
            )}
          </div>

          {/* Section: RCA Analysis Trigger */}
          <div className="sidebar-section rca-action-section">
            <div className="section-header">
              <span className="section-title">Анализ первопричин</span>
            </div>

            <label className="checkbox-label mb-2">
              <input
                type="checkbox"
                checked={useHistoricalBaseline}
                onChange={(e) => setUseHistoricalBaseline(e.target.checked)}
              />
              <span>Исторический baseline (Пуассон)</span>
            </label>

            <details className="advanced-time-details mb-2">
              <summary className="text-sm cursor-pointer text-muted">Временные рамки (опционально)</summary>
              <div className="time-pickers-box">
                <label className="text-xs">Начало инцидента:</label>
                <input
                  type="datetime-local"
                  value={incidentStart}
                  onChange={(e) => setIncidentStart(e.target.value)}
                  className="input-xs w-full"
                />
                <label className="text-xs mt-1">Конец инцидента:</label>
                <input
                  type="datetime-local"
                  value={incidentEnd}
                  onChange={(e) => setIncidentEnd(e.target.value)}
                  className="input-xs w-full"
                />
              </div>
            </details>

            <button
              type="button"
              className="btn btn-analyze w-full"
              onClick={() => void handleAnalyze()}
              disabled={analyzing}
              title="Запустить ранжирование гипотез RCA, поиск причинного графа и формирование отчётов"
            >
              {analyzing ? (
                <>
                  <span className="spinner-icon">⟳</span> Анализ инцидента...
                </>
              ) : (
                <>
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                    <circle cx="11" cy="11" r="8" />
                    <line x1="21" y1="21" x2="16.65" y2="16.65" />
                    <line x1="11" y1="8" x2="11" y2="14" />
                    <line x1="8" y1="11" x2="14" y2="11" />
                  </svg>
                  Запустить анализ RCA
                </>
              )}
            </button>
          </div>
        </>
      ) : (
        <div className="sidebar-no-active-case">
          <p>Выберите или создайте кейс для выполнения операций инжеста и анализа.</p>
        </div>
      )}

      {/* Error notification */}
      {errorMessage && (
        <div className="sidebar-error-toast">
          <span>⚠ {errorMessage}</span>
          <button type="button" className="btn-close-sm" onClick={() => setErrorMessage(null)}>✕</button>
        </div>
      )}

      {/* Modal: New Case */}
      {showCreateModal && (
        <div className="modal-backdrop" onClick={() => setShowCreateModal(false)}>
          <div className="modal-content" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <h3>Создание нового кейса</h3>
              <button type="button" className="btn-close-sm" onClick={() => setShowCreateModal(false)}>✕</button>
            </div>
            <form onSubmit={(e) => void handleCreateCase(e)}>
              <div className="form-group">
                <label htmlFor="case-title-input">Название инцидента / кейса *</label>
                <input
                  id="case-title-input"
                  type="text"
                  required
                  placeholder="Например: Сбой оплаты 503 в chat-com"
                  value={newTitle}
                  onChange={(e) => setNewTitle(e.target.value)}
                  className="input-text w-full"
                  autoFocus
                />
              </div>
              <div className="form-group">
                <label htmlFor="case-desc-input">Описание инцидента</label>
                <textarea
                  id="case-desc-input"
                  rows={3}
                  placeholder="Симптомы, затронутые сервисы, комментарии инженеров..."
                  value={newDesc}
                  onChange={(e) => setNewDesc(e.target.value)}
                  className="input-textarea w-full"
                />
              </div>
              <div className="modal-actions">
                <button
                  type="button"
                  className="btn btn-ghost"
                  onClick={() => setShowCreateModal(false)}
                  disabled={creatingCase}
                >
                  Отмена
                </button>
                <button type="submit" className="btn btn-primary" disabled={creatingCase || !newTitle.trim()}>
                  {creatingCase ? 'Создание...' : 'Создать кейс'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </aside>
  );
};
