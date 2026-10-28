package normalizer

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

var (
	uuidHyphenRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	uuidHexRe    = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
	numberRe     = regexp.MustCompile(`^[0-9]+$`)

	timeLayouts = []string{
		time.RFC3339Nano,
		time.RFC3339,
		"060102 15:04:05 -0700",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02T15:04:05",
	}
)

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func parseLokiTs(rec contracts.RawScriptRecord) time.Time {
	if rec.Timestamp != "" {
		if ns, err := strconv.ParseInt(rec.Timestamp, 10, 64); err == nil && ns > 0 {
			return time.Unix(0, ns).UTC()
		}
	}
	if rec.Date != "" {
		if t, err := time.Parse(time.RFC3339Nano, rec.Date); err == nil {
			return t.UTC()
		}
		if t, err := time.Parse(time.RFC3339, rec.Date); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

func cleanJSONMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	res := make(map[string]any, len(m)*2)
	for k, v := range m {
		if nested, ok := v.(map[string]any); ok {
			v = cleanJSONMap(nested)
		}
		res[k] = v

		trimmed := strings.Trim(k, " :\t\r\n")
		words := strings.Fields(trimmed)
		cleanedKey := strings.Join(words, "_")
		if cleanedKey != "" && cleanedKey != k {
			res[cleanedKey] = v
		}
	}
	return res
}

func normalizeRoute(uri string) string {
	if uri == "" {
		return ""
	}
	path := uri
	if strings.Contains(uri, "://") {
		if u, err := url.Parse(uri); err == nil && u.Path != "" {
			path = u.Path
		}
	}
	if idx := strings.IndexByte(path, '?'); idx != -1 {
		path = path[:idx]
	}
	if idx := strings.IndexByte(path, '#'); idx != -1 {
		path = path[:idx]
	}

	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "" {
			continue
		}
		if numberRe.MatchString(part) || uuidHyphenRe.MatchString(part) || uuidHexRe.MatchString(part) {
			parts[i] = "<id>"
		}
	}
	return strings.Join(parts, "/")
}

func parseStatus(v any) uint16 {
	switch val := v.(type) {
	case int:
		return uint16(val)
	case int64:
		return uint16(val)
	case float64:
		return uint16(val)
	case string:
		if n, err := strconv.ParseUint(val, 10, 16); err == nil {
			return uint16(n)
		}
	}
	return 0
}

func parseLatency(v any) uint64 {
	switch val := v.(type) {
	case int:
		return uint64(val)
	case int64:
		return uint64(val)
	case float64:
		return uint64(val)
	case string:
		if n, err := strconv.ParseUint(val, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

func parseAppTs(v any) *time.Time {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return nil
	}
	s = strings.TrimSpace(s)
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			tUTC := t.UTC()
			return &tUTC
		}
	}
	return nil
}

func determineEchoLevel(status uint16, errMsg string) string {
	if status >= 500 {
		return contracts.LevelError
	}
	if status >= 400 {
		return contracts.LevelWarn
	}
	if errMsg != "" {
		return contracts.LevelError
	}
	return contracts.LevelInfo
}

func normalizeLevelStr(s string) string {
	lower := strings.ToLower(strings.TrimSpace(s))
	switch lower {
	case "error", "err", "fatal", "critical":
		return contracts.LevelError
	case "warn", "warning":
		return contracts.LevelWarn
	case "info":
		return contracts.LevelInfo
	case "debug", "trace":
		return contracts.LevelDebug
	default:
		return ""
	}
}

func determineLevel(schema string, obj map[string]any, status uint16, errMsg string, detectedLevel string) string {
	if schema == contracts.SchemaEchoAccess {
		return determineEchoLevel(status, errMsg)
	}

	if obj != nil {
		for _, key := range []string{"level", "severity", "lvl"} {
			if v, ok := obj[key]; ok && v != nil {
				if lvl := normalizeLevelStr(fmt.Sprint(v)); lvl != "" {
					return lvl
				}
			}
		}
	}

	if detectedLevel != "" {
		if lvl := normalizeLevelStr(detectedLevel); lvl != "" {
			return lvl
		}
	}

	return contracts.LevelInfo
}

// NormalizeRecord парсит сырую запись и возвращает нормализованное событие и внутреннюю JSON-карту (или nil для plain text).
func NormalizeRecord(caseID string, rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) (contracts.NormalizedEvent, map[string]any) {
	ev := contracts.NormalizedEvent{
		CaseID:          caseID,
		EventID:         newUUID(),
		Ts:              parseLokiTs(rec),
		SourceFile:      sourceFile,
		SourceRecordIdx: recordIdx,
	}

	var detectedLevel string
	if rec.Fields != nil {
		if s := rec.Fields["service_name"]; s != "" {
			ev.Service = s
		} else if s := rec.Fields["container"]; s != "" {
			ev.Service = s
		} else if s := rec.Fields["app"]; s != "" {
			ev.Service = s
		} else {
			ev.Service = "unknown"
		}

		if e := rec.Fields["deployment_environment"]; e != "" {
			ev.Environment = e
		} else if e := rec.Fields["environment"]; e != "" {
			ev.Environment = e
		}

		ev.Pod = rec.Fields["pod"]
		ev.Node = rec.Fields["node"]
		ev.ServiceVersion = rec.Fields["service_version"]
		detectedLevel = rec.Fields["detected_level"]
	} else {
		ev.Service = "unknown"
	}

	var parsedJSON map[string]any
	trimmedLine := strings.TrimSpace(rec.Line)
	if strings.HasPrefix(trimmedLine, "{") && strings.HasSuffix(trimmedLine, "}") {
		var rawMap map[string]any
		if err := json.Unmarshal([]byte(trimmedLine), &rawMap); err == nil && rawMap != nil {
			parsedJSON = cleanJSONMap(rawMap)
		}
	}

	if parsedJSON != nil {
		if src, ok := parsedJSON["source"].(map[string]any); ok {
			file := ""
			line := ""
			if f, exists := src["file"]; exists && f != nil {
				file = fmt.Sprint(f)
			}
			if l, exists := src["line"]; exists && l != nil {
				line = fmt.Sprint(l)
			}
			if file != "" || line != "" {
				ev.SchemaKind = contracts.SchemaGoSlog
				if file != "" && line != "" {
					ev.SourcePlace = filepath.Base(file) + ":" + line
				} else if file != "" {
					ev.SourcePlace = filepath.Base(file)
				} else {
					ev.SourcePlace = line
				}
			}
		}

		if ev.SchemaKind == "" && fmt.Sprint(parsedJSON["type"]) == "app" && parsedJSON["line"] != nil && fmt.Sprint(parsedJSON["line"]) != "" {
			ev.SchemaKind = contracts.SchemaPyTornado
			ev.SourcePlace = fmt.Sprint(parsedJSON["line"])
		}

		if ev.SchemaKind == "" && parsedJSON["method"] != nil && parsedJSON["uri"] != nil && parsedJSON["status"] != nil && parsedJSON["latency"] != nil {
			ev.SchemaKind = contracts.SchemaEchoAccess
			ev.HTTPMethod = fmt.Sprint(parsedJSON["method"])
			ev.HTTPRoute = normalizeRoute(fmt.Sprint(parsedJSON["uri"]))
			ev.HTTPStatus = parseStatus(parsedJSON["status"])
			ev.LatencyNs = parseLatency(parsedJSON["latency"])
		}

		if ev.SchemaKind == "" && (parsedJSON["http.request.method"] != nil || parsedJSON["http.request.url"] != nil) {
			ev.SchemaKind = contracts.SchemaOTelHTTP
			if m := parsedJSON["http.request.method"]; m != nil {
				ev.HTTPMethod = fmt.Sprint(m)
			} else if m := parsedJSON["http.method"]; m != nil {
				ev.HTTPMethod = fmt.Sprint(m)
			}

			if r := parsedJSON["http.route"]; r != nil {
				ev.HTTPRoute = normalizeRoute(fmt.Sprint(r))
			} else if u := parsedJSON["http.request.url"]; u != nil {
				ev.HTTPRoute = normalizeRoute(fmt.Sprint(u))
			}

			if d := parsedJSON["http.duration"]; d != nil {
				ev.LatencyNs = parseLatency(d)
			}
			if st := parsedJSON["http.response.status_code"]; st != nil {
				ev.HTTPStatus = parseStatus(st)
			} else if st := parsedJSON["http.status_code"]; st != nil {
				ev.HTTPStatus = parseStatus(st)
			}
		}

		if ev.SchemaKind == "" {
			ev.SchemaKind = contracts.SchemaGenericJSON
		}

		if errVal, ok := parsedJSON["error"]; ok && errVal != nil {
			if s, ok := errVal.(string); ok {
				ev.ErrorMsg = s
			} else {
				ev.ErrorMsg = fmt.Sprint(errVal)
			}
		}

		if ev.SchemaKind == contracts.SchemaEchoAccess {
			ev.Message = fmt.Sprintf("HTTP %s %s -> %d", ev.HTTPMethod, ev.HTTPRoute, ev.HTTPStatus)
		} else if msg, ok := parsedJSON["msg"]; ok && msg != nil && fmt.Sprint(msg) != "" {
			ev.Message = fmt.Sprint(msg)
		} else if msg, ok := parsedJSON["message"]; ok && msg != nil && fmt.Sprint(msg) != "" {
			ev.Message = fmt.Sprint(msg)
		} else {
			ev.Message = rec.Line
		}

		if tVal, ok := parsedJSON["time"]; ok && tVal != nil {
			if appTs := parseAppTs(tVal); appTs != nil {
				ev.AppTs = appTs
				ev.ClockSkewMs = (appTs.UnixNano() - ev.Ts.UnixNano()) / 1_000_000
			}
		}
	} else {
		ev.SchemaKind = contracts.SchemaPlainText
		ev.Message = rec.Line
	}

	ev.Level = determineLevel(ev.SchemaKind, parsedJSON, ev.HTTPStatus, ev.ErrorMsg, detectedLevel)
	ev.SeverityNum = contracts.SeverityFromLevel(ev.Level)

	return ev, parsedJSON
}
