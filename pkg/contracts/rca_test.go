package contracts

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestSignalBreakdown_JSONAndSanitize(t *testing.T) {
	sb := SignalBreakdown{
		TraceChainScore:   0.85,
		OnsetScore:        0.90,
		LeadLagScore:      0.75,
		NoveltyScore:      1.0,
		CausalDirScore:    0.65,
		GraphRootScore:    0.95,
		LocalizationScore: 0.80,
		ChangeCorrScore:   0.70,
	}

	data, err := json.Marshal(sb)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed SignalBreakdown
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed != sb {
		t.Errorf("expected %+v, got %+v", sb, parsed)
	}

	// Test NaN / Inf sanitization
	sbNaN := SignalBreakdown{
		TraceChainScore:   math.NaN(),
		OnsetScore:        math.Inf(1),
		LeadLagScore:      math.Inf(-1),
		NoveltyScore:      math.NaN(),
		CausalDirScore:    math.NaN(),
		GraphRootScore:    math.Inf(1),
		LocalizationScore: math.NaN(),
		ChangeCorrScore:   math.NaN(),
	}
	sbNaN.Sanitize()
	if sbNaN.TraceChainScore != 0.0 || sbNaN.OnsetScore != 0.0 || sbNaN.LeadLagScore != 0.0 ||
		sbNaN.NoveltyScore != 0.0 || sbNaN.CausalDirScore != 0.0 || sbNaN.GraphRootScore != 0.0 ||
		sbNaN.LocalizationScore != 0.0 || sbNaN.ChangeCorrScore != 0.0 {
		t.Errorf("Sanitize did not reset NaN/Inf to 0.0: %+v", sbNaN)
	}

	// Marshaling should succeed after sanitize
	if _, err := json.Marshal(sbNaN); err != nil {
		t.Errorf("marshal after sanitize failed: %v", err)
	}
}

func TestLeadLagLink_JSON(t *testing.T) {
	link := LeadLagLink{
		PeerService:  "chat-session",
		PeerTemplate: 1234567890,
		LagSeconds:   5,
		CorrelationR: 0.88,
	}

	data, err := json.Marshal(link)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed LeadLagLink
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed != link {
		t.Errorf("expected %+v, got %+v", link, parsed)
	}
}

func TestClockSkewFinding_JSON(t *testing.T) {
	finding := ClockSkewFinding{
		Service:     "chat-com",
		Pod:         "chat-com-7d587574b6-4k2x8",
		SkewMs:      -420,
		SampleRawTs: "2026-10-28T14:32:01.123456Z",
		SampleAppTs: "2026-10-28T14:32:01.543456Z",
	}

	data, err := json.Marshal(finding)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed ClockSkewFinding
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed != finding {
		t.Errorf("expected %+v, got %+v", finding, parsed)
	}
}

func TestHypothesis_JSONAndSanitize(t *testing.T) {
	now := time.Date(2026, 10, 28, 14, 30, 0, 0, time.UTC)
	hyp := Hypothesis{
		ID:                 "H-01",
		Rank:               1,
		Role:               RoleRootCause,
		Confidence:         0.92,
		Service:            "chat-session",
		TemplateID:         987654321,
		TemplateText:       "failed to acquire connection from pool: timeout",
		ErrorClass:         ErrClassDeadline,
		SourcePlace:        "pool.go:142",
		DominantPod:        "chat-session-pool-abc",
		PodSkewRatio:       0.85,
		DominantRoute:      "/session/acquire",
		DominantVersion:    "v1.4.2",
		OnsetTime:          now,
		BaselineRatePerMin: 0.1,
		IncidentRatePerMin: 120.5,
		GrowthRatio:        1205.0,
		PoissonPValue:      1e-9,
		IsConstantNoise:    false,
		RecurringCaseIDs:   []string{"case-20261020-01", "case-20261025-02"},
		LeadLagPeers: []LeadLagLink{
			{
				PeerService:  "chat-com",
				PeerTemplate: 111222333,
				LagSeconds:   8,
				CorrelationR: 0.94,
			},
		},
		Signals: SignalBreakdown{
			TraceChainScore:   0.8,
			OnsetScore:        0.9,
			LeadLagScore:      0.94,
			NoveltyScore:      1.0,
			CausalDirScore:    0.85,
			GraphRootScore:    0.9,
			LocalizationScore: 0.85,
			ChangeCorrScore:   0.88,
		},
		Explanation: "Primary connection pool exhaustion in chat-session leads to downstream gateway timeouts.",
		EvidenceIDs: []string{"E-01", "E-02"},
	}

	data, err := json.Marshal(hyp)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed Hypothesis
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed.ID != hyp.ID || parsed.Role != hyp.Role || parsed.Confidence != hyp.Confidence ||
		parsed.TemplateID != hyp.TemplateID || len(parsed.RecurringCaseIDs) != 2 || len(parsed.LeadLagPeers) != 1 {
		t.Errorf("mismatch in parsed hypothesis: %+v", parsed)
	}

	// Test Sanitize with NaNs
	hypNaN := Hypothesis{
		Confidence:         math.NaN(),
		PodSkewRatio:       math.Inf(1),
		BaselineRatePerMin: math.NaN(),
		IncidentRatePerMin: math.Inf(-1),
		GrowthRatio:        math.NaN(),
		PoissonPValue:      math.NaN(),
		LeadLagPeers: []LeadLagLink{
			{CorrelationR: math.NaN()},
		},
		Signals: SignalBreakdown{
			OnsetScore: math.NaN(),
		},
	}
	hypNaN.Sanitize()
	if hypNaN.Confidence != 0.0 || hypNaN.PodSkewRatio != 0.0 || hypNaN.BaselineRatePerMin != 0.0 ||
		hypNaN.IncidentRatePerMin != 0.0 || hypNaN.GrowthRatio != 0.0 || hypNaN.PoissonPValue != 0.0 ||
		hypNaN.LeadLagPeers[0].CorrelationR != 0.0 || hypNaN.Signals.OnsetScore != 0.0 {
		t.Errorf("Hypothesis.Sanitize failed: %+v", hypNaN)
	}

	if _, err := json.Marshal(hypNaN); err != nil {
		t.Errorf("marshal after sanitize failed: %v", err)
	}
}

func TestEvidenceItem_JSON(t *testing.T) {
	now := time.Date(2026, 10, 28, 14, 30, 0, 0, time.UTC)
	item := EvidenceItem{
		ID:              "E-01",
		Kind:            EvidenceKindRawLog,
		Service:         "chat-session",
		Summary:         "DB pool timeout error on pod chat-session-pool-abc",
		Timestamp:       now,
		SourceFile:      "/logs/chat-session_part-0001.json",
		SourceRecordIdx: 42,
		SHA256:          "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		RedactedLine:    `{"level":"ERROR","msg":"pool exhausted [REDACTED_IP]"}`,
		LogQLSnippet:    `{service="chat-session"} |= "pool exhausted"`,
		Metadata: map[string]any{
			"trace_id": "tr-987123",
			"pod":      "chat-session-pool-abc",
		},
	}

	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed EvidenceItem
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed.ID != item.ID || parsed.Kind != item.Kind || parsed.SourceRecordIdx != 42 ||
		parsed.Metadata["trace_id"] != "tr-987123" {
		t.Errorf("mismatch in parsed evidence item: %+v", parsed)
	}
}

func TestAnalyzeCaseResponse_JSONAndSanitize(t *testing.T) {
	start := time.Date(2026, 10, 28, 14, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)

	resp := AnalyzeCaseResponse{
		CaseID:        "case-01",
		RunID:         "run-101",
		IncidentStart: start,
		IncidentEnd:   end,
		Hypotheses: []Hypothesis{
			{
				ID:         "H-01",
				Rank:       1,
				Role:       RoleRootCause,
				Confidence: 0.95,
				Service:    "chat-session",
				Signals: SignalBreakdown{
					TraceChainScore: 0.9,
				},
			},
		},
		ServiceEdges: []ServiceEdge{
			{
				CaseID:         "case-01",
				CallerService:  "chat-com",
				CalleeService:  "chat-session",
				EvidenceSource: "trace_propagation",
				CallCount:      5000,
				ErrorCount:     450,
			},
		},
		ClockSkewFindings: []ClockSkewFinding{
			{
				Service: "chat-com",
				Pod:     "pod-1",
				SkewMs:  120,
			},
		},
		Timeline: []TimelinePoint{
			{
				Bucket:      start,
				Service:     "chat-session",
				Level:       LevelError,
				EventsCount: 42,
				MaxLatency:  15000000,
			},
		},
		EvidenceItems: []EvidenceItem{
			{
				ID:      "E-01",
				Kind:    EvidenceKindRawLog,
				Service: "chat-session",
			},
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed AnalyzeCaseResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed.CaseID != resp.CaseID || parsed.RunID != resp.RunID ||
		len(parsed.Hypotheses) != 1 || len(parsed.ServiceEdges) != 1 ||
		len(parsed.ClockSkewFindings) != 1 || len(parsed.Timeline) != 1 ||
		len(parsed.EvidenceItems) != 1 {
		t.Errorf("mismatch in parsed AnalyzeCaseResponse: %+v", parsed)
	}

	// Test Sanitize
	respNaN := AnalyzeCaseResponse{
		Hypotheses: []Hypothesis{
			{Confidence: math.NaN()},
		},
	}
	respNaN.Sanitize()
	if respNaN.Hypotheses[0].Confidence != 0.0 {
		t.Errorf("AnalyzeCaseResponse.Sanitize did not reset NaN: %v", respNaN.Hypotheses[0].Confidence)
	}
}

func TestStorageStatusResponse_JSON(t *testing.T) {
	status := StorageStatusResponse{
		RawBytes:      100 * 1024 * 1024,
		StagingBytes:  20 * 1024 * 1024,
		AggBytes:      5 * 1024 * 1024,
		ReportsBytes:  2 * 1024 * 1024,
		TotalBytes:    127 * 1024 * 1024,
		QuotaBytes:    2 * 1024 * 1024 * 1024,
		RawQuotaBytes: 1024 * 1024 * 1024,
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed StorageStatusResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed != status {
		t.Errorf("expected %+v, got %+v", status, parsed)
	}
}

func TestPurgeRawResponse_JSON(t *testing.T) {
	resp := PurgeRawResponse{
		FreedBytes: 100 * 1024 * 1024,
		DurationMs: 45,
		Status:     "SUCCESS",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed PurgeRawResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed != resp {
		t.Errorf("expected %+v, got %+v", resp, parsed)
	}
}

func TestCaseMeta_JSON(t *testing.T) {
	now := time.Date(2026, 10, 28, 14, 0, 0, 0, time.UTC)
	meta := CaseMeta{
		ID:            "case-42",
		Title:         "High 502 Rate Incident",
		Description:   "Sudden spike of 502 Bad Gateway between chat-com and chat-session",
		IncidentStart: &now,
		IncidentEnd:   &now,
		BaselineStart: nil,
		BaselineEnd:   nil,
		Pinned:        true,
		Status:        CaseStatusReady,
		CreatedAt:     now,
		LatestRunID:   "run-999",
	}

	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed CaseMeta
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed.ID != meta.ID || parsed.Title != meta.Title || !parsed.Pinned || parsed.LatestRunID != "run-999" {
		t.Errorf("mismatch in parsed CaseMeta: %+v", parsed)
	}
}

func TestIngestBatchRequestAndResponse_JSON(t *testing.T) {
	req := IngestBatchRequest{
		CaseID:     "case-01",
		SourceName: "tilt:chat-session-7f9c",
		Records: []RawScriptRecord{
			{
				Timestamp: "2026-10-28T14:32:00Z",
				Line:      `{"msg":"started"}`,
			},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsedReq IngestBatchRequest
	if err := json.Unmarshal(data, &parsedReq); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsedReq.CaseID != req.CaseID || parsedReq.SourceName != req.SourceName || len(parsedReq.Records) != 1 {
		t.Errorf("mismatch in parsed IngestBatchRequest: %+v", parsedReq)
	}

	res := IngestBatchResponse{
		RowsIngested:      100,
		ClockSkewWarnings: []string{"warning 1"},
	}
	resData, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var parsedRes IngestBatchResponse
	if err := json.Unmarshal(resData, &parsedRes); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsedRes.RowsIngested != 100 || len(parsedRes.ClockSkewWarnings) != 1 {
		t.Errorf("mismatch in parsed IngestBatchResponse: %+v", parsedRes)
	}
}

func TestTiltTarget_JSON(t *testing.T) {
	target := TiltTarget{
		ID:          "cid-12345",
		Runtime:     RuntimeDocker,
		ServiceName: "chat-com",
		PodName:     "chat-com-pod-1",
		Namespace:   "default",
		Status:      "running",
		Labels: map[string]string{
			"app": "chat-com",
		},
	}

	data, err := json.Marshal(target)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed TiltTarget
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed.ID != target.ID || parsed.ServiceName != target.ServiceName || parsed.Labels["app"] != "chat-com" {
		t.Errorf("mismatch in parsed TiltTarget: %+v", parsed)
	}
}

func TestTiltStartCaptureRequest_JSON(t *testing.T) {
	req := TiltStartCaptureRequest{
		CaseID:       "case-01",
		TargetIDs:    []string{"cid-1", "cid-2"},
		SinceSeconds: 300,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed TiltStartCaptureRequest
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed.CaseID != req.CaseID || len(parsed.TargetIDs) != 2 || parsed.SinceSeconds != 300 {
		t.Errorf("mismatch in parsed TiltStartCaptureRequest: %+v", parsed)
	}
}

func TestTiltStatusResponse_JSON(t *testing.T) {
	now := time.Date(2026, 10, 28, 14, 0, 0, 0, time.UTC)
	resp := TiltStatusResponse{
		CaseID:         "case-01",
		Active:         true,
		StartedAt:      &now,
		TargetsWatched: 3,
		LinesCaptured:  1500,
		BatchesFlushed: 15,
		LastError:      "",
		Targets: []TiltTarget{
			{
				ID:          "c-1",
				Runtime:     RuntimeDocker,
				ServiceName: "chat-session",
			},
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed TiltStatusResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if parsed.CaseID != resp.CaseID || !parsed.Active || parsed.TargetsWatched != 3 ||
		parsed.LinesCaptured != 1500 || len(parsed.Targets) != 1 {
		t.Errorf("mismatch in parsed TiltStatusResponse: %+v", parsed)
	}
}

func TestAdditionalAPIDTOs_JSON(t *testing.T) {
	now := time.Date(2026, 10, 28, 14, 0, 0, 0, time.UTC)

	// StartIngestRequest
	sir := StartIngestRequest{
		JobID:     "job-1",
		CaseID:    "case-1",
		FilePaths: []string{"/tmp/test.log"},
	}
	sirBytes, _ := json.Marshal(sir)
	var sirParsed StartIngestRequest
	_ = json.Unmarshal(sirBytes, &sirParsed)
	if sirParsed.JobID != "job-1" {
		t.Errorf("sir parsed mismatch")
	}

	// IngestStatusResponse
	isr := IngestStatusResponse{
		JobID:                 "job-1",
		CaseID:                "case-1",
		Status:                IngestStatusCompleted,
		FilesTotal:            2,
		FilesDone:             2,
		SkippedDuplicateFiles: 0,
		RowsIngested:          5000,
		BytesRead:             1024000,
		ClockSkewWarnings:     []string{"skew warning"},
		Error:                 "",
	}
	isrBytes, _ := json.Marshal(isr)
	var isrParsed IngestStatusResponse
	_ = json.Unmarshal(isrBytes, &isrParsed)
	if isrParsed.RowsIngested != 5000 {
		t.Errorf("isr parsed mismatch")
	}

	// AvailableFile
	af := AvailableFile{
		Path:      "/data/logs.json",
		Name:      "logs.json",
		SizeBytes: 4096,
		ModTime:   now,
	}
	afBytes, _ := json.Marshal(af)
	var afParsed AvailableFile
	_ = json.Unmarshal(afBytes, &afParsed)
	if afParsed.SizeBytes != 4096 {
		t.Errorf("af parsed mismatch")
	}

	// AnalyzeCaseRequest
	acr := AnalyzeCaseRequest{
		CaseID:                "case-1",
		IncidentStart:         &now,
		IncidentEnd:           &now,
		BaselineStart:         &now,
		BaselineEnd:           &now,
		UseHistoricalBaseline: true,
	}
	acrBytes, _ := json.Marshal(acr)
	var acrParsed AnalyzeCaseRequest
	_ = json.Unmarshal(acrBytes, &acrParsed)
	if !acrParsed.UseHistoricalBaseline {
		t.Errorf("acr parsed mismatch")
	}

	// BuildEvidenceRequest & BuildEvidenceResponse
	berReq := BuildEvidenceRequest{
		CaseID: "case-1",
		RunID:  "run-1",
		Analysis: AnalyzeCaseResponse{
			CaseID: "case-1",
			RunID:  "run-1",
		},
	}
	berReqBytes, _ := json.Marshal(berReq)
	var berReqParsed BuildEvidenceRequest
	_ = json.Unmarshal(berReqBytes, &berReqParsed)
	if berReqParsed.Analysis.RunID != "run-1" {
		t.Errorf("berReq parsed mismatch")
	}

	berResp := BuildEvidenceResponse{
		CaseID:       "case-1",
		RunID:        "run-1",
		ReportDir:    "/reports/1",
		MarkdownPath: "/reports/1/report.md",
		HTMLPath:     "/reports/1/report.html",
		GeminiPath:   "/reports/1/gemini.json",
		ZipPath:      "/reports/1/bundle.zip",
		SizeBytes:    1024,
		EvidenceItems: []EvidenceItem{
			{ID: "E-01"},
		},
	}
	berRespBytes, _ := json.Marshal(berResp)
	var berRespParsed BuildEvidenceResponse
	_ = json.Unmarshal(berRespBytes, &berRespParsed)
	if berRespParsed.MarkdownPath != "/reports/1/report.md" || len(berRespParsed.EvidenceItems) != 1 {
		t.Errorf("berResp parsed mismatch")
	}

	// CreateCaseRequest
	ccr := CreateCaseRequest{
		Title:       "Incident Alpha",
		Description: "Detailed description",
	}
	ccrBytes, _ := json.Marshal(ccr)
	var ccrParsed CreateCaseRequest
	_ = json.Unmarshal(ccrBytes, &ccrParsed)
	if ccrParsed.Title != "Incident Alpha" {
		t.Errorf("ccr parsed mismatch")
	}

	// IngestLocalRequest
	ilr := IngestLocalRequest{
		FilePaths: []string{"a.json", "b.json"},
	}
	ilrBytes, _ := json.Marshal(ilr)
	var ilrParsed IngestLocalRequest
	_ = json.Unmarshal(ilrBytes, &ilrParsed)
	if len(ilrParsed.FilePaths) != 2 {
		t.Errorf("ilr parsed mismatch")
	}

	// ContextLogsResponse
	clr := ContextLogsResponse{
		RawPurged: false,
		Mode:      ContextModePod,
		Events: []NormalizedEvent{
			{EventID: "evt-1"},
		},
	}
	clrBytes, _ := json.Marshal(clr)
	var clrParsed ContextLogsResponse
	_ = json.Unmarshal(clrBytes, &clrParsed)
	if clrParsed.Mode != ContextModePod || len(clrParsed.Events) != 1 {
		t.Errorf("clr parsed mismatch")
	}
}

func TestValidationMethods(t *testing.T) {
	// StartIngestRequest.Validate()
	var sir StartIngestRequest
	if err := sir.Validate(); err != ErrEmptyCaseID {
		t.Errorf("expected ErrEmptyCaseID, got %v", err)
	}
	sir.CaseID = "case-1"
	if err := sir.Validate(); err != ErrEmptyJobID {
		t.Errorf("expected ErrEmptyJobID, got %v", err)
	}
	sir.JobID = "job-1"
	if err := sir.Validate(); err != ErrEmptyFilePaths {
		t.Errorf("expected ErrEmptyFilePaths, got %v", err)
	}
	sir.FilePaths = []string{"/tmp/test.log"}
	if err := sir.Validate(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}

	// AnalyzeCaseRequest.Validate()
	var acr AnalyzeCaseRequest
	if err := acr.Validate(); err != ErrEmptyCaseID {
		t.Errorf("expected ErrEmptyCaseID, got %v", err)
	}
	acr.CaseID = "case-1"
	if err := acr.Validate(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}

	// CreateCaseRequest.Validate()
	var ccr CreateCaseRequest
	if err := ccr.Validate(); err != ErrEmptyTitle {
		t.Errorf("expected ErrEmptyTitle, got %v", err)
	}
	ccr.Title = "Case 1"
	if err := ccr.Validate(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}

	// IngestLocalRequest.Validate()
	var ilr IngestLocalRequest
	if err := ilr.Validate(); err != ErrEmptyFilePaths {
		t.Errorf("expected ErrEmptyFilePaths, got %v", err)
	}
	ilr.FilePaths = []string{"path1"}
	if err := ilr.Validate(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}

	// IngestBatchRequest.Validate()
	var ibr IngestBatchRequest
	if err := ibr.Validate(); err != ErrEmptyCaseID {
		t.Errorf("expected ErrEmptyCaseID, got %v", err)
	}
	ibr.CaseID = "case-1"
	if err := ibr.Validate(); err != ErrEmptyRecords {
		t.Errorf("expected ErrEmptyRecords, got %v", err)
	}
	ibr.Records = []RawScriptRecord{{Line: "test"}}
	if err := ibr.Validate(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}

	// TiltStartCaptureRequest.Validate()
	var tcr TiltStartCaptureRequest
	if err := tcr.Validate(); err != ErrEmptyCaseID {
		t.Errorf("expected ErrEmptyCaseID, got %v", err)
	}
	tcr.CaseID = "case-1"
	if err := tcr.Validate(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}
