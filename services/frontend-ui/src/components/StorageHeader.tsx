import React, { useState, useEffect, useCallback } from 'react';
import { api, StorageStatusResponse, PurgeRawResponse } from '../api/client';

export interface StorageHeaderProps {
  status?: StorageStatusResponse | null;
  onRefresh?: () => void;
  onPurgeSuccess?: (res: PurgeRawResponse) => void;
}

export const DISK_QUOTA_BYTES = 15 * 1024 * 1024 * 1024; // 15 GB

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / Math.pow(1024, i);
  return `${value.toFixed(value >= 10 || i === 0 ? 1 : 2)} ${units[i]}`;
}

export const StorageHeader: React.FC<StorageHeaderProps> = ({
  status: initialStatus,
  onRefresh,
  onPurgeSuccess,
}) => {
  const [status, setStatus] = useState<StorageStatusResponse | null>(initialStatus || null);
  const [loading, setLoading] = useState<boolean>(false);
  const [purging, setPurging] = useState<boolean>(false);
  const [showConfirm, setShowConfirm] = useState<boolean>(false);
  const [lastPurgeResult, setLastPurgeResult] = useState<PurgeRawResponse | null>(null);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  const fetchStatus = useCallback(async () => {
    setLoading(true);
    setErrorMessage(null);
    try {
      const data = await api.getStorageStatus();
      setStatus(data);
      if (onRefresh) {
        onRefresh();
      }
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Не удалось получить статус диска';
      setErrorMessage(msg);
    } finally {
      setLoading(false);
    }
  }, [onRefresh]);

  useEffect(() => {
    if (initialStatus) {
      setStatus(initialStatus);
    } else {
      void fetchStatus();
    }
  }, [initialStatus, fetchStatus]);

  const handlePurge = async () => {
    setPurging(true);
    setErrorMessage(null);
    setShowConfirm(false);
    try {
      const res = await api.purgeRaw();
      setLastPurgeResult(res);
      if (onPurgeSuccess) {
        onPurgeSuccess(res);
      }
      await fetchStatus();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Ошибка при очистке сырых логов';
      setErrorMessage(msg);
    } finally {
      setPurging(false);
    }
  };

  const quota = status?.quota_bytes && status.quota_bytes > 0 ? status.quota_bytes : DISK_QUOTA_BYTES;
  const rawBytes = status?.raw_bytes ?? 0;
  const aggBytes = status?.agg_bytes ?? 0;
  const reportsBytes = status?.reports_bytes ?? 0;
  const totalUsed = status?.total_bytes ?? (rawBytes + aggBytes + reportsBytes);
  const freeBytes = Math.max(0, quota - totalUsed);

  const rawPercent = Math.min(100, Math.max(0, (rawBytes / quota) * 100));
  const aggPercent = Math.min(100, Math.max(0, (aggBytes / quota) * 100));
  const reportsPercent = Math.min(100, Math.max(0, (reportsBytes / quota) * 100));
  const freePercent = Math.max(0, 100 - (rawPercent + aggPercent + reportsPercent));

  const isNearLimit = totalUsed >= quota * 0.85;

  return (
    <header className="storage-header">
      <div className="storage-header-main">
        <div className="storage-title-area">
          <div className="storage-logo">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M4 6h16M4 12h16M4 18h16" />
              <rect x="2" y="3" width="20" height="18" rx="2" />
            </svg>
            <span className="app-title">RCA Logs Analyzer</span>
          </div>
          <span className="storage-budget-badge">
            Бюджет: <strong>15 ГБ</strong> ({formatBytes(totalUsed)} / {formatBytes(quota)})
          </span>
          {loading && <span className="storage-spinner" title="Обновление статуса...">⟳</span>}
        </div>

        <div className="storage-actions">
          {lastPurgeResult && (
            <div className="purge-success-badge" title="Результат последней очистки">
              ✓ Освобождено: <strong>{formatBytes(lastPurgeResult.freed_bytes)}</strong> ({lastPurgeResult.duration_ms} мс)
            </div>
          )}

          {!showConfirm ? (
            <button
              type="button"
              className="btn btn-purge"
              disabled={purging || rawBytes === 0}
              onClick={() => setShowConfirm(true)}
              title={rawBytes === 0 ? 'Сырые логи уже очищены' : 'Удалить сырые строки логов, сохранив агрегаты и отчёты'}
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <polyline points="3 6 5 6 21 6" />
                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                <line x1="10" y1="11" x2="10" y2="17" />
                <line x1="14" y1="11" x2="14" y2="17" />
              </svg>
              {purging ? 'Очистка...' : 'Очистить сырые логи'}
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
                Да, очистить
              </button>
              <button
                type="button"
                className="btn btn-ghost-sm"
                onClick={() => setShowConfirm(false)}
                disabled={purging}
              >
                Отмена
              </button>
            </div>
          )}

          <button
            type="button"
            className="btn btn-icon"
            onClick={() => void fetchStatus()}
            disabled={loading}
            title="Обновить статус хранилища"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <polyline points="23 4 23 10 17 10" />
              <polyline points="1 20 1 14 7 14" />
              <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
            </svg>
          </button>
        </div>
      </div>

      {errorMessage && (
        <div className="storage-error-banner">
          <span>⚠ {errorMessage}</span>
          <button type="button" className="btn-close-sm" onClick={() => setErrorMessage(null)}>✕</button>
        </div>
      )}

      {/* Шкала распределения диска 15 ГБ */}
      <div className="storage-bar-container">
        <div className="storage-bar" role="progressbar" aria-valuenow={totalUsed} aria-valuemin={0} aria-valuemax={quota}>
          {rawPercent > 0 && (
            <div
              className="storage-segment segment-raw"
              style={{ width: `${rawPercent}%` }}
              title={`Сырые логи (events_raw): ${formatBytes(rawBytes)} (${rawPercent.toFixed(1)}%)`}
            />
          )}
          {aggPercent > 0 && (
            <div
              className="storage-segment segment-agg"
              style={{ width: `${aggPercent}%` }}
              title={`Агрегаты и шаблоны (events_agg, templates): ${formatBytes(aggBytes)} (${aggPercent.toFixed(1)}%)`}
            />
          )}
          {reportsPercent > 0 && (
            <div
              className="storage-segment segment-reports"
              style={{ width: `${reportsPercent}%` }}
              title={`Сгенерированные отчёты (reports): ${formatBytes(reportsBytes)} (${reportsPercent.toFixed(1)}%)`}
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

        <div className="storage-legend">
          <div className="legend-item">
            <span className="legend-dot dot-raw" />
            <span className="legend-label">Сырые логи:</span>
            <span className="legend-value">{formatBytes(rawBytes)}</span>
          </div>
          <div className="legend-item">
            <span className="legend-dot dot-agg" />
            <span className="legend-label">Агрегаты и шаблоны:</span>
            <span className="legend-value">{formatBytes(aggBytes)}</span>
          </div>
          <div className="legend-item">
            <span className="legend-dot dot-reports" />
            <span className="legend-label">Отчёты RCA:</span>
            <span className="legend-value">{formatBytes(reportsBytes)}</span>
          </div>
          <div className="legend-item">
            <span className="legend-dot dot-free" />
            <span className="legend-label">Свободно:</span>
            <span className="legend-value">{formatBytes(freeBytes)}</span>
          </div>
          {isNearLimit && (
            <span className="quota-warning-badge">
              ⚠ Внимание: занято более 85% дискового бюджета! Рекомендуется очистить сырые логи.
            </span>
          )}
        </div>
      </div>
    </header>
  );
};
