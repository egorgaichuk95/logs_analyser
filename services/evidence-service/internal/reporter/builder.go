package reporter

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/evidence-service/internal/svgchart"
)

const (
	geminiPromptHeader = `# SYSTEM PROMPT FOR GOOGLE GEMINI (RCA VERIFICATION)

Ты — Principal SRE и эксперт по распределённым микросервисным системам.
Ниже представлен автоматический отчёт поиска первопричины инцидента (RCA) и пакет проверяемых артефактов (` + "`E-01` .. `E-NN`" + `), собранный по логам нескольких сервисов. Все секреты и PII замаскированы детерминированными токенами.

Твоя задача:
1. Проверить ранжированные гипотезы (` + "`ROOT_CAUSE`, `PROPAGATION`, `SYMPTOM`, `NOISE`" + `) и оценить, верно ли отделена первопричина от каскадных следствий (обрати особое внимание на направление ошибок: ` + "`CANCELED` (`context canceled`)" + ` означает отмену сверху вызывающим сервисом, а ` + "`OWN_DEFECT` / `RESOURCE` / `DEADLINE`" + ` указывают на корень проблемы).
2. Указать точное место поломки: **Сервис → Pod → Файл и строка кода (` + "`source_place`" + `) → HTTP-ручка**.
3. В каждом своём выводе обязательно ссылаться на конкретные идентификаторы доказательств в квадратных скобках, например ` + "`[E-01]`, `[E-02]`" + `.`
)

type manifestFile struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

type manifestDoc struct {
	CaseID    string            `json:"case_id"`
	RunID     string            `json:"run_id"`
	CreatedAt time.Time         `json:"created_at"`
	Files     []manifestFile    `json:"files"`
	Checksums map[string]string `json:"checksums"`
}

// BuildAllArtifacts создаёт в reportsDir/<case_id>/<run_id>/ файлы:
// timeline.svg, topology.svg, evidence.jsonl, report.md, report.html, gemini_bundle.md, evidence_<case_id>.zip
func BuildAllArtifacts(reportsDir string, req contracts.BuildEvidenceRequest, items []contracts.EvidenceItem) (contracts.BuildEvidenceResponse, error) {
	if req.CaseID == "" {
		return contracts.BuildEvidenceResponse{}, contracts.ErrEmptyCaseID
	}

	runID := req.RunID
	if runID == "" {
		if req.Analysis.RunID != "" {
			runID = req.Analysis.RunID
		} else {
			runID = fmt.Sprintf("run_%d", time.Now().UTC().UnixNano())
		}
	}
	req.RunID = runID

	runDir := filepath.Join(reportsDir, req.CaseID, runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return contracts.BuildEvidenceResponse{}, fmt.Errorf("create run dir: %w", err)
	}

	// 1. Redact all items' PII fields
	sanitizedItems := make([]contracts.EvidenceItem, len(items))
	for i, it := range items {
		sanitizedItems[i] = it
		sanitizedItems[i].Summary = RedactPII(it.Summary)
		sanitizedItems[i].RedactedLine = RedactPII(it.RedactedLine)
	}

	// 2. Render and write timeline.svg
	timelineSVG := svgchart.RenderTimelineSVG(req.Analysis.Timeline, req.Analysis.Hypotheses)
	timelinePath := filepath.Join(runDir, "timeline.svg")
	if err := os.WriteFile(timelinePath, []byte(timelineSVG), 0644); err != nil {
		return contracts.BuildEvidenceResponse{}, fmt.Errorf("write timeline.svg: %w", err)
	}

	// 3. Render and write topology.svg
	topologySVG := svgchart.RenderTopologySVG(req.Analysis.ServiceEdges, req.Analysis.Hypotheses)
	topologyPath := filepath.Join(runDir, "topology.svg")
	if err := os.WriteFile(topologyPath, []byte(topologySVG), 0644); err != nil {
		return contracts.BuildEvidenceResponse{}, fmt.Errorf("write topology.svg: %w", err)
	}

	// 4. Write evidence.jsonl
	jsonlPath := filepath.Join(runDir, "evidence.jsonl")
	var jsonlBuf bytes.Buffer
	for _, it := range sanitizedItems {
		line, err := json.Marshal(it)
		if err != nil {
			return contracts.BuildEvidenceResponse{}, fmt.Errorf("marshal evidence item: %w", err)
		}
		jsonlBuf.Write(line)
		jsonlBuf.WriteByte('\n')
	}
	if err := os.WriteFile(jsonlPath, jsonlBuf.Bytes(), 0644); err != nil {
		return contracts.BuildEvidenceResponse{}, fmt.Errorf("write evidence.jsonl: %w", err)
	}

	// 5. Generate and write report.md
	mdContent := buildReportMD(req, sanitizedItems)
	mdPath := filepath.Join(runDir, "report.md")
	if err := os.WriteFile(mdPath, []byte(mdContent), 0644); err != nil {
		return contracts.BuildEvidenceResponse{}, fmt.Errorf("write report.md: %w", err)
	}

	// 6. Generate and write report.html
	htmlContent := buildReportHTML(req, sanitizedItems, timelineSVG, topologySVG)
	htmlPath := filepath.Join(runDir, "report.html")
	if err := os.WriteFile(htmlPath, []byte(htmlContent), 0644); err != nil {
		return contracts.BuildEvidenceResponse{}, fmt.Errorf("write report.html: %w", err)
	}

	// 7. Generate and write gemini_bundle.md (max 320 KB)
	bundleContent := buildGeminiBundleMD(req, sanitizedItems)
	bundlePath := filepath.Join(runDir, "gemini_bundle.md")
	if err := os.WriteFile(bundlePath, []byte(bundleContent), 0644); err != nil {
		return contracts.BuildEvidenceResponse{}, fmt.Errorf("write gemini_bundle.md: %w", err)
	}

	// 8. Generate manifest.json and create evidence_<case_id>.zip
	fileNames := []string{
		"timeline.svg",
		"topology.svg",
		"evidence.jsonl",
		"report.md",
		"report.html",
		"gemini_bundle.md",
	}

	manifest := manifestDoc{
		CaseID:    req.CaseID,
		RunID:     runID,
		CreatedAt: time.Now().UTC(),
		Files:     make([]manifestFile, 0, len(fileNames)),
		Checksums: make(map[string]string),
	}

	fileBytesMap := make(map[string][]byte, len(fileNames))
	for _, fn := range fileNames {
		data, err := os.ReadFile(filepath.Join(runDir, fn))
		if err != nil {
			return contracts.BuildEvidenceResponse{}, fmt.Errorf("read file for manifest %s: %w", fn, err)
		}
		fileBytesMap[fn] = data
		h := sha256.Sum256(data)
		hStr := hex.EncodeToString(h[:])

		manifest.Files = append(manifest.Files, manifestFile{
			Name:      fn,
			SizeBytes: int64(len(data)),
			SHA256:    hStr,
		})
		manifest.Checksums[fn] = hStr
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return contracts.BuildEvidenceResponse{}, fmt.Errorf("marshal manifest: %w", err)
	}

	zipName := fmt.Sprintf("evidence_%s.zip", req.CaseID)
	zipPath := filepath.Join(runDir, zipName)
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return contracts.BuildEvidenceResponse{}, fmt.Errorf("create zip: %w", err)
	}

	zw := zip.NewWriter(zipFile)
	for _, fn := range fileNames {
		w, err := zw.Create(fn)
		if err != nil {
			zipFile.Close()
			return contracts.BuildEvidenceResponse{}, fmt.Errorf("zip entry %s: %w", fn, err)
		}
		if _, err := w.Write(fileBytesMap[fn]); err != nil {
			zipFile.Close()
			return contracts.BuildEvidenceResponse{}, fmt.Errorf("write zip entry %s: %w", fn, err)
		}
	}

	// Add manifest.json inside zip
	mw, err := zw.Create("manifest.json")
	if err != nil {
		zipFile.Close()
		return contracts.BuildEvidenceResponse{}, fmt.Errorf("zip manifest entry: %w", err)
	}
	if _, err := mw.Write(manifestBytes); err != nil {
		zipFile.Close()
		return contracts.BuildEvidenceResponse{}, fmt.Errorf("write zip manifest: %w", err)
	}

	if err := zw.Close(); err != nil {
		zipFile.Close()
		return contracts.BuildEvidenceResponse{}, fmt.Errorf("close zip writer: %w", err)
	}
	if err := zipFile.Close(); err != nil {
		return contracts.BuildEvidenceResponse{}, fmt.Errorf("close zip file: %w", err)
	}

	// Calculate total size of generated artifacts in run directory
	var totalBytes int64
	entries, _ := os.ReadDir(runDir)
	for _, ent := range entries {
		info, err := ent.Info()
		if err == nil {
			totalBytes += info.Size()
		}
	}

	return contracts.BuildEvidenceResponse{
		CaseID:        req.CaseID,
		RunID:         runID,
		ReportDir:     runDir,
		MarkdownPath:  mdPath,
		HTMLPath:      htmlPath,
		GeminiPath:    bundlePath,
		ZipPath:       zipPath,
		SizeBytes:     totalBytes,
		EvidenceItems: sanitizedItems,
	}, nil
}

func buildReportMD(req contracts.BuildEvidenceRequest, items []contracts.EvidenceItem) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# RCA Incident Report: %s\n\n", req.CaseID))
	sb.WriteString(fmt.Sprintf("- **Case ID:** `%s`\n", req.CaseID))
	sb.WriteString(fmt.Sprintf("- **Run ID:** `%s`\n", req.RunID))
	if !req.Analysis.IncidentStart.IsZero() && !req.Analysis.IncidentEnd.IsZero() {
		sb.WriteString(fmt.Sprintf("- **Incident Window:** %s — %s (UTC)\n",
			req.Analysis.IncidentStart.Format(time.RFC3339), req.Analysis.IncidentEnd.Format(time.RFC3339)))
	}
	sb.WriteString("\n---\n\n")

	// Executive Summary
	sb.WriteString("## Executive Summary\n\n")
	var rootCause *contracts.Hypothesis
	for i := range req.Analysis.Hypotheses {
		if req.Analysis.Hypotheses[i].Role == contracts.RoleRootCause {
			rootCause = &req.Analysis.Hypotheses[i]
			break
		}
	}

	if rootCause != nil {
		sb.WriteString(fmt.Sprintf("> **Root Cause Identified:** Service **`%s`** (Confidence: `%.0f%%`)\n",
			rootCause.Service, rootCause.Confidence*100))
		sb.WriteString(fmt.Sprintf("> - **Error Class:** `%s`\n", rootCause.ErrorClass))
		sb.WriteString(fmt.Sprintf("> - **Source Location:** `%s`\n", rootCause.SourcePlace))
		if rootCause.DominantPod != "" {
			sb.WriteString(fmt.Sprintf("> - **Dominant Pod:** `%s`\n", rootCause.DominantPod))
		}
		if rootCause.DominantRoute != "" {
			sb.WriteString(fmt.Sprintf("> - **Route:** `%s`\n", rootCause.DominantRoute))
		}
		if rootCause.Explanation != "" {
			sb.WriteString(fmt.Sprintf("> - **Details:** %s\n", RedactPII(rootCause.Explanation)))
		}
	} else {
		sb.WriteString("> *No definitive root cause hypothesis identified with sufficient confidence.*\n")
	}
	sb.WriteString("\n---\n\n")

	// Hypotheses Ranking
	sb.WriteString("## Hypotheses Ranking\n\n")
	if len(req.Analysis.Hypotheses) > 0 {
		sb.WriteString("| Rank | Role | Service | Confidence | Error Class | Source Place | Evidence IDs |\n")
		sb.WriteString("| :---: | :--- | :--- | :---: | :--- | :--- | :--- |\n")
		for _, h := range req.Analysis.Hypotheses {
			evIDs := strings.Join(h.EvidenceIDs, ", ")
			if evIDs == "" {
				evIDs = "-"
			}
			sb.WriteString(fmt.Sprintf("| %d | `%s` | **%s** | %.1f%% | `%s` | `%s` | %s |\n",
				h.Rank, h.Role, h.Service, h.Confidence*100, h.ErrorClass, h.SourcePlace, evIDs))
		}
	} else {
		sb.WriteString("*No hypotheses recorded.*\n")
	}
	sb.WriteString("\n---\n\n")

	// Visualizations
	sb.WriteString("## Visual Artifacts\n\n")
	sb.WriteString("### Event Frequencies & Onset Timeline\n\n")
	sb.WriteString("![Timeline](timeline.svg)\n\n")
	sb.WriteString("### Service Topology & Error Propagation\n\n")
	sb.WriteString("![Topology](topology.svg)\n\n")
	sb.WriteString("\n---\n\n")

	// Clock Skew Findings
	if len(req.Analysis.ClockSkewFindings) > 0 {
		sb.WriteString("## Clock Skew Findings\n\n")
		sb.WriteString("| Service | Pod | Skew (ms) | Raw Timestamp | App Timestamp |\n")
		sb.WriteString("| :--- | :--- | :---: | :--- | :--- |\n")
		for _, cs := range req.Analysis.ClockSkewFindings {
			sb.WriteString(fmt.Sprintf("| %s | %s | %d | `%s` | `%s` |\n",
				cs.Service, cs.Pod, cs.SkewMs, cs.SampleRawTs, cs.SampleAppTs))
		}
		sb.WriteString("\n---\n\n")
	}

	// Evidence Catalog
	sb.WriteString("## Evidence Catalog\n\n")
	if len(items) > 0 {
		for _, it := range items {
			sb.WriteString(fmt.Sprintf("### [%s] %s (`%s`)\n\n", it.ID, it.Summary, it.Kind))
			sb.WriteString(fmt.Sprintf("- **Service:** `%s`\n", it.Service))
			if !it.Timestamp.IsZero() {
				sb.WriteString(fmt.Sprintf("- **Timestamp:** `%s`\n", it.Timestamp.Format(time.RFC3339Nano)))
			}
			if it.SourceFile != "" {
				sb.WriteString(fmt.Sprintf("- **Source:** `%s#%d`\n", it.SourceFile, it.SourceRecordIdx))
			}
			if it.SHA256 != "" {
				sb.WriteString(fmt.Sprintf("- **SHA-256:** `%s`\n", it.SHA256))
			}
			if it.LogQLSnippet != "" {
				sb.WriteString(fmt.Sprintf("- **LogQL:** `%s`\n", it.LogQLSnippet))
			}
			if it.RedactedLine != "" {
				sb.WriteString("\n```text\n")
				sb.WriteString(it.RedactedLine)
				sb.WriteString("\n```\n")
			}
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString("*No evidence items recorded for this run.*\n")
	}

	return RedactPII(sb.String())
}

func buildReportHTML(req contracts.BuildEvidenceRequest, items []contracts.EvidenceItem, timelineSVG, topologySVG string) string {
	var sb strings.Builder

	sb.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>RCA Incident Report - `)
	sb.WriteString(req.CaseID)
	sb.WriteString(`</title>
<style>
  :root {
    --bg-main: #0f172a;
    --bg-card: #1e293b;
    --border-card: #334155;
    --text-primary: #f8fafc;
    --text-secondary: #94a3b8;
    --accent-red: #ef4444;
    --accent-orange: #f59e0b;
    --accent-blue: #3b82f6;
  }
  body {
    background-color: var(--bg-main);
    color: var(--text-primary);
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    line-height: 1.6;
    margin: 0;
    padding: 32px;
  }
  .container { max-width: 1040px; margin: 0 auto; }
  h1, h2, h3 { color: #fff; font-weight: 700; margin-top: 24px; }
  h1 { font-size: 28px; border-bottom: 2px solid var(--border-card); padding-bottom: 12px; }
  .card {
    background: var(--bg-card);
    border: 1px solid var(--border-card);
    border-radius: 8px;
    padding: 20px;
    margin-bottom: 24px;
  }
  .meta-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: 16px;
    margin-bottom: 16px;
  }
  .meta-item { font-size: 14px; }
  .meta-label { color: var(--text-secondary); text-transform: uppercase; font-size: 11px; font-weight: 700; letter-spacing: 0.5px; }
  .meta-val { font-family: monospace; font-size: 15px; margin-top: 4px; }
  table {
    width: 100%;
    border-collapse: collapse;
    margin-top: 16px;
    font-size: 14px;
  }
  th, td {
    border: 1px solid var(--border-card);
    padding: 10px 14px;
    text-align: left;
  }
  th { background: #131d31; color: var(--text-secondary); }
  .badge {
    display: inline-block;
    padding: 2px 8px;
    border-radius: 4px;
    font-size: 11px;
    font-weight: 700;
  }
  .badge-root { background: #7f1d1d; color: #fca5a5; border: 1px solid #ef4444; }
  .badge-prop { background: #78350f; color: #fde68a; border: 1px solid #f59e0b; }
  .badge-symptom { background: #1e3a8a; color: #93c5fd; border: 1px solid #3b82f6; }
  .badge-noise { background: #334155; color: #cbd5e1; }
  .svg-wrap { margin: 20px 0; overflow-x: auto; }
  pre {
    background: #0f172a;
    border: 1px solid var(--border-card);
    border-radius: 6px;
    padding: 12px;
    overflow-x: auto;
    font-family: monospace;
    font-size: 13px;
    color: #e2e8f0;
  }
  code {
    background: #0f172a;
    padding: 2px 6px;
    border-radius: 4px;
    font-family: monospace;
    font-size: 13px;
    color: #38bdf8;
  }
</style>
</head>
<body>
<div class="container">
  <h1>RCA Incident Report: `)
	sb.WriteString(req.CaseID)
	sb.WriteString(`</h1>
  <div class="card">
    <div class="meta-grid">
      <div class="meta-item">
        <div class="meta-label">Case ID</div>
        <div class="meta-val">`)
	sb.WriteString(req.CaseID)
	sb.WriteString(`</div>
      </div>
      <div class="meta-item">
        <div class="meta-label">Run ID</div>
        <div class="meta-val">`)
	sb.WriteString(req.RunID)
	sb.WriteString(`</div>
      </div>
      <div class="meta-item">
        <div class="meta-label">Incident Window</div>
        <div class="meta-val">`)
	if !req.Analysis.IncidentStart.IsZero() {
		sb.WriteString(req.Analysis.IncidentStart.Format("2006-01-02 15:04:05"))
		sb.WriteString(" - ")
		sb.WriteString(req.Analysis.IncidentEnd.Format("15:04:05 UTC"))
	} else {
		sb.WriteString("N/A")
	}
	sb.WriteString(`</div>
      </div>
    </div>
  </div>

  <h2>Hypotheses Ranking</h2>
  <div class="card">
    <table>
      <thead>
        <tr>
          <th>Rank</th>
          <th>Role</th>
          <th>Service</th>
          <th>Confidence</th>
          <th>Error Class</th>
          <th>Location</th>
          <th>Evidence</th>
        </tr>
      </thead>
      <tbody>
`)

	for _, h := range req.Analysis.Hypotheses {
		roleClass := "badge-noise"
		switch h.Role {
		case contracts.RoleRootCause:
			roleClass = "badge-root"
		case contracts.RolePropagation:
			roleClass = "badge-prop"
		case contracts.RoleSymptom:
			roleClass = "badge-symptom"
		}

		evStr := strings.Join(h.EvidenceIDs, ", ")
		if evStr == "" {
			evStr = "-"
		}

		sb.WriteString(fmt.Sprintf(`        <tr>
          <td style="text-align:center; font-weight:bold;">%d</td>
          <td><span class="badge %s">%s</span></td>
          <td><strong>%s</strong></td>
          <td>%.1f%%</td>
          <td><code>%s</code></td>
          <td><code>%s</code></td>
          <td>%s</td>
        </tr>
`, h.Rank, roleClass, h.Role, h.Service, h.Confidence*100, h.ErrorClass, h.SourcePlace, evStr))
	}

	sb.WriteString(`      </tbody>
    </table>
  </div>

  <h2>Failure Timeline</h2>
  <div class="card svg-wrap">
`)
	sb.WriteString(timelineSVG)
	sb.WriteString(`
  </div>

  <h2>Service Call Topology</h2>
  <div class="card svg-wrap">
`)
	sb.WriteString(topologySVG)
	sb.WriteString(`
  </div>

  <h2>Evidence Artifacts</h2>
`)

	for _, it := range items {
		sb.WriteString(fmt.Sprintf(`  <div class="card">
    <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:8px;">
      <h3 style="margin:0; font-size:16px;">[%s] %s</h3>
      <span class="badge badge-symptom">%s</span>
    </div>
    <div class="meta-grid" style="margin-top:12px;">
      <div class="meta-item"><div class="meta-label">Service</div><div class="meta-val">%s</div></div>
      <div class="meta-item"><div class="meta-label">Source</div><div class="meta-val">%s#%d</div></div>
      <div class="meta-item"><div class="meta-label">SHA256</div><div class="meta-val" style="font-size:12px;">%s</div></div>
    </div>
`, it.ID, it.Summary, it.Kind, it.Service, it.SourceFile, it.SourceRecordIdx, it.SHA256))

		if it.LogQLSnippet != "" {
			sb.WriteString(fmt.Sprintf(`    <div style="margin-top:8px;"><span class="meta-label">LogQL:</span> <code>%s</code></div>`+"\n", it.LogQLSnippet))
		}
		if it.RedactedLine != "" {
			sb.WriteString(fmt.Sprintf(`    <pre style="margin-top:8px;"><code>%s</code></pre>`+"\n", it.RedactedLine))
		}
		sb.WriteString("  </div>\n")
	}

	sb.WriteString(`</div>
</body>
</html>`)

	return RedactPII(sb.String())
}

func buildGeminiBundleMD(req contracts.BuildEvidenceRequest, items []contracts.EvidenceItem) string {
	const maxBundleBytes = 320 * 1024 // 320 KB hard limit

	var sb strings.Builder
	sb.WriteString(geminiPromptHeader)
	sb.WriteString("\n\n---\n\n")

	// Metadata
	sb.WriteString(fmt.Sprintf("## Incident Metadata\n- **Case ID:** `%s`\n- **Run ID:** `%s`\n", req.CaseID, req.RunID))
	if !req.Analysis.IncidentStart.IsZero() && !req.Analysis.IncidentEnd.IsZero() {
		sb.WriteString(fmt.Sprintf("- **Incident Window:** %s — %s (UTC)\n",
			req.Analysis.IncidentStart.Format(time.RFC3339), req.Analysis.IncidentEnd.Format(time.RFC3339)))
	}
	sb.WriteString("\n")

	// Topology in Mermaid
	sb.WriteString("## Service Call Topology\n\n```mermaid\ngraph LR\n")
	if len(req.Analysis.ServiceEdges) > 0 {
		for _, e := range req.Analysis.ServiceEdges {
			if e.CallerService != "" && e.CalleeService != "" {
				sb.WriteString(fmt.Sprintf("  %s -->|err:%d/tot:%d| %s\n",
					e.CallerService, e.ErrorCount, e.CallCount, e.CalleeService))
			}
		}
	} else {
		sb.WriteString("  NoTopologyEdges[No Topology Edges]\n")
	}
	sb.WriteString("```\n\n")

	// Hypotheses Ranking
	sb.WriteString("## Ranked Hypotheses Table\n\n")
	sb.WriteString("| Rank | Role | Service | Confidence | Template | Error Class | Location | Onset | Dominant Pod | Evidence IDs |\n")
	sb.WriteString("| :---: | :--- | :--- | :---: | :--- | :--- | :--- | :--- | :--- | :--- |\n")
	for _, h := range req.Analysis.Hypotheses {
		evIDs := strings.Join(h.EvidenceIDs, ", ")
		if evIDs == "" {
			evIDs = "-"
		}
		onsetStr := "-"
		if !h.OnsetTime.IsZero() {
			onsetStr = h.OnsetTime.Format("15:04:05")
		}
		sb.WriteString(fmt.Sprintf("| %d | `%s` | **%s** | %.1f%% | %s | `%s` | `%s` | %s | `%s` | %s |\n",
			h.Rank, h.Role, h.Service, h.Confidence*100, RedactPII(h.TemplateText), h.ErrorClass, h.SourcePlace, onsetStr, h.DominantPod, evIDs))
	}
	sb.WriteString("\n---\n\n")

	// Clock Skew Findings
	if len(req.Analysis.ClockSkewFindings) > 0 {
		sb.WriteString("## Clock Skew Findings\n\n")
		sb.WriteString("| Service | Pod | Skew (ms) | Raw Timestamp | App Timestamp |\n")
		sb.WriteString("| :--- | :--- | :---: | :--- | :--- |\n")
		for _, cs := range req.Analysis.ClockSkewFindings {
			sb.WriteString(fmt.Sprintf("| %s | %s | %d | `%s` | `%s` |\n",
				cs.Service, cs.Pod, cs.SkewMs, cs.SampleRawTs, cs.SampleAppTs))
		}
		sb.WriteString("\n---\n\n")
	}

	// Evidence Artifacts
	sb.WriteString("## Evidence Artifacts\n\n")
	headerAndTables := RedactPII(sb.String())

	var fullSb strings.Builder
	fullSb.WriteString(headerAndTables)

	// Add evidence items respecting the 320 KB limit
	for i, it := range items {
		var itemSb strings.Builder
		itemSb.WriteString(fmt.Sprintf("### [%s] %s (Kind: `%s`, Service: `%s`)\n",
			it.ID, it.Summary, it.Kind, it.Service))
		if !it.Timestamp.IsZero() {
			itemSb.WriteString(fmt.Sprintf("- **Timestamp:** `%s`\n", it.Timestamp.Format(time.RFC3339Nano)))
		}
		if it.SourceFile != "" {
			itemSb.WriteString(fmt.Sprintf("- **Source:** `%s#%d`\n", it.SourceFile, it.SourceRecordIdx))
		}
		if it.SHA256 != "" {
			itemSb.WriteString(fmt.Sprintf("- **SHA256:** `%s`\n", it.SHA256))
		}
		if it.LogQLSnippet != "" {
			itemSb.WriteString(fmt.Sprintf("- **LogQL:** `%s`\n", it.LogQLSnippet))
		}
		if it.RedactedLine != "" {
			itemSb.WriteString("```text\n")
			itemSb.WriteString(it.RedactedLine)
			itemSb.WriteString("\n```\n")
		}
		itemSb.WriteString("\n")

		redactedItem := RedactPII(itemSb.String())

		// If adding this item would exceed maxBundleBytes - safety margin (2 KB), stop
		if fullSb.Len()+len(redactedItem)+2048 > maxBundleBytes {
			fullSb.WriteString(fmt.Sprintf("\n> [!NOTE]\n> Further evidence items (%d of %d) were truncated to maintain the bundle size <= 320 KB.\n",
				len(items)-i, len(items)))
			break
		}
		fullSb.WriteString(redactedItem)
	}

	return fullSb.String()
}
