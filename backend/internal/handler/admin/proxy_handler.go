package admin

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ProxyHandler handles admin proxy management
type ProxyHandler struct {
	adminService service.AdminService
	// <fork:proxy-circuit-breaker>
	// proxyHealth is the shadow read-model for auto-circuit-breaker columns
	// (health_status / last_probed_at / …). Optional: nil is tolerated so
	// tests / minimal wire graphs still compile.
	proxyHealth service.ProxyHealthRepository
	// </fork>
}

// NewProxyHandler creates a new admin proxy handler
// <fork:proxy-circuit-breaker>
// proxyHealth added as a second parameter; wire graph passes the shared
// repository instance. Keeping it in the constructor signature (vs a setter)
// makes DI failures visible at compile time.
// </fork>
func NewProxyHandler(adminService service.AdminService, proxyHealth service.ProxyHealthRepository) *ProxyHandler {
	return &ProxyHandler{
		adminService: adminService,
		proxyHealth:  proxyHealth,
	}
}

// CreateProxyRequest represents create proxy request
type CreateProxyRequest struct {
	Name           string `json:"name" binding:"required"`
	Protocol       string `json:"protocol" binding:"required,oneof=http https socks5 socks5h"`
	Host           string `json:"host" binding:"required"`
	Port           int    `json:"port" binding:"required,min=1,max=65535"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	ExpiresAt      *int64 `json:"expires_at"`
	FallbackMode   string `json:"fallback_mode" binding:"omitempty,oneof=none proxy direct"`
	BackupProxyID  *int64 `json:"backup_proxy_id"`
	ExpiryWarnDays int    `json:"expiry_warn_days" binding:"omitempty,min=0"`
}

// UpdateProxyRequest represents update proxy request
type UpdateProxyRequest struct {
	Name           string `json:"name"`
	Protocol       string `json:"protocol" binding:"omitempty,oneof=http https socks5 socks5h"`
	Host           string `json:"host"`
	Port           int    `json:"port" binding:"omitempty,min=1,max=65535"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	Status         string `json:"status" binding:"omitempty,oneof=active inactive"`
	ExpiresAt      *int64 `json:"expires_at"`
	FallbackMode   string `json:"fallback_mode" binding:"omitempty,oneof=none proxy direct"`
	BackupProxyID  *int64 `json:"backup_proxy_id"`
	ExpiryWarnDays int    `json:"expiry_warn_days" binding:"omitempty,min=0"`
}

// List handles listing all proxies with pagination
// GET /api/v1/admin/proxies
func (h *ProxyHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	protocol := c.Query("protocol")
	status := c.Query("status")
	search := c.Query("search")
	sortBy := c.DefaultQuery("sort_by", "id")
	sortOrder := c.DefaultQuery("sort_order", "desc")
	// 标准化和验证 search 参数
	search = strings.TrimSpace(search)
	if len(search) > 100 {
		search = search[:100]
	}

	proxies, total, err := h.adminService.ListProxiesWithAccountCount(c.Request.Context(), page, pageSize, protocol, status, search, sortBy, sortOrder)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]dto.AdminProxyWithAccountCount, 0, len(proxies))
	// <fork:proxy-circuit-breaker>
	// Load health snapshots in one round trip so the paginated table can
	// render health_status without an extra N calls from the frontend.
	healthMap := h.loadHealthMapForProxiesWithCount(c.Request.Context(), proxies)
	for i := range proxies {
		out = append(out, *dto.ProxyWithAccountCountFromServiceAdminWithHealth(&proxies[i], healthMap[proxies[i].ID]))
	}
	// </fork>
	response.Paginated(c, out, total, page, pageSize)
}

// GetAll handles getting all active proxies without pagination
// GET /api/v1/admin/proxies/all
// Optional query param: with_count=true to include account count per proxy
func (h *ProxyHandler) GetAll(c *gin.Context) {
	withCount := c.Query("with_count") == "true"

	if withCount {
		proxies, err := h.adminService.GetAllProxiesWithAccountCount(c.Request.Context())
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		out := make([]dto.AdminProxyWithAccountCount, 0, len(proxies))
		// <fork:proxy-circuit-breaker>
		healthMap := h.loadHealthMapForProxiesWithCount(c.Request.Context(), proxies)
		for i := range proxies {
			out = append(out, *dto.ProxyWithAccountCountFromServiceAdminWithHealth(&proxies[i], healthMap[proxies[i].ID]))
		}
		// </fork>
		response.Success(c, out)
		return
	}

	proxies, err := h.adminService.GetAllProxies(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]dto.AdminProxy, 0, len(proxies))
	// <fork:proxy-circuit-breaker>
	healthMap := h.loadHealthMapForProxies(c.Request.Context(), proxies)
	for i := range proxies {
		out = append(out, *dto.ProxyFromServiceAdminWithHealth(&proxies[i], healthMap[proxies[i].ID]))
	}
	// </fork>
	response.Success(c, out)
}

// GetByID handles getting a proxy by ID
// GET /api/v1/admin/proxies/:id
func (h *ProxyHandler) GetByID(c *gin.Context) {
	proxyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid proxy ID")
		return
	}

	proxy, err := h.adminService.GetProxy(c.Request.Context(), proxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// <fork:proxy-circuit-breaker>
	// Load the single-row health snapshot so the admin detail view can
	// display health_status alongside the base proxy fields.
	var snap *service.ProxyHealthSnapshot
	if h.proxyHealth != nil && proxy != nil {
		if s, herr := h.proxyHealth.LoadHealth(c.Request.Context(), proxy.ID); herr == nil {
			snap = s
		}
	}
	response.Success(c, dto.ProxyFromServiceAdminWithHealth(proxy, snap))
	// </fork>
}

// <fork:proxy-circuit-breaker>
// loadHealthMapForProxies batches health lookup for a slice of Proxy structs.
// Returns an empty map on missing repo or empty input to keep call sites
// branch-free.
func (h *ProxyHandler) loadHealthMapForProxies(ctx context.Context, proxies []service.Proxy) map[int64]*service.ProxyHealthSnapshot {
	if h.proxyHealth == nil || len(proxies) == 0 {
		return map[int64]*service.ProxyHealthSnapshot{}
	}
	ids := make([]int64, 0, len(proxies))
	for i := range proxies {
		ids = append(ids, proxies[i].ID)
	}
	m, err := h.proxyHealth.LoadHealthByIDs(ctx, ids)
	if err != nil {
		return map[int64]*service.ProxyHealthSnapshot{}
	}
	return m
}

// loadHealthMapForProxiesWithCount is the ProxyWithAccountCount overload; kept
// separate to avoid an interface/generics dance in Go 1.20-compatible code.
func (h *ProxyHandler) loadHealthMapForProxiesWithCount(ctx context.Context, proxies []service.ProxyWithAccountCount) map[int64]*service.ProxyHealthSnapshot {
	if h.proxyHealth == nil || len(proxies) == 0 {
		return map[int64]*service.ProxyHealthSnapshot{}
	}
	ids := make([]int64, 0, len(proxies))
	for i := range proxies {
		ids = append(ids, proxies[i].ID)
	}
	m, err := h.proxyHealth.LoadHealthByIDs(ctx, ids)
	if err != nil {
		return map[int64]*service.ProxyHealthSnapshot{}
	}
	return m
}

// ProxyHealthResponseItem is the payload shape returned by GET /admin/proxies/health.
// Matches the frontend contract in frontend/src/api/admin/proxies.ts.
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
func (h *ProxyHandler) GetHealth(c *gin.Context) {
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

// </fork>

// Create handles creating a new proxy
// POST /api/v1/admin/proxies
func (h *ProxyHandler) Create(c *gin.Context) {
	var req CreateProxyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	executeAdminIdempotentJSON(c, "admin.proxies.create", req, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		var expiresAt *time.Time
		if req.ExpiresAt != nil && *req.ExpiresAt > 0 {
			t := time.Unix(*req.ExpiresAt, 0).UTC()
			expiresAt = &t
		}
		proxy, err := h.adminService.CreateProxy(ctx, &service.CreateProxyInput{
			Name:           strings.TrimSpace(req.Name),
			Protocol:       strings.TrimSpace(req.Protocol),
			Host:           strings.TrimSpace(req.Host),
			Port:           req.Port,
			Username:       strings.TrimSpace(req.Username),
			Password:       strings.TrimSpace(req.Password),
			ExpiresAt:      expiresAt,
			FallbackMode:   strings.TrimSpace(req.FallbackMode),
			BackupProxyID:  req.BackupProxyID,
			ExpiryWarnDays: req.ExpiryWarnDays,
		})
		if err != nil {
			return nil, err
		}
		return dto.ProxyFromServiceAdmin(proxy), nil
	})
}

// Update handles updating a proxy
// PUT /api/v1/admin/proxies/:id
func (h *ProxyHandler) Update(c *gin.Context) {
	proxyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid proxy ID")
		return
	}

	var req UpdateProxyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt > 0 {
		t := time.Unix(*req.ExpiresAt, 0).UTC()
		expiresAt = &t
	}
	proxy, err := h.adminService.UpdateProxy(c.Request.Context(), proxyID, &service.UpdateProxyInput{
		Name:           strings.TrimSpace(req.Name),
		Protocol:       strings.TrimSpace(req.Protocol),
		Host:           strings.TrimSpace(req.Host),
		Port:           req.Port,
		Username:       strings.TrimSpace(req.Username),
		Password:       strings.TrimSpace(req.Password),
		Status:         strings.TrimSpace(req.Status),
		ExpiresAt:      expiresAt,
		FallbackMode:   strings.TrimSpace(req.FallbackMode),
		BackupProxyID:  req.BackupProxyID,
		ExpiryWarnDays: req.ExpiryWarnDays,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.ProxyFromServiceAdmin(proxy))
}

// Delete handles deleting a proxy
// DELETE /api/v1/admin/proxies/:id
func (h *ProxyHandler) Delete(c *gin.Context) {
	proxyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid proxy ID")
		return
	}

	err = h.adminService.DeleteProxy(c.Request.Context(), proxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "Proxy deleted successfully"})
}

// BatchDelete handles batch deleting proxies
// POST /api/v1/admin/proxies/batch-delete
func (h *ProxyHandler) BatchDelete(c *gin.Context) {
	type BatchDeleteRequest struct {
		IDs []int64 `json:"ids" binding:"required,min=1"`
	}

	var req BatchDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	result, err := h.adminService.BatchDeleteProxies(c.Request.Context(), req.IDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, result)
}

// Test handles testing proxy connectivity
// POST /api/v1/admin/proxies/:id/test
func (h *ProxyHandler) Test(c *gin.Context) {
	proxyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid proxy ID")
		return
	}

	result, err := h.adminService.TestProxy(c.Request.Context(), proxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, result)
}

// CheckQuality handles checking proxy quality across common AI targets.
// POST /api/v1/admin/proxies/:id/quality-check
func (h *ProxyHandler) CheckQuality(c *gin.Context) {
	proxyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid proxy ID")
		return
	}

	result, err := h.adminService.CheckProxyQuality(c.Request.Context(), proxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, result)
}

// GetStats handles getting proxy statistics
// GET /api/v1/admin/proxies/:id/stats
func (h *ProxyHandler) GetStats(c *gin.Context) {
	proxyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid proxy ID")
		return
	}

	// Return mock data for now
	_ = proxyID
	response.Success(c, gin.H{
		"total_accounts":  0,
		"active_accounts": 0,
		"total_requests":  0,
		"success_rate":    100.0,
		"average_latency": 0,
	})
}

// GetProxyAccounts handles getting accounts using a proxy
// GET /api/v1/admin/proxies/:id/accounts
func (h *ProxyHandler) GetProxyAccounts(c *gin.Context) {
	proxyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid proxy ID")
		return
	}

	accounts, err := h.adminService.GetProxyAccounts(c.Request.Context(), proxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]dto.ProxyAccountSummary, 0, len(accounts))
	for i := range accounts {
		out = append(out, *dto.ProxyAccountSummaryFromService(&accounts[i]))
	}
	response.Success(c, out)
}

// BatchCreateProxyItem represents a single proxy in batch create request.
// <fork:proxy-smart-import> Protocol is now optional — empty string or "auto"
// triggers protocol auto-detection (http vs socks5). When detection fails the
// proxy is saved with protocol="http" (safe default) and the per-item response
// carries a detection_error field so the UI can surface a warning. Original
// echoes the caller-supplied raw line so the UI can render the original input
// unchanged in the result table (falls back to "host:port" if omitted).
type BatchCreateProxyItem struct {
	Protocol string `json:"protocol"`
	Host     string `json:"host" binding:"required"`
	Port     int    `json:"port" binding:"required,min=1,max=65535"`
	Username string `json:"username"`
	Password string `json:"password"`
	Original string `json:"original"`
}

// BatchCreateRequest represents batch create proxies request
type BatchCreateRequest struct {
	Proxies []BatchCreateProxyItem `json:"proxies" binding:"required,min=1"`
}

// BatchCreateResultItem is the per-item outcome returned by BatchCreate.
// <fork:proxy-smart-import> Status values:
//   - "created"       — persisted successfully
//   - "skipped"       — duplicate, transient lookup error, or create-race
//   - "detect_failed" — auto-detection failed; proxy still saved with fallback
//     protocol (http) so the row is not lost, but the UI should
//     flag it so the operator knows the protocol is a guess.
//
// Both Error and Reason carry the same message; the pair exists so callers
// written against either field name work without a breaking rename.
type BatchCreateResultItem struct {
	Original          string `json:"original,omitempty"`
	Host              string `json:"host"`
	Port              int    `json:"port"`
	Status            string `json:"status"` // created | skipped | detect_failed
	Protocol          string `json:"protocol,omitempty"`
	DetectedProtocol  string `json:"detected_protocol,omitempty"`
	DetectedLatencyMs int64  `json:"detected_latency_ms,omitempty"`
	DetectionError    string `json:"detection_error,omitempty"`
	Error             string `json:"error,omitempty"`
	Reason            string `json:"reason,omitempty"`
}

// validBatchProtocols is the set of explicit protocol strings the batch
// endpoint accepts (in addition to "" and "auto" which trigger detection).
// <fork:proxy-smart-import>
var validBatchProtocols = map[string]bool{
	"http":    true,
	"https":   true,
	"socks5":  true,
	"socks5h": true,
}

// BatchCreate handles batch creating proxies
// POST /api/v1/admin/proxies/batch
func (h *ProxyHandler) BatchCreate(c *gin.Context) {
	var req BatchCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	created := 0
	skipped := 0
	// <fork:proxy-smart-import> "errored" counts rows whose auto-detection failed
	// (they are still saved with a fallback protocol, but the operator should
	// know the protocol is a guess).
	errored := 0
	items := make([]BatchCreateResultItem, 0, len(req.Proxies))

	// <fork:proxy-smart-import> setReason writes the same string into both
	// Error and Reason so old (json:"error") and new (json:"reason") clients
	// both see the message without a breaking rename.
	setReason := func(r *BatchCreateResultItem, msg string) {
		r.Error = msg
		r.Reason = msg
	}

	for _, item := range req.Proxies {
		// Trim all string fields
		host := strings.TrimSpace(item.Host)
		protocol := strings.ToLower(strings.TrimSpace(item.Protocol))
		username := strings.TrimSpace(item.Username)
		password := strings.TrimSpace(item.Password)

		// <fork:proxy-smart-import> echo caller-supplied original; fall back to
		// "host:port" so the UI always has something to render.
		original := strings.TrimSpace(item.Original)
		if original == "" {
			original = host + ":" + strconv.Itoa(item.Port)
		}
		resultItem := BatchCreateResultItem{Original: original, Host: host, Port: item.Port}

		// <fork:proxy-smart-import> validate protocol (accept "" / "auto" / one of {http,https,socks5,socks5h}).
		if protocol != "" && protocol != "auto" && !validBatchProtocols[protocol] {
			resultItem.Status = "skipped"
			setReason(&resultItem, "unsupported protocol: "+protocol)
			skipped++
			items = append(items, resultItem)
			continue
		}

		// Check for duplicates (same host, port, username, password).
		// <fork:proxy-smart-import> A transient error from the duplicate lookup
		// must not abort the whole batch — surface it on this row and continue,
		// otherwise a single flaky DB call would blackhole every remaining row.
		exists, err := h.adminService.CheckProxyExists(c.Request.Context(), host, item.Port, username, password)
		if err != nil {
			resultItem.Status = "skipped"
			setReason(&resultItem, err.Error())
			skipped++
			items = append(items, resultItem)
			continue
		}
		if exists {
			resultItem.Status = "skipped"
			setReason(&resultItem, "duplicate")
			skipped++
			items = append(items, resultItem)
			continue
		}

		// <fork:proxy-smart-import> auto-detect protocol when unspecified.
		detectFailed := false
		if protocol == "" || protocol == "auto" {
			det, derr := h.adminService.DetectProxyProtocol(c.Request.Context(), host, item.Port, username, password)
			if derr != nil {
				// Detection failed → fall back to http (safe default) and
				// carry the error into the response so the UI can flag it.
				resultItem.DetectionError = derr.Error()
				setReason(&resultItem, derr.Error())
				protocol = "http"
				detectFailed = true
			} else {
				protocol = det.Protocol
				resultItem.DetectedProtocol = det.Protocol
				resultItem.DetectedLatencyMs = det.LatencyMs
			}
		}
		resultItem.Protocol = protocol

		// Create proxy with default name
		_, err = h.adminService.CreateProxy(c.Request.Context(), &service.CreateProxyInput{
			Name:     "default",
			Protocol: protocol,
			Host:     host,
			Port:     item.Port,
			Username: username,
			Password: password,
		})
		if err != nil {
			// If creation fails (e.g. race with duplicate check) count as skipped.
			resultItem.Status = "skipped"
			setReason(&resultItem, err.Error())
			skipped++
			items = append(items, resultItem)
			continue
		}

		// <fork:proxy-smart-import> mark rows whose protocol was a guess so the
		// UI can render a warning badge — but the row was still saved, so it
		// does NOT count toward "skipped".
		if detectFailed {
			resultItem.Status = "detect_failed"
			errored++
		} else {
			resultItem.Status = "created"
			created++
		}
		items = append(items, resultItem)
	}

	response.Success(c, gin.H{
		"created": created,
		"skipped": skipped,
		"errored": errored,
		"items":   items,
	})
}
