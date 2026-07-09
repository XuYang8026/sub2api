// <fork:proxy-circuit-breaker>
// ScheduledProxyProbeService periodically probes every active proxy
// (status='active') and updates the proxy_health_status columns:
//
//	success -> MarkHealthy(latencyMs, now)
//	failure -> MarkUnhealthy(errMsg, now)
//
// Recovery is automatic: an unhealthy proxy that starts responding to the
// probe URL is flipped back to healthy by MarkHealthy, and its bound
// accounts become eligible again (their temp_unschedulable naturally
// expires within 15 min anyway).
// </fork>

package service

import (
	"context"
	"log/slog"
	neturl "net/url"
	"sync"
	"sync/atomic"
	"time"
)

const (
	scheduledProxyProbeDefaultInterval    = 5 * time.Minute
	scheduledProxyProbeDefaultConcurrency = 8
	scheduledProxyProbePerProbeTimeout    = 15 * time.Second
	scheduledProxyProbeShutdownTimeout    = 3 * time.Second
)

// ScheduledProxyProbeService is a long-running cron-style service that
// periodically probes every active proxy and updates its health snapshot.
//
// On startup it fires one probe pass immediately so cold-starts don't leave
// all proxies at the default 'unknown' health status for a full interval.
type ScheduledProxyProbeService struct {
	healthRepo ProxyHealthRepository
	proxyRepo  ProxyRepository
	prober     ProxyExitInfoProber
	interval   time.Duration

	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	startOnce sync.Once
	stopOnce  sync.Once
}

// NewScheduledProxyProbeService constructs the service. A zero or negative
// interval falls back to the 5-minute default. Nil dependencies produce a
// noop service — Start() short-circuits.
func NewScheduledProxyProbeService(
	healthRepo ProxyHealthRepository,
	proxyRepo ProxyRepository,
	prober ProxyExitInfoProber,
	interval time.Duration,
) *ScheduledProxyProbeService {
	if interval <= 0 {
		interval = scheduledProxyProbeDefaultInterval
	}
	return &ScheduledProxyProbeService{
		healthRepo: healthRepo,
		proxyRepo:  proxyRepo,
		prober:     prober,
		interval:   interval,
	}
}

// Start launches the background probe loop. Safe to call multiple times —
// only the first invocation has any effect.
func (s *ScheduledProxyProbeService) Start() {
	if s == nil || s.healthRepo == nil || s.proxyRepo == nil || s.prober == nil {
		return
	}
	s.startOnce.Do(func() {
		s.ctx, s.cancel = context.WithCancel(context.Background())
		s.wg.Add(1)
		go s.loop()
		slog.Info("proxy_probe.started", "interval", s.interval.String())
	})
}

// Stop signals the background loop to exit and waits up to
// scheduledProxyProbeShutdownTimeout for in-flight probes to drain. Safe to
// call multiple times.
func (s *ScheduledProxyProbeService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		done := make(chan struct{})
		go func() {
			s.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(scheduledProxyProbeShutdownTimeout):
			slog.Warn("proxy_probe.stop_timeout",
				"timeout", scheduledProxyProbeShutdownTimeout.String())
		}
	})
}

func (s *ScheduledProxyProbeService) loop() {
	defer s.wg.Done()

	// Kick off an immediate first pass so cold-start doesn't leave everyone
	// stuck at 'unknown' for a full interval.
	s.runOnce(s.ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.runOnce(s.ctx)
		}
	}
}

// runOnce executes one probe pass over all active proxies. It respects the
// service-level context (ctx cancellation aborts remaining probes) and
// bounds concurrency via a semaphore.
func (s *ScheduledProxyProbeService) runOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	// List every active proxy. We deliberately re-query on every tick so
	// newly-added or newly-activated proxies are picked up without needing a
	// service restart.
	proxies, err := s.proxyRepo.ListActive(ctx)
	if err != nil {
		slog.Warn("proxy_probe.list_active_failed", "error", err)
		return
	}
	if len(proxies) == 0 {
		return
	}

	var (
		total     = int64(len(proxies))
		healthy   int64
		unhealthy int64
		errs      int64
	)

	sem := make(chan struct{}, scheduledProxyProbeDefaultConcurrency)
	var wg sync.WaitGroup

	for i := range proxies {
		p := proxies[i]

		select {
		case <-ctx.Done():
			// Service is shutting down — stop dispatching new probes.
			// Still drain in-flight goroutines below via wg.Wait().
			goto drain
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(pr Proxy) {
			defer wg.Done()
			defer func() { <-sem }()

			outcome := s.probeOne(ctx, pr)
			switch outcome {
			case probeOutcomeHealthy:
				atomic.AddInt64(&healthy, 1)
			case probeOutcomeUnhealthy:
				atomic.AddInt64(&unhealthy, 1)
			case probeOutcomeError:
				atomic.AddInt64(&errs, 1)
			}
		}(p)
	}

drain:
	wg.Wait()

	slog.Info("proxy_probe.tick_complete",
		"total", total,
		"healthy", atomic.LoadInt64(&healthy),
		"unhealthy", atomic.LoadInt64(&unhealthy),
		"errors", atomic.LoadInt64(&errs),
	)
}

type probeOutcome int

const (
	probeOutcomeHealthy probeOutcome = iota + 1
	probeOutcomeUnhealthy
	probeOutcomeError
)

// probeOne performs a single proxy probe with its own per-probe timeout and
// updates the health snapshot. It also logs transitions (healthy->unhealthy,
// unhealthy->healthy) at slog.Info.
func (s *ScheduledProxyProbeService) probeOne(parentCtx context.Context, p Proxy) probeOutcome {
	prevStatus := s.previousStatus(parentCtx, p.ID)

	proxyURL := p.URL()
	// Skip proxies that would probe an empty/schemeless URL. This can happen
	// when the protocol column is blank (partial import) or the parsed URL
	// comes back empty. Marking such a row unhealthy would be actively wrong
	// (the misconfiguration is on our side, not the remote endpoint); log a
	// warning so admins can spot and fix the row.
	if proxyURL == "" || !hasValidScheme(proxyURL) {
		slog.Warn("proxy_probe.skipped_no_scheme",
			"proxy_id", p.ID,
			"proxy_name", p.Name,
			"protocol", p.Protocol,
		)
		return probeOutcomeError
	}

	probeCtx, cancel := context.WithTimeout(parentCtx, scheduledProxyProbePerProbeTimeout)
	defer cancel()

	_, latencyMs, err := s.prober.ProbeProxy(probeCtx, proxyURL)
	now := time.Now()

	// DB writes must survive parentCtx cancellation (service shutdown races)
	// so a probe that just finished doesn't leave the health row stale. The
	// short per-probe timeout is only for the outbound HTTP request itself.
	writeCtx := context.WithoutCancel(parentCtx)

	if err == nil {
		if markErr := s.healthRepo.MarkHealthy(writeCtx, p.ID, latencyMs, now); markErr != nil {
			slog.Warn("proxy_probe.mark_healthy_failed",
				"proxy_id", p.ID,
				"proxy_name", p.Name,
				"error", markErr,
			)
			return probeOutcomeError
		}
		if prevStatus == ProxyHealthUnhealthy {
			slog.Info("proxy_probe.transition_recovered",
				"proxy_id", p.ID,
				"proxy_name", p.Name,
				"latency_ms", latencyMs,
			)
		}
		return probeOutcomeHealthy
	}

	errMsg := err.Error()
	if markErr := s.healthRepo.MarkUnhealthy(writeCtx, p.ID, errMsg, now); markErr != nil {
		slog.Warn("proxy_probe.mark_unhealthy_failed",
			"proxy_id", p.ID,
			"proxy_name", p.Name,
			"error", markErr,
		)
		return probeOutcomeError
	}
	if prevStatus != ProxyHealthUnhealthy {
		slog.Info("proxy_probe.transition_unhealthy",
			"proxy_id", p.ID,
			"proxy_name", p.Name,
			"prev_status", prevStatus,
			"probe_error", errMsg,
		)
	}
	return probeOutcomeUnhealthy
}

// hasValidScheme returns true when rawURL parses successfully AND has a
// non-empty scheme. Kept private and dep-free so probeOne stays cheap.
func hasValidScheme(rawURL string) bool {
	u, err := neturl.Parse(rawURL)
	if err != nil || u == nil {
		return false
	}
	return u.Scheme != ""
}

// previousStatus best-effort reads the current health_status for transition
// detection. Failures collapse to 'unknown' so at worst we log a spurious
// transition on the first tick after a DB hiccup.
func (s *ScheduledProxyProbeService) previousStatus(ctx context.Context, id int64) string {
	if s.healthRepo == nil {
		return ProxyHealthUnknown
	}
	snap, err := s.healthRepo.LoadHealth(ctx, id)
	if err != nil || snap == nil {
		return ProxyHealthUnknown
	}
	if snap.Status == "" {
		return ProxyHealthUnknown
	}
	return snap.Status
}
