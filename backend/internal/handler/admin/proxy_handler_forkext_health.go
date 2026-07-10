package admin

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// <fork:proxy-circuit-breaker>
//
// Sidecar handler for the proxy circuit-breaker's admin endpoints. The
// upstream ProxyHandler stays free of proxy_health dependencies; the
// frontend fetches the health snapshot from a separate endpoint and merges
// it client-side.

// ProxyHealthHandler exposes the shadow ProxyHealthRepository over the admin
// API. Fork-only.
type ProxyHealthHandler struct {
	proxyHealth service.ProxyHealthRepository
}

// NewProxyHealthHandler creates a new sidecar handler for proxy health.
// proxyHealth may be nil (e.g. tests or minimal wire graphs) — endpoints
// return an empty snapshot in that case.
func NewProxyHealthHandler(proxyHealth service.ProxyHealthRepository) *ProxyHealthHandler {
	return &ProxyHealthHandler{proxyHealth: proxyHealth}
}

// ProxyHealthResponseItem is the payload shape returned by GET /admin/proxies/health.
// Matches the frontend contract in frontend/src/api/admin/proxies-fork-ext.ts.
type ProxyHealthResponseItem struct {
	ProxyID             int64      `json:"proxy_id"`
	HealthStatus        string     `json:"health_status"`
	LastProbedAt        *time.Time `json:"last_probed_at,omitempty"`
	LastProbeError      string     `json:"last_probe_error,omitempty"`
	LastProbeLatencyMs  *int64     `json:"last_probe_latency_ms,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	UnhealthySince      *time.Time `json:"unhealthy_since,omitempty"`
}

// GetHealth returns the health snapshot for every currently-active proxy.
// GET /api/v1/admin/proxies/health
//
// Response is a flat array (no pagination) since the fleet is small and the
// frontend polls this to overlay health badges on the proxy table.
func (h *ProxyHealthHandler) GetHealth(c *gin.Context) {
	if h.proxyHealth == nil {
		response.Success(c, []ProxyHealthResponseItem{})
		return
	}
	ctx := c.Request.Context()
	ids, err := h.proxyHealth.ListActiveIDs(ctx)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if len(ids) == 0 {
		response.Success(c, []ProxyHealthResponseItem{})
		return
	}
	snaps, err := h.proxyHealth.LoadHealthByIDs(ctx, ids)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]ProxyHealthResponseItem, 0, len(ids))
	for _, id := range ids {
		snap, ok := snaps[id]
		if !ok || snap == nil {
			out = append(out, ProxyHealthResponseItem{
				ProxyID:      id,
				HealthStatus: service.ProxyHealthUnknown,
			})
			continue
		}
		out = append(out, ProxyHealthResponseItem{
			ProxyID:             snap.ProxyID,
			HealthStatus:        snap.Status,
			LastProbedAt:        snap.LastProbedAt,
			LastProbeError:      snap.LastProbeError,
			LastProbeLatencyMs:  snap.LastProbeLatencyMs,
			ConsecutiveFailures: snap.ConsecutiveFailures,
			UnhealthySince:      snap.UnhealthySince,
		})
	}
	response.Success(c, out)
}

// LoadHealthMap batches health lookup for a slice of proxy IDs. Kept here as
// a helper for future sidecar handlers that need to enrich a listing without
// touching upstream code.
func (h *ProxyHealthHandler) LoadHealthMap(ctx context.Context, ids []int64) map[int64]*service.ProxyHealthSnapshot {
	if h.proxyHealth == nil || len(ids) == 0 {
		return map[int64]*service.ProxyHealthSnapshot{}
	}
	m, err := h.proxyHealth.LoadHealthByIDs(ctx, ids)
	if err != nil {
		return map[int64]*service.ProxyHealthSnapshot{}
	}
	return m
}

// </fork:proxy-circuit-breaker>
