// <fork:proxy-circuit-breaker>
// Package-level registry that lets any service call the circuit breaker
// without touching the constructor chain (which is upstream-managed via
// wire_gen.go). Installation happens once at startup from cmd/server, and
// callers use GetProxyCircuitBreaker() with nil-safety.
//
// The registry is atomic and nil-safe; callers never crash if the breaker
// hasn't been installed (e.g. in unit tests or partial startup).
// </fork>

package service

import "sync/atomic"

var globalProxyCircuitBreaker atomic.Pointer[ProxyCircuitBreaker]

// SetProxyCircuitBreaker installs the singleton breaker. Called once from
// wire_gen after all dependencies are constructed. Passing nil clears it.
func SetProxyCircuitBreaker(b *ProxyCircuitBreaker) {
	globalProxyCircuitBreaker.Store(b)
}

// GetProxyCircuitBreaker returns the installed breaker or nil. Callers must
// nil-check before use; the returned pointer is also safe to use with any
// method call because the receiver methods themselves guard against nil.
func GetProxyCircuitBreaker() *ProxyCircuitBreaker {
	return globalProxyCircuitBreaker.Load()
}
