package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/ingest-service/internal/chwriter"
	"github.com/local/logs-analyzer/services/ingest-service/internal/classifier"
	"github.com/local/logs-analyzer/services/ingest-service/internal/drain"
	"github.com/local/logs-analyzer/services/ingest-service/internal/normalizer"
	"github.com/local/logs-analyzer/services/ingest-service/internal/reader"
)

type Server struct {
	writer     *chwriter.Writer
	miner      *drain.Miner
	importDir  string
	exampleDir string
	mu         sync.RWMutex
	jobs       map[string]*contracts.IngestStatusResponse
}

func NewServer(writer *chwriter.Writer, importDir, exampleDir string) *Server {
	return &Server{
		writer:     writer,
		miner:      drain.NewMiner(),
		importDir:  importDir,
		exampleDir: exampleDir,
		jobs:       make(map[string]*contracts.IngestStatusResponse),
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /internal/ingest/browse", s.handleBrowse)
	mux.HandleFunc("POST /internal/ingest/start", s.handleStart)
	mux.HandleFunc("GET /internal/ingest/status/", s.handleStatus)
	mux.HandleFunc("POST /internal/ingest/batch", s.handleBatch)
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

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	var available []contracts.AvailableFile
	dirs := []string{s.importDir, s.exampleDir}

	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(dir); err != nil {
			continue
		}

		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d == nil || d.IsDir() {
				return nil
			}

			name := d.Name()
			if strings.HasPrefix(name, ".") {
				return nil
			}

			lower := strings.ToLower(name)
			isSupported := strings.HasSuffix(lower, ".json") ||
				strings.HasSuffix(lower, ".jsonl") ||
				strings.HasSuffix(lower, ".ndjson") ||
				strings.HasSuffix(lower, ".gz") ||
				strings.HasSuffix(lower, ".zip")

			if !isSupported {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return nil
			}

			available = append(available, contracts.AvailableFile{
				Path:      path,
				Name:      name,
				SizeBytes: info.Size(),
				ModTime:   info.ModTime().UTC(),
			})
			return nil
		})
	}

	if available == nil {
		available = []contracts.AvailableFile{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(available)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	var req contracts.StartIngestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}

	if err := req.Validate(); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	initialStatus := &contracts.IngestStatusResponse{
		JobID:                 req.JobID,
		CaseID:                req.CaseID,
		Status:                contracts.IngestStatusRunning,
		FilesTotal:            len(req.FilePaths),
		FilesDone:             0,
		SkippedDuplicateFiles: 0,
		RowsIngested:          0,
		BytesRead:             0,
		ClockSkewWarnings:     make([]string, 0),
	}
	s.jobs[req.JobID] = initialStatus
	s.mu.Unlock()

	go s.runIngestJob(req.JobID, req.CaseID, req.FilePaths)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(initialStatus)
}

func (s *Server) runIngestJob(jobID, caseID string, filePaths []string) {
	ctx := context.Background()

	for _, filePath := range filePaths {
		sha, size, err := reader.ComputeFileSHA256(filePath)
		if err != nil {
			s.failJob(jobID, fmt.Sprintf("hash file %s: %v", filePath, err))
			return
		}

		s.mu.Lock()
		if job, ok := s.jobs[jobID]; ok {
			job.BytesRead += uint64(size)
		}
		s.mu.Unlock()

		already, err := s.writer.IsFileAlreadyIngested(ctx, caseID, sha)
		if err != nil {
			s.failJob(jobID, fmt.Sprintf("check duplicate %s: %v", filePath, err))
			return
		}

		if already {
			s.mu.Lock()
			if job, ok := s.jobs[jobID]; ok {
				job.SkippedDuplicateFiles++
				job.FilesDone++
			}
			s.mu.Unlock()
			continue
		}

		batchEvents := make([]contracts.NormalizedEvent, 0, 5000)
		batchTemplates := make([]contracts.TemplateRecord, 0, 5000)
		batchIDEdges := make([]contracts.IdentityEdge, 0, 5000)
		batchSrvEdges := make([]contracts.ServiceEdge, 0, 5000)
		var fileRows uint64
		seenWarnings := make(map[string]bool)

		flush := func() error {
			if len(batchEvents) == 0 && len(batchTemplates) == 0 && len(batchIDEdges) == 0 && len(batchSrvEdges) == 0 {
				return nil
			}
			err := s.writer.FlushBatch(ctx, batchEvents, batchTemplates, batchIDEdges, batchSrvEdges)
			if err != nil {
				return err
			}
			batchEvents = batchEvents[:0]
			batchTemplates = batchTemplates[:0]
			batchIDEdges = batchIDEdges[:0]
			batchSrvEdges = batchSrvEdges[:0]
			return nil
		}

		streamErr := reader.StreamPath(ctx, filePath, func(rec contracts.RawScriptRecord, sourceFile string, recordIdx uint32) error {
			ev, parsedJSON := normalizer.NormalizeRecord(caseID, rec, sourceFile, recordIdx)
			idEdges, srvEdge := classifier.EnrichAndClassify(&ev, parsedJSON, rec.Line)
			tplRec := s.miner.Mine(&ev)

			absSkew := ev.ClockSkewMs
			if absSkew < 0 {
				absSkew = -absSkew
			}
			if absSkew > 60000 {
				warn := fmt.Sprintf("service %s has clock skew %dms", ev.Service, ev.ClockSkewMs)
				if !seenWarnings[warn] {
					seenWarnings[warn] = true
					s.addJobWarning(jobID, warn)
				}
			}

			batchEvents = append(batchEvents, ev)
			batchTemplates = append(batchTemplates, tplRec)
			if len(idEdges) > 0 {
				batchIDEdges = append(batchIDEdges, idEdges...)
			}
			if srvEdge != nil {
				batchSrvEdges = append(batchSrvEdges, *srvEdge)
			}

			fileRows++
			s.addJobRows(jobID, 1)

			if len(batchEvents) >= 5000 {
				return flush()
			}
			return nil
		})

		if streamErr != nil {
			s.failJob(jobID, fmt.Sprintf("stream file %s: %v", filePath, streamErr))
			return
		}

		if err := flush(); err != nil {
			s.failJob(jobID, fmt.Sprintf("flush final batch %s: %v", filePath, err))
			return
		}

		if err := s.writer.RecordIngestedFile(ctx, caseID, sha, filepath.Base(filePath), fileRows); err != nil {
			s.failJob(jobID, fmt.Sprintf("record ingested file %s: %v", filePath, err))
			return
		}

		s.mu.Lock()
		if job, ok := s.jobs[jobID]; ok {
			job.FilesDone++
		}
		s.mu.Unlock()
	}

	s.completeJob(jobID)
}

func (s *Server) failJob(jobID, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job, ok := s.jobs[jobID]; ok {
		job.Status = contracts.IngestStatusFailed
		job.Error = errMsg
	}
}

func (s *Server) completeJob(jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job, ok := s.jobs[jobID]; ok {
		job.Status = contracts.IngestStatusCompleted
	}
}

func (s *Server) addJobWarning(jobID, warning string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job, ok := s.jobs[jobID]; ok {
		job.ClockSkewWarnings = append(job.ClockSkewWarnings, warning)
	}
}

func (s *Server) addJobRows(jobID string, n uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job, ok := s.jobs[jobID]; ok {
		job.RowsIngested += n
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimPrefix(r.URL.Path, "/internal/ingest/status/")
	if jobID == "" {
		http.Error(w, `{"error":"missing job_id"}`, http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	job, exists := s.jobs[jobID]
	var respCopy contracts.IngestStatusResponse
	if exists && job != nil {
		respCopy = *job
		respCopy.ClockSkewWarnings = make([]string, len(job.ClockSkewWarnings))
		copy(respCopy.ClockSkewWarnings, job.ClockSkewWarnings)
	}
	s.mu.RUnlock()

	if !exists {
		http.Error(w, `{"error":"job not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(respCopy)
}

func (s *Server) handleBatch(w http.ResponseWriter, r *http.Request) {
	var req contracts.IngestBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}

	if err := req.Validate(); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}

	var warnings []string
	seenWarnings := make(map[string]bool)

	batchEvents := make([]contracts.NormalizedEvent, 0, len(req.Records))
	batchTemplates := make([]contracts.TemplateRecord, 0, len(req.Records))
	batchIDEdges := make([]contracts.IdentityEdge, 0, len(req.Records))
	batchSrvEdges := make([]contracts.ServiceEdge, 0, len(req.Records))

	for idx, rec := range req.Records {
		ev, parsedJSON := normalizer.NormalizeRecord(req.CaseID, rec, req.SourceName, uint32(idx))
		idEdges, srvEdge := classifier.EnrichAndClassify(&ev, parsedJSON, rec.Line)
		tplRec := s.miner.Mine(&ev)

		absSkew := ev.ClockSkewMs
		if absSkew < 0 {
			absSkew = -absSkew
		}
		if absSkew > 60000 {
			warn := fmt.Sprintf("service %s has clock skew %dms", ev.Service, ev.ClockSkewMs)
			if !seenWarnings[warn] {
				seenWarnings[warn] = true
				warnings = append(warnings, warn)
			}
		}

		batchEvents = append(batchEvents, ev)
		batchTemplates = append(batchTemplates, tplRec)
		if len(idEdges) > 0 {
			batchIDEdges = append(batchIDEdges, idEdges...)
		}
		if srvEdge != nil {
			batchSrvEdges = append(batchSrvEdges, *srvEdge)
		}
	}

	if err := s.writer.FlushBatch(r.Context(), batchEvents, batchTemplates, batchIDEdges, batchSrvEdges); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	resp := contracts.IngestBatchResponse{
		RowsIngested:      uint64(len(req.Records)),
		ClockSkewWarnings: warnings,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
