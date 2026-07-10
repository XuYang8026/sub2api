package service

import (
	"time"

	"github.com/google/wire"
)

// <fork:proxy-circuit-breaker> + <fork:proxy-smart-import>
//
// ForkExtSet is the wire ProviderSet for fork-only service components. It is
// included in cmd/server/wire.go alongside the upstream service.ProviderSet
// so fork extensions do not require edits to service/wire.go.

var ForkExtSet = wire.NewSet(
	ProvideProxyCircuitBreakerAndRegister,
	ProvideScheduledProxyProbeService,
	ProvideProxyProtocolDetector,
)

// ProvideProxyProtocolDetector adapts the exported AdminService interface to
// the fork-only ProxyProtocolDetector via a runtime type assertion. Safe
// because *adminServiceImpl (the sole AdminService impl) provides
// DetectProxyProtocol in admin_service_forkext_detect.go.
func ProvideProxyProtocolDetector(admin AdminService) ProxyProtocolDetector {
	if d, ok := admin.(ProxyProtocolDetector); ok {
		return d
	}
	return nil
}

// RuntimeSchedulingBlocker → *OpenAIGatewayService is bound in
// cmd/server/wire.go so it shares scope with the upstream ProviderSet.

// ProvideProxyCircuitBreakerAndRegister constructs the circuit breaker AND
// registers it as the global singleton so scheduler filters / sticky
// pre-checks / forward failure hooks (registered from forkext_*.go files
// via init()) can look it up without a wire dependency of their own.
func ProvideProxyCircuitBreakerAndRegister(
	accountRepo AccountRepository,
	proxyRepo ProxyRepository,
	proxyHealth ProxyHealthRepository,
	tempUnschedCache TempUnschedCache,
	runtimeBlocker RuntimeSchedulingBlocker,
) *ProxyCircuitBreaker {
	cb := NewProxyCircuitBreaker(accountRepo, proxyRepo, proxyHealth, tempUnschedCache, runtimeBlocker)
	SetProxyCircuitBreaker(cb)
	return cb
}

// ProvideScheduledProxyProbeService constructs the periodic probe service
// with a 5-minute interval and immediately starts it. Callers wiring cleanup
// should register a Stop() call.
func ProvideScheduledProxyProbeService(
	healthRepo ProxyHealthRepository,
	proxyRepo ProxyRepository,
	prober ProxyExitInfoProber,
) *ScheduledProxyProbeService {
	s := NewScheduledProxyProbeService(healthRepo, proxyRepo, prober, 5*time.Minute)
	s.Start()
	return s
}

// </fork>
