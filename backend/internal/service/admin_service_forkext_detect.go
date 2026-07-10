package service

import (
	"context"
	"fmt"
)

// <fork:proxy-smart-import>
//
// Sidecar for the proxy smart-import feature's DetectProxyProtocol capability.
// Rather than growing the upstream AdminService interface, this file defines
// a narrow ProxyProtocolDetector capability interface that handlers/services
// can depend on. The same *adminServiceImpl struct still provides the impl,
// so wire binds it to both AdminService (upstream) and ProxyProtocolDetector
// (fork).

// ProxyProtocolDetector auto-detects http vs socks5 for a proxy endpoint.
// Fork-only interface.
type ProxyProtocolDetector interface {
	DetectProxyProtocol(ctx context.Context, host string, port int, username, password string) (*ProxyProtocolDetectResult, error)
}

// DetectProxyProtocol races http:// and socks5:// probes against the given
// endpoint and returns the winning protocol. Method on *adminServiceImpl so
// wire can bind the same struct to ProxyProtocolDetector.
func (s *adminServiceImpl) DetectProxyProtocol(ctx context.Context, host string, port int, username, password string) (*ProxyProtocolDetectResult, error) {
	if s.proxyProber == nil {
		return nil, fmt.Errorf("proxy prober not configured")
	}
	return DetectProxyProtocol(ctx, s.proxyProber, host, port, username, password)
}

// Compile-time assertion.
var _ ProxyProtocolDetector = (*adminServiceImpl)(nil)

// </fork:proxy-smart-import>
