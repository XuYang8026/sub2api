// <fork:proxy-circuit-breaker>
// Package-local proxy error classifier for the auto-circuit-breaker feature.
// Detects proxy-connection failures (dial refused, DNS, TLS handshake, etc.)
// so callers can (1) route the failure into the account temp-unschedulable
// mechanism and (2) count failures per proxy to trigger proxy-level circuit
// breaking. Kept as a self-contained module to minimize rebase pain when
// syncing upstream.
// </fork>

package service

import (
	"errors"
	"fmt"
	"strings"
)

// ProxyErrorClass categorizes a low-level connection failure that is
// unambiguously attributable to the outbound HTTP proxy, not the upstream
// Anthropic/OpenAI/Gemini API.
type ProxyErrorClass string

const (
	// ProxyErrorNone means the error is not a proxy-attributable failure.
	// Callers must NOT auto-pause the account based on this.
	ProxyErrorNone ProxyErrorClass = ""

	// ProxyErrorRefused: TCP dial to the proxy was refused.
	// Message shape: `proxyconnect tcp: dial tcp X.X.X.X:PORT: connect: connection refused`
	// or `socks connect tcp X.X.X.X:PORT: connection refused`.
	ProxyErrorRefused ProxyErrorClass = "proxy_connection_refused"

	// ProxyErrorTimeout: TCP dial to the proxy timed out, or the proxy
	// itself did not respond within the client deadline.
	// Message shape: `proxyconnect tcp: dial tcp ... i/o timeout` or
	// `proxy` combined with `timeout`.
	ProxyErrorTimeout ProxyErrorClass = "proxy_connection_timeout"

	// ProxyErrorTLS: the proxy replied to the CONNECT / TLS handshake with
	// non-TLS bytes. Typical when a proxy is misconfigured or the endpoint
	// is actually plain HTTP behind a TLS URL.
	// Message shape: `tls: first record does not look like a TLS handshake`
	// (only classified as proxy error when observed in a proxy context).
	ProxyErrorTLS ProxyErrorClass = "proxy_tls_handshake"

	// ProxyErrorDNS: proxy hostname could not be resolved, or the proxy
	// itself returned DNS resolution failure for the target.
	ProxyErrorDNS ProxyErrorClass = "proxy_dns_failed"

	// ProxyErrorUnreachable: no route to the proxy host, or network
	// unreachable errors bubbling from proxyconnect.
	ProxyErrorUnreachable ProxyErrorClass = "proxy_unreachable"
)

// proxyContextMarkers are substrings whose presence indicates the surrounding
// error was raised while dialing/using an HTTP/SOCKS proxy, as opposed to a
// direct connection to the upstream API or a context/deadline cancel from the
// gateway itself.
var proxyContextMarkers = []string{
	"proxyconnect",
	"socks connect",
	"socks5",
	"socks4",
	"http_proxy",
	"proxy connect",
}

// nonProxyExactMatches are lowercase error text prefixes/substrings that we
// explicitly refuse to classify as proxy errors, even if their surrounding
// context contains proxy keywords. This guards against auto-pausing accounts
// for gateway-side cancellations or upstream 4xx/5xx.
var nonProxyExactMatches = []string{
	"context canceled",
	"context deadline exceeded",
	"401 unauthorized",
	"403 forbidden",
	"429 too many",
	"upstream returned",
	"invalid_grant",
}

// ClassifyProxyError inspects err and, when its message unambiguously
// describes an HTTP/SOCKS proxy dial or handshake failure, returns the
// specific ProxyErrorClass. It returns ProxyErrorNone for nil errors,
// context cancellations, and upstream API errors.
//
// Matching rules (all case-insensitive):
//  1. If msg contains any nonProxyExactMatches AND no proxyContextMarkers,
//     return ProxyErrorNone.
//  2. If msg contains "tls: first record does not look like a TLS handshake",
//     return ProxyErrorTLS (this is unambiguous — Go's TLS lib only emits it
//     from a Dial after CONNECT, which is a proxy CONNECT tunnel path).
//  3. If msg contains a proxyContextMarker, dispatch on inner keyword:
//     - "connection refused"        -> ProxyErrorRefused
//     - "i/o timeout" | "timeout"   -> ProxyErrorTimeout
//     - "no such host" | "dns"      -> ProxyErrorDNS
//     - "no route to host" | "unreachable" -> ProxyErrorUnreachable
//     - fallback (still proxy-attributable) -> ProxyErrorRefused
//  4. Anything else -> ProxyErrorNone.
func ClassifyProxyError(err error) ProxyErrorClass {
	if err == nil {
		return ProxyErrorNone
	}
	msg := strings.ToLower(err.Error())
	if msg == "" {
		return ProxyErrorNone
	}

	hasProxyContext := containsAny(msg, proxyContextMarkers)

	// Rule 2: TLS handshake mismatch is always a proxy-level classify.
	if strings.Contains(msg, "tls: first record does not look like a tls handshake") {
		return ProxyErrorTLS
	}

	// Rule 1: definitively non-proxy messages get filtered out unless proxy
	// context is explicit.
	if !hasProxyContext && containsAny(msg, nonProxyExactMatches) {
		return ProxyErrorNone
	}

	if !hasProxyContext {
		// Message lacks any proxy identifier — be conservative and skip.
		return ProxyErrorNone
	}

	// Rule 3: proxy context present, dispatch on inner cause.
	switch {
	case strings.Contains(msg, "no such host"),
		strings.Contains(msg, "dns"):
		return ProxyErrorDNS
	case strings.Contains(msg, "no route to host"),
		strings.Contains(msg, "network is unreachable"),
		strings.Contains(msg, "host is unreachable"):
		return ProxyErrorUnreachable
	case strings.Contains(msg, "connection refused"):
		return ProxyErrorRefused
	case strings.Contains(msg, "i/o timeout"),
		strings.Contains(msg, "deadline exceeded"),
		strings.Contains(msg, "timeout"):
		return ProxyErrorTimeout
	default:
		// proxy context but unknown inner cause — still count as proxy
		// failure (conservative for upstream side, aggressive for pool).
		return ProxyErrorRefused
	}
}

// IsProxyError is a shortcut for ClassifyProxyError(err) != ProxyErrorNone.
func IsProxyError(err error) bool {
	return ClassifyProxyError(err) != ProxyErrorNone
}

// ProxyErrorReason returns a short, user-facing Chinese description that
// combines the class label and the original error's key snippet. Suitable
// for storing as an account's error_message or a proxy's last_probe_error.
// Never returns "".
func ProxyErrorReason(err error) string {
	class := ClassifyProxyError(err)
	if class == ProxyErrorNone {
		if err == nil {
			return ""
		}
		return err.Error()
	}
	label := classLabelZh(class)
	return fmt.Sprintf("代理不可用 (%s): %s", label, truncateErrorMessage(err.Error(), 240))
}

// ProxyErrorReasonWithProxyID adds a proxy id/name preamble for aggregated logs.
func ProxyErrorReasonWithProxyID(err error, proxyID int64, proxyName string) string {
	base := ProxyErrorReason(err)
	if proxyID <= 0 && proxyName == "" {
		return base
	}
	if proxyName == "" {
		return fmt.Sprintf("[proxy #%d] %s", proxyID, base)
	}
	if proxyID <= 0 {
		return fmt.Sprintf("[proxy %s] %s", proxyName, base)
	}
	return fmt.Sprintf("[proxy #%d %s] %s", proxyID, proxyName, base)
}

func classLabelZh(c ProxyErrorClass) string {
	switch c {
	case ProxyErrorRefused:
		return "拒绝连接"
	case ProxyErrorTimeout:
		return "连接超时"
	case ProxyErrorTLS:
		return "TLS 握手失败"
	case ProxyErrorDNS:
		return "DNS 解析失败"
	case ProxyErrorUnreachable:
		return "网络不可达"
	default:
		return "未知代理错误"
	}
}

func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if n == "" {
			continue
		}
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

func truncateErrorMessage(msg string, max int) string {
	if max <= 0 {
		return msg
	}
	if len(msg) <= max {
		return msg
	}
	return msg[:max] + "..."
}

// AsProxyError wraps err so downstream code can annotate that the failure
// has been classified as a proxy error. The wrapped error retains the
// original for errors.Is / errors.As traversal.
type proxyErrorWrapper struct {
	err   error
	class ProxyErrorClass
}

func (w *proxyErrorWrapper) Error() string {
	return ProxyErrorReason(w.err)
}

func (w *proxyErrorWrapper) Unwrap() error {
	return w.err
}

// AsProxyError wraps err with proxy-error metadata, unless it's already
// non-proxy classified. Returns err unchanged if not a proxy error.
func AsProxyError(err error) error {
	class := ClassifyProxyError(err)
	if class == ProxyErrorNone {
		return err
	}
	return &proxyErrorWrapper{err: err, class: class}
}

// ExtractProxyErrorClass returns the classified class if err wraps a proxy
// error, otherwise ClassifyProxyError(err).
func ExtractProxyErrorClass(err error) ProxyErrorClass {
	var wrapper *proxyErrorWrapper
	if errors.As(err, &wrapper) {
		return wrapper.class
	}
	return ClassifyProxyError(err)
}
