package repository

import (
	"github.com/google/wire"
)

// <fork:proxy-circuit-breaker>
//
// ForkExtSet is the wire ProviderSet for fork-only repository components.
// Included from cmd/server/wire.go — leaves the upstream repository.ProviderSet
// unmodified.
//
// NewProxyHealthRepository directly returns service.ProxyHealthRepository so
// no wire.Bind is required.

var ForkExtSet = wire.NewSet(
	NewProxyHealthRepository,
)

// </fork>
