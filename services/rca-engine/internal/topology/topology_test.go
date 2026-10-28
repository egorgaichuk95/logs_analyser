package topology_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/rca-engine/internal/topology"
)

func floatEquals(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}

// TestCascadeTopology проверяет каскад api-gateway -> chat-session -> chat-com
// Оценки: api-gateway (0.15), chat-session (0.35), chat-com (1.0).
func TestCascadeTopology(t *testing.T) {
	explicitEdges := []contracts.ServiceEdge{
		{
			CaseID:         "case-101",
			CallerService:  "api-gateway",
			CalleeService:  "chat-session",
			EvidenceSource: "peer_url",
			CallCount:      100,
			ErrorCount:     20,
		},
	}
	chainEdges := []contracts.ServiceEdge{
		{
			CaseID:         "case-101",
			CallerService:  "chat-session",
			CalleeService:  "chat-com",
			EvidenceSource: "trace_propagation",
			CallCount:      80,
			ErrorCount:     15,
		},
	}
	anomalous := map[string]bool{
		"api-gateway":  true,
		"chat-session": true,
		"chat-com":     true,
	}

	res := topology.ComputeTopology(explicitEdges, chainEdges, anomalous)

	if !floatEquals(res.ServiceRootScore["chat-com"], 1.0) {
		t.Fatalf("expected chat-com score 1.0, got %v", res.ServiceRootScore["chat-com"])
	}
	if !floatEquals(res.ServiceRootScore["chat-session"], 0.35) {
		t.Fatalf("expected chat-session score 0.35, got %v", res.ServiceRootScore["chat-session"])
	}
	if !floatEquals(res.ServiceRootScore["api-gateway"], 0.15) {
		t.Fatalf("expected api-gateway score 0.15, got %v", res.ServiceRootScore["api-gateway"])
	}
}

// TestIsolatedAnomalousService проверяет изолированный аномальный сервис (0.75).
func TestIsolatedAnomalousService(t *testing.T) {
	explicitEdges := []contracts.ServiceEdge{
		{
			CaseID:         "case-102",
			CallerService:  "srv-a",
			CalleeService:  "srv-b",
			EvidenceSource: "peer_url",
			CallCount:      10,
			ErrorCount:     0,
		},
	}
	anomalous := map[string]bool{
		"srv-isolated": true,
		"srv-a":        false,
		"srv-b":        false,
	}

	res := topology.ComputeTopology(explicitEdges, nil, anomalous)

	if !floatEquals(res.ServiceRootScore["srv-isolated"], 0.75) {
		t.Fatalf("expected srv-isolated score 0.75, got %v", res.ServiceRootScore["srv-isolated"])
	}
}

// TestNonAnomalousServices проверяет, что неаномальные сервисы получают оценку 0.0.
func TestNonAnomalousServices(t *testing.T) {
	explicitEdges := []contracts.ServiceEdge{
		{
			CaseID:         "case-103",
			CallerService:  "gateway",
			CalleeService:  "healthy-backend",
			EvidenceSource: "peer_url",
			CallCount:      50,
			ErrorCount:     0,
		},
	}
	anomalous := map[string]bool{
		"gateway":         true,
		"healthy-backend": false, // явно false
		// "unlisted-svc" вообще отсутствует в словаре
	}

	res := topology.ComputeTopology(explicitEdges, nil, anomalous)

	if !floatEquals(res.ServiceRootScore["healthy-backend"], 0.0) {
		t.Fatalf("expected healthy-backend score 0.0, got %v", res.ServiceRootScore["healthy-backend"])
	}
	// gateway вызывает healthy-backend, но healthy-backend не аномален, поэтому Danom(gateway) == 0, Uanom == 0 -> 0.75
	if !floatEquals(res.ServiceRootScore["gateway"], 0.75) {
		t.Fatalf("expected gateway score 0.75 (no anomalous callees), got %v", res.ServiceRootScore["gateway"])
	}
}

// TestEdgeAggregationAndDeduplication проверяет сложение call/error count и дедупликацию рёбер.
func TestEdgeAggregationAndDeduplication(t *testing.T) {
	explicitEdges := []contracts.ServiceEdge{
		{
			CaseID:         "case-200",
			CallerService:  "svc-a",
			CalleeService:  "svc-b",
			EvidenceSource: "peer_url",
			CallCount:      40,
			ErrorCount:     10,
		},
		{
			CaseID:         "case-200",
			CallerService:  "svc-b",
			CalleeService:  "svc-c",
			EvidenceSource: "peer_url",
			CallCount:      20,
			ErrorCount:     5,
		},
	}
	chainEdges := []contracts.ServiceEdge{
		{
			CaseID:         "case-200",
			CallerService:  "svc-a",
			CalleeService:  "svc-b",
			EvidenceSource: "trace_propagation",
			CallCount:      60,
			ErrorCount:     15,
		},
	}
	anomalous := map[string]bool{
		"svc-a": true,
		"svc-b": true,
		"svc-c": true,
	}

	res := topology.ComputeTopology(explicitEdges, chainEdges, anomalous)

	if len(res.Edges) != 2 {
		t.Fatalf("expected 2 aggregated edges, got %d", len(res.Edges))
	}

	// Ищем ребро svc-a -> svc-b
	var abEdge *contracts.ServiceEdge
	var bcEdge *contracts.ServiceEdge
	for i := range res.Edges {
		e := &res.Edges[i]
		if e.CallerService == "svc-a" && e.CalleeService == "svc-b" {
			abEdge = e
		}
		if e.CallerService == "svc-b" && e.CalleeService == "svc-c" {
			bcEdge = e
		}
	}

	if abEdge == nil {
		t.Fatal("missing edge svc-a -> svc-b")
	}
	if abEdge.CallCount != 100 {
		t.Fatalf("expected svc-a -> svc-b CallCount 100, got %d", abEdge.CallCount)
	}
	if abEdge.ErrorCount != 25 {
		t.Fatalf("expected svc-a -> svc-b ErrorCount 25, got %d", abEdge.ErrorCount)
	}
	if abEdge.EvidenceSource == "" {
		t.Fatal("expected non-empty EvidenceSource")
	}

	if bcEdge == nil {
		t.Fatal("missing edge svc-b -> svc-c")
	}
	if bcEdge.CallCount != 20 || bcEdge.ErrorCount != 5 {
		t.Fatalf("expected svc-b -> svc-c CallCount 20, ErrorCount 5, got call=%d err=%d", bcEdge.CallCount, bcEdge.ErrorCount)
	}
}

// TestDiamondTopology проверяет ромбовидный граф:
// gateway -> auth -> db
// gateway -> payments -> db
func TestDiamondTopology(t *testing.T) {
	explicitEdges := []contracts.ServiceEdge{
		{CallerService: "gateway", CalleeService: "auth"},
		{CallerService: "gateway", CalleeService: "payments"},
		{CallerService: "auth", CalleeService: "db"},
		{CallerService: "payments", CalleeService: "db"},
	}
	anomalous := map[string]bool{
		"gateway":  true,
		"auth":     true,
		"payments": true,
		"db":       true,
	}

	res := topology.ComputeTopology(explicitEdges, nil, anomalous)

	if !floatEquals(res.ServiceRootScore["db"], 1.0) {
		t.Fatalf("expected db score 1.0, got %v", res.ServiceRootScore["db"])
	}
	if !floatEquals(res.ServiceRootScore["auth"], 0.35) {
		t.Fatalf("expected auth score 0.35, got %v", res.ServiceRootScore["auth"])
	}
	if !floatEquals(res.ServiceRootScore["payments"], 0.35) {
		t.Fatalf("expected payments score 0.35, got %v", res.ServiceRootScore["payments"])
	}
	if !floatEquals(res.ServiceRootScore["gateway"], 0.15) {
		t.Fatalf("expected gateway score 0.15, got %v", res.ServiceRootScore["gateway"])
	}
}

// TestCyclicCascade проверяет цикл между двумя аномальными сервисами svc-1 <-> svc-2.
// Оба являются промежуточными звеньями (Danom > 0, Uanom > 0) -> 0.35.
func TestCyclicCascade(t *testing.T) {
	explicitEdges := []contracts.ServiceEdge{
		{CallerService: "svc-1", CalleeService: "svc-2"},
		{CallerService: "svc-2", CalleeService: "svc-1"},
	}
	anomalous := map[string]bool{
		"svc-1": true,
		"svc-2": true,
	}

	res := topology.ComputeTopology(explicitEdges, nil, anomalous)

	if !floatEquals(res.ServiceRootScore["svc-1"], 0.35) {
		t.Fatalf("expected svc-1 score 0.35 in cycle, got %v", res.ServiceRootScore["svc-1"])
	}
	if !floatEquals(res.ServiceRootScore["svc-2"], 0.35) {
		t.Fatalf("expected svc-2 score 0.35 in cycle, got %v", res.ServiceRootScore["svc-2"])
	}
}

// TestSelfLoopsAndInvalidEdges проверяет, что петли на самого себя и пустые имена сервисов не влияют на оценки.
func TestSelfLoopsAndInvalidEdges(t *testing.T) {
	explicitEdges := []contracts.ServiceEdge{
		{CallerService: "svc-self", CalleeService: "svc-self"},
		{CallerService: "", CalleeService: "svc-self"},
		{CallerService: "svc-self", CalleeService: ""},
	}
	anomalous := map[string]bool{
		"svc-self": true,
	}

	res := topology.ComputeTopology(explicitEdges, nil, anomalous)

	// svc-self не должен считать себя как зависимость: Danom=0, Uanom=0 -> 0.75
	if !floatEquals(res.ServiceRootScore["svc-self"], 0.75) {
		t.Fatalf("expected svc-self score 0.75, got %v", res.ServiceRootScore["svc-self"])
	}
	// Петли и пустые ребра не должны попасть в список рёбер
	if len(res.Edges) != 0 {
		t.Fatalf("expected 0 valid edges, got %d", len(res.Edges))
	}
}

// TestSanitizeFloatProtection проверяет защиту от некорректных значений и корректную сериализацию JSON.
func TestSanitizeFloatProtection(t *testing.T) {
	anomalous := map[string]bool{
		"node-a": true,
	}
	res := topology.ComputeTopology(nil, nil, anomalous)

	// Проверяем, что сериализация в JSON проходит без ошибок (нет NaN/Inf)
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("failed to marshal GraphResult: %v", err)
	}

	var parsed topology.GraphResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal GraphResult: %v", err)
	}

	if !floatEquals(parsed.ServiceRootScore["node-a"], 0.75) {
		t.Fatalf("expected parsed node-a score 0.75, got %v", parsed.ServiceRootScore["node-a"])
	}
}

// TestEmptyInputs проверяет работу при пустых/nil параметрах.
func TestEmptyInputs(t *testing.T) {
	res1 := topology.ComputeTopology(nil, nil, nil)
	if res1.Edges == nil || res1.ServiceRootScore == nil {
		t.Fatal("expected non-nil slices/maps for nil input")
	}
	if len(res1.Edges) != 0 || len(res1.ServiceRootScore) != 0 {
		t.Fatalf("expected empty result, got %d edges and %d scores", len(res1.Edges), len(res1.ServiceRootScore))
	}

	res2 := topology.ComputeTopology([]contracts.ServiceEdge{}, []contracts.ServiceEdge{}, map[string]bool{})
	if len(res2.Edges) != 0 || len(res2.ServiceRootScore) != 0 {
		t.Fatalf("expected empty result, got %d edges and %d scores", len(res2.Edges), len(res2.ServiceRootScore))
	}
}

// TestEdgeMetadataMergeAndSorting проверяет дозаполнение CaseID/EvidenceSource и сортировку рёбер.
func TestEdgeMetadataMergeAndSorting(t *testing.T) {
	explicitEdges := []contracts.ServiceEdge{
		{
			CallerService: "srv-x",
			CalleeService: "srv-z",
			// пустые CaseID и EvidenceSource
			CallCount: 1,
		},
		{
			CallerService: "srv-x",
			CalleeService: "srv-y",
			CallCount:     2,
		},
	}
	chainEdges := []contracts.ServiceEdge{
		{
			CaseID:         "case-later",
			CallerService:  "srv-x",
			CalleeService:  "srv-z",
			EvidenceSource: "trace_propagation",
			CallCount:      3,
		},
	}

	res := topology.ComputeTopology(explicitEdges, chainEdges, nil)
	if len(res.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(res.Edges))
	}
	// Сортировка: srv-x->srv-y, затем srv-x->srv-z
	if res.Edges[0].CalleeService != "srv-y" || res.Edges[1].CalleeService != "srv-z" {
		t.Fatalf("unexpected sorting: %s, %s", res.Edges[0].CalleeService, res.Edges[1].CalleeService)
	}
	if res.Edges[1].CaseID != "case-later" {
		t.Fatalf("expected merged CaseID 'case-later', got %q", res.Edges[1].CaseID)
	}
	if res.Edges[1].EvidenceSource != "trace_propagation" {
		t.Fatalf("expected merged EvidenceSource 'trace_propagation', got %q", res.Edges[1].EvidenceSource)
	}
	if res.Edges[1].CallCount != 4 {
		t.Fatalf("expected merged CallCount 4, got %d", res.Edges[1].CallCount)
	}
}

