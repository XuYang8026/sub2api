package service

import (
	"context"
	"log/slog"
)

// <fork:proxy-circuit-breaker>
//
// Sidecar wiring for the proxy circuit breaker's integration with the
// token refresh service. When refreshWithRetry exhausts its retries and the
// cause is proxy-attributable, delegate to the circuit breaker which sets a
// longer 15-min cooldown and increments the proxy failure counter — instead
// of the default 10-min block that would otherwise overwrite this state in
// a race.

func init() {
	RegisterTokenRefreshFailureHook(deferTokenRefreshToCircuitBreaker)
}

func deferTokenRefreshToCircuitBreaker(ctx context.Context, account *Account, lastErr error) bool {
	if lastErr == nil || account == nil {
		return false
	}
	cb := GetProxyCircuitBreaker()
	if cb == nil {
		return false
	}
	class := cb.HandleAccountProxyError(ctx, account, lastErr)
	if class == ProxyErrorNone {
		return false
	}
	slog.Info("token_refresh.deferred_to_proxy_circuit_breaker",
		"account_id", account.ID,
		"class", string(class),
	)
	return true
}

// </fork:proxy-circuit-breaker>
