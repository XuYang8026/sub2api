// <fork:proxy-circuit-breaker>
// Public service types + interface for proxy health tracking. The
// implementation is split into:
//   - proxy_error_classifier.go — pure error → class mapping
//   - proxy_circuit_breaker.go  — account-level + proxy-level state machine
//   - scheduled_proxy_probe.go  — periodic cron probing all active proxies
//
// This file defines the shared vocabulary all three use, plus the repository
// interface that repository/proxy_health_repo.go implements.
// </fork>

package service

import (
	"context"
	"time"
)

// Proxy health status constants (system-managed, distinct from Proxy.Status
// which is admin-managed). Kept as bare strings to match DB CHECK-free
// storage; validation is intentionally soft.
const (
	ProxyHealthUnknown   = "unknown"
	ProxyHealthHealthy   = "healthy"
	ProxyHealthUnhealthy = "unhealthy"
	ProxyHealthProbing   = "probing"
)

// ProxyHealthSnapshot is a read-model of the health columns on the proxies
// table. Nil pointer fields mean the DB column is NULL.
type ProxyHealthSnapshot struct {
	ProxyID             int64      `json:"proxy_id"`
	Status              string     `json:"status"`
	LastProbedAt        *time.Time `json:"last_probed_at,omitempty"`
	LastProbeError      string     `json:"last_probe_error,omitempty"`
	LastProbeLatencyMs  *int64     `json:"last_probe_latency_ms,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	UnhealthySince      *time.Time `json:"unhealthy_since,omitempty"`
}

// IsUnhealthy reports whether the proxy should be excluded from scheduling.
// Unknown is treated as healthy (do NOT skip): brand-new proxies start at
// unknown until their first probe, and blocking them by default would break
// fresh deployments.
func (s *ProxyHealthSnapshot) IsUnhealthy() bool {
	if s == nil {
		return false
	}
	return s.Status == ProxyHealthUnhealthy
}

// ProxyHealthRepository is the storage contract for the auto-circuit-breaker
// feature. Implementation lives in repository/proxy_health_repo.go.
type ProxyHealthRepository interface {
	LoadHealth(ctx context.Context, id int64) (*ProxyHealthSnapshot, error)
	LoadHealthByIDs(ctx context.Context, ids []int64) (map[int64]*ProxyHealthSnapshot, error)
	MarkHealthy(ctx context.Context, id int64, latencyMs int64, at time.Time) error
	MarkUnhealthy(ctx context.Context, id int64, probeErr string, at time.Time) error
	IncrementFailure(ctx context.Context, id int64, at time.Time) (int, error)
	ListActiveIDs(ctx context.Context) ([]int64, error)
	ListUnhealthyIDs(ctx context.Context) ([]int64, error)
	ListAccountIDsByProxyID(ctx context.Context, proxyID int64) ([]int64, error)
}

// RuntimeSchedulingBlocker is the subset of the runtime-notification hook
// used by ProxyCircuitBreaker. The real implementation is RateLimitService
// (see ratelimit_service.go's AccountRuntimeBlocker), which takes *Account
// directly — matching that shape here lets wire_gen.go pass rateLimitService
// with no adapter.
//
// Kept in this file (rather than proxy_circuit_breaker.go) because both
// caller and callee live in the service package and the interface is part of
// the fork's public wiring contract.
type RuntimeSchedulingBlocker interface {
	BlockAccountScheduling(account *Account, until time.Time, reason string)
}
