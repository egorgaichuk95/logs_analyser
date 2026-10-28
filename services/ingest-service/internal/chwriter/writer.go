package chwriter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

type Writer struct {
	baseURL string
	client  *http.Client
}

func NewWriter(baseURL string) *Writer {
	return &Writer{
		baseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func escapeSQL(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `'`, `\'`)
}

func formatCHTime(t time.Time) string {
	if t.IsZero() {
		return "1970-01-01 00:00:00.000000000"
	}
	return t.UTC().Format("2006-01-02 15:04:05.000000000")
}

type chIngestedFile struct {
	CaseID       string `json:"case_id"`
	FileSHA256   string `json:"file_sha256"`
	FileName     string `json:"file_name"`
	RowsIngested uint64 `json:"rows_ingested"`
	IngestedAt   string `json:"ingested_at"`
}

type chEvent struct {
	CaseID           string  `json:"case_id"`
	EventID          string  `json:"event_id"`
	Ts               string  `json:"ts"`
	AppTs            *string `json:"app_ts,omitempty"`
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

func toCHEvent(ev contracts.NormalizedEvent) chEvent {
	var appTsStr *string
	if ev.AppTs != nil {
		s := formatCHTime(*ev.AppTs)
		appTsStr = &s
	}
	return chEvent{
		CaseID:           ev.CaseID,
		EventID:          ev.EventID,
		Ts:               formatCHTime(ev.Ts),
		AppTs:            appTsStr,
		ClockSkewMs:      ev.ClockSkewMs,
		Service:          ev.Service,
		Environment:      ev.Environment,
		Pod:              ev.Pod,
		Node:             ev.Node,
		ServiceVersion:   ev.ServiceVersion,
		Level:            ev.Level,
		SeverityNum:      ev.SeverityNum,
		SchemaKind:       ev.SchemaKind,
		TemplateID:       ev.TemplateID,
		Message:          ev.Message,
		ErrorClass:       ev.ErrorClass,
		ErrorMsg:         ev.ErrorMsg,
		ExceptionType:    ev.ExceptionType,
		SourcePlace:      ev.SourcePlace,
		StackFingerprint: ev.StackFingerprint,
		TraceID:          ev.TraceID,
		SpanID:           ev.SpanID,
		RequestID:        ev.RequestID,
		SessionID:        ev.SessionID,
		OperatorID:       ev.OperatorID,
		VisitorID:        ev.VisitorID,
		HTTPMethod:       ev.HTTPMethod,
		HTTPRoute:        ev.HTTPRoute,
		HTTPStatus:       ev.HTTPStatus,
		LatencyNs:        ev.LatencyNs,
		PeerService:      ev.PeerService,
		SourceFile:       ev.SourceFile,
		SourceRecordIdx:  ev.SourceRecordIdx,
		RawHash:          ev.RawHash,
		RawRedacted:      ev.RawRedacted,
	}
}

type chTemplate struct {
	CaseID         string `json:"case_id"`
	Service        string `json:"service"`
	TemplateID     uint64 `json:"template_id"`
	TemplateText   string `json:"template_text"`
	Level          string `json:"level"`
	ErrorClass     string `json:"error_class"`
	SourcePlace    string `json:"source_place"`
	SampleRedacted string `json:"sample_redacted"`
	FirstSeen      string `json:"first_seen"`
	LastSeen       string `json:"last_seen"`
}

func toCHTemplate(t contracts.TemplateRecord) chTemplate {
	return chTemplate{
		CaseID:         t.CaseID,
		Service:        t.Service,
		TemplateID:     t.TemplateID,
		TemplateText:   t.TemplateText,
		Level:          t.Level,
		ErrorClass:     t.ErrorClass,
		SourcePlace:    t.SourcePlace,
		SampleRedacted: t.SampleRedacted,
		FirstSeen:      formatCHTime(t.FirstSeen),
		LastSeen:       formatCHTime(t.LastSeen),
	}
}

type chIdentityEdge struct {
	CaseID    string `json:"case_id"`
	Ts        string `json:"ts"`
	Service   string `json:"service"`
	LeftType  string `json:"left_type"`
	LeftVal   string `json:"left_val"`
	RightType string `json:"right_type"`
	RightVal  string `json:"right_val"`
}

func toCHIdentityEdge(edge contracts.IdentityEdge) chIdentityEdge {
	return chIdentityEdge{
		CaseID:    edge.CaseID,
		Ts:        formatCHTime(edge.Ts),
		Service:   edge.Service,
		LeftType:  edge.LeftType,
		LeftVal:   edge.LeftVal,
		RightType: edge.RightType,
		RightVal:  edge.RightVal,
	}
}

// IsFileAlreadyIngested проверяет, есть ли (case_id, file_sha256) в logs_rca.ingested_files
func (w *Writer) IsFileAlreadyIngested(ctx context.Context, caseID, fileSHA256 string) (bool, error) {
	query := fmt.Sprintf("SELECT count() FROM logs_rca.ingested_files WHERE case_id = '%s' AND file_sha256 = '%s' FORMAT TabSeparated",
		escapeSQL(caseID), escapeSQL(fileSHA256))
	endpoint := w.baseURL + "/?query=" + url.QueryEscape(query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, fmt.Errorf("create ch request: %w", err)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("do ch query: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("read ch response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("clickhouse query status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	trimmed := strings.TrimSpace(string(body))
	count, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return false, fmt.Errorf("parse count from ch response %q: %w", trimmed, err)
	}

	return count > 0, nil
}

// RecordIngestedFile записывает факт загрузки файла в logs_rca.ingested_files
func (w *Writer) RecordIngestedFile(ctx context.Context, caseID, fileSHA256, fileName string, rows uint64) error {
	rec := chIngestedFile{
		CaseID:       caseID,
		FileSHA256:   fileSHA256,
		FileName:     fileName,
		RowsIngested: rows,
		IngestedAt:   time.Now().UTC().Format("2006-01-02 15:04:05"),
	}

	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal ingested file: %w", err)
	}
	data = append(data, '\n')

	endpoint := w.baseURL + "/?date_time_input_format=best_effort&query=" + url.QueryEscape("INSERT INTO logs_rca.ingested_files FORMAT JSONEachRow")
	return w.sendInsert(ctx, endpoint, data)
}

func (w *Writer) sendInsert(ctx context.Context, endpoint string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create ch insert request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("do ch insert: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("clickhouse insert status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return nil
}

// FlushBatch отправляет накопленный батч (до 5000 строк) в logs_rca.events_raw, templates_dict, identity_edges, service_edges
func (w *Writer) FlushBatch(
	ctx context.Context,
	events []contracts.NormalizedEvent,
	templates []contracts.TemplateRecord,
	idEdges []contracts.IdentityEdge,
	srvEdges []contracts.ServiceEdge,
) error {
	if len(events) > 0 {
		var buf bytes.Buffer
		for _, ev := range events {
			b, err := json.Marshal(toCHEvent(ev))
			if err != nil {
				return fmt.Errorf("marshal event: %w", err)
			}
			buf.Write(b)
			buf.WriteByte('\n')
		}
		endpoint := w.baseURL + "/?date_time_input_format=best_effort&query=" + url.QueryEscape("INSERT INTO logs_rca.events_raw FORMAT JSONEachRow")
		if err := w.sendInsert(ctx, endpoint, buf.Bytes()); err != nil {
			return fmt.Errorf("insert events_raw: %w", err)
		}
	}

	if len(templates) > 0 {
		var buf bytes.Buffer
		for _, t := range templates {
			b, err := json.Marshal(toCHTemplate(t))
			if err != nil {
				return fmt.Errorf("marshal template: %w", err)
			}
			buf.Write(b)
			buf.WriteByte('\n')
		}
		endpoint := w.baseURL + "/?date_time_input_format=best_effort&query=" + url.QueryEscape("INSERT INTO logs_rca.templates_dict FORMAT JSONEachRow")
		if err := w.sendInsert(ctx, endpoint, buf.Bytes()); err != nil {
			return fmt.Errorf("insert templates_dict: %w", err)
		}
	}

	if len(idEdges) > 0 {
		var buf bytes.Buffer
		for _, edge := range idEdges {
			b, err := json.Marshal(toCHIdentityEdge(edge))
			if err != nil {
				return fmt.Errorf("marshal identity edge: %w", err)
			}
			buf.Write(b)
			buf.WriteByte('\n')
		}
		endpoint := w.baseURL + "/?date_time_input_format=best_effort&query=" + url.QueryEscape("INSERT INTO logs_rca.identity_edges FORMAT JSONEachRow")
		if err := w.sendInsert(ctx, endpoint, buf.Bytes()); err != nil {
			return fmt.Errorf("insert identity_edges: %w", err)
		}
	}

	if len(srvEdges) > 0 {
		var buf bytes.Buffer
		for _, edge := range srvEdges {
			b, err := json.Marshal(edge)
			if err != nil {
				return fmt.Errorf("marshal service edge: %w", err)
			}
			buf.Write(b)
			buf.WriteByte('\n')
		}
		endpoint := w.baseURL + "/?date_time_input_format=best_effort&query=" + url.QueryEscape("INSERT INTO logs_rca.service_edges FORMAT JSONEachRow")
		if err := w.sendInsert(ctx, endpoint, buf.Bytes()); err != nil {
			return fmt.Errorf("insert service_edges: %w", err)
		}
	}

	return nil
}
