// <fork:proxy-smart-import>
package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeProber lets tests seed a per-URL outcome (ok/error + latency + optional delay).
type fakeProber struct {
	mu       sync.Mutex
	byURL    map[string]fakeProbeOutcome
	calls    atomic.Int64
	seenURLs []string
}

type fakeProbeOutcome struct {
	err     error
	latency int64
	delay   time.Duration // simulates slow probe; consumed on Wait
}

func newFakeProber() *fakeProber {
	return &fakeProber{byURL: map[string]fakeProbeOutcome{}}
}

// seedScheme is a helper — matches ProbeProxy(ctx, url) where url begins with the given prefix.
func (f *fakeProber) seedScheme(prefix string, o fakeProbeOutcome) {
	f.mu.Lock()
	// We don't know the exact URL in advance because DetectProxyProtocol builds
	// it from host+port+creds. Store under prefix; probeMatch handles lookup.
	f.byURL["__prefix__"+prefix] = o
	f.mu.Unlock()
}

func (f *fakeProber) ProbeProxy(ctx context.Context, proxyURL string) (*ProxyExitInfo, int64, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.seenURLs = append(f.seenURLs, proxyURL)
	out, ok := f.byURL[proxyURL]
	if !ok {
		// Try prefix match.
		for k, v := range f.byURL {
			if strings.HasPrefix(k, "__prefix__") {
				prefix := strings.TrimPrefix(k, "__prefix__")
				if strings.HasPrefix(proxyURL, prefix) {
					out = v
					ok = true
					break
				}
			}
		}
	}
	f.mu.Unlock()
	if !ok {
		return nil, 0, errors.New("no outcome seeded for " + proxyURL)
	}
	if out.delay > 0 {
		select {
		case <-time.After(out.delay):
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
	}
	if out.err != nil {
		return nil, out.latency, out.err
	}
	return &ProxyExitInfo{IP: "1.2.3.4"}, out.latency, nil
}

func TestDetectProxyProtocol_HTTPWinsOnLowerLatency(t *testing.T) {
	f := newFakeProber()
	f.seedScheme("http://", fakeProbeOutcome{latency: 100})
	f.seedScheme("socks5://", fakeProbeOutcome{latency: 300})

	res, err := DetectProxyProtocol(context.Background(), f, "proxy.example.com", 8080, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Protocol != "http" {
		t.Errorf("expected 'http' winner, got %q", res.Protocol)
	}
	if res.LatencyMs != 100 {
		t.Errorf("expected latency 100, got %d", res.LatencyMs)
	}
	if !res.TriedHTTP.OK || !res.TriedSocks.OK {
		t.Errorf("both should have succeeded: %+v", res)
	}
}

func TestDetectProxyProtocol_SOCKS5WinsOnLowerLatency(t *testing.T) {
	f := newFakeProber()
	f.seedScheme("http://", fakeProbeOutcome{latency: 500})
	f.seedScheme("socks5://", fakeProbeOutcome{latency: 50})

	res, err := DetectProxyProtocol(context.Background(), f, "proxy.example.com", 8080, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Protocol != "socks5h" {
		t.Errorf("expected 'socks5h' winner, got %q", res.Protocol)
	}
	if res.LatencyMs != 50 {
		t.Errorf("expected latency 50, got %d", res.LatencyMs)
	}
}

func TestDetectProxyProtocol_OnlyHTTPSucceeds(t *testing.T) {
	f := newFakeProber()
	f.seedScheme("http://", fakeProbeOutcome{latency: 200})
	f.seedScheme("socks5://", fakeProbeOutcome{err: errors.New("connection refused"), latency: 10})

	res, err := DetectProxyProtocol(context.Background(), f, "proxy.example.com", 8080, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Protocol != "http" {
		t.Errorf("expected 'http' winner, got %q", res.Protocol)
	}
	if !res.TriedHTTP.OK || res.TriedSocks.OK {
		t.Errorf("unexpected attempts: %+v", res)
	}
	if !strings.Contains(res.TriedSocks.Error, "connection refused") {
		t.Errorf("socks attempt error should be captured, got %q", res.TriedSocks.Error)
	}
}

func TestDetectProxyProtocol_OnlySOCKSSucceeds(t *testing.T) {
	f := newFakeProber()
	f.seedScheme("http://", fakeProbeOutcome{err: errors.New("proxy auth failed"), latency: 5})
	f.seedScheme("socks5://", fakeProbeOutcome{latency: 200})

	res, err := DetectProxyProtocol(context.Background(), f, "proxy.example.com", 8080, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Protocol != "socks5h" {
		t.Errorf("expected 'socks5h' winner, got %q", res.Protocol)
	}
}

func TestDetectProxyProtocol_BothFail(t *testing.T) {
	f := newFakeProber()
	f.seedScheme("http://", fakeProbeOutcome{err: errors.New("http-err"), latency: 0})
	f.seedScheme("socks5://", fakeProbeOutcome{err: errors.New("socks-err"), latency: 0})

	res, err := DetectProxyProtocol(context.Background(), f, "proxy.example.com", 8080, "", "")
	if err == nil {
		t.Fatal("expected error when both fail")
	}
	if res == nil {
		t.Fatal("result should still be populated for observability")
	}
	if !strings.Contains(err.Error(), "http-err") || !strings.Contains(err.Error(), "socks-err") {
		t.Errorf("error should include both attempt errors, got %q", err.Error())
	}
}

func TestDetectProxyProtocol_WithCredentials(t *testing.T) {
	f := newFakeProber()
	f.seedScheme("http://", fakeProbeOutcome{latency: 100})
	f.seedScheme("socks5://", fakeProbeOutcome{err: errors.New("no socks here")})

	_, err := DetectProxyProtocol(context.Background(), f, "proxy.example.com", 8080, "alice", "s3cr3t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify the built URL includes escaped credentials.
	f.mu.Lock()
	defer f.mu.Unlock()
	foundHTTPWithAuth := false
	foundSOCKSWithAuth := false
	for _, u := range f.seenURLs {
		if strings.HasPrefix(u, "http://alice:s3cr3t@proxy.example.com:8080") {
			foundHTTPWithAuth = true
		}
		if strings.HasPrefix(u, "socks5://alice:s3cr3t@proxy.example.com:8080") {
			foundSOCKSWithAuth = true
		}
	}
	if !foundHTTPWithAuth {
		t.Errorf("expected http probe URL to include credentials, saw %v", f.seenURLs)
	}
	if !foundSOCKSWithAuth {
		t.Errorf("expected socks probe URL to include credentials, saw %v", f.seenURLs)
	}
}

func TestDetectProxyProtocol_PasswordWithSpecialChars(t *testing.T) {
	f := newFakeProber()
	f.seedScheme("http://", fakeProbeOutcome{latency: 100})
	f.seedScheme("socks5://", fakeProbeOutcome{err: errors.New("nope")})
	_, err := DetectProxyProtocol(context.Background(), f, "proxy.example.com", 8080, "alice", "p@ss:w/d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// URL should have percent-encoded '@' and ':' in the password.
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.seenURLs {
		if strings.Contains(u, "p@ss:w/d") && !strings.Contains(u, "p%40ss") {
			t.Errorf("password should be percent-encoded in probe URL, got raw: %q", u)
		}
	}
}

func TestDetectProxyProtocol_NilProber(t *testing.T) {
	_, err := DetectProxyProtocol(context.Background(), nil, "h", 8080, "", "")
	if err == nil {
		t.Fatal("expected error with nil prober")
	}
}

func TestDetectProxyProtocol_EmptyHost(t *testing.T) {
	f := newFakeProber()
	_, err := DetectProxyProtocol(context.Background(), f, "", 8080, "", "")
	if err == nil {
		t.Fatal("expected error with empty host")
	}
}

func TestDetectProxyProtocol_PortOutOfRange(t *testing.T) {
	f := newFakeProber()
	_, err := DetectProxyProtocol(context.Background(), f, "h", 0, "", "")
	if err == nil {
		t.Fatal("expected error for port 0")
	}
	_, err = DetectProxyProtocol(context.Background(), f, "h", 70000, "", "")
	if err == nil {
		t.Fatal("expected error for out-of-range port")
	}
}

func TestDetectProxyProtocol_ProbesRunConcurrently(t *testing.T) {
	// If probes ran serially, elapsed >= 2*delay. Concurrent → ~1*delay.
	f := newFakeProber()
	delay := 200 * time.Millisecond
	f.seedScheme("http://", fakeProbeOutcome{latency: 50, delay: delay})
	f.seedScheme("socks5://", fakeProbeOutcome{latency: 60, delay: delay})

	start := time.Now()
	_, err := DetectProxyProtocol(context.Background(), f, "h.example", 8080, "", "")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Generous upper bound to avoid CI flakes.
	if elapsed >= 2*delay-20*time.Millisecond {
		t.Errorf("probes should run concurrently: elapsed=%v want < ~2*delay=%v", elapsed, 2*delay)
	}
}

func TestDetectProxyProtocol_PerAttemptTimeout(t *testing.T) {
	restore := SetProxyProtocolDetectTimeoutForTest(50 * time.Millisecond)
	defer restore()
	f := newFakeProber()
	// Delay longer than the timeout → context deadline exceeded.
	f.seedScheme("http://", fakeProbeOutcome{delay: 500 * time.Millisecond, latency: 0})
	f.seedScheme("socks5://", fakeProbeOutcome{delay: 500 * time.Millisecond, latency: 0})

	start := time.Now()
	_, err := DetectProxyProtocol(context.Background(), f, "h.example", 8080, "", "")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error when both time out")
	}
	if elapsed > 400*time.Millisecond {
		t.Errorf("expected timeout to bound elapsed to ~50ms, got %v", elapsed)
	}
}

func TestDetectProxyProtocol_TieOnLatencyPrefersHTTP(t *testing.T) {
	// Rationale: HTTP is more common, so it's a safer default when both succeed
	// with equal latency.
	f := newFakeProber()
	f.seedScheme("http://", fakeProbeOutcome{latency: 100})
	f.seedScheme("socks5://", fakeProbeOutcome{latency: 100})
	res, err := DetectProxyProtocol(context.Background(), f, "h.example", 8080, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Protocol != "http" {
		t.Errorf("tie should prefer http, got %q", res.Protocol)
	}
}

func TestDetectProxyProtocol_IPv6Host(t *testing.T) {
	f := newFakeProber()
	f.seedScheme("http://[::1]:8080", fakeProbeOutcome{latency: 100})
	f.seedScheme("socks5://[::1]:8080", fakeProbeOutcome{err: errors.New("nope")})
	res, err := DetectProxyProtocol(context.Background(), f, "::1", 8080, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Protocol != "http" {
		t.Errorf("expected http winner for IPv6 host, got %q", res.Protocol)
	}
}
