-- <fork:proxy-circuit-breaker>
-- Add proxy health-tracking columns for the auto-circuit-breaker feature.
-- Design: keep the existing `status` column ('active'/'inactive'/'expired')
-- untouched — that stays the admin-managed lifecycle field. This migration
-- introduces a parallel `health_status` for the system-managed availability
-- signal, along with rolling failure/probe metadata.

ALTER TABLE proxies
    ADD COLUMN IF NOT EXISTS health_status VARCHAR(20) NOT NULL DEFAULT 'unknown',
    ADD COLUMN IF NOT EXISTS last_probed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_probe_error TEXT,
    ADD COLUMN IF NOT EXISTS last_probe_latency_ms INTEGER,
    ADD COLUMN IF NOT EXISTS consecutive_failures INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS unhealthy_since TIMESTAMPTZ;

-- Valid health_status values (checked at application layer for flexibility):
--   'unknown'   – never probed yet, treat as healthy (do not skip)
--   'healthy'   – last probe succeeded
--   'unhealthy' – circuit breaker tripped (probe failed OR ≥3 accounts reported proxy errors within window)
--   'probing'   – reserved for probe-in-progress marker (not used by cron)
COMMENT ON COLUMN proxies.health_status IS 'System-managed availability flag: unknown | healthy | unhealthy | probing';
COMMENT ON COLUMN proxies.last_probed_at IS 'Last cron/on-demand probe completion time';
COMMENT ON COLUMN proxies.last_probe_error IS 'Last probe error message (truncated); NULL if last probe succeeded';
COMMENT ON COLUMN proxies.last_probe_latency_ms IS 'Last successful probe RTT in milliseconds';
COMMENT ON COLUMN proxies.consecutive_failures IS 'Streak of consecutive failed probes; resets to 0 on success';
COMMENT ON COLUMN proxies.unhealthy_since IS 'Timestamp when health_status transitioned to unhealthy; NULL when healthy/unknown';
