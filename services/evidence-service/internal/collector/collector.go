package collector

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

var variableFilterRegex = regexp.MustCompile(`<[^>]+>|[{}":,]+`)

// CollectEvidence собирает доказательства E-01..E-NN для гипотез и системных находок кейса
// и прописывает собранные ID в поле EvidenceIDs каждой гипотезы в analysis.Hypotheses.
func CollectEvidence(ctx context.Context, chHTTPURL string, analysis *contracts.AnalyzeCaseResponse) ([]contracts.EvidenceItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if analysis == nil {
		return []contracts.EvidenceItem{}, nil
	}

	evidenceItems := make([]contracts.EvidenceItem, 0)
	evidenceIdx := 1
	nextID := func() string {
		id := fmt.Sprintf("E-%02d", evidenceIdx)
		evidenceIdx++
		return id
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	cleanCHURL := strings.TrimRight(chHTTPURL, "/")

	// 1. Для каждой гипотезы собираем stat_test и raw_log доказательства
	for i := range analysis.Hypotheses {
		h := &analysis.Hypotheses[i]
		if h.EvidenceIDs == nil {
			h.EvidenceIDs = make([]string, 0)
		}

		// 1.1. Доказательство Kind = "stat_test"
		statID := nextID()
		h.EvidenceIDs = append(h.EvidenceIDs, statID)

		baseRate := contracts.SanitizeFloat(h.BaselineRatePerMin)
		incRate := contracts.SanitizeFloat(h.IncidentRatePerMin)
		growth := contracts.SanitizeFloat(h.GrowthRatio)
		pValue := contracts.SanitizeFloat(h.PoissonPValue)

		statSummary := fmt.Sprintf("Статистика шаблона %s: baseline=%.2f/мин, инцидент=%.2f/мин, рост=%.1fx, p-value=%.4g",
			h.Service, baseRate, incRate, growth, pValue)

		statTs := h.OnsetTime
		if statTs.IsZero() {
			statTs = analysis.IncidentStart
		}
		if statTs.IsZero() {
			statTs = time.Now().UTC()
		}

		evidenceItems = append(evidenceItems, contracts.EvidenceItem{
			ID:        statID,
			Kind:      contracts.EvidenceKindStatTest,
			Service:   h.Service,
			Summary:   statSummary,
			Timestamp: statTs,
			Metadata: map[string]any{
				"template_id":           h.TemplateID,
				"error_class":           h.ErrorClass,
				"role":                  h.Role,
				"growth_ratio":          growth,
				"p_value":               pValue,
				"baseline_rate_per_min": baseRate,
				"incident_rate_per_min": incRate,
			},
		})

		// 1.2. Доказательства Kind = "raw_log": запрос до 3 строк из events_raw (или fallback на templates_dict)
		rawRows := queryRawEvents(ctx, httpClient, cleanCHURL, analysis.CaseID, h.Service, h.TemplateID)
		if len(rawRows) == 0 {
			// Fallback: запрос sample_redacted из templates_dict
			tmplRow := queryTemplateRecord(ctx, httpClient, cleanCHURL, analysis.CaseID, h.Service, h.TemplateID)
			if tmplRow != nil && (tmplRow.SampleRedacted != "" || tmplRow.TemplateText != "") {
				rawID := nextID()
				h.EvidenceIDs = append(h.EvidenceIDs, rawID)

				redactedLine := tmplRow.SampleRedacted
				if redactedLine == "" {
					redactedLine = tmplRow.TemplateText
				}
				shaSum := fmt.Sprintf("%x", sha256.Sum256([]byte(redactedLine)))
				token := extractShortToken(tmplRow.TemplateText, redactedLine, h.ErrorClass)
				logql := fmt.Sprintf("{service_name=%q, pod=%q} |= %q", h.Service, h.DominantPod, token)

				summary := fmt.Sprintf("Образец шаблона %s (fallback): %s", h.Service, truncate(redactedLine, 120))
				evidenceItems = append(evidenceItems, contracts.EvidenceItem{
					ID:              rawID,
					Kind:            contracts.EvidenceKindRawLog,
					Service:         h.Service,
					Summary:         summary,
					Timestamp:       statTs,
					SourceFile:      "templates_dict",
					SourceRecordIdx: 0,
					SHA256:          shaSum,
					RedactedLine:    redactedLine,
					LogQLSnippet:    logql,
					Metadata: map[string]any{
						"template_id":  h.TemplateID,
						"source_place": tmplRow.SourcePlace,
						"fallback":     true,
					},
				})
			} else if h.TemplateText != "" {
				// Fallback на текст шаблона из самой гипотезы
				rawID := nextID()
				h.EvidenceIDs = append(h.EvidenceIDs, rawID)

				redactedLine := h.TemplateText
				shaSum := fmt.Sprintf("%x", sha256.Sum256([]byte(redactedLine)))
				token := extractShortToken(h.TemplateText, redactedLine, h.ErrorClass)
				logql := fmt.Sprintf("{service_name=%q, pod=%q} |= %q", h.Service, h.DominantPod, token)

				summary := fmt.Sprintf("Шаблон лога %s: %s", h.Service, truncate(redactedLine, 120))
				evidenceItems = append(evidenceItems, contracts.EvidenceItem{
					ID:              rawID,
					Kind:            contracts.EvidenceKindRawLog,
					Service:         h.Service,
					Summary:         summary,
					Timestamp:       statTs,
					SourceFile:      "templates_dict",
					SourceRecordIdx: 0,
					SHA256:          shaSum,
					RedactedLine:    redactedLine,
					LogQLSnippet:    logql,
					Metadata: map[string]any{
						"template_id": h.TemplateID,
						"error_class": h.ErrorClass,
						"fallback":    true,
					},
				})
			}
		} else {
			for _, row := range rawRows {
				rawID := nextID()
				h.EvidenceIDs = append(h.EvidenceIDs, rawID)

				redactedLine := row.RawRedacted
				shaSum := fmt.Sprintf("%x", sha256.Sum256([]byte(redactedLine)))
				token := extractShortToken(h.TemplateText, redactedLine, h.ErrorClass)
				logql := fmt.Sprintf("{service_name=%q, pod=%q} |= %q", h.Service, h.DominantPod, token)

				rowTs := parseCHDateTime(row.Ts)
				if rowTs.IsZero() {
					rowTs = statTs
				}

				summary := fmt.Sprintf("Сырой лог %s (pod=%s): %s", h.Service, row.Pod, truncate(redactedLine, 120))
				evidenceItems = append(evidenceItems, contracts.EvidenceItem{
					ID:              rawID,
					Kind:            contracts.EvidenceKindRawLog,
					Service:         h.Service,
					Summary:         summary,
					Timestamp:       rowTs,
					SourceFile:      row.SourceFile,
					SourceRecordIdx: row.SourceRecordIdx,
					SHA256:          shaSum,
					RedactedLine:    redactedLine,
					LogQLSnippet:    logql,
					Metadata: map[string]any{
						"pod":         row.Pod,
						"template_id": h.TemplateID,
						"event_id":    row.EventID,
					},
				})
			}
		}
	}

	// 2. Для каждого ClockSkewFinding создаём элемент Kind = "clock_skew"
	for _, csf := range analysis.ClockSkewFindings {
		skewID := nextID()
		summary := fmt.Sprintf("Расхождение времени (clock skew) на сервисе %s (pod %s): %d мс (сырой ts=%s, app ts=%s)",
			csf.Service, csf.Pod, csf.SkewMs, csf.SampleRawTs, csf.SampleAppTs)

		ts := parseCHDateTime(csf.SampleRawTs)
		if ts.IsZero() {
			ts = analysis.IncidentStart
		}
		if ts.IsZero() {
			ts = time.Now().UTC()
		}

		evidenceItems = append(evidenceItems, contracts.EvidenceItem{
			ID:        skewID,
			Kind:      contracts.EvidenceKindClockSkew,
			Service:   csf.Service,
			Summary:   summary,
			Timestamp: ts,
			Metadata: map[string]any{
				"pod":           csf.Pod,
				"skew_ms":       csf.SkewMs,
				"sample_raw_ts": csf.SampleRawTs,
				"sample_app_ts": csf.SampleAppTs,
			},
		})
	}

	// 3. Для каждого ребра с ошибками создаём элемент Kind = "topology_edge"
	for _, edge := range analysis.ServiceEdges {
		if edge.ErrorCount > 0 {
			edgeID := nextID()
			summary := fmt.Sprintf("Ребро топологии %s -> %s: %d ошибок из %d вызовов (источник: %s)",
				edge.CallerService, edge.CalleeService, edge.ErrorCount, edge.CallCount, edge.EvidenceSource)

			ts := analysis.IncidentStart
			if ts.IsZero() {
				ts = time.Now().UTC()
			}

			evidenceItems = append(evidenceItems, contracts.EvidenceItem{
				ID:        edgeID,
				Kind:      contracts.EvidenceKindTopologyEdge,
				Service:   edge.CallerService,
				Summary:   summary,
				Timestamp: ts,
				Metadata: map[string]any{
					"caller":          edge.CallerService,
					"callee":          edge.CalleeService,
					"call_count":      edge.CallCount,
					"error_count":     edge.ErrorCount,
					"evidence_source": edge.EvidenceSource,
				},
			})
		}
	}

	analysis.EvidenceItems = evidenceItems
	return evidenceItems, nil
}

type chRawEventRow struct {
	CaseID          string `json:"case_id"`
	EventID         string `json:"event_id"`
	Ts              string `json:"ts"`
	Service         string `json:"service"`
	Pod             string `json:"pod"`
	TemplateID      uint64 `json:"template_id"`
	SourceFile      string `json:"source_file"`
	SourceRecordIdx uint32 `json:"source_record_idx"`
	RawRedacted     string `json:"raw_redacted"`
}

type chTemplateRow struct {
	CaseID         string `json:"case_id"`
	Service        string `json:"service"`
	TemplateID     uint64 `json:"template_id"`
	TemplateText   string `json:"template_text"`
	SourcePlace    string `json:"source_place"`
	SampleRedacted string `json:"sample_redacted"`
}

func queryRawEvents(ctx context.Context, client *http.Client, chURL, caseID, service string, templateID uint64) []chRawEventRow {
	if chURL == "" {
		return nil
	}
	sql := fmt.Sprintf("SELECT case_id, event_id, ts, service, pod, template_id, source_file, source_record_idx, raw_redacted FROM logs_rca.events_raw WHERE case_id = '%s' AND service = '%s' AND template_id = %d LIMIT 3 FORMAT JSONEachRow",
		escapeSQL(caseID), escapeSQL(service), templateID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chURL, strings.NewReader(sql+"\n"))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "text/plain")

	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()

	var rows []chRawEventRow
	dec := json.NewDecoder(resp.Body)
	for dec.More() {
		var r chRawEventRow
		if err := dec.Decode(&r); err == nil && r.RawRedacted != "" {
			rows = append(rows, r)
		}
	}
	return rows
}

func queryTemplateRecord(ctx context.Context, client *http.Client, chURL, caseID, service string, templateID uint64) *chTemplateRow {
	if chURL == "" {
		return nil
	}
	sql := fmt.Sprintf("SELECT case_id, service, template_id, template_text, source_place, sample_redacted FROM logs_rca.templates_dict WHERE case_id = '%s' AND service = '%s' AND template_id = %d LIMIT 1 FORMAT JSONEachRow",
		escapeSQL(caseID), escapeSQL(service), templateID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chURL, strings.NewReader(sql+"\n"))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "text/plain")

	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(resp.Body)
	if dec.More() {
		var r chTemplateRow
		if err := dec.Decode(&r); err == nil {
			return &r
		}
	}
	return nil
}

func extractShortToken(templateText, rawRedacted, errorClass string) string {
	if templateText != "" {
		cleaned := variableFilterRegex.ReplaceAllString(templateText, " ")
		words := strings.Fields(cleaned)
		if len(words) > 0 {
			var chosen []string
			for _, w := range words {
				if len(w) >= 3 && !strings.ContainsAny(w, `=<>{}[]"'\`) {
					chosen = append(chosen, w)
					if len(chosen) >= 3 {
						break
					}
				}
			}
			if len(chosen) > 0 {
				return strings.Join(chosen, " ")
			}
			return words[0]
		}
	}
	if errorClass != "" && errorClass != contracts.ErrClassNone && errorClass != "NONE" && errorClass != "UNKNOWN" {
		return errorClass
	}
	if rawRedacted != "" {
		cleaned := variableFilterRegex.ReplaceAllString(rawRedacted, " ")
		words := strings.Fields(cleaned)
		for _, w := range words {
			if len(w) >= 4 && !strings.ContainsAny(w, `=<>{}[]"'\`) {
				return w
			}
		}
	}
	return "error"
}

func parseCHDateTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, s, time.UTC); err == nil {
			return t
		}
	}
	return time.Time{}
}

func escapeSQL(val string) string {
	return strings.ReplaceAll(strings.ReplaceAll(val, `\`, `\\`), `'`, `\'`)
}

func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.TrimSpace(s)
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
