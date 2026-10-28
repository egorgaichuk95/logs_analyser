package topology

import (
	"sort"

	"github.com/local/logs-analyzer/pkg/contracts"
)

type GraphResult struct {
	Edges            []contracts.ServiceEdge
	ServiceRootScore map[string]float64 // key: service -> GraphRootScore [0..1]
}

// ComputeTopology агрегирует рёбра вызовов и рассчитывает S6 (GraphRootScore) для каждого сервиса.
// anomalousServices — множество сервисов, в которых обнаружены ошибки в окне инцидента.
func ComputeTopology(
	explicitEdges []contracts.ServiceEdge,
	chainEdges []contracts.ServiceEdge,
	anomalousServices map[string]bool,
) GraphResult {
	edgeMap := make(map[string]*contracts.ServiceEdge)

	mergeEdge := func(e contracts.ServiceEdge) {
		if e.CallerService == "" || e.CalleeService == "" || e.CallerService == e.CalleeService {
			return
		}
		key := e.CallerService + "->" + e.CalleeService
		existing, ok := edgeMap[key]
		if !ok {
			edgeMap[key] = &contracts.ServiceEdge{
				CaseID:         e.CaseID,
				CallerService:  e.CallerService,
				CalleeService:  e.CalleeService,
				EvidenceSource: e.EvidenceSource,
				CallCount:      e.CallCount,
				ErrorCount:     e.ErrorCount,
			}
			return
		}

		existing.CallCount += e.CallCount
		existing.ErrorCount += e.ErrorCount
		if existing.CaseID == "" && e.CaseID != "" {
			existing.CaseID = e.CaseID
		}
		if existing.EvidenceSource == "" && e.EvidenceSource != "" {
			existing.EvidenceSource = e.EvidenceSource
		}
	}

	for _, e := range explicitEdges {
		mergeEdge(e)
	}
	for _, e := range chainEdges {
		mergeEdge(e)
	}

	allServices := make(map[string]struct{})
	for svc := range anomalousServices {
		if svc != "" {
			allServices[svc] = struct{}{}
		}
	}
	for _, e := range edgeMap {
		allServices[e.CallerService] = struct{}{}
		allServices[e.CalleeService] = struct{}{}
	}

	anomCallees := make(map[string]map[string]struct{})
	anomCallers := make(map[string]map[string]struct{})

	for _, e := range edgeMap {
		caller := e.CallerService
		callee := e.CalleeService

		if anomalousServices != nil && anomalousServices[callee] {
			if anomCallees[caller] == nil {
				anomCallees[caller] = make(map[string]struct{})
			}
			anomCallees[caller][callee] = struct{}{}
		}

		if anomalousServices != nil && anomalousServices[caller] {
			if anomCallers[callee] == nil {
				anomCallers[callee] = make(map[string]struct{})
			}
			anomCallers[callee][caller] = struct{}{}
		}
	}

	scores := make(map[string]float64)
	for svc := range allServices {
		if anomalousServices == nil || !anomalousServices[svc] {
			scores[svc] = contracts.SanitizeFloat(0.0)
			continue
		}

		dAnom := len(anomCallees[svc])
		uAnom := len(anomCallers[svc])

		var score float64
		switch {
		case dAnom == 0 && uAnom > 0:
			// Дно каскада: сервис никого из аномальных не вызывает, но от него зависят сбоящие соседи (ROOT_CAUSE)
			score = 1.0
		case dAnom == 0 && uAnom == 0:
			// Изолированный аномальный сервис
			score = 0.75
		case dAnom > 0 && uAnom > 0:
			// Промежуточное звено каскада (PROPAGATION)
			score = 0.35
		case dAnom > 0 && uAnom == 0:
			// Входной шлюз, вызывающий сбоящий downstream (SYMPTOM)
			score = 0.15
		}

		scores[svc] = contracts.SanitizeFloat(score)
	}

	edges := make([]contracts.ServiceEdge, 0, len(edgeMap))
	for _, e := range edgeMap {
		edges = append(edges, *e)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].CallerService == edges[j].CallerService {
			return edges[i].CalleeService < edges[j].CalleeService
		}
		return edges[i].CallerService < edges[j].CallerService
	})

	return GraphResult{
		Edges:            edges,
		ServiceRootScore: scores,
	}
}
