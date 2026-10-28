export interface StorageStatusResponse {
  raw_bytes: number;
  staging_bytes: number;
  agg_bytes: number;
  reports_bytes: number;
  total_bytes: number;
  quota_bytes: number;
  raw_quota_bytes: number;
}

export interface PurgeRawResponse {
  freed_bytes: number;
  duration_ms: number;
  status: string;
}

export interface AvailableFile {
  path: string;
  name: string;
  size_bytes: number;
  mod_time: string;
}

export interface CaseMeta {
  id: string;
  title: string;
  description: string;
  status: string;
  created_at: string;
  latest_run_id?: string;
}

export interface IngestStatusResponse {
  job_id: string;
  case_id: string;
  status: string;
  files_total: number;
  files_done: number;
  skipped_duplicate_files: number;
  rows_ingested: number;
  bytes_read: number;
  clock_skew_warnings: string[];
  error?: string;
}

export interface SignalBreakdown {
  trace_chain_score: number;
  onset_score: number;
  lead_lag_score: number;
  novelty_score: number;
  causal_dir_score: number;
  graph_root_score: number;
  localization_score: number;
  change_corr_score: number;
}

export interface LeadLagLink {
  peer_service: string;
  peer_template: number;
  lag_seconds: number;
  correlation_r: number;
}

export interface Hypothesis {
  id: string;
  rank: number;
  role: "ROOT_CAUSE" | "PROPAGATION" | "SYMPTOM" | "NOISE";
  confidence: number;
  service: string;
  template_id: number;
  template_text: string;
  error_class: string;
  source_place: string;
  dominant_pod: string;
  pod_skew_ratio: number;
  dominant_route: string;
  dominant_version: string;
  onset_time: string;
  baseline_rate_per_min: number;
  incident_rate_per_min: number;
  growth_ratio: number;
  poisson_p_value: number;
  is_constant_noise: boolean;
  recurring_case_ids?: string[];
  lead_lag_peers: LeadLagLink[];
  signals: SignalBreakdown;
  explanation: string;
  evidence_ids: string[];
}

export interface ServiceEdge {
  case_id: string;
  caller_service: string;
  callee_service: string;
  evidence_source: string;
  call_count: number;
  error_count: number;
}

export interface ClockSkewFinding {
  service: string;
  pod: string;
  skew_ms: number;
  sample_raw_ts: string;
  sample_app_ts: string;
}

export interface TimelinePoint {
  bucket: string;
  service: string;
  level: string;
  events_count: number;
  max_latency_ns: number;
}

export interface EvidenceItem {
  id: string;
  kind: string;
  service: string;
  summary: string;
  timestamp: string;
  source_file: string;
  source_record_idx: number;
  sha256: string;
  redacted_line: string;
  logql_snippet: string;
}

export interface AnalyzeCaseResponse {
  case_id: string;
  run_id: string;
  incident_start: string;
  incident_end: string;
  hypotheses: Hypothesis[];
  service_edges: ServiceEdge[];
  clock_skew_findings: ClockSkewFinding[];
  timeline: TimelinePoint[];
  evidence_items?: EvidenceItem[];
}

export interface CreateCaseRequest {
  title: string;
  description: string;
}

export interface IngestLocalRequest {
  file_paths: string[];
}

export interface AnalyzeCaseRequest {
  case_id: string;
  incident_start?: string;
  incident_end?: string;
  baseline_start?: string;
  baseline_end?: string;
  use_historical_baseline: boolean;
}

export interface NormalizedEvent {
  case_id: string;
  event_id: string;
  ts: string;
  app_ts?: string;
  clock_skew_ms: number;
  service: string;
  environment: string;
  pod: string;
  node: string;
  service_version: string;
  level: string;
  severity_num: number;
  schema_kind: string;
  template_id: number;
  message: string;
  error_class: string;
  error_msg: string;
  exception_type: string;
  source_place: string;
  stack_fingerprint: number;
  trace_id: string;
  span_id: string;
  request_id: string;
  session_id: string;
  operator_id: string;
  visitor_id: string;
  http_method: string;
  http_route: string;
  http_status: number;
  latency_ns: number;
  peer_service: string;
  source_file: string;
  source_record_idx: number;
  raw_hash: number;
  raw_redacted: string;
}

export interface ContextLogsResponse {
  raw_purged: boolean;
  mode: "pod" | "all_services";
  events: NormalizedEvent[];
}

export interface TiltTarget {
  id: string;
  runtime: string;
  service_name: string;
  pod_name: string;
  namespace: string;
  status: string;
  labels?: Record<string, string>;
}

export interface TiltStartCaptureRequest {
  case_id: string;
  target_ids?: string[];
  since_seconds: number;
}

export interface TiltStatusResponse {
  case_id?: string;
  active: boolean;
  started_at?: string;
  targets_watched: number;
  lines_captured: number;
  batches_flushed: number;
  last_error?: string;
  targets?: TiltTarget[];
}

export class ApiClient {
  private baseUrl: string;

  constructor(baseUrl: string = '/api/v1') {
    this.baseUrl = baseUrl.endsWith('/') ? baseUrl.slice(0, -1) : baseUrl;
  }

  private async request<T>(path: string, options?: RequestInit): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`, {
      ...options,
      headers: {
        ...(options?.headers),
      },
    });

    if (!res.ok) {
      let errMsg = `HTTP ${res.status} ${res.statusText}`;
      try {
        const data = await res.json();
        if (data && typeof data === 'object') {
          if ('error' in data && typeof data.error === 'string') {
            errMsg = data.error;
          } else if ('message' in data && typeof data.message === 'string') {
            errMsg = data.message;
          }
        }
      } catch {
        // use default HTTP error
      }
      throw new Error(errMsg);
    }

    return res.json() as Promise<T>;
  }

  async getHealth(): Promise<{ status: string }> {
    return this.request<{ status: string }>('/health');
  }

  async getStorageStatus(): Promise<StorageStatusResponse> {
    return this.request<StorageStatusResponse>('/storage/status');
  }

  async purgeRaw(): Promise<PurgeRawResponse> {
    return this.request<PurgeRawResponse>('/storage/purge-raw', {
      method: 'POST',
    });
  }

  async getAvailableFiles(): Promise<AvailableFile[]> {
    return this.request<AvailableFile[]>('/files/available');
  }

  async getCases(): Promise<CaseMeta[]> {
    return this.request<CaseMeta[]>('/cases');
  }

  async createCase(req: CreateCaseRequest): Promise<CaseMeta> {
    return this.request<CaseMeta>('/cases', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    });
  }

  async deleteCase(caseId: string): Promise<{ status: string }> {
    return this.request<{ status: string }>(`/cases/${encodeURIComponent(caseId)}`, {
      method: 'DELETE',
    });
  }

  async ingestLocal(caseId: string, req: IngestLocalRequest): Promise<IngestStatusResponse> {
    return this.request<IngestStatusResponse>(`/cases/${encodeURIComponent(caseId)}/ingest-local`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    });
  }

  async uploadFile(caseId: string, files: File[] | FileList): Promise<IngestStatusResponse> {
    const formData = new FormData();
    const fileList = Array.from(files);
    for (const file of fileList) {
      formData.append('files', file, file.name);
    }
    return this.request<IngestStatusResponse>(`/cases/${encodeURIComponent(caseId)}/upload`, {
      method: 'POST',
      body: formData,
    });
  }

  async getJobStatus(jobId: string): Promise<IngestStatusResponse> {
    return this.request<IngestStatusResponse>(`/jobs/${encodeURIComponent(jobId)}`);
  }

  async analyzeCase(caseId: string, req: AnalyzeCaseRequest): Promise<AnalyzeCaseResponse> {
    return this.request<AnalyzeCaseResponse>(`/cases/${encodeURIComponent(caseId)}/analyze`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    });
  }

  async getAnalysis(caseId: string): Promise<AnalyzeCaseResponse> {
    return this.request<AnalyzeCaseResponse>(`/cases/${encodeURIComponent(caseId)}/analysis`);
  }

  async getChain(caseId: string, key: string): Promise<NormalizedEvent[]> {
    const q = new URLSearchParams({ key }).toString();
    return this.request<NormalizedEvent[]>(`/cases/${encodeURIComponent(caseId)}/chain?${q}`);
  }

  async getContextLogs(
    caseId: string,
    pod: string,
    ts: string,
    mode: 'pod' | 'all_services' = 'pod'
  ): Promise<ContextLogsResponse> {
    const q = new URLSearchParams({ pod, ts, mode }).toString();
    return this.request<ContextLogsResponse>(`/cases/${encodeURIComponent(caseId)}/logs/context?${q}`);
  }

  async getTiltTargets(): Promise<TiltTarget[]> {
    return this.request<TiltTarget[]>('/tilt/targets');
  }

  async startTiltCapture(
    caseId: string,
    sinceSeconds: number = 300,
    targetIds?: string[]
  ): Promise<TiltStatusResponse> {
    const payload: TiltStartCaptureRequest = {
      case_id: caseId,
      since_seconds: sinceSeconds,
      ...(targetIds && targetIds.length > 0 ? { target_ids: targetIds } : {}),
    };
    return this.request<TiltStatusResponse>(`/cases/${encodeURIComponent(caseId)}/tilt/start`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
  }

  async stopTiltCapture(caseId: string): Promise<TiltStatusResponse> {
    return this.request<TiltStatusResponse>(`/cases/${encodeURIComponent(caseId)}/tilt/stop`, {
      method: 'POST',
    });
  }

  async getTiltStatus(caseId: string): Promise<TiltStatusResponse> {
    return this.request<TiltStatusResponse>(`/cases/${encodeURIComponent(caseId)}/tilt/status`);
  }

  getReportUrl(caseId: string, runId: string, kind: 'markdown' | 'html' | 'gemini' | 'zip'): string {
    return `${this.baseUrl}/cases/${encodeURIComponent(caseId)}/reports/${encodeURIComponent(runId)}/${encodeURIComponent(kind)}`;
  }
}

export const api = new ApiClient();
