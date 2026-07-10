package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"

	"github.com/google/wire"
)

// <fork:proxy-circuit-breaker> + <fork:proxy-smart-import>
//
// ForkExtSet is the wire ProviderSet for fork-only handler components.
// Included from cmd/server/wire.go — the upstream handler.ProviderSet stays
// unchanged.

var ForkExtSet = wire.NewSet(
	admin.NewProxyHealthHandler,
	admin.NewProxySmartImportHandler,
	NewAdminHandlersForkExt,
)

// </fork>
