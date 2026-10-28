import React, { useState, useMemo, useCallback } from 'react';
import { api, EvidenceItem } from '../api/client';

export interface EvidenceAndGeminiPanelProps {
  caseId: string;
  runId?: string;
  evidenceItems?: EvidenceItem[];
  loading?: boolean;
}

export const EvidenceAndGeminiPanel: React.FC<EvidenceAndGeminiPanelProps> = ({
  caseId,
  runId = 'latest',
  evidenceItems = [],
  loading = false,
}) => {
  // Search and filter state
  const [filterText, setFilterText] = useState<string>('');
  const [selectedKind, setSelectedKind] = useState<string>('ALL');
  const [copiedLogQLId, setCopiedLogQLId] = useState<string | null>(null);

  // Gemini Modal state
  const [isGeminiModalOpen, setIsGeminiModalOpen] = useState<boolean>(false);
  const [geminiBundleText, setGeminiBundleText] = useState<string>('');
  const [geminiLoading, setGeminiLoading] = useState<boolean>(false);
  const [geminiCopied, setGeminiCopied] = useState<boolean>(false);
  const [geminiError, setGeminiError] = useState<string | null>(null);

  // Filter evidence items
  const filteredItems = useMemo(() => {
    return evidenceItems.filter((item) => {
      if (selectedKind !== 'ALL' && item.kind !== selectedKind) {
        return false;
      }
      if (filterText.trim()) {
        const query = filterText.toLowerCase();
        const matchesId = item.id.toLowerCase().includes(query);
        const matchesSvc = item.service.toLowerCase().includes(query);
        const matchesSummary = item.summary.toLowerCase().includes(query);
        const matchesLine = (item.redacted_line || '').toLowerCase().includes(query);
        const matchesFile = (item.source_file || '').toLowerCase().includes(query);
        const matchesSha = (item.sha256 || '').toLowerCase().includes(query);
        return matchesId || matchesSvc || matchesSummary || matchesLine || matchesFile || matchesSha;
      }
      return true;
    });
  }, [evidenceItems, selectedKind, filterText]);

  // Unique kinds for filter
  const kinds = useMemo(() => {
    const set = new Set<string>();
    evidenceItems.forEach((i) => {
      if (i.kind) set.add(i.kind);
    });
    return Array.from(set);
  }, [evidenceItems]);

  // 1. Copy LogQL snippet handler
  const handleCopyLogQL = async (item: EvidenceItem) => {
    if (!item.logql_snippet) return;
    try {
      await navigator.clipboard.writeText(item.logql_snippet);
      setCopiedLogQLId(item.id);
      setTimeout(() => {
        setCopiedLogQLId((prev) => (prev === item.id ? null : prev));
      }, 2000);
    } catch {
      // Fallback
    }
  };

  // 2. Fetch Gemini Bundle handler
  const handleOpenGeminiModal = useCallback(async () => {
    setIsGeminiModalOpen(true);
    setGeminiCopied(false);
    setGeminiError(null);
    setGeminiLoading(true);
    try {
      const url = api.getReportUrl(caseId, runId, 'gemini');
      const res = await fetch(url);
      if (!res.ok) {
        throw new Error(`Ошибка загрузки пакета Gemini: HTTP ${res.status}`);
      }
      const text = await res.text();
      setGeminiBundleText(text);
    } catch (err: unknown) {
      setGeminiError(err instanceof Error ? err.message : 'Не удалось загрузить пакет Gemini');
      setGeminiBundleText('');
    } finally {
      setGeminiLoading(false);
    }
  }, [caseId, runId]);

  // 3. 1-Click Copy for Gemini
  const handleCopyGeminiPrompt = async () => {
    if (!geminiBundleText) return;
    try {
      await navigator.clipboard.writeText(geminiBundleText);
      setGeminiCopied(true);
      setTimeout(() => setGeminiCopied(false), 3500);
    } catch {
      // Fallback
    }
  };

  const mdUrl = api.getReportUrl(caseId, runId, 'markdown');
  const htmlUrl = api.getReportUrl(caseId, runId, 'html');
  const zipUrl = api.getReportUrl(caseId, runId, 'zip');
  const geminiUrl = api.getReportUrl(caseId, runId, 'gemini');

  return (
    <div className="evidence-panel-container">
      {/* 1. Header with Report Download buttons and Gemini trigger */}
      <div className="evidence-header-card">
        <div className="evidence-header-left">
          <h3>Доказательства инцидента и отчёты RCA</h3>
          <span className="text-xs text-muted">
            Кейс: <strong>{caseId}</strong> | Прогон: <strong>{runId}</strong> | Всего улик:{' '}
            <strong>{evidenceItems.length}</strong>
          </span>
        </div>

        <div className="reports-download-group">
          <a
            href={mdUrl}
            target="_blank"
            rel="noopener noreferrer"
            download={`report_${caseId}.md`}
            className="btn btn-secondary-sm"
            title="Скачать структурированный отчет в формате Markdown"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
              <polyline points="14 2 14 8 20 8" />
              <line x1="16" y1="13" x2="8" y2="13" />
              <line x1="16" y1="17" x2="8" y2="17" />
              <polyline points="10 9 9 9 8 9" />
            </svg>
            report.md
          </a>

          <a
            href={htmlUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="btn btn-secondary-sm"
            title="Открыть интерактивный автономный HTML-отчет в новой вкладке"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <circle cx="12" cy="12" r="10" />
              <line x1="2" y1="12" x2="22" y2="12" />
              <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
            </svg>
            report.html
          </a>

          <a
            href={zipUrl}
            target="_blank"
            rel="noopener noreferrer"
            download={`evidence_${caseId}.zip`}
            className="btn btn-secondary-sm"
            title="Скачать полный ZIP архив с отчетами, графиками и уликами"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
              <polyline points="7 10 12 15 17 10" />
              <line x1="12" y1="15" x2="12" y2="3" />
            </svg>
            evidence.zip
          </a>

          <button
            type="button"
            className="btn btn-gemini-trigger"
            onClick={() => void handleOpenGeminiModal()}
            title="Открыть пакет промпта и улик для передачи в Google Gemini"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M12 2L15 9L22 12L15 15L12 22L9 15L2 12L9 9L12 2Z" />
            </svg>
            ✨ Пакет для Google Gemini
          </button>
        </div>
      </div>

      {/* 2. Filters & Search toolbar */}
      <div className="evidence-filter-bar">
        <input
          type="text"
          className="input-text evidence-search-input"
          placeholder="Поиск по уликам (файл, SHA-256, сервис, текст строки)..."
          value={filterText}
          onChange={(e) => setFilterText(e.target.value)}
        />

        <div className="filter-kind-group">
          <button
            type="button"
            className={`btn-chip ${selectedKind === 'ALL' ? 'chip-active' : ''}`}
            onClick={() => setSelectedKind('ALL')}
          >
            Все ({evidenceItems.length})
          </button>
          {kinds.map((k) => (
            <button
              key={k}
              type="button"
              className={`btn-chip ${selectedKind === k ? 'chip-active' : ''}`}
              onClick={() => setSelectedKind(k)}
            >
              {k}
            </button>
          ))}
        </div>
      </div>

      {/* 3. Evidence Table E-01..E-NN */}
      <div className="evidence-table-card">
        {loading ? (
          <div className="rca-loading-box">
            <span className="spinner-icon">⟳</span> Загрузка таблицы улик...
          </div>
        ) : filteredItems.length === 0 ? (
          <div className="empty-hint p-4">
            {evidenceItems.length === 0
              ? 'Улики для данного прогона пока не сгенерированы. Запустите анализ RCA.'
              : 'Ни одна улика не соответствует критериям фильтра.'}
          </div>
        ) : (
          <div className="evidence-table-scroll">
            <table className="evidence-table">
              <thead>
                <tr>
                  <th style={{ width: '65px' }}>ID</th>
                  <th style={{ width: '90px' }}>Вид</th>
                  <th style={{ width: '120px' }}>Сервис</th>
                  <th style={{ width: '100px' }}>Время</th>
                  <th style={{ width: '150px' }}>Файл источника</th>
                  <th style={{ width: '70px' }}>№ строки</th>
                  <th style={{ width: '90px' }}>SHA-256</th>
                  <th>Обезличенная строка (Redacted Line)</th>
                  <th style={{ width: '120px' }}>LogQL</th>
                </tr>
              </thead>
              <tbody>
                {filteredItems.map((item: EvidenceItem) => {
                  const isCopied = copiedLogQLId === item.id;
                  const shortSha = item.sha256 ? `${item.sha256.slice(0, 8)}…` : '-';
                  const shortFile = item.source_file ? item.source_file.split(/[/\\]/).pop() : '-';

                  return (
                    <tr key={item.id} className="evidence-row">
                      <td className="font-mono font-semibold text-accent">{item.id}</td>
                      <td>
                        <span className={`kind-pill kind-${item.kind}`}>{item.kind}</span>
                      </td>
                      <td className="font-semibold text-xs">{item.service}</td>
                      <td className="font-mono text-xs text-muted">
                        {item.timestamp ? new Date(item.timestamp).toLocaleTimeString() : '-'}
                      </td>
                      <td className="font-mono text-xs" title={item.source_file}>
                        {shortFile}
                      </td>
                      <td className="font-mono text-xs text-muted">
                        {item.source_record_idx > 0 ? `#${item.source_record_idx}` : '-'}
                      </td>
                      <td className="font-mono text-xs" title={item.sha256}>
                        {shortSha}
                      </td>
                      <td className="redacted-line-cell">
                        <div className="redacted-line-text" title={item.redacted_line}>
                          <code>{item.redacted_line || item.summary}</code>
                        </div>
                      </td>
                      <td>
                        {item.logql_snippet ? (
                          <button
                            type="button"
                            className={`btn-copy-logql ${isCopied ? 'btn-copied' : ''}`}
                            onClick={() => void handleCopyLogQL(item)}
                            title={`Скопировать LogQL: ${item.logql_snippet}`}
                          >
                            {isCopied ? '✓ Скопировано' : 'Скопировать LogQL'}
                          </button>
                        ) : (
                          <span className="text-muted text-xs">-</span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* --- Modal: Package for Google Gemini --- */}
      {isGeminiModalOpen && (
        <div className="modal-backdrop" onClick={() => setIsGeminiModalOpen(false)}>
          <div className="modal-content modal-content-wide gemini-modal" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <div className="gemini-title-area">
                <span className="gemini-sparkle-icon">✨</span>
                <div>
                  <h3>Пакет доказательств для Google Gemini</h3>
                  <span className="text-xs text-muted">
                    Автономный бандл: инцидент, роли гипотез, связи Lead-Lag, улики E-01..E-NN
                  </span>
                </div>
              </div>
              <button type="button" className="btn-close-sm" onClick={() => setIsGeminiModalOpen(false)}>
                ✕
              </button>
            </div>

            {/* 1-Click Action Bar */}
            <div className="gemini-actions-bar">
              <button
                type="button"
                className={`btn btn-gemini-copy-big ${geminiCopied ? 'btn-gemini-copied' : ''}`}
                onClick={() => void handleCopyGeminiPrompt()}
                disabled={geminiLoading || !geminiBundleText}
              >
                {geminiCopied ? (
                  <>
                    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                      <polyline points="20 6 9 17 4 12" />
                    </svg>
                    ✓ Скопировано в буфер обмена! Вставьте в Gemini (Ctrl+V)
                  </>
                ) : (
                  <>
                    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                      <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
                      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
                    </svg>
                    Скопировать промпт и доказательства для Gemini
                  </>
                )}
              </button>

              <a
                href={geminiUrl}
                target="_blank"
                rel="noopener noreferrer"
                download={`gemini_bundle_${caseId}.md`}
                className="btn btn-secondary-sm"
                title="Скачать исходный markdown файл бандла"
              >
                Скачать gemini_bundle.md
              </a>
            </div>

            {/* Bundle Content preview */}
            {geminiLoading ? (
              <div className="rca-loading-box">
                <span className="spinner-icon">⟳</span> Формирование пакета для Gemini...
              </div>
            ) : geminiError ? (
              <div className="sidebar-error-toast">{geminiError}</div>
            ) : (
              <div className="gemini-preview-wrapper">
                <pre className="gemini-code-preview">{geminiBundleText}</pre>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
};
