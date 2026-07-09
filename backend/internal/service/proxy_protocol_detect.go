// <fork:proxy-smart-import>
// proxy_protocol_detect.go — auto-detects whether a given host:port speaks
// HTTP-proxy or SOCKS5-proxy by racing two probes concurrently. Used by the
// smart batch import endpoint so users can paste bare "host:port[:user:pass]"
// entries without specifying the protocol.
//
// Semantics:
//   - Runs http:// and socks5:// probes in parallel, each with an 8-second
//     per-attempt timeout.
//   - If both succeed, the lower-latency winner is returned.
//   - If exactly one succeeds, that one wins.
//   - If both fail, an error is returned summarizing both attempts.
//
// The socks5 attempt uses socks5:// on the wire — the underlying prober
// (via proxyurl.Parse) normalizes it to socks5h, so DNS resolution happens
// at the proxy exit.
package service

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProtoAttempt records the outcome of one protocol probe attempt.
type ProtoAttempt struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

// ProxyProtocolDetectResult carries the winning protocol plus per-attempt
// details for observability. The UI surfaces these so operators can see
// which protocol was tried and why one lost.
type ProxyProtocolDetectResult struct {
	Protocol   string       `json:"protocol"`
	LatencyMs  int64        `json:"latency_ms"`
	TriedHTTP  ProtoAttempt `json:"tried_http"`
	TriedSocks ProtoAttempt `json:"tried_socks"`
}

// proxyProtocolDetectPerAttemptTimeout is the per-probe timeout. Tests may
// override via SetProxyProtocolDetectTimeoutForTest.
var proxyProtocolDetectPerAttemptTimeout = 8 * time.Second

// SetProxyProtocolDetectTimeoutForTest overrides the per-attempt timeout.
// Test-only helper; production callers use the default.
func SetProxyProtocolDetectTimeoutForTest(d time.Duration) (restore func()) {
	prev := proxyProtocolDetectPerAttemptTimeout
	proxyProtocolDetectPerAttemptTimeout = d
	return func() { proxyProtocolDetectPerAttemptTimeout = prev }
}

// DetectProxyProtocol probes host:port once as http and once as socks5,
// concurrently, and returns the winner (lowest latency on tie-of-success,
// the only success on one-of-two, or an error when both fail).
//
// prober must be non-nil. host must be non-empty; port must be 1..65535.
// username/password may be empty.
func DetectProxyProtocol(
	ctx context.Context,
	prober ProxyExitInfoProber,
	host string,
	port int,
	username, password string,
) (*ProxyProtocolDetectResult, error) {
	if prober == nil {
		return nil, fmt.Errorf("prober is nil")
	}
	if host == "" {
		return nil, fmt.Errorf("host is empty")
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port out of range: %d", port)
	}

	httpURL := buildProxyURL("http", host, port, username, password)
	socksURL := buildProxyURL("socks5", host, port, username, password)

	var wg sync.WaitGroup
	var httpAttempt, socksAttempt ProtoAttempt
	wg.Add(2)

	go func() {
		defer wg.Done()
		httpAttempt = probeOne(ctx, prober, httpURL)
	}()
	go func() {
		defer wg.Done()
		socksAttempt = probeOne(ctx, prober, socksURL)
	}()
	wg.Wait()

	result := &ProxyProtocolDetectResult{
		TriedHTTP:  httpAttempt,
		TriedSocks: socksAttempt,
	}

	switch {
	case httpAttempt.OK && socksAttempt.OK:
		// Pick lower latency.
		if socksAttempt.LatencyMs < httpAttempt.LatencyMs {
			result.Protocol = "socks5h"
			result.LatencyMs = socksAttempt.LatencyMs
		} else {
			result.Protocol = "http"
			result.LatencyMs = httpAttempt.LatencyMs
		}
		return result, nil
	case httpAttempt.OK:
		result.Protocol = "http"
		result.LatencyMs = httpAttempt.LatencyMs
		return result, nil
	case socksAttempt.OK:
		result.Protocol = "socks5h"
		result.LatencyMs = socksAttempt.LatencyMs
		return result, nil
	default:
		return result, fmt.Errorf(
			"both protocol probes failed: http=%s; socks5=%s",
			httpAttempt.Error, socksAttempt.Error,
		)
	}
}

// probeOne runs a single probe with per-attempt timeout and captures result.
func probeOne(ctx context.Context, prober ProxyExitInfoProber, proxyURL string) ProtoAttempt {
	attemptCtx, cancel := context.WithTimeout(ctx, proxyProtocolDetectPerAttemptTimeout)
	defer cancel()
	_, latency, err := prober.ProbeProxy(attemptCtx, proxyURL)
	if err != nil {
		return ProtoAttempt{OK: false, LatencyMs: latency, Error: err.Error()}
	}
	return ProtoAttempt{OK: true, LatencyMs: latency}
}

// buildProxyURL constructs "scheme://[user:pass@]host:port" with correct
// escaping via net/url so passwords containing special characters survive.
// IPv6 host literals are bracketed automatically.
func buildProxyURL(scheme, host string, port int, username, password string) string {
	hostPart := host
	// Any host with ':' is either already-bracketed or a raw IPv6 literal;
	// bracket it if needed so the resulting URL is unambiguous.
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		hostPart = "[" + host + "]"
	}
	u := &url.URL{
		Scheme: scheme,
		Host:   hostPart + ":" + strconv.Itoa(port),
	}
	if username != "" || password != "" {
		if password == "" {
			u.User = url.User(username)
		} else {
			u.User = url.UserPassword(username, password)
		}
	}
	return u.String()
}
