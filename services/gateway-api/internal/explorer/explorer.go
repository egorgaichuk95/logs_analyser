package explorer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/gateway-api/internal/metadb"
)

type Explorer struct {
	chHTTPURL  string
	httpClient *http.Client
	meta       *metadb.Store
}

func NewExplorer(chHTTPURL string, meta *metadb.Store, client *http.Client) *Explorer {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Explorer{
		chHTTPURL:  strings.TrimRight(chHTTPURL, "/"),
		httpClient: client,
		meta:       meta,
	}
}

type chRawEvent struct {
	CaseID           string  `json:"case_id"`
	EventID          string  `json:"event_id"`
	Ts               string  `json:"ts"`
	AppTs            *string `json:"app_ts"`
	ClockSkewMs      int64   `json:"clock_skew_ms"`
	Service          string  `json:"service"`
	Environment      string  `json:"environment"`
	Pod              string  `json:"pod"`
	Node             string  `json:"node"`
	ServiceVersion   string  `json:"service_version"`
	Level            string  `json:"level"`
	SeverityNum      uint8   `json:"severity_num"`
	SchemaKind       string  `json:"schema_kind"`
	TemplateID       uint64  `json:"template_id"`
	Message          string  `json:"message"`
	ErrorClass       string  `json:"error_class"`
	ErrorMsg         string  `json:"error_msg"`
	ExceptionType    string  `json:"exception_type"`
	SourcePlace      string  `json:"source_place"`
	StackFingerprint uint64  `json:"stack_fingerprint"`
	TraceID          string  `json:"trace_id"`
	SpanID           string  `json:"span_id"`
	RequestID        string  `json:"request_id"`
	SessionID        string  `json:"session_id"`
	OperatorID       string  `json:"operator_id"`
	VisitorID        string  `json:"visitor_id"`
	HTTPMethod       string  `json:"http_method"`
	HTTPRoute        string  `json:"http_route"`
	HTTPStatus       uint16  `json:"http_status"`
	LatencyNs        uint64  `json:"latency_ns"`
	PeerService      string  `json:"peer_service"`
	SourceFile       string  `json:"source_file"`
	SourceRecordIdx  uint32  `json:"source_record_idx"`
	RawHash          uint64  `json:"raw_hash"`
	RawRedacted      string  `json:"raw_redacted"`
}

type chCountRow struct {
	Cnt json.Number `json:"cnt"`
}

func escapeSQL(val string) string {
	return strings.ReplaceAll(strings.ReplaceAll(val, `\`, `\\`), `'`, `\'`)
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
		"2006-01-02T15:04:05",
	}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, s, time.UTC); err == nil {
			return t
		}
	}
	return time.Time{}
}

func formatCHTimestamp(tsStr string) string {
	t := parseCHDateTime(tsStr)
	if !t.IsZero() {
		return t.UTC().Format("2006-01-02 15:04:05.000000000")
	}
	return escapeSQL(tsStr)
}

func (e *Explorer) queryClickHouse(ctx context.Context, sql string) (*http.Response, error) {
	if e.chHTTPURL == "" {
		return nil, errors.New("clickhouse url not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.chHTTPURL, strings.NewReader(sql+"\n"))
	if err != nil {
		return nil, fmt.Errorf("create clickhouse request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute clickhouse query: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("clickhouse status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return resp, nil
}

func (e *Explorer) GetContextLogs(ctx context.Context, caseID, pod, tsStr, mode string) (contracts.ContextLogsResponse, error) {
	if mode != contracts.ContextModeAllServices {
		mode = contracts.ContextModePod
	}

	if caseID == "" {
		return contracts.ContextLogsResponse{
			RawPurged: true,
			Mode:      mode,
			Events:    []contracts.NormalizedEvent{},
		}, nil
	}

	// 1. Проверяем metadb на статус RAW_PURGED
	if e.meta != nil {
		if c, ok := e.meta.GetCase(caseID); ok && c.Status == contracts.CaseStatusRawPurged {
			return contracts.ContextLogsResponse{
				RawPurged: true,
				Mode:      mode,
				Events:    []contracts.NormalizedEvent{},
			}, nil
		}
	}

	escCaseID := escapeSQL(caseID)

	// 2. Проверяем наличие сырых строк кейса в logs_rca.events_raw
	countSQL := fmt.Sprintf("SELECT count() AS cnt FROM logs_rca.events_raw WHERE case_id = '%s' FORMAT JSONEachRow", escCaseID)
	countResp, err := e.queryClickHouse(ctx, countSQL)
	if err != nil {
		// Если ClickHouse вернул ошибку и мета говорит, что кейс очищен — возвращаем RawPurged: true
		if e.meta != nil {
			if c, ok := e.meta.GetCase(caseID); ok && c.Status == contracts.CaseStatusRawPurged {
				return contracts.ContextLogsResponse{
					RawPurged: true,
					Mode:      mode,
					Events:    []contracts.NormalizedEvent{},
				}, nil
			}
		}
		return contracts.ContextLogsResponse{}, fmt.Errorf("query events count: %w", err)
	}
	defer countResp.Body.Close()

	var cnt uint64
	dec := json.NewDecoder(countResp.Body)
	for dec.More() {
		var crow chCountRow
		if err := dec.Decode(&crow); err == nil {
			u, err := strconv.ParseUint(crow.Cnt.String(), 10, 64)
			if err == nil {
				cnt = u
			}
		}
	}

	if cnt == 0 {
		return contracts.ContextLogsResponse{
			RawPurged: true,
			Mode:      mode,
			Events:    []contracts.NormalizedEvent{},
		}, nil
	}

	// 3. Выполняем контекстный запрос
	formattedTs := formatCHTimestamp(tsStr)
	var query string
	if mode == contracts.ContextModePod && pod != "" {
		escPod := escapeSQL(pod)
		query = fmt.Sprintf("SELECT case_id, event_id, ts, app_ts, clock_skew_ms, service, environment, pod, node, service_version, level, severity_num, schema_kind, template_id, message, error_class, error_msg, exception_type, source_place, stack_fingerprint, trace_id, span_id, request_id, session_id, operator_id, visitor_id, http_method, http_route, http_status, latency_ns, peer_service, source_file, source_record_idx, raw_hash, raw_redacted FROM logs_rca.events_raw WHERE case_id = '%s' AND pod = '%s' AND ts BETWEEN toDateTime64('%s', 9, 'UTC') - INTERVAL 30 SECOND AND toDateTime64('%s', 9, 'UTC') + INTERVAL 30 SECOND ORDER BY ts ASC LIMIT 40 FORMAT JSONEachRow", escCaseID, escPod, formattedTs, formattedTs)
	} else {
		query = fmt.Sprintf("SELECT case_id, event_id, ts, app_ts, clock_skew_ms, service, environment, pod, node, service_version, level, severity_num, schema_kind, template_id, message, error_class, error_msg, exception_type, source_place, stack_fingerprint, trace_id, span_id, request_id, session_id, operator_id, visitor_id, http_method, http_route, http_status, latency_ns, peer_service, source_file, source_record_idx, raw_hash, raw_redacted FROM logs_rca.events_raw WHERE case_id = '%s' AND ts BETWEEN toDateTime64('%s', 9, 'UTC') - INTERVAL 30 SECOND AND toDateTime64('%s', 9, 'UTC') + INTERVAL 30 SECOND ORDER BY ts ASC LIMIT 40 FORMAT JSONEachRow", escCaseID, formattedTs, formattedTs)
	}

	resp, err := e.queryClickHouse(ctx, query)
	if err != nil {
		return contracts.ContextLogsResponse{}, fmt.Errorf("query context logs: %w", err)
	}
	defer resp.Body.Close()

	events := make([]contracts.NormalizedEvent, 0)
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var re chRawEvent
		if err := json.Unmarshal(line, &re); err == nil {
			parsedTs := parseCHDateTime(re.Ts)
			var appTs *time.Time
			if re.AppTs != nil && *re.AppTs != "" {
				t := parseCHDateTime(*re.AppTs)
				appTs = &t
			}
			events = append(events, contracts.NormalizedEvent{
				CaseID:           re.CaseID,
				EventID:          re.EventID,
				Ts:               parsedTs,
				AppTs:            appTs,
				ClockSkewMs:      re.ClockSkewMs,
				Service:          re.Service,
				Environment:      re.Environment,
				Pod:              re.Pod,
				Node:             re.Node,
				ServiceVersion:   re.ServiceVersion,
				Level:            re.Level,
				SeverityNum:      re.SeverityNum,
				SchemaKind:       re.SchemaKind,
				TemplateID:       re.TemplateID,
				Message:          re.Message,
				ErrorClass:       re.ErrorClass,
				ErrorMsg:         re.ErrorMsg,
				ExceptionType:    re.ExceptionType,
				SourcePlace:      re.SourcePlace,
				StackFingerprint: re.StackFingerprint,
				TraceID:          re.TraceID,
				SpanID:           re.SpanID,
				RequestID:        re.RequestID,
				SessionID:        re.SessionID,
				OperatorID:       re.OperatorID,
				VisitorID:        re.VisitorID,
				HTTPMethod:       re.HTTPMethod,
				HTTPRoute:        re.HTTPRoute,
				HTTPStatus:       re.HTTPStatus,
				LatencyNs:        re.LatencyNs,
				PeerService:      re.PeerService,
				SourceFile:       re.SourceFile,
				SourceRecordIdx:  re.SourceRecordIdx,
				RawHash:          re.RawHash,
				RawRedacted:      re.RawRedacted,
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return contracts.ContextLogsResponse{}, fmt.Errorf("scan context logs: %w", err)
	}

	return contracts.ContextLogsResponse{
		RawPurged: false,
		Mode:      mode,
		Events:    events,
	}, nil
}
