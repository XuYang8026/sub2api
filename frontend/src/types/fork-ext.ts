// <fork:proxy-circuit-breaker> + <fork:proxy-smart-import>
// Fork-only frontend types. Kept separate from types/index.ts so upstream
// changes to the base DTOs don't collide with fork additions.

// Backend GET /admin/proxies/health returns an array of these; the ProxiesView
// polls it and merges into the per-proxy row via a client-side map keyed by
// proxy_id.
export interface ProxyHealthSnapshot {
  proxy_id: number
  health_status: 'unknown' | 'healthy' | 'unhealthy' | 'probing'
  last_probed_at?: string | null
  last_probe_error?: string
  last_probe_latency_ms?: number | null
  consecutive_failures: number
  unhealthy_since?: string | null
}

// Smart batch import result item shape.
export interface SmartBatchImportResultItem {
  original?: string
  host: string
  port: number
  status: 'created' | 'skipped' | 'detect_failed'
  protocol?: string
  detected_protocol?: string
  detected_latency_ms?: number
  detection_error?: string
  error?: string
  reason?: string
}

export interface SmartBatchImportResponse {
  created: number
  skipped: number
  errored: number
  items: SmartBatchImportResultItem[]
}

// </fork>
