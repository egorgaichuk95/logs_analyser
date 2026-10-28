package reporter_test

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/evidence-service/internal/reporter"
)

func TestRedactPII_Email(t *testing.T) {
	input := "User logged in with email: ak.odilov@talkwus.com on system."
	output := reporter.RedactPII(input)

	if strings.Contains(output, "ak.odilov@talkwus.com") {
		t.Fatalf("expected email to be redacted, got: %s", output)
	}
	if !strings.Contains(output, "<EMAIL_") {
		t.Fatalf("expected <EMAIL_ token, got: %s", output)
	}

	// Determinism check
	output2 := reporter.RedactPII(input)
	if output != output2 {
		t.Fatalf("expected deterministic redaction, got %s != %s", output, output2)
	}
}

func TestRedactPII_DisplayName(t *testing.T) {
	// URL-encoded display name
	input1 := "GET /api/user?data=%22display_name%22%3A%22Rahil+Nabiyev%22&id=123"
	output1 := reporter.RedactPII(input1)

	if strings.Contains(output1, "Rahil+Nabiyev") {
		t.Fatalf("expected display name to be redacted, got: %s", output1)
	}
	if !strings.Contains(output1, "<USER_") {
		t.Fatalf("expected <USER_ token, got: %s", output1)
	}

	// JSON format display name
	input2 := `{"display_name": "Rahil Nabiyev", "role": "admin"}`
	output2 := reporter.RedactPII(input2)
	if strings.Contains(output2, "Rahil") {
		t.Fatalf("expected JSON display name to be redacted, got: %s", output2)
	}
	if !strings.Contains(output2, "<USER_") {
		t.Fatalf("expected <USER_ token in json, got: %s", output2)
	}

	// Plain equal format
	input3 := "event auth display_name=JohnDoe status=ok"
	output3 := reporter.RedactPII(input3)
	if strings.Contains(output3, "JohnDoe") {
		t.Fatalf("expected equal display name to be redacted, got: %s", output3)
	}
	if !strings.Contains(output3, "<USER_") {
		t.Fatalf("expected <USER_ token in equal, got: %s", output3)
	}
}

func TestRedactPII_IPPreservationAndMasking(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		shouldKeep []string
		shouldMask []string
	}{
		{
			name:       "Public IP is masked",
			input:      "Incoming connection from 5.191.119.115 on port 443",
			shouldKeep: []string{"port 443"},
			shouldMask: []string{"5.191.119.115"},
		},
		{
			name:       "Private 10.x.x.x is preserved",
			input:      "Connecting to k8s pod at 10.128.26.1:8080 successfully",
			shouldKeep: []string{"10.128.26.1"},
			shouldMask: nil,
		},
		{
			name:       "Private 172.16-31.x.x is preserved",
			input:      "Internal routing 172.16.0.1 and 172.24.12.5 and 172.31.255.254",
			shouldKeep: []string{"172.16.0.1", "172.24.12.5", "172.31.255.254"},
			shouldMask: nil,
		},
		{
			name:       "Private 192.168.x.x is preserved",
			input:      "Router gateway 192.168.1.1 responded in 2ms",
			shouldKeep: []string{"192.168.1.1"},
			shouldMask: nil,
		},
		{
			name:       "Loopback 127.x.x.x is preserved",
			input:      "Local healthcheck on 127.0.0.1:8080/health",
			shouldKeep: []string{"127.0.0.1"},
			shouldMask: nil,
		},
		{
			name:       "K8s internal DNS cluster.local is preserved",
			input:      "DNS query to chat-com.chat.svc.cluster.local succeeded",
			shouldKeep: []string{"chat-com.chat.svc.cluster.local"},
			shouldMask: nil,
		},
		{
			name:       "Multiple IPs mixed",
			input:      "Forwarded from 8.8.8.8 via proxy 10.128.26.1 to client 93.184.216.34",
			shouldKeep: []string{"10.128.26.1"},
			shouldMask: []string{"8.8.8.8", "93.184.216.34"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			output := reporter.RedactPII(tc.input)

			for _, keep := range tc.shouldKeep {
				if !strings.Contains(output, keep) {
					t.Fatalf("expected preserved string %q in output: %s", keep, output)
				}
			}

			for _, mask := range tc.shouldMask {
				if strings.Contains(output, mask) {
					t.Fatalf("expected masked IP %q to be gone, but found in output: %s", mask, output)
				}
				if !strings.Contains(output, "<CLIENT_IP_") {
					t.Fatalf("expected <CLIENT_IP_ token in output: %s", output)
				}
			}
		})
	}
}

func TestRedactPII_JWTFallback(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	input := fmt.Sprintf("Authorization: Bearer %s expired", jwt)
	output := reporter.RedactPII(input)

	if strings.Contains(output, "SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c") {
		t.Fatalf("expected JWT to be redacted, got: %s", output)
	}
	if !strings.Contains(output, "<TOKEN_") {
		t.Fatalf("expected <TOKEN_ token in output: %s", output)
	}
}

func TestRedactPII_Empty(t *testing.T) {
	if got := reporter.RedactPII(""); got != "" {
		t.Fatalf("expected empty string, got: %s", got)
	}
}

func sampleBuildRequest() (contracts.BuildEvidenceRequest, []contracts.EvidenceItem) {
	now := time.Date(2026, 10, 28, 14, 0, 0, 0, time.UTC)
	incidentStart := now.Add(-15 * time.Minute)
	incidentEnd := now

	req := contracts.BuildEvidenceRequest{
		CaseID: "case-alpha-42",
		RunID:  "run-777",
		Analysis: contracts.AnalyzeCaseResponse{
			CaseID:        "case-alpha-42",
			RunID:         "run-777",
			IncidentStart: incidentStart,
			IncidentEnd:   incidentEnd,
			Hypotheses: []contracts.Hypothesis{
				{
					ID:                 "H-01",
					Rank:               1,
					Role:               contracts.RoleRootCause,
					Confidence:         0.94,
					Service:            "chat-session",
					TemplateID:         1001,
					TemplateText:       "failed to acquire connection pool: context deadline exceeded",
					ErrorClass:         "DEADLINE",
					SourcePlace:        "pool.go:142",
					DominantPod:        "chat-session-6b9c-7x",
					DominantRoute:      "POST /v1/session/start",
					DominantVersion:    "v1.4.2",
					OnsetTime:          incidentStart.Add(2 * time.Minute),
					BaselineRatePerMin: 0.05,
					IncidentRatePerMin: 24.5,
					GrowthRatio:        490.0,
					PoissonPValue:      1e-9,
					Explanation:        "Severe connection starvation in pool causing cascading cancellations upstream",
					EvidenceIDs:        []string{"E-01", "E-02"},
				},
				{
					ID:                 "H-02",
					Rank:               2,
					Role:               contracts.RolePropagation,
					Confidence:         0.78,
					Service:            "chat-com",
					TemplateID:         2002,
					TemplateText:       "upstream session request timed out: context canceled",
					ErrorClass:         "CANCELED",
					SourcePlace:        "client.go:88",
					DominantPod:        "chat-com-9f1a-3y",
					DominantRoute:      "POST /v1/chat/message",
					DominantVersion:    "v1.4.0",
					OnsetTime:          incidentStart.Add(3 * time.Minute),
					BaselineRatePerMin: 0.1,
					IncidentRatePerMin: 18.0,
					GrowthRatio:        180.0,
					PoissonPValue:      1e-7,
					Explanation:        "Canceled due to timeout from downstream chat-session",
					EvidenceIDs:        []string{"E-03"},
				},
			},
			ServiceEdges: []contracts.ServiceEdge{
				{
					CaseID:         "case-alpha-42",
					CallerService:  "chat-com",
					CalleeService:  "chat-session",
					EvidenceSource: "trace_propagation",
					CallCount:      150,
					ErrorCount:     142,
				},
			},
			ClockSkewFindings: []contracts.ClockSkewFinding{
				{
					Service:     "chat-com",
					Pod:         "chat-com-9f1a-3y",
					SkewMs:      12,
					SampleRawTs: "2026-10-28 14:02:00.012",
					SampleAppTs: "2026-10-28 14:02:00.000",
				},
			},
			Timeline: []contracts.TimelinePoint{
				{
					Bucket:      incidentStart,
					Service:     "chat-com",
					Level:       "INFO",
					EventsCount: 100,
				},
				{
					Bucket:      incidentStart.Add(2 * time.Minute),
					Service:     "chat-session",
					Level:       "ERROR",
					EventsCount: 45,
					MaxLatency:  5000000000,
				},
			},
		},
	}

	items := []contracts.EvidenceItem{
		{
			ID:              "E-01",
			Kind:            contracts.EvidenceKindStatTest,
			Service:         "chat-session",
			Summary:         "Stat test onset for connection pool failure",
			Timestamp:       incidentStart.Add(2 * time.Minute),
			SourceFile:      "chat-session.log",
			SourceRecordIdx: 124,
			SHA256:          "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
			RedactedLine:    "connection pool exhausted on pod chat-session-6b9c-7x for user ak.odilov@talkwus.com",
			LogQLSnippet:    `{service="chat-session"} |= "connection pool exhausted"`,
		},
		{
			ID:              "E-02",
			Kind:            contracts.EvidenceKindRawLog,
			Service:         "chat-session",
			Summary:         "Raw log sample with client IP",
			Timestamp:       incidentStart.Add(2 * time.Minute + 5*time.Second),
			SourceFile:      "chat-session.log",
			SourceRecordIdx: 130,
			SHA256:          "fedcba0987654321fedcba0987654321fedcba0987654321fedcba0987654321",
			RedactedLine:    `request from 5.191.119.115 to node 10.128.26.1 dns chat-com.chat.svc.cluster.local failed %22display_name%22%3A%22Rahil+Nabiyev%22`,
			LogQLSnippet:    `{service="chat-session"} |= "request from"`,
		},
		{
			ID:              "E-03",
			Kind:            contracts.EvidenceKindTraceChain,
			Service:         "chat-com",
			Summary:         "Trace chain timeout propagation",
			Timestamp:       incidentStart.Add(3 * time.Minute),
			SourceFile:      "chat-com.log",
			SourceRecordIdx: 215,
			SHA256:          "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			RedactedLine:    "context canceled while waiting for chat-session response",
			LogQLSnippet:    `{service="chat-com"} |= "context canceled"`,
		},
	}

	return req, items
}

func TestBuildAllArtifacts_CreatesAll7FilesAndValidZip(t *testing.T) {
	tmpDir := t.TempDir()
	req, items := sampleBuildRequest()

	resp, err := reporter.BuildAllArtifacts(tmpDir, req, items)
	if err != nil {
		t.Fatalf("BuildAllArtifacts returned unexpected error: %v", err)
	}

	if resp.CaseID != req.CaseID {
		t.Errorf("expected CaseID %s, got %s", req.CaseID, resp.CaseID)
	}
	if resp.RunID != req.RunID {
		t.Errorf("expected RunID %s, got %s", req.RunID, resp.RunID)
	}

	runDir := filepath.Join(tmpDir, req.CaseID, req.RunID)
	if resp.ReportDir != runDir {
		t.Errorf("expected ReportDir %s, got %s", runDir, resp.ReportDir)
	}

	// 1. Verify all 7 files exist on disk
	expectedFiles := []string{
		"timeline.svg",
		"topology.svg",
		"evidence.jsonl",
		"report.md",
		"report.html",
		"gemini_bundle.md",
		fmt.Sprintf("evidence_%s.zip", req.CaseID),
	}

	for _, fname := range expectedFiles {
		p := filepath.Join(runDir, fname)
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("missing expected file %s: %v", fname, err)
		}
		if info.Size() == 0 {
			t.Fatalf("file %s is unexpectedly empty (0 bytes)", fname)
		}
	}

	// 2. Verify SVGs
	timelineContent, err := os.ReadFile(filepath.Join(runDir, "timeline.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(timelineContent), "<svg") || !strings.Contains(string(timelineContent), "</svg>") {
		t.Errorf("timeline.svg is not valid SVG: %s", string(timelineContent[:100]))
	}

	topologyContent, err := os.ReadFile(filepath.Join(runDir, "topology.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(topologyContent), "<svg") || !strings.Contains(string(topologyContent), "</svg>") {
		t.Errorf("topology.svg is not valid SVG: %s", string(topologyContent[:100]))
	}

	// 3. Verify evidence.jsonl
	jsonlContent, err := os.ReadFile(filepath.Join(runDir, "evidence.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(jsonlContent)), "\n")
	if len(lines) != len(items) {
		t.Fatalf("expected %d lines in evidence.jsonl, got %d", len(items), len(lines))
	}
	for i, line := range lines {
		var item contracts.EvidenceItem
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			t.Fatalf("line %d is not valid json: %v", i, err)
		}
		if item.ID != items[i].ID {
			t.Errorf("item %d id mismatch: %s != %s", i, item.ID, items[i].ID)
		}
	}

	// 4. Verify report.md
	mdContent, err := os.ReadFile(filepath.Join(runDir, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	mdStr := string(mdContent)
	if !strings.Contains(mdStr, "timeline.svg") || !strings.Contains(mdStr, "topology.svg") {
		t.Errorf("report.md should reference timeline.svg and topology.svg")
	}
	if !strings.Contains(mdStr, req.CaseID) {
		t.Errorf("report.md should contain case ID %s", req.CaseID)
	}
	if !strings.Contains(mdStr, "ROOT_CAUSE") {
		t.Errorf("report.md should contain ROOT_CAUSE")
	}

	// 5. Verify report.html
	htmlContent, err := os.ReadFile(filepath.Join(runDir, "report.html"))
	if err != nil {
		t.Fatal(err)
	}
	htmlStr := string(htmlContent)
	if !strings.Contains(htmlStr, "<!DOCTYPE html>") {
		t.Errorf("report.html should have DOCTYPE")
	}
	if !strings.Contains(htmlStr, "<svg") {
		t.Errorf("report.html should inline SVGs")
	}

	// 6. Verify gemini_bundle.md (system prompt, size <= 320 KB, PII redaction)
	bundleContent, err := os.ReadFile(filepath.Join(runDir, "gemini_bundle.md"))
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(bundleContent)) > 320*1024 {
		t.Fatalf("gemini_bundle.md size %d exceeds 320 KB limit", len(bundleContent))
	}
	bundleStr := string(bundleContent)
	requiredSystemPrompt := "# SYSTEM PROMPT FOR GOOGLE GEMINI (RCA VERIFICATION)"
	if !strings.Contains(bundleStr, requiredSystemPrompt) {
		t.Fatalf("gemini_bundle.md must start with system prompt: %s", requiredSystemPrompt)
	}
	if !strings.Contains(bundleStr, "Ты — Principal SRE и эксперт по распределённым микросервисным системам.") {
		t.Fatalf("gemini_bundle.md must contain exact Russian system prompt text")
	}
	// Check PII redaction in bundle
	if strings.Contains(bundleStr, "ak.odilov@talkwus.com") {
		t.Errorf("gemini_bundle.md must redact email")
	}
	if strings.Contains(bundleStr, "5.191.119.115") {
		t.Errorf("gemini_bundle.md must redact public IP 5.191.119.115")
	}
	if strings.Contains(bundleStr, "Rahil+Nabiyev") {
		t.Errorf("gemini_bundle.md must redact display_name Rahil+Nabiyev")
	}
	if !strings.Contains(bundleStr, "10.128.26.1") {
		t.Errorf("gemini_bundle.md must preserve private IP 10.128.26.1")
	}
	if !strings.Contains(bundleStr, "chat-com.chat.svc.cluster.local") {
		t.Errorf("gemini_bundle.md must preserve k8s DNS")
	}

	// 7. Verify evidence_<case_id>.zip and manifest.json
	zipPath := filepath.Join(runDir, fmt.Sprintf("evidence_%s.zip", req.CaseID))
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("failed to open zip file: %v", err)
	}
	defer zr.Close()

	zipFileMap := make(map[string][]byte)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("failed to open file %s inside zip: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("failed to read file %s inside zip: %v", f.Name, err)
		}
		zipFileMap[f.Name] = data
	}

	// Check manifest.json is inside zip
	manifestData, ok := zipFileMap["manifest.json"]
	if !ok {
		t.Fatalf("manifest.json not found inside zip archive")
	}

	var manifest struct {
		CaseID    string `json:"case_id"`
		RunID     string `json:"run_id"`
		Files     []struct {
			Name      string `json:"name"`
			SizeBytes int64  `json:"size_bytes"`
			SHA256    string `json:"sha256"`
		} `json:"files"`
		Checksums map[string]string `json:"checksums"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("invalid json in manifest.json: %v", err)
	}

	if manifest.CaseID != req.CaseID || manifest.RunID != req.RunID {
		t.Errorf("manifest metadata mismatch: got case=%s, run=%s", manifest.CaseID, manifest.RunID)
	}

	// Check that all 6 files in zip have matching SHA256 in manifest
	expectedZipFiles := []string{
		"timeline.svg",
		"topology.svg",
		"evidence.jsonl",
		"report.md",
		"report.html",
		"gemini_bundle.md",
	}

	for _, fname := range expectedZipFiles {
		data, ok := zipFileMap[fname]
		if !ok {
			t.Fatalf("file %s missing in zip", fname)
		}
		h := sha256.Sum256(data)
		actualSHA := hex.EncodeToString(h[:])

		var foundSHA string
		for _, mf := range manifest.Files {
			if mf.Name == fname {
				foundSHA = mf.SHA256
				break
			}
		}
		if foundSHA == "" && manifest.Checksums != nil {
			foundSHA = manifest.Checksums[fname]
		}

		if foundSHA != actualSHA {
			t.Errorf("SHA256 mismatch for %s: manifest=%s, actual=%s", fname, foundSHA, actualSHA)
		}
	}
}

func TestBuildAllArtifacts_GeminiBundleSizeEnforcement(t *testing.T) {
	tmpDir := t.TempDir()
	req, _ := sampleBuildRequest()

	// Create 1000 large evidence items to trigger size enforcement
	largeItems := make([]contracts.EvidenceItem, 0, 1000)
	for i := 0; i < 1000; i++ {
		largeItems = append(largeItems, contracts.EvidenceItem{
			ID:              fmt.Sprintf("E-%04d", i+1),
			Kind:            contracts.EvidenceKindRawLog,
			Service:         "chat-session",
			Summary:         fmt.Sprintf("Large payload test event %d with repetitive content for stress testing bundle size limit", i),
			Timestamp:       time.Now().UTC(),
			SourceFile:      "chat-session.log",
			SourceRecordIdx: uint32(i),
			SHA256:          "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
			RedactedLine:    strings.Repeat(fmt.Sprintf("Event #%d log data content snippet ", i), 20),
			LogQLSnippet:    `{service="chat-session"} |= "Event"`,
		})
	}

	resp, err := reporter.BuildAllArtifacts(tmpDir, req, largeItems)
	if err != nil {
		t.Fatalf("BuildAllArtifacts returned unexpected error with large items: %v", err)
	}

	bundlePath := resp.GeminiPath
	fi, err := os.Stat(bundlePath)
	if err != nil {
		t.Fatalf("stat failed on bundle path: %v", err)
	}

	const maxBytes = 320 * 1024
	if fi.Size() > maxBytes {
		t.Fatalf("bundle size %d exceeded maximum limit of %d bytes", fi.Size(), maxBytes)
	}
}

func TestBuildAllArtifacts_EmptyRequestAndEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Empty CaseID should return error
	emptyReq := contracts.BuildEvidenceRequest{}
	_, err := reporter.BuildAllArtifacts(tmpDir, emptyReq, nil)
	if err == nil {
		t.Fatalf("expected error for empty CaseID, got nil")
	}

	// 2. Empty runID should default cleanly
	validReq := contracts.BuildEvidenceRequest{
		CaseID: "case-empty-data",
	}
	resp, err := reporter.BuildAllArtifacts(tmpDir, validReq, nil)
	if err != nil {
		t.Fatalf("unexpected error for empty hypotheses/evidence: %v", err)
	}
	if resp.RunID == "" {
		t.Errorf("expected generated RunID for empty request, got empty")
	}

	// Check files created
	if _, err := os.Stat(resp.ZipPath); err != nil {
		t.Errorf("expected zip file to exist at %s: %v", resp.ZipPath, err)
	}
}
