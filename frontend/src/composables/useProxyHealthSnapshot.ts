// <fork:proxy-circuit-breaker>
// Shared health snapshot cache used by components that need to reason about
// which proxies are currently marked unhealthy by the fork's proxy circuit
// breaker (backend GET /admin/proxies/health). Fetches lazily on first use;
// callers can trigger a manual refresh via the returned `refresh` fn.
//
// Kept as a module-scoped singleton (rather than a Pinia store) so it stays
// contained in the fork surface and does not touch the upstream store
// registry.

import { computed, ref } from 'vue'

import type { ProxyHealthSnapshot } from '@/types/fork-ext'
import { getProxyHealth } from '@/api/admin/proxies-fork-ext'

const snapshots = ref<ProxyHealthSnapshot[]>([])
let hasFetched = false
let inflight: Promise<void> | null = null

async function refresh(): Promise<void> {
  if (inflight) {
    return inflight
  }
  inflight = (async () => {
    try {
      snapshots.value = await getProxyHealth()
      hasFetched = true
    } catch {
      // Endpoint may be missing in a minimal deployment; keep the last
      // known snapshot to avoid clearing already-rendered badges.
    } finally {
      inflight = null
    }
  })()
  return inflight
}

const unhealthyProxyIds = computed<Set<number>>(() => {
  const s = new Set<number>()
  for (const snap of snapshots.value) {
    if (snap.health_status === 'unhealthy') {
      s.add(snap.proxy_id)
    }
  }
  return s
})

export function useProxyHealthSnapshot() {
  if (!hasFetched && !inflight) {
    void refresh()
  }
  return {
    snapshots,
    unhealthyProxyIds,
    refresh
  }
}

export function isProxyUnhealthy(proxyId: number | null | undefined): boolean {
  if (proxyId == null) return false
  return unhealthyProxyIds.value.has(proxyId)
}

// </fork>
