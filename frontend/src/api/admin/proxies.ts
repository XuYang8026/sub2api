/**
 * Admin Proxies API endpoints
 * Handles proxy server management for administrators
 */

import { apiClient } from '../client'
import type {
  Proxy,
  ProxyAccountSummary,
  ProxyQualityCheckResult,
  CreateProxyRequest,
  UpdateProxyRequest,
  PaginatedResponse,
  AdminDataPayload,
  AdminDataImportResult
} from '@/types'

/**
 * List all proxies with pagination
 * @param page - Page number (default: 1)
 * @param pageSize - Items per page (default: 20)
 * @param filters - Optional filters
 * @returns Paginated list of proxies
 */
export async function list(
  page: number = 1,
  pageSize: number = 20,
  filters?: {
    protocol?: string
    status?: 'active' | 'inactive' | 'expired'
    search?: string
    sort_by?: string
    sort_order?: 'asc' | 'desc'
  },
  options?: {
    signal?: AbortSignal
  }
): Promise<PaginatedResponse<Proxy>> {
  const { data } = await apiClient.get<PaginatedResponse<Proxy>>('/admin/proxies', {
    params: {
      page,
      page_size: pageSize,
      ...filters
    },
    signal: options?.signal
  })
  return data
}

/**
 * Get all active proxies (without pagination)
 * @returns List of all active proxies
 */
export async function getAll(): Promise<Proxy[]> {
  const { data } = await apiClient.get<Proxy[]>('/admin/proxies/all')
  return data
}

/**
 * Get all active proxies with account count (sorted by creation time desc)
 * @returns List of all active proxies with account count
 */
export async function getAllWithCount(): Promise<Proxy[]> {
  const { data } = await apiClient.get<Proxy[]>('/admin/proxies/all', {
    params: { with_count: 'true' }
  })
  return data
}

/**
 * Get proxy by ID
 * @param id - Proxy ID
 * @returns Proxy details
 */
export async function getById(id: number): Promise<Proxy> {
  const { data } = await apiClient.get<Proxy>(`/admin/proxies/${id}`)
  return data
}

/**
 * Create new proxy
 * @param proxyData - Proxy data
 * @returns Created proxy
 */
export async function create(proxyData: CreateProxyRequest): Promise<Proxy> {
  const { data } = await apiClient.post<Proxy>('/admin/proxies', proxyData)
  return data
}

/**
 * Update proxy
 * @param id - Proxy ID
 * @param updates - Fields to update
 * @returns Updated proxy
 */
export async function update(id: number, updates: UpdateProxyRequest): Promise<Proxy> {
  const { data } = await apiClient.put<Proxy>(`/admin/proxies/${id}`, updates)
  return data
}

/**
 * Delete proxy
 * @param id - Proxy ID
 * @returns Success confirmation
 */
export async function deleteProxy(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(`/admin/proxies/${id}`)
  return data
}

/**
 * Toggle proxy status
 * @param id - Proxy ID
 * @param status - New status
 * @returns Updated proxy
 */
export async function toggleStatus(id: number, status: 'active' | 'inactive'): Promise<Proxy> {
  return update(id, { status })
}

/**
 * Test proxy connectivity
 * @param id - Proxy ID
 * @returns Test result with IP info
 */
export async function testProxy(id: number): Promise<{
  success: boolean
  message: string
  latency_ms?: number
  ip_address?: string
  city?: string
  region?: string
  country?: string
  country_code?: string
}> {
  const { data } = await apiClient.post<{
    success: boolean
    message: string
    latency_ms?: number
    ip_address?: string
    city?: string
    region?: string
    country?: string
    country_code?: string
  }>(`/admin/proxies/${id}/test`)
  return data
}

/**
 * Check proxy quality across common AI targets
 * @param id - Proxy ID
 * @returns Quality check result
 */
export async function checkProxyQuality(id: number): Promise<ProxyQualityCheckResult> {
  const { data } = await apiClient.post<ProxyQualityCheckResult>(`/admin/proxies/${id}/quality-check`)
  return data
}

/**
 * Get proxy usage statistics
 * @param id - Proxy ID
 * @returns Proxy usage statistics
 */
export async function getStats(id: number): Promise<{
  total_accounts: number
  active_accounts: number
  total_requests: number
  success_rate: number
  average_latency: number
}> {
  const { data } = await apiClient.get<{
    total_accounts: number
    active_accounts: number
    total_requests: number
    success_rate: number
    average_latency: number
  }>(`/admin/proxies/${id}/stats`)
  return data
}

/**
 * Get accounts using a proxy
 * @param id - Proxy ID
 * @returns List of accounts using the proxy
 */
export async function getProxyAccounts(id: number): Promise<ProxyAccountSummary[]> {
  const { data } = await apiClient.get<ProxyAccountSummary[]>(`/admin/proxies/${id}/accounts`)
  return data
}

/**
 * Batch create proxies
 * @param proxies - Array of proxy data to create
 * @returns Creation result with count of created and skipped, plus per-item detected details
 */
// <fork:proxy-smart-import> extended batch response with per-item detected
// protocol/latency. `original` echoes the caller-supplied raw line so the UI
// can render the input unchanged; it falls back to "host:port" server-side
// when omitted, so it is effectively always present but typed as optional to
// stay compatible with older backend builds.
// `status` values:
//   - 'created'       — persisted successfully
//   - 'skipped'       — duplicate / transient error / create-race
//   - 'detect_failed' — auto-detection failed; row is still saved with a
//                       fallback protocol (http) so the operator can decide
//                       whether to keep or correct it
//   - 'failed'        — reserved for future use (kept in the union for
//                       backward compat with early builds)
// `reason` is the human-readable per-row explanation; older backend builds
// may still emit `error` instead, so both are accepted.
export interface BatchCreateItemResult {
  original?: string
  host?: string
  port?: number
  status: 'created' | 'skipped' | 'detect_failed' | 'failed'
  protocol?: string
  detected_protocol?: string
  detected_latency_ms?: number
  detection_error?: string
  reason?: string
  error?: string
}

export interface BatchCreateResult {
  created: number
  skipped: number
  errored?: number
  items?: BatchCreateItemResult[]
}

export async function batchCreate(
  proxies: Array<{
    protocol: string
    host: string
    port: number
    username?: string
    password?: string
    original?: string
  }>
): Promise<BatchCreateResult> {
  const { data } = await apiClient.post<BatchCreateResult>('/admin/proxies/batch', { proxies })
  return data
}

/**
 * Get proxy health snapshot map (id -> health fields).
 * Prefer the dedicated /health endpoint when available; falls back to /all payload.
 */
// <fork:proxy-circuit-breaker> health snapshot map
export interface ProxyHealthSnapshot {
  id: number
  health_status?: 'unknown' | 'healthy' | 'unhealthy' | 'probing'
  last_probed_at?: string | null
  last_probe_error?: string
  last_probe_latency_ms?: number | null
  consecutive_failures?: number
  unhealthy_since?: string | null
}

export async function getHealth(): Promise<ProxyHealthSnapshot[]> {
  try {
    const { data } = await apiClient.get<ProxyHealthSnapshot[]>('/admin/proxies/health')
    return Array.isArray(data) ? data : []
  } catch (error) {
    // Endpoint may not be deployed; degrade to /all which now includes health fields.
    try {
      const { data } = await apiClient.get<Proxy[]>('/admin/proxies/all')
      return (Array.isArray(data) ? data : []).map((p) => ({
        id: p.id,
        health_status: p.health_status,
        last_probed_at: p.last_probed_at,
        last_probe_error: p.last_probe_error,
        last_probe_latency_ms: p.last_probe_latency_ms,
        consecutive_failures: p.consecutive_failures,
        unhealthy_since: p.unhealthy_since
      }))
    } catch {
      throw error
    }
  }
}

export async function batchDelete(ids: number[]): Promise<{
  deleted_ids: number[]
  skipped: Array<{ id: number; reason: string }>
}> {
  const { data } = await apiClient.post<{
    deleted_ids: number[]
    skipped: Array<{ id: number; reason: string }>
  }>('/admin/proxies/batch-delete', { ids })
  return data
}

export async function exportData(options?: {
  ids?: number[]
  filters?: {
    protocol?: string
    status?: 'active' | 'inactive' | 'expired'
    search?: string
    sort_by?: string
    sort_order?: 'asc' | 'desc'
  }
}): Promise<AdminDataPayload> {
  const params: Record<string, string> = {}
  if (options?.ids && options.ids.length > 0) {
    params.ids = options.ids.join(',')
  } else if (options?.filters) {
    const { protocol, status, search, sort_by, sort_order } = options.filters
    if (protocol) params.protocol = protocol
    if (status) params.status = status
    if (search) params.search = search
    if (sort_by) params.sort_by = sort_by
    if (sort_order) params.sort_order = sort_order
  }
  const { data } = await apiClient.get<AdminDataPayload>('/admin/proxies/data', { params })
  return data
}

export async function importData(payload: {
  data: AdminDataPayload
}): Promise<AdminDataImportResult> {
  const { data } = await apiClient.post<AdminDataImportResult>('/admin/proxies/data', payload)
  return data
}

export const proxiesAPI = {
  list,
  getAll,
  getAllWithCount,
  getById,
  create,
  update,
  delete: deleteProxy,
  toggleStatus,
  testProxy,
  checkProxyQuality,
  getStats,
  getProxyAccounts,
  batchCreate,
  batchDelete,
  exportData,
  importData,
  // <fork:proxy-circuit-breaker>
  getHealth
}

export default proxiesAPI
