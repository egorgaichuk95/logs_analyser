package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/local/logs-analyzer/pkg/contracts"
	"github.com/local/logs-analyzer/services/tilt-collector/internal/collector"
	"github.com/local/logs-analyzer/services/tilt-collector/internal/discovery"
)

type Server struct {
	runtime *discovery.RuntimeClient
	manager *collector.Manager
}

func NewServer(runtime *discovery.RuntimeClient, manager *collector.Manager) *Server {
	return &Server{
		runtime: runtime,
		manager: manager,
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /internal/tilt/targets", s.handleTargets)
	mux.HandleFunc("POST /internal/tilt/start", s.handleStart)
	mux.HandleFunc("POST /internal/tilt/stop", s.handleStop)
	mux.HandleFunc("GET /internal/tilt/status", s.handleStatus)
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

func (s *Server) handleTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := s.runtime.DiscoverTargets(r.Context())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if targets == nil {
		targets = []contracts.TiltTarget{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(targets)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	var req contracts.TiltStartCaptureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json body"})
		return
	}

	if err := req.Validate(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	status, err := s.manager.StartCapture(r.Context(), req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("start capture: %v", err)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(status)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	status := s.manager.StopCapture()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(status)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := s.manager.GetStatus()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(status)
}
