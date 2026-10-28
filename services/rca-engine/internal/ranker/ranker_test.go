package ranker

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/rca-engine/internal/idgraph"
	"github.com/local/logs-analyzer/services/rca-engine/internal/timeseries"
	"github.com/local/logs-analyzer/services/rca-engine/internal/topology"
)

func TestRankHypotheses_Cascade4Candidates(t *testing.T) {
	// Candidate 1: chat-com OwnDefect
	cand1 := CandidateInput{
		Template: contracts.TemplateRecord{
			CaseID:       "case-1",
			Service:      "chat-com",
			TemplateID:   1001,
			TemplateText: "AttributeError: 'NoneType' object has no attribute 'get'",
			Level:        contracts.LevelError,
			ErrorClass:   contracts.ErrClassOwnDefect,
			SourcePlace:  "request_processing.py:173",
		},
		Stats: timeseries.TemplateStats{
			Service:         "chat-com",
			TemplateID:      1001,
			OnsetScore:      1.0,
			NoveltyScore:    1.0,
			IsConstantNoise: false,
			OnsetTime:       time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC),
		},
		DominantPod:    "chat-com-pod-1",
		PodSkewRatio:   0.85,
		VersionChanged: false,
	}

	// Candidate 2: chat-session Deadline (PeerService = chat-com)
	cand2 := CandidateInput{
		Template: contracts.TemplateRecord{
			CaseID:       "case-1",
			Service:      "chat-session",
			TemplateID:   1002,
			TemplateText: "context deadline exceeded while calling chat-com",
			Level:        contracts.LevelError,
			ErrorClass:   contracts.ErrClassDeadline,
			SourcePlace:  "client.go:173",
		},
		Stats: timeseries.TemplateStats{
			Service:         "chat-session",
			TemplateID:      1002,
			OnsetScore:      0.8,
			NoveltyScore:    0.9,
			IsConstantNoise: false,
			OnsetTime:       time.Date(2026, 10, 28, 12, 0, 5, 0, time.UTC),
		},
		DominantPod:  "chat-session-pod-2",
		PodSkewRatio: 0.5,
		PeerService:  "chat-com",
	}

	// Candidate 3: api-gateway Canceled
	cand3 := CandidateInput{
		Template: contracts.TemplateRecord{
			CaseID:       "case-1",
			Service:      "api-gateway",
			TemplateID:   1003,
			TemplateText: "context canceled",
			Level:        contracts.LevelError,
			ErrorClass:   contracts.ErrClassCanceled,
			SourcePlace:  "proxy.go:88",
		},
		Stats: timeseries.TemplateStats{
			Service:         "api-gateway",
			TemplateID:      1003,
			OnsetScore:      0.7,
			NoveltyScore:    0.8,
			IsConstantNoise: false,
			OnsetTime:       time.Date(2026, 10, 28, 12, 0, 10, 0, time.UTC),
		},
		DominantPod:  "api-gateway-pod-1",
		PodSkewRatio: 0.3,
	}

	// Candidate 4: chat-com cache:98 constant noise
	cand4 := CandidateInput{
		Template: contracts.TemplateRecord{
			CaseID:       "case-1",
			Service:      "chat-com",
			TemplateID:   1004,
			TemplateText: "cache:98 miss key=user_token",
			Level:        contracts.LevelInfo,
			ErrorClass:   contracts.ErrClassNone,
			SourcePlace:  "cache.py:98",
		},
		Stats: timeseries.TemplateStats{
			Service:         "chat-com",
			TemplateID:      1004,
			OnsetScore:      0.0,
			NoveltyScore:    0.0,
			IsConstantNoise: true,
		},
		DominantPod:  "chat-com-pod-1",
		PodSkewRatio: 0.33,
	}

	candidates := []CandidateInput{cand1, cand2, cand3, cand4}

	idRes := idgraph.Result{
		TemplateS1Scores: map[uint64]float64{
			1001: 0.9,
			1002: 0.7,
			1003: 0.3,
			1004: 0.0,
		},
	}

	topoRes := topology.GraphResult{
		ServiceRootScore: map[string]float64{
			"chat-com":     1.0,
			"chat-session": 0.35,
			"api-gateway":  0.15,
		},
	}

	llRes := timeseries.LeadLagResult{
		ScoreByTemplate: map[uint64]float64{
			1001: 0.85,
			1002: 0.2,
			1003: 0.0,
			1004: 0.0,
		},
		LinksByTemplate: map[uint64][]contracts.LeadLagLink{
			1001: {
				{
					PeerService:  "chat-session",
					PeerTemplate: 1002,
					LagSeconds:   5,
					CorrelationR: 0.85,
				},
			},
		},
	}

	hyps := RankHypotheses(candidates, idRes, topoRes, llRes)
	if len(hyps) != 4 {
		t.Fatalf("expected 4 hypotheses, got %d", len(hyps))
	}

	// Check Rank 1: chat-com OwnDefect -> ROOT_CAUSE
	if hyps[0].Rank != 1 {
		t.Errorf("expected Rank 1 for hyp[0], got %d", hyps[0].Rank)
	}
	if hyps[0].Role != contracts.RoleRootCause {
		t.Errorf("expected Role ROOT_CAUSE for hyp[0], got %s", hyps[0].Role)
	}
	if hyps[0].TemplateID != 1001 {
		t.Errorf("expected TemplateID 1001 for hyp[0], got %d", hyps[0].TemplateID)
	}
	if hyps[0].Service != "chat-com" {
		t.Errorf("expected Service chat-com, got %s", hyps[0].Service)
	}
	if hyps[0].Confidence <= 0.5 {
		t.Errorf("expected high confidence for root cause, got %f", hyps[0].Confidence)
	}

	// Check hyp map by template
	byTpl := make(map[uint64]contracts.Hypothesis)
	for _, h := range hyps {
		byTpl[h.TemplateID] = h
	}

	// Check chat-session Deadline -> PROPAGATION
	hSession, ok := byTpl[1002]
	if !ok {
		t.Fatalf("missing hypothesis for 1002")
	}
	if hSession.Role != contracts.RolePropagation {
		t.Errorf("expected Role PROPAGATION for chat-session, got %s", hSession.Role)
	}
	if hSession.Signals.CausalDirScore != 0.25 {
		t.Errorf("expected CausalDirScore 0.25 for chat-session (peer has own defect), got %f", hSession.Signals.CausalDirScore)
	}

	// Check api-gateway Canceled -> SYMPTOM
	hGateway, ok := byTpl[1003]
	if !ok {
		t.Fatalf("missing hypothesis for 1003")
	}
	if hGateway.Role != contracts.RoleSymptom {
		t.Errorf("expected Role SYMPTOM for api-gateway, got %s", hGateway.Role)
	}
	if hGateway.Signals.CausalDirScore != 0.10 {
		t.Errorf("expected CausalDirScore 0.10 for canceled error, got %f", hGateway.Signals.CausalDirScore)
	}

	// Check chat-com cache:98 -> NOISE
	hNoise, ok := byTpl[1004]
	if !ok {
		t.Fatalf("missing hypothesis for 1004")
	}
	if hNoise.Role != contracts.RoleNoise {
		t.Errorf("expected Role NOISE for cache:98, got %s", hNoise.Role)
	}
	if hNoise.Confidence != 0.05 {
		t.Errorf("expected Confidence 0.05 for noise, got %f", hNoise.Confidence)
	}
}

func TestRankHypotheses_S5_CausalDirScoreVariants(t *testing.T) {
	cases := []struct {
		errClass    string
		peerService string
		hasPeerDef  bool
		expectedS5  float64
	}{
		{contracts.ErrClassOwnDefect, "", false, 1.0},
		{contracts.ErrClassResource, "", false, 0.95},
		{contracts.ErrClassDeadline, "peer-svc", true, 0.25},
		{contracts.ErrClassDeadline, "peer-svc", false, 0.75},
		{contracts.ErrClassUnavailable, "peer-svc", true, 0.25},
		{contracts.ErrClassUnavailable, "peer-svc", false, 0.75},
		{contracts.ErrClassPeer5xx, "peer-svc", true, 0.25},
		{contracts.ErrClassPeer5xx, "peer-svc", false, 0.75},
		{contracts.ErrClassPeer4xx, "", false, 0.15},
		{contracts.ErrClassCanceled, "", false, 0.10},
		{"CUSTOM_UNKNOWN", "", false, 0.50},
		{contracts.ErrClassNone, "", false, 0.0},
	}

	for _, tc := range cases {
		cands := []CandidateInput{
			{
				Template: contracts.TemplateRecord{
					Service:    "test-svc",
					TemplateID: 1,
					ErrorClass: tc.errClass,
				},
				Stats: timeseries.TemplateStats{
					TemplateID: 1,
				},
				PeerService: tc.peerService,
			},
		}
		if tc.hasPeerDef {
			cands = append(cands, CandidateInput{
				Template: contracts.TemplateRecord{
					Service:    tc.peerService,
					TemplateID: 2,
					ErrorClass: contracts.ErrClassOwnDefect,
				},
				Stats: timeseries.TemplateStats{
					TemplateID: 2,
				},
			})
		}

		hyps := RankHypotheses(cands, idgraph.Result{}, topology.GraphResult{}, timeseries.LeadLagResult{})
		if len(hyps) == 0 {
			t.Fatalf("no hypotheses returned for %s", tc.errClass)
		}
		var found *contracts.Hypothesis
		for i := range hyps {
			if hyps[i].TemplateID == 1 {
				found = &hyps[i]
				break
			}
		}
		if found == nil {
			t.Fatalf("template 1 not found for %s", tc.errClass)
		}
		if math.Abs(found.Signals.CausalDirScore-tc.expectedS5) > 1e-6 {
			t.Errorf("errClass %s (hasPeerDef=%v): expected S5=%f, got %f", tc.errClass, tc.hasPeerDef, tc.expectedS5, found.Signals.CausalDirScore)
		}
	}
}

func TestRankHypotheses_S7_LocalizationScore(t *testing.T) {
	testCases := []struct {
		sourcePlace   string
		podSkewRatio  float64
		dominantRoute string
		expectedS7    float64
	}{
		{"", 0.1, "", 0.0},
		{"file.go:12", 0.1, "", 0.5},
		{"", 0.85, "", 0.5},
		{"", 0.1, "/api/v1/chat", 0.5},
		{"file.go:12", 0.85, "", 1.0},
		{"file.go:12", 0.1, "/api/v1/chat", 1.0},
		{"file.go:12", 0.9, "/api/v1/chat", 1.0},
	}

	for _, tc := range testCases {
		cands := []CandidateInput{
			{
				Template: contracts.TemplateRecord{
					Service:     "svc",
					TemplateID:  10,
					ErrorClass:  contracts.ErrClassOwnDefect,
					SourcePlace: tc.sourcePlace,
				},
				Stats: timeseries.TemplateStats{
					TemplateID: 10,
				},
				PodSkewRatio:  tc.podSkewRatio,
				DominantRoute: tc.dominantRoute,
			},
		}

		hyps := RankHypotheses(cands, idgraph.Result{}, topology.GraphResult{}, timeseries.LeadLagResult{})
		if len(hyps) != 1 {
			t.Fatalf("expected 1 hypothesis, got %d", len(hyps))
		}
		if math.Abs(hyps[0].Signals.LocalizationScore-tc.expectedS7) > 1e-6 {
			t.Errorf("tc %+v: expected S7=%f, got %f", tc, tc.expectedS7, hyps[0].Signals.LocalizationScore)
		}
	}
}

func TestRankHypotheses_S8_ChangeCorrScore(t *testing.T) {
	cands := []CandidateInput{
		{
			Template: contracts.TemplateRecord{
				Service:    "svc-a",
				TemplateID: 1,
				ErrorClass: contracts.ErrClassOwnDefect,
			},
			Stats: timeseries.TemplateStats{
				TemplateID: 1,
			},
			VersionChanged: true,
		},
		{
			Template: contracts.TemplateRecord{
				Service:    "svc-b",
				TemplateID: 2,
				ErrorClass: contracts.ErrClassOwnDefect,
			},
			Stats: timeseries.TemplateStats{
				TemplateID: 2,
			},
			VersionChanged: false,
		},
	}

	hyps := RankHypotheses(cands, idgraph.Result{}, topology.GraphResult{}, timeseries.LeadLagResult{})
	for _, h := range hyps {
		if h.TemplateID == 1 && h.Signals.ChangeCorrScore != 1.0 {
			t.Errorf("expected S8=1.0 for VersionChanged=true, got %f", h.Signals.ChangeCorrScore)
		}
		if h.TemplateID == 2 && h.Signals.ChangeCorrScore != 0.0 {
			t.Errorf("expected S8=0.0 for VersionChanged=false, got %f", h.Signals.ChangeCorrScore)
		}
	}
}

func TestRankHypotheses_ConfidenceFormula(t *testing.T) {
	cands := []CandidateInput{
		{
			Template: contracts.TemplateRecord{
				Service:     "billing",
				TemplateID:  501,
				ErrorClass:  contracts.ErrClassOwnDefect, // S5 = 1.0
				SourcePlace: "billing.go:50",            // S7 = 1.0
			},
			Stats: timeseries.TemplateStats{
				TemplateID:   501,
				OnsetScore:   0.8, // S2 = 0.8
				NoveltyScore: 0.9, // S4 = 0.9
			},
			PodSkewRatio:   0.9,  // S7 += 0.5 -> S7 = 1.0
			VersionChanged: true, // S8 = 1.0
		},
	}

	idRes := idgraph.Result{
		TemplateS1Scores: map[uint64]float64{
			501: 0.7, // S1 = 0.7
		},
	}
	topoRes := topology.GraphResult{
		ServiceRootScore: map[string]float64{
			"billing": 0.6, // S6 = 0.6
		},
	}
	llRes := timeseries.LeadLagResult{
		ScoreByTemplate: map[uint64]float64{
			501: 0.5, // S3 = 0.5
		},
	}

	hyps := RankHypotheses(cands, idRes, topoRes, llRes)
	if len(hyps) != 1 {
		t.Fatalf("expected 1 hyp, got %d", len(hyps))
	}

	expectedScore := 0.22*0.7 +
		0.20*0.8 +
		0.12*0.5 +
		0.14*0.9 +
		0.16*1.0 +
		0.10*0.6 +
		0.06*1.0

	expectedScore = math.Min(0.99, math.Max(0.05, expectedScore))
	if math.Abs(hyps[0].Confidence-expectedScore) > 1e-4 {
		t.Errorf("expected Confidence %f, got %f", expectedScore, hyps[0].Confidence)
	}
}

func TestRankHypotheses_EdgeCasesAndSafety(t *testing.T) {
	// 1. Empty input
	hyps := RankHypotheses(nil, idgraph.Result{}, topology.GraphResult{}, timeseries.LeadLagResult{})
	if len(hyps) != 0 {
		t.Errorf("expected empty slice for nil candidates, got %d", len(hyps))
	}

	// 2. All candidates are ErrClassCanceled
	canceledCands := []CandidateInput{
		{
			Template: contracts.TemplateRecord{
				Service:    "svc-1",
				TemplateID: 1,
				ErrorClass: contracts.ErrClassCanceled,
			},
			Stats: timeseries.TemplateStats{TemplateID: 1},
		},
		{
			Template: contracts.TemplateRecord{
				Service:    "svc-2",
				TemplateID: 2,
				ErrorClass: contracts.ErrClassCanceled,
			},
			Stats: timeseries.TemplateStats{TemplateID: 2},
		},
	}
	hypsCanceled := RankHypotheses(canceledCands, idgraph.Result{}, topology.GraphResult{}, timeseries.LeadLagResult{})
	for _, h := range hypsCanceled {
		if h.Role != contracts.RoleSymptom {
			t.Errorf("expected Role SYMPTOM for canceled candidate, got %s", h.Role)
		}
	}

	// 3. Entry service symptom with GraphRootScore <= 0.2 and 5xx error
	entrySymptomCands := []CandidateInput{
		{
			Template: contracts.TemplateRecord{
				Service:      "gateway",
				TemplateID:   301,
				TemplateText: "HTTP 502 Bad Gateway while proxying to backend",
				ErrorClass:   contracts.ErrClassPeer5xx,
			},
			Stats: timeseries.TemplateStats{TemplateID: 301, OnsetScore: 0.5},
		},
		{
			Template: contracts.TemplateRecord{
				Service:    "backend",
				TemplateID: 302,
				ErrorClass: contracts.ErrClassOwnDefect,
			},
			Stats: timeseries.TemplateStats{TemplateID: 302, OnsetScore: 0.9},
		},
	}
	topo := topology.GraphResult{
		ServiceRootScore: map[string]float64{
			"gateway": 0.15,
			"backend": 1.0,
		},
	}
	hypsEntry := RankHypotheses(entrySymptomCands, idgraph.Result{}, topo, timeseries.LeadLagResult{})
	for _, h := range hypsEntry {
		if h.TemplateID == 301 && h.Role != contracts.RoleSymptom {
			t.Errorf("expected gateway 502 to have Role SYMPTOM, got %s", h.Role)
		}
		if h.TemplateID == 302 && h.Role != contracts.RoleRootCause {
			t.Errorf("expected backend to have Role ROOT_CAUSE, got %s", h.Role)
		}
	}

	// 4. NaN / Inf inputs and JSON serialization safety
	nanCands := []CandidateInput{
		{
			Template: contracts.TemplateRecord{
				Service:    "nan-svc",
				TemplateID: 999,
				ErrorClass: contracts.ErrClassOwnDefect,
			},
			Stats: timeseries.TemplateStats{
				TemplateID:   999,
				OnsetScore:   math.NaN(),
				NoveltyScore: math.Inf(1),
			},
			PodSkewRatio: math.Inf(-1),
		},
	}
	hypsNaN := RankHypotheses(nanCands, idgraph.Result{TemplateS1Scores: map[uint64]float64{999: math.NaN()}}, topology.GraphResult{}, timeseries.LeadLagResult{})
	if len(hypsNaN) != 1 {
		t.Fatalf("expected 1 hypothesis for nan cand, got %d", len(hypsNaN))
	}
	// Verify JSON marshal doesn't fail
	data, err := json.Marshal(hypsNaN[0])
	if err != nil {
		t.Fatalf("failed to marshal hypothesis with NaN protection: %v", err)
	}
	var parsed contracts.Hypothesis
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal hypothesis: %v", err)
	}
	if math.IsNaN(parsed.Confidence) || math.IsInf(parsed.Confidence, 0) {
		t.Errorf("parsed confidence is NaN or Inf")
	}
}
