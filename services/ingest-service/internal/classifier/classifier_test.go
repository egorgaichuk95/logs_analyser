package classifier

import (
	"strings"
	"testing"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

// 1. Blueprint Scenario 1: JWT masking in URL, context canceled, PeerService chat-com, SessionID
func TestEnrichAndClassify_Scenario1_JWTAndCanceled(t *testing.T) {
	rawLine := `Get "http://chat-com.chat.svc.cluster.local:8260/l/v/init?action=init&auth-token=eyJhbGciOiJSUzI1NiIsInR5cCIgOiAiSldUIiwia2lkIiA6ICJXdVVwd2M3MTIzNDU2Nzg5MCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c&visit-session-id=18cb9e028be84b0d8378e56447909e12": context canceled`

	ev := &contracts.NormalizedEvent{
		CaseID:  "case-sc1",
		Service: "chat-session",
		Level:   contracts.LevelError,
		Message: rawLine,
		Ts:      time.Now().UTC(),
	}

	edges, sEdge := EnrichAndClassify(ev, nil, rawLine)

	if strings.Contains(ev.RawRedacted, "eyJhbGci") {
		t.Errorf("ev.RawRedacted must NOT contain raw JWT token, got: %s", ev.RawRedacted)
	}
	if !strings.Contains(ev.RawRedacted, "<REDACTED_JWT:") {
		t.Errorf("ev.RawRedacted must contain <REDACTED_JWT:..., got: %s", ev.RawRedacted)
	}
	if ev.ErrorClass != contracts.ErrClassCanceled {
		t.Errorf("expected ErrorClass %q, got %q", contracts.ErrClassCanceled, ev.ErrorClass)
	}
	if ev.PeerService != "chat-com" {
		t.Errorf("expected PeerService 'chat-com', got %q", ev.PeerService)
	}
	if ev.SessionID != "18cb9e028be84b0d8378e56447909e12" {
		t.Errorf("expected SessionID '18cb9e028be84b0d8378e56447909e12', got %q", ev.SessionID)
	}
	if sEdge == nil {
		t.Fatalf("expected non-nil ServiceEdge")
	}
	if sEdge.CalleeService != "chat-com" {
		t.Errorf("expected CalleeService 'chat-com', got %q", sEdge.CalleeService)
	}
	if sEdge.CallerService != "chat-session" {
		t.Errorf("expected CallerService 'chat-session', got %q", sEdge.CallerService)
	}
	if sEdge.EvidenceSource != "peer_url" {
		t.Errorf("expected EvidenceSource 'peer_url', got %q", sEdge.EvidenceSource)
	}
	if sEdge.CallCount != 1 {
		t.Errorf("expected CallCount 1, got %d", sEdge.CallCount)
	}
	if sEdge.ErrorCount != 1 {
		t.Errorf("expected ErrorCount 1, got %d", sEdge.ErrorCount)
	}
	if ev.RawHash == 0 {
		t.Errorf("expected non-zero RawHash")
	}
	_ = edges
}

// 2. Blueprint Scenario 2: Traceparent and X-Request-Id in http.request.headers with IdentityEdge
func TestEnrichAndClassify_Scenario2_TraceparentAndRequestID(t *testing.T) {
	parsedJSON := map[string]any{
		"http.request.headers": map[string]any{
			"Traceparent":  []any{"00-169fef323de3db49a3643f9a10a558aa-f34c60ee2a4c87e0-01"},
			"X-Request-Id": []any{"8a52fbd7-c4e4-9b46-a8a8-45b29ee6dce2"},
		},
	}
	rawLine := `{"msg":"incoming request","http.request.headers":{"Traceparent":["00-169fef323de3db49a3643f9a10a558aa-f34c60ee2a4c87e0-01"],"X-Request-Id":["8a52fbd7-c4e4-9b46-a8a8-45b29ee6dce2"]}}`

	ev := &contracts.NormalizedEvent{
		CaseID:  "case-sc2",
		Service: "gateway-api",
		Level:   contracts.LevelInfo,
		Message: "incoming request",
		Ts:      time.Now().UTC(),
	}

	edges, sEdge := EnrichAndClassify(ev, parsedJSON, rawLine)

	if ev.TraceID != "169fef323de3db49a3643f9a10a558aa" {
		t.Errorf("expected TraceID '169fef323de3db49a3643f9a10a558aa', got %q", ev.TraceID)
	}
	if ev.SpanID != "f34c60ee2a4c87e0" {
		t.Errorf("expected SpanID 'f34c60ee2a4c87e0', got %q", ev.SpanID)
	}
	if ev.RequestID != "8a52fbd7-c4e4-9b46-a8a8-45b29ee6dce2" {
		t.Errorf("expected RequestID '8a52fbd7-c4e4-9b46-a8a8-45b29ee6dce2', got %q", ev.RequestID)
	}
	if len(edges) < 1 {
		t.Fatalf("expected len(edges) >= 1, got %d", len(edges))
	}
	edge := edges[0]
	if edge.LeftType != "request_id" || edge.LeftVal != "8a52fbd7-c4e4-9b46-a8a8-45b29ee6dce2" {
		t.Errorf("unexpected left side in IdentityEdge: %s=%s", edge.LeftType, edge.LeftVal)
	}
	if edge.RightType != "trace_id" || edge.RightVal != "169fef323de3db49a3643f9a10a558aa" {
		t.Errorf("unexpected right side in IdentityEdge: %s=%s", edge.RightType, edge.RightVal)
	}
	if edge.Service != "gateway-api" {
		t.Errorf("expected Service 'gateway-api', got %q", edge.Service)
	}
	if sEdge != nil {
		t.Errorf("expected nil ServiceEdge, got %+v", sEdge)
	}
}

// 3. Blueprint Scenario 3: chat-com with Traceback, application frame and AttributeError
func TestEnrichAndClassify_Scenario3_TracebackOwnDefect(t *testing.T) {
	trace := []any{
		"Traceback (most recent call last):",
		"  File '/opt/pypy/lib/python3.8/site-packages/tornado/web.py', line 1704, in _execute",
		"    result = method(*self.path_args, **self.path_kwargs)",
		"  File '/var/www/supportchat/python/source/supportchat/operator/request_processing.py', line 173, in on_message",
		"    val = obj.get('nonexistent')",
		"  File '/opt/pypy/lib/python3.8/site-packages/tornado/gen.py', line 234, in wrapper",
		"    yield future",
	}
	rawLine := `{"level": "ERROR", "msg": "AttributeError: 'NoneType' object has no attribute 'get'", "trace": ["..."]}`
	parsedJSON := map[string]any{
		"level": "ERROR",
		"msg":   "AttributeError: 'NoneType' object has no attribute 'get'",
		"trace": trace,
	}

	ev := &contracts.NormalizedEvent{
		CaseID:  "case-sc3",
		Service: "chat-com",
		Level:   contracts.LevelError,
		Message: "AttributeError: 'NoneType' object has no attribute 'get'",
		Ts:      time.Now().UTC(),
	}

	EnrichAndClassify(ev, parsedJSON, rawLine)

	if ev.ErrorClass != contracts.ErrClassOwnDefect {
		t.Errorf("expected ErrorClass %q, got %q", contracts.ErrClassOwnDefect, ev.ErrorClass)
	}
	if ev.ExceptionType != "AttributeError" {
		t.Errorf("expected ExceptionType 'AttributeError', got %q", ev.ExceptionType)
	}
	if ev.SourcePlace != "request_processing.py:173" {
		t.Errorf("expected SourcePlace 'request_processing.py:173', got %q", ev.SourcePlace)
	}
}

// 4. Redactor Tests: JWT, Bearer, auth-token, multiple tokens, determinism, edge cases
func TestRedactSecrets_Various(t *testing.T) {
	jwt1 := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	jwt2 := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJlbWFpbCI6InVzZXJAZXhhbXBsZS5jb20iLCJuYW1lIjoiQWxpY2UiLCJpYXQiOjE2MDAwMDAwMDB9.abcdefghijklmnopqrstuvwxyz0123456789"

	// 1) Masking JWT
	masked1 := RedactSecrets("Bearer " + jwt1)
	if strings.Contains(masked1, jwt1) {
		t.Errorf("JWT token was not redacted")
	}
	if !strings.Contains(masked1, "<REDACTED_JWT:") {
		t.Errorf("expected <REDACTED_JWT: prefix, got: %s", masked1)
	}

	// 2) Determinism: same token -> same hash
	masked1Again := RedactSecrets("token=" + jwt1)
	hash1 := shortHash8(jwt1)
	if !strings.Contains(masked1, hash1) || !strings.Contains(masked1Again, hash1) {
		t.Errorf("hash mismatch for deterministic token: %s vs %s", masked1, masked1Again)
	}

	// 3) Different tokens -> different hashes
	masked2 := RedactSecrets("Bearer " + jwt2)
	hash2 := shortHash8(jwt2)
	if hash1 == hash2 {
		t.Errorf("different JWTs produced identical hash: %s", hash1)
	}
	if !strings.Contains(masked2, hash2) {
		t.Errorf("expected hash2 in masked2: %s", masked2)
	}

	// 4) Bearer non-JWT token (>= 20 chars)
	bearerSecret := "dXNlcjpwYXNzd29yZDEyMzQ1Njc4OTAxMjM="
	maskedBearer := RedactSecrets("Authorization: Bearer " + bearerSecret)
	if strings.Contains(maskedBearer, bearerSecret) {
		t.Errorf("bearer token was not redacted")
	}
	if !strings.Contains(maskedBearer, "Bearer <REDACTED_TOKEN:") {
		t.Errorf("expected Bearer <REDACTED_TOKEN:..., got: %s", maskedBearer)
	}

	// 5) auth-token URL param
	authTokenVal := "sec_token_val_123456789012345"
	maskedAuth := RedactSecrets("http://host/path?auth-token=" + authTokenVal)
	if strings.Contains(maskedAuth, authTokenVal) {
		t.Errorf("auth-token was not redacted")
	}
	if !strings.Contains(maskedAuth, "auth-token=<REDACTED_TOKEN:") {
		t.Errorf("expected auth-token=<REDACTED_TOKEN:..., got: %s", maskedAuth)
	}

	// 6) access_token with %20
	accTokenVal := "another_access_token_abcdefg_123456"
	maskedAcc := RedactSecrets("access_token%20" + accTokenVal)
	if strings.Contains(maskedAcc, accTokenVal) {
		t.Errorf("access_token was not redacted")
	}
	if !strings.Contains(maskedAcc, "access_token%20<REDACTED_TOKEN:") {
		t.Errorf("expected access_token%%20<REDACTED_TOKEN:..., got: %s", maskedAcc)
	}

	// 7) Empty and clean string
	if RedactSecrets("") != "" {
		t.Errorf("empty string must return empty")
	}
	clean := "GET /api/v1/health HTTP/1.1 200 OK"
	if RedactSecrets(clean) != clean {
		t.Errorf("clean string must not be modified, got: %s", RedactSecrets(clean))
	}

	// 8) Short token (< 20 chars) should not be redacted
	shortTok := "token=abc123"
	if RedactSecrets(shortTok) != shortTok {
		t.Errorf("short token (<20) should not be redacted, got: %s", RedactSecrets(shortTok))
	}
}

// 5. Test all ErrorClass classifications according to the priority rules
func TestEnrichAndClassify_AllErrorClasses(t *testing.T) {
	tests := []struct {
		name          string
		level         string
		httpStatus    uint16
		errorMsg      string
		message       string
		trace         string
		wantClass     string
		wantException string
	}{
		// Priority 1: CANCELED
		{
			name:      "Canceled_ContextCanceled",
			level:     contracts.LevelError,
			message:   `request failed: context canceled`,
			wantClass: contracts.ErrClassCanceled,
		},
		{
			name:      "Canceled_ClientDisconnected",
			level:     contracts.LevelWarn,
			message:   `stream closed: client disconnected`,
			wantClass: contracts.ErrClassCanceled,
		},
		// Priority 2: OWN_DEFECT
		{
			name:          "OwnDefect_Traceback",
			level:         contracts.LevelError,
			message:       `Traceback (most recent call last): File 'a.py', line 1`,
			wantClass:     contracts.ErrClassOwnDefect,
			wantException: "",
		},
		{
			name:          "OwnDefect_TypeError",
			level:         contracts.LevelError,
			message:       `TypeError: unsupported operand type`,
			wantClass:     contracts.ErrClassOwnDefect,
			wantException: "TypeError",
		},
		{
			name:          "OwnDefect_KeyError",
			level:         contracts.LevelError,
			message:       `KeyError: 'user_id'`,
			wantClass:     contracts.ErrClassOwnDefect,
			wantException: "KeyError",
		},
		{
			name:          "OwnDefect_ValueError",
			level:         contracts.LevelError,
			message:       `ValueError: invalid literal for int()`,
			wantClass:     contracts.ErrClassOwnDefect,
			wantException: "ValueError",
		},
		{
			name:          "OwnDefect_NullPointerException",
			level:         contracts.LevelError,
			message:       `java.lang.NullPointerException at com.app.Main`,
			wantClass:     contracts.ErrClassOwnDefect,
			wantException: "NullPointerException",
		},
		{
			name:          "OwnDefect_GoPanic",
			level:         contracts.LevelError,
			message:       `panic: runtime error: slice bounds out of range`,
			wantClass:     contracts.ErrClassOwnDefect,
			wantException: "panic",
		},
		{
			name:          "OwnDefect_RuntimeError",
			level:         contracts.LevelError,
			message:       `runtime error: invalid memory address`,
			wantClass:     contracts.ErrClassOwnDefect,
			wantException: "RuntimeError",
		},
		// Priority 3: RESOURCE
		{
			name:      "Resource_OutOfMemory",
			level:     contracts.LevelError,
			message:   `fatal: OutOfMemory occurred in worker`,
			wantClass: contracts.ErrClassResource,
		},
		{
			name:      "Resource_OOM",
			level:     contracts.LevelError,
			message:   `Process killed by OOM killer`,
			wantClass: contracts.ErrClassResource,
		},
		{
			name:      "Resource_ConnectionNotAvailable",
			level:     contracts.LevelError,
			message:   `Connection is not available, request failed`,
			wantClass: contracts.ErrClassResource,
		},
		{
			name:      "Resource_PoolExhausted",
			level:     contracts.LevelError,
			message:   `db error: pool exhausted`,
			wantClass: contracts.ErrClassResource,
		},
		{
			name:      "Resource_TooManyConnections",
			level:     contracts.LevelError,
			message:   `pg: too many connections`,
			wantClass: contracts.ErrClassResource,
		},
		{
			name:      "Resource_DiskFull",
			level:     contracts.LevelError,
			message:   `write failed: disk full`,
			wantClass: contracts.ErrClassResource,
		},
		// Priority 4: DEADLINE
		{
			name:      "Deadline_ContextDeadlineExceeded",
			level:     contracts.LevelError,
			message:   `rpc error: context deadline exceeded`,
			wantClass: contracts.ErrClassDeadline,
		},
		{
			name:      "Deadline_TimedOut",
			level:     contracts.LevelError,
			message:   `operation timed out after 30s`,
			wantClass: contracts.ErrClassDeadline,
		},
		{
			name:      "Deadline_Timeout",
			level:     contracts.LevelError,
			message:   `HTTP request timeout`,
			wantClass: contracts.ErrClassDeadline,
		},
		{
			name:      "Deadline_IOTimeout",
			level:     contracts.LevelError,
			message:   `read tcp 127.0.0.1: i/o timeout`,
			wantClass: contracts.ErrClassDeadline,
		},
		// Priority 5: UNAVAILABLE
		{
			name:      "Unavailable_ConnectionRefused",
			level:     contracts.LevelError,
			message:   `dial tcp: connection refused`,
			wantClass: contracts.ErrClassUnavailable,
		},
		{
			name:      "Unavailable_ConnectionReset",
			level:     contracts.LevelError,
			message:   `read: connection reset by peer`,
			wantClass: contracts.ErrClassUnavailable,
		},
		{
			name:      "Unavailable_NoSuchHost",
			level:     contracts.LevelError,
			message:   `lookup redis: no such host`,
			wantClass: contracts.ErrClassUnavailable,
		},
		{
			name:      "Unavailable_BrokenPipe",
			level:     contracts.LevelError,
			message:   `write: broken pipe`,
			wantClass: contracts.ErrClassUnavailable,
		},
		{
			name:      "Unavailable_UnexpectedEOF",
			level:     contracts.LevelError,
			message:   `decode failed: unexpected EOF`,
			wantClass: contracts.ErrClassUnavailable,
		},
		// Priority 6: PEER_5XX
		{
			name:       "Peer5xx_HTTPStatus500",
			level:      contracts.LevelError,
			httpStatus: 500,
			message:    `HTTP GET /api/v1/test -> 500`,
			wantClass:  contracts.ErrClassPeer5xx,
		},
		{
			name:       "Peer5xx_HTTPStatus502",
			level:      contracts.LevelError,
			httpStatus: 502,
			message:    `HTTP GET /api/v1/test -> 502`,
			wantClass:  contracts.ErrClassPeer5xx,
		},
		{
			name:      "Peer5xx_Status503String",
			level:     contracts.LevelError,
			message:   `backend returned status 503`,
			wantClass: contracts.ErrClassPeer5xx,
		},
		{
			name:      "Peer5xx_Status504String",
			level:     contracts.LevelError,
			message:   `gateway reported status 504`,
			wantClass: contracts.ErrClassPeer5xx,
		},
		// Priority 7: PEER_4XX
		{
			name:       "Peer4xx_HTTPStatus404",
			level:      contracts.LevelWarn,
			httpStatus: 404,
			message:    `not found`,
			wantClass:  contracts.ErrClassPeer4xx,
		},
		{
			name:       "Peer4xx_HTTPStatus400",
			level:      contracts.LevelWarn,
			httpStatus: 400,
			message:    `bad request`,
			wantClass:  contracts.ErrClassPeer4xx,
		},
		{
			name:      "Peer4xx_Status400String",
			level:     contracts.LevelWarn,
			message:   `call failed with status 400`,
			wantClass: contracts.ErrClassPeer4xx,
		},
		{
			name:      "Peer4xx_Status404String",
			level:     contracts.LevelWarn,
			message:   `call failed with status 404`,
			wantClass: contracts.ErrClassPeer4xx,
		},
		{
			name:      "Peer4xx_NotFoundString",
			level:     contracts.LevelWarn,
			message:   `resource not found in repository`,
			wantClass: contracts.ErrClassPeer4xx,
		},
		{
			name:      "Peer4xx_ProvidedHashEmpty",
			level:     contracts.LevelError,
			message:   `validation failed: provided hash is empty`,
			wantClass: contracts.ErrClassPeer4xx,
		},
		// Priority 8: Default OWN_DEFECT for unclassified errors
		{
			name:      "OwnDefect_DefaultGenericError",
			level:     contracts.LevelError,
			message:   `something mysterious went wrong in calculations`,
			wantClass: contracts.ErrClassOwnDefect,
		},
		{
			name:      "OwnDefect_FromNonEmptyErrorMsg",
			level:     contracts.LevelInfo,
			errorMsg:  `database query calculation failed`,
			message:   `request finished`,
			wantClass: contracts.ErrClassOwnDefect,
		},
		// NONE: Normal logs
		{
			name:       "None_NormalInfoLog",
			level:      contracts.LevelInfo,
			httpStatus: 200,
			message:    `User logged in successfully`,
			wantClass:  contracts.ErrClassNone,
		},
		{
			name:       "None_NormalDebugLog",
			level:      contracts.LevelDebug,
			httpStatus: 0,
			message:    `Cache miss, reading from memory`,
			wantClass:  contracts.ErrClassNone,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev := &contracts.NormalizedEvent{
				CaseID:     "case-test",
				Service:    "test-service",
				Level:      tc.level,
				HTTPStatus: tc.httpStatus,
				ErrorMsg:   tc.errorMsg,
				Message:    tc.message,
				Ts:         time.Now().UTC(),
			}

			var parsedJSON map[string]any
			if tc.trace != "" {
				parsedJSON = map[string]any{"trace": tc.trace}
			}

			EnrichAndClassify(ev, parsedJSON, tc.message)

			if ev.ErrorClass != tc.wantClass {
				t.Errorf("ErrorClass mismatch: got %q, want %q", ev.ErrorClass, tc.wantClass)
			}
			if tc.wantException != "" && ev.ExceptionType != tc.wantException {
				t.Errorf("ExceptionType mismatch: got %q, want %q", ev.ExceptionType, tc.wantException)
			}
		})
	}
}

// 6. Test Priority Order: Canceled > Defect > Resource > Deadline > Unavailable > 5xx > 4xx
func TestEnrichAndClassify_PriorityOrder(t *testing.T) {
	// context canceled overrides status 500 and timeout
	ev1 := &contracts.NormalizedEvent{
		CaseID:     "case-prio",
		Service:    "test-service",
		Level:      contracts.LevelError,
		HTTPStatus: 500,
		Message:    "status 500 i/o timeout: context canceled",
	}
	EnrichAndClassify(ev1, nil, ev1.Message)
	if ev1.ErrorClass != contracts.ErrClassCanceled {
		t.Errorf("expected CANCELED to win, got %q", ev1.ErrorClass)
	}

	// Traceback / AttributeError overrides timeout and status 500
	ev2 := &contracts.NormalizedEvent{
		CaseID:     "case-prio",
		Service:    "test-service",
		Level:      contracts.LevelError,
		HTTPStatus: 500,
		Message:    "timeout status 500: AttributeError: missing attr",
	}
	EnrichAndClassify(ev2, nil, ev2.Message)
	if ev2.ErrorClass != contracts.ErrClassOwnDefect {
		t.Errorf("expected OWN_DEFECT to win over timeout/500, got %q", ev2.ErrorClass)
	}

	// Resource (OOM) overrides timeout and status 500
	ev3 := &contracts.NormalizedEvent{
		CaseID:     "case-prio",
		Service:    "test-service",
		Level:      contracts.LevelError,
		HTTPStatus: 500,
		Message:    "timed out because OutOfMemory",
	}
	EnrichAndClassify(ev3, nil, ev3.Message)
	if ev3.ErrorClass != contracts.ErrClassResource {
		t.Errorf("expected RESOURCE to win over deadline/500, got %q", ev3.ErrorClass)
	}

	// Deadline overrides status 500
	ev4 := &contracts.NormalizedEvent{
		CaseID:     "case-prio",
		Service:    "test-service",
		Level:      contracts.LevelError,
		HTTPStatus: 500,
		Message:    "status 500 context deadline exceeded",
	}
	EnrichAndClassify(ev4, nil, ev4.Message)
	if ev4.ErrorClass != contracts.ErrClassDeadline {
		t.Errorf("expected DEADLINE to win over 500, got %q", ev4.ErrorClass)
	}

	// Unavailable overrides status 500
	ev5 := &contracts.NormalizedEvent{
		CaseID:     "case-prio",
		Service:    "test-service",
		Level:      contracts.LevelError,
		HTTPStatus: 500,
		Message:    "status 500 connection refused",
	}
	EnrichAndClassify(ev5, nil, ev5.Message)
	if ev5.ErrorClass != contracts.ErrClassUnavailable {
		t.Errorf("expected UNAVAILABLE to win over 500, got %q", ev5.ErrorClass)
	}

	// 5xx overrides 4xx
	ev6 := &contracts.NormalizedEvent{
		CaseID:     "case-prio",
		Service:    "test-service",
		Level:      contracts.LevelError,
		HTTPStatus: 500,
		Message:    "not found on peer, status 500 returned",
	}
	EnrichAndClassify(ev6, nil, ev6.Message)
	if ev6.ErrorClass != contracts.ErrClassPeer5xx {
		t.Errorf("expected PEER_5XX to win over 4xx, got %q", ev6.ErrorClass)
	}
}

// 7. Test IdentityEdge generation combinations
func TestEnrichAndClassify_IdentityEdges_Combinations(t *testing.T) {
	// Case 1: All 4 IDs present -> 3 edges connecting RequestID to TraceID, SessionID, VisitorID
	ev1 := &contracts.NormalizedEvent{
		CaseID:    "case-ids",
		Service:   "chat-session",
		RequestID: "req-111",
		TraceID:   "trc-222",
		SessionID: "ses-333",
		VisitorID: "vis-444",
		Ts:        time.Now().UTC(),
	}
	edges1, _ := EnrichAndClassify(ev1, nil, "")
	if len(edges1) != 3 {
		t.Fatalf("expected 3 edges for 4 IDs, got %d", len(edges1))
	}
	if edges1[0].LeftType != "request_id" || edges1[0].RightType != "trace_id" {
		t.Errorf("unexpected edge 0: %+v", edges1[0])
	}
	if edges1[1].LeftType != "request_id" || edges1[1].RightType != "session_id" {
		t.Errorf("unexpected edge 1: %+v", edges1[1])
	}
	if edges1[2].LeftType != "request_id" || edges1[2].RightType != "visitor_id" {
		t.Errorf("unexpected edge 2: %+v", edges1[2])
	}

	// Case 2: Only 2 IDs (SessionID + VisitorID) -> 1 edge
	ev2 := &contracts.NormalizedEvent{
		CaseID:    "case-ids",
		Service:   "chat-session",
		SessionID: "ses-333",
		VisitorID: "vis-444",
		Ts:        time.Now().UTC(),
	}
	edges2, _ := EnrichAndClassify(ev2, nil, "")
	if len(edges2) != 1 {
		t.Fatalf("expected 1 edge for 2 IDs, got %d", len(edges2))
	}
	if edges2[0].LeftType != "session_id" || edges2[0].RightType != "visitor_id" {
		t.Errorf("unexpected edge: %+v", edges2[0])
	}

	// Case 3: Only 1 ID -> 0 edges
	ev3 := &contracts.NormalizedEvent{
		CaseID:  "case-ids",
		Service: "chat-session",
		TraceID: "trc-222",
		Ts:      time.Now().UTC(),
	}
	edges3, _ := EnrichAndClassify(ev3, nil, "")
	if len(edges3) != 0 {
		t.Errorf("expected 0 edges for 1 ID, got %d", len(edges3))
	}

	// Case 4: 0 IDs -> 0 edges
	ev4 := &contracts.NormalizedEvent{
		CaseID:  "case-ids",
		Service: "chat-session",
		Ts:      time.Now().UTC(),
	}
	edges4, _ := EnrichAndClassify(ev4, nil, "")
	if len(edges4) != 0 {
		t.Errorf("expected 0 edges for 0 IDs, got %d", len(edges4))
	}
}

// 8. Test ID extractors from rawLine and parsedJSON
func TestEnrichAndClassify_IDExtractors(t *testing.T) {
	// OperatorID from rawLine
	ev := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev, nil, "Operator (operator_id=55754) was not found in cache")
	if ev.OperatorID != "55754" {
		t.Errorf("expected OperatorID '55754', got %q", ev.OperatorID)
	}

	// VisitorID from parsedJSON["ID"] as numeric float64
	ev2 := &contracts.NormalizedEvent{CaseID: "c1", Service: "chat-session"}
	EnrichAndClassify(ev2, map[string]any{"ID": float64(34219180)}, "")
	if ev2.VisitorID != "34219180" {
		t.Errorf("expected VisitorID '34219180', got %q", ev2.VisitorID)
	}

	// VisitorID from rawLine regex
	ev3 := &contracts.NormalizedEvent{CaseID: "c1", Service: "chat-session"}
	EnrichAndClassify(ev3, nil, "visitor_id: abc1234567890 initiated dialog")
	if ev3.VisitorID != "abc1234567890" {
		t.Errorf("expected VisitorID 'abc1234567890', got %q", ev3.VisitorID)
	}

	// SessionID from rawLine with "session_id="
	ev4 := &contracts.NormalizedEvent{CaseID: "c1", Service: "chat-session"}
	EnrichAndClassify(ev4, nil, "session_id=7eab3cab9d1bf2a4d5ab6f7c closed")
	if ev4.SessionID != "7eab3cab9d1bf2a4d5ab6f7c" {
		t.Errorf("expected SessionID '7eab3cab9d1bf2a4d5ab6f7c', got %q", ev4.SessionID)
	}

	// RequestID from parsedJSON["id"] UUID
	ev5 := &contracts.NormalizedEvent{CaseID: "c1", Service: "chat-session"}
	EnrichAndClassify(ev5, map[string]any{"id": "550e8400-e29b-41d4-a716-446655440000"}, "")
	if ev5.RequestID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("expected RequestID '550e8400-e29b-41d4-a716-446655440000', got %q", ev5.RequestID)
	}

	// TraceID from parsedJSON["trace_id"]
	ev6 := &contracts.NormalizedEvent{CaseID: "c1", Service: "chat-session"}
	EnrichAndClassify(ev6, map[string]any{"trace_id": "0123456789abcdef0123456789abcdef"}, "")
	if ev6.TraceID != "0123456789abcdef0123456789abcdef" {
		t.Errorf("expected TraceID, got %q", ev6.TraceID)
	}
}

// 9. Test PeerService and ServiceEdge handling
func TestEnrichAndClassify_PeerServiceAndEdge(t *testing.T) {
	// 1) Same service (ev.Service == peer_service) -> no ServiceEdge
	ev1 := &contracts.NormalizedEvent{
		CaseID:  "c1",
		Service: "chat-com",
		Level:   contracts.LevelError,
	}
	rawLine1 := "calling http://chat-com.chat.svc.cluster.local:8080/path: connection refused"
	_, sEdge1 := EnrichAndClassify(ev1, nil, rawLine1)
	if ev1.PeerService != "chat-com" {
		t.Errorf("expected PeerService 'chat-com', got %q", ev1.PeerService)
	}
	if sEdge1 != nil {
		t.Errorf("expected nil ServiceEdge when caller == callee, got %+v", sEdge1)
	}

	// 2) Different service, no error -> ErrorCount = 0
	ev2 := &contracts.NormalizedEvent{
		CaseID:     "c1",
		Service:    "chat-session",
		Level:      contracts.LevelInfo,
		HTTPStatus: 200,
	}
	rawLine2 := "calling http://chat-com.chat.svc.cluster.local:8080/path 200 OK"
	_, sEdge2 := EnrichAndClassify(ev2, nil, rawLine2)
	if sEdge2 == nil {
		t.Fatalf("expected non-nil ServiceEdge")
	}
	if sEdge2.ErrorCount != 0 {
		t.Errorf("expected ErrorCount 0 for successful call, got %d", sEdge2.ErrorCount)
	}

	// 3) No peer service in rawLine -> PeerService is empty and ServiceEdge is nil
	ev3 := &contracts.NormalizedEvent{
		CaseID:  "c1",
		Service: "chat-session",
		Level:   contracts.LevelInfo,
	}
	_, sEdge3 := EnrichAndClassify(ev3, nil, "just an internal log without peer")
	if ev3.PeerService != "" {
		t.Errorf("expected empty PeerService, got %q", ev3.PeerService)
	}
	if sEdge3 != nil {
		t.Errorf("expected nil ServiceEdge, got %+v", sEdge3)
	}
}

// 10. Test Traceback frame filtering: pick last application code frame, skip site-packages and /opt/pypy/
func TestEnrichAndClassify_TracebackFiltering(t *testing.T) {
	parsedJSON := map[string]any{
		"trace": `Traceback (most recent call last):
  File '/opt/pypy/lib/python3.8/site-packages/tornado/web.py', line 1704, in _execute
    result = method(*self.path_args, **self.path_kwargs)
  File '/var/www/source/app/handlers/auth.py', line 55, in post
    token = self.verify()
  File '/var/www/source/app/utils/tokens.py', line 99, in verify
    raise ValueError("invalid token")
  File '/opt/pypy/lib/python3.8/site-packages/jwt/api_jwt.py', line 62, in decode
    return payload`,
	}
	ev := &contracts.NormalizedEvent{
		CaseID:  "c1",
		Service: "chat-com",
		Level:   contracts.LevelError,
		Message: "ValueError: invalid token",
	}

	EnrichAndClassify(ev, parsedJSON, "")

	if ev.ErrorClass != contracts.ErrClassOwnDefect {
		t.Errorf("expected ErrorClass %q, got %q", contracts.ErrClassOwnDefect, ev.ErrorClass)
	}
	if ev.ExceptionType != "ValueError" {
		t.Errorf("expected ExceptionType 'ValueError', got %q", ev.ExceptionType)
	}
	// The last application frame before site-packages/jwt is tokens.py:99
	if ev.SourcePlace != "tokens.py:99" {
		t.Errorf("expected SourcePlace 'tokens.py:99', got %q", ev.SourcePlace)
	}
}

// 11. Test Edge Cases: nil parsedJSON, empty rawLine, empty ev
func TestEnrichAndClassify_EdgeCases(t *testing.T) {
	ev := &contracts.NormalizedEvent{}
	edges, sEdge := EnrichAndClassify(ev, nil, "")

	if len(edges) != 0 {
		t.Errorf("expected 0 edges on empty inputs, got %d", len(edges))
	}
	if sEdge != nil {
		t.Errorf("expected nil ServiceEdge on empty inputs")
	}
	if ev.ErrorClass != contracts.ErrClassNone {
		t.Errorf("expected ErrorClass None for empty event, got %q", ev.ErrorClass)
	}
	if ev.RawRedacted != "" {
		t.Errorf("expected empty RawRedacted, got %q", ev.RawRedacted)
	}

	// nil ev
	edgesNil, sEdgeNil := EnrichAndClassify(nil, nil, "")
	if edgesNil != nil || sEdgeNil != nil {
		t.Errorf("expected nil returns for nil ev")
	}
}

// 12. Test Additional Extractor Paths for Deep Branch Coverage
func TestEnrichAndClassify_DeepBranchCoverage(t *testing.T) {
	// 1) Traceparent from rawLine
	ev1 := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev1, nil, "traceparent: 00-abcdef1234567890abcdef1234567890-1234567890abcdef-01")
	if ev1.TraceID != "abcdef1234567890abcdef1234567890" || ev1.SpanID != "1234567890abcdef" {
		t.Errorf("failed traceparent from rawLine: trace=%s span=%s", ev1.TraceID, ev1.SpanID)
	}

	// 2) Traceparent from headers map with string value and []string
	ev2 := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev2, map[string]any{
		"headers": map[string]any{
			"Traceparent": "00-11112222333344445555666677778888-99990000aaaabbbb-01",
		},
	}, "")
	if ev2.TraceID != "11112222333344445555666677778888" {
		t.Errorf("expected TraceID from headers string, got %s", ev2.TraceID)
	}

	ev2b := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev2b, map[string]any{
		"request_headers": map[string]any{
			"Traceparent": []string{"00-22223333444455556666777788889999-0000aaaabbbbcccc-01"},
		},
	}, "")
	if ev2b.TraceID != "22223333444455556666777788889999" {
		t.Errorf("expected TraceID from request_headers []string, got %s", ev2b.TraceID)
	}

	// 3) RequestID from 32-char hex UUID, request_id in JSON, X-Request-Id as string/[]string, and rawLine
	ev3 := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev3, map[string]any{"id": "550e8400e29b41d4a716446655440000"}, "")
	if ev3.RequestID != "550e8400e29b41d4a716446655440000" {
		t.Errorf("expected hex UUID RequestID, got %s", ev3.RequestID)
	}

	ev3b := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev3b, map[string]any{"request_id": "custom-req-id-123"}, "")
	if ev3b.RequestID != "custom-req-id-123" {
		t.Errorf("expected RequestID from parsedJSON[request_id], got %s", ev3b.RequestID)
	}

	ev3c := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev3c, map[string]any{
		"headers": map[string]any{"X-Request-Id": "header-req-string-456"},
	}, "")
	if ev3c.RequestID != "header-req-string-456" {
		t.Errorf("expected RequestID from header string, got %s", ev3c.RequestID)
	}

	ev3d := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev3d, map[string]any{
		"headers": map[string]any{"X-Request-Id": []string{"header-req-slice-789"}},
	}, "")
	if ev3d.RequestID != "header-req-slice-789" {
		t.Errorf("expected RequestID from header slice, got %s", ev3d.RequestID)
	}

	ev3e := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev3e, nil, "x-request-id: 12345678-1234-1234-1234-123456789abc")
	if ev3e.RequestID != "12345678-1234-1234-1234-123456789abc" {
		t.Errorf("expected RequestID from rawLine regex, got %s", ev3e.RequestID)
	}

	// 4) SessionID from parsedJSON keys
	ev4 := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev4, map[string]any{"session_id": "ses-from-json-123"}, "")
	if ev4.SessionID != "ses-from-json-123" {
		t.Errorf("expected SessionID from JSON, got %s", ev4.SessionID)
	}

	ev4b := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev4b, map[string]any{"visit_session_id": "ses-from-visit-123"}, "")
	if ev4b.SessionID != "ses-from-visit-123" {
		t.Errorf("expected SessionID from visit_session_id, got %s", ev4b.SessionID)
	}

	// 5) OperatorID from parsedJSON
	ev5 := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev5, map[string]any{"operator_id": "999888"}, "")
	if ev5.OperatorID != "999888" {
		t.Errorf("expected OperatorID from JSON, got %s", ev5.OperatorID)
	}

	// 6) VisitorID from parsedJSON with int, int64, string, and visitor_id/provided_visitor_id
	ev6a := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev6a, map[string]any{"ID": int(12345)}, "")
	if ev6a.VisitorID != "12345" {
		t.Errorf("expected VisitorID '12345', got %s", ev6a.VisitorID)
	}

	ev6b := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev6b, map[string]any{"ID": int64(67890)}, "")
	if ev6b.VisitorID != "67890" {
		t.Errorf("expected VisitorID '67890', got %s", ev6b.VisitorID)
	}

	ev6c := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev6c, map[string]any{"ID": "vis-str-111"}, "")
	if ev6c.VisitorID != "vis-str-111" {
		t.Errorf("expected VisitorID 'vis-str-111', got %s", ev6c.VisitorID)
	}

	ev6d := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev6d, map[string]any{"visitor_id": "vis-from-key-222"}, "")
	if ev6d.VisitorID != "vis-from-key-222" {
		t.Errorf("expected VisitorID 'vis-from-key-222', got %s", ev6d.VisitorID)
	}

	ev6e := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev6e, map[string]any{"provided_visitor_id": "vis-from-provided-333"}, "")
	if ev6e.VisitorID != "vis-from-provided-333" {
		t.Errorf("expected VisitorID 'vis-from-provided-333', got %s", ev6e.VisitorID)
	}

	// 7) PeerService from parsedJSON keys
	ev7a := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev7a, map[string]any{"peer_service": "auth-service.auth.svc.cluster.local"}, "")
	if ev7a.PeerService != "auth-service" {
		t.Errorf("expected PeerService 'auth-service', got %s", ev7a.PeerService)
	}

	ev7b := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1"}
	EnrichAndClassify(ev7b, map[string]any{"http.request.url": "http://billing-svc.billing.svc.cluster.local:9090/charge"}, "")
	if ev7b.PeerService != "billing-svc" {
		t.Errorf("expected PeerService 'billing-svc', got %s", ev7b.PeerService)
	}

	// 8) Trace string from stack and stacktrace, and []string
	ev8a := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1", Level: contracts.LevelError}
	EnrichAndClassify(ev8a, map[string]any{"stack": []string{"Traceback (most recent call last):", "  File '/app/worker.py', line 12, in run", "KeyError: 'foo'"}}, "")
	if ev8a.SourcePlace != "worker.py:12" || ev8a.ExceptionType != "KeyError" {
		t.Errorf("expected worker.py:12 and KeyError, got %s / %s", ev8a.SourcePlace, ev8a.ExceptionType)
	}

	ev8b := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1", Level: contracts.LevelError}
	EnrichAndClassify(ev8b, map[string]any{"stacktrace": "Traceback (most recent call last):\n  File '/app/server.py', line 77, in start\nIndexError: list index"}, "")
	if ev8b.SourcePlace != "server.py:77" || ev8b.ExceptionType != "IndexError" {
		t.Errorf("expected server.py:77 and IndexError, got %s / %s", ev8b.SourcePlace, ev8b.ExceptionType)
	}

	// 9) Trace with only library frames -> SourcePlace remains untouched
	ev9 := &contracts.NormalizedEvent{CaseID: "c1", Service: "s1", Level: contracts.LevelError, SourcePlace: "existing:10"}
	EnrichAndClassify(ev9, map[string]any{"trace": "  File '/opt/pypy/lib/python3.8/site-packages/tornado/web.py', line 100, in run"}, "")
	if ev9.SourcePlace != "existing:10" {
		t.Errorf("expected existing:10 preserved when no app frames, got %s", ev9.SourcePlace)
	}
}
