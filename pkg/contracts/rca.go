package contracts

import "time"

const (
	RoleRootCause   = "ROOT_CAUSE"
	RolePropagation = "PROPAGATION"
	RoleSymptom     = "SYMPTOM"
	RoleNoise       = "NOISE"
)

const (
	EvidenceKindRawLog       = "raw_log"
	EvidenceKindTraceChain   = "trace_chain"
	EvidenceKindStatTest     = "stat_test"
	EvidenceKindClockSkew    = "clock_skew"
	EvidenceKindTopologyEdge = "topology_edge"
)

type SignalBreakdown struct {
	TraceChainScore   float64 `json:"trace_chain_score"`   // S1
	OnsetScore        float64 `json:"onset_score"`         // S2
	LeadLagScore      float64 `json:"lead_lag_score"`      // S3
	NoveltyScore      float64 `json:"novelty_score"`       // S4
	CausalDirScore    float64 `json:"causal_dir_score"`    // S5
	GraphRootScore    float64 `json:"graph_root_score"`    // S6
	LocalizationScore float64 `json:"localization_score"`  // S7
	ChangeCorrScore   float64 `json:"change_corr_score"`   // S8
}

type LeadLagLink struct {
	PeerService  string  `json:"peer_service"`
	PeerTemplate uint64  `json:"peer_template"`
	LagSeconds   int     `json:"lag_seconds"` // >0 означает, что текущий ряд опережает Peer
	CorrelationR float64 `json:"correlation_r"`
}

type ClockSkewFinding struct {
	Service     string `json:"service"`
	Pod         string `json:"pod"`
	SkewMs      int64  `json:"skew_ms"`
	SampleRawTs string `json:"sample_raw_ts"`
	SampleAppTs string `json:"sample_app_ts"`
}

type Hypothesis struct {
	ID                 string          `json:"id"`
	Rank               int             `json:"rank"`
	Role               string          `json:"role"` // ROOT_CAUSE, PROPAGATION, SYMPTOM, NOISE
	Confidence         float64         `json:"confidence"`
	Service            string          `json:"service"`
	TemplateID         uint64          `json:"template_id"`
	TemplateText       string          `json:"template_text"`
	ErrorClass         string          `json:"error_class"`
	SourcePlace        string          `json:"source_place"`
	DominantPod        string          `json:"dominant_pod"`
	PodSkewRatio       float64         `json:"pod_skew_ratio"` // доля ошибок на DominantPod (0..1)
	DominantRoute      string          `json:"dominant_route"`
	DominantVersion    string          `json:"dominant_version"`
	OnsetTime          time.Time       `json:"onset_time"`
	BaselineRatePerMin float64         `json:"baseline_rate_per_min"`
	IncidentRatePerMin float64         `json:"incident_rate_per_min"`
	GrowthRatio        float64         `json:"growth_ratio"`
	PoissonPValue      float64         `json:"poisson_p_value"`
	IsConstantNoise    bool            `json:"is_constant_noise"`
	RecurringCaseIDs   []string        `json:"recurring_case_ids,omitempty"`
	LeadLagPeers       []LeadLagLink   `json:"lead_lag_peers"`
	Signals            SignalBreakdown `json:"signals"`
	Explanation        string          `json:"explanation"`
	EvidenceIDs        []string        `json:"evidence_ids"`
}

type EvidenceItem struct {
	ID              string         `json:"id"`   // "E-01", "E-02"...
	Kind            string         `json:"kind"` // "raw_log", "trace_chain", "stat_test", "clock_skew", "topology_edge"
	Service         string         `json:"service"`
	Summary         string         `json:"summary"`
	Timestamp       time.Time      `json:"timestamp"`
	SourceFile      string         `json:"source_file"`
	SourceRecordIdx uint32         `json:"source_record_idx"`
	SHA256          string         `json:"sha256"`
	RedactedLine    string         `json:"redacted_line"`
	LogQLSnippet    string         `json:"logql_snippet"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

// Sanitize защищает поля SignalBreakdown от NaN и Inf
func (s *SignalBreakdown) Sanitize() {
	s.TraceChainScore = SanitizeFloat(s.TraceChainScore)
	s.OnsetScore = SanitizeFloat(s.OnsetScore)
	s.LeadLagScore = SanitizeFloat(s.LeadLagScore)
	s.NoveltyScore = SanitizeFloat(s.NoveltyScore)
	s.CausalDirScore = SanitizeFloat(s.CausalDirScore)
	s.GraphRootScore = SanitizeFloat(s.GraphRootScore)
	s.LocalizationScore = SanitizeFloat(s.LocalizationScore)
	s.ChangeCorrScore = SanitizeFloat(s.ChangeCorrScore)
}

// Sanitize защищает поля Hypothesis и вложенных структур от NaN и Inf
func (h *Hypothesis) Sanitize() {
	h.Confidence = SanitizeFloat(h.Confidence)
	h.PodSkewRatio = SanitizeFloat(h.PodSkewRatio)
	h.BaselineRatePerMin = SanitizeFloat(h.BaselineRatePerMin)
	h.IncidentRatePerMin = SanitizeFloat(h.IncidentRatePerMin)
	h.GrowthRatio = SanitizeFloat(h.GrowthRatio)
	h.PoissonPValue = SanitizeFloat(h.PoissonPValue)
	for i := range h.LeadLagPeers {
		h.LeadLagPeers[i].CorrelationR = SanitizeFloat(h.LeadLagPeers[i].CorrelationR)
	}
	h.Signals.Sanitize()
}
