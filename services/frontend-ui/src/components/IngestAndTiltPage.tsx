import React, { useState, useEffect, useCallback, useRef } from 'react';
import {
  api,
  AvailableFile,
  IngestStatusResponse,
  TiltTarget,
  TiltStatusResponse,
} from '../api/client';
import { IngestAndTiltPageProps } from '../types/navigation';
import { formatBytes, safePercent } from './TopNavHeader';

export const IngestAndTiltPage: React.FC<IngestAndTiltPageProps> = ({
  activeCase,
  onIngestComplete,
  onAnalyzeSuccess,
  onNavigateToHypotheses,
}) => {
  // Global message / error state
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [successMessage, setSuccessMessage] = useState<string | null>(null);

  // --- Block 1: Local Files (/import & /log_example) ---
  const [availableFiles, setAvailableFiles] = useState<AvailableFile[]>([]);
  const [selectedFilePaths, setSelectedFilePaths] = useState<string[]>([]);
  const [fileSearchQuery, setFileSearchQuery] = useState<string>('');
  const [loadingFiles, setLoadingFiles] = useState<boolean>(false);
  const [ingestingLocal, setIngestingLocal] = useState<boolean>(false);
  const [localJob, setLocalJob] = useState<IngestStatusResponse | null>(null);

  // --- Block 2: Drag & Drop Browser Upload ---
  const [stagedFiles, setStagedFiles] = useState<File[]>([]);
  const [isDragging, setIsDragging] = useState<boolean>(false);
  const [uploading, setUploading] = useState<boolean>(false);
  const [uploadJob, setUploadJob] = useState<IngestStatusResponse | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  // --- Block 3: Tilt Live Capture ---
  const [tiltTargets, setTiltTargets] = useState<TiltTarget[]>([]);
  const [selectedTargetIds, setSelectedTargetIds] = useState<string[]>([]);
  const [tiltServiceFilter, setTiltServiceFilter] = useState<string>('');
  const [sinceSeconds, setSinceSeconds] = useState<number>(300);
  const [customSinceInput, setCustomSinceInput] = useState<string>('300');
  const [tiltStatus, setTiltStatus] = useState<TiltStatusResponse | null>(null);
  const [loadingTilt, setLoadingTilt] = useState<boolean>(false);
  const [tiltActionLoading, setTiltActionLoading] = useState<boolean>(false);

  // Quick RCA run state
  const [analyzingQuick, setAnalyzingQuick] = useState<boolean>(false);

  // Polling timer refs
  const localJobTimerRef = useRef<number | null>(null);
  const uploadJobTimerRef = useRef<number | null>(null);
  const tiltTimerRef = useRef<number | null>(null);

  // Clean up all timers on unmount
  useEffect(() => {
    return () => {
      if (localJobTimerRef.current) clearInterval(localJobTimerRef.current);
      if (uploadJobTimerRef.current) clearInterval(uploadJobTimerRef.current);
      if (tiltTimerRef.current) clearInterval(tiltTimerRef.current);
    };
  }, []);

  // 1. Fetch available files
  const fetchAvailableFiles = useCallback(async () => {
    setLoadingFiles(true);
    try {
      const files = await api.getAvailableFiles();
      setAvailableFiles(files);
      // Auto-select all by default for fast 1-click experience
      setSelectedFilePaths(files.map((f) => f.path));
    } catch {
      setAvailableFiles([]);
    } finally {
      setLoadingFiles(false);
    }
  }, []);

  useEffect(() => {
    void fetchAvailableFiles();
  }, [fetchAvailableFiles]);

  // 2. Fetch Tilt info
  const fetchTiltInfo = useCallback(async () => {
    if (!activeCase?.id) return;
    setLoadingTilt(true);
    try {
      const [targets, status] = await Promise.all([
        api.getTiltTargets().catch(() => [] as TiltTarget[]),
        api.getTiltStatus(activeCase.id).catch(() => ({
          active: false,
          targets_watched: 0,
          lines_captured: 0,
          batches_flushed: 0,
        } as TiltStatusResponse)),
      ]);
      setTiltTargets(targets);
      setSelectedTargetIds(targets.map((t) => t.id));
      setTiltStatus(status);
    } catch {
      // Tilt offline
    } finally {
      setLoadingTilt(false);
    }
  }, [activeCase?.id]);

  useEffect(() => {
    void fetchTiltInfo();
  }, [fetchTiltInfo]);

  // Tilt live polling
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

  // Start polling local job
  const pollLocalJob = useCallback((jobId: string) => {
    if (localJobTimerRef.current) clearInterval(localJobTimerRef.current);

    localJobTimerRef.current = window.setInterval(async () => {
      try {
        const job = await api.getJobStatus(jobId);
        setLocalJob(job);
        if (job.status === 'COMPLETED' || job.status === 'FAILED') {
          if (localJobTimerRef.current) clearInterval(localJobTimerRef.current);
          setIngestingLocal(false);
          if (job.status === 'COMPLETED') {
            setSuccessMessage(`Локальные файлы успешно импортированы: ${job.rows_ingested.toLocaleString()} строк!`);
            onIngestComplete();
          } else if (job.status === 'FAILED') {
            setErrorMessage(`Ошибка импорта: ${job.error || 'Неизвестная ошибка'}`);
          }
        }
      } catch {
        // keep polling
      }
    }, 1500);
  }, [onIngestComplete]);

  // Start polling upload job
  const pollUploadJob = useCallback((jobId: string) => {
    if (uploadJobTimerRef.current) clearInterval(uploadJobTimerRef.current);

    uploadJobTimerRef.current = window.setInterval(async () => {
      try {
        const job = await api.getJobStatus(jobId);
        setUploadJob(job);
        if (job.status === 'COMPLETED' || job.status === 'FAILED') {
          if (uploadJobTimerRef.current) clearInterval(uploadJobTimerRef.current);
          setUploading(false);
          if (job.status === 'COMPLETED') {
            setSuccessMessage(`Загрузка завершена: ${job.rows_ingested.toLocaleString()} строк обработано!`);
            setStagedFiles([]);
            onIngestComplete();
          } else if (job.status === 'FAILED') {
            setErrorMessage(`Ошибка загрузки: ${job.error || 'Неизвестная ошибка'}`);
          }
        }
      } catch {
        // keep polling
      }
    }, 1500);
  }, [onIngestComplete]);

  // --- Handlers: Block 1 Local Ingest ---
  const handleIngestLocalFiles = async () => {
    if (!activeCase?.id || selectedFilePaths.length === 0) return;
    setIngestingLocal(true);
    setErrorMessage(null);
    setSuccessMessage(null);
    try {
      const res = await api.ingestLocal(activeCase.id, { file_paths: selectedFilePaths });
      setLocalJob(res);
      pollLocalJob(res.job_id);
    } catch (err: unknown) {
      setErrorMessage(err instanceof Error ? err.message : 'Не удалось начать импорт локальных файлов');
      setIngestingLocal(false);
    }
  };

  const toggleSelectAllFiles = () => {
    if (selectedFilePaths.length === availableFiles.length) {
      setSelectedFilePaths([]);
    } else {
      setSelectedFilePaths(availableFiles.map((f) => f.path));
    }
  };

  const toggleSelectFile = (path: string) => {
    setSelectedFilePaths((prev) =>
      prev.includes(path) ? prev.filter((p) => p !== path) : [...prev, path]
    );
  };

  // Filter available files by search
  const filteredAvailableFiles = availableFiles.filter((f) => {
    const q = fileSearchQuery.toLowerCase().trim();
    if (!q) return true;
    return f.name.toLowerCase().includes(q) || f.path.toLowerCase().includes(q);
  });

  // --- Handlers: Block 2 Browser Upload ---
  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDragging(true);
  };

  const handleDragLeave = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDragging(false);
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDragging(false);
    if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      const newFiles = Array.from(e.dataTransfer.files);
      setStagedFiles((prev) => [...prev, ...newFiles]);
    }
  };

  const handleFileInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files && e.target.files.length > 0) {
      const newFiles = Array.from(e.target.files);
      setStagedFiles((prev) => [...prev, ...newFiles]);
    }
  };

  const handleRemoveStagedFile = (idx: number) => {
    setStagedFiles((prev) => prev.filter((_, i) => i !== idx));
  };

  const handleStartUpload = async () => {
    if (!activeCase?.id || stagedFiles.length === 0) return;
    setUploading(true);
    setErrorMessage(null);
    setSuccessMessage(null);
    try {
      const res = await api.uploadFile(activeCase.id, stagedFiles);
      setUploadJob(res);
      pollUploadJob(res.job_id);
    } catch (err: unknown) {
      setErrorMessage(err instanceof Error ? err.message : 'Ошибка при загрузке файлов');
      setUploading(false);
    }
  };

  // --- Handlers: Block 3 Tilt Live Capture ---
  const handleStartTilt = async () => {
    if (!activeCase?.id) return;
    setTiltActionLoading(true);
    setErrorMessage(null);
    try {
      const res = await api.startTiltCapture(
        activeCase.id,
        sinceSeconds,
        selectedTargetIds.length > 0 ? selectedTargetIds : undefined
      );
      setTiltStatus(res);
      setSuccessMessage('Захват логов из Tilt успешно запущен!');
    } catch (err: unknown) {
      setErrorMessage(err instanceof Error ? err.message : 'Не удалось запустить захват Tilt');
    } finally {
      setTiltActionLoading(false);
    }
  };

  const handleStopTilt = async () => {
    if (!activeCase?.id) return;
    setTiltActionLoading(true);
    setErrorMessage(null);
    try {
      const res = await api.stopTiltCapture(activeCase.id);
      setTiltStatus(res);
      setSuccessMessage('Захват логов Tilt остановлен.');
      onIngestComplete();
    } catch (err: unknown) {
      setErrorMessage(err instanceof Error ? err.message : 'Не удалось остановить захват Tilt');
    } finally {
      setTiltActionLoading(false);
    }
  };

  const toggleSelectAllTargets = () => {
    if (selectedTargetIds.length === tiltTargets.length) {
      setSelectedTargetIds([]);
    } else {
      setSelectedTargetIds(tiltTargets.map((t) => t.id));
    }
  };

  const toggleSelectTarget = (id: string) => {
    setSelectedTargetIds((prev) =>
      prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id]
    );
  };

  // Filter tilt targets
  const filteredTargets = tiltTargets.filter((t) => {
    const q = tiltServiceFilter.toLowerCase().trim();
    if (!q) return true;
    return (
      t.service_name.toLowerCase().includes(q) ||
      t.pod_name.toLowerCase().includes(q) ||
      t.runtime.toLowerCase().includes(q)
    );
  });

  // Quick Analyze
  const handleQuickAnalyze = async () => {
    if (!activeCase?.id) return;
    setAnalyzingQuick(true);
    setErrorMessage(null);
    try {
      const res = await api.analyzeCase(activeCase.id, {
        case_id: activeCase.id,
        use_historical_baseline: true,
      });
      onAnalyzeSuccess(res);
      onNavigateToHypotheses();
    } catch (err: unknown) {
      setErrorMessage(err instanceof Error ? err.message : 'Ошибка при анализе RCA');
    } finally {
      setAnalyzingQuick(false);
    }
  };

  return (
    <div className="v2-page-container">
      {/* Case Header Hero Banner */}
      <div className="v2-page-hero">
        <div className="v2-hero-info">
          <div className="v2-hero-title-row">
            <span className="v2-hero-icon">📥</span>
            <h2>Источники и Загрузка логов</h2>
            <span className={`v2-status-pill status-${activeCase.status.toLowerCase()}`}>
              {activeCase.status}
            </span>
          </div>
          <p className="v2-hero-desc">
            Активный кейс: <strong>{activeCase.title}</strong>{' '}
            <span className="text-muted text-xs">({activeCase.description || 'без описания'})</span>
          </p>
        </div>

        <div className="v2-hero-actions">
          <button
            type="button"
            className="btn btn-secondary"
            onClick={onNavigateToHypotheses}
          >
            <span>🎯 Перейти к гипотезам RCA</span>
          </button>
          <button
            type="button"
            className="btn btn-primary"
            onClick={() => void handleQuickAnalyze()}
            disabled={analyzingQuick}
          >
            <span>{analyzingQuick ? 'Выполняется анализ...' : '🚀 Запустить RCA'}</span>
          </button>
        </div>
      </div>

      {/* Notifications */}
      {errorMessage && (
        <div className="storage-error-banner mb-2">
          <span>⚠ {errorMessage}</span>
          <button type="button" className="btn-close-sm" onClick={() => setErrorMessage(null)}>✕</button>
        </div>
      )}

      {successMessage && (
        <div className="purge-success-badge mb-2">
          <span>✓ {successMessage}</span>
          <button type="button" className="btn-close-sm ml-2" onClick={() => setSuccessMessage(null)}>✕</button>
        </div>
      )}

      {/* Main 3-Column Grid */}
      <div className="v2-grid-3col">
        {/* =========================================================================
            BLOCK 1: Локальные файлы из /import и /log_example
            ========================================================================= */}
        <section className="v2-card v2-block-card">
          <div className="v2-card-header">
            <div className="v2-card-title-group">
              <span className="v2-block-icon">📁</span>
              <div>
                <h3>Локальные файлы</h3>
                <span className="v2-card-subtitle">Каталоги /import и /log_example</span>
              </div>
            </div>
            <button
              type="button"
              className="btn btn-ghost-sm"
              onClick={() => void fetchAvailableFiles()}
              disabled={loadingFiles}
              title="Обновить список файлов"
            >
              🔄
            </button>
          </div>

          <div className="v2-card-body">
            {/* Search and Selection Toolbar */}
            <div className="v2-toolbar-row">
              <input
                type="text"
                className="input-text v2-input-flex text-xs"
                placeholder="Поиск по имени файла или пути..."
                value={fileSearchQuery}
                onChange={(e) => setFileSearchQuery(e.target.value)}
              />
              <button
                type="button"
                className="btn btn-ghost-sm"
                onClick={toggleSelectAllFiles}
              >
                {selectedFilePaths.length === availableFiles.length && availableFiles.length > 0
                  ? 'Снять выбор'
                  : 'Выбрать все'}
              </button>
            </div>

            {/* Files List Table */}
            <div className="v2-files-table-container">
              {loadingFiles ? (
                <div className="rca-loading-box">
                  <span className="storage-spinner">⟳</span>
                  <span className="text-xs">Поиск доступных файлов...</span>
                </div>
              ) : filteredAvailableFiles.length === 0 ? (
                <div className="empty-hint">
                  {availableFiles.length === 0
                    ? 'Файлы не найдены в директориях /import или /log_example.'
                    : 'По вашему запросу ничего не найдено.'}
                </div>
              ) : (
                <table className="v2-table">
                  <thead>
                    <tr>
                      <th style={{ width: '32px' }}></th>
                      <th>Файл</th>
                      <th style={{ width: '90px' }}>Размер</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filteredAvailableFiles.map((file) => {
                      const isSelected = selectedFilePaths.includes(file.path);
                      return (
                        <tr
                          key={file.path}
                          className={isSelected ? 'v2-row-selected' : ''}
                          onClick={() => toggleSelectFile(file.path)}
                        >
                          <td>
                            <input
                              type="checkbox"
                              checked={isSelected}
                              onChange={() => toggleSelectFile(file.path)}
                              onClick={(e) => e.stopPropagation()}
                            />
                          </td>
                          <td className="v2-file-cell" title={file.path}>
                            <span className="v2-file-name-strong">{file.name}</span>
                            <span className="v2-file-path-sub">{file.path}</span>
                          </td>
                          <td className="v2-size-cell">{formatBytes(file.size_bytes)}</td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              )}
            </div>

            {/* Ingest Progress for Local Files */}
            {localJob && (
              <div className="v2-job-progress-card mt-2">
                <div className="v2-job-header">
                  <span className="font-semibold text-xs">Статус задачи:</span>
                  <span className={`v2-status-pill status-${localJob.status.toLowerCase()}`}>
                    {localJob.status}
                  </span>
                </div>
                <div className="v2-progress-bar-bg">
                  <div
                    className="v2-progress-bar-fill"
                    style={{ width: `${safePercent(localJob.files_done, localJob.files_total)}%` }}
                  />
                </div>
                <div className="v2-job-metrics-row">
                  <span>Файлы: {localJob.files_done} / {localJob.files_total}</span>
                  <span>Строк: {localJob.rows_ingested.toLocaleString()}</span>
                  <span>Прочитано: {formatBytes(localJob.bytes_read)}</span>
                </div>
                {localJob.clock_skew_warnings && localJob.clock_skew_warnings.length > 0 && (
                  <div className="skew-warn-text mt-1">
                    ⚠ Предупреждения рассинхрона времени: {localJob.clock_skew_warnings.length}
                  </div>
                )}
              </div>
            )}
          </div>

          <div className="v2-card-footer">
            <span className="text-xs text-muted">
              Выбрано: <strong>{selectedFilePaths.length}</strong> из {availableFiles.length}
            </span>
            <button
              type="button"
              className="btn btn-primary"
              disabled={ingestingLocal || selectedFilePaths.length === 0}
              onClick={() => void handleIngestLocalFiles()}
            >
              {ingestingLocal ? 'Импортирование...' : `📥 Импортировать (${selectedFilePaths.length})`}
            </button>
          </div>
        </section>

        {/* =========================================================================
            BLOCK 2: Потоковая загрузка через браузер (Drag & Drop)
            ========================================================================= */}
        <section className="v2-card v2-block-card">
          <div className="v2-card-header">
            <div className="v2-card-title-group">
              <span className="v2-block-icon">☁️</span>
              <div>
                <h3>Потоковая загрузка</h3>
                <span className="v2-card-subtitle">Drag & Drop файлов логов в браузер</span>
              </div>
            </div>
            {stagedFiles.length > 0 && (
              <button
                type="button"
                className="btn btn-ghost-sm"
                onClick={() => setStagedFiles([])}
                disabled={uploading}
              >
                Очистить
              </button>
            )}
          </div>

          <div className="v2-card-body">
            {/* Drag & Drop Zone */}
            <div
              className={`v2-dropzone ${isDragging ? 'v2-dropzone-active' : ''}`}
              onDragOver={handleDragOver}
              onDragLeave={handleDragLeave}
              onDrop={handleDrop}
              onClick={() => fileInputRef.current?.click()}
            >
              <input
                ref={fileInputRef}
                type="file"
                multiple
                accept=".json,.jsonl,.gz,.zip,application/json,application/gzip,application/zip"
                style={{ display: 'none' }}
                onChange={handleFileInputChange}
              />
              <div className="v2-dropzone-content">
                <span className="v2-dropzone-icon">📤</span>
                <span className="v2-dropzone-prompt font-semibold">
                  Перетащите файлы сюда или нажмите для выбора
                </span>
                <span className="text-xs text-muted">
                  Поддерживаются форматы .json, .jsonl, .gz, .zip без ограничений
                </span>
              </div>
            </div>

            {/* Staged Files List */}
            <div className="v2-staged-files-list">
              {stagedFiles.length === 0 ? (
                <div className="empty-hint">Файлы ещё не выбраны для загрузки</div>
              ) : (
                <div className="v2-staged-items">
                  {stagedFiles.map((file, idx) => (
                    <div key={`${file.name}-${idx}`} className="v2-staged-row">
                      <span className="v2-staged-name" title={file.name}>
                        📄 {file.name}
                      </span>
                      <span className="v2-staged-size">{formatBytes(file.size)}</span>
                      <button
                        type="button"
                        className="btn-icon-xs text-danger"
                        disabled={uploading}
                        onClick={() => handleRemoveStagedFile(idx)}
                        title="Удалить из списка"
                      >
                        ✕
                      </button>
                    </div>
                  ))}
                </div>
              )}
            </div>

            {/* Ingest Progress for Upload */}
            {uploadJob && (
              <div className="v2-job-progress-card mt-2">
                <div className="v2-job-header">
                  <span className="font-semibold text-xs">Статус загрузки:</span>
                  <span className={`v2-status-pill status-${uploadJob.status.toLowerCase()}`}>
                    {uploadJob.status}
                  </span>
                </div>
                <div className="v2-progress-bar-bg">
                  <div
                    className="v2-progress-bar-fill"
                    style={{ width: `${safePercent(uploadJob.files_done, uploadJob.files_total)}%` }}
                  />
                </div>
                <div className="v2-job-metrics-row">
                  <span>Файлов: {uploadJob.files_done} / {uploadJob.files_total}</span>
                  <span>Строк: {uploadJob.rows_ingested.toLocaleString()}</span>
                  <span>Размер: {formatBytes(uploadJob.bytes_read)}</span>
                </div>
                {uploadJob.skipped_duplicate_files > 0 && (
                  <span className="text-xs text-muted mt-1">
                    Пропущено дубликатов: {uploadJob.skipped_duplicate_files}
                  </span>
                )}
              </div>
            )}
          </div>

          <div className="v2-card-footer">
            <span className="text-xs text-muted">
              К загрузке: <strong>{stagedFiles.length}</strong> файлов
            </span>
            <button
              type="button"
              className="btn btn-primary"
              disabled={uploading || stagedFiles.length === 0}
              onClick={() => void handleStartUpload()}
            >
              {uploading ? 'Загрузка...' : `Загрузить и обработать (${stagedFiles.length})`}
            </button>
          </div>
        </section>

        {/* =========================================================================
            BLOCK 3: Живой захват из Tilt (Live Capture)
            ========================================================================= */}
        <section className="v2-card v2-block-card">
          <div className="v2-card-header">
            <div className="v2-card-title-group">
              <span className="v2-block-icon">⚡</span>
              <div>
                <h3>Живой захват Tilt</h3>
                <span className="v2-card-subtitle">Сбор stdout/stderr из локальных подов Tilt</span>
              </div>
            </div>
            <button
              type="button"
              className="btn btn-ghost-sm"
              onClick={() => void fetchTiltInfo()}
              disabled={loadingTilt}
              title="Обновить список сервисов Tilt"
            >
              🔄
            </button>
          </div>

          <div className="v2-card-body">
            {/* Live Status Badge / Pulsing banner */}
            {tiltStatus?.active ? (
              <div className="v2-tilt-live-banner">
                <div className="v2-pulse-dot" />
                <div className="v2-tilt-live-text">
                  <strong>Захват логов активен!</strong>
                  <span className="text-xs">Сбор событий в реальном времени</span>
                </div>
              </div>
            ) : (
              <div className="v2-tilt-idle-banner">
                <span className="text-xs text-muted">
                  Захват остановлен. Выберите глубину и сервисы для старта.
                </span>
              </div>
            )}

            {/* Live Metrics Dashboard */}
            <div className="v2-tilt-metrics-grid">
              <div className="v2-tilt-stat-box">
                <span className="v2-tilt-stat-val">{tiltStatus?.lines_captured ?? 0}</span>
                <span className="v2-tilt-stat-lbl">Строк собрано</span>
              </div>
              <div className="v2-tilt-stat-box">
                <span className="v2-tilt-stat-val">{tiltStatus?.batches_flushed ?? 0}</span>
                <span className="v2-tilt-stat-lbl">Батчей в ClickHouse</span>
              </div>
              <div className="v2-tilt-stat-box">
                <span className="v2-tilt-stat-val">{tiltStatus?.targets_watched ?? 0}</span>
                <span className="v2-tilt-stat-lbl">Сервисов под наблюдением</span>
              </div>
            </div>

            {/* Depth Presets Row */}
            <div className="v2-tilt-presets-section">
              <span className="text-xs font-semibold text-muted">Глубина захвата:</span>
              <div className="v2-presets-chips">
                <button
                  type="button"
                  className={`btn-chip ${sinceSeconds === 60 ? 'btn-chip-active' : ''}`}
                  onClick={() => {
                    setSinceSeconds(60);
                    setCustomSinceInput('60');
                  }}
                  disabled={tiltStatus?.active}
                >
                  1 мин
                </button>
                <button
                  type="button"
                  className={`btn-chip ${sinceSeconds === 300 ? 'btn-chip-active' : ''}`}
                  onClick={() => {
                    setSinceSeconds(300);
                    setCustomSinceInput('300');
                  }}
                  disabled={tiltStatus?.active}
                >
                  5 мин
                </button>
                <button
                  type="button"
                  className={`btn-chip ${sinceSeconds === 900 ? 'btn-chip-active' : ''}`}
                  onClick={() => {
                    setSinceSeconds(900);
                    setCustomSinceInput('900');
                  }}
                  disabled={tiltStatus?.active}
                >
                  15 мин
                </button>
                <div className="v2-custom-since-input">
                  <input
                    type="number"
                    min={10}
                    max={86400}
                    className="input-xs"
                    value={customSinceInput}
                    disabled={tiltStatus?.active}
                    onChange={(e) => {
                      setCustomSinceInput(e.target.value);
                      const parsed = parseInt(e.target.value, 10);
                      if (Number.isFinite(parsed) && parsed > 0) {
                        setSinceSeconds(parsed);
                      }
                    }}
                  />
                  <span className="text-xs text-sub">сек</span>
                </div>
              </div>
            </div>

            {/* Discovered Targets Toolbar & List */}
            <div className="v2-tilt-targets-container">
              <div className="v2-toolbar-row">
                <input
                  type="text"
                  className="input-text v2-input-flex text-xs"
                  placeholder="Фильтр сервисов и подов..."
                  value={tiltServiceFilter}
                  disabled={tiltStatus?.active}
                  onChange={(e) => setTiltServiceFilter(e.target.value)}
                />
                <button
                  type="button"
                  className="btn btn-ghost-sm"
                  disabled={tiltStatus?.active || tiltTargets.length === 0}
                  onClick={toggleSelectAllTargets}
                >
                  {selectedTargetIds.length === tiltTargets.length ? 'Снять' : 'Все'}
                </button>
              </div>

              <div className="v2-targets-scroll-area">
                {loadingTilt ? (
                  <div className="rca-loading-box">
                    <span className="storage-spinner">⟳</span>
                    <span className="text-xs">Поиск сервисов Tilt...</span>
                  </div>
                ) : filteredTargets.length === 0 ? (
                  <div className="empty-hint">
                    {tiltTargets.length === 0
                      ? 'Сервисы Tilt не обнаружены. Убедитесь, что tilt-collector запущен.'
                      : 'По фильтру ничего не найдено.'}
                  </div>
                ) : (
                  filteredTargets.map((target) => {
                    const isSelected = selectedTargetIds.includes(target.id);
                    return (
                      <div
                        key={target.id}
                        className={`v2-target-row ${isSelected ? 'selected' : ''}`}
                        onClick={() => {
                          if (!tiltStatus?.active) {
                            toggleSelectTarget(target.id);
                          }
                        }}
                      >
                        <input
                          type="checkbox"
                          checked={isSelected}
                          disabled={tiltStatus?.active}
                          onChange={() => toggleSelectTarget(target.id)}
                          onClick={(e) => e.stopPropagation()}
                        />
                        <div className="v2-target-labels">
                          <span className="v2-target-service">{target.service_name}</span>
                          <span className="v2-target-pod text-muted">{target.pod_name}</span>
                        </div>
                        <span className="v2-target-runtime text-xs">{target.runtime}</span>
                      </div>
                    );
                  })
                )}
              </div>
            </div>
          </div>

          <div className="v2-card-footer">
            <span className="text-xs text-muted">
              Выбрано сервисов: <strong>{selectedTargetIds.length}</strong>
            </span>
            {!tiltStatus?.active ? (
              <button
                type="button"
                className="btn btn-tilt-start"
                disabled={tiltActionLoading || selectedTargetIds.length === 0}
                onClick={() => void handleStartTilt()}
              >
                {tiltActionLoading ? 'Запуск...' : '▶️ Начать захват'}
              </button>
            ) : (
              <button
                type="button"
                className="btn btn-danger"
                disabled={tiltActionLoading}
                onClick={() => void handleStopTilt()}
              >
                {tiltActionLoading ? 'Остановка...' : '⏹️ Остановить захват'}
              </button>
            )}
          </div>
        </section>
      </div>
    </div>
  );
};
