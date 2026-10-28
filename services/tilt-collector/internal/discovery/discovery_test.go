package discovery

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

func TestNormalizeTarget_TiltLabels(t *testing.T) {
	// 1. Tilt label: dev.tilt.resource
	labels := map[string]string{
		"dev.tilt.resource":         "chat-session",
		"io.kubernetes.pod.name":    "chat-session-7f9c",
		"io.kubernetes.pod.namespace": "chat",
	}
	target, ok := NormalizeTarget("c123", "/chat-session-7f9c", "running", labels)
	if !ok {
		t.Fatalf("expected NormalizeTarget to return ok=true, got false")
	}
	if target.ServiceName != "chat-session" {
		t.Errorf("expected ServiceName 'chat-session', got '%s'", target.ServiceName)
	}
	if target.PodName != "chat-session-7f9c" {
		t.Errorf("expected PodName 'chat-session-7f9c', got '%s'", target.PodName)
	}
	if target.Namespace != "chat" {
		t.Errorf("expected Namespace 'chat', got '%s'", target.Namespace)
	}
	if target.Runtime != contracts.RuntimeK8s {
		t.Errorf("expected Runtime '%s', got '%s'", contracts.RuntimeK8s, target.Runtime)
	}
	if target.ID != "c123" {
		t.Errorf("expected ID 'c123', got '%s'", target.ID)
	}

	// 2. Tilt label: tilt.dev/resource (Docker standalone)
	labels2 := map[string]string{
		"tilt.dev/resource": "payment-api",
	}
	target2, ok2 := NormalizeTarget("c456", "/payment-api", "running", labels2)
	if !ok2 {
		t.Fatalf("expected ok=true, got false")
	}
	if target2.ServiceName != "payment-api" {
		t.Errorf("expected ServiceName 'payment-api', got '%s'", target2.ServiceName)
	}
	if target2.PodName != "payment-api" {
		t.Errorf("expected PodName 'payment-api', got '%s'", target2.PodName)
	}
	if target2.Namespace != "tilt-local" {
		t.Errorf("expected default Namespace 'tilt-local', got '%s'", target2.Namespace)
	}
	if target2.Runtime != contracts.RuntimeDocker {
		t.Errorf("expected Runtime '%s', got '%s'", contracts.RuntimeDocker, target2.Runtime)
	}
}

func TestNormalizeTarget_K8sAndComposeLabels(t *testing.T) {
	// app.kubernetes.io/name
	t1, ok1 := NormalizeTarget("id1", "fe-pod", "running", map[string]string{
		"app.kubernetes.io/name":   "frontend-ui",
		"io.kubernetes.pod.name":   "frontend-ui-xyz",
		"io.kubernetes.pod.namespace": "web",
	})
	if !ok1 || t1.ServiceName != "frontend-ui" || t1.PodName != "frontend-ui-xyz" {
		t.Errorf("failed app.kubernetes.io/name test: %+v, ok=%v", t1, ok1)
	}

	// app
	t2, ok2 := NormalizeTarget("id2", "auth-pod", "running", map[string]string{
		"app": "auth-service",
	})
	if !ok2 || t2.ServiceName != "auth-service" {
		t.Errorf("failed app label test: %+v, ok=%v", t2, ok2)
	}

	// service_name
	t3, ok3 := NormalizeTarget("id3", "orders-pod", "running", map[string]string{
		"service_name": "orders-svc",
	})
	if !ok3 || t3.ServiceName != "orders-svc" {
		t.Errorf("failed service_name label test: %+v, ok=%v", t3, ok3)
	}

	// io.kubernetes.container.name
	t4, ok4 := NormalizeTarget("id4", "worker-pod", "running", map[string]string{
		"io.kubernetes.container.name": "worker-runner",
	})
	if !ok4 || t4.ServiceName != "worker-runner" {
		t.Errorf("failed io.kubernetes.container.name test: %+v, ok=%v", t4, ok4)
	}

	// com.docker.compose.service & com.docker.compose.project
	t5, ok5 := NormalizeTarget("id5", "redis", "running", map[string]string{
		"com.docker.compose.service": "redis-cache",
		"com.docker.compose.project": "custom-project",
	})
	if !ok5 || t5.ServiceName != "redis-cache" || t5.Namespace != "custom-project" {
		t.Errorf("failed docker compose labels test: %+v, ok=%v", t5, ok5)
	}
}

func TestNormalizeTarget_ContainerNameFallback(t *testing.T) {
	// Clean container name with k8s pod hash suffix stripped
	t1, ok1 := NormalizeTarget("id1", "/chat-session-7f9c", "running", nil)
	if !ok1 {
		t.Fatalf("expected ok1=true")
	}
	if t1.ServiceName != "chat-session" {
		t.Errorf("expected ServiceName 'chat-session', got '%s'", t1.ServiceName)
	}
	if t1.PodName != "chat-session-7f9c" {
		t.Errorf("expected PodName 'chat-session-7f9c', got '%s'", t1.PodName)
	}

	// Deployment hash + pod hash: chat-com-7b649fd7b4-z5w2k
	t2, ok2 := NormalizeTarget("id2", "chat-com-7b649fd7b4-z5w2k", "running", nil)
	if !ok2 {
		t.Fatalf("expected ok2=true")
	}
	if t2.ServiceName != "chat-com" {
		t.Errorf("expected ServiceName 'chat-com', got '%s'", t2.ServiceName)
	}

	// No hash suffix
	t3, ok3 := NormalizeTarget("id3", "/gateway-proxy", "running", nil)
	if !ok3 || t3.ServiceName != "gateway-proxy" {
		t.Errorf("expected ServiceName 'gateway-proxy', got '%s'", t3.ServiceName)
	}
}

func TestNormalizeTarget_SystemFiltering(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		rawName string
		labels  map[string]string
	}{
		{
			name:    "rca-clickhouse container",
			id:      "c1",
			rawName: "/rca-clickhouse",
			labels:  nil,
		},
		{
			name:    "rca-ingest container without leading slash",
			id:      "c2",
			rawName: "rca-ingest",
			labels:  nil,
		},
		{
			name:    "rca-tilt-collector container",
			id:      "c3",
			rawName: "/rca-tilt-collector",
			labels:  nil,
		},
		{
			name:    "kube-system namespace",
			id:      "c4",
			rawName: "/coredns-554",
			labels:  map[string]string{"io.kubernetes.pod.namespace": "kube-system"},
		},
		{
			name:    "local-path-storage namespace",
			id:      "c5",
			rawName: "/storage-prov",
			labels:  map[string]string{"io.kubernetes.pod.namespace": "local-path-storage"},
		},
		{
			name:    "ingress-nginx namespace",
			id:      "c6",
			rawName: "/nginx-controller",
			labels:  map[string]string{"io.kubernetes.pod.namespace": "ingress-nginx"},
		},
		{
			name:    "metallb-system namespace",
			id:      "c7",
			rawName: "/speaker",
			labels:  map[string]string{"io.kubernetes.pod.namespace": "metallb-system"},
		},
		{
			name:    "podsandbox pause container",
			id:      "c8",
			rawName: "/k8s_POD_chat-session",
			labels:  map[string]string{"io.kubernetes.docker.type": "podsandbox"},
		},
		{
			name:    "empty name and labels",
			id:      "c9",
			rawName: "",
			labels:  nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := NormalizeTarget(tc.id, tc.rawName, "running", tc.labels)
			if ok {
				t.Errorf("expected container %s to be filtered out (ok=false), but got ok=true", tc.name)
			}
		})
	}
}

func TestParseLogLine_CRIFormat(t *testing.T) {
	target := contracts.TiltTarget{
		ID:          "cid-1",
		Runtime:     contracts.RuntimeK8s,
		ServiceName: "chat-session",
		PodName:     "chat-session-7f9c",
		Namespace:   "chat",
	}

	// CRI stderr with F tag (Full line)
	rawCRI := `2026-09-30T08:20:00.500000000Z stderr F {"level":"error","msg":"db timeout"}`
	rec, ok := ParseLogLine(rawCRI, target)
	if !ok {
		t.Fatalf("expected ParseLogLine to return ok=true")
	}
	expectedLine := `{"level":"error","msg":"db timeout"}`
	if rec.Line != expectedLine {
		t.Errorf("expected rec.Line %q, got %q", expectedLine, rec.Line)
	}
	if rec.Fields["service_name"] != "chat-session" {
		t.Errorf("expected service_name 'chat-session', got '%s'", rec.Fields["service_name"])
	}
	if rec.Fields["pod"] != "chat-session-7f9c" {
		t.Errorf("expected pod 'chat-session-7f9c', got '%s'", rec.Fields["pod"])
	}
	if rec.Fields["namespace"] != "chat" {
		t.Errorf("expected namespace 'chat', got '%s'", rec.Fields["namespace"])
	}
	if rec.Fields["source"] != "tilt" {
		t.Errorf("expected source 'tilt', got '%s'", rec.Fields["source"])
	}
	if rec.Timestamp != "1790756400500000000" {
		t.Errorf("expected Timestamp '1790756400500000000', got '%s'", rec.Timestamp)
	}
	if rec.Date != "2026-09-30T08:20:00.5Z" {
		t.Errorf("expected Date '2026-09-30T08:20:00.5Z', got '%s'", rec.Date)
	}

	// CRI stdout with P tag (Partial line)
	rawCRIPartial := `2026-09-30T08:20:01.000000000Z stdout P partial message payload`
	rec2, ok2 := ParseLogLine(rawCRIPartial, target)
	if !ok2 || rec2.Line != "partial message payload" {
		t.Errorf("failed CRI partial line parsing: ok=%v, Line=%q", ok2, rec2.Line)
	}
}

func TestParseLogLine_DockerFormat(t *testing.T) {
	target := contracts.TiltTarget{
		ServiceName: "chat-com",
		PodName:     "chat-com-abc",
		Namespace:   "default",
	}

	// Docker format with timestamps=1 (no stdout/stderr CRI prefix)
	rawDocker := `2026-09-30T08:20:00.123456789Z {"level":"info","msg":"request started"}`
	rec, ok := ParseLogLine(rawDocker, target)
	if !ok {
		t.Fatalf("expected ParseLogLine ok=true")
	}
	expectedLine := `{"level":"info","msg":"request started"}`
	if rec.Line != expectedLine {
		t.Errorf("expected rec.Line %q, got %q", expectedLine, rec.Line)
	}
	if rec.Timestamp != "1790756400123456789" {
		t.Errorf("expected Timestamp '1790756400123456789', got '%s'", rec.Timestamp)
	}
}

func TestParseLogLine_PlainFormat(t *testing.T) {
	target := contracts.TiltTarget{
		ServiceName: "chat-com",
		PodName:     "chat-com-abc",
		Namespace:   "default",
	}

	before := time.Now().UTC().UnixNano()
	rawPlain := `plain text log message without timestamp prefix`
	rec, ok := ParseLogLine(rawPlain, target)
	after := time.Now().UTC().UnixNano()

	if !ok {
		t.Fatalf("expected ParseLogLine ok=true")
	}
	if rec.Line != rawPlain {
		t.Errorf("expected rec.Line %q, got %q", rawPlain, rec.Line)
	}
	tsNano := time.Now().UnixNano()
	_ = tsNano
	if rec.Timestamp == "" || rec.Date == "" {
		t.Errorf("expected non-empty Timestamp and Date")
	}
	_, err := json.Number(rec.Timestamp).Int64()
	if err != nil {
		t.Errorf("invalid int64 timestamp: %v", err)
	}
	_ = before
	_ = after
}

func TestParseLogLine_WithDockerHeader(t *testing.T) {
	target := contracts.TiltTarget{
		ServiceName: "chat-session",
		PodName:     "chat-session-1",
		Namespace:   "tilt-local",
	}

	// Create 8-byte Docker stream header + text
	line := `2026-09-30T08:20:00.000000000Z hello from docker`
	header := make([]byte, 8)
	header[0] = 1 // stdout
	binary.BigEndian.PutUint32(header[4:8], uint32(len(line)))
	fullString := string(header) + line

	rec, ok := ParseLogLine(fullString, target)
	if !ok {
		t.Fatalf("expected ParseLogLine ok=true")
	}
	if rec.Line != "hello from docker" {
		t.Errorf("expected rec.Line 'hello from docker', got %q", rec.Line)
	}
}

func TestParseLogLine_EmptyAndWhitespace(t *testing.T) {
	target := contracts.TiltTarget{ServiceName: "test"}

	if _, ok := ParseLogLine("", target); ok {
		t.Errorf("expected empty line to return ok=false")
	}
	if _, ok := ParseLogLine("   \r\n\t  ", target); ok {
		t.Errorf("expected whitespace line to return ok=false")
	}
	if _, ok := ParseLogLine("2026-09-30T08:20:00Z stdout F   ", target); ok {
		t.Errorf("expected empty payload to return ok=false")
	}
}

func TestStreamReader_DockerMultiplexed(t *testing.T) {
	target := contracts.TiltTarget{
		ServiceName: "chat-session",
		PodName:     "chat-session-7f9c",
		Namespace:   "chat",
	}

	// Build a multiplexed stream:
	// Frame 1: stdout, 1 line
	// Frame 2: stderr, 1 line
	// Frame 3: stdout, 2 lines separated by \n
	// Frame 4: size 0 (empty frame)
	// Frame 5: stdout, 1 line without trailing newline
	var buf bytes.Buffer

	writeFrame := func(streamType byte, payload string) {
		header := make([]byte, 8)
		header[0] = streamType
		binary.BigEndian.PutUint32(header[4:8], uint32(len(payload)))
		buf.Write(header)
		buf.WriteString(payload)
	}

	writeFrame(1, "2026-09-30T08:20:00.100000000Z stdout F line 1\n")
	writeFrame(2, "2026-09-30T08:20:00.200000000Z stderr F line 2\n")
	writeFrame(1, "2026-09-30T08:20:00.300000000Z line 3a\n2026-09-30T08:20:00.400000000Z line 3b\n")
	writeFrame(1, "")
	writeFrame(1, "2026-09-30T08:20:00.500000000Z line 5")

	var collected []contracts.RawScriptRecord
	var mu sync.Mutex
	err := StreamReader(context.Background(), &buf, target, func(rec contracts.RawScriptRecord) {
		mu.Lock()
		collected = append(collected, rec)
		mu.Unlock()
	})

	if err != nil {
		t.Fatalf("StreamReader returned unexpected error: %v", err)
	}

	expectedLines := []string{"line 1", "line 2", "line 3a", "line 3b", "line 5"}
	if len(collected) != len(expectedLines) {
		t.Fatalf("expected %d records, got %d", len(expectedLines), len(collected))
	}
	for i, expected := range expectedLines {
		if collected[i].Line != expected {
			t.Errorf("record %d: expected Line %q, got %q", i, expected, collected[i].Line)
		}
		if collected[i].Fields["service_name"] != "chat-session" {
			t.Errorf("record %d: expected service_name 'chat-session', got %q", i, collected[i].Fields["service_name"])
		}
	}
}

func TestStreamReader_PlainTextAndCRI(t *testing.T) {
	target := contracts.TiltTarget{
		ServiceName: "chat-com",
		PodName:     "chat-com-pod",
		Namespace:   "chat",
	}

	rawStream := "2026-09-30T08:20:00.100000000Z stdout F {\"msg\":\"msg1\"}\r\n" +
		"2026-09-30T08:20:00.200000000Z stderr F {\"msg\":\"msg2\"}\n" +
		"\n" + // empty line to skip
		"2026-09-30T08:20:00.300000000Z {\"msg\":\"msg3\"}\n"

	var collected []contracts.RawScriptRecord
	err := StreamReader(context.Background(), strings.NewReader(rawStream), target, func(rec contracts.RawScriptRecord) {
		collected = append(collected, rec)
	})

	if err != nil {
		t.Fatalf("StreamReader unexpected error: %v", err)
	}
	if len(collected) != 3 {
		t.Fatalf("expected 3 records, got %d", len(collected))
	}
	if collected[0].Line != `{"msg":"msg1"}` {
		t.Errorf("unexpected line 0: %q", collected[0].Line)
	}
	if collected[1].Line != `{"msg":"msg2"}` {
		t.Errorf("unexpected line 1: %q", collected[1].Line)
	}
	if collected[2].Line != `{"msg":"msg3"}` {
		t.Errorf("unexpected line 2: %q", collected[2].Line)
	}
}

func TestStreamReader_EmptyStream(t *testing.T) {
	target := contracts.TiltTarget{ServiceName: "chat-session"}
	count := 0
	err := StreamReader(context.Background(), strings.NewReader(""), target, func(rec contracts.RawScriptRecord) {
		count++
	})
	if err != nil {
		t.Fatalf("expected nil error on empty stream, got %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 records, got %d", count)
	}
}

func TestStreamReader_ContextCancellation(t *testing.T) {
	target := contracts.TiltTarget{ServiceName: "chat-session"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	pipeR, _ := io.Pipe()
	err := StreamReader(ctx, pipeR, target, func(rec contracts.RawScriptRecord) {})
	if err == nil {
		t.Errorf("expected error on canceled context, got nil")
	}
}

func TestDiscoverTargets_DockerAPI(t *testing.T) {
	dockerContainers := []map[string]interface{}{
		{
			"Id":     "id-chat-session",
			"Names":  []string{"/chat-session-7f9c"},
			"State":  "running",
			"Status": "Up 2 hours",
			"Labels": map[string]string{
				"dev.tilt.resource":         "chat-session",
				"io.kubernetes.pod.name":    "chat-session-7f9c",
				"io.kubernetes.pod.namespace": "chat",
			},
		},
		{
			"Id":     "id-rca-clickhouse",
			"Names":  []string{"/rca-clickhouse"},
			"State":  "running",
			"Status": "Up 2 hours",
			"Labels": map[string]string{
				"dev.tilt.resource": "clickhouse",
			},
		},
		{
			"Id":     "id-coredns",
			"Names":  []string{"/coredns"},
			"State":  "running",
			"Status": "Up 2 hours",
			"Labels": map[string]string{
				"io.kubernetes.pod.namespace": "kube-system",
			},
		},
		{
			"Id":     "id-pause",
			"Names":  []string{"/k8s_POD_chat-session"},
			"State":  "running",
			"Status": "Up 2 hours",
			"Labels": map[string]string{
				"io.kubernetes.docker.type": "podsandbox",
			},
		},
		{
			"Id":     "id-payment",
			"Names":  []string{"/payment-service"},
			"State":  "running",
			"Status": "Up 1 hour",
			"Labels": map[string]string{
				"com.docker.compose.service": "payment-service",
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/containers/json") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(dockerContainers)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := NewRuntimeClient(ts.URL, "")
	targets, err := client.DiscoverTargets(context.Background())
	if err != nil {
		t.Fatalf("DiscoverTargets failed: %v", err)
	}

	// Should contain only chat-session and payment-service (rca-clickhouse, coredns, pause filtered)
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d: %+v", len(targets), targets)
	}

	names := map[string]contracts.TiltTarget{
		targets[0].ServiceName: targets[0],
		targets[1].ServiceName: targets[1],
	}

	if _, ok := names["chat-session"]; !ok {
		t.Errorf("missing chat-session target")
	}
	if _, ok := names["payment-service"]; !ok {
		t.Errorf("missing payment-service target")
	}
}

func TestDiscoverTargets_Errors(t *testing.T) {
	// 1. Server returns 500
	tsErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer tsErr.Close()

	clientErr := NewRuntimeClient(tsErr.URL, "")
	_, err := clientErr.DiscoverTargets(context.Background())
	if err == nil {
		t.Errorf("expected error on 500 response, got nil")
	}

	// 2. Server returns invalid JSON
	tsBadJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{invalid-json"))
	}))
	defer tsBadJSON.Close()

	clientBad := NewRuntimeClient(tsBadJSON.URL, "")
	_, err = clientBad.DiscoverTargets(context.Background())
	if err == nil {
		t.Errorf("expected error on invalid JSON, got nil")
	}

	// 3. Client is nil
	var nilClient *RuntimeClient
	_, err = nilClient.DiscoverTargets(context.Background())
	if err == nil {
		t.Errorf("expected error on nil client")
	}
}

func TestDiscoverTargets_WithK8s(t *testing.T) {
	k8sPods := map[string]interface{}{
		"items": []map[string]interface{}{
			{
				"metadata": map[string]interface{}{
					"name":      "k8s-order-service-1",
					"namespace": "production",
					"uid":       "uid-k8s-order",
					"labels": map[string]string{
						"app": "k8s-order",
					},
				},
				"status": map[string]interface{}{
					"phase": "Running",
				},
			},
			{
				"metadata": map[string]interface{}{
					"name":      "kube-dns-123",
					"namespace": "kube-system",
					"uid":       "uid-dns",
				},
				"status": map[string]interface{}{
					"phase": "Running",
				},
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/containers/json") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]interface{}{})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/api/v1/pods") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(k8sPods)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := NewRuntimeClient(ts.URL, ts.URL)
	targets, err := client.DiscoverTargets(context.Background())
	if err != nil {
		t.Fatalf("DiscoverTargets failed: %v", err)
	}

	// Should contain k8s-order, but not kube-dns
	if len(targets) != 1 {
		t.Fatalf("expected 1 target from k8s, got %d: %+v", len(targets), targets)
	}
	if targets[0].ServiceName != "k8s-order" {
		t.Errorf("expected ServiceName 'k8s-order', got '%s'", targets[0].ServiceName)
	}
}

func TestNewRuntimeClient(t *testing.T) {
	// Unix socket path default
	c1 := NewRuntimeClient("", "")
	if c1.dockerBaseURL == "" {
		t.Errorf("expected non-empty dockerBaseURL")
	}

	// Unix socket custom
	c2 := NewRuntimeClient("unix:///var/custom/docker.sock", "http://k8s:8080")
	if c2.k8sAPIURL != "http://k8s:8080" {
		t.Errorf("expected k8sAPIURL 'http://k8s:8080', got '%s'", c2.k8sAPIURL)
	}

	// HTTP URL
	c3 := NewRuntimeClient("http://127.0.0.1:2375", "")
	if c3.dockerBaseURL != "http://127.0.0.1:2375" {
		t.Errorf("expected dockerBaseURL 'http://127.0.0.1:2375', got '%s'", c3.dockerBaseURL)
	}
}

func TestNormalizeTarget_EdgeCases(t *testing.T) {
	// k8s container name format: k8s_container-name_pod_namespace_uid_attempt
	t1, ok1 := NormalizeTarget("id1", "k8s_chat-backend_chat-backend-xyz_default_123_0", "running", nil)
	if !ok1 || t1.ServiceName != "chat-backend" {
		t.Errorf("failed k8s container prefix normalization: %+v, ok=%v", t1, ok1)
	}

	// Empty cleanName but labels present
	t2, ok2 := NormalizeTarget("id2", "", "running", map[string]string{
		"app": "label-only-service",
	})
	if !ok2 || t2.ServiceName != "label-only-service" || t2.PodName != "label-only-service" {
		t.Errorf("failed empty cleanName with labels test: %+v, ok=%v", t2, ok2)
	}

	// Docker compose project namespace
	t3, ok3 := NormalizeTarget("id3", "compose-svc", "running", map[string]string{
		"com.docker.compose.service": "compose-svc",
		"com.docker.compose.project": "my-compose-proj",
	})
	if !ok3 || t3.Namespace != "my-compose-proj" {
		t.Errorf("failed compose project namespace test: %+v, ok=%v", t3, ok3)
	}
}

func TestParseLogLine_EdgeCases(t *testing.T) {
	target := contracts.TiltTarget{
		ServiceName: "chat-session",
		PodName:     "chat-session-1",
		Namespace:   "default",
	}

	// Standard RFC3339 (without nano)
	rfc3339Line := `2026-09-30T08:20:00Z {"level":"info","msg":"standard rfc3339"}`
	rec, ok := ParseLogLine(rfc3339Line, target)
	if !ok || rec.Line != `{"level":"info","msg":"standard rfc3339"}` {
		t.Errorf("failed standard RFC3339 parsing: ok=%v, Line=%q", ok, rec.Line)
	}

	// CRI with 2 parts (stdout without F or P tag)
	cri2Parts := `2026-09-30T08:20:00.123456789Z stdout direct-payload`
	rec2, ok2 := ParseLogLine(cri2Parts, target)
	if !ok2 || rec2.Line != "direct-payload" {
		t.Errorf("failed CRI 2 parts parsing: ok=%v, Line=%q", ok2, rec2.Line)
	}
}

func TestStreamReader_ShortLinesAndContext(t *testing.T) {
	target := contracts.TiltTarget{ServiceName: "chat-session"}

	// Stream with short plain lines (< 8 bytes)
	shortStream := "hi\nbye\n"
	var records []contracts.RawScriptRecord
	err := StreamReader(context.Background(), strings.NewReader(shortStream), target, func(rec contracts.RawScriptRecord) {
		records = append(records, rec)
	})
	if err != nil {
		t.Fatalf("StreamReader short lines failed: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("expected 2 records, got %d", len(records))
	}

	// Stream with short line without trailing newline at EOF (< 8 bytes)
	noNewlineStream := "hi"
	var records2 []contracts.RawScriptRecord
	err2 := StreamReader(context.Background(), strings.NewReader(noNewlineStream), target, func(rec contracts.RawScriptRecord) {
		records2 = append(records2, rec)
	})
	if err2 != nil {
		t.Fatalf("StreamReader no newline short failed: %v", err2)
	}
	if len(records2) != 1 {
		t.Errorf("expected 1 record, got %d", len(records2))
	}
}

func TestDiscoverTargets_K8sEdgeCases(t *testing.T) {
	// Docker succeeds but K8s returns 500
	tsK8s500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/containers/json") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]interface{}{})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/api/v1/pods") {
			http.Error(w, "k8s api error", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer tsK8s500.Close()

	clientK8s500 := NewRuntimeClient(tsK8s500.URL, tsK8s500.URL)
	targets, err := clientK8s500.DiscoverTargets(context.Background())
	if err != nil {
		t.Errorf("expected DiscoverTargets to succeed gracefully when k8s fails: %v", err)
	}
	if len(targets) != 0 {
		t.Errorf("expected 0 targets, got %d", len(targets))
	}

	// Docker succeeds but K8s returns malformed JSON
	tsK8sBadJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/containers/json") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]interface{}{})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/api/v1/pods") {
			_, _ = w.Write([]byte("{invalid-json"))
			return
		}
		http.NotFound(w, r)
	}))
	defer tsK8sBadJSON.Close()

	clientK8sBad := NewRuntimeClient(tsK8sBadJSON.URL, tsK8sBadJSON.URL)
	targets2, err2 := clientK8sBad.DiscoverTargets(context.Background())
	if err2 != nil {
		t.Errorf("expected DiscoverTargets to succeed gracefully when k8s bad json: %v", err2)
	}
	if len(targets2) != 0 {
		t.Errorf("expected 0 targets, got %d", len(targets2))
	}
}

func TestStreamContainerLogs_Docker(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/containers/c123/logs") {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("2026-09-30T08:20:00.100Z log message 1\n2026-09-30T08:20:00.200Z log message 2\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := NewRuntimeClient(ts.URL, "")
	target := contracts.TiltTarget{
		ID:          "c123",
		Runtime:     contracts.RuntimeDocker,
		ServiceName: "chat-com",
		PodName:     "chat-com-pod",
	}

	var records []contracts.RawScriptRecord
	err := client.StreamContainerLogs(context.Background(), target, 10, func(rec contracts.RawScriptRecord) {
		records = append(records, rec)
	})
	if err != nil {
		t.Fatalf("StreamContainerLogs failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].Line != "log message 1" || records[1].Line != "log message 2" {
		t.Errorf("unexpected lines: %+v", records)
	}
}

func TestStreamContainerLogs_K8s(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/v1/namespaces/default/pods/pod-1/log") {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("2026-09-30T08:20:00.300Z stdout F k8s log line\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := NewRuntimeClient("", ts.URL)
	target := contracts.TiltTarget{
		ID:          "pod-1",
		Runtime:     contracts.RuntimeK8s,
		ServiceName: "k8s-service",
		PodName:     "pod-1",
		Namespace:   "default",
	}

	var records []contracts.RawScriptRecord
	err := client.StreamContainerLogs(context.Background(), target, 60, func(rec contracts.RawScriptRecord) {
		records = append(records, rec)
	})
	if err != nil {
		t.Fatalf("StreamContainerLogs K8s failed: %v", err)
	}
	if len(records) != 1 || records[0].Line != "k8s log line" {
		t.Errorf("unexpected records: %+v", records)
	}
}

func TestOpenContainerLogs_Errors(t *testing.T) {
	var nilClient *RuntimeClient
	target := contracts.TiltTarget{ID: "c1"}
	if _, err := nilClient.OpenContainerLogs(context.Background(), target, 0); err == nil {
		t.Errorf("expected error on nil client")
	}

	ts500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer ts500.Close()

	client500 := NewRuntimeClient(ts500.URL, ts500.URL)
	if _, err := client500.OpenContainerLogs(context.Background(), target, 0); err == nil {
		t.Errorf("expected error on docker 500 response")
	}

	k8sTarget := contracts.TiltTarget{Runtime: contracts.RuntimeK8s, PodName: "p1", Namespace: "n1"}
	if _, err := client500.OpenContainerLogs(context.Background(), k8sTarget, 0); err == nil {
		t.Errorf("expected error on k8s 500 response")
	}
}


