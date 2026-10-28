package idgraph_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/rca-engine/internal/idgraph"
)

// Helper to create a base NormalizedEvent
func makeEvent(
	caseID, service, pod string,
	ts time.Time,
	recIdx uint32,
	level string,
	errClass string,
	templateID uint64,
	reqID, traceID, sessID, visID string,
	sourcePlace, msg string,
	httpStatus uint16,
) contracts.NormalizedEvent {
	return contracts.NormalizedEvent{
		CaseID:          caseID,
		EventID:         caseID + "-" + service + "-" + ts.Format(time.RFC3339Nano),
		Ts:              ts,
		Service:         service,
		Pod:             pod,
		Level:           level,
		ErrorClass:      errClass,
		TemplateID:      templateID,
		RequestID:       reqID,
		TraceID:         traceID,
		SessionID:       sessID,
		VisitorID:       visID,
		SourcePlace:     sourcePlace,
		Message:         msg,
		HTTPStatus:      httpStatus,
		SourceRecordIdx: recIdx,
	}
}

// 1. Blueprint Contract Scenario 1: Transitive gluing of 4 events
// access (req=R1) + interceptor (req=R1, trace=T1) + chat-session app (trace=T1, sess=S1) + chat-com err (sess=S1, OWN_DEFECT)
// Assert: all 4 in one chain, OriginEvent points to chat-com err, and its TemplateS1Scores == 1.0
func TestBuildChainsAndScores_TransitiveGluing4Events(t *testing.T) {
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	const errTemplateID uint64 = 10099

	events := []contracts.NormalizedEvent{
		makeEvent("case-1", "api-gateway", "gw-pod-1", t0.Add(0*time.Millisecond), 1,
			contracts.LevelInfo, contracts.ErrClassNone, 1001, "R1", "", "", "", "access.go:20", "HTTP GET /chat", 200),
		makeEvent("case-1", "api-gateway", "gw-pod-1", t0.Add(5*time.Millisecond), 2,
			contracts.LevelInfo, contracts.ErrClassNone, 1002, "R1", "T1", "", "", "interceptor.go:45", "trace propagated", 200),
		makeEvent("case-1", "chat-session", "cs-pod-1", t0.Add(20*time.Millisecond), 3,
			contracts.LevelInfo, contracts.ErrClassNone, 2001, "", "T1", "S1", "", "session_app.py:110", "session active", 200),
		makeEvent("case-1", "chat-com", "cc-pod-1", t0.Add(50*time.Millisecond), 4,
			contracts.LevelError, contracts.ErrClassOwnDefect, errTemplateID, "", "", "S1", "", "request_processing.py:173", "AttributeError: NoneType has no attribute 'get'", 500),
	}

	res := idgraph.BuildChainsAndScores(events, nil)

	if len(res.Chains) != 1 {
		t.Fatalf("expected 1 chain, got %d", len(res.Chains))
	}

	chain := res.Chains[0]
	if len(chain.Events) != 4 {
		t.Fatalf("expected 4 events in chain, got %d", len(chain.Events))
	}

	// Verify order by Ts
	for i := 1; i < len(chain.Events); i++ {
		if chain.Events[i].Ts.Before(chain.Events[i-1].Ts) {
			t.Fatalf("events in chain are not sorted chronologically: idx %d ts=%v before idx %d ts=%v",
				i, chain.Events[i].Ts, i-1, chain.Events[i-1].Ts)
		}
	}

	// Check OriginEvent
	if chain.OriginEvent == nil {
		t.Fatalf("expected non-nil OriginEvent")
	}
	if chain.OriginEvent.Service != "chat-com" {
		t.Errorf("expected OriginEvent service 'chat-com', got '%s'", chain.OriginEvent.Service)
	}
	if chain.OriginEvent.ErrorClass != contracts.ErrClassOwnDefect {
		t.Errorf("expected OriginEvent ErrorClass '%s', got '%s'", contracts.ErrClassOwnDefect, chain.OriginEvent.ErrorClass)
	}
	if chain.OriginEvent.TemplateID != errTemplateID {
		t.Errorf("expected OriginEvent TemplateID %d, got %d", errTemplateID, chain.OriginEvent.TemplateID)
	}

	// Check TemplateS1Scores == 1.0
	score, ok := res.TemplateS1Scores[errTemplateID]
	if !ok {
		t.Fatalf("expected TemplateS1Scores to contain template %d", errTemplateID)
	}
	if score != 1.0 {
		t.Errorf("expected TemplateS1Score == 1.0, got %f", score)
	}

	// Check DerivedEdges (gateway -> chat-session -> chat-com)
	if len(res.DerivedEdges) < 2 {
		t.Fatalf("expected at least 2 derived edges, got %d", len(res.DerivedEdges))
	}
	foundGWtoCS := false
	foundCStoCC := false
	for _, edge := range res.DerivedEdges {
		if edge.CallerService == "api-gateway" && edge.CalleeService == "chat-session" {
			foundGWtoCS = true
			if edge.CallCount != 1 {
				t.Errorf("expected CallCount 1 for api-gateway -> chat-session, got %d", edge.CallCount)
			}
		}
		if edge.CallerService == "chat-session" && edge.CalleeService == "chat-com" {
			foundCStoCC = true
			if edge.ErrorCount != 1 {
				t.Errorf("expected ErrorCount 1 for chat-session -> chat-com, got %d", edge.ErrorCount)
			}
		}
	}
	if !foundGWtoCS {
		t.Errorf("missing derived edge api-gateway -> chat-session")
	}
	if !foundCStoCC {
		t.Errorf("missing derived edge chat-session -> chat-com")
	}
}

// 2. Blueprint Contract Scenario 2: Pod proximity fallback (±50ms) without shared IDs
func TestBuildChainsAndScores_PodProximityFallback(t *testing.T) {
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

	events := []contracts.NormalizedEvent{
		// 3 error/warn events in pod-worker-a within 50ms of each other (0ms, 25ms, 50ms)
		makeEvent("case-2", "worker-srv", "pod-worker-a", t0.Add(0*time.Millisecond), 1,
			contracts.LevelWarn, contracts.ErrClassNone, 301, "", "", "", "", "worker.go:50", "high memory warning", 0),
		makeEvent("case-2", "worker-srv", "pod-worker-a", t0.Add(25*time.Millisecond), 2,
			contracts.LevelError, contracts.ErrClassResource, 302, "", "", "", "", "worker.go:88", "pool exhausted", 0),
		makeEvent("case-2", "worker-srv", "pod-worker-a", t0.Add(50*time.Millisecond), 3,
			contracts.LevelError, contracts.ErrClassResource, 303, "", "", "", "", "worker.go:95", "failed allocating buffer", 0),
		// 1 error in same pod but 200ms later (>50ms gap from 50ms) -> should NOT glue with previous 3
		makeEvent("case-2", "worker-srv", "pod-worker-a", t0.Add(250*time.Millisecond), 4,
			contracts.LevelError, contracts.ErrClassResource, 304, "", "", "", "", "worker.go:120", "late error", 0),
		// 1 error in DIFFERENT pod pod-worker-b at t0+20ms -> should NOT glue with pod-worker-a
		makeEvent("case-2", "worker-srv", "pod-worker-b", t0.Add(20*time.Millisecond), 5,
			contracts.LevelError, contracts.ErrClassResource, 305, "", "", "", "", "worker.go:88", "pool exhausted b", 0),
	}

	res := idgraph.BuildChainsAndScores(events, nil)

	// We expect:
	// - Chain 1: 3 events in pod-worker-a (0ms, 25ms, 50ms)
	// - Chain 2: 1 event in pod-worker-a (250ms)
	// - Chain 3: 1 event in pod-worker-b (20ms)
	if len(res.Chains) != 3 {
		t.Fatalf("expected 3 chains, got %d", len(res.Chains))
	}

	var gluedChain *idgraph.Chain
	for i := range res.Chains {
		if len(res.Chains[i].Events) == 3 {
			gluedChain = &res.Chains[i]
			break
		}
	}
	if gluedChain == nil {
		t.Fatalf("expected a glued chain with 3 events, but none found")
	}

	// Verify all 3 events are from pod-worker-a
	for _, ev := range gluedChain.Events {
		if ev.Pod != "pod-worker-a" {
			t.Errorf("expected pod 'pod-worker-a', got '%s'", ev.Pod)
		}
	}
	// Verify OriginEvent is the first error (302 at 25ms or warn 301)
	if gluedChain.OriginEvent == nil {
		t.Fatalf("expected non-nil OriginEvent for gluedChain")
	}
}

// 3. Blueprint Contract Scenario 3: Filtering out background traces (count > 30, span > 60s, consumer/fetched)
func TestBuildChainsAndScores_FilterBackgroundTraces(t *testing.T) {
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

	// Normal incident events (2 events)
	normalEvents := []contracts.NormalizedEvent{
		makeEvent("case-3", "api-gateway", "gw-1", t0.Add(10*time.Millisecond), 1,
			contracts.LevelInfo, contracts.ErrClassNone, 100, "req-inc-1", "trace-inc-1", "", "", "main.go:10", "ok", 200),
		makeEvent("case-3", "chat-com", "cc-1", t0.Add(20*time.Millisecond), 2,
			contracts.LevelError, contracts.ErrClassOwnDefect, 200, "req-inc-1", "trace-inc-1", "", "", "app.go:50", "fail", 500),
	}

	// 35 background events with trace_id="bg-nats-loop" (count > 30)
	var bgEvents []contracts.NormalizedEvent
	for i := 0; i < 35; i++ {
		bgEvents = append(bgEvents, makeEvent("case-3", "worker", "worker-pod",
			t0.Add(time.Duration(i*500)*time.Millisecond), uint32(100+i),
			contracts.LevelInfo, contracts.ErrClassNone, 999,
			"", "bg-nats-loop", "", "", "consumer.go:42", "polled batch", 200))
	}

	allEvents := append(normalEvents, bgEvents...)
	res := idgraph.BuildChainsAndScores(allEvents, nil)

	// Background trace should be filtered out. Only normal incident chain should be formed.
	if len(res.Chains) != 1 {
		t.Fatalf("expected exactly 1 chain (background trace filtered out), got %d chains", len(res.Chains))
	}
	if len(res.Chains[0].Events) != 2 {
		t.Fatalf("expected 2 events in incident chain, got %d", len(res.Chains[0].Events))
	}
	if res.Chains[0].Events[0].TraceID != "trace-inc-1" {
		t.Errorf("expected incident trace-inc-1, got %s", res.Chains[0].Events[0].TraceID)
	}

	// FindChainByKey with background trace ID must return empty/nil
	bgChain := idgraph.FindChainByKey(allEvents, nil, "bg-nats-loop")
	if len(bgChain) != 0 {
		t.Errorf("expected FindChainByKey for background trace to return empty, got %d events", len(bgChain))
	}

	// FindChainByKey with incident trace ID must return 2 events
	incChain := idgraph.FindChainByKey(allEvents, nil, "trace-inc-1")
	if len(incChain) != 2 {
		t.Errorf("expected FindChainByKey for incident trace to return 2 events, got %d", len(incChain))
	}

	// Also test span > 60s
	spanEvents := []contracts.NormalizedEvent{
		makeEvent("case-span", "worker", "pod-1", t0, 1,
			contracts.LevelInfo, contracts.ErrClassNone, 1, "", "trace-long-span", "", "", "poll.go:1", "tick 1", 200),
		makeEvent("case-span", "worker", "pod-1", t0.Add(65*time.Second), 2,
			contracts.LevelInfo, contracts.ErrClassNone, 1, "", "trace-long-span", "", "", "poll.go:1", "tick 2", 200),
	}
	resSpan := idgraph.BuildChainsAndScores(spanEvents, nil)
	if len(resSpan.Chains) != 0 {
		t.Errorf("expected trace with span > 60s to be filtered out, got %d chains", len(resSpan.Chains))
	}

	// Test ev.Message == "fetched" filter
	fetchedEvents := []contracts.NormalizedEvent{
		makeEvent("case-fetch", "worker", "pod-1", t0, 1,
			contracts.LevelInfo, contracts.ErrClassNone, 1, "", "trace-fetched", "", "", "handler.go:10", "fetched", 200),
		makeEvent("case-fetch", "worker", "pod-1", t0.Add(1*time.Second), 2,
			contracts.LevelInfo, contracts.ErrClassNone, 1, "", "trace-fetched", "", "", "handler.go:20", "process", 200),
	}
	resFetched := idgraph.BuildChainsAndScores(fetchedEvents, nil)
	if len(resFetched.Chains) != 0 {
		t.Errorf("expected trace with 'fetched' message to be filtered out, got %d chains", len(resFetched.Chains))
	}
}

// 4. Blueprint Contract Scenario 4: CANCELED error behavior
// - If chain has both CANCELED and other error: CANCELED is not chosen as OriginEvent, non-CANCELED error is chosen.
// - If chain has ONLY CANCELED: CANCELED is chosen as OriginEvent with weight 0.2.
func TestBuildChainsAndScores_CanceledErrorHandling(t *testing.T) {
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	const (
		tmplCanceled uint64 = 4001
		tmplDefect   uint64 = 4002
		tmplOnlyCanc uint64 = 4003
	)

	// Chain 1: CANCELED at t0, then OWN_DEFECT at t0+10ms
	chain1Events := []contracts.NormalizedEvent{
		makeEvent("case-4", "api-gateway", "gw-pod", t0, 1,
			contracts.LevelError, contracts.ErrClassCanceled, tmplCanceled, "R-1", "T-1", "", "", "gw.go:10", "context canceled", 499),
		makeEvent("case-4", "chat-com", "cc-pod", t0.Add(10*time.Millisecond), 2,
			contracts.LevelError, contracts.ErrClassOwnDefect, tmplDefect, "R-1", "T-1", "", "", "cc.go:20", "NPE", 500),
	}

	// Chain 2: ONLY CANCELED error
	chain2Events := []contracts.NormalizedEvent{
		makeEvent("case-4", "auth-srv", "auth-pod", t0.Add(50*time.Millisecond), 3,
			contracts.LevelError, contracts.ErrClassCanceled, tmplOnlyCanc, "R-2", "T-2", "", "", "auth.go:30", "context canceled", 499),
	}

	allEvents := append(chain1Events, chain2Events...)
	res := idgraph.BuildChainsAndScores(allEvents, nil)

	if len(res.Chains) != 2 {
		t.Fatalf("expected 2 chains, got %d", len(res.Chains))
	}

	// Find chain 1
	var c1, c2 *idgraph.Chain
	for i := range res.Chains {
		if res.Chains[i].ChainID == "T-1" || (len(res.Chains[i].Events) > 0 && res.Chains[i].Events[0].TraceID == "T-1") {
			c1 = &res.Chains[i]
		}
		if res.Chains[i].ChainID == "T-2" || (len(res.Chains[i].Events) > 0 && res.Chains[i].Events[0].TraceID == "T-2") {
			c2 = &res.Chains[i]
		}
	}

	if c1 == nil || c2 == nil {
		t.Fatalf("could not identify c1 and c2")
	}

	// In Chain 1: OriginEvent must be tmplDefect (OWN_DEFECT), NOT CANCELED!
	if c1.OriginEvent == nil {
		t.Fatalf("c1 OriginEvent is nil")
	}
	if c1.OriginEvent.TemplateID != tmplDefect {
		t.Errorf("expected c1 OriginEvent template %d, got %d (CANCELED was incorrectly chosen)",
			tmplDefect, c1.OriginEvent.TemplateID)
	}

	// In Chain 2: OriginEvent must be tmplOnlyCanc (since only CANCELED exists)
	if c2.OriginEvent == nil {
		t.Fatalf("c2 OriginEvent is nil")
	}
	if c2.OriginEvent.TemplateID != tmplOnlyCanc {
		t.Errorf("expected c2 OriginEvent template %d, got %d", tmplOnlyCanc, c2.OriginEvent.TemplateID)
	}

	// Total error chains = 2.
	// c1 weight = 1.0 -> score = 1.0 / 2.0 = 0.5.
	// c2 weight = 0.2 -> score = 0.2 / 2.0 = 0.1.
	if score, ok := res.TemplateS1Scores[tmplDefect]; !ok || math.Abs(score-0.5) > 1e-6 {
		t.Errorf("expected tmplDefect score 0.5, got %f (ok=%v)", score, ok)
	}
	if score, ok := res.TemplateS1Scores[tmplOnlyCanc]; !ok || math.Abs(score-0.1) > 1e-6 {
		t.Errorf("expected tmplOnlyCanc score 0.1, got %f (ok=%v)", score, ok)
	}
}

// 5. Zero division guard, SanitizeFloat, empty inputs
func TestBuildChainsAndScores_ZeroDivisionAndEmpty(t *testing.T) {
	// Empty inputs
	resEmpty := idgraph.BuildChainsAndScores(nil, nil)
	if len(resEmpty.Chains) != 0 || len(resEmpty.TemplateS1Scores) != 0 || len(resEmpty.DerivedEdges) != 0 {
		t.Errorf("expected all empty slices/maps on nil input, got %+v", resEmpty)
	}

	// Only INFO events (N_err_chains == 0)
	t0 := time.Now()
	infoEvents := []contracts.NormalizedEvent{
		makeEvent("case-info", "srv-1", "pod-1", t0, 1, contracts.LevelInfo, contracts.ErrClassNone, 10, "req-1", "tr-1", "", "", "a.go:1", "ok 1", 200),
		makeEvent("case-info", "srv-2", "pod-2", t0.Add(5*time.Millisecond), 2, contracts.LevelInfo, contracts.ErrClassNone, 20, "req-1", "tr-1", "", "", "b.go:1", "ok 2", 200),
	}
	resInfo := idgraph.BuildChainsAndScores(infoEvents, nil)
	if len(resInfo.Chains) != 1 {
		t.Fatalf("expected 1 chain for info events, got %d", len(resInfo.Chains))
	}
	if resInfo.Chains[0].OriginEvent != nil {
		t.Errorf("expected nil OriginEvent when no errors, got %+v", resInfo.Chains[0].OriginEvent)
	}
	for tmpl, score := range resInfo.TemplateS1Scores {
		if math.IsNaN(score) || math.IsInf(score, 0) || score != 0.0 {
			t.Errorf("invalid score for template %d: %f", tmpl, score)
		}
	}
}

// 6. IdentityEdge linking events without shared IDs
func TestBuildChainsAndScores_IdentityEdgesLinking(t *testing.T) {
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

	// Event 1 has ONLY req-X
	// Event 2 has ONLY sess-Y
	events := []contracts.NormalizedEvent{
		makeEvent("case-edge", "frontend", "fe-pod", t0, 1,
			contracts.LevelInfo, contracts.ErrClassNone, 101, "req-X", "", "", "", "fe.go:10", "start", 200),
		makeEvent("case-edge", "backend", "be-pod", t0.Add(15*time.Millisecond), 2,
			contracts.LevelError, contracts.ErrClassOwnDefect, 102, "", "", "sess-Y", "", "be.go:20", "err", 500),
	}

	edges := []contracts.IdentityEdge{
		{
			CaseID:    "case-edge",
			Ts:        t0.Add(5 * time.Millisecond),
			Service:   "frontend",
			LeftType:  "request_id",
			LeftVal:   "req-X",
			RightType: "session_id",
			RightVal:  "sess-Y",
		},
	}

	res := idgraph.BuildChainsAndScores(events, edges)

	if len(res.Chains) != 1 {
		t.Fatalf("expected 1 chain joined by IdentityEdge, got %d", len(res.Chains))
	}
	if len(res.Chains[0].Events) != 2 {
		t.Errorf("expected 2 events in chain, got %d", len(res.Chains[0].Events))
	}
	if res.Chains[0].OriginEvent == nil || res.Chains[0].OriginEvent.TemplateID != 102 {
		t.Errorf("expected OriginEvent with TemplateID 102")
	}
	if res.TemplateS1Scores[102] != 1.0 {
		t.Errorf("expected TemplateS1Scores[102] == 1.0, got %f", res.TemplateS1Scores[102])
	}
}

// 7. FindChainByKey tests
func TestFindChainByKey(t *testing.T) {
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

	events := []contracts.NormalizedEvent{
		makeEvent("case-f", "gw", "p1", t0, 1, contracts.LevelInfo, contracts.ErrClassNone, 1, "R-100", "T-100", "S-100", "V-100", "", "msg", 200),
		makeEvent("case-f", "api", "p2", t0.Add(10*time.Millisecond), 2, contracts.LevelInfo, contracts.ErrClassNone, 2, "R-100", "T-100", "S-100", "V-100", "", "msg", 200),
	}

	// Find by request_id
	chainReq := idgraph.FindChainByKey(events, nil, "R-100")
	if len(chainReq) != 2 {
		t.Errorf("expected 2 events for R-100, got %d", len(chainReq))
	}

	// Find by trace_id with prefix
	chainTr := idgraph.FindChainByKey(events, nil, "tr:T-100")
	if len(chainTr) != 2 {
		t.Errorf("expected 2 events for tr:T-100, got %d", len(chainTr))
	}

	// Find by session_id
	chainSess := idgraph.FindChainByKey(events, nil, "sess:S-100")
	if len(chainSess) != 2 {
		t.Errorf("expected 2 events for sess:S-100, got %d", len(chainSess))
	}

	// Find by visitor_id
	chainVis := idgraph.FindChainByKey(events, nil, "vis:V-100")
	if len(chainVis) != 2 {
		t.Errorf("expected 2 events for vis:V-100, got %d", len(chainVis))
	}

	// Find non-existing key
	chainNone := idgraph.FindChainByKey(events, nil, "non-existent")
	if len(chainNone) != 0 {
		t.Errorf("expected 0 events for non-existent key, got %d", len(chainNone))
	}

	// Find with empty key
	chainEmpty := idgraph.FindChainByKey(events, nil, "")
	if len(chainEmpty) != 0 {
		t.Errorf("expected 0 events for empty key, got %d", len(chainEmpty))
	}
}

// 8. JSON Serialization / Deserialization
func TestJSONSerialization(t *testing.T) {
	chain := idgraph.Chain{
		ChainID: "chain-test-1",
		Events: []contracts.NormalizedEvent{
			{EventID: "ev-1", Level: "INFO"},
		},
		OriginEvent: &contracts.NormalizedEvent{
			EventID: "ev-1", Level: "ERROR", ErrorClass: "OWN_DEFECT",
		},
	}

	data, err := json.Marshal(chain)
	if err != nil {
		t.Fatalf("failed to marshal Chain: %v", err)
	}

	var parsed idgraph.Chain
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal Chain: %v", err)
	}

	if parsed.ChainID != chain.ChainID {
		t.Errorf("expected ChainID %s, got %s", chain.ChainID, parsed.ChainID)
	}
	if parsed.OriginEvent == nil || parsed.OriginEvent.ErrorClass != "OWN_DEFECT" {
		t.Errorf("expected unmarshaled OriginEvent to match")
	}
}

// 9. Comprehensive Edge cases and branch tests
func TestEdgeCasesAndBranches(t *testing.T) {
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

	// Test SeverityNum, HTTPStatus, and ErrorClass branches
	evSeverity := contracts.NormalizedEvent{
		CaseID:          "case-edge-2",
		EventID:         "ev-sev",
		Ts:              t0,
		Service:         "srv-a",
		Pod:             "pod-a",
		SeverityNum:     contracts.SeverityError,
		Level:           contracts.LevelInfo, // Level is info, but SeverityNum is 40
		SourceRecordIdx: 1,
	}
	evWarn := contracts.NormalizedEvent{
		CaseID:          "case-edge-2",
		EventID:         "ev-warn",
		Ts:              t0.Add(10 * time.Millisecond),
		Service:         "srv-b",
		Pod:             "pod-a",
		SeverityNum:     contracts.SeverityWarn,
		HTTPStatus:      404,
		SourceRecordIdx: 2,
	}
	// Pod proximity should glue evSeverity and evWarn in pod-a (diff 10ms <= 50ms)
	resSev := idgraph.BuildChainsAndScores([]contracts.NormalizedEvent{evSeverity, evWarn}, nil)
	if len(resSev.Chains) != 1 {
		t.Errorf("expected 1 chain from pod proximity, got %d", len(resSev.Chains))
	}
	if len(resSev.DerivedEdges) != 1 {
		t.Errorf("expected 1 derived edge srv-a -> srv-b, got %d", len(resSev.DerivedEdges))
	}

	// Test various IdentityEdge types
	idEdges := []contracts.IdentityEdge{
		{LeftType: "tr", LeftVal: "tr-1", RightType: "tr:", RightVal: "tr-2"},
		{LeftType: "trace_id", LeftVal: "tr-2", RightType: "trace_id", RightVal: "tr-3"},
		{LeftType: "sess", LeftVal: "s-1", RightType: "sess:", RightVal: "s-2"},
		{LeftType: "session_id", LeftVal: "s-2", RightType: "session_id", RightVal: "s-3"},
		{LeftType: "vis", LeftVal: "v-1", RightType: "vis:", RightVal: "v-2"},
		{LeftType: "visitor_id", LeftVal: "v-2", RightType: "visitor_id", RightVal: "v-3"},
		{LeftType: "custom", LeftVal: "c-1", RightType: "custom", RightVal: "c-2"},
		{LeftType: "req", LeftVal: "req-1", RightType: "req:", RightVal: "req-2"},
		{LeftType: "", LeftVal: "req:r-a", RightType: "", RightVal: "req:r-b"},
		{LeftType: "req", LeftVal: "", RightType: "req", RightVal: "req-3"}, // empty LeftVal skipped
	}
	eventsEdges := []contracts.NormalizedEvent{
		makeEvent("case-e", "srv", "p", t0, 1, contracts.LevelInfo, contracts.ErrClassNone, 1, "req-1", "", "", "", "", "1", 200),
		makeEvent("case-e", "srv", "p", t0.Add(5*time.Millisecond), 2, contracts.LevelInfo, contracts.ErrClassNone, 1, "req-2", "", "", "", "", "2", 200),
	}
	resEdgeTypes := idgraph.BuildChainsAndScores(eventsEdges, idEdges)
	if len(resEdgeTypes.Chains) != 1 {
		t.Errorf("expected 1 chain for req-1 and req-2 joined via IdentityEdge, got %d", len(resEdgeTypes.Chains))
	}

	// Test IdentityEdge with background trace being skipped
	bgEvents := make([]contracts.NormalizedEvent, 32)
	for i := 0; i < 32; i++ {
		bgEvents[i] = makeEvent("case-bg", "srv", "p", t0.Add(time.Duration(i)*time.Second), uint32(i),
			contracts.LevelInfo, contracts.ErrClassNone, 1, "", "tr-bg-x", "", "", "", "bg", 200)
	}
	bgEdges := []contracts.IdentityEdge{
		{LeftType: "trace_id", LeftVal: "tr-bg-x", RightType: "req", RightVal: "req-valid"},
		{LeftType: "req", LeftVal: "req-valid-2", RightType: "trace_id", RightVal: "tr-bg-x"},
	}
	resBGEdge := idgraph.BuildChainsAndScores(bgEvents, bgEdges)
	if len(resBGEdge.Chains) != 0 {
		t.Errorf("expected 0 chains for background events, got %d", len(resBGEdge.Chains))
	}

	// Test ChainID priority: SessionID only, VisitorID only, Pod only, Root fallback
	evSessOnly := makeEvent("case-id", "srv", "p", t0, 1, contracts.LevelInfo, contracts.ErrClassNone, 1, "", "", "sess-unique", "", "", "ok", 200)
	resSess := idgraph.BuildChainsAndScores([]contracts.NormalizedEvent{evSessOnly}, nil)
	if len(resSess.Chains) != 1 || resSess.Chains[0].ChainID != "sess-unique" {
		t.Errorf("expected ChainID 'sess-unique', got '%s'", resSess.Chains[0].ChainID)
	}

	evVisOnly := makeEvent("case-id", "srv", "p", t0, 1, contracts.LevelInfo, contracts.ErrClassNone, 1, "", "", "", "vis-unique", "", "ok", 200)
	resVis := idgraph.BuildChainsAndScores([]contracts.NormalizedEvent{evVisOnly}, nil)
	if len(resVis.Chains) != 1 || resVis.Chains[0].ChainID != "vis-unique" {
		t.Errorf("expected ChainID 'vis-unique', got '%s'", resVis.Chains[0].ChainID)
	}

	evPodOnly := makeEvent("case-id", "srv", "p-only", t0, 1, contracts.LevelError, contracts.ErrClassOwnDefect, 1, "", "", "", "", "", "err", 500)
	resPod := idgraph.BuildChainsAndScores([]contracts.NormalizedEvent{evPodOnly}, nil)
	if len(resPod.Chains) != 1 || !strings.HasPrefix(resPod.Chains[0].ChainID, "pod:p-only:") {
		t.Errorf("expected ChainID starting with 'pod:p-only:', got '%s'", resPod.Chains[0].ChainID)
	}

	// Test event sorting with identical Ts but different SourceRecordIdx
	evSort1 := makeEvent("case-s", "srv", "p", t0, 20, contracts.LevelInfo, contracts.ErrClassNone, 1, "R-sort", "", "", "", "", "second", 200)
	evSort2 := makeEvent("case-s", "srv", "p", t0, 10, contracts.LevelInfo, contracts.ErrClassNone, 1, "R-sort", "", "", "", "", "first", 200)
	resSort := idgraph.BuildChainsAndScores([]contracts.NormalizedEvent{evSort1, evSort2}, nil)
	if len(resSort.Chains) != 1 || resSort.Chains[0].Events[0].SourceRecordIdx != 10 {
		t.Errorf("expected first event to have SourceRecordIdx 10, got %d", resSort.Chains[0].Events[0].SourceRecordIdx)
	}

	// FindChainByKey with colon prefix but empty val
	resEmptyVal := idgraph.FindChainByKey(eventsEdges, nil, "req:")
	if len(resEmptyVal) != 0 {
		t.Errorf("expected 0 events for empty val key, got %d", len(resEmptyVal))
	}
}

