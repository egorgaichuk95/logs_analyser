package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/rca-engine/internal/idgraph"
	"github.com/local/logs-analyzer/services/rca-engine/internal/ranker"
	"github.com/local/logs-analyzer/services/rca-engine/internal/timeseries"
	"github.com/local/logs-analyzer/services/rca-engine/internal/topology"
)

type Server struct {
	chURL      string
	client     *http.Client
	mu         sync.RWMutex
	caseEvents map[string][]contracts.NormalizedEvent
	caseEdges  map[string][]contracts.IdentityEdge
}

func NewServer(chURL string) *Server {
	return &Server{
		chURL:      strings.TrimRight(chURL, "/"),
		client:     &http.Client{Timeout: 60 * time.Second},
		caseEvents: make(map[string][]contracts.NormalizedEvent),
		caseEdges:  make(map[string][]contracts.IdentityEdge),
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /internal/rca/analyze", s.handleAnalyze)
	mux.HandleFunc("GET /internal/rca/chain/{case_id}", s.handleChain)
	return mux
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.Routes().ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func escapeSQL(val string) string {
	return strings.ReplaceAll(strings.ReplaceAll(val, `\`, `\\`), `'`, `\'`)
}

func parseCHDateTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, s, time.UTC); err == nil {
			return t
		}
	}
	return time.Time{}
}

func parseUintNumber(n json.Number) uint64 {
	v, err := strconv.ParseUint(n.String(), 10, 64)
	if err != nil {
		f, ferr := n.Float64()
		if ferr == nil && f >= 0 {
			return uint64(f)
		}
		return 0
	}
	return v
}

func (s *Server) queryClickHouse(ctx context.Context, sql string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.chURL, strings.NewReader(sql+"\n"))
	if err != nil {
		return nil, fmt.Errorf("create clickhouse request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute clickhouse request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("clickhouse error (status %d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return resp, nil
}

type chCountRow struct {
	Cnt json.Number `json:"cnt"`
}

type chRawEvent struct {
	CaseID           string `json:"case_id"`
	EventID          string `json:"event_id"`
	Ts               string `json:"ts"`
	AppTs            *string `json:"app_ts"`
	ClockSkewMs      int64  `json:"clock_skew_ms"`
	Service          string `json:"service"`
	Environment      string `json:"environment"`
	Pod              string `json:"pod"`
	Node             string `json:"node"`
	ServiceVersion   string `json:"service_version"`
	Level            string `json:"level"`
	SeverityNum      uint8  `json:"severity_num"`
	SchemaKind       string `json:"schema_kind"`
	TemplateID       uint64 `json:"template_id"`
	Message          string `json:"message"`
	ErrorClass       string `json:"error_class"`
	ErrorMsg         string `json:"error_msg"`
	ExceptionType    string `json:"exception_type"`
	SourcePlace      string `json:"source_place"`
	StackFingerprint uint64 `json:"stack_fingerprint"`
	TraceID          string `json:"trace_id"`
	SpanID           string `json:"span_id"`
	RequestID        string `json:"request_id"`
	SessionID        string `json:"session_id"`
	OperatorID       string `json:"operator_id"`
	VisitorID        string `json:"visitor_id"`
	HTTPMethod       string `json:"http_method"`
	HTTPRoute        string `json:"http_route"`
	HTTPStatus       uint16 `json:"http_status"`
	LatencyNs        uint64 `json:"latency_ns"`
	PeerService      string `json:"peer_service"`
	SourceFile       string `json:"source_file"`
	SourceRecordIdx  uint32 `json:"source_record_idx"`
	RawHash          uint64 `json:"raw_hash"`
	RawRedacted      string `json:"raw_redacted"`
}

type chIdentityEdge struct {
	CaseID    string `json:"case_id"`
	Ts        string `json:"ts"`
	Service   string `json:"service"`
	LeftType  string `json:"left_type"`
	LeftVal   string `json:"left_val"`
	RightType string `json:"right_type"`
	RightVal  string `json:"right_val"`
}

type chAggRow struct {
	Bucket1m       string      `json:"bucket_1m"`
	Service        string      `json:"service"`
	Pod            string      `json:"pod"`
	ServiceVersion string      `json:"service_version"`
	Level          string      `json:"level"`
	ErrorClass     string      `json:"error_class"`
	TemplateID     uint64      `json:"template_id"`
	SourcePlace    string      `json:"source_place"`
	PeerService    string      `json:"peer_service"`
	EventsCount    json.Number `json:"events_count"`
	MaxLatencyNs   json.Number `json:"max_latency_ns"`
	MaxSkewMs      json.Number `json:"max_skew_ms"`
}

type chTemplateRow struct {
	CaseID         string `json:"case_id"`
	Service        string `json:"service"`
	TemplateID     uint64 `json:"template_id"`
	TemplateText   string `json:"template_text"`
	Level          string `json:"level"`
	ErrorClass     string `json:"error_class"`
	SourcePlace    string `json:"source_place"`
	SampleRedacted string `json:"sample_redacted"`
	FirstSeen      string `json:"first_seen"`
	LastSeen       string `json:"last_seen"`
}

type chServiceEdgeRow struct {
	CaseID         string      `json:"case_id"`
	CallerService  string      `json:"caller_service"`
	CalleeService  string      `json:"callee_service"`
	EvidenceSource string      `json:"evidence_source"`
	CallCount      json.Number `json:"call_count"`
	ErrorCount     json.Number `json:"error_count"`
}

type chHistRow struct {
	TemplateID   uint64      `json:"template_id"`
	TotalEvents  json.Number `json:"total_events"`
	TotalMinutes json.Number `json:"total_minutes"`
}

func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	var req contracts.AnalyzeCaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}

	if err := req.Validate(); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	escCaseID := escapeSQL(req.CaseID)

	// 1. Проверка наличия сырых строк кейса
	countSQL := fmt.Sprintf("SELECT count() AS cnt FROM logs_rca.events_raw WHERE case_id = '%s' FORMAT JSONEachRow", escCaseID)
	countResp, err := s.queryClickHouse(ctx, countSQL)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	defer countResp.Body.Close()

	var cnt uint64
	dec := json.NewDecoder(countResp.Body)
	for dec.More() {
		var crow chCountRow
		if err := dec.Decode(&crow); err == nil {
			cnt = parseUintNumber(crow.Cnt)
		}
	}

	var rawEvents []contracts.NormalizedEvent
	var identityEdges []contracts.IdentityEdge
	var idRes idgraph.Result

	// 2. Если cnt > 0, загрузить сырые события и рёбра идентификаторов
	if cnt > 0 {
		eventsSQL := fmt.Sprintf("SELECT case_id, event_id, ts, app_ts, clock_skew_ms, service, environment, pod, node, service_version, level, severity_num, schema_kind, template_id, message, error_class, error_msg, exception_type, source_place, stack_fingerprint, trace_id, span_id, request_id, session_id, operator_id, visitor_id, http_method, http_route, http_status, latency_ns, peer_service, source_file, source_record_idx, raw_hash, raw_redacted FROM logs_rca.events_raw WHERE case_id = '%s' ORDER BY ts ASC FORMAT JSONEachRow", escCaseID)
		evResp, err := s.queryClickHouse(ctx, eventsSQL)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		defer evResp.Body.Close()

		evDec := json.NewDecoder(evResp.Body)
		for evDec.More() {
			var re chRawEvent
			if err := evDec.Decode(&re); err == nil {
				parsedTs := parseCHDateTime(re.Ts)
				var appTs *time.Time
				if re.AppTs != nil {
					t := parseCHDateTime(*re.AppTs)
					appTs = &t
				}
				rawEvents = append(rawEvents, contracts.NormalizedEvent{
					CaseID:           re.CaseID,
					EventID:          re.EventID,
					Ts:               parsedTs,
					AppTs:            appTs,
					ClockSkewMs:      re.ClockSkewMs,
					Service:          re.Service,
					Environment:      re.Environment,
					Pod:              re.Pod,
					Node:             re.Node,
					ServiceVersion:   re.ServiceVersion,
					Level:            re.Level,
					SeverityNum:      re.SeverityNum,
					SchemaKind:       re.SchemaKind,
					TemplateID:       re.TemplateID,
					Message:          re.Message,
					ErrorClass:       re.ErrorClass,
					ErrorMsg:         re.ErrorMsg,
					ExceptionType:    re.ExceptionType,
					SourcePlace:      re.SourcePlace,
					StackFingerprint: re.StackFingerprint,
					TraceID:          re.TraceID,
					SpanID:           re.SpanID,
					RequestID:        re.RequestID,
					SessionID:        re.SessionID,
					OperatorID:       re.OperatorID,
					VisitorID:        re.VisitorID,
					HTTPMethod:       re.HTTPMethod,
					HTTPRoute:        re.HTTPRoute,
					HTTPStatus:       re.HTTPStatus,
					LatencyNs:        re.LatencyNs,
					PeerService:      re.PeerService,
					SourceFile:       re.SourceFile,
					SourceRecordIdx:  re.SourceRecordIdx,
					RawHash:          re.RawHash,
					RawRedacted:      re.RawRedacted,
				})
			}
		}

		idEdgesSQL := fmt.Sprintf("SELECT case_id, ts, service, left_type, left_val, right_type, right_val FROM logs_rca.identity_edges WHERE case_id = '%s' FORMAT JSONEachRow", escCaseID)
		idResp, err := s.queryClickHouse(ctx, idEdgesSQL)
		if err == nil {
			defer idResp.Body.Close()
			idDec := json.NewDecoder(idResp.Body)
			for idDec.More() {
				var ie chIdentityEdge
				if err := idDec.Decode(&ie); err == nil {
					identityEdges = append(identityEdges, contracts.IdentityEdge{
						CaseID:    ie.CaseID,
						Ts:        parseCHDateTime(ie.Ts),
						Service:   ie.Service,
						LeftType:  ie.LeftType,
						LeftVal:   ie.LeftVal,
						RightType: ie.RightType,
						RightVal:  ie.RightVal,
					})
				}
			}
		}

		idRes = idgraph.BuildChainsAndScores(rawEvents, identityEdges)

		s.mu.Lock()
		s.caseEvents[req.CaseID] = rawEvents
		s.caseEdges[req.CaseID] = identityEdges
		s.mu.Unlock()
	} else {
		idRes = idgraph.Result{
			Chains:           make([]idgraph.Chain, 0),
			TemplateS1Scores: make(map[uint64]float64),
			DerivedEdges:     make([]contracts.ServiceEdge, 0),
		}
	}

	// 3. Загрузить минутные ряды и метаданные из logs_rca.events_agg_1m
	aggSQL := fmt.Sprintf("SELECT bucket_1m, service, pod, service_version, level, error_class, template_id, source_place, peer_service, sum(events_count) AS events_count, max(latency_max_ns) AS max_latency_ns, max(clock_skew_abs_ms) AS max_skew_ms FROM logs_rca.events_agg_1m WHERE case_id = '%s' GROUP BY bucket_1m, service, pod, service_version, level, error_class, template_id, source_place, peer_service ORDER BY bucket_1m ASC FORMAT JSONEachRow", escCaseID)
	aggResp, err := s.queryClickHouse(ctx, aggSQL)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	defer aggResp.Body.Close()

	var aggRows []chAggRow
	aggDec := json.NewDecoder(aggResp.Body)
	for aggDec.More() {
		var row chAggRow
		if err := aggDec.Decode(&row); err == nil {
			aggRows = append(aggRows, row)
		}
	}

	// 4. Загрузить справочник шаблонов и рёбра сервисов
	templatesMap := make(map[uint64]contracts.TemplateRecord)
	tmplSQL := fmt.Sprintf("SELECT case_id, service, template_id, template_text, level, error_class, source_place, sample_redacted, first_seen, last_seen FROM logs_rca.templates_dict WHERE case_id = '%s' FORMAT JSONEachRow", escCaseID)
	tmplResp, err := s.queryClickHouse(ctx, tmplSQL)
	if err == nil {
		defer tmplResp.Body.Close()
		tmplDec := json.NewDecoder(tmplResp.Body)
		for tmplDec.More() {
			var tr chTemplateRow
			if err := tmplDec.Decode(&tr); err == nil {
				templatesMap[tr.TemplateID] = contracts.TemplateRecord{
					CaseID:         tr.CaseID,
					Service:        tr.Service,
					TemplateID:     tr.TemplateID,
					TemplateText:   tr.TemplateText,
					Level:          tr.Level,
					ErrorClass:     tr.ErrorClass,
					SourcePlace:    tr.SourcePlace,
					SampleRedacted: tr.SampleRedacted,
					FirstSeen:      parseCHDateTime(tr.FirstSeen),
					LastSeen:       parseCHDateTime(tr.LastSeen),
				}
			}
		}
	}

	var explicitEdges []contracts.ServiceEdge
	srvEdgesSQL := fmt.Sprintf("SELECT case_id, caller_service, callee_service, evidence_source, sum(call_count) AS call_count, sum(error_count) AS error_count FROM logs_rca.service_edges WHERE case_id = '%s' GROUP BY case_id, caller_service, callee_service, evidence_source FORMAT JSONEachRow", escCaseID)
	srvResp, err := s.queryClickHouse(ctx, srvEdgesSQL)
	if err == nil {
		defer srvResp.Body.Close()
		srvDec := json.NewDecoder(srvResp.Body)
		for srvDec.More() {
			var se chServiceEdgeRow
			if err := srvDec.Decode(&se); err == nil {
				explicitEdges = append(explicitEdges, contracts.ServiceEdge{
					CaseID:         se.CaseID,
					CallerService:  se.CallerService,
					CalleeService:  se.CalleeService,
					EvidenceSource: se.EvidenceSource,
					CallCount:      parseUintNumber(se.CallCount),
					ErrorCount:     parseUintNumber(se.ErrorCount),
				})
			}
		}
	}

	// 5. Исторический baseline при use_historical_baseline == true
	historicalBaseRate := make(map[uint64]float64)
	if req.UseHistoricalBaseline {
		histSQL := fmt.Sprintf("SELECT template_id, sum(events_count) AS total_events, count(DISTINCT bucket_1m) AS total_minutes FROM logs_rca.events_agg_1m WHERE case_id != '%s' GROUP BY template_id FORMAT JSONEachRow", escCaseID)
		histResp, err := s.queryClickHouse(ctx, histSQL)
		if err == nil {
			defer histResp.Body.Close()
			hDec := json.NewDecoder(histResp.Body)
			for hDec.More() {
				var hr chHistRow
				if err := hDec.Decode(&hr); err == nil {
					totEv := float64(parseUintNumber(hr.TotalEvents))
					totMin := float64(parseUintNumber(hr.TotalMinutes))
					if totMin > 0 {
						historicalBaseRate[hr.TemplateID] = contracts.SanitizeFloat(totEv / totMin)
					}
				}
			}
		}
	}

	// 6. Формирование временных рядов и метаданных кандидатов
	seriesMap := make(map[uint64]map[time.Time]uint64)
	serviceByTpl := make(map[uint64]string)
	anomalousServices := make(map[string]bool)

	type tplPodCount struct {
		totalCount   uint64
		countsByPod  map[string]uint64
		routes       map[string]uint64
		versions     map[string]uint64
		peerServices map[string]uint64
	}
	tplPodMap := make(map[uint64]*tplPodCount)

	serviceVersions := make(map[string]map[string]struct{})
	skewFindingsMap := make(map[string]*contracts.ClockSkewFinding)

	type timelineKey struct {
		bucket  time.Time
		service string
		level   string
	}
	timelineMap := make(map[timelineKey]*contracts.TimelinePoint)

	var caseMinTime time.Time
	var caseMaxTime time.Time

	for _, row := range aggRows {
		bTime := parseCHDateTime(row.Bucket1m)
		if bTime.IsZero() {
			continue
		}
		if caseMinTime.IsZero() || bTime.Before(caseMinTime) {
			caseMinTime = bTime
		}
		if caseMaxTime.IsZero() || bTime.After(caseMaxTime) {
			caseMaxTime = bTime
		}

		cntVal := parseUintNumber(row.EventsCount)
		latVal := parseUintNumber(row.MaxLatencyNs)
		skewVal := parseUintNumber(row.MaxSkewMs)

		if seriesMap[row.TemplateID] == nil {
			seriesMap[row.TemplateID] = make(map[time.Time]uint64)
		}
		seriesMap[row.TemplateID][bTime] += cntVal
		serviceByTpl[row.TemplateID] = row.Service

		if row.Level == contracts.LevelError || (row.ErrorClass != "" && row.ErrorClass != contracts.ErrClassNone) {
			anomalousServices[row.Service] = true
		}

		meta, ok := tplPodMap[row.TemplateID]
		if !ok {
			meta = &tplPodCount{
				countsByPod:  make(map[string]uint64),
				routes:       make(map[string]uint64),
				versions:     make(map[string]uint64),
				peerServices: make(map[string]uint64),
			}
			tplPodMap[row.TemplateID] = meta
		}
		meta.totalCount += cntVal
		if row.Pod != "" {
			meta.countsByPod[row.Pod] += cntVal
		}
		if row.ServiceVersion != "" {
			meta.versions[row.ServiceVersion] += cntVal
		}
		if row.PeerService != "" {
			meta.peerServices[row.PeerService] += cntVal
		}

		if row.Service != "" && row.ServiceVersion != "" {
			if serviceVersions[row.Service] == nil {
				serviceVersions[row.Service] = make(map[string]struct{})
			}
			serviceVersions[row.Service][row.ServiceVersion] = struct{}{}
		}

		// Сбор ClockSkewFindings
		if skewVal > 0 && row.Service != "" && row.Pod != "" {
			skewKey := row.Service + "/" + row.Pod
			existing, found := skewFindingsMap[skewKey]
			if !found || int64(skewVal) > existing.SkewMs {
				skewFindingsMap[skewKey] = &contracts.ClockSkewFinding{
					Service:     row.Service,
					Pod:         row.Pod,
					SkewMs:      int64(skewVal),
					SampleRawTs: bTime.Format(time.RFC3339),
					SampleAppTs: bTime.Format(time.RFC3339),
				}
			}
		}

		// Сбор TimelinePoint
		tk := timelineKey{bucket: bTime, service: row.Service, level: row.Level}
		tp, exists := timelineMap[tk]
		if !exists {
			tp = &contracts.TimelinePoint{
				Bucket:      bTime,
				Service:     row.Service,
				Level:       row.Level,
				EventsCount: 0,
				MaxLatency:  0,
			}
			timelineMap[tk] = tp
		}
		tp.EventsCount += cntVal
		if latVal > tp.MaxLatency {
			tp.MaxLatency = latVal
		}
	}

	// Обогащение из сырых событий (если доступны)
	for _, ev := range rawEvents {
		if ev.ClockSkewMs != 0 && ev.Service != "" && ev.Pod != "" {
			absSkew := ev.ClockSkewMs
			if absSkew < 0 {
				absSkew = -absSkew
			}
			skewKey := ev.Service + "/" + ev.Pod
			existing, found := skewFindingsMap[skewKey]
			if !found || absSkew > existing.SkewMs {
				sampleApp := ""
				if ev.AppTs != nil {
					sampleApp = ev.AppTs.Format(time.RFC3339Nano)
				}
				skewFindingsMap[skewKey] = &contracts.ClockSkewFinding{
					Service:     ev.Service,
					Pod:         ev.Pod,
					SkewMs:      ev.ClockSkewMs,
					SampleRawTs: ev.Ts.Format(time.RFC3339Nano),
					SampleAppTs: sampleApp,
				}
			}
		}
		if ev.HTTPRoute != "" && ev.TemplateID != 0 {
			if meta, ok := tplPodMap[ev.TemplateID]; ok {
				meta.routes[ev.HTTPRoute]++
			}
		}
	}

	// Построение итогового BucketPoint ряда для каждого шаблона
	bucketSeries := make(map[uint64][]timeseries.BucketPoint)
	for tplID, ptsMap := range seriesMap {
		pts := make([]timeseries.BucketPoint, 0, len(ptsMap))
		for ts, c := range ptsMap {
			pts = append(pts, timeseries.BucketPoint{Ts: ts, Count: c})
		}
		sort.Slice(pts, func(i, j int) bool {
			return pts[i].Ts.Before(pts[j].Ts)
		})
		bucketSeries[tplID] = pts
	}

	var baselineEnd time.Time
	if req.BaselineEnd != nil {
		baselineEnd = *req.BaselineEnd
	}

	statsMap := timeseries.AnalyzeSeries(bucketSeries, serviceByTpl, historicalBaseRate, baselineEnd)
	llRes := timeseries.ComputeLeadLag(statsMap, 60)
	topoRes := topology.ComputeTopology(explicitEdges, idRes.DerivedEdges, anomalousServices)

	var candidates []ranker.CandidateInput
	for tplID, stats := range statsMap {
		tmplRec, ok := templatesMap[tplID]
		if !ok {
			tmplRec = contracts.TemplateRecord{
				CaseID:     req.CaseID,
				Service:    stats.Service,
				TemplateID: tplID,
				Level:      contracts.LevelInfo,
			}
		}

		meta := tplPodMap[tplID]
		var (
			domPod     string
			domVersion string
			domRoute   string
			peerSvc    string
			skewRatio  float64
		)
		if meta != nil {
			var maxPodCnt uint64
			for pod, cnt := range meta.countsByPod {
				if cnt > maxPodCnt {
					maxPodCnt = cnt
					domPod = pod
				}
			}
			if meta.totalCount > 0 {
				skewRatio = contracts.SanitizeFloat(float64(maxPodCnt) / float64(meta.totalCount))
			}

			var maxVerCnt uint64
			for ver, cnt := range meta.versions {
				if cnt > maxVerCnt {
					maxVerCnt = cnt
					domVersion = ver
				}
			}

			var maxRouteCnt uint64
			for route, cnt := range meta.routes {
				if cnt > maxRouteCnt {
					maxRouteCnt = cnt
					domRoute = route
				}
			}

			var maxPeerCnt uint64
			for peer, cnt := range meta.peerServices {
				if cnt > maxPeerCnt {
					maxPeerCnt = cnt
					peerSvc = peer
				}
			}
		}

		verChanged := false
		if tmplRec.Service != "" && len(serviceVersions[tmplRec.Service]) > 1 {
			verChanged = true
		}

		candidates = append(candidates, ranker.CandidateInput{
			Template:        tmplRec,
			Stats:           stats,
			DominantPod:     domPod,
			PodSkewRatio:    skewRatio,
			DominantRoute:   domRoute,
			DominantVersion: domVersion,
			PeerService:     peerSvc,
			VersionChanged:  verChanged,
		})
	}

	hypotheses := ranker.RankHypotheses(candidates, idRes, topoRes, llRes)

	// Timeline
	timeline := make([]contracts.TimelinePoint, 0, len(timelineMap))
	for _, tp := range timelineMap {
		timeline = append(timeline, *tp)
	}
	sort.Slice(timeline, func(i, j int) bool {
		if timeline[i].Bucket.Equal(timeline[j].Bucket) {
			return timeline[i].Service < timeline[j].Service
		}
		return timeline[i].Bucket.Before(timeline[j].Bucket)
	})

	// Clock skew findings
	clockSkewFindings := make([]contracts.ClockSkewFinding, 0, len(skewFindingsMap))
	for _, csf := range skewFindingsMap {
		clockSkewFindings = append(clockSkewFindings, *csf)
	}
	sort.Slice(clockSkewFindings, func(i, j int) bool {
		return clockSkewFindings[i].Service < clockSkewFindings[j].Service
	})

	incStart := caseMinTime
	if req.IncidentStart != nil {
		incStart = *req.IncidentStart
	} else if incStart.IsZero() {
		incStart = time.Now().UTC()
	}

	incEnd := caseMaxTime
	if req.IncidentEnd != nil {
		incEnd = *req.IncidentEnd
	} else if incEnd.IsZero() {
		incEnd = incStart.Add(time.Hour)
	}

	runID := fmt.Sprintf("run-%d", time.Now().UnixNano())

	resp := contracts.AnalyzeCaseResponse{
		CaseID:            req.CaseID,
		RunID:             runID,
		IncidentStart:     incStart,
		IncidentEnd:       incEnd,
		Hypotheses:        hypotheses,
		ServiceEdges:      topoRes.Edges,
		ClockSkewFindings: clockSkewFindings,
		Timeline:          timeline,
		EvidenceItems:     make([]contracts.EvidenceItem, 0),
	}
	resp.Sanitize()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleChain(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	prefix := "/internal/rca/chain/"
	if !strings.HasPrefix(path, prefix) {
		http.Error(w, `{"error":"invalid path"}`, http.StatusBadRequest)
		return
	}

	caseID := strings.TrimPrefix(path, prefix)
	if idx := strings.Index(caseID, "/"); idx != -1 {
		caseID = caseID[:idx]
	}
	if caseID == "" {
		http.Error(w, `{"error":"missing case_id"}`, http.StatusBadRequest)
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]contracts.NormalizedEvent{})
		return
	}

	s.mu.RLock()
	events, hasEvents := s.caseEvents[caseID]
	edges := s.caseEdges[caseID]
	s.mu.RUnlock()

	if !hasEvents {
		ctx := r.Context()
		escCaseID := escapeSQL(caseID)

		countSQL := fmt.Sprintf("SELECT count() AS cnt FROM logs_rca.events_raw WHERE case_id = '%s' FORMAT JSONEachRow", escCaseID)
		countResp, err := s.queryClickHouse(ctx, countSQL)
		if err == nil {
			defer countResp.Body.Close()
			var cnt uint64
			dec := json.NewDecoder(countResp.Body)
			for dec.More() {
				var crow chCountRow
				if err := dec.Decode(&crow); err == nil {
					cnt = parseUintNumber(crow.Cnt)
				}
			}

			if cnt > 0 {
				eventsSQL := fmt.Sprintf("SELECT case_id, event_id, ts, app_ts, clock_skew_ms, service, environment, pod, node, service_version, level, severity_num, schema_kind, template_id, message, error_class, error_msg, exception_type, source_place, stack_fingerprint, trace_id, span_id, request_id, session_id, operator_id, visitor_id, http_method, http_route, http_status, latency_ns, peer_service, source_file, source_record_idx, raw_hash, raw_redacted FROM logs_rca.events_raw WHERE case_id = '%s' ORDER BY ts ASC FORMAT JSONEachRow", escCaseID)
				evResp, err := s.queryClickHouse(ctx, eventsSQL)
				if err == nil {
					defer evResp.Body.Close()
					evDec := json.NewDecoder(evResp.Body)
					for evDec.More() {
						var re chRawEvent
						if err := evDec.Decode(&re); err == nil {
							var appTs *time.Time
							if re.AppTs != nil {
								t := parseCHDateTime(*re.AppTs)
								appTs = &t
							}
							events = append(events, contracts.NormalizedEvent{
								CaseID:           re.CaseID,
								EventID:          re.EventID,
								Ts:               parseCHDateTime(re.Ts),
								AppTs:            appTs,
								ClockSkewMs:      re.ClockSkewMs,
								Service:          re.Service,
								Environment:      re.Environment,
								Pod:              re.Pod,
								Node:             re.Node,
								ServiceVersion:   re.ServiceVersion,
								Level:            re.Level,
								SeverityNum:      re.SeverityNum,
								SchemaKind:       re.SchemaKind,
								TemplateID:       re.TemplateID,
								Message:          re.Message,
								ErrorClass:       re.ErrorClass,
								ErrorMsg:         re.ErrorMsg,
								ExceptionType:    re.ExceptionType,
								SourcePlace:      re.SourcePlace,
								StackFingerprint: re.StackFingerprint,
								TraceID:          re.TraceID,
								SpanID:           re.SpanID,
								RequestID:        re.RequestID,
								SessionID:        re.SessionID,
								OperatorID:       re.OperatorID,
								VisitorID:        re.VisitorID,
								HTTPMethod:       re.HTTPMethod,
								HTTPRoute:        re.HTTPRoute,
								HTTPStatus:       re.HTTPStatus,
								LatencyNs:        re.LatencyNs,
								PeerService:      re.PeerService,
								SourceFile:       re.SourceFile,
								SourceRecordIdx:  re.SourceRecordIdx,
								RawHash:          re.RawHash,
								RawRedacted:      re.RawRedacted,
							})
						}
					}
				}

				idEdgesSQL := fmt.Sprintf("SELECT case_id, ts, service, left_type, left_val, right_type, right_val FROM logs_rca.identity_edges WHERE case_id = '%s' FORMAT JSONEachRow", escCaseID)
				idResp, err := s.queryClickHouse(ctx, idEdgesSQL)
				if err == nil {
					defer idResp.Body.Close()
					idDec := json.NewDecoder(idResp.Body)
					for idDec.More() {
						var ie chIdentityEdge
						if err := idDec.Decode(&ie); err == nil {
							edges = append(edges, contracts.IdentityEdge{
								CaseID:    ie.CaseID,
								Ts:        parseCHDateTime(ie.Ts),
								Service:   ie.Service,
								LeftType:  ie.LeftType,
								LeftVal:   ie.LeftVal,
								RightType: ie.RightType,
								RightVal:  ie.RightVal,
							})
						}
					}
				}

				s.mu.Lock()
				s.caseEvents[caseID] = events
				s.caseEdges[caseID] = edges
				s.mu.Unlock()
			}
		}
	}

	chain := idgraph.FindChainByKey(events, edges, key)
	if chain == nil {
		chain = []contracts.NormalizedEvent{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(chain)
}
