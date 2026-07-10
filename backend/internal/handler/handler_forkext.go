package handler

import "github.com/Wei-Shaw/sub2api/internal/handler/admin"

// <fork:proxy-circuit-breaker> + <fork:proxy-smart-import>
//
// AdminHandlersForkExt bundles all fork-only admin handlers so the upstream
// AdminHandlers struct grows only by a single optional embed field.
type AdminHandlersForkExt struct {
	ProxyHealth      *admin.ProxyHealthHandler
	ProxySmartImport *admin.ProxySmartImportHandler
}

// NewAdminHandlersForkExt is the wire provider for the sidecar handler bundle.
func NewAdminHandlersForkExt(
	proxyHealth *admin.ProxyHealthHandler,
	proxySmartImport *admin.ProxySmartImportHandler,
) *AdminHandlersForkExt {
	return &AdminHandlersForkExt{
		ProxyHealth:      proxyHealth,
		ProxySmartImport: proxySmartImport,
	}
}

// </fork>
