package server

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/gateway-api/internal/explorer"
	"github.com/local/logs-analyzer/services/gateway-api/internal/metadb"
	"github.com/local/logs-analyzer/services/gateway-api/internal/storage"
)

type Config struct {
	CHHTTPURL        string
	IngestURL        string
	RCAURL           string
	EvidenceURL      string
	TiltCollectorURL string
	StagingDir       string
	ReportsDir       string
	QuotaBytes       int64
	RawQuotaBytes    int64
	MetaStore        *metadb.Store
	StorageMgr       *storage.Manager
	Explorer         *explorer.Explorer
	HTTPClient       *http.Client
}

type Server struct {
	cfg        Config
	meta       *metadb.Store
	storage    *storage.Manager
	explorer   *explorer.Explorer
	httpClient *http.Client
}

func NewServer(cfg Config) *Server {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	s := &Server{
		cfg:        cfg,
		meta:       cfg.MetaStore,
		storage:    cfg.StorageMgr,
		explorer:   cfg.Explorer,
		httpClient: client,
	}
	return s
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/storage/status", s.handleStorageStatus)
	mux.HandleFunc("POST /api/v1/storage/purge-raw", s.handleStoragePurgeRaw)
	mux.HandleFunc("GET /api/v1/files/available", s.handleFilesAvailable)
	mux.HandleFunc("GET /api/v1/cases", s.handleListCases)
	mux.HandleFunc("POST /api/v1/cases", s.handleCreateCase)
	mux.HandleFunc("DELETE /api/v1/cases/{id}", s.handleDeleteCase)
	mux.HandleFunc("POST /api/v1/cases/{id}/ingest-local", s.handleIngestLocal)
	mux.HandleFunc("POST /api/v1/cases/{id}/upload", s.handleUpload)
	mux.HandleFunc("GET /api/v1/jobs/{job_id}", s.handleJobStatus)
	mux.HandleFunc("POST /api/v1/cases/{id}/analyze", s.handleAnalyzeCase)
	mux.HandleFunc("GET /api/v1/cases/{id}/analysis", s.handleGetAnalysis)
	mux.HandleFunc("GET /api/v1/cases/{id}/chain", s.handleGetChain)
	mux.HandleFunc("GET /api/v1/cases/{id}/logs/context", s.handleGetContextLogs)
	mux.HandleFunc("GET /api/v1/cases/{id}/reports/{run_id}/{kind}", s.handleGetReportFile)
	mux.HandleFunc("GET /api/v1/tilt/targets", s.handleTiltTargets)
	mux.HandleFunc("POST /api/v1/cases/{id}/tilt/start", s.handleTiltStart)
	mux.HandleFunc("POST /api/v1/cases/{id}/tilt/stop", s.handleTiltStop)
	mux.HandleFunc("GET /api/v1/cases/{id}/tilt/status", s.handleTiltStatus)

	return corsMiddleware(mux)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.Routes().ServeHTTP(w, r)
}

func (s *Server) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) writeError(w http.ResponseWriter, code int, msg string) {
	s.writeJSON(w, code, map[string]string{"error": msg})
}

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// 1. GET /api/v1/health
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// 2. GET /api/v1/storage/status
func (s *Server) handleStorageStatus(w http.ResponseWriter, r *http.Request) {
	if s.storage == nil {
		s.writeError(w, http.StatusInternalServerError, "storage manager not initialized")
		return
	}
	status, err := s.storage.GetStatus(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, status)
}

// 3. POST /api/v1/storage/purge-raw
func (s *Server) handleStoragePurgeRaw(w http.ResponseWriter, r *http.Request) {
	if s.storage == nil {
		s.writeError(w, http.StatusInternalServerError, "storage manager not initialized")
		return
	}
	resp, err := s.storage.PurgeAllRawLogs(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// 4. GET /api/v1/files/available
func (s *Server) handleFilesAvailable(w http.ResponseWriter, r *http.Request) {
	if s.cfg.IngestURL == "" {
		s.writeJSON(w, http.StatusOK, []contracts.AvailableFile{})
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.cfg.IngestURL+"/internal/ingest/browse", nil)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("proxy to ingest failed: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		s.writeError(w, resp.StatusCode, string(body))
		return
	}

	var files []contracts.AvailableFile
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		files = []contracts.AvailableFile{}
	}
	s.writeJSON(w, http.StatusOK, files)
}

// 5. GET /api/v1/cases
func (s *Server) handleListCases(w http.ResponseWriter, r *http.Request) {
	if s.meta == nil {
		s.writeJSON(w, http.StatusOK, []contracts.CaseMeta{})
		return
	}
	cases := s.meta.ListCases()
	if cases == nil {
		cases = []contracts.CaseMeta{}
	}
	s.writeJSON(w, http.StatusOK, cases)
}

// 6. POST /api/v1/cases
func (s *Server) handleCreateCase(w http.ResponseWriter, r *http.Request) {
	var req contracts.CreateCaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := req.Validate(); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	caseID := fmt.Sprintf("case-%s", generateUUID()[:8])
	caseMeta := contracts.CaseMeta{
		ID:          caseID,
		Title:       req.Title,
		Description: req.Description,
		Status:      contracts.CaseStatusCreated,
		CreatedAt:   time.Now().UTC(),
	}

	if s.meta != nil {
		if err := s.meta.SaveCase(caseMeta); err != nil {
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("save case: %v", err))
			return
		}
	}

	s.writeJSON(w, http.StatusCreated, caseMeta)
}

// 7. DELETE /api/v1/cases/{id}
func (s *Server) handleDeleteCase(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, "missing case id")
		return
	}

	if s.meta != nil {
		if _, ok := s.meta.GetCase(id); !ok {
			s.writeError(w, http.StatusNotFound, fmt.Sprintf("case %q not found", id))
			return
		}
	}

	// Remove staging and reports folders
	if s.cfg.StagingDir != "" {
		_ = os.RemoveAll(filepath.Join(s.cfg.StagingDir, id))
	}
	if s.cfg.ReportsDir != "" {
		_ = os.RemoveAll(filepath.Join(s.cfg.ReportsDir, id))
	}

	// Delete ClickHouse partitions / rows
	if s.cfg.CHHTTPURL != "" {
		tables := []string{
			"logs_rca.events_raw",
			"logs_rca.events_agg_1m",
			"logs_rca.templates_dict",
			"logs_rca.identity_edges",
			"logs_rca.service_edges",
			"logs_rca.ingested_files",
		}
		for _, tbl := range tables {
			query := fmt.Sprintf("ALTER TABLE %s DELETE WHERE case_id = '%s'", tbl, strings.ReplaceAll(id, "'", "''"))
			req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.cfg.CHHTTPURL, strings.NewReader(query+"\n"))
			if err == nil {
				req.Header.Set("Content-Type", "text/plain")
				if resp, err := s.httpClient.Do(req); err == nil {
					_ = resp.Body.Close()
				}
			}
		}
	}

	if s.meta != nil {
		if err := s.meta.DeleteCase(id); err != nil {
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("delete case from meta: %v", err))
			return
		}
	}

	s.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// 8. POST /api/v1/cases/{id}/ingest-local
func (s *Server) handleIngestLocal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, "missing case id")
		return
	}

	if s.meta != nil {
		if _, ok := s.meta.GetCase(id); !ok {
			s.writeError(w, http.StatusNotFound, fmt.Sprintf("case %q not found", id))
			return
		}
	}

	var req contracts.IngestLocalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := req.Validate(); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	jobID := fmt.Sprintf("job-%d", time.Now().UnixNano())
	startReq := contracts.StartIngestRequest{
		JobID:     jobID,
		CaseID:    id,
		FilePaths: req.FilePaths,
	}

	body, _ := json.Marshal(startReq)
	postReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.cfg.IngestURL+"/internal/ingest/start", bytes.NewReader(body))
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	postReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(postReq)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("proxy to ingest failed: %v", err))
		return
	}
	defer resp.Body.Close()

	var ingStatus contracts.IngestStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&ingStatus); err != nil {
		s.writeError(w, resp.StatusCode, "failed to decode ingest response")
		return
	}

	s.writeJSON(w, resp.StatusCode, ingStatus)
}

// 9. POST /api/v1/cases/{id}/upload
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, "missing case id")
		return
	}

	if s.meta != nil {
		if _, ok := s.meta.GetCase(id); !ok {
			s.writeError(w, http.StatusNotFound, fmt.Sprintf("case %q not found", id))
			return
		}
	}

	mr, err := r.MultipartReader()
	if err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid multipart body: %v", err))
		return
	}

	targetDir := filepath.Join(s.cfg.StagingDir, id)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("create staging dir: %v", err))
		return
	}

	var savedPaths []string
	buf := make([]byte, 256*1024)

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.writeError(w, http.StatusBadRequest, fmt.Sprintf("read multipart part: %v", err))
			return
		}

		filename := part.FileName()
		if filename == "" {
			_ = part.Close()
			continue
		}

		cleanName := filepath.Base(filename)
		destPath := filepath.Join(targetDir, cleanName)

		outFile, err := os.Create(destPath)
		if err != nil {
			_ = part.Close()
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("create file: %v", err))
			return
		}

		_, copyErr := io.CopyBuffer(outFile, part, buf)
		_ = outFile.Close()
		_ = part.Close()

		if copyErr != nil {
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("write file: %v", copyErr))
			return
		}

		savedPaths = append(savedPaths, destPath)
	}

	if len(savedPaths) == 0 {
		s.writeError(w, http.StatusBadRequest, "no files uploaded")
		return
	}

	jobID := fmt.Sprintf("job-%d", time.Now().UnixNano())
	startReq := contracts.StartIngestRequest{
		JobID:     jobID,
		CaseID:    id,
		FilePaths: savedPaths,
	}

	body, _ := json.Marshal(startReq)
	postReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.cfg.IngestURL+"/internal/ingest/start", bytes.NewReader(body))
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	postReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(postReq)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("proxy to ingest failed: %v", err))
		return
	}
	defer resp.Body.Close()

	var ingStatus contracts.IngestStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&ingStatus); err != nil {
		s.writeError(w, resp.StatusCode, "failed to decode ingest response")
		return
	}

	s.writeJSON(w, resp.StatusCode, ingStatus)
}

// 10. GET /api/v1/jobs/{job_id}
func (s *Server) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	if jobID == "" {
		s.writeError(w, http.StatusBadRequest, "missing job id")
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.cfg.IngestURL+"/internal/ingest/status/"+jobID, nil)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("proxy to ingest status failed: %v", err))
		return
	}
	defer resp.Body.Close()

	var ingStatus contracts.IngestStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&ingStatus); err != nil {
		s.writeError(w, resp.StatusCode, "failed to decode ingest status")
		return
	}

	s.writeJSON(w, resp.StatusCode, ingStatus)
}

// 11. POST /api/v1/cases/{id}/analyze
func (s *Server) handleAnalyzeCase(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, "missing case id")
		return
	}

	var req contracts.AnalyzeCaseRequest
	bodyBytes, _ := io.ReadAll(r.Body)
	if len(bytes.TrimSpace(bodyBytes)) > 0 {
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid json body")
			return
		}
	}
	req.CaseID = id

	if err := req.Validate(); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 1) Вызов rca-engine (/internal/rca/analyze)
	rcaBody, _ := json.Marshal(req)
	rcaReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.cfg.RCAURL+"/internal/rca/analyze", bytes.NewReader(rcaBody))
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rcaReq.Header.Set("Content-Type", "application/json")

	rcaResp, err := s.httpClient.Do(rcaReq)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("rca-engine call failed: %v", err))
		return
	}
	defer rcaResp.Body.Close()

	if rcaResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(rcaResp.Body)
		s.writeError(w, rcaResp.StatusCode, fmt.Sprintf("rca-engine error: %s", string(b)))
		return
	}

	var analysis contracts.AnalyzeCaseResponse
	if err := json.NewDecoder(rcaResp.Body).Decode(&analysis); err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode rca response: %v", err))
		return
	}

	// 2) Проставляем RecurringCaseIDs через metadb.Store.FindRecurringCases
	if s.meta != nil {
		for i := range analysis.Hypotheses {
			hyp := &analysis.Hypotheses[i]
			recurring := s.meta.FindRecurringCases(id, hyp.TemplateID, hyp.SourcePlace)
			hyp.RecurringCaseIDs = recurring
		}
	}

	// 3) Вызов evidence-service (/internal/evidence/build)
	evReq := contracts.BuildEvidenceRequest{
		CaseID:   id,
		RunID:    analysis.RunID,
		Analysis: analysis,
	}
	evBody, _ := json.Marshal(evReq)
	evHTTPReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.cfg.EvidenceURL+"/internal/evidence/build", bytes.NewReader(evBody))
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	evHTTPReq.Header.Set("Content-Type", "application/json")

	evHTTPResp, err := s.httpClient.Do(evHTTPReq)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("evidence-service call failed: %v", err))
		return
	}
	defer evHTTPResp.Body.Close()

	if evHTTPResp.StatusCode == http.StatusOK {
		var buildResp contracts.BuildEvidenceResponse
		if err := json.NewDecoder(evHTTPResp.Body).Decode(&buildResp); err == nil {
			analysis.EvidenceItems = buildResp.EvidenceItems
			if s.meta != nil {
				_ = s.meta.SaveReport(buildResp)
			}
		}
	}

	// 4) Сохраняем AnalysisRun в metadb
	if s.meta != nil {
		_ = s.meta.SaveAnalysisRun(analysis)
	}

	analysis.Sanitize()
	s.writeJSON(w, http.StatusOK, analysis)
}

// 12. GET /api/v1/cases/{id}/analysis
func (s *Server) handleGetAnalysis(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, "missing case id")
		return
	}

	if s.meta == nil {
		s.writeError(w, http.StatusNotFound, "metadb store not initialized")
		return
	}

	analysis, ok := s.meta.GetAnalysisRun(id, "latest")
	if !ok {
		s.writeError(w, http.StatusNotFound, fmt.Sprintf("analysis not found for case %q", id))
		return
	}

	analysis.Sanitize()
	s.writeJSON(w, http.StatusOK, analysis)
}

// 13. GET /api/v1/cases/{id}/chain?key=...
func (s *Server) handleGetChain(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, "missing case id")
		return
	}

	key := r.URL.Query().Get("key")
	chainURL := fmt.Sprintf("%s/internal/rca/chain/%s?key=%s", s.cfg.RCAURL, id, url.QueryEscape(key))

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, chainURL, nil)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("proxy to rca chain failed: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		s.writeError(w, resp.StatusCode, string(b))
		return
	}

	var events []contracts.NormalizedEvent
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		events = []contracts.NormalizedEvent{}
	}
	s.writeJSON(w, http.StatusOK, events)
}

// 14. GET /api/v1/cases/{id}/logs/context?pod=...&ts=...&mode=pod|all_services
func (s *Server) handleGetContextLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, "missing case id")
		return
	}

	pod := r.URL.Query().Get("pod")
	ts := r.URL.Query().Get("ts")
	mode := r.URL.Query().Get("mode")

	if s.explorer == nil {
		s.writeError(w, http.StatusInternalServerError, "explorer not initialized")
		return
	}

	res, err := s.explorer.GetContextLogs(r.Context(), id, pod, ts, mode)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, res)
}

// 15. GET /api/v1/cases/{id}/reports/{run_id}/{kind}
func (s *Server) handleGetReportFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	runID := r.PathValue("run_id")
	kind := r.PathValue("kind")

	if id == "" || runID == "" || kind == "" {
		s.writeError(w, http.StatusBadRequest, "missing case id, run id, or kind")
		return
	}

	if runID == "latest" && s.meta != nil {
		if c, ok := s.meta.GetCase(id); ok && c.LatestRunID != "" {
			runID = c.LatestRunID
		}
	}

	var hasRep bool
	var rep contracts.BuildEvidenceResponse
	if s.meta != nil {
		rep, hasRep = s.meta.GetReport(id, runID)
	}

	runDir := filepath.Join(s.cfg.ReportsDir, id, runID)
	var filePath string
	var contentType string

	switch kind {
	case "markdown":
		contentType = "text/markdown; charset=utf-8"
		if hasRep && rep.MarkdownPath != "" && fileExists(rep.MarkdownPath) {
			filePath = rep.MarkdownPath
		} else {
			filePath = filepath.Join(runDir, "report.md")
		}
	case "html":
		contentType = "text/html; charset=utf-8"
		if hasRep && rep.HTMLPath != "" && fileExists(rep.HTMLPath) {
			filePath = rep.HTMLPath
		} else {
			filePath = filepath.Join(runDir, "report.html")
		}
	case "gemini":
		contentType = "text/markdown; charset=utf-8"
		if hasRep && rep.GeminiPath != "" && fileExists(rep.GeminiPath) {
			filePath = rep.GeminiPath
		} else {
			filePath = filepath.Join(runDir, "gemini_bundle.md")
			if !fileExists(filePath) {
				filePath = filepath.Join(runDir, "gemini_prompt.md")
			}
		}
	case "zip":
		contentType = "application/zip"
		if hasRep && rep.ZipPath != "" && fileExists(rep.ZipPath) {
			filePath = rep.ZipPath
		} else {
			filePath = filepath.Join(runDir, fmt.Sprintf("evidence_%s.zip", id))
			if !fileExists(filePath) {
				// Search for any zip in runDir
				matches, _ := filepath.Glob(filepath.Join(runDir, "*.zip"))
				if len(matches) > 0 {
					filePath = matches[0]
				}
			}
		}
	default:
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown report kind: %q", kind))
		return
	}

	if !fileExists(filePath) {
		s.writeError(w, http.StatusNotFound, fmt.Sprintf("report file not found for kind %q", kind))
		return
	}

	w.Header().Set("Content-Type", contentType)
	http.ServeFile(w, r, filePath)
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// 16. GET /api/v1/tilt/targets
func (s *Server) handleTiltTargets(w http.ResponseWriter, r *http.Request) {
	if s.cfg.TiltCollectorURL == "" {
		s.writeJSON(w, http.StatusOK, []contracts.TiltTarget{})
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.cfg.TiltCollectorURL+"/internal/tilt/targets", nil)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("proxy to tilt targets failed: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		s.writeError(w, resp.StatusCode, string(b))
		return
	}

	var targets []contracts.TiltTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		targets = []contracts.TiltTarget{}
	}
	s.writeJSON(w, http.StatusOK, targets)
}

// 17. POST /api/v1/cases/{id}/tilt/start
func (s *Server) handleTiltStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, "missing case id")
		return
	}

	var req contracts.TiltStartCaptureRequest
	bodyBytes, _ := io.ReadAll(r.Body)
	if len(bytes.TrimSpace(bodyBytes)) > 0 {
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid json body")
			return
		}
	}
	req.CaseID = id

	if err := req.Validate(); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	body, _ := json.Marshal(req)
	postReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.cfg.TiltCollectorURL+"/internal/tilt/start", bytes.NewReader(body))
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	postReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(postReq)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("tilt start failed: %v", err))
		return
	}
	defer resp.Body.Close()

	var tiltResp contracts.TiltStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&tiltResp); err != nil {
		s.writeError(w, resp.StatusCode, "decode tilt start response failed")
		return
	}

	s.writeJSON(w, resp.StatusCode, tiltResp)
}

// 18. POST /api/v1/cases/{id}/tilt/stop
func (s *Server) handleTiltStop(w http.ResponseWriter, r *http.Request) {
	postReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.cfg.TiltCollectorURL+"/internal/tilt/stop", nil)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp, err := s.httpClient.Do(postReq)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("tilt stop failed: %v", err))
		return
	}
	defer resp.Body.Close()

	var tiltResp contracts.TiltStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&tiltResp); err != nil {
		s.writeError(w, resp.StatusCode, "decode tilt stop response failed")
		return
	}

	s.writeJSON(w, resp.StatusCode, tiltResp)
}

// 19. GET /api/v1/cases/{id}/tilt/status
func (s *Server) handleTiltStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.cfg.TiltCollectorURL+"/internal/tilt/status", nil)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("tilt status failed: %v", err))
		return
	}
	defer resp.Body.Close()

	var tiltResp contracts.TiltStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&tiltResp); err != nil {
		s.writeError(w, resp.StatusCode, "decode tilt status response failed")
		return
	}

	if tiltResp.CaseID == "" {
		tiltResp.CaseID = id
	}

	s.writeJSON(w, resp.StatusCode, tiltResp)
}
