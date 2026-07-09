// <fork:proxy-circuit-breaker>
package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifyProxyError_TableDriven(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ProxyErrorClass
	}{
		{"nil returns none", nil, ProxyErrorNone},
		{"empty string returns none", errors.New(""), ProxyErrorNone},

		// canonical proxy-refused shapes
		{
			"http proxyconnect refused",
			errors.New(`Post "https://api.anthropic.com/v1/messages": proxyconnect tcp: dial tcp 59.153.255.7:8014: connect: connection refused`),
			ProxyErrorRefused,
		},
		{
			"socks connect refused",
			errors.New(`Get "https://x": socks connect tcp 151.247.123.98:50101: connection refused`),
			ProxyErrorRefused,
		},

		// timeout shapes
		{
			"proxyconnect i/o timeout",
			errors.New(`Post "https://api.anthropic.com/v1/messages": proxyconnect tcp: dial tcp 168.158.29.33:44670: i/o timeout`),
			ProxyErrorTimeout,
		},
		{
			"proxy + generic timeout",
			errors.New(`proxyconnect tcp: timeout while dialing`),
			ProxyErrorTimeout,
		},

		// DNS
		{
			"proxyconnect no such host",
			errors.New(`proxyconnect tcp: dial tcp: lookup gone.example.com: no such host`),
			ProxyErrorDNS,
		},
		{
			"proxy + dns explicit",
			errors.New(`proxyconnect: dns resolution error`),
			ProxyErrorDNS,
		},

		// unreachable
		{
			"proxyconnect no route",
			errors.New(`proxyconnect tcp: dial tcp 1.2.3.4:80: connect: no route to host`),
			ProxyErrorUnreachable,
		},
		{
			"proxyconnect network unreachable",
			errors.New(`proxyconnect tcp: dial tcp: network is unreachable`),
			ProxyErrorUnreachable,
		},

		// TLS handshake mismatch — always classified regardless of context marker
		{
			"tls handshake mismatch with proxy",
			errors.New(`Post "https://x": remote error: tls: first record does not look like a TLS handshake`),
			ProxyErrorTLS,
		},
		{
			"tls handshake mismatch without proxy marker",
			errors.New(`tls: first record does not look like a TLS handshake`),
			ProxyErrorTLS,
		},

		// non-proxy: guardrail cases we MUST NOT misclassify
		{
			"plain context canceled",
			errors.New(`context canceled`),
			ProxyErrorNone,
		},
		{
			"plain context deadline exceeded",
			errors.New(`context deadline exceeded`),
			ProxyErrorNone,
		},
		{
			"upstream 401 body",
			errors.New(`upstream returned 401 unauthorized: {"error":"invalid_grant"}`),
			ProxyErrorNone,
		},
		{
			"upstream 429",
			errors.New(`upstream returned 429 too many requests`),
			ProxyErrorNone,
		},
		{
			"upstream 403 forbidden",
			errors.New(`403 forbidden from anthropic api`),
			ProxyErrorNone,
		},
		{
			"random error unrelated",
			errors.New(`something unrelated broke`),
			ProxyErrorNone,
		},

		// Uppercase / mixed-case still matches (case insensitive)
		{
			"uppercase refused",
			errors.New(`PROXYCONNECT TCP: dial tcp: CONNECT: connection refused`),
			ProxyErrorRefused,
		},

		// proxy context but unknown inner cause — conservative: refused
		{
			"proxy context unknown cause",
			errors.New(`proxyconnect tcp: some unfamiliar failure`),
			ProxyErrorRefused,
		},

		// gateway context canceled that ALSO mentions proxy: because Go wraps
		// the underlying dial error into the outer request error, we DO want
		// to classify this as proxy — the actual root cause was the proxy.
		{
			"combined proxyconnect refused with outer context cancel wrapper",
			errors.New(`Post "https://x": context canceled: proxyconnect tcp: dial tcp 1.2.3.4:80: connect: connection refused`),
			ProxyErrorRefused,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyProxyError(tc.err)
			require.Equal(t, tc.want, got, "err=%v", tc.err)
		})
	}
}

func TestIsProxyError(t *testing.T) {
	require.False(t, IsProxyError(nil))
	require.False(t, IsProxyError(errors.New("context canceled")))
	require.True(t, IsProxyError(errors.New("proxyconnect tcp: dial tcp: connect: connection refused")))
	require.True(t, IsProxyError(errors.New("tls: first record does not look like a TLS handshake")))
}

func TestProxyErrorReason_ContainsClassAndDetail(t *testing.T) {
	err := errors.New(`Post "https://x": proxyconnect tcp: dial tcp 1.2.3.4:80: connect: connection refused`)
	reason := ProxyErrorReason(err)
	require.Contains(t, reason, "代理不可用")
	require.Contains(t, reason, "拒绝连接")
	require.Contains(t, reason, "connection refused")
}

func TestProxyErrorReason_PassesThroughNonProxy(t *testing.T) {
	err := errors.New(`context canceled`)
	require.Equal(t, "context canceled", ProxyErrorReason(err))
	require.Equal(t, "", ProxyErrorReason(nil))
}

func TestProxyErrorReason_Truncates(t *testing.T) {
	// build a very long proxyconnect error
	long := "proxyconnect tcp: dial tcp: " + strings.Repeat("x", 500) + ": connect: connection refused"
	reason := ProxyErrorReason(errors.New(long))
	require.True(t, len(reason) < len(long), "expected truncation, len=%d", len(reason))
	require.Contains(t, reason, "...")
}

func TestProxyErrorReasonWithProxyID(t *testing.T) {
	err := errors.New(`proxyconnect tcp: dial tcp: connect: connection refused`)
	require.Contains(t, ProxyErrorReasonWithProxyID(err, 8, "default"), "#8")
	require.Contains(t, ProxyErrorReasonWithProxyID(err, 8, "default"), "default")
	require.Contains(t, ProxyErrorReasonWithProxyID(err, 0, "default"), "default")
	require.Contains(t, ProxyErrorReasonWithProxyID(err, 8, ""), "#8")
}

func TestAsProxyError_WrapsAndUnwraps(t *testing.T) {
	root := errors.New(`proxyconnect tcp: dial tcp: connect: connection refused`)
	wrapped := AsProxyError(root)
	require.NotEqual(t, root, wrapped)
	require.True(t, errors.Is(wrapped, root))
	require.Equal(t, ProxyErrorRefused, ExtractProxyErrorClass(wrapped))

	nonProxy := errors.New(`context canceled`)
	require.Equal(t, nonProxy, AsProxyError(nonProxy))
}

// Sanity: keeping strings for use in other packages should not break linter
var _ = fmt.Sprintf
