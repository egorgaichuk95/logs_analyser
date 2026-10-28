package normalizer

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

// 1. Тест реального кейса chat-session (OPTIONS 204 с error="" -> LevelInfo)
func TestNormalizeRecord_RealChatSession(t *testing.T) {
	rec := contracts.RawScriptRecord{
		Timestamp: "1790754654285247264",
		Date:      "2026-09-30T07:50:54.285247Z",
		Line:      `{"time":"2026-09-30T07:50:54.285153537Z","id":"d9f74895-ccc5-9ecb-9c38-8fdd487b7dc9","remote_ip":"5.191.119.115","host":"chat-com.pin-up.talkwus.com","method":"OPTIONS","uri":"/api/v1/licenses/com/auth/","status":204,"error":"","latency":9658}`,
		Fields: map[string]string{
			"brand":                  "pinup",
			"cluster":                "crm-ml-pin-up",
			"container":              "chat-session",
			"deployment_environment": "pin-up",
			"detected_level":         "error", // Ложный уровень от Loki!
			"domain":                 "crm",
			"environment":            "pin-up",
			"license":                "ml",
			"node":                   "ip-10-128-21-204.eu-central-1.compute.internal",
			"pod":                    "chat-session-84f9b9749f-8gxvw",
			"service_name":           "chat-session",
			"service_version":        "v1.10.2",
		},
	}

	ev, obj := NormalizeRecord("case-001", rec, "chat-session_part-0020.json", 10)

	if ev.CaseID != "case-001" {
		t.Errorf("expected CaseID 'case-001', got %q", ev.CaseID)
	}
	if ev.SourceFile != "chat-session_part-0020.json" || ev.SourceRecordIdx != 10 {
		t.Errorf("source mismatch: %s / %d", ev.SourceFile, ev.SourceRecordIdx)
	}
	if ev.EventID == "" {
		t.Errorf("expected non-empty EventID")
	}

	// Честный уровень: статус 204 и error="" обязаны давать LevelInfo!
	if ev.SchemaKind != contracts.SchemaEchoAccess {
		t.Fatalf("expected SchemaKind %q, got %q", contracts.SchemaEchoAccess, ev.SchemaKind)
	}
	if ev.Level != contracts.LevelInfo {
		t.Fatalf("CRITICAL: expected honest Level %q, got %q (Loki detected_level must be overridden)", contracts.LevelInfo, ev.Level)
	}
	if ev.SeverityNum != contracts.SeverityInfo {
		t.Errorf("expected SeverityNum %d, got %d", contracts.SeverityInfo, ev.SeverityNum)
	}
	if ev.HTTPStatus != 204 {
		t.Errorf("expected HTTPStatus 204, got %d", ev.HTTPStatus)
	}
	if ev.HTTPMethod != "OPTIONS" {
		t.Errorf("expected HTTPMethod OPTIONS, got %q", ev.HTTPMethod)
	}
	if ev.HTTPRoute != "/api/v1/licenses/com/auth/" {
		t.Errorf("expected HTTPRoute /api/v1/licenses/com/auth/, got %q", ev.HTTPRoute)
	}
	if ev.LatencyNs != 9658 {
		t.Errorf("expected LatencyNs 9658, got %d", ev.LatencyNs)
	}
	if ev.Service != "chat-session" {
		t.Errorf("expected Service chat-session, got %q", ev.Service)
	}
	if ev.Environment != "pin-up" {
		t.Errorf("expected Environment pin-up, got %q", ev.Environment)
	}
	if ev.Pod != "chat-session-84f9b9749f-8gxvw" {
		t.Errorf("expected Pod chat-session-84f9b9749f-8gxvw, got %q", ev.Pod)
	}
	if ev.ServiceVersion != "v1.10.2" {
		t.Errorf("expected ServiceVersion v1.10.2, got %q", ev.ServiceVersion)
	}
	if ev.Message != "HTTP OPTIONS /api/v1/licenses/com/auth/ -> 204" {
		t.Errorf("expected Message 'HTTP OPTIONS /api/v1/licenses/com/auth/ -> 204', got %q", ev.Message)
	}
	if ev.ErrorMsg != "" {
		t.Errorf("expected empty ErrorMsg, got %q", ev.ErrorMsg)
	}
	if obj == nil {
		t.Fatal("expected non-nil parsedJSON map")
	}
}

// 2. Тест реального кейса chat-com (type=app, line=cache:98, time сдвинут на +7200000 мс)
func TestNormalizeRecord_RealChatCom(t *testing.T) {
	rec := contracts.RawScriptRecord{
		Timestamp: "1790750870128800915",
		Date:      "2026-09-30T06:47:50.128800Z",
		Line:      `{"msg": "Operator (operator_id=55754) was not found in cache", "time": "260930 08:47:50 +0000", "level": "ERROR", "line": "cache:98", "type": "app"}`,
		Fields: map[string]string{
			"container":              "chat-com",
			"deployment_environment": "pin-up",
			"detected_level":         "error",
			"node":                   "ip-10-128-17-230.eu-central-1.compute.internal",
			"pod":                    "chat-com-64dbb8949-4dmwd",
			"service_name":           "chat-com",
			"service_version":        "v2.9.13-chat",
		},
	}

	ev, obj := NormalizeRecord("case-001", rec, "chat-com_part-0024.json", 42)

	if ev.SchemaKind != contracts.SchemaPyTornado {
		t.Fatalf("expected SchemaKind %q, got %q", contracts.SchemaPyTornado, ev.SchemaKind)
	}
	if ev.SourcePlace != "cache:98" {
		t.Errorf("expected SourcePlace 'cache:98', got %q", ev.SourcePlace)
	}
	if ev.Level != contracts.LevelError {
		t.Errorf("expected Level %q, got %q", contracts.LevelError, ev.Level)
	}
	if ev.SeverityNum != contracts.SeverityError {
		t.Errorf("expected SeverityNum %d, got %d", contracts.SeverityError, ev.SeverityNum)
	}
	if ev.Message != "Operator (operator_id=55754) was not found in cache" {
		t.Errorf("unexpected message: %q", ev.Message)
	}
	if ev.AppTs == nil {
		t.Fatal("expected non-nil AppTs")
	}

	// Ожидается опережение времени приложения на 2 часа (+7200000 мс)
	if ev.ClockSkewMs < 7199000 || ev.ClockSkewMs > 7201000 {
		t.Errorf("expected ClockSkewMs in range [7199000, 7201000], got %d", ev.ClockSkewMs)
	}
	if obj == nil {
		t.Fatal("expected non-nil parsedJSON map")
	}
}

// 3. Тест всех 6 схем
func TestNormalizeRecord_AllSixSchemas(t *testing.T) {
	tests := []struct {
		name         string
		line         string
		expectedKind string
		checkFunc    func(t *testing.T, ev contracts.NormalizedEvent)
	}{
		{
			name: "GoSlog schema with source map",
			line: `{"msg":"connecting to redis","level":"info","source":{"file":"/workspace/services/client.go","line":173}}`,
			expectedKind: contracts.SchemaGoSlog,
			checkFunc: func(t *testing.T, ev contracts.NormalizedEvent) {
				if ev.SourcePlace != "client.go:173" {
					t.Errorf("expected SourcePlace client.go:173, got %q", ev.SourcePlace)
				}
				if ev.Message != "connecting to redis" {
					t.Errorf("expected msg connecting to redis, got %q", ev.Message)
				}
				if ev.Level != contracts.LevelInfo {
					t.Errorf("expected Level INFO, got %q", ev.Level)
				}
			},
		},
		{
			name: "PyTornado schema with type app and line",
			line: `{"message":"request failed","level":"WARNING","type":"app","line":"views:22"}`,
			expectedKind: contracts.SchemaPyTornado,
			checkFunc: func(t *testing.T, ev contracts.NormalizedEvent) {
				if ev.SourcePlace != "views:22" {
					t.Errorf("expected SourcePlace views:22, got %q", ev.SourcePlace)
				}
				if ev.Level != contracts.LevelWarn {
					t.Errorf("expected Level WARN, got %q", ev.Level)
				}
			},
		},
		{
			name: "EchoAccess schema with method/uri/status/latency",
			line: `{"method":"POST","uri":"/api/v1/sessions","status":201,"latency":12000}`,
			expectedKind: contracts.SchemaEchoAccess,
			checkFunc: func(t *testing.T, ev contracts.NormalizedEvent) {
				if ev.HTTPMethod != "POST" {
					t.Errorf("expected POST, got %q", ev.HTTPMethod)
				}
				if ev.HTTPRoute != "/api/v1/sessions" {
					t.Errorf("expected /api/v1/sessions, got %q", ev.HTTPRoute)
				}
				if ev.HTTPStatus != 201 {
					t.Errorf("expected 201, got %d", ev.HTTPStatus)
				}
				if ev.LatencyNs != 12000 {
					t.Errorf("expected 12000, got %d", ev.LatencyNs)
				}
				if ev.Message != "HTTP POST /api/v1/sessions -> 201" {
					t.Errorf("unexpected EchoAccess message: %q", ev.Message)
				}
			},
		},
		{
			name: "OTelHTTP schema with http.request.method and url",
			line: `{"http.request.method":"GET","http.request.url":"http://service.local/api/items/123","http.duration":5000000,"level":"info"}`,
			expectedKind: contracts.SchemaOTelHTTP,
			checkFunc: func(t *testing.T, ev contracts.NormalizedEvent) {
				if ev.HTTPMethod != "GET" {
					t.Errorf("expected GET, got %q", ev.HTTPMethod)
				}
				if ev.HTTPRoute != "/api/items/<id>" {
					t.Errorf("expected /api/items/<id>, got %q", ev.HTTPRoute)
				}
				if ev.LatencyNs != 5000000 {
					t.Errorf("expected 5000000, got %d", ev.LatencyNs)
				}
			},
		},
		{
			name: "OTelHTTP schema with http.request.method only",
			line: `{"http.request.method":"PUT","http.route":"/api/update","http.duration":2000}`,
			expectedKind: contracts.SchemaOTelHTTP,
			checkFunc: func(t *testing.T, ev contracts.NormalizedEvent) {
				if ev.HTTPMethod != "PUT" {
					t.Errorf("expected PUT, got %q", ev.HTTPMethod)
				}
				if ev.HTTPRoute != "/api/update" {
					t.Errorf("expected /api/update, got %q", ev.HTTPRoute)
				}
			},
		},
		{
			name: "GenericJSON schema",
			line: `{"user_action":"login","account_id":"acc-999","level":"debug","msg":"user signed in"}`,
			expectedKind: contracts.SchemaGenericJSON,
			checkFunc: func(t *testing.T, ev contracts.NormalizedEvent) {
				if ev.Level != contracts.LevelDebug {
					t.Errorf("expected DEBUG, got %q", ev.Level)
				}
				if ev.Message != "user signed in" {
					t.Errorf("expected 'user signed in', got %q", ev.Message)
				}
			},
		},
		{
			name: "PlainText schema",
			line: `Oct 28 14:00:00 server app[123]: Starting background worker`,
			expectedKind: contracts.SchemaPlainText,
			checkFunc: func(t *testing.T, ev contracts.NormalizedEvent) {
				if ev.Message != `Oct 28 14:00:00 server app[123]: Starting background worker` {
					t.Errorf("expected raw line as message, got %q", ev.Message)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := contracts.RawScriptRecord{
				Timestamp: "1790750870000000000",
				Line:      tc.line,
			}
			ev, obj := NormalizeRecord("case-all", rec, "test.log", 1)
			if ev.SchemaKind != tc.expectedKind {
				t.Fatalf("expected SchemaKind %q, got %q", tc.expectedKind, ev.SchemaKind)
			}
			if tc.expectedKind == contracts.SchemaPlainText {
				if obj != nil {
					t.Fatalf("expected nil obj for plain text, got %v", obj)
				}
			} else {
				if obj == nil {
					t.Fatal("expected non-nil obj for json schema")
				}
			}
			tc.checkFunc(t, ev)
		})
	}
}

// 4. Тест нормализации HTTPRoute (UUID, числа в <id>, сохранение префиксов/слешей)
func TestNormalizeRecord_HTTPRouteNormalization(t *testing.T) {
	routes := []struct {
		uri      string
		expected string
	}{
		{"/api/v1/licenses/com/auth/", "/api/v1/licenses/com/auth/"},
		{"/api/v1/users/123/profile", "/api/v1/users/<id>/profile"},
		{"/api/v1/orders/550e8400-e29b-41d4-a716-446655440000/items/42", "/api/v1/orders/<id>/items/<id>"},
		{"/api/v1/orders/550e8400e29b41d4a716446655440000", "/api/v1/orders/<id>"},
		{"/api/v1/licenses/com/auth/?foo=bar&baz=123", "/api/v1/licenses/com/auth/"},
		{"http://api.local/api/v2/items/99#section", "/api/v2/items/<id>"},
		{"/v1/version/100", "/v1/version/<id>"},
		{"/users/0/", "/users/<id>/"},
		{"42", "<id>"},
		{"", ""},
	}

	for _, tc := range routes {
		t.Run(tc.uri, func(t *testing.T) {
			rec := contracts.RawScriptRecord{
				Timestamp: "1790750870000000000",
				Line:      `{"method":"GET","uri":"` + tc.uri + `","status":200,"latency":100}`,
			}
			ev, _ := NormalizeRecord("case-route", rec, "route.log", 0)
			if ev.HTTPRoute != tc.expected {
				t.Errorf("normalizeRoute(%q) = %q; want %q", tc.uri, ev.HTTPRoute, tc.expected)
			}
		})
	}
}

// 5. Тест очистки ключей от пробелов и двоеточий ("license: ", "is vip:")
func TestNormalizeRecord_KeyCleaning(t *testing.T) {
	rec := contracts.RawScriptRecord{
		Timestamp: "1790750870000000000",
		Line:      `{"license: ":"ml","is vip:":true,"status:":200,"msg":"user status"}`,
	}

	_, obj := NormalizeRecord("case-clean", rec, "clean.log", 0)
	if obj == nil {
		t.Fatal("expected non-nil parsed map")
	}

	// Проверяем очищенные ключи
	if obj["license"] != "ml" {
		t.Errorf("expected obj['license'] == 'ml', got %v", obj["license"])
	}
	if obj["is_vip"] != true {
		t.Errorf("expected obj['is_vip'] == true, got %v", obj["is_vip"])
	}
	if obj["status"] != 200.0 && obj["status"] != 200 {
		t.Errorf("expected obj['status'] == 200, got %v", obj["status"])
	}

	// Проверяем сохранение оригинальных ключей
	if obj["license: "] != "ml" {
		t.Errorf("expected original obj['license: '] preserved, got %v", obj["license: "])
	}
	if obj["is vip:"] != true {
		t.Errorf("expected original obj['is vip:'] preserved, got %v", obj["is vip:"])
	}
	if obj["status:"] != 200.0 && obj["status:"] != 200 {
		t.Errorf("expected original obj['status:'] preserved, got %v", obj["status:"])
	}
}

// 6. Тест правил уровней для EchoAccess
func TestNormalizeRecord_EchoAccessLevels(t *testing.T) {
	cases := []struct {
		name          string
		status        int
		errMsg        string
		detectedLevel string
		expectedLevel string
		expectedSev   uint8
	}{
		{"200 ok empty error", 200, "", "error", contracts.LevelInfo, contracts.SeverityInfo},
		{"204 no content empty error", 204, "", "error", contracts.LevelInfo, contracts.SeverityInfo},
		{"304 not modified", 304, "", "warn", contracts.LevelInfo, contracts.SeverityInfo},
		{"400 bad request", 400, "", "info", contracts.LevelWarn, contracts.SeverityWarn},
		{"404 not found", 404, "not found", "error", contracts.LevelWarn, contracts.SeverityWarn},
		{"499 client closed", 499, "client closed", "error", contracts.LevelWarn, contracts.SeverityWarn},
		{"500 internal server error", 500, "", "info", contracts.LevelError, contracts.SeverityError},
		{"502 bad gateway", 502, "bad gateway", "error", contracts.LevelError, contracts.SeverityError},
		{"200 with non-empty error string", 200, "broken pipe", "info", contracts.LevelError, contracts.SeverityError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := contracts.RawScriptRecord{
				Timestamp: "1790750870000000000",
				Line:      `{"method":"GET","uri":"/api/test","status":` + strings.TrimSpace(strings.Split(tc.name, " ")[0]) + `,"error":"` + tc.errMsg + `","latency":10}`,
				Fields: map[string]string{
					"detected_level": tc.detectedLevel,
				},
			}
			ev, _ := NormalizeRecord("case-lvl", rec, "test.log", 0)
			if ev.Level != tc.expectedLevel {
				t.Fatalf("[%s] expected Level %s, got %s", tc.name, tc.expectedLevel, ev.Level)
			}
			if ev.SeverityNum != tc.expectedSev {
				t.Fatalf("[%s] expected SeverityNum %d, got %d", tc.name, tc.expectedSev, ev.SeverityNum)
			}
		})
	}
}

// 7. Тест правил уровней для остальных JSON-схем и fallback
func TestNormalizeRecord_OtherJSONLevels(t *testing.T) {
	tests := []struct {
		jsonLine      string
		detectedLevel string
		expectedLevel string
		expectedSev   uint8
	}{
		{`{"level":"error","msg":"err"}`, "info", contracts.LevelError, contracts.SeverityError},
		{`{"level":"FATAL","msg":"panic"}`, "info", contracts.LevelError, contracts.SeverityError},
		{`{"level":"critical","msg":"oom"}`, "info", contracts.LevelError, contracts.SeverityError},
		{`{"level":"err","msg":"e"}`, "info", contracts.LevelError, contracts.SeverityError},
		{`{"severity":"WARN","msg":"w"}`, "error", contracts.LevelWarn, contracts.SeverityWarn},
		{`{"lvl":"warning","msg":"w"}`, "error", contracts.LevelWarn, contracts.SeverityWarn},
		{`{"level":"info","msg":"i"}`, "error", contracts.LevelInfo, contracts.SeverityInfo},
		{`{"level":"DEBUG","msg":"d"}`, "error", contracts.LevelDebug, contracts.SeverityDebug},
		{`{"level":"trace","msg":"t"}`, "error", contracts.LevelDebug, contracts.SeverityDebug},
		// Поле уровня в JSON отсутствует — берем из detected_level
		{`{"msg":"no level"}`, "error", contracts.LevelError, contracts.SeverityError},
		{`{"msg":"no level"}`, "warning", contracts.LevelWarn, contracts.SeverityWarn},
		{`{"msg":"no level"}`, "warn", contracts.LevelWarn, contracts.SeverityWarn},
		{`{"msg":"no level"}`, "debug", contracts.LevelDebug, contracts.SeverityDebug},
		{`{"msg":"no level"}`, "unknown", contracts.LevelInfo, contracts.SeverityInfo},
		{`{"msg":"no level"}`, "", contracts.LevelInfo, contracts.SeverityInfo},
	}

	for i, tc := range tests {
		rec := contracts.RawScriptRecord{
			Timestamp: "1790750870000000000",
			Line:      tc.jsonLine,
			Fields: map[string]string{
				"detected_level": tc.detectedLevel,
			},
		}
		ev, _ := NormalizeRecord("case-lvl2", rec, "test.log", uint32(i))
		if ev.Level != tc.expectedLevel {
			t.Errorf("case %d: expected Level %s, got %s (json: %s, detected: %s)",
				i, tc.expectedLevel, ev.Level, tc.jsonLine, tc.detectedLevel)
		}
		if ev.SeverityNum != tc.expectedSev {
			t.Errorf("case %d: expected SeverityNum %d, got %d", i, tc.expectedSev, ev.SeverityNum)
		}
	}
}

// 8. Тест парсинга времени Loki и AppTs с ClockSkewMs
func TestNormalizeRecord_TimeAndClockSkew(t *testing.T) {
	// 8.1 Loki Timestamp валидный наносекундный
	rec1 := contracts.RawScriptRecord{
		Timestamp: "1790754654285247264",
		Line:      `{"msg":"hello"}`,
	}
	ev1, _ := NormalizeRecord("c", rec1, "f", 0)
	expectedTs1 := time.Unix(0, 1790754654285247264).UTC()
	if !ev1.Ts.Equal(expectedTs1) {
		t.Errorf("expected Ts %v, got %v", expectedTs1, ev1.Ts)
	}

	// 8.2 Loki Timestamp пуст, используется Date (RFC3339Nano)
	rec2 := contracts.RawScriptRecord{
		Date: "2026-09-30T07:50:54.285247Z",
		Line: `{"msg":"hello"}`,
	}
	ev2, _ := NormalizeRecord("c", rec2, "f", 0)
	expectedTs2, _ := time.Parse(time.RFC3339Nano, "2026-09-30T07:50:54.285247Z")
	if !ev2.Ts.Equal(expectedTs2.UTC()) {
		t.Errorf("expected Ts %v, got %v", expectedTs2, ev2.Ts)
	}

	// 8.3 Timestamp и Date пусты — fallback
	rec3 := contracts.RawScriptRecord{
		Line: `{"msg":"hello"}`,
	}
	ev3, _ := NormalizeRecord("c", rec3, "f", 0)
	if ev3.Ts.IsZero() {
		t.Error("expected non-zero Ts fallback")
	}

	// 8.4 Различные форматы времени внутри obj["time"]
	formats := []struct {
		timeStr string
	}{
		{"2026-09-30T07:50:54.285153537Z"}, // RFC3339Nano
		{"2026-09-30T07:50:54Z"},           // RFC3339
		{"260930 08:47:50 +0000"},          // "060102 15:04:05 -0700"
		{"2026-09-30 07:50:54"},            // fallback datetime
	}

	for _, f := range formats {
		rec := contracts.RawScriptRecord{
			Timestamp: "1790754654000000000",
			Line:      `{"time":"` + f.timeStr + `","msg":"time test"}`,
		}
		ev, _ := NormalizeRecord("c", rec, "f", 0)
		if ev.AppTs == nil {
			t.Errorf("failed to parse time format %q", f.timeStr)
		}
	}

	// 8.5 Невалидная строка time не должна паниковать
	recBadTime := contracts.RawScriptRecord{
		Timestamp: "1790754654000000000",
		Line:      `{"time":"invalid-time-format","msg":"test"}`,
	}
	evBad, _ := NormalizeRecord("c", recBadTime, "f", 0)
	if evBad.AppTs != nil {
		t.Errorf("expected nil AppTs for invalid time, got %v", evBad.AppTs)
	}
	if evBad.ClockSkewMs != 0 {
		t.Errorf("expected ClockSkewMs 0 for invalid time, got %d", evBad.ClockSkewMs)
	}
}

// 9. Тест граничных случаев (Edge Cases)
func TestNormalizeRecord_EdgeCases(t *testing.T) {
	// 9.1 Nil Fields
	recNilFields := contracts.RawScriptRecord{
		Timestamp: "1790754654000000000",
		Line:      `{"msg":"nil fields"}`,
		Fields:    nil,
	}
	evNil, _ := NormalizeRecord("case-nil", recNilFields, "f", 0)
	if evNil.Service != "unknown" {
		t.Errorf("expected service 'unknown', got %q", evNil.Service)
	}

	// 9.2 Fallback выбора сервиса: service_name -> container -> app -> unknown
	recContainer := contracts.RawScriptRecord{
		Line: `{"msg":"container test"}`,
		Fields: map[string]string{
			"container": "my-container",
		},
	}
	evContainer, _ := NormalizeRecord("c", recContainer, "f", 0)
	if evContainer.Service != "my-container" {
		t.Errorf("expected service 'my-container', got %q", evContainer.Service)
	}

	recApp := contracts.RawScriptRecord{
		Line: `{"msg":"app test"}`,
		Fields: map[string]string{
			"app": "my-app",
		},
	}
	evApp, _ := NormalizeRecord("c", recApp, "f", 0)
	if evApp.Service != "my-app" {
		t.Errorf("expected service 'my-app', got %q", evApp.Service)
	}

	// 9.3 Environment fallback: deployment_environment -> environment -> ""
	recEnv := contracts.RawScriptRecord{
		Line: `{"msg":"env test"}`,
		Fields: map[string]string{
			"environment": "staging",
		},
	}
	evEnv, _ := NormalizeRecord("c", recEnv, "f", 0)
	if evEnv.Environment != "staging" {
		t.Errorf("expected environment 'staging', got %q", evEnv.Environment)
	}

	// 9.4 Невалидный JSON / пустая строка
	recEmpty := contracts.RawScriptRecord{
		Line: "",
	}
	evEmpty, objEmpty := NormalizeRecord("c", recEmpty, "f", 0)
	if evEmpty.SchemaKind != contracts.SchemaPlainText {
		t.Errorf("expected SchemaPlainText for empty line, got %s", evEmpty.SchemaKind)
	}
	if objEmpty != nil {
		t.Errorf("expected nil obj for empty line, got %v", objEmpty)
	}

	recCorrupted := contracts.RawScriptRecord{
		Line: `{"unclosed_json: true`,
	}
	evCorrupted, objCorrupted := NormalizeRecord("c", recCorrupted, "f", 0)
	if evCorrupted.SchemaKind != contracts.SchemaPlainText {
		t.Errorf("expected SchemaPlainText for corrupted JSON, got %s", evCorrupted.SchemaKind)
	}
	if objCorrupted != nil {
		t.Errorf("expected nil obj for corrupted JSON, got %v", objCorrupted)
	}

	// 9.5 Числовые и строковые типы для latency и status
	recStringTypes := contracts.RawScriptRecord{
		Line: `{"method":"GET","uri":"/test","status":"200","latency":"5500"}`,
	}
	evStr, _ := NormalizeRecord("c", recStringTypes, "f", 0)
	if evStr.HTTPStatus != 200 || evStr.LatencyNs != 5500 {
		t.Errorf("expected status 200 and latency 5500, got status %d latency %d", evStr.HTTPStatus, evStr.LatencyNs)
	}

	// 9.6 Пустые сообщения fallback на rec.Line
	recNoMsg := contracts.RawScriptRecord{
		Line: `{"level":"info","code":123}`,
	}
	evNoMsg, _ := NormalizeRecord("c", recNoMsg, "f", 0)
	if evNoMsg.Message != recNoMsg.Line {
		t.Errorf("expected fallback message %q, got %q", recNoMsg.Line, evNoMsg.Message)
	}

	// 9.7 ErrorMsg извлечение
	recErr := contracts.RawScriptRecord{
		Line: `{"msg":"failed","error":"network timeout"}`,
	}
	evErr, _ := NormalizeRecord("c", recErr, "f", 0)
	if evErr.ErrorMsg != "network timeout" {
		t.Errorf("expected ErrorMsg 'network timeout', got %q", evErr.ErrorMsg)
	}
}

// 10. Проверка уникальности UUID у генерируемых записей
func TestNormalizeRecord_UniqueUUID(t *testing.T) {
	rec := contracts.RawScriptRecord{
		Line: `{"msg":"id test"}`,
	}
	ev1, _ := NormalizeRecord("c1", rec, "f", 1)
	ev2, _ := NormalizeRecord("c1", rec, "f", 2)

	if ev1.EventID == "" || ev2.EventID == "" {
		t.Fatal("EventID should not be empty")
	}
	if ev1.EventID == ev2.EventID {
		t.Fatalf("EventIDs should be unique: got %q for both", ev1.EventID)
	}
	if len(ev1.EventID) != 36 {
		t.Fatalf("EventID should be 36 characters UUID format, got %q (len %d)", ev1.EventID, len(ev1.EventID))
	}
}

// Вспомогательный тест для SanitizeFloat
func TestSanitizeFloatDirect(t *testing.T) {
	if contracts.SanitizeFloat(math.NaN()) != 0.0 {
		t.Error("expected 0 for NaN")
	}
}
