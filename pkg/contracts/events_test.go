package contracts

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestSeverityFromLevel(t *testing.T) {
	tests := []struct {
		name     string
		level    string
		expected uint8
	}{
		{"Error", LevelError, SeverityError},
		{"Warn", LevelWarn, SeverityWarn},
		{"Info", LevelInfo, SeverityInfo},
		{"Debug", LevelDebug, SeverityDebug},
		{"Unknown Level", "UNKNOWN", SeverityDebug},
		{"Empty Level", "", SeverityDebug},
		{"Trace Level", "TRACE", SeverityDebug},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SeverityFromLevel(tc.level)
			if got != tc.expected {
				t.Fatalf("SeverityFromLevel(%q) = %d; want %d", tc.level, got, tc.expected)
			}
		})
	}
}

func TestSanitizeFloat(t *testing.T) {
	tests := []struct {
		name     string
		input    float64
		expected float64
	}{
		{"NaN", math.NaN(), 0.0},
		{"Positive Inf", math.Inf(1), 0.0},
		{"Negative Inf", math.Inf(-1), 0.0},
		{"Zero", 0.0, 0.0},
		{"Positive value", 123.456, 123.456},
		{"Negative value", -987.654, -987.654},
		{"MaxFloat64", math.MaxFloat64, math.MaxFloat64},
		{"SmallestNonzeroFloat64", math.SmallestNonzeroFloat64, math.SmallestNonzeroFloat64},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeFloat(tc.input)
			if math.IsNaN(tc.input) || math.IsInf(tc.input, 0) {
				if got != 0.0 || math.IsNaN(got) || math.IsInf(got, 0) {
					t.Fatalf("SanitizeFloat(%v) = %v; want 0.0", tc.input, got)
				}
			} else if got != tc.expected {
				t.Fatalf("SanitizeFloat(%v) = %v; want %v", tc.input, got, tc.expected)
			}
		})
	}
}

func TestSanitizeFloat_JSONSerialization(t *testing.T) {
	type testPayload struct {
		Score float64 `json:"score"`
	}

	rawNaN := testPayload{Score: math.NaN()}
	if _, err := json.Marshal(rawNaN); err == nil {
		t.Fatal("expected error serializing raw NaN to JSON, but got none")
	}

	sanitizedNaN := testPayload{Score: SanitizeFloat(math.NaN())}
	data, err := json.Marshal(sanitizedNaN)
	if err != nil {
		t.Fatalf("failed to marshal sanitized float: %v", err)
	}
	expectedJSON := `{"score":0}`
	if string(data) != expectedJSON {
		t.Fatalf("marshaled json = %s; want %s", string(data), expectedJSON)
	}

	sanitizedInf := testPayload{Score: SanitizeFloat(math.Inf(1))}
	dataInf, err := json.Marshal(sanitizedInf)
	if err != nil {
		t.Fatalf("failed to marshal sanitized +Inf: %v", err)
	}
	if string(dataInf) != expectedJSON {
		t.Fatalf("marshaled json = %s; want %s", string(dataInf), expectedJSON)
	}
}

func TestRawScriptRecord_JSON(t *testing.T) {
	rec := RawScriptRecord{
		Timestamp: "2026-10-28T12:00:00Z",
		Date:      "2026-10-28",
		Line:      "part-0001: 500 internal server error",
		Fields: map[string]string{
			"status": "500",
			"pod":    "chat-com-abc",
		},
	}

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("failed to marshal RawScriptRecord: %v", err)
	}

	var parsed RawScriptRecord
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal RawScriptRecord: %v", err)
	}

	if parsed.Timestamp != rec.Timestamp || parsed.Date != rec.Date || parsed.Line != rec.Line {
		t.Fatalf("parsed record mismatch: %+v vs %+v", parsed, rec)
	}
	if parsed.Fields["status"] != "500" || parsed.Fields["pod"] != "chat-com-abc" {
		t.Fatalf("parsed fields mismatch: %+v", parsed.Fields)
	}
}

func TestNormalizedEvent_JSON(t *testing.T) {
	fixedTime := time.Date(2026, 10, 28, 14, 30, 0, 0, time.UTC)
	appTime := fixedTime.Add(-100 * time.Millisecond)

	event := NormalizedEvent{
		CaseID:           "case-01",
		EventID:          "ev-999",
		Ts:               fixedTime,
		AppTs:            &appTime,
		ClockSkewMs:      100,
		Service:          "chat-com",
		Environment:      "production",
		Pod:              "chat-com-7bb5d",
		Node:             "node-01",
		ServiceVersion:   "v1.4.2",
		Level:            LevelError,
		SeverityNum:      SeverityError,
		SchemaKind:       SchemaGoSlog,
		TemplateID:       1234567890,
		Message:          "database connection refused",
		ErrorClass:       ErrClassUnavailable,
		ErrorMsg:         "dial tcp 10.0.0.5:5432: connect: connection refused",
		ExceptionType:    "net.OpError",
		SourcePlace:      "main.go:123",
		StackFingerprint: 9876543210,
		TraceID:          "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:           "00f067aa0ba902b7",
		RequestID:        "req-xyz-123",
		SessionID:        "sess-abc-789",
		OperatorID:       "op-1",
		VisitorID:        "vis-2",
		HTTPMethod:       "POST",
		HTTPRoute:        "/api/v1/chat",
		HTTPStatus:       503,
		LatencyNs:        15000000,
		PeerService:      "postgres",
		SourceFile:       "chat-com_part-0001.json",
		SourceRecordIdx:  42,
		RawHash:          1122334455,
		RawRedacted:      `{"level":"ERROR","msg":"database connection refused"}`,
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal NormalizedEvent: %v", err)
	}

	var parsed NormalizedEvent
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal NormalizedEvent: %v", err)
	}

	if parsed.CaseID != event.CaseID || parsed.EventID != event.EventID || !parsed.Ts.Equal(event.Ts) {
		t.Fatalf("parsed event mismatch: %+v vs %+v", parsed, event)
	}
	if parsed.AppTs == nil || !parsed.AppTs.Equal(*event.AppTs) {
		t.Fatalf("parsed AppTs mismatch: got %v, want %v", parsed.AppTs, event.AppTs)
	}
	if parsed.SeverityNum != SeverityError || parsed.Level != LevelError || parsed.ErrorClass != ErrClassUnavailable {
		t.Fatalf("parsed error/level fields mismatch: %+v", parsed)
	}
	if parsed.TraceID != event.TraceID || parsed.SpanID != event.SpanID || parsed.LatencyNs != event.LatencyNs {
		t.Fatalf("parsed trace/latency fields mismatch: %+v", parsed)
	}

	// Test without AppTs (omitempty check)
	eventNoAppTs := event
	eventNoAppTs.AppTs = nil
	dataNoAppTs, err := json.Marshal(eventNoAppTs)
	if err != nil {
		t.Fatalf("failed to marshal NormalizedEvent without AppTs: %v", err)
	}
	var parsedNoAppTs NormalizedEvent
	if err := json.Unmarshal(dataNoAppTs, &parsedNoAppTs); err != nil {
		t.Fatalf("failed to unmarshal NormalizedEvent without AppTs: %v", err)
	}
	if parsedNoAppTs.AppTs != nil {
		t.Fatalf("expected nil AppTs, got %v", parsedNoAppTs.AppTs)
	}
}

func TestIdentityEdge_JSON(t *testing.T) {
	ts := time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)
	edge := IdentityEdge{
		CaseID:    "case-01",
		Ts:        ts,
		Service:   "chat-session",
		LeftType:  "request_id",
		LeftVal:   "req-123",
		RightType: "session_id",
		RightVal:  "sess-456",
	}

	data, err := json.Marshal(edge)
	if err != nil {
		t.Fatalf("failed to marshal IdentityEdge: %v", err)
	}

	var parsed IdentityEdge
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal IdentityEdge: %v", err)
	}

	if parsed.CaseID != edge.CaseID || !parsed.Ts.Equal(edge.Ts) || parsed.LeftVal != edge.LeftVal || parsed.RightVal != edge.RightVal {
		t.Fatalf("parsed IdentityEdge mismatch: %+v vs %+v", parsed, edge)
	}
}

func TestServiceEdge_JSON(t *testing.T) {
	edge := ServiceEdge{
		CaseID:         "case-01",
		CallerService:  "chat-com",
		CalleeService:  "chat-session",
		EvidenceSource: "peer_url",
		CallCount:      150,
		ErrorCount:     12,
	}

	data, err := json.Marshal(edge)
	if err != nil {
		t.Fatalf("failed to marshal ServiceEdge: %v", err)
	}

	var parsed ServiceEdge
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal ServiceEdge: %v", err)
	}

	if parsed != edge {
		t.Fatalf("parsed ServiceEdge mismatch: %+v vs %+v", parsed, edge)
	}
}

func TestTemplateRecord_JSON(t *testing.T) {
	t1 := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 10, 28, 11, 0, 0, 0, time.UTC)
	rec := TemplateRecord{
		CaseID:         "case-01",
		Service:        "chat-com",
		TemplateID:     1001,
		TemplateText:   "Connection to <*> timed out after <*> ms",
		Level:          LevelWarn,
		ErrorClass:     ErrClassDeadline,
		SourcePlace:    "client.go:88",
		SampleRedacted: "Connection to api.internal timed out after 5000 ms",
		FirstSeen:      t1,
		LastSeen:       t2,
	}

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("failed to marshal TemplateRecord: %v", err)
	}

	var parsed TemplateRecord
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal TemplateRecord: %v", err)
	}

	if parsed.CaseID != rec.CaseID || parsed.TemplateID != rec.TemplateID || !parsed.FirstSeen.Equal(rec.FirstSeen) || !parsed.LastSeen.Equal(rec.LastSeen) {
		t.Fatalf("parsed TemplateRecord mismatch: %+v vs %+v", parsed, rec)
	}
}
