import React, { useState, useMemo, useCallback, useEffect, useRef } from 'react';
import { api, EvidenceItem } from '../api/client';

export interface EvidenceAndGeminiPanelProps {
  caseId: string;
  runId: string;
  evidenceItems: EvidenceItem[];
  highlightEvidenceId?: string;
  loading?: boolean;
}

function formatEvidenceTime(isoStr?: string): string {
  if (!isoStr) return '-';
  try {
    const d = new Date(isoStr);
    if (Number.isNaN(d.getTime())) return isoStr;
    const timeStr = d.toLocaleTimeString([], {
      hour12: false,
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    });
    const ms = String(d.getMilliseconds()).padStart(3, '0');
    return `${timeStr}.${ms}`;
  } catch {
    return isoStr;
  }
}

export const EvidenceAndGeminiPanel: React.FC<EvidenceAndGeminiPanelProps> = ({
  caseId,
  runId = 'latest',
  evidenceItems = [],
  highlightEvidenceId,
  loading = false,
}) => {
  // Search and filter state
  const [filterText, setFilterText] = useState<string>('');
  const [selectedKind, setSelectedKind] = useState<string>('ALL');

  // Copied states
  const [copiedLogQLId, setCopiedLogQLId] = useState<string | null>(null);
  const [copiedModalField, setCopiedModalField] = useState<string | null>(null);

  // Evidence Inspector Modal state
  const [inspectedItem, setInspectedItem] = useState<EvidenceItem | null>(null);

  // Gemini Modal state
  const [isGeminiModalOpen, setIsGeminiModalOpen] = useState<boolean>(false);
  const [geminiBundleText, setGeminiBundleText] = useState<string>('');
  const [geminiLoading, setGeminiLoading] = useState<boolean>(false);
  const [geminiCopied, setGeminiCopied] = useState<boolean>(false);
  const [geminiError, setGeminiError] = useState<string | null>(null);

  // Ref to highlighted row for scrolling
  const highlightedRowRef = useRef<HTMLTableRowElement | null>(null);

  // Auto-highlight or inspect item if highlightEvidenceId is provided
  useEffect(() => {
    if (highlightEvidenceId && evidenceItems.length > 0) {
      const match = evidenceItems.find((e) => e.id === highlightEvidenceId);
      if (match) {
        setInspectedItem(match);
      }
    }
  }, [highlightEvidenceId, evidenceItems]);

  // Scroll to highlighted row when table renders
  useEffect(() => {
    if (highlightedRowRef.current) {
      highlightedRowRef.current.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }
  }, [highlightEvidenceId, evidenceItems]);

  // Unique kinds for filter pills
  const kinds = useMemo(() => {
    const set = new Set<string>();
    evidenceItems.forEach((i) => {
      if (i.kind && i.kind.trim()) set.add(i.kind.trim());
    });
    return Array.from(set).sort();
  }, [evidenceItems]);

  // Filtered evidence items
  const filteredItems = useMemo(() => {
    return evidenceItems.filter((item) => {
      if (selectedKind !== 'ALL' && item.kind !== selectedKind) {
        return false;
      }
      if (filterText.trim()) {
        const query = filterText.toLowerCase();
        const matchesId = item.id.toLowerCase().includes(query);
        const matchesSvc = (item.service || '').toLowerCase().includes(query);
        const matchesSummary = (item.summary || '').toLowerCase().includes(query);
        const matchesLine = (item.redacted_line || '').toLowerCase().includes(query);
        const matchesFile = (item.source_file || '').toLowerCase().includes(query);
        const matchesSha = (item.sha256 || '').toLowerCase().includes(query);
        const matchesKind = (item.kind || '').toLowerCase().includes(query);
        return matchesId || matchesSvc || matchesSummary || matchesLine || matchesFile || matchesSha || matchesKind;
      }
      return true;
    });
  }, [evidenceItems, selectedKind, filterText]);

  // 1. Copy LogQL snippet handler
  const handleCopyLogQL = async (item: EvidenceItem, e?: React.MouseEvent) => {
    if (e) e.stopPropagation();
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

  // 2. Copy generic text with feedback key
  const handleCopyText = async (text: string, fieldKey: string) => {
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopiedModalField(fieldKey);
      setTimeout(() => {
        setCopiedModalField((prev) => (prev === fieldKey ? null : prev));
      }, 2000);
    } catch {
      // Fallback
    }
  };

  // 3. Fetch Gemini Bundle handler
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

  // 4. 1-Click Copy for Gemini prompt
  const handleCopyGeminiPrompt = async () => {
    if (!geminiBundleText) return;
    try {
      await navigator.clipboard.writeText(geminiBundleText);
      setGeminiCopied(true);
      setTimeout(() => setGeminiCopied(false), 3000);
    } catch {
      // Fallback
    }
  };

  const mdUrl = api.getReportUrl(caseId, runId, 'markdown');
  const htmlUrl = api.getReportUrl(caseId, runId, 'html');
  const zipUrl = api.getReportUrl(caseId, runId, 'zip');
  const geminiUrl = api.getReportUrl(caseId, runId, 'gemini');

  return (
    <div className="v2-evidence-page-container">
      {/* ===================================================================== */}
      {/* 1. TOP EXPORT BLOCK: 4 Large Cards in a Row */}
      {/* ===================================================================== */}
      <div className="v2-export-cards-grid">
        {/* Card 1: Google Gemini Bundle */}
        <div className="v2-export-card v2-export-card-gemini">
          <div className="v2-export-card-header">
            <div className="v2-export-card-icon gemini-icon">✨</div>
            <span className="v2-export-card-badge badge-gemini">RCA & PII-Safe</span>
          </div>

          <div className="v2-export-card-body">
            <h4 className="v2-export-card-title">Пакет для Google Gemini</h4>
            <p className="v2-export-card-desc">
              Специально структурированный Markdown-бандл (<code>gemini_bundle.md</code>) с обезличенными уликами,
              гипотезами и графом связей.
            </p>
          </div>

          <div className="v2-export-card-footer">
            <button
              type="button"
              className="btn v2-btn-gemini-open"
              onClick={() => void handleOpenGeminiModal()}
              title="Открыть предпросмотр пакета и скопировать в 1 клик для отправки в Gemini"
            >
              ✨ Открыть окно Gemini и скопировать
            </button>
            <a
              href={geminiUrl}
              target="_blank"
              rel="noopener noreferrer"
              download={`gemini_bundle_${caseId}.md`}
              className="v2-card-link text-xs text-muted"
            >
              Скачать gemini_bundle.md ↗
            </a>
          </div>
        </div>

        {/* Card 2: Interactive HTML Report */}
        <div className="v2-export-card">
          <div className="v2-export-card-header">
            <div className="v2-export-card-icon html-icon">🌐</div>
            <span className="v2-export-card-badge badge-html">Standalone</span>
          </div>

          <div className="v2-export-card-body">
            <h4 className="v2-export-card-title">Интерактивный HTML-отчёт</h4>
            <p className="v2-export-card-desc">
              Автономный отчёт (<code>report.html</code>) со встроенными интерактивными SVG-диаграммами, таймлайном и
              сводкой доказательств.
            </p>
          </div>

          <div className="v2-export-card-footer">
            <a
              href={htmlUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="btn btn-secondary v2-export-card-btn"
              title="Открыть автономный HTML-отчёт в новой вкладке"
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <circle cx="12" cy="12" r="10" />
                <line x1="2" y1="12" x2="22" y2="12" />
                <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
              </svg>
              Открыть report.html ↗
            </a>
            <span className="text-xs text-muted">Работает без сервера</span>
          </div>
        </div>

        {/* Card 3: Markdown Analytical Report */}
        <div className="v2-export-card">
          <div className="v2-export-card-header">
            <div className="v2-export-card-icon md-icon">📝</div>
            <span className="v2-export-card-badge badge-md">Markdown</span>
          </div>

          <div className="v2-export-card-body">
            <h4 className="v2-export-card-title">Текстовый отчёт (Markdown)</h4>
            <p className="v2-export-card-desc">
              Строгий аналитический документ (<code>report.md</code>) с матрицей ранжирования гипотез, математическими
              сигналами и таймкодами.
            </p>
          </div>

          <div className="v2-export-card-footer">
            <a
              href={mdUrl}
              target="_blank"
              rel="noopener noreferrer"
              download={`report_${caseId}.md`}
              className="btn btn-secondary v2-export-card-btn"
              title="Скачать отчёт в формате Markdown для Jira / Confluence / GitHub"
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                <polyline points="14 2 14 8 20 8" />
                <line x1="16" y1="13" x2="8" y2="13" />
                <line x1="16" y1="17" x2="8" y2="17" />
              </svg>
              Скачать report.md
            </a>
            <span className="text-xs text-muted">Формат GitHub / Jira</span>
          </div>
        </div>

        {/* Card 4: Complete Evidence ZIP Archive */}
        <div className="v2-export-card">
          <div className="v2-export-card-header">
            <div className="v2-export-card-icon zip-icon">📦</div>
            <span className="v2-export-card-badge badge-zip">ZIP Archive</span>
          </div>

          <div className="v2-export-card-body">
            <h4 className="v2-export-card-title">Архив доказательств (ZIP)</h4>
            <p className="v2-export-card-desc">
              Полный пакет инцидента (<code>evidence.zip</code>): HTML и MD отчёты, бандл Gemini, SVG-топология и дампы
              криптографических хэшей.
            </p>
          </div>

          <div className="v2-export-card-footer">
            <a
              href={zipUrl}
              target="_blank"
              rel="noopener noreferrer"
              download={`evidence_${caseId}.zip`}
              className="btn btn-secondary v2-export-card-btn"
              title="Скачать полный архив доказательств и графиков"
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                <polyline points="7 10 12 15 17 10" />
                <line x1="12" y1="15" x2="12" y2="3" />
              </svg>
              Скачать evidence.zip
            </a>
            <span className="text-xs text-muted">Все артефакты расследования</span>
          </div>
        </div>
      </div>

      {/* ===================================================================== */}
      {/* 2. SEARCH & FILTER TOOLBAR */}
      {/* ===================================================================== */}
      <div className="v2-evidence-toolbar-card">
        <div className="v2-evidence-search-box">
          <span className="v2-search-icon">🔍</span>
          <input
            type="text"
            className="v2-evidence-search-input"
            placeholder="Поиск по ID улики (E-01), сервису, тексту строки, файлу или SHA-256..."
            value={filterText}
            onChange={(e) => setFilterText(e.target.value)}
          />
          {filterText && (
            <button
              type="button"
              className="v2-search-clear-btn"
              onClick={() => setFilterText('')}
              title="Очистить поиск"
            >
              ✕
            </button>
          )}
        </div>

        <div className="v2-evidence-kinds-chips">
          <button
            type="button"
            className={`v2-kind-chip ${selectedKind === 'ALL' ? 'active' : ''}`}
            onClick={() => setSelectedKind('ALL')}
          >
            Все типы ({evidenceItems.length})
          </button>
          {kinds.map((k) => (
            <button
              key={k}
              type="button"
              className={`v2-kind-chip kind-${k} ${selectedKind === k ? 'active' : ''}`}
              onClick={() => setSelectedKind(k)}
            >
              {k}
            </button>
          ))}
        </div>

        <div className="v2-evidence-counter text-xs text-muted">
          Показано: <strong>{filteredItems.length}</strong> из <strong>{evidenceItems.length}</strong> улик
        </div>
      </div>

      {/* ===================================================================== */}
      {/* 3. WIDE READABLE EVIDENCE TABLE E-01..E-NN */}
      {/* ===================================================================== */}
      <div className="v2-evidence-table-container">
        {loading ? (
          <div className="v2-loading-box">
            <span className="spinner-icon">⟳</span> Загрузка таблицы доказательств...
          </div>
        ) : filteredItems.length === 0 ? (
          <div className="v2-empty-state-card">
            <span className="v2-empty-icon">📑</span>
            <h4>
              {evidenceItems.length === 0
                ? 'Улики для данного кейса ещё не сформированы'
                : 'Нет доказательств, соответствующих выбранным фильтрам'}
            </h4>
            <p className="v2-empty-hint">
              {evidenceItems.length === 0
                ? 'Запустите анализ RCA в верхнем меню для автоматического сбора улик E-01..E-NN.'
                : 'Попробуйте сбросить строку поиска или выбрать фильтр «Все типы».'}
            </p>
          </div>
        ) : (
          <div className="v2-table-wrapper">
            <table className="v2-evidence-table">
              <thead>
                <tr>
                  <th style={{ width: '85px' }}>ID</th>
                  <th style={{ width: '160px' }}>Сервис / Тип</th>
                  <th style={{ width: '110px' }}>Время</th>
                  <th style={{ width: '220px' }}>Источник (файл : строка)</th>
                  <th>Краткое описание / Обезличенная строка</th>
                  <th style={{ width: '190px', textAlign: 'center' }}>Действия</th>
                </tr>
              </thead>
              <tbody>
                {filteredItems.map((item: EvidenceItem) => {
                  const isHighlighted = highlightEvidenceId === item.id;
                  const isCopied = copiedLogQLId === item.id;
                  const shortFile = item.source_file ? item.source_file.split(/[/\\]/).pop() : '-';

                  return (
                    <tr
                      key={item.id}
                      ref={isHighlighted ? highlightedRowRef : null}
                      className={`v2-evidence-row ${isHighlighted ? 'v2-evidence-row-highlighted' : ''}`}
                      onClick={() => setInspectedItem(item)}
                      title="Нажмите на строку для открытия детального инспектора улики"
                    >
                      {/* 1. ID */}
                      <td className="cell-id">
                        <span className="v2-evidence-id-badge">{item.id}</span>
                      </td>

                      {/* 2. Service & Type */}
                      <td className="cell-service">
                        <div className="v2-service-kind-stack">
                          <span className="v2-service-name">{item.service || '-'}</span>
                          <span className={`v2-kind-pill kind-${item.kind}`}>{item.kind}</span>
                        </div>
                      </td>

                      {/* 3. Timestamp */}
                      <td className="cell-time" title={item.timestamp}>
                        <span className="font-mono text-xs">{formatEvidenceTime(item.timestamp)}</span>
                      </td>

                      {/* 4. Source (file : line) */}
                      <td className="cell-source" title={`${item.source_file} (запись #${item.source_record_idx})`}>
                        <div className="v2-source-stack">
                          <code className="source-filename">{shortFile}</code>
                          <span className="source-record text-xs text-muted">
                            строка: #{item.source_record_idx > 0 ? item.source_record_idx : '-'}
                          </span>
                        </div>
                      </td>

                      {/* 5. Summary / Redacted Line */}
                      <td className="cell-summary">
                        <div className="v2-summary-snippet">
                          <strong className="summary-title">{item.summary}</strong>
                          {item.redacted_line && (
                            <code className="summary-redacted-code">{item.redacted_line}</code>
                          )}
                        </div>
                      </td>

                      {/* 6. Actions */}
                      <td className="cell-actions" onClick={(e) => e.stopPropagation()}>
                        <div className="v2-evidence-actions-row">
                          <button
                            type="button"
                            className="btn btn-secondary-sm v2-btn-inspect"
                            onClick={() => setInspectedItem(item)}
                            title="Открыть инспектор улики"
                          >
                            🔍 Подробнее
                          </button>

                          {item.logql_snippet ? (
                            <button
                              type="button"
                              className={`btn btn-secondary-sm v2-btn-logql ${isCopied ? 'btn-copied' : ''}`}
                              onClick={(e) => void handleCopyLogQL(item, e)}
                              title={`Скопировать запрос LogQL: ${item.logql_snippet}`}
                            >
                              {isCopied ? '✓ LogQL' : '📋 LogQL'}
                            </button>
                          ) : (
                            <span className="text-muted text-xs">-</span>
                          )}
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* ===================================================================== */}
      {/* 4. EVIDENCE INSPECTOR MODAL (Full details without truncating) */}
      {/* ===================================================================== */}
      {inspectedItem && (
        <div className="modal-backdrop" onClick={() => setInspectedItem(null)}>
          <div
            className="modal-content modal-content-wide v2-evidence-inspector-modal"
            onClick={(e) => e.stopPropagation()}
          >
            {/* Modal Header */}
            <div className="modal-header">
              <div className="v2-inspector-title-area">
                <span className="v2-inspector-icon">🔍</span>
                <div>
                  <div className="v2-inspector-heading-row">
                    <h3>Инспектор доказательства {inspectedItem.id}</h3>
                    <span className="v2-step-service-badge">{inspectedItem.service}</span>
                    <span className={`v2-kind-pill kind-${inspectedItem.kind}`}>{inspectedItem.kind}</span>
                  </div>
                  <span className="text-xs text-muted">{inspectedItem.summary}</span>
                </div>
              </div>
              <button
                type="button"
                className="btn-close-sm"
                onClick={() => setInspectedItem(null)}
                title="Закрыть (Esc)"
              >
                ✕
              </button>
            </div>

            {/* Metadata Grid */}
            <div className="v2-inspector-meta-grid">
              <div className="v2-inspector-meta-item">
                <span className="meta-label">Файл источника:</span>
                <code className="meta-val font-mono" title={inspectedItem.source_file}>
                  {inspectedItem.source_file || '-'}
                </code>
              </div>

              <div className="v2-inspector-meta-item">
                <span className="meta-label">Номер записи в файле:</span>
                <strong className="meta-val font-mono">
                  #{inspectedItem.source_record_idx > 0 ? inspectedItem.source_record_idx : '-'}
                </strong>
              </div>

              <div className="v2-inspector-meta-item">
                <span className="meta-label">Точное время события:</span>
                <span className="meta-val font-mono">
                  {inspectedItem.timestamp || '-'} ({formatEvidenceTime(inspectedItem.timestamp)})
                </span>
              </div>

              <div className="v2-inspector-meta-item">
                <div className="meta-label-row">
                  <span className="meta-label">Криптографический хэш SHA-256:</span>
                  {inspectedItem.sha256 && (
                    <button
                      type="button"
                      className="btn-link-sm"
                      onClick={() => void handleCopyText(inspectedItem.sha256, 'sha256')}
                    >
                      {copiedModalField === 'sha256' ? '✓ Скопирован' : '📋 Скопировать'}
                    </button>
                  )}
                </div>
                <code className="meta-val font-mono text-xs" title={inspectedItem.sha256}>
                  {inspectedItem.sha256 || '-'}
                </code>
              </div>
            </div>

            {/* Full Redacted Line without cutoff */}
            <div className="v2-inspector-code-section">
              <div className="v2-code-header-bar">
                <span className="code-title">Обезличенная строка лога (Redacted Line):</span>
                <button
                  type="button"
                  className={`btn btn-secondary-sm ${copiedModalField === 'line' ? 'btn-copied' : ''}`}
                  onClick={() => void handleCopyText(inspectedItem.redacted_line || inspectedItem.summary, 'line')}
                  title="Скопировать полный текст строки"
                >
                  {copiedModalField === 'line' ? '✓ Строка скопирована' : '📋 Скопировать строку'}
                </button>
              </div>
              <div className="v2-inspector-code-box">
                <pre className="v2-full-code-text font-mono">
                  {inspectedItem.redacted_line || inspectedItem.summary || '(Текст строки пуст)'}
                </pre>
              </div>
            </div>

            {/* LogQL Snippet block with 1-click copy */}
            <div className="v2-inspector-code-section">
              <div className="v2-code-header-bar">
                <div className="code-title-group">
                  <span className="code-title">Запрос LogQL для Loki / Grafana Explore:</span>
                  <span className="text-xs text-muted">1-клик воспроизведение выборки</span>
                </div>
                {inspectedItem.logql_snippet && (
                  <button
                    type="button"
                    className={`btn btn-primary-sm ${copiedModalField === 'logql' ? 'btn-copied' : ''}`}
                    onClick={() => void handleCopyText(inspectedItem.logql_snippet, 'logql')}
                    title="Скопировать готовый запрос LogQL"
                  >
                    {copiedModalField === 'logql' ? '✓ LogQL скопирован' : '📋 Скопировать LogQL в 1 клик'}
                  </button>
                )}
              </div>
              <div className="v2-inspector-code-box v2-logql-box">
                <pre className="v2-full-code-text font-mono text-accent">
                  {inspectedItem.logql_snippet || '(LogQL-сниппет не сгенерирован)'}
                </pre>
              </div>
              <p className="text-xs text-muted mt-1">
                Этот запрос можно вставить в поле поиска <strong>Grafana Explore (Loki datasource)</strong> или
                использовать через утилиту <code>logcli</code>.
              </p>
            </div>

            {/* Modal Footer */}
            <div className="modal-footer">
              <button type="button" className="btn btn-secondary" onClick={() => setInspectedItem(null)}>
                Закрыть
              </button>
            </div>
          </div>
        </div>
      )}

      {/* ===================================================================== */}
      {/* 5. GOOGLE GEMINI PROMPT MODAL */}
      {/* ===================================================================== */}
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
              <div className="v2-loading-box">
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
