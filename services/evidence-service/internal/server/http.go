package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/evidence-service/internal/collector"
	"github.com/local/logs-analyzer/services/evidence-service/internal/reporter"
)

type Server struct {
	chURL      string
	reportsDir string
	client     *http.Client
}

func NewServer(chURL string, reportsDir string) *Server {
	return &Server{
		chURL:      strings.TrimRight(chURL, "/"),
		reportsDir: reportsDir,
		client:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /internal/evidence/build", s.handleBuild)
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

func (s *Server) handleBuild(w http.ResponseWriter, r *http.Request) {
	var req contracts.BuildEvidenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid json body: %v", err), http.StatusBadRequest)
		return
	}

	if req.CaseID == "" {
		http.Error(w, contracts.ErrEmptyCaseID.Error(), http.StatusBadRequest)
		return
	}

	items := req.Analysis.EvidenceItems
	if len(items) == 0 && s.chURL != "" {
		collected, err := collector.CollectEvidence(r.Context(), s.chURL, &req.Analysis)
		if err == nil && len(collected) > 0 {
			items = collected
			req.Analysis.EvidenceItems = collected
		}
	}

	resp, err := reporter.BuildAllArtifacts(s.reportsDir, req, items)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to build artifacts: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
