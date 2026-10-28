package idgraph

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
)

type Chain struct {
	ChainID     string                      `json:"chain_id"`
	Events      []contracts.NormalizedEvent `json:"events"`
	OriginEvent *contracts.NormalizedEvent  `json:"origin_event,omitempty"`
}

type Result struct {
	Chains           []Chain
	TemplateS1Scores map[uint64]float64 // key: TemplateID -> TraceChainScore [0..1]
	DerivedEdges     []contracts.ServiceEdge
}

type traceMeta struct {
	count                int
	minTs                time.Time
	maxTs                time.Time
	hasConsumerOrFetched bool
}

type unionFind struct {
	parent map[string]string
	rank   map[string]int
}

func newUnionFind() *unionFind {
	return &unionFind{
		parent: make(map[string]string),
		rank:   make(map[string]int),
	}
}

func (uf *unionFind) find(x string) string {
	p, ok := uf.parent[x]
	if !ok {
		uf.parent[x] = x
		return x
	}
	if p != x {
		uf.parent[x] = uf.find(p)
	}
	return uf.parent[x]
}

func (uf *unionFind) union(x, y string) {
	rx := uf.find(x)
	ry := uf.find(y)
	if rx == ry {
		return
	}
	if uf.rank[rx] < uf.rank[ry] {
		uf.parent[rx] = ry
	} else if uf.rank[rx] > uf.rank[ry] {
		uf.parent[ry] = rx
	} else {
		uf.parent[ry] = rx
		uf.rank[rx]++
	}
}

func checkConsumerOrFetched(ev contracts.NormalizedEvent) bool {
	if strings.Contains(strings.ToLower(ev.SourcePlace), "consumer") {
		return true
	}
	if ev.Message == "fetched" || strings.Contains(strings.ToLower(ev.Message), "fetched") {
		return true
	}
	return false
}

func isBackgroundEvent(ev contracts.NormalizedEvent, bgTraces map[string]bool) bool {
	if ev.TraceID != "" && bgTraces[ev.TraceID] {
		if ev.RequestID == "" && ev.SessionID == "" && ev.VisitorID == "" {
			return true
		}
	}
	return false
}

func isErrOrWarn(ev contracts.NormalizedEvent) bool {
	if ev.Level == contracts.LevelError || ev.Level == contracts.LevelWarn {
		return true
	}
	if ev.SeverityNum >= contracts.SeverityWarn {
		return true
	}
	if ev.HTTPStatus >= 400 {
		return true
	}
	if ev.ErrorClass != "" && ev.ErrorClass != contracts.ErrClassNone {
		return true
	}
	return false
}

func isError(ev contracts.NormalizedEvent) bool {
	if ev.Level == contracts.LevelError {
		return true
	}
	if ev.SeverityNum >= contracts.SeverityError {
		return true
	}
	if ev.HTTPStatus >= 500 {
		return true
	}
	if ev.ErrorClass != "" && ev.ErrorClass != contracts.ErrClassNone {
		return true
	}
	return false
}

func normalizeEdgeKey(typ, val string) string {
	if val == "" {
		return ""
	}
	switch strings.ToLower(typ) {
	case "request_id", "req", "req:":
		return "req:" + val
	case "trace_id", "tr", "tr:":
		return "tr:" + val
	case "session_id", "sess", "sess:":
		return "sess:" + val
	case "visitor_id", "vis", "vis:":
		return "vis:" + val
	default:
		if strings.HasPrefix(val, "req:") || strings.HasPrefix(val, "tr:") ||
			strings.HasPrefix(val, "sess:") || strings.HasPrefix(val, "vis:") {
			return val
		}
		if typ != "" {
			return typ + ":" + val
		}
		return val
	}
}

// BuildChainsAndScores строит сквозные цепочки из событий и рёбер идентификаторов,
// вычисляет S1 (TraceChainScore) и выводит межсервисные переходы внутри цепочек.
func BuildChainsAndScores(
	events []contracts.NormalizedEvent,
	edges []contracts.IdentityEdge,
) Result {
	if len(events) == 0 {
		return Result{
			Chains:           make([]Chain, 0),
			TemplateS1Scores: make(map[uint64]float64),
			DerivedEdges:     make([]contracts.ServiceEdge, 0),
		}
	}

	// 1. Фильтр фоновых циклов (isBackgroundTrace)
	traceMetaMap := make(map[string]*traceMeta)
	for _, ev := range events {
		if ev.TraceID == "" {
			continue
		}
		m, ok := traceMetaMap[ev.TraceID]
		if !ok {
			traceMetaMap[ev.TraceID] = &traceMeta{
				count:                1,
				minTs:                ev.Ts,
				maxTs:                ev.Ts,
				hasConsumerOrFetched: checkConsumerOrFetched(ev),
			}
		} else {
			m.count++
			if ev.Ts.Before(m.minTs) {
				m.minTs = ev.Ts
			}
			if ev.Ts.After(m.maxTs) {
				m.maxTs = ev.Ts
			}
			if checkConsumerOrFetched(ev) {
				m.hasConsumerOrFetched = true
			}
		}
	}

	bgTraces := make(map[string]bool)
	for tid, m := range traceMetaMap {
		span := m.maxTs.Sub(m.minTs)
		if m.count > 30 || span > 60*time.Second || m.hasConsumerOrFetched {
			bgTraces[tid] = true
		}
	}

	// 2. Union-Find по узлам идентификаторов
	uf := newUnionFind()
	for i, ev := range events {
		if isBackgroundEvent(ev, bgTraces) {
			continue
		}
		evNode := "ev:" + strconv.Itoa(i)
		uf.find(evNode)

		if ev.RequestID != "" {
			uf.union(evNode, "req:"+ev.RequestID)
		}
		if ev.TraceID != "" && !bgTraces[ev.TraceID] {
			uf.union(evNode, "tr:"+ev.TraceID)
		}
		if ev.SessionID != "" {
			uf.union(evNode, "sess:"+ev.SessionID)
		}
		if ev.VisitorID != "" {
			uf.union(evNode, "vis:"+ev.VisitorID)
		}
	}

	for _, edge := range edges {
		k1 := normalizeEdgeKey(edge.LeftType, edge.LeftVal)
		k2 := normalizeEdgeKey(edge.RightType, edge.RightVal)
		if k1 == "" || k2 == "" {
			continue
		}
		if strings.HasPrefix(k1, "tr:") && bgTraces[strings.TrimPrefix(k1, "tr:")] {
			continue
		}
		if strings.HasPrefix(k2, "tr:") && bgTraces[strings.TrimPrefix(k2, "tr:")] {
			continue
		}
		uf.union(k1, k2)
	}

	// 3. Fallback по близости внутри pod (±50 мс)
	podErrEvents := make(map[string][]int)
	for i, ev := range events {
		if isBackgroundEvent(ev, bgTraces) {
			continue
		}
		if ev.Pod != "" && isErrOrWarn(ev) {
			podErrEvents[ev.Pod] = append(podErrEvents[ev.Pod], i)
		}
	}

	for _, indices := range podErrEvents {
		if len(indices) < 2 {
			continue
		}
		sort.SliceStable(indices, func(a, b int) bool {
			return events[indices[a]].Ts.Before(events[indices[b]].Ts)
		})
		for k := 0; k < len(indices)-1; k++ {
			idx1 := indices[k]
			idx2 := indices[k+1]
			diff := events[idx2].Ts.Sub(events[idx1].Ts)
			if diff < 0 {
				diff = -diff
			}
			if diff <= 50*time.Millisecond {
				uf.union("ev:"+strconv.Itoa(idx1), "ev:"+strconv.Itoa(idx2))
			}
		}
	}

	// 4. Сборка цепочек событий
	chainMap := make(map[string][]contracts.NormalizedEvent)
	for i, ev := range events {
		if isBackgroundEvent(ev, bgTraces) {
			continue
		}
		root := uf.find("ev:" + strconv.Itoa(i))
		chainMap[root] = append(chainMap[root], ev)
	}

	chains := make([]Chain, 0, len(chainMap))
	originWeights := make(map[uint64]float64)
	errChainsCount := 0

	edgeMap := make(map[string]*contracts.ServiceEdge)

	// Детерминированный порядок ключей chainMap
	roots := make([]string, 0, len(chainMap))
	for root := range chainMap {
		roots = append(roots, root)
	}
	sort.Strings(roots)

	for _, root := range roots {
		evList := chainMap[root]
		if len(evList) == 0 {
			continue
		}

		// Сортировка событий в цепочке по (Ts, SourceRecordIdx)
		sort.SliceStable(evList, func(i, j int) bool {
			if evList[i].Ts.Equal(evList[j].Ts) {
				return evList[i].SourceRecordIdx < evList[j].SourceRecordIdx
			}
			return evList[i].Ts.Before(evList[j].Ts)
		})

		// Определение ChainID
		chainID := ""
		for _, ev := range evList {
			if ev.TraceID != "" && !bgTraces[ev.TraceID] {
				chainID = ev.TraceID
				break
			}
		}
		if chainID == "" {
			for _, ev := range evList {
				if ev.RequestID != "" {
					chainID = ev.RequestID
					break
				}
			}
		}
		if chainID == "" {
			for _, ev := range evList {
				if ev.SessionID != "" {
					chainID = ev.SessionID
					break
				}
			}
		}
		if chainID == "" {
			for _, ev := range evList {
				if ev.VisitorID != "" {
					chainID = ev.VisitorID
					break
				}
			}
		}
		if chainID == "" {
			if len(evList) > 0 && evList[0].Pod != "" {
				chainID = fmt.Sprintf("pod:%s:%d", evList[0].Pod, evList[0].Ts.UnixMilli())
			} else {
				chainID = root
			}
		}

		// 4. Определение OriginEvent и расчёт TemplateS1Scores
		hasError := false
		for _, ev := range evList {
			if isError(ev) {
				hasError = true
				break
			}
		}

		var originEvent *contracts.NormalizedEvent
		if hasError {
			errChainsCount++

			// Найти первую ошибку в цепочке, у которой ErrorClass != contracts.ErrClassCanceled
			firstNonCanceledIdx := -1
			firstCanceledIdx := -1

			for idx, ev := range evList {
				if isError(ev) {
					if ev.ErrorClass != contracts.ErrClassCanceled {
						firstNonCanceledIdx = idx
						break
					} else if firstCanceledIdx == -1 {
						firstCanceledIdx = idx
					}
				}
			}

			if firstNonCanceledIdx != -1 {
				originCopy := evList[firstNonCanceledIdx]
				originEvent = &originCopy
				originWeights[originEvent.TemplateID] += 1.0
			} else if firstCanceledIdx != -1 {
				// если в цепочке есть только ErrClassCanceled, взять её с коэффициентом 0.2
				originCopy := evList[firstCanceledIdx]
				originEvent = &originCopy
				originWeights[originEvent.TemplateID] += 0.2
			}
		}

		// Вывод межсервисных переходов внутри цепочки
		var prevService string
		var prevCaseID string
		for _, ev := range evList {
			if ev.Service == "" {
				continue
			}
			if prevService != "" && prevService != ev.Service {
				caller := prevService
				callee := ev.Service
				cID := ev.CaseID
				if cID == "" {
					cID = prevCaseID
				}
				key := caller + "->" + callee
				edge, exists := edgeMap[key]
				if !exists {
					edge = &contracts.ServiceEdge{
						CaseID:         cID,
						CallerService:  caller,
						CalleeService:  callee,
						EvidenceSource: "trace_propagation",
						CallCount:      0,
						ErrorCount:     0,
					}
					edgeMap[key] = edge
				}
				edge.CallCount++
				if isError(ev) {
					edge.ErrorCount++
				}
			}
			prevService = ev.Service
			prevCaseID = ev.CaseID
		}

		chains = append(chains, Chain{
			ChainID:     chainID,
			Events:      evList,
			OriginEvent: originEvent,
		})
	}

	// Сортировка chains для детерминированности
	sort.SliceStable(chains, func(i, j int) bool {
		if len(chains[i].Events) == 0 || len(chains[j].Events) == 0 {
			return chains[i].ChainID < chains[j].ChainID
		}
		if chains[i].Events[0].Ts.Equal(chains[j].Events[0].Ts) {
			return chains[i].ChainID < chains[j].ChainID
		}
		return chains[i].Events[0].Ts.Before(chains[j].Events[0].Ts)
	})

	// Расчёт TemplateS1Scores с защитой от деления на 0
	templateS1Scores := make(map[uint64]float64)
	if errChainsCount > 0 {
		for tmplID, weight := range originWeights {
			score := weight / float64(errChainsCount)
			templateS1Scores[tmplID] = contracts.SanitizeFloat(score)
		}
	}

	// Формирование DerivedEdges
	derivedEdges := make([]contracts.ServiceEdge, 0, len(edgeMap))
	for _, edge := range edgeMap {
		derivedEdges = append(derivedEdges, *edge)
	}
	sort.SliceStable(derivedEdges, func(i, j int) bool {
		if derivedEdges[i].CallerService == derivedEdges[j].CallerService {
			return derivedEdges[i].CalleeService < derivedEdges[j].CalleeService
		}
		return derivedEdges[i].CallerService < derivedEdges[j].CallerService
	})

	return Result{
		Chains:           chains,
		TemplateS1Scores: templateS1Scores,
		DerivedEdges:     derivedEdges,
	}
}

// FindChainByKey возвращает упорядоченную по времени цепочку для заданного trace_id / request_id / session_id
func FindChainByKey(events []contracts.NormalizedEvent, edges []contracts.IdentityEdge, key string) []contracts.NormalizedEvent {
	if key == "" || len(events) == 0 {
		return make([]contracts.NormalizedEvent, 0)
	}

	val := key
	if strings.HasPrefix(key, "req:") || strings.HasPrefix(key, "tr:") ||
		strings.HasPrefix(key, "sess:") || strings.HasPrefix(key, "vis:") {
		idx := strings.Index(key, ":")
		if idx != -1 {
			val = key[idx+1:]
		}
	}
	if val == "" {
		return make([]contracts.NormalizedEvent, 0)
	}

	res := BuildChainsAndScores(events, edges)
	for _, ch := range res.Chains {
		if ch.ChainID == key || ch.ChainID == val {
			return ch.Events
		}
		for _, ev := range ch.Events {
			if ev.TraceID == val || ev.RequestID == val || ev.SessionID == val || ev.VisitorID == val {
				return ch.Events
			}
		}
	}

	return make([]contracts.NormalizedEvent, 0)
}
