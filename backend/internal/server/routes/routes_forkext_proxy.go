package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"

	"github.com/gin-gonic/gin"
)

// <fork:proxy-circuit-breaker> + <fork:proxy-smart-import>
//
// Sidecar route registrar for fork-only proxy endpoints. Kept out of
// admin.go so upstream can rename / refactor its route groups without
// touching fork state.

func init() {
	RegisterAdminRouteRegistrar(registerForkExtProxyRoutes)
}

func registerForkExtProxyRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	if h == nil || h.Admin == nil || h.Admin.ForkExt == nil {
		return
	}
	ext := h.Admin.ForkExt
	proxies := admin.Group("/proxies")
	if ext.ProxyHealth != nil {
		// Health snapshot for all active proxies (single round-trip).
		// Frontend polls this to overlay health badges on the proxy table.
		proxies.GET("/health", ext.ProxyHealth.GetHealth)
	}
	if ext.ProxySmartImport != nil {
		// Smart batch import (auto-detect http vs socks5). The upstream
		// POST /admin/proxies/batch endpoint (strict, required protocol)
		// stays untouched; the frontend targets the -smart variant when
		// the operator opts in via the auto-detect toggle.
		proxies.POST("/batch-smart", ext.ProxySmartImport.SmartBatchCreate)
	}
}

// </fork>
