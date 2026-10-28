package ranker

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/rca-engine/internal/idgraph"
	"github.com/local/logs-analyzer/services/rca-engine/internal/timeseries"
	"github.com/local/logs-analyzer/services/rca-engine/internal/topology"
)

type CandidateInput struct {
	Template        contracts.TemplateRecord
	Stats           timeseries.TemplateStats
	DominantPod     string
	PodSkewRatio    float64
	DominantRoute   string
	DominantVersion string
	PeerService     string
	VersionChanged  bool
}

type rankedCandidate struct {
	cand       CandidateInput
	signals    contracts.SignalBreakdown
	confidence float64
	role       string
}

// RankHypotheses объединяет сигналы S1..S8, назначает роли ROOT_CAUSE / PROPAGATION / SYMPTOM / NOISE и формирует Explanation.
func RankHypotheses(
	candidates []CandidateInput,
	idRes idgraph.Result,
	topoRes topology.GraphResult,
	llRes timeseries.LeadLagResult,
) []contracts.Hypothesis {
	if len(candidates) == 0 {
		return []contracts.Hypothesis{}
	}

	peerDefectMap := make(map[string]bool)
	for _, c := range candidates {
		if c.Template.ErrorClass == contracts.ErrClassOwnDefect || c.Template.ErrorClass == contracts.ErrClassResource {
			if c.Template.Service != "" {
				peerDefectMap[c.Template.Service] = true
			}
		}
	}

	var nonNoise []rankedCandidate
	var noiseList []rankedCandidate

	for _, c := range candidates {
		s1 := idRes.TemplateS1Scores[c.Template.TemplateID]
		s1 = contracts.SanitizeFloat(s1)

		s2 := contracts.SanitizeFloat(c.Stats.OnsetScore)

		s3 := llRes.ScoreByTemplate[c.Template.TemplateID]
		s3 = contracts.SanitizeFloat(s3)

		s4 := contracts.SanitizeFloat(c.Stats.NoveltyScore)

		var s5 float64
		switch c.Template.ErrorClass {
		case contracts.ErrClassOwnDefect:
			s5 = 1.0
		case contracts.ErrClassResource:
			s5 = 0.95
		case contracts.ErrClassDeadline, contracts.ErrClassUnavailable, contracts.ErrClassPeer5xx:
			if c.PeerService != "" && peerDefectMap[c.PeerService] {
				s5 = 0.25
			} else {
				s5 = 0.75
			}
		case contracts.ErrClassPeer4xx:
			s5 = 0.15
		case contracts.ErrClassCanceled:
			s5 = 0.10
		case contracts.ErrClassNone, "":
			s5 = 0.0
		default:
			s5 = 0.50
		}
		s5 = contracts.SanitizeFloat(s5)

		s6 := topoRes.ServiceRootScore[c.Template.Service]
		s6 = contracts.SanitizeFloat(s6)

		var s7 float64
		if c.Template.SourcePlace != "" {
			s7 += 0.5
		}
		if c.PodSkewRatio >= 0.8 || c.DominantRoute != "" {
			s7 += 0.5
		}
		s7 = contracts.SanitizeFloat(s7)

		var s8 float64
		if c.VersionChanged {
			s8 = 1.0
		}
		s8 = contracts.SanitizeFloat(s8)

		signals := contracts.SignalBreakdown{
			TraceChainScore:   s1,
			OnsetScore:        s2,
			LeadLagScore:      s3,
			NoveltyScore:      s4,
			CausalDirScore:    s5,
			GraphRootScore:    s6,
			LocalizationScore: s7,
			ChangeCorrScore:   s8,
		}
		signals.Sanitize()

		if c.Stats.IsConstantNoise {
			noiseList = append(noiseList, rankedCandidate{
				cand:       c,
				signals:    signals,
				confidence: 0.05,
				role:       contracts.RoleNoise,
			})
		} else {
			score := 0.22*s1 +
				0.20*s2 +
				0.12*s3 +
				0.14*s4 +
				0.16*s5 +
				0.10*s6 +
				0.06*s8
			conf := contracts.SanitizeFloat(math.Min(0.99, math.Max(0.05, score)))
			nonNoise = append(nonNoise, rankedCandidate{
				cand:       c,
				signals:    signals,
				confidence: conf,
			})
		}
	}

	sort.SliceStable(nonNoise, func(i, j int) bool {
		if nonNoise[i].confidence != nonNoise[j].confidence {
			return nonNoise[i].confidence > nonNoise[j].confidence
		}
		return nonNoise[i].cand.Template.TemplateID < nonNoise[j].cand.Template.TemplateID
	})

	rootCauseIdx := -1
	for i := range nonNoise {
		if nonNoise[i].cand.Template.ErrorClass != contracts.ErrClassCanceled {
			rootCauseIdx = i
			break
		}
	}

	var orderedNonNoise []rankedCandidate
	if rootCauseIdx != -1 {
		rc := nonNoise[rootCauseIdx]
		rc.role = contracts.RoleRootCause
		orderedNonNoise = append(orderedNonNoise, rc)

		for i := range nonNoise {
			if i == rootCauseIdx {
				continue
			}
			item := nonNoise[i]
			isSymptom := item.cand.Template.ErrorClass == contracts.ErrClassCanceled ||
				(item.signals.GraphRootScore <= 0.2 && is5xxOrGateway(item.cand))
			if isSymptom {
				item.role = contracts.RoleSymptom
			} else {
				item.role = contracts.RolePropagation
			}
			orderedNonNoise = append(orderedNonNoise, item)
		}
	} else {
		for i := range nonNoise {
			item := nonNoise[i]
			item.role = contracts.RoleSymptom
			orderedNonNoise = append(orderedNonNoise, item)
		}
	}

	sort.SliceStable(noiseList, func(i, j int) bool {
		return noiseList[i].cand.Template.TemplateID < noiseList[j].cand.Template.TemplateID
	})

	finalRanked := append(orderedNonNoise, noiseList...)
	hypotheses := make([]contracts.Hypothesis, 0, len(finalRanked))

	for rankIdx, r := range finalRanked {
		rank := rankIdx + 1
		links := llRes.LinksByTemplate[r.cand.Template.TemplateID]
		if links == nil {
			links = []contracts.LeadLagLink{}
		}

		hyp := contracts.Hypothesis{
			ID:                 fmt.Sprintf("H-%02d", rank),
			Rank:               rank,
			Role:               r.role,
			Confidence:         r.confidence,
			Service:            r.cand.Template.Service,
			TemplateID:         r.cand.Template.TemplateID,
			TemplateText:       r.cand.Template.TemplateText,
			ErrorClass:         r.cand.Template.ErrorClass,
			SourcePlace:        r.cand.Template.SourcePlace,
			DominantPod:        r.cand.DominantPod,
			PodSkewRatio:       contracts.SanitizeFloat(r.cand.PodSkewRatio),
			DominantRoute:      r.cand.DominantRoute,
			DominantVersion:    r.cand.DominantVersion,
			OnsetTime:          r.cand.Stats.OnsetTime,
			BaselineRatePerMin: contracts.SanitizeFloat(r.cand.Stats.BaselineRatePerMin),
			IncidentRatePerMin: contracts.SanitizeFloat(r.cand.Stats.IncidentRatePerMin),
			GrowthRatio:        contracts.SanitizeFloat(r.cand.Stats.GrowthRatio),
			PoissonPValue:      contracts.SanitizeFloat(r.cand.Stats.PoissonPValue),
			IsConstantNoise:    r.cand.Stats.IsConstantNoise,
			LeadLagPeers:       links,
			Signals:            r.signals,
			EvidenceIDs:        []string{},
		}
		hyp.Explanation = generateExplanation(&hyp)
		hyp.Sanitize()
		hypotheses = append(hypotheses, hyp)
	}

	return hypotheses
}

func is5xxOrGateway(c CandidateInput) bool {
	if c.Template.ErrorClass == contracts.ErrClassPeer5xx {
		return true
	}
	text := c.Template.TemplateText + " " + c.Template.SampleRedacted
	if strings.Contains(text, "500") || strings.Contains(text, "502") || strings.Contains(text, "503") || strings.Contains(text, "504") {
		return true
	}
	return false
}

func generateExplanation(h *contracts.Hypothesis) string {
	switch h.Role {
	case contracts.RoleRootCause:
		return fmt.Sprintf("Primary root cause in service '%s' (error class: %s, template %d): %q. High onset and causal scores indicate failure origination.",
			h.Service, h.ErrorClass, h.TemplateID, h.TemplateText)
	case contracts.RolePropagation:
		return fmt.Sprintf("Propagation failure in service '%s' (error class: %s): %q cascading from upstream/peer service.",
			h.Service, h.ErrorClass, h.TemplateText)
	case contracts.RoleSymptom:
		return fmt.Sprintf("Edge/gateway symptom in service '%s' (error class: %s): %q caused by downstream degradation.",
			h.Service, h.ErrorClass, h.TemplateText)
	default:
		return fmt.Sprintf("Constant background noise in service '%s' (template %d): %q with stable frequency across timeline.",
			h.Service, h.TemplateID, h.TemplateText)
	}
}
