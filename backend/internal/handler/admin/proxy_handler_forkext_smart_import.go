package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// <fork:proxy-smart-import>
//
// Sidecar handler for the smart batch-import endpoint. Provides
// POST /api/v1/admin/proxies/batch-smart, which accepts BatchCreateProxyItem
// entries with optional protocol (empty / "auto" triggers auto-detection)
// and returns a per-row status including detection metadata.
//
// The upstream POST /admin/proxies/batch endpoint (strict, required protocol)
// stays unchanged; the frontend targets the -smart variant when the operator
// opts in via the auto-detect toggle.

// ProxySmartImportHandler exposes the smart-import endpoint. Fork-only.
type ProxySmartImportHandler struct {
	admin    service.AdminService
	detector service.ProxyProtocolDetector
}

// NewProxySmartImportHandler creates a new sidecar handler for smart batch
// import. detector may be nil — auto-detection then fails per row with a
// clear error while explicit-protocol rows still work.
func NewProxySmartImportHandler(admin service.AdminService, detector service.ProxyProtocolDetector) *ProxySmartImportHandler {
	return &ProxySmartImportHandler{admin: admin, detector: detector}
}

// SmartBatchCreateProxyItem is the input row for the smart-import endpoint.
// Protocol is optional — empty string or "auto" triggers detection. Original
// echoes the caller-supplied raw line so the UI can render the original
// input unchanged in the result table (falls back to "host:port" if omitted).
type SmartBatchCreateProxyItem struct {
	Protocol string `json:"protocol"`
	Host     string `json:"host" binding:"required"`
	Port     int    `json:"port" binding:"required,min=1,max=65535"`
	Username string `json:"username"`
	Password string `json:"password"`
	Original string `json:"original"`
}

// SmartBatchCreateRequest wraps a batch of smart-import rows.
type SmartBatchCreateRequest struct {
	Proxies []SmartBatchCreateProxyItem `json:"proxies" binding:"required,min=1"`
}

// SmartBatchCreateResultItem is the per-item outcome. Status values:
//   - "created"       — persisted successfully
//   - "skipped"       — duplicate, transient lookup error, or create-race
//   - "detect_failed" — auto-detection failed; proxy still saved with fallback
//     protocol (http) so the row is not lost, but the UI should
//     flag it so the operator knows the protocol is a guess.
//
// Both Error and Reason carry the same message; the pair exists so callers
// written against either field name work without a breaking rename.
type SmartBatchCreateResultItem struct {
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

// validSmartBatchProtocols is the set of explicit protocol strings the
// smart-batch endpoint accepts (in addition to "" and "auto" which trigger
// detection).
var validSmartBatchProtocols = map[string]bool{
	"http":    true,
	"https":   true,
	"socks5":  true,
	"socks5h": true,
}

// SmartBatchCreate handles smart batch creation of proxies with optional
// auto-detection.
// POST /api/v1/admin/proxies/batch-smart
func (h *ProxySmartImportHandler) SmartBatchCreate(c *gin.Context) {
	var req SmartBatchCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	created := 0
	skipped := 0
	// errored counts rows whose auto-detection failed (they are still saved
	// with a fallback protocol, but the operator should know the protocol is
	// a guess).
	errored := 0
	items := make([]SmartBatchCreateResultItem, 0, len(req.Proxies))

	// setReason writes the same string into both Error and Reason so old
	// (json:"error") and new (json:"reason") clients both see the message
	// without a breaking rename.
	setReason := func(r *SmartBatchCreateResultItem, msg string) {
		r.Error = msg
		r.Reason = msg
	}

	for _, item := range req.Proxies {
		host := strings.TrimSpace(item.Host)
		protocol := strings.ToLower(strings.TrimSpace(item.Protocol))
		username := strings.TrimSpace(item.Username)
		password := strings.TrimSpace(item.Password)

		// echo caller-supplied original; fall back to "host:port" so the UI
		// always has something to render.
		original := strings.TrimSpace(item.Original)
		if original == "" {
			original = host + ":" + strconv.Itoa(item.Port)
		}
		resultItem := SmartBatchCreateResultItem{Original: original, Host: host, Port: item.Port}

		// validate protocol (accept "" / "auto" / one of {http,https,socks5,socks5h}).
		if protocol != "" && protocol != "auto" && !validSmartBatchProtocols[protocol] {
			resultItem.Status = "skipped"
			setReason(&resultItem, "unsupported protocol: "+protocol)
			skipped++
			items = append(items, resultItem)
			continue
		}

		// Duplicate check — a transient error must not abort the whole
		// batch; surface it on this row and continue.
		exists, err := h.admin.CheckProxyExists(c.Request.Context(), host, item.Port, username, password)
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

		// auto-detect protocol when unspecified.
		detectFailed := false
		if protocol == "" || protocol == "auto" {
			if h.detector == nil {
				resultItem.DetectionError = "protocol detector not configured"
				setReason(&resultItem, resultItem.DetectionError)
				protocol = "http"
				detectFailed = true
			} else {
				det, derr := h.detector.DetectProxyProtocol(c.Request.Context(), host, item.Port, username, password)
				if derr != nil {
					// Detection failed → fall back to http and carry the
					// error so the UI can flag it.
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
		}
		resultItem.Protocol = protocol

		_, err = h.admin.CreateProxy(c.Request.Context(), &service.CreateProxyInput{
			Name:     "default",
			Protocol: protocol,
			Host:     host,
			Port:     item.Port,
			Username: username,
			Password: password,
		})
		if err != nil {
			resultItem.Status = "skipped"
			setReason(&resultItem, err.Error())
			skipped++
			items = append(items, resultItem)
			continue
		}

		// rows whose protocol was a guess are marked detect_failed so the UI
		// can render a warning badge — but the row was still saved.
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

// </fork:proxy-smart-import>
