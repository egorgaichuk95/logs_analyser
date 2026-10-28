package classifier

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/local/logs-analyzer/pkg/contracts"
)

var (
	traceparentRe   = regexp.MustCompile(`00-([0-9a-fA-F]{32})-([0-9a-fA-F]{16})-[0-9a-fA-F]{2}`)
	uuidHyphenRe    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	uuidHexRe       = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
	sessionIDRe     = regexp.MustCompile(`(?i)(?:visit-session-id|visit_session_id|session_id|session id|request session)[=:\s]+([0-9a-fA-F]{24,36})`)
	operatorIDRe    = regexp.MustCompile(`(?i)operator_id[=:\s]+(\d+)`)
	visitorIDRe     = regexp.MustCompile(`(?i)(?:visitor_id|provided_visitor_id)[=:\s]+([0-9a-fA-F]{8,36}|\d+)`)
	peerServiceRe   = regexp.MustCompile(`([a-z0-9-]+)\.[a-z0-9-]+\.svc\.cluster\.local`)
	exceptionTypeRe = regexp.MustCompile(`([A-Za-z]+Error|[A-Za-z]+Exception|panic)`)
	traceFrameRe    = regexp.MustCompile(`File\s+['"]([^'"]+)['"],\s+line\s+(\d+)`)
	oomRe           = regexp.MustCompile(`(?i)\boom\b`)
)

type idPair struct {
	typ string
	val string
}

// EnrichAndClassify заполняет в ev поля ErrorClass, ExceptionType, SourcePlace (из traceback),
// TraceID, SpanID, RequestID, SessionID, OperatorID, VisitorID, PeerService, RawHash, RawRedacted
// и возвращает найденные рёбра IdentityEdge и ServiceEdge.
func EnrichAndClassify(
	ev *contracts.NormalizedEvent,
	parsedJSON map[string]any,
	rawLine string,
) ([]contracts.IdentityEdge, *contracts.ServiceEdge) {
	if ev == nil {
		return nil, nil
	}

	// 1. RawRedacted and RawHash
	if rawLine != "" {
		ev.RawRedacted = RedactSecrets(rawLine)
		h := fnv.New64a()
		_, _ = h.Write([]byte(ev.RawRedacted))
		ev.RawHash = h.Sum64()
	}

	// 2. Extract TraceID & SpanID
	extractTraceAndSpan(ev, parsedJSON, rawLine)

	// 3. Extract RequestID
	extractRequestID(ev, parsedJSON, rawLine)

	// 4. Extract SessionID
	extractSessionID(ev, parsedJSON, rawLine)

	// 5. Extract OperatorID
	extractOperatorID(ev, parsedJSON, rawLine)

	// 6. Extract VisitorID
	extractVisitorID(ev, parsedJSON, rawLine)

	// 7. Extract PeerService
	extractPeerService(ev, parsedJSON, rawLine)

	// 8. Error classification & exception/traceback extraction
	classifyErrors(ev, parsedJSON, rawLine)

	// 9. Generate ServiceEdge
	var serviceEdge *contracts.ServiceEdge
	if ev.PeerService != "" && ev.PeerService != ev.Service {
		var isErr uint64
		if ev.Level == contracts.LevelError ||
			ev.HTTPStatus >= 400 ||
			ev.ErrorMsg != "" ||
			(ev.ErrorClass != "" && ev.ErrorClass != contracts.ErrClassNone) {
			isErr = 1
		}
		serviceEdge = &contracts.ServiceEdge{
			CaseID:         ev.CaseID,
			CallerService:  ev.Service,
			CalleeService:  ev.PeerService,
			EvidenceSource: "peer_url",
			CallCount:      1,
			ErrorCount:     isErr,
		}
	}

	// 10. Generate IdentityEdge slice
	identityEdges := buildIdentityEdges(ev)

	return identityEdges, serviceEdge
}

func extractTraceAndSpan(ev *contracts.NormalizedEvent, parsedJSON map[string]any, rawLine string) {
	if ev.TraceID != "" && ev.SpanID != "" {
		return
	}

	if parsedJSON != nil {
		if ev.TraceID == "" {
			if v, ok := parsedJSON["trace_id"]; ok && v != nil && fmt.Sprint(v) != "" {
				ev.TraceID = fmt.Sprint(v)
			}
		}
		if ev.SpanID == "" {
			if v, ok := parsedJSON["span_id"]; ok && v != nil && fmt.Sprint(v) != "" {
				ev.SpanID = fmt.Sprint(v)
			}
		}

		if ev.TraceID == "" || ev.SpanID == "" {
			for _, hKey := range []string{"http.request.headers", "headers", "request_headers"} {
				if hMap, ok := parsedJSON[hKey].(map[string]any); ok && hMap != nil {
					for k, v := range hMap {
						if strings.EqualFold(k, "Traceparent") {
							var headerVal string
							switch val := v.(type) {
							case []any:
								if len(val) > 0 && val[0] != nil {
									headerVal = fmt.Sprint(val[0])
								}
							case []string:
								if len(val) > 0 {
									headerVal = val[0]
								}
							case string:
								headerVal = val
							}
							if matches := traceparentRe.FindStringSubmatch(headerVal); len(matches) == 3 {
								if ev.TraceID == "" {
									ev.TraceID = matches[1]
								}
								if ev.SpanID == "" {
									ev.SpanID = matches[2]
								}
							}
							break
						}
					}
				}
			}
		}
	}

	if (ev.TraceID == "" || ev.SpanID == "") && rawLine != "" {
		if matches := traceparentRe.FindStringSubmatch(rawLine); len(matches) == 3 {
			if ev.TraceID == "" {
				ev.TraceID = matches[1]
			}
			if ev.SpanID == "" {
				ev.SpanID = matches[2]
			}
		}
	}
}

func extractRequestID(ev *contracts.NormalizedEvent, parsedJSON map[string]any, rawLine string) {
	if ev.RequestID != "" {
		return
	}

	if parsedJSON != nil {
		if v, ok := parsedJSON["id"]; ok && v != nil {
			s := fmt.Sprint(v)
			if uuidHyphenRe.MatchString(s) || uuidHexRe.MatchString(s) {
				ev.RequestID = s
				return
			}
		}

		if v, ok := parsedJSON["request_id"]; ok && v != nil && fmt.Sprint(v) != "" {
			ev.RequestID = fmt.Sprint(v)
			return
		}

		for _, hKey := range []string{"http.request.headers", "headers", "request_headers"} {
			if hMap, ok := parsedJSON[hKey].(map[string]any); ok && hMap != nil {
				for k, v := range hMap {
					if strings.EqualFold(k, "X-Request-Id") || strings.EqualFold(k, "X-Request-ID") {
						var headerVal string
						switch val := v.(type) {
						case []any:
							if len(val) > 0 && val[0] != nil {
								headerVal = fmt.Sprint(val[0])
							}
						case []string:
							if len(val) > 0 {
								headerVal = val[0]
							}
						case string:
							headerVal = val
						}
						if headerVal != "" {
							ev.RequestID = headerVal
							return
						}
					}
				}
			}
		}
	}

	if rawLine != "" {
		reqIDRe := regexp.MustCompile(`(?i)(?:x-request-id|request_id|request-id)[=:\s]+([0-9a-fA-F-]{32,36})`)
		if m := reqIDRe.FindStringSubmatch(rawLine); len(m) > 1 {
			ev.RequestID = m[1]
		}
	}
}

func extractSessionID(ev *contracts.NormalizedEvent, parsedJSON map[string]any, rawLine string) {
	if ev.SessionID != "" {
		return
	}

	if rawLine != "" {
		if m := sessionIDRe.FindStringSubmatch(rawLine); len(m) > 1 {
			ev.SessionID = m[1]
			return
		}
	}

	if parsedJSON != nil {
		for _, key := range []string{"session_id", "visit_session_id", "visit-session-id"} {
			if v, ok := parsedJSON[key]; ok && v != nil && fmt.Sprint(v) != "" {
				ev.SessionID = fmt.Sprint(v)
				return
			}
		}
	}
}

func extractOperatorID(ev *contracts.NormalizedEvent, parsedJSON map[string]any, rawLine string) {
	if ev.OperatorID != "" {
		return
	}

	if rawLine != "" {
		if m := operatorIDRe.FindStringSubmatch(rawLine); len(m) > 1 {
			ev.OperatorID = m[1]
			return
		}
	}

	if parsedJSON != nil {
		if v, ok := parsedJSON["operator_id"]; ok && v != nil && fmt.Sprint(v) != "" {
			ev.OperatorID = fmt.Sprint(v)
		}
	}
}

func extractVisitorID(ev *contracts.NormalizedEvent, parsedJSON map[string]any, rawLine string) {
	if ev.VisitorID != "" {
		return
	}

	if parsedJSON != nil {
		if v, ok := parsedJSON["ID"]; ok && v != nil {
			switch val := v.(type) {
			case float64:
				ev.VisitorID = strconv.FormatInt(int64(val), 10)
			case int:
				ev.VisitorID = strconv.Itoa(val)
			case int64:
				ev.VisitorID = strconv.FormatInt(val, 10)
			case string:
				ev.VisitorID = val
			default:
				s := fmt.Sprint(v)
				if s != "" && s != "<nil>" {
					ev.VisitorID = s
				}
			}
			if ev.VisitorID != "" {
				return
			}
		}

		for _, key := range []string{"visitor_id", "provided_visitor_id"} {
			if v, ok := parsedJSON[key]; ok && v != nil && fmt.Sprint(v) != "" {
				ev.VisitorID = fmt.Sprint(v)
				return
			}
		}
	}

	if rawLine != "" {
		if m := visitorIDRe.FindStringSubmatch(rawLine); len(m) > 1 {
			ev.VisitorID = m[1]
		}
	}
}

func extractPeerService(ev *contracts.NormalizedEvent, parsedJSON map[string]any, rawLine string) {
	if ev.PeerService != "" {
		return
	}

	if rawLine != "" {
		if m := peerServiceRe.FindStringSubmatch(rawLine); len(m) > 1 {
			ev.PeerService = m[1]
			return
		}
	}

	if parsedJSON != nil {
		for _, key := range []string{"peer_service", "peer.service", "http.request.url", "uri"} {
			if v, ok := parsedJSON[key]; ok && v != nil {
				s := fmt.Sprint(v)
				if m := peerServiceRe.FindStringSubmatch(s); len(m) > 1 {
					ev.PeerService = m[1]
					return
				}
			}
		}
	}
}

func getTraceStr(parsedJSON map[string]any, rawLine string) string {
	if parsedJSON == nil {
		return ""
	}
	for _, key := range []string{"trace", "stack", "stacktrace"} {
		if tr, ok := parsedJSON[key]; ok && tr != nil {
			switch v := tr.(type) {
			case string:
				return v
			case []string:
				return strings.Join(v, "\n")
			case []any:
				var lines []string
				for _, item := range v {
					if item != nil {
						lines = append(lines, fmt.Sprint(item))
					}
				}
				return strings.Join(lines, "\n")
			default:
				return fmt.Sprint(v)
			}
		}
	}
	return ""
}

func extractSourcePlaceFromTrace(ev *contracts.NormalizedEvent, traceStr string) {
	if traceStr == "" {
		return
	}
	matches := traceFrameRe.FindAllStringSubmatch(traceStr, -1)
	var appFile, appLine string
	for _, m := range matches {
		if len(m) >= 3 {
			file := m[1]
			line := m[2]
			if !strings.Contains(file, "site-packages") &&
				!strings.Contains(file, "/opt/pypy/") &&
				!strings.Contains(file, `\opt\pypy\`) {
				appFile = file
				appLine = line
			}
		}
	}
	if appFile != "" && appLine != "" {
		base := filepath.Base(filepath.FromSlash(appFile))
		ev.SourcePlace = base + ":" + appLine
	}
}

func classifyErrors(ev *contracts.NormalizedEvent, parsedJSON map[string]any, rawLine string) {
	traceStr := getTraceStr(parsedJSON, rawLine)
	combined := ev.Message + " " + ev.ErrorMsg + " " + traceStr
	combinedLower := strings.ToLower(combined)

	isCriticalPattern := strings.Contains(combinedLower, "context canceled") ||
		strings.Contains(combinedLower, "client disconnected") ||
		strings.Contains(combinedLower, "traceback (most recent call last)") ||
		strings.Contains(combinedLower, "panic:") ||
		strings.Contains(combinedLower, "runtime error:") ||
		strings.Contains(combined, "AttributeError") ||
		strings.Contains(combined, "TypeError") ||
		strings.Contains(combined, "KeyError") ||
		strings.Contains(combined, "ValueError") ||
		strings.Contains(combined, "NullPointerException")

	if ev.Level != contracts.LevelError &&
		ev.Level != contracts.LevelWarn &&
		ev.HTTPStatus < 400 &&
		ev.ErrorMsg == "" &&
		!isCriticalPattern {
		ev.ErrorClass = contracts.ErrClassNone
		return
	}

	// 1. CANCELED
	if strings.Contains(combinedLower, "context canceled") ||
		strings.Contains(combinedLower, "context cancelled") ||
		strings.Contains(combinedLower, "client disconnected") {
		ev.ErrorClass = contracts.ErrClassCanceled
		return
	}

	// 2. OWN_DEFECT
	hasDefect := strings.Contains(combinedLower, "traceback (most recent call last)") ||
		strings.Contains(combined, "AttributeError") ||
		strings.Contains(combined, "TypeError") ||
		strings.Contains(combined, "KeyError") ||
		strings.Contains(combined, "ValueError") ||
		strings.Contains(combined, "NullPointerException") ||
		strings.Contains(combinedLower, "panic:") ||
		strings.Contains(combinedLower, "runtime error:")

	if hasDefect {
		ev.ErrorClass = contracts.ErrClassOwnDefect
		if m := exceptionTypeRe.FindStringSubmatch(combined); len(m) > 1 {
			ev.ExceptionType = m[1]
		} else if strings.Contains(combinedLower, "runtime error") {
			ev.ExceptionType = "RuntimeError"
		}
		extractSourcePlaceFromTrace(ev, traceStr)
		return
	}

	// 3. RESOURCE
	hasResource := strings.Contains(combinedLower, "outofmemory") ||
		oomRe.MatchString(combined) ||
		strings.Contains(combinedLower, "connection is not available") ||
		strings.Contains(combinedLower, "pool exhausted") ||
		strings.Contains(combinedLower, "too many connections") ||
		strings.Contains(combinedLower, "disk full")

	if hasResource {
		ev.ErrorClass = contracts.ErrClassResource
		return
	}

	// 4. DEADLINE
	hasDeadline := strings.Contains(combinedLower, "context deadline exceeded") ||
		strings.Contains(combinedLower, "timed out") ||
		strings.Contains(combinedLower, "timeout") ||
		strings.Contains(combinedLower, "i/o timeout")

	if hasDeadline {
		ev.ErrorClass = contracts.ErrClassDeadline
		return
	}

	// 5. UNAVAILABLE
	hasUnavailable := strings.Contains(combinedLower, "connection refused") ||
		strings.Contains(combinedLower, "connection reset") ||
		strings.Contains(combinedLower, "no such host") ||
		strings.Contains(combinedLower, "broken pipe") ||
		strings.Contains(combinedLower, "unexpected eof")

	if hasUnavailable {
		ev.ErrorClass = contracts.ErrClassUnavailable
		return
	}

	// 6. PEER_5XX
	hasPeer5xx := strings.Contains(combinedLower, "status 500") ||
		strings.Contains(combinedLower, "status 502") ||
		strings.Contains(combinedLower, "status 503") ||
		strings.Contains(combinedLower, "status 504") ||
		ev.HTTPStatus >= 500

	if hasPeer5xx {
		ev.ErrorClass = contracts.ErrClassPeer5xx
		return
	}

	// 7. PEER_4XX
	hasPeer4xx := strings.Contains(combinedLower, "status 400") ||
		strings.Contains(combinedLower, "status 404") ||
		strings.Contains(combinedLower, "not found") ||
		strings.Contains(combinedLower, "provided hash is empty") ||
		ev.HTTPStatus >= 400

	if hasPeer4xx {
		ev.ErrorClass = contracts.ErrClassPeer4xx
		return
	}

	// 8. Default
	ev.ErrorClass = contracts.ErrClassOwnDefect
}

func buildIdentityEdges(ev *contracts.NormalizedEvent) []contracts.IdentityEdge {
	var candidatePairs []idPair
	if ev.RequestID != "" {
		candidatePairs = append(candidatePairs, idPair{"request_id", ev.RequestID})
	}
	if ev.TraceID != "" {
		candidatePairs = append(candidatePairs, idPair{"trace_id", ev.TraceID})
	}
	if ev.SessionID != "" {
		candidatePairs = append(candidatePairs, idPair{"session_id", ev.SessionID})
	}
	if ev.VisitorID != "" {
		candidatePairs = append(candidatePairs, idPair{"visitor_id", ev.VisitorID})
	}

	if len(candidatePairs) < 2 {
		return nil
	}

	left := candidatePairs[0]
	edges := make([]contracts.IdentityEdge, 0, len(candidatePairs)-1)
	for _, right := range candidatePairs[1:] {
		edges = append(edges, contracts.IdentityEdge{
			CaseID:    ev.CaseID,
			Ts:        ev.Ts,
			Service:   ev.Service,
			LeftType:  left.typ,
			LeftVal:   left.val,
			RightType: right.typ,
			RightVal:  right.val,
		})
	}
	return edges
}
