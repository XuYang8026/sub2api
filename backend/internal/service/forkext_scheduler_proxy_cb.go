package service

import (
	"context"
	"log/slog"
)

// <fork:proxy-circuit-breaker>
//
// Sidecar wiring for the proxy circuit breaker's integration with the
// scheduler. The upstream gateway_service.go only knows about the generic
// hooks (SchedulerAccountFilter / StickyPreCheck / ForwardFailureHook) and
// stays agnostic of circuit-breaker semantics.
//
// This file is fork-only; deleting it removes the circuit breaker from the
// scheduling path with no upstream changes required.

func init() {
	RegisterSchedulerAccountFilter(filterAccountsByProxyHealth)
	RegisterStickyPreCheck(stickyProxyPreCheck)
	RegisterForwardFailureHook(handleForwardProxyError)
}

// filterAccountsByProxyHealth removes accounts whose bound proxy is currently
// tripped by the proxy circuit breaker. Accounts with no bound proxy always
// pass through. Nil-safe: with no breaker registered the input is returned
// unchanged.
func filterAccountsByProxyHealth(ctx context.Context, accounts []Account, groupID *int64, platform string) []Account {
	if len(accounts) == 0 {
		return accounts
	}
	cb := GetProxyCircuitBreaker()
	if cb == nil {
		return accounts
	}
	proxyIDs := make([]int64, 0, len(accounts))
	seen := make(map[int64]struct{}, len(accounts))
	for _, acc := range accounts {
		if acc.ProxyID == nil {
			continue
		}
		pid := *acc.ProxyID
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}
		proxyIDs = append(proxyIDs, pid)
	}
	if len(proxyIDs) == 0 {
		return accounts
	}
	healthyIDs := cb.FilterHealthyProxyIDs(ctx, proxyIDs)
	if len(healthyIDs) == len(proxyIDs) {
		// No proxy was unhealthy; short-circuit.
		return accounts
	}
	healthySet := make(map[int64]struct{}, len(healthyIDs))
	for _, id := range healthyIDs {
		healthySet[id] = struct{}{}
	}
	filtered := make([]Account, 0, len(accounts))
	for _, acc := range accounts {
		if acc.ProxyID == nil {
			filtered = append(filtered, acc)
			continue
		}
		if _, ok := healthySet[*acc.ProxyID]; ok {
			filtered = append(filtered, acc)
		}
	}
	if len(filtered) < len(accounts) {
		slog.Info("proxy_cb.scheduler_filtered",
			"original", len(accounts),
			"filtered", len(filtered),
			"platform", platform,
			"groupID", derefGroupID(groupID))
	}
	return filtered
}

// stickyProxyPreCheck evicts a sticky binding whose bound proxy is unhealthy.
// If it evicts, the log line + cache delete are performed here so the
// upstream sticky code path stays free of fork-specific side effects.
func stickyProxyPreCheck(ctx context.Context, account *Account, groupID *int64, sessionHash string, cache GatewayCache, path string) bool {
	if account == nil || account.ProxyID == nil || *account.ProxyID <= 0 {
		return false
	}
	cb := GetProxyCircuitBreaker()
	if cb == nil {
		return false
	}
	if !cb.IsProxyUnhealthy(ctx, *account.ProxyID) {
		return false
	}
	slog.Info("sticky.evicted_proxy_unhealthy",
		"account_id", account.ID,
		"proxy_id", derefInt64(account.ProxyID),
		"session", shortSessionHash(sessionHash),
		"path", path,
	)
	if cache != nil {
		_ = cache.DeleteSessionAccountID(ctx, derefGroupID(groupID), sessionHash)
	}
	return true
}

// handleForwardProxyError classifies a forward error as proxy-attributable
// (proxyconnect refused / socks connect / TLS handshake, etc.); if so, marks
// the account temp_unschedulable and increments the proxy failure counter.
// Non-proxy errors fall through untouched so the existing upstream_error
// path handles them.
func handleForwardProxyError(ctx context.Context, account *Account, err error) {
	if err == nil || account == nil {
		return
	}
	cb := GetProxyCircuitBreaker()
	if cb == nil {
		return
	}
	cb.HandleAccountProxyError(ctx, account, err)
}

// </fork:proxy-circuit-breaker>
