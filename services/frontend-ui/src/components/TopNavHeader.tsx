import React, { useState, useEffect, useCallback } from 'react';
import {
  api,
  CaseMeta,
  StorageStatusResponse,
  PurgeRawResponse,
  AnalyzeCaseRequest,
  CreateCaseRequest,
} from '../api/client';
import { TopNavHeaderProps } from '../types/navigation';

export const DISK_QUOTA_BYTES = 15 * 1024 * 1024 * 1024; // 15 GB

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / Math.pow(1024, i);
  return `${value.toFixed(value >= 10 || i === 0 ? 1 : 2)} ${units[i]}`;
}

export function safePercent(part: number, total: number): number {
  if (!Number.isFinite(part) || !Number.isFinite(total) || total <= 0) {
    return 0;
  }
  const pct = (part / total) * 100;
  return Math.min(100, Math.max(0, Number.isFinite(pct) ? pct : 0));
}

export const TopNavHeader: React.FC<TopNavHeaderProps> = ({
  activeCase,
  activePage,
  analysis,
  onSelectPage,
  onSelectCase,
  onCaseCreated,
  onCaseDeleted,
  onAnalyzeSuccess,
  onPurgeSuccess,
}) => {
  // Case Modal state
  const [showCaseModal, setShowCaseModal] = useState<boolean>(false);
  const [casesList, setCasesList] = useState<CaseMeta[]>([]);
  const [caseSearchQuery, setCaseSearchQuery] = useState<string>('');
  const [loadingCases, setLoadingCases] = useState<boolean>(false);
  const [caseError, setCaseError] = useState<string | null>(null);

  // New Case form inside modal
  const [newTitle, setNewTitle] = useState<string>('');
  const [newDesc, setNewDesc] = useState<string>('');
  const [creatingCase, setCreatingCase] = useState<boolean>(false);

  // Storage state & Quota Modal
  const [storageStatus, setStorageStatus] = useState<StorageStatusResponse | null>(null);
  const [loadingStorage, setLoadingStorage] = useState<boolean>(false);
  const [showQuotaModal, setShowQuotaModal] = useState<boolean>(false);
  const [purging, setPurging] = useState<boolean>(false);
  const [showPurgeConfirm, setShowPurgeConfirm] = useState<boolean>(false);
  const [purgeResult, setPurgeResult] = useState<PurgeRawResponse | null>(null);
  const [storageError, setStorageError] = useState<string | null>(null);

  // Analyze Modal state
  const [showAnalyzeModal, setShowAnalyzeModal] = useState<boolean>(false);
  const [analyzing, setAnalyzing] = useState<boolean>(false);
  const [useHistoricalBaseline, setUseHistoricalBaseline] = useState<boolean>(true);
  const [incidentStart, setIncidentStart] = useState<string>('');
  const [incidentEnd, setIncidentEnd] = useState<string>('');
  const [analyzeError, setAnalyzeError] = useState<string | null>(null);

  // 1. Fetch storage status
  const fetchStorageStatus = useCallback(async () => {
    setLoadingStorage(true);
    setStorageError(null);
    try {
      const data = await api.getStorageStatus();
      setStorageStatus(data);
    } catch (err: unknown) {
      setStorageError(err instanceof Error ? err.message : 'Не удалось получить статус диска');
    } finally {
      setLoadingStorage(false);
    }
  }, []);

  useEffect(() => {
    void fetchStorageStatus();
  }, [fetchStorageStatus]);

  // 2. Fetch cases list for modal
  const fetchCases = useCallback(async () => {
    setLoadingCases(true);
    setCaseError(null);
    try {
      const list = await api.getCases();
      setCasesList(list);
    } catch (err: unknown) {
      setCaseError(err instanceof Error ? err.message : 'Не удалось загрузить список кейсов');
    } finally {
      setLoadingCases(false);
    }
  }, []);

  useEffect(() => {
    if (showCaseModal) {
      void fetchCases();
    }
  }, [showCaseModal, fetchCases]);

  // Case creation handler
  const handleCreateCase = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newTitle.trim()) return;
    setCreatingCase(true);
    setCaseError(null);
    try {
      const payload: CreateCaseRequest = {
        title: newTitle.trim(),
        description: newDesc.trim(),
      };
      const created = await api.createCase(payload);
      setCasesList((prev) => [created, ...prev]);
      onCaseCreated(created);
      onSelectCase(created);
      setNewTitle('');
      setNewDesc('');
      setShowCaseModal(false);
    } catch (err: unknown) {
      setCaseError(err instanceof Error ? err.message : 'Ошибка при создании кейса');
    } finally {
      setCreatingCase(false);
    }
  };

  // Case deletion handler
  const handleDeleteCase = async (caseId: string, caseTitle: string) => {
    if (!window.confirm(`Вы уверены, что хотите удалить кейс «${caseTitle}»? Все связанные логи и отчёты будут удалены.`)) {
      return;
    }
    setCaseError(null);
    try {
      await api.deleteCase(caseId);
      setCasesList((prev) => prev.filter((c) => c.id !== caseId));
      onCaseDeleted(caseId);
    } catch (err: unknown) {
      setCaseError(err instanceof Error ? err.message : 'Ошибка при удалении кейса');
    }
  };

  // Storage purge handler
  const handlePurge = async () => {
    setPurging(true);
    setStorageError(null);
    setShowPurgeConfirm(false);
    try {
      const res = await api.purgeRaw();
      setPurgeResult(res);
      await fetchStorageStatus();
      onPurgeSuccess();
    } catch (err: unknown) {
      setStorageError(err instanceof Error ? err.message : 'Ошибка при очистке сырых логов');
    } finally {
      setPurging(false);
    }
  };

  // RCA analyze handler
  const handleAnalyze = async () => {
    if (!activeCase?.id) return;
    setAnalyzing(true);
    setAnalyzeError(null);
    try {
      const payload: AnalyzeCaseRequest = {
        case_id: activeCase.id,
        use_historical_baseline: useHistoricalBaseline,
        incident_start: incidentStart ? new Date(incidentStart).toISOString() : undefined,
        incident_end: incidentEnd ? new Date(incidentEnd).toISOString() : undefined,
      };
      const res = await api.analyzeCase(activeCase.id, payload);
      onAnalyzeSuccess(res);
      setShowAnalyzeModal(false);
    } catch (err: unknown) {
      setAnalyzeError(err instanceof Error ? err.message : 'Ошибка при запуске анализа RCA');
    } finally {
      setAnalyzing(false);
    }
  };

  // Calculate storage metrics
  const quota = storageStatus?.quota_bytes && storageStatus.quota_bytes > 0
    ? storageStatus.quota_bytes
    : DISK_QUOTA_BYTES;
  const rawBytes = storageStatus?.raw_bytes ?? 0;
  const aggBytes = storageStatus?.agg_bytes ?? 0;
  const reportsBytes = storageStatus?.reports_bytes ?? 0;
  const stagingBytes = storageStatus?.staging_bytes ?? 0;
  const totalUsed = storageStatus?.total_bytes ?? (rawBytes + aggBytes + reportsBytes + stagingBytes);
  const freeBytes = Math.max(0, quota - totalUsed);

  const rawPercent = safePercent(rawBytes, quota);
  const aggPercent = safePercent(aggBytes, quota);
  const reportsPercent = safePercent(reportsBytes, quota);
  const stagingPercent = safePercent(stagingBytes, quota);
  const usedPercent = safePercent(totalUsed, quota);
  const freePercent = Math.max(0, 100 - (rawPercent + aggPercent + reportsPercent + stagingPercent));

  // Determine disk status color
  const diskStatusColor = usedPercent >= 85 ? '#ef4444' : usedPercent >= 70 ? '#f59e0b' : '#10b981';

  // Filter cases in modal
  const filteredCases = casesList.filter((c) => {
    const q = caseSearchQuery.toLowerCase().trim();
    if (!q) return true;
    return (
      c.title.toLowerCase().includes(q) ||
      (c.description && c.description.toLowerCase().includes(q)) ||
      c.id.toLowerCase().includes(q)
    );
  });

  const hypothesesCount = analysis?.hypotheses?.length ?? 0;
  const evidenceCount = analysis?.evidence_items?.length ?? 0;

  return (
    <>
      <header className="v2-top-nav">
        {/* Left Side: Brand + Active Case Selector */}
        <div className="v2-top-nav-left">
          <div className="v2-brand">
            <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M4 6h16M4 12h16M4 18h16" />
              <rect x="2" y="3" width="20" height="18" rx="2" />
            </svg>
            <span className="v2-brand-name">RCA Analyzer</span>
          </div>

          <div className="v2-case-picker-button-wrapper">
            <button
              type="button"
              className="v2-case-picker-btn"
              onClick={() => setShowCaseModal(true)}
              title="Открыть управление кейсами расследования"
            >
              <span className="v2-case-picker-icon">📁</span>
              <div className="v2-case-picker-info">
                <span className="v2-case-picker-label">Текущий кейс:</span>
                <span className="v2-case-picker-title">
                  {activeCase ? activeCase.title : 'Кейс не выбран'}
                </span>
              </div>
              {activeCase && (
                <span className={`v2-status-pill status-${activeCase.status.toLowerCase()}`}>
                  {activeCase.status}
                </span>
              )}
              <span className="v2-case-picker-action">Сменить ▾</span>
            </button>
          </div>
        </div>

        {/* Center: 5 Page Tabs */}
        <nav className="v2-nav-tabs">
          <button
            type="button"
            className={`v2-nav-tab ${activePage === 'ingest' ? 'active' : ''}`}
            onClick={() => onSelectPage('ingest')}
          >
            <span className="v2-tab-icon">📥</span>
            <span className="v2-tab-label">Загрузка и Tilt</span>
          </button>

          <button
            type="button"
            className={`v2-nav-tab ${activePage === 'hypotheses' ? 'active' : ''}`}
            onClick={() => onSelectPage('hypotheses')}
          >
            <span className="v2-tab-icon">🎯</span>
            <span className="v2-tab-label">Гипотезы RCA</span>
            {hypothesesCount > 0 && (
              <span className="v2-tab-badge badge-primary">{hypothesesCount}</span>
            )}
          </button>

          <button
            type="button"
            className={`v2-nav-tab ${activePage === 'topology' ? 'active' : ''}`}
            onClick={() => onSelectPage('topology')}
          >
            <span className="v2-tab-icon">🕸️</span>
            <span className="v2-tab-label">Граф и Таймлайн</span>
          </button>

          <button
            type="button"
            className={`v2-nav-tab ${activePage === 'chains' ? 'active' : ''}`}
            onClick={() => onSelectPage('chains')}
          >
            <span className="v2-tab-icon">🔍</span>
            <span className="v2-tab-label">Цепочки и Логи</span>
          </button>

          <button
            type="button"
            className={`v2-nav-tab ${activePage === 'evidence' ? 'active' : ''}`}
            onClick={() => onSelectPage('evidence')}
          >
            <span className="v2-tab-icon">📑</span>
            <span className="v2-tab-label">Улики и Gemini</span>
            {evidenceCount > 0 && (
              <span className="v2-tab-badge badge-evidence">{evidenceCount}</span>
            )}
          </button>
        </nav>

        {/* Right Side: RCA Trigger + Compact Quota Indicator */}
        <div className="v2-top-nav-right">
          <button
            type="button"
            className="v2-btn-analyze-quick"
            disabled={!activeCase || analyzing}
            onClick={() => setShowAnalyzeModal(true)}
            title={activeCase ? 'Открыть параметры запуска анализа RCA' : 'Выберите кейс для анализа'}
          >
            <span className="v2-analyze-icon">🚀</span>
            <span>Запустить RCA</span>
          </button>

          <button
            type="button"
            className="v2-disk-indicator-btn"
            onClick={() => setShowQuotaModal(true)}
            title="Открыть детальное управление дисковой квотой 15 ГБ"
          >
            <span className="v2-disk-dot" style={{ backgroundColor: diskStatusColor }} />
            <span className="v2-disk-text">
              💾 {formatBytes(totalUsed)} / {formatBytes(quota)}
            </span>
          </button>
        </div>
      </header>

      {/* MODAL 1: Case Management */}
      {showCaseModal && (
        <div className="v2-modal-overlay" onClick={() => setShowCaseModal(false)}>
          <div className="v2-modal-dialog v2-modal-xl" onClick={(e) => e.stopPropagation()}>
            <div className="v2-modal-header">
              <div className="v2-modal-title-group">
                <h3>📁 Управление кейсами расследования</h3>
                <span className="text-xs text-muted">
                  Выберите активный инцидент или создайте новый кейс для импорта логов
                </span>
              </div>
              <button
                type="button"
                className="btn-close-sm"
                onClick={() => setShowCaseModal(false)}
                title="Закрыть модальное окно"
              >
                ✕
              </button>
            </div>

            {caseError && (
              <div className="storage-error-banner mb-2">
                <span>⚠ {caseError}</span>
                <button type="button" className="btn-close-sm" onClick={() => setCaseError(null)}>✕</button>
              </div>
            )}

            <div className="v2-modal-body">
              {/* Form to create a new case */}
              <div className="v2-create-case-box">
                <h4 className="v2-box-subtitle">+ Создать новый кейс</h4>
                <form onSubmit={(e) => void handleCreateCase(e)} className="v2-create-case-form">
                  <div className="v2-form-row">
                    <input
                      type="text"
                      className="input-text v2-input-flex"
                      placeholder="Название кейса (например: Incident 2026-10-30 Chat Service Degradation)"
                      value={newTitle}
                      onChange={(e) => setNewTitle(e.target.value)}
                      required
                    />
                    <button
                      type="submit"
                      className="btn btn-primary"
                      disabled={creatingCase || !newTitle.trim()}
                    >
                      {creatingCase ? 'Создание...' : 'Создать кейс'}
                    </button>
                  </div>
                  <input
                    type="text"
                    className="input-text w-full mt-1"
                    placeholder="Описание инцидента, гипотезы или затронутые симптомы (опционально)"
                    value={newDesc}
                    onChange={(e) => setNewDesc(e.target.value)}
                  />
                </form>
              </div>

              {/* Case Search & Filter */}
              <div className="v2-cases-filter-bar">
                <input
                  type="text"
                  className="input-text w-full"
                  placeholder="🔍 Поиск по названию, описанию или ID кейса..."
                  value={caseSearchQuery}
                  onChange={(e) => setCaseSearchQuery(e.target.value)}
                />
              </div>

              {/* Cases Grid */}
              <div className="v2-cases-grid-wrapper">
                {loadingCases && (
                  <div className="rca-loading-box">
                    <span className="storage-spinner">⟳</span>
                    <span>Загрузка списка кейсов...</span>
                  </div>
                )}

                {!loadingCases && filteredCases.length === 0 && (
                  <div className="empty-hint">
                    {casesList.length === 0
                      ? 'Нет сохранённых кейсов. Создайте свой первый кейс выше!'
                      : 'По вашему запросу кейсов не найдено.'}
                  </div>
                )}

                <div className="v2-cases-cards-grid">
                  {filteredCases.map((c) => {
                    const isActive = activeCase?.id === c.id;
                    return (
                      <div
                        key={c.id}
                        className={`v2-case-card ${isActive ? 'v2-case-card-active' : ''}`}
                        onClick={() => {
                          onSelectCase(c);
                          setShowCaseModal(false);
                        }}
                      >
                        <div className="v2-case-card-top">
                          <span className="v2-case-card-title" title={c.title}>
                            {c.title}
                          </span>
                          <span className={`v2-status-pill status-${c.status.toLowerCase()}`}>
                            {c.status}
                          </span>
                        </div>

                        <p className="v2-case-card-desc">
                          {c.description ? c.description : 'Без описания'}
                        </p>

                        <div className="v2-case-card-bottom">
                          <div className="v2-case-card-meta">
                            <span>ID: <code>{c.id.substring(0, 8)}...</code></span>
                            <span>{new Date(c.created_at).toLocaleDateString()}</span>
                          </div>

                          <div className="v2-case-card-actions" onClick={(e) => e.stopPropagation()}>
                            <button
                              type="button"
                              className="btn btn-ghost-sm"
                              onClick={() => {
                                onSelectCase(c);
                                setShowCaseModal(false);
                              }}
                            >
                              {isActive ? '✓ Выбран' : 'Выбрать'}
                            </button>
                            <button
                              type="button"
                              className="btn-icon-xs text-danger"
                              title="Удалить кейс"
                              onClick={() => void handleDeleteCase(c.id, c.title)}
                            >
                              🗑️
                            </button>
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </div>
            </div>

            <div className="v2-modal-footer">
              <span className="text-xs text-muted">Всего кейсов: {casesList.length}</span>
              <button
                type="button"
                className="btn btn-secondary"
                onClick={() => setShowCaseModal(false)}
              >
                Закрыть
              </button>
            </div>
          </div>
        </div>
      )}

      {/* MODAL 2: Disk Quota Management (15 GB) */}
      {showQuotaModal && (
        <div className="v2-modal-overlay" onClick={() => setShowQuotaModal(false)}>
          <div className="v2-modal-dialog v2-modal-lg" onClick={(e) => e.stopPropagation()}>
            <div className="v2-modal-header">
              <div className="v2-modal-title-group">
                <h3>💾 Управление дисковой квотой (15 ГБ)</h3>
                <span className="text-xs text-muted">
                  Контроль распределения слоёв хранения в ClickHouse и очистка сырых логов
                </span>
              </div>
              <button
                type="button"
                className="btn-close-sm"
                onClick={() => setShowQuotaModal(false)}
                title="Закрыть"
              >
                ✕
              </button>
            </div>

            {storageError && (
              <div className="storage-error-banner mb-2">
                <span>⚠ {storageError}</span>
                <button type="button" className="btn-close-sm" onClick={() => setStorageError(null)}>✕</button>
              </div>
            )}

            {purgeResult && (
              <div className="purge-success-badge mb-2">
                ✓ Успешно освобождено: <strong>{formatBytes(purgeResult.freed_bytes)}</strong> ({purgeResult.duration_ms} мс)
              </div>
            )}

            <div className="v2-modal-body">
              {/* Large Quota Progress Bar */}
              <div className="v2-quota-bar-large-wrapper">
                <div className="v2-quota-bar-large" role="progressbar" aria-valuenow={totalUsed} aria-valuemin={0} aria-valuemax={quota}>
                  {rawPercent > 0 && (
                    <div
                      className="storage-segment segment-raw"
                      style={{ width: `${rawPercent}%` }}
                      title={`Сырые логи: ${formatBytes(rawBytes)} (${rawPercent.toFixed(1)}%)`}
                    />
                  )}
                  {aggPercent > 0 && (
                    <div
                      className="storage-segment segment-agg"
                      style={{ width: `${aggPercent}%` }}
                      title={`Агрегаты и шаблоны: ${formatBytes(aggBytes)} (${aggPercent.toFixed(1)}%)`}
                    />
                  )}
                  {reportsPercent > 0 && (
                    <div
                      className="storage-segment segment-reports"
                      style={{ width: `${reportsPercent}%` }}
                      title={`Отчёты RCA: ${formatBytes(reportsBytes)} (${reportsPercent.toFixed(1)}%)`}
                    />
                  )}
                  {freePercent > 0 && (
                    <div
                      className="storage-segment segment-free"
                      style={{ width: `${freePercent}%` }}
                      title={`Свободно: ${formatBytes(freeBytes)} (${freePercent.toFixed(1)}%)`}
                    />
                  )}
                </div>
              </div>

              {/* Detailed Storage Breakdown Cards */}
              <div className="v2-storage-breakdown-grid">
                <div className="v2-storage-stat-card">
                  <div className="v2-stat-header">
                    <span className="legend-dot dot-raw" />
                    <span className="v2-stat-title">Сырые логи (events_raw)</span>
                  </div>
                  <div className="v2-stat-value">{formatBytes(rawBytes)}</div>
                  <div className="v2-stat-meta">{rawPercent.toFixed(1)}% от квоты</div>
                </div>

                <div className="v2-storage-stat-card">
                  <div className="v2-stat-header">
                    <span className="legend-dot dot-agg" />
                    <span className="v2-stat-title">Агрегаты и шаблоны</span>
                  </div>
                  <div className="v2-stat-value">{formatBytes(aggBytes)}</div>
                  <div className="v2-stat-meta">{aggPercent.toFixed(1)}% от квоты</div>
                </div>

                <div className="v2-storage-stat-card">
                  <div className="v2-stat-header">
                    <span className="legend-dot dot-reports" />
                    <span className="v2-stat-title">Отчёты RCA (reports)</span>
                  </div>
                  <div className="v2-stat-value">{formatBytes(reportsBytes)}</div>
                  <div className="v2-stat-meta">{reportsPercent.toFixed(1)}% от квоты</div>
                </div>

                <div className="v2-storage-stat-card">
                  <div className="v2-stat-header">
                    <span className="legend-dot dot-free" />
                    <span className="v2-stat-title">Свободно в квоте</span>
                  </div>
                  <div className="v2-stat-value">{formatBytes(freeBytes)}</div>
                  <div className="v2-stat-meta">{freePercent.toFixed(1)}% свободно</div>
                </div>
              </div>

              {/* Purge Raw Logs Action Section */}
              <div className="v2-purge-section-box">
                <div className="v2-purge-info">
                  <h4>🗑️ Очистка сырых логов (Purge Raw)</h4>
                  <p className="text-sm text-muted">
                    Удаляет неагрегированные строки из таблицы <code>events_raw</code>, освобождая дисковое пространство.
                    При этом все агрегаты (1 мин), шаблоны Drain, граф топологии и сгенерированные отчёты RCA сохраняются на 100%.
                  </p>
                </div>

                {!showPurgeConfirm ? (
                  <button
                    type="button"
                    className="btn btn-purge"
                    disabled={purging || rawBytes === 0}
                    onClick={() => setShowPurgeConfirm(true)}
                  >
                    {rawBytes === 0 ? 'Сырые логи пусты' : `Очистить сырые логи (${formatBytes(rawBytes)})`}
                  </button>
                ) : (
                  <div className="confirm-purge-box">
                    <span className="confirm-text">Удалить {formatBytes(rawBytes)} сырых логов?</span>
                    <button
                      type="button"
                      className="btn btn-danger-sm"
                      onClick={() => void handlePurge()}
                      disabled={purging}
                    >
                      {purging ? 'Очистка...' : 'Да, подтвердить'}
                    </button>
                    <button
                      type="button"
                      className="btn btn-ghost-sm"
                      onClick={() => setShowPurgeConfirm(false)}
                      disabled={purging}
                    >
                      Отмена
                    </button>
                  </div>
                )}
              </div>
            </div>

            <div className="v2-modal-footer">
              <button
                type="button"
                className="btn btn-ghost"
                onClick={() => void fetchStorageStatus()}
                disabled={loadingStorage}
              >
                {loadingStorage ? 'Обновление...' : '🔄 Обновить статус'}
              </button>
              <button
                type="button"
                className="btn btn-secondary"
                onClick={() => setShowQuotaModal(false)}
              >
                Закрыть
              </button>
            </div>
          </div>
        </div>
      )}

      {/* MODAL 3: RCA Analysis Launcher */}
      {showAnalyzeModal && activeCase && (
        <div className="v2-modal-overlay" onClick={() => setShowAnalyzeModal(false)}>
          <div className="v2-modal-dialog v2-modal-md" onClick={(e) => e.stopPropagation()}>
            <div className="v2-modal-header">
              <div className="v2-modal-title-group">
                <h3>🚀 Запуск RCA Анализа</h3>
                <span className="text-xs text-muted">
                  Кейс: <strong>{activeCase.title}</strong>
                </span>
              </div>
              <button
                type="button"
                className="btn-close-sm"
                onClick={() => setShowAnalyzeModal(false)}
                title="Закрыть"
              >
                ✕
              </button>
            </div>

            {analyzeError && (
              <div className="storage-error-banner mb-2">
                <span>⚠ {analyzeError}</span>
                <button type="button" className="btn-close-sm" onClick={() => setAnalyzeError(null)}>✕</button>
              </div>
            )}

            <div className="v2-modal-body">
              <div className="v2-analyze-options">
                <label className="checkbox-label mb-2">
                  <input
                    type="checkbox"
                    checked={useHistoricalBaseline}
                    onChange={(e) => setUseHistoricalBaseline(e.target.checked)}
                  />
                  <span>Использовать исторический baseline (до 7 дней) для статистических тестов</span>
                </label>

                <div className="v2-time-range-group">
                  <span className="text-xs font-semibold text-muted">Временной диапазон инцидента (опционально):</span>
                  <div className="v2-time-inputs-row">
                    <div className="v2-time-col">
                      <label className="text-xs text-sub">Начало:</label>
                      <input
                        type="datetime-local"
                        className="input-text w-full text-xs"
                        value={incidentStart}
                        onChange={(e) => setIncidentStart(e.target.value)}
                      />
                    </div>
                    <div className="v2-time-col">
                      <label className="text-xs text-sub">Конец:</label>
                      <input
                        type="datetime-local"
                        className="input-text w-full text-xs"
                        value={incidentEnd}
                        onChange={(e) => setIncidentEnd(e.target.value)}
                      />
                    </div>
                  </div>
                </div>

                <div className="v2-analyze-summary-hint text-xs text-muted mt-2">
                  Алгоритм выполнит 8-факторный скоринг, построит причинно-следственные связи,
                  выявит рассинхронизацию часов и сформирует ранжированные гипотезы.
                </div>
              </div>
            </div>

            <div className="v2-modal-footer">
              <button
                type="button"
                className="btn btn-secondary"
                onClick={() => setShowAnalyzeModal(false)}
                disabled={analyzing}
              >
                Отмена
              </button>
              <button
                type="button"
                className="btn btn-primary"
                onClick={() => void handleAnalyze()}
                disabled={analyzing}
              >
                {analyzing ? 'Выполняется анализ...' : 'Начать расследование'}
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
};
