// <fork:proxy-circuit-breaker> + <fork:proxy-smart-import>
// Fork-only English translations. Merged into the main en locale by
// i18n/index.ts.

export default {
  admin: {
    accounts: {
      proxy_unavailable: 'Proxy unavailable'
    },
    proxies: {
      columns: {
        health: 'Health'
      },
      health_status: {
        healthy: 'Healthy',
        unhealthy: 'Unavailable',
        unknown: 'Unknown',
        probing: 'Probing'
      },
      probe_now: 'Probe now',
      auto_detect_protocol: 'Auto-detect',
      batch_import_result_title: 'Import Result',
      batch_import_detected_protocol: 'Detected protocol: {protocol}',
      batch_import_latency_ms: '{latency}ms',
      // per-row status labels shown in the import result table.
      // `detect_failed` = row was saved but the protocol is a guess
      // (auto-detection failed) — rendered with an amber warning badge.
      batch_status: {
        created: 'Created',
        skipped: 'Skipped',
        detect_failed: 'Detect failed',
        failed: 'Failed'
      }
    }
  }
}

// </fork>
