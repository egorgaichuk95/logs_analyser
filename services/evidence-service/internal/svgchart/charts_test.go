package svgchart

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

func TestRenderTimelineSVG_ValidXML(t *testing.T) {
	now := time.Date(2026, 10, 28, 14, 0, 0, 0, time.UTC)
	points := []contracts.TimelinePoint{
		{
			Bucket:      now,
			Service:     "chat-com",
			Level:       "INFO",
			EventsCount: 120,
			MaxLatency:  15000000,
		},
		{
			Bucket:      now.Add(time.Minute),
			Service:     "chat-session",
			Level:       "ERROR",
			EventsCount: 450,
			MaxLatency:  85000000,
		},
		{
			Bucket:      now.Add(2 * time.Minute),
			Service:     "chat-com",
			Level:       "ERROR",
			EventsCount: 300,
			MaxLatency:  50000000,
		},
	}

	hypotheses := []contracts.Hypothesis{
		{
			ID:                 "H-01",
			Service:            "chat-session",
			Role:               contracts.RoleRootCause,
			OnsetTime:          now.Add(time.Minute),
			IncidentRatePerMin: 450.0,
			BaselineRatePerMin: 2.0,
			Confidence:         0.95,
		},
		{
			ID:                 "H-02",
			Service:            "chat-com",
			Role:               contracts.RolePropagation,
			OnsetTime:          now.Add(2 * time.Minute),
			IncidentRatePerMin: 300.0,
			BaselineRatePerMin: 5.0,
			Confidence:         0.80,
		},
	}

	svg := RenderTimelineSVG(points, hypotheses)
	if !strings.HasPrefix(strings.TrimSpace(svg), "<svg") {
		t.Fatalf("SVG must start with '<svg', got: %s", svg[:min(50, len(svg))])
	}
	if !strings.HasSuffix(strings.TrimSpace(svg), "</svg>") {
		t.Fatalf("SVG must end with '</svg>'")
	}

	// Verify exact dimensions and viewBox
	if !strings.Contains(svg, `width="900"`) || !strings.Contains(svg, `height="320"`) {
		t.Errorf("expected 900x320 dimensions in SVG: %s", svg[:min(200, len(svg))])
	}
	if !strings.Contains(svg, `viewBox="0 0 900 320"`) {
		t.Errorf("expected viewBox '0 0 900 320' in SVG")
	}

	// Verify valid XML structure
	var parsed struct {
		XMLName xml.Name `xml:"svg"`
	}
	if err := xml.Unmarshal([]byte(svg), &parsed); err != nil {
		t.Fatalf("SVG is not valid XML: %v\nContent:\n%s", err, svg)
	}

	// Check services and roles are included
	if !strings.Contains(svg, "chat-session") {
		t.Errorf("expected 'chat-session' in SVG")
	}
	if !strings.Contains(svg, "chat-com") {
		t.Errorf("expected 'chat-com' in SVG")
	}
	if !strings.Contains(svg, contracts.RoleRootCause) {
		t.Errorf("expected '%s' in SVG", contracts.RoleRootCause)
	}
}

func TestRenderTimelineSVG_Empty(t *testing.T) {
	svg := RenderTimelineSVG(nil, nil)
	if !strings.HasPrefix(strings.TrimSpace(svg), "<svg") || !strings.HasSuffix(strings.TrimSpace(svg), "</svg>") {
		t.Fatalf("invalid SVG wrapper on empty input: %s", svg)
	}
	if !strings.Contains(svg, `width="900"`) || !strings.Contains(svg, `height="320"`) {
		t.Errorf("expected 900x320 dimensions on empty input")
	}

	var parsed struct {
		XMLName xml.Name `xml:"svg"`
	}
	if err := xml.Unmarshal([]byte(svg), &parsed); err != nil {
		t.Fatalf("empty SVG is not valid XML: %v", err)
	}

	// Also test with empty points but non-empty hypotheses
	hypotheses := []contracts.Hypothesis{
		{
			ID:      "H-01",
			Service: "payment-gateway",
			Role:    contracts.RoleRootCause,
		},
	}
	svgWithHyp := RenderTimelineSVG(nil, hypotheses)
	if !strings.Contains(svgWithHyp, "payment-gateway") || !strings.Contains(svgWithHyp, contracts.RoleRootCause) {
		t.Errorf("expected hypothesis info in empty points timeline: %s", svgWithHyp)
	}
	if err := xml.Unmarshal([]byte(svgWithHyp), &parsed); err != nil {
		t.Fatalf("SVG with hypotheses is not valid XML: %v", err)
	}
}

func TestRenderTopologySVG_ValidXML(t *testing.T) {
	edges := []contracts.ServiceEdge{
		{
			CaseID:         "case-101",
			CallerService:  "frontend-proxy",
			CalleeService:  "chat-com",
			EvidenceSource: "trace_propagation",
			CallCount:      5000,
			ErrorCount:     25,
		},
		{
			CaseID:         "case-101",
			CallerService:  "chat-com",
			CalleeService:  "chat-session",
			EvidenceSource: "peer_url",
			CallCount:      4000,
			ErrorCount:     850,
		},
		{
			CaseID:         "case-101",
			CallerService:  "chat-session",
			CalleeService:  "postgres",
			EvidenceSource: "peer_url",
			CallCount:      2000,
			ErrorCount:     1900,
		},
	}

	hypotheses := []contracts.Hypothesis{
		{
			ID:         "H-01",
			Service:    "chat-session",
			Role:       contracts.RoleRootCause,
			Confidence: 0.96,
		},
		{
			ID:         "H-02",
			Service:    "chat-com",
			Role:       contracts.RolePropagation,
			Confidence: 0.85,
		},
		{
			ID:         "H-03",
			Service:    "frontend-proxy",
			Role:       contracts.RoleSymptom,
			Confidence: 0.40,
		},
	}

	svg := RenderTopologySVG(edges, hypotheses)
	if !strings.HasPrefix(strings.TrimSpace(svg), "<svg") {
		t.Fatalf("SVG must start with '<svg', got: %s", svg[:min(50, len(svg))])
	}
	if !strings.HasSuffix(strings.TrimSpace(svg), "</svg>") {
		t.Fatalf("SVG must end with '</svg>'")
	}

	// Verify exact dimensions and viewBox
	if !strings.Contains(svg, `width="800"`) || !strings.Contains(svg, `height="280"`) {
		t.Errorf("expected 800x280 dimensions in SVG")
	}
	if !strings.Contains(svg, `viewBox="0 0 800 280"`) {
		t.Errorf("expected viewBox '0 0 800 280' in SVG")
	}

	// Verify valid XML structure
	var parsed struct {
		XMLName xml.Name `xml:"svg"`
	}
	if err := xml.Unmarshal([]byte(svg), &parsed); err != nil {
		t.Fatalf("SVG is not valid XML: %v\nContent:\n%s", err, svg)
	}

	// Check services and arrow markers
	if !strings.Contains(svg, "chat-session") {
		t.Errorf("expected 'chat-session' in SVG")
	}
	if !strings.Contains(svg, "chat-com") {
		t.Errorf("expected 'chat-com' in SVG")
	}
	if !strings.Contains(svg, contracts.RoleRootCause) {
		t.Errorf("expected '%s' in SVG", contracts.RoleRootCause)
	}
	if !strings.Contains(svg, "arrow") {
		t.Errorf("expected arrow marker in SVG")
	}
}

func TestRenderTopologySVG_Empty(t *testing.T) {
	svg := RenderTopologySVG(nil, nil)
	if !strings.HasPrefix(strings.TrimSpace(svg), "<svg") || !strings.HasSuffix(strings.TrimSpace(svg), "</svg>") {
		t.Fatalf("invalid SVG wrapper on empty input: %s", svg)
	}
	if !strings.Contains(svg, `width="800"`) || !strings.Contains(svg, `height="280"`) {
		t.Errorf("expected 800x280 dimensions on empty input")
	}

	var parsed struct {
		XMLName xml.Name `xml:"svg"`
	}
	if err := xml.Unmarshal([]byte(svg), &parsed); err != nil {
		t.Fatalf("empty topology SVG is not valid XML: %v", err)
	}
}

func TestRenderTopologySVG_HypothesesOnly(t *testing.T) {
	hypotheses := []contracts.Hypothesis{
		{
			ID:         "H-01",
			Service:    "isolated-service",
			Role:       contracts.RoleRootCause,
			Confidence: 0.90,
		},
	}

	svg := RenderTopologySVG(nil, hypotheses)
	var parsed struct {
		XMLName xml.Name `xml:"svg"`
	}
	if err := xml.Unmarshal([]byte(svg), &parsed); err != nil {
		t.Fatalf("SVG is not valid XML: %v", err)
	}
	if !strings.Contains(svg, "isolated-service") {
		t.Errorf("expected 'isolated-service' in SVG")
	}
	if !strings.Contains(svg, contracts.RoleRootCause) {
		t.Errorf("expected ROOT_CAUSE highlight in SVG")
	}
}

func TestRenderTopologySVG_MultiNodeTwoRows(t *testing.T) {
	edges := []contracts.ServiceEdge{
		{CallerService: "api-gw", CalleeService: "auth-svc", CallCount: 100, ErrorCount: 0},
		{CallerService: "auth-svc", CalleeService: "user-svc", CallCount: 100, ErrorCount: 2},
		{CallerService: "user-svc", CalleeService: "order-svc", CallCount: 100, ErrorCount: 5},
		{CallerService: "order-svc", CalleeService: "payment-svc", CallCount: 100, ErrorCount: 20},
		{CallerService: "payment-svc", CalleeService: "db-node", CallCount: 100, ErrorCount: 80},
	}
	hypotheses := []contracts.Hypothesis{
		{Service: "db-node", Role: contracts.RoleRootCause},
		{Service: "payment-svc", Role: contracts.RolePropagation},
		{Service: "order-svc", Role: contracts.RoleSymptom},
		{Service: "user-svc", Role: contracts.RoleSymptom},
		{Service: "auth-svc", Role: contracts.RoleSymptom},
		{Service: "api-gw", Role: contracts.RoleSymptom},
	}

	svg := RenderTopologySVG(edges, hypotheses)
	var parsed struct {
		XMLName xml.Name `xml:"svg"`
	}
	if err := xml.Unmarshal([]byte(svg), &parsed); err != nil {
		t.Fatalf("multi-node SVG is not valid XML: %v", err)
	}
	if !strings.Contains(svg, "db-node") || !strings.Contains(svg, "payment-svc") || !strings.Contains(svg, "api-gw") {
		t.Errorf("expected all services in multi-node SVG")
	}
}
