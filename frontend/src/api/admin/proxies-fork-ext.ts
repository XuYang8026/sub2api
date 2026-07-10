// <fork:proxy-circuit-breaker> + <fork:proxy-smart-import>
//
// Sidecar API client for fork-only proxy endpoints. Kept out of proxies.ts
// so upstream can iterate on the base proxy client without breaking fork
// contracts.
//
// - GET  /admin/proxies/health       — health snapshot for every active proxy
// - POST /admin/proxies/batch-smart  — smart batch import (auto-detect protocol)

import { apiClient } from '@/api'
import type { ProxyHealthSnapshot, SmartBatchImportResponse } from '@/types/fork-ext'

export async function getProxyHealth(): Promise<ProxyHealthSnapshot[]> {
  const { data } = await apiClient.get<ProxyHealthSnapshot[]>('/admin/proxies/health')
  return Array.isArray(data) ? data : []
}

export async function smartBatchImport(
  proxies: Array<{
    protocol?: string // '' or 'auto' triggers auto-detection
    host: string
    port: number
    username?: string
    password?: string
    original?: string
  }>
): Promise<SmartBatchImportResponse> {
  const { data } = await apiClient.post<SmartBatchImportResponse>(
    '/admin/proxies/batch-smart',
    { proxies }
  )
  return data
}

export const proxiesForkExtAPI = {
  getProxyHealth,
  smartBatchImport
}

// </fork>
