package contracts

import (
	"errors"
	"time"
)

const (
	IngestStatusQueued    = "QUEUED"
	IngestStatusRunning   = "RUNNING"
	IngestStatusCompleted = "COMPLETED"
	IngestStatusFailed    = "FAILED"

	CaseStatusCreated   = "CREATED"
	CaseStatusIngesting = "INGESTING"
	CaseStatusReady     = "READY"
	CaseStatusAnalyzed  = "ANALYZED"
	CaseStatusRawPurged = "RAW_PURGED"

	ContextModePod         = "pod"
	ContextModeAllServices = "all_services"

	RuntimeDocker = "docker"
	RuntimeK8s    = "k8s"
)

var (
	ErrEmptyCaseID    = errors.New("case_id cannot be empty")
	ErrEmptyJobID     = errors.New("job_id cannot be empty")
	ErrEmptyFilePaths = errors.New("file_paths cannot be empty")
	ErrEmptyTitle     = errors.New("title cannot be empty")
	ErrEmptyRecords   = errors.New("records cannot be empty")
)

type StartIngestRequest struct {
	JobID     string   `json:"job_id"`
	CaseID    string   `json:"case_id"`
	FilePaths []string `json:"file_paths"`
}

type IngestStatusResponse struct {
	JobID                 string   `json:"job_id"`
	CaseID                string   `json:"case_id"`
	Status                string   `json:"status"` // QUEUED, RUNNING, COMPLETED, FAILED
	FilesTotal            int      `json:"files_total"`
	FilesDone             int      `json:"files_done"`
	SkippedDuplicateFiles int      `json:"skipped_duplicate_files"`
	RowsIngested          uint64   `json:"rows_ingested"`
	BytesRead             uint64   `json:"bytes_read"`
	ClockSkewWarnings     []string `json:"clock_skew_warnings"`
	Error                 string   `json:"error,omitempty"`
}

type AvailableFile struct {
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	SizeBytes int64     `json:"size_bytes"`
	ModTime   time.Time `json:"mod_time"`
}

type AnalyzeCaseRequest struct {
	CaseID                string     `json:"case_id"`
	IncidentStart         *time.Time `json:"incident_start,omitempty"`
	IncidentEnd           *time.Time `json:"incident_end,omitempty"`
	BaselineStart         *time.Time `json:"baseline_start,omitempty"`
	BaselineEnd           *time.Time `json:"baseline_end,omitempty"`
	UseHistoricalBaseline bool       `json:"use_historical_baseline"`
}

type TimelinePoint struct {
	Bucket      time.Time `json:"bucket"`
	Service     string    `json:"service"`
	Level       string    `json:"level"`
	EventsCount uint64    `json:"events_count"`
	MaxLatency  uint64    `json:"max_latency_ns"`
}

type AnalyzeCaseResponse struct {
	CaseID            string             `json:"case_id"`
	RunID             string             `json:"run_id"`
	IncidentStart     time.Time          `json:"incident_start"`
	IncidentEnd       time.Time          `json:"incident_end"`
	Hypotheses        []Hypothesis       `json:"hypotheses"`
	ServiceEdges      []ServiceEdge      `json:"service_edges"`
	ClockSkewFindings []ClockSkewFinding `json:"clock_skew_findings"`
	Timeline          []TimelinePoint    `json:"timeline"`
	EvidenceItems     []EvidenceItem     `json:"evidence_items,omitempty"`
}

type BuildEvidenceRequest struct {
	CaseID   string              `json:"case_id"`
	RunID    string              `json:"run_id"`
	Analysis AnalyzeCaseResponse `json:"analysis"`
}

type BuildEvidenceResponse struct {
	CaseID        string         `json:"case_id"`
	RunID         string         `json:"run_id"`
	ReportDir     string         `json:"report_dir"`
	MarkdownPath  string         `json:"markdown_path"`
	HTMLPath      string         `json:"html_path"`
	GeminiPath    string         `json:"gemini_path"`
	ZipPath       string         `json:"zip_path"`
	SizeBytes     int64          `json:"size_bytes"`
	EvidenceItems []EvidenceItem `json:"evidence_items"`
}

type StorageStatusResponse struct {
	RawBytes      int64 `json:"raw_bytes"`
	StagingBytes  int64 `json:"staging_bytes"`
	AggBytes      int64 `json:"agg_bytes"`
	ReportsBytes  int64 `json:"reports_bytes"`
	TotalBytes    int64 `json:"total_bytes"`
	QuotaBytes    int64 `json:"quota_bytes"`
	RawQuotaBytes int64 `json:"raw_quota_bytes"`
}

type PurgeRawResponse struct {
	FreedBytes int64  `json:"freed_bytes"`
	DurationMs int64  `json:"duration_ms"`
	Status     string `json:"status"`
}

type CaseMeta struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	IncidentStart *time.Time `json:"incident_start,omitempty"`
	IncidentEnd   *time.Time `json:"incident_end,omitempty"`
	BaselineStart *time.Time `json:"baseline_start,omitempty"`
	BaselineEnd   *time.Time `json:"baseline_end,omitempty"`
	Pinned        bool       `json:"pinned"`
	Status        string     `json:"status"` // CREATED, INGESTING, READY, ANALYZED, RAW_PURGED
	CreatedAt     time.Time  `json:"created_at"`
	LatestRunID   string     `json:"latest_run_id,omitempty"`
}

type CreateCaseRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type IngestLocalRequest struct {
	FilePaths []string `json:"file_paths"`
}

type ContextLogsResponse struct {
	RawPurged bool              `json:"raw_purged"`
	Mode      string            `json:"mode"` // "pod" or "all_services"
	Events    []NormalizedEvent `json:"events"`
}

// --- Live Tilt / Local Runtime Capture DTOs (tilt-collector :8084 <-> ingest-service :8081 <-> gateway-api :8080) ---

type IngestBatchRequest struct {
	CaseID     string            `json:"case_id"`
	SourceName string            `json:"source_name"` // например "tilt:chat-session-7f9c"
	Records    []RawScriptRecord `json:"records"`
}

type IngestBatchResponse struct {
	RowsIngested      uint64   `json:"rows_ingested"`
	ClockSkewWarnings []string `json:"clock_skew_warnings,omitempty"`
}

type TiltTarget struct {
	ID          string            `json:"id"`           // container_id или namespace/pod/container
	Runtime     string            `json:"runtime"`      // "docker" или "k8s"
	ServiceName string            `json:"service_name"` // нормализованное имя микросервиса
	PodName     string            `json:"pod_name"`
	Namespace   string            `json:"namespace"`
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type TiltStartCaptureRequest struct {
	CaseID       string   `json:"case_id"`
	TargetIDs    []string `json:"target_ids,omitempty"` // если пусто — все обнаруженные сервисы Tilt
	SinceSeconds int      `json:"since_seconds"`        // например 300 (захватить последние 5 минут + live-хвост)
}

type TiltStatusResponse struct {
	CaseID         string       `json:"case_id,omitempty"`
	Active         bool         `json:"active"`
	StartedAt      *time.Time   `json:"started_at,omitempty"`
	TargetsWatched int          `json:"targets_watched"`
	LinesCaptured  uint64       `json:"lines_captured"`
	BatchesFlushed uint64       `json:"batches_flushed"`
	LastError      string       `json:"last_error,omitempty"`
	Targets        []TiltTarget `json:"targets,omitempty"`
}

// Sanitize защищает вложенные гипотезы AnalyzeCaseResponse от NaN и Inf
func (r *AnalyzeCaseResponse) Sanitize() {
	for i := range r.Hypotheses {
		r.Hypotheses[i].Sanitize()
	}
}

// Validate проверяет валидность параметров StartIngestRequest
func (r *StartIngestRequest) Validate() error {
	if r.CaseID == "" {
		return ErrEmptyCaseID
	}
	if r.JobID == "" {
		return ErrEmptyJobID
	}
	if len(r.FilePaths) == 0 {
		return ErrEmptyFilePaths
	}
	return nil
}

// Validate проверяет валидность параметров AnalyzeCaseRequest
func (r *AnalyzeCaseRequest) Validate() error {
	if r.CaseID == "" {
		return ErrEmptyCaseID
	}
	return nil
}

// Validate проверяет валидность параметров CreateCaseRequest
func (r *CreateCaseRequest) Validate() error {
	if r.Title == "" {
		return ErrEmptyTitle
	}
	return nil
}

// Validate проверяет валидность параметров IngestLocalRequest
func (r *IngestLocalRequest) Validate() error {
	if len(r.FilePaths) == 0 {
		return ErrEmptyFilePaths
	}
	return nil
}

// Validate проверяет валидность параметров IngestBatchRequest
func (r *IngestBatchRequest) Validate() error {
	if r.CaseID == "" {
		return ErrEmptyCaseID
	}
	if len(r.Records) == 0 {
		return ErrEmptyRecords
	}
	return nil
}

// Validate проверяет валидность параметров TiltStartCaptureRequest
func (r *TiltStartCaptureRequest) Validate() error {
	if r.CaseID == "" {
		return ErrEmptyCaseID
	}
	return nil
}
