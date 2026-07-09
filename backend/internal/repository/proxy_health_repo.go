// <fork:proxy-circuit-breaker>
// Package-local shadow repository that reads/writes the auto-circuit-breaker
// columns added by migration 159 without touching the upstream ent schema
// definition. This keeps `backend/ent/schema/proxy.go` and its ~200 generated
// files untouched, so upstream syncs never conflict on ent code.
//
// If upstream later adopts a similar feature, we simply remove this file and
// switch callers to the upstream API.
// </fork>

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// proxyHealthRepository implements service.ProxyHealthRepository backed by
// raw SQL against the columns added in migration 159.
type proxyHealthRepository struct {
	sql sqlExecutor
}

// NewProxyHealthRepository constructs a fork-local repo talking to the same
// underlying sql.DB used by proxyRepository.
func NewProxyHealthRepository(sqlDB *sql.DB) service.ProxyHealthRepository {
	return &proxyHealthRepository{sql: sqlDB}
}

const maxProxyProbeErrorLen = 500

// LoadHealth returns the health snapshot for a single proxy id. If the row
// exists but health columns are still at defaults ('unknown'), returns a
// snapshot with Status=ProxyHealthUnknown.
func (r *proxyHealthRepository) LoadHealth(ctx context.Context, id int64) (*service.ProxyHealthSnapshot, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id, COALESCE(health_status,'unknown'), last_probed_at, last_probe_error,
		       last_probe_latency_ms, COALESCE(consecutive_failures,0), unhealthy_since
		FROM proxies
		WHERE id=$1 AND deleted_at IS NULL
		LIMIT 1
	`, id)
	if err != nil {
		return nil, fmt.Errorf("proxy_health load: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	snap := &service.ProxyHealthSnapshot{}
	var probeErr sql.NullString
	var probedAt, unhealthySince sql.NullTime
	var latency sql.NullInt64
	if err := rows.Scan(&snap.ProxyID, &snap.Status, &probedAt, &probeErr, &latency, &snap.ConsecutiveFailures, &unhealthySince); err != nil {
		return nil, fmt.Errorf("proxy_health load scan: %w", err)
	}
	if probedAt.Valid {
		t := probedAt.Time
		snap.LastProbedAt = &t
	}
	if probeErr.Valid {
		snap.LastProbeError = probeErr.String
	}
	if latency.Valid {
		v := latency.Int64
		snap.LastProbeLatencyMs = &v
	}
	if unhealthySince.Valid {
		t := unhealthySince.Time
		snap.UnhealthySince = &t
	}
	return snap, nil
}

// LoadHealthByIDs returns snapshots for a batch of ids in one round trip.
// Ids not found in DB are silently skipped.
func (r *proxyHealthRepository) LoadHealthByIDs(ctx context.Context, ids []int64) (map[int64]*service.ProxyHealthSnapshot, error) {
	if len(ids) == 0 {
		return map[int64]*service.ProxyHealthSnapshot{}, nil
	}
	// PostgreSQL ANY-array binding is safer than string concat for id lists.
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id, COALESCE(health_status,'unknown'), last_probed_at, last_probe_error,
		       last_probe_latency_ms, COALESCE(consecutive_failures,0), unhealthy_since
		FROM proxies
		WHERE id = ANY($1) AND deleted_at IS NULL
	`, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("proxy_health load_many: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]*service.ProxyHealthSnapshot, len(ids))
	for rows.Next() {
		snap := &service.ProxyHealthSnapshot{}
		var probeErr sql.NullString
		var probedAt, unhealthySince sql.NullTime
		var latency sql.NullInt64
		if err := rows.Scan(&snap.ProxyID, &snap.Status, &probedAt, &probeErr, &latency, &snap.ConsecutiveFailures, &unhealthySince); err != nil {
			return nil, fmt.Errorf("proxy_health load_many scan: %w", err)
		}
		if probedAt.Valid {
			t := probedAt.Time
			snap.LastProbedAt = &t
		}
		if probeErr.Valid {
			snap.LastProbeError = probeErr.String
		}
		if latency.Valid {
			v := latency.Int64
			snap.LastProbeLatencyMs = &v
		}
		if unhealthySince.Valid {
			t := unhealthySince.Time
			snap.UnhealthySince = &t
		}
		out[snap.ProxyID] = snap
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proxy_health load_many rows: %w", err)
	}
	return out, nil
}

// MarkHealthy records a successful probe. Resets consecutive_failures to 0
// and clears unhealthy_since / last_probe_error.
func (r *proxyHealthRepository) MarkHealthy(ctx context.Context, id int64, latencyMs int64, at time.Time) error {
	_, err := r.sql.ExecContext(ctx, `
		UPDATE proxies
		SET health_status = 'healthy',
		    last_probed_at = $2,
		    last_probe_error = NULL,
		    last_probe_latency_ms = $3,
		    consecutive_failures = 0,
		    unhealthy_since = NULL,
		    updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, id, at.UTC(), latencyMs)
	if err != nil {
		return fmt.Errorf("proxy_health mark_healthy: %w", err)
	}
	return nil
}

// MarkUnhealthy records a failed probe or a circuit-breaker trip.
// unhealthy_since is set only on the transition from healthy/unknown to
// unhealthy (preserves the original timestamp on repeated failures).
func (r *proxyHealthRepository) MarkUnhealthy(ctx context.Context, id int64, probeErr string, at time.Time) error {
	probeErr = truncateProbeError(probeErr, maxProxyProbeErrorLen)
	_, err := r.sql.ExecContext(ctx, `
		UPDATE proxies
		SET health_status = 'unhealthy',
		    last_probed_at = $2,
		    last_probe_error = $3,
		    consecutive_failures = consecutive_failures + 1,
		    unhealthy_since = COALESCE(unhealthy_since, $2),
		    updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, id, at.UTC(), probeErr)
	if err != nil {
		return fmt.Errorf("proxy_health mark_unhealthy: %w", err)
	}
	return nil
}

// IncrementFailure records one account-side proxy-error attributed to this
// proxy WITHOUT probing. Used by the account-level path to accumulate
// evidence before tripping the circuit breaker.
// Returns the new consecutive_failures count (0 if the proxy no longer exists).
func (r *proxyHealthRepository) IncrementFailure(ctx context.Context, id int64, at time.Time) (int, error) {
	_ = at // reserved for future last_failure_at column; column not present yet
	rows, err := r.sql.QueryContext(ctx, `
		UPDATE proxies
		SET consecutive_failures = consecutive_failures + 1,
		    updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING consecutive_failures
	`, id)
	if err != nil {
		return 0, fmt.Errorf("proxy_health increment_failure: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return 0, nil
	}
	var count int
	if err := rows.Scan(&count); err != nil {
		return 0, fmt.Errorf("proxy_health increment_failure scan: %w", err)
	}
	return count, nil
}

// ListActiveIDs returns proxy IDs where status='active' AND not soft-deleted,
// used by the periodic probe cron.
func (r *proxyHealthRepository) ListActiveIDs(ctx context.Context) ([]int64, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id FROM proxies
		WHERE status='active' AND deleted_at IS NULL
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("proxy_health list_active: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListUnhealthyIDs returns proxy IDs currently marked unhealthy, used to
// (a) filter scheduling and (b) surface the fleet's health in admin UI.
func (r *proxyHealthRepository) ListUnhealthyIDs(ctx context.Context) ([]int64, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id FROM proxies
		WHERE health_status='unhealthy' AND deleted_at IS NULL
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("proxy_health list_unhealthy: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListAccountIDsByProxyID returns account IDs currently bound to the given
// proxy (via accounts.proxy_id). Used for cascade notifications when a proxy
// flips to unhealthy.
func (r *proxyHealthRepository) ListAccountIDsByProxyID(ctx context.Context, proxyID int64) ([]int64, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id FROM accounts
		WHERE proxy_id = $1 AND deleted_at IS NULL
		ORDER BY id
	`, proxyID)
	if err != nil {
		return nil, fmt.Errorf("proxy_health list_accounts: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// truncateProbeError trims probeErr to at most maxBytes bytes, respecting
// UTF-8 rune boundaries so callers never persist a broken multibyte prefix
// (e.g. a Chinese message truncated mid-codepoint). Appends "..." when a
// trim actually occurs.
func truncateProbeError(probeErr string, maxBytes int) string {
	if len(probeErr) <= maxBytes {
		return probeErr
	}
	cut := maxBytes
	// Walk back until we land on a valid rune boundary. DecodeLastRuneInString
	// returns RuneError with size=1 for invalid bytes; guard the loop so we
	// never underflow.
	for cut > 0 {
		r, size := utf8.DecodeLastRuneInString(probeErr[:cut])
		if r != utf8.RuneError || size != 1 {
			break
		}
		cut -= size
	}
	return probeErr[:cut] + "..."
}
