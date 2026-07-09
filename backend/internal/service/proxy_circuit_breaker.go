// <fork:proxy-circuit-breaker>
// ProxyCircuitBreaker centralizes the account-level and proxy-level state
// machine for outbound-proxy failures. It is stateless across process
// restarts; all durable state lives in DB columns added by migration 159
// (proxies.consecutive_failures / health_status) and the account's
// existing temp_unschedulable_* columns.
//
// Callers use two entry points:
//
//   HandleAccountProxyError(ctx, account, err)
//     Called from gateway forward + token refresh when an outgoing request
//     failed with a proxy-classifiable error. Sets temp_unschedulable on the
//     account for 15min, increments the bound proxy's failure counter, and
//     trips the proxy-level breaker when >= tripThreshold accounts have
//     reported failures within the recent window (approximated by counter
//     value, cleared on the next successful probe).
//
//   IsProxyUnhealthy(ctx, proxyID)
//     Read-only lookup used by the scheduler to skip accounts whose proxy is
//     marked unhealthy. Cached-friendly (single DB row read).
//
// The scheduled probe cron (see scheduled_proxy_probe.go) is the sole
// mechanism that flips a proxy back from unhealthy to healthy — accounts
// bound to a recovered proxy automatically become eligible again as soon
// as their own temp_unschedulable expires.
// </fork>

package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// Tunables. Kept as package variables (not consts) so tests can override
// without touching production defaults.
var (
	// ProxyCBAccountCooldown is how long an account stays temp_unschedulable
	// after a single proxy error. Matches the user requirement (15 min).
	ProxyCBAccountCooldown = 15 * time.Minute

	// ProxyCBTripThreshold is the accumulated failure count at which the
	// proxy itself is marked unhealthy (regardless of probe outcome).
	// Rationale: 3 different accounts reporting proxy errors is a strong
	// signal that the proxy — not the accounts — is at fault.
	ProxyCBTripThreshold = 3

	// ProxyCBFailureCeiling is the upper bound on consecutive_failures we are
	// willing to keep incrementing. Beyond this, the proxy is already
	// definitively marked unhealthy and further increments waste DB writes
	// (and can overflow the counter in pathological loops). Fresh probes
	// still reset the counter on recovery, so this ceiling is safe.
	ProxyCBFailureCeiling = 100
)

// ProxyCircuitBreaker orchestrates the account-level and proxy-level state
// updates in response to proxy-classifiable errors.
type ProxyCircuitBreaker struct {
	accountRepo       AccountRepository
	proxyRepo         ProxyRepository
	proxyHealth       ProxyHealthRepository
	tempUnschedCache  TempUnschedCache // may be nil in tests / when Redis absent
	runtimeBlocker    RuntimeSchedulingBlocker // may be nil
}

// RuntimeSchedulingBlocker is defined in proxy_health_types.go so both the
// interface and the ProxyHealthRepository contract stay in one file.

// NewProxyCircuitBreaker constructs the breaker. Nil dependencies are
// tolerated for testability; missing deps degrade gracefully (e.g. no cache
// invalidation, no runtime notification).
func NewProxyCircuitBreaker(
	accountRepo AccountRepository,
	proxyRepo ProxyRepository,
	proxyHealth ProxyHealthRepository,
	tempUnschedCache TempUnschedCache,
	runtimeBlocker RuntimeSchedulingBlocker,
) *ProxyCircuitBreaker {
	return &ProxyCircuitBreaker{
		accountRepo:      accountRepo,
		proxyRepo:        proxyRepo,
		proxyHealth:      proxyHealth,
		tempUnschedCache: tempUnschedCache,
		runtimeBlocker:   runtimeBlocker,
	}
}

// HandleAccountProxyError classifies err, and if it's proxy-attributable:
//  1. Sets the account temp_unschedulable for ProxyCBAccountCooldown.
//  2. Increments the bound proxy's consecutive_failures counter.
//  3. If the counter crosses ProxyCBTripThreshold, marks the proxy unhealthy.
//
// Returns the classified ProxyErrorClass (ProxyErrorNone means nothing was
// done — caller should fall back to its regular error handling).
func (b *ProxyCircuitBreaker) HandleAccountProxyError(ctx context.Context, account *Account, err error) ProxyErrorClass {
	if b == nil || account == nil || err == nil {
		return ProxyErrorNone
	}
	class := ClassifyProxyError(err)
	if class == ProxyErrorNone {
		return ProxyErrorNone
	}
	now := time.Now()
	until := now.Add(ProxyCBAccountCooldown)

	// (1) Account-level cooldown. Serialize as TempUnschedState so admin UI
	// picks up the structured reason exactly like existing 401/429 paths.
	state := &TempUnschedState{
		UntilUnix:       until.Unix(),
		TriggeredAtUnix: now.Unix(),
		StatusCode:      0, // proxy dial failed — no HTTP status observed
		MatchedKeyword:  string(class),
		RuleIndex:       -1, // -1 signals "fork:proxy-circuit-breaker", not a config rule
		ErrorMessage:    ProxyErrorReason(err),
	}
	reason := marshalTempUnschedState(state)

	if b.runtimeBlocker != nil {
		b.runtimeBlocker.BlockAccountScheduling(account, until, "proxy_unavailable")
	}
	if setErr := b.accountRepo.SetTempUnschedulable(ctx, account.ID, until, reason); setErr != nil {
		slog.Warn("proxy_cb.account_set_temp_unsched_failed",
			"account_id", account.ID,
			"proxy_id", derefInt64(account.ProxyID),
			"error", setErr)
		// continue — proxy-level bookkeeping is still valuable.
	}
	if b.tempUnschedCache != nil {
		if cacheErr := b.tempUnschedCache.SetTempUnsched(ctx, account.ID, state); cacheErr != nil {
			slog.Warn("proxy_cb.account_cache_set_failed",
				"account_id", account.ID,
				"error", cacheErr)
		}
	}
	slog.Info("proxy_cb.account_temp_unschedulable",
		"account_id", account.ID,
		"proxy_id", derefInt64(account.ProxyID),
		"class", string(class),
		"until", until,
	)

	// (2)+(3) Proxy-level bookkeeping.
	//
	// The MarkUnhealthy path in the repo also bumps consecutive_failures by 1
	// (COALESCE +1 semantics), so calling BOTH IncrementFailure and
	// MarkUnhealthy on every failure would double-increment. To keep the drift
	// bounded while still using only the two existing repo methods (fixer:dto+repo
	// owns the repo file, we can't add a new atomic method here), we:
	//   - Skip further recording once we've already exceeded ProxyCBFailureCeiling
	//     (the proxy is already unhealthy; extra writes just churn the row).
	//   - Trigger MarkUnhealthy exactly ONCE per crossing, when the incremented
	//     counter equals ProxyCBTripThreshold. Subsequent failures still
	//     increment via IncrementFailure but skip MarkUnhealthy. Total drift
	//     over N failures is at most +1 (only the trip event double-increments),
	//     which is acceptable and does not affect scheduling correctness.
	if account.ProxyID != nil && *account.ProxyID > 0 && b.proxyHealth != nil {
		proxyID := *account.ProxyID

		// Ceiling check: if we've already piled up a lot of failures, stop
		// churning the counter. LoadHealth is a single-row lookup and fails
		// open on error (we still record the failure below).
		if snap, loadErr := b.proxyHealth.LoadHealth(ctx, proxyID); loadErr == nil && snap != nil && snap.ConsecutiveFailures >= ProxyCBFailureCeiling {
			slog.Debug("proxy_cb.failure_ceiling_skipped",
				"proxy_id", proxyID,
				"consecutive_failures", snap.ConsecutiveFailures,
				"ceiling", ProxyCBFailureCeiling,
			)
			return class
		}

		count, incErr := b.proxyHealth.IncrementFailure(ctx, proxyID, now)
		if incErr != nil {
			slog.Warn("proxy_cb.increment_failure_failed",
				"proxy_id", proxyID,
				"error", incErr)
		} else if count == ProxyCBTripThreshold {
			// Exact-equal crossing: this is the first call that puts the
			// counter at (or above) the trip threshold. Fire MarkUnhealthy
			// exactly once. MarkUnhealthy is itself idempotent
			// (COALESCE(unhealthy_since, $now)), so re-tripping under a race
			// is cheap and safe.
			markErr := b.proxyHealth.MarkUnhealthy(ctx, proxyID, ProxyErrorReason(err), now)
			if markErr != nil {
				slog.Warn("proxy_cb.mark_unhealthy_failed",
					"proxy_id", proxyID,
					"error", markErr)
			} else {
				slog.Warn("proxy_cb.proxy_tripped_unhealthy",
					"proxy_id", proxyID,
					"consecutive_failures", count,
					"class", string(class),
				)
			}
		}
		// count < threshold: no proxy-level action; account cooldown alone.
		// count > threshold: proxy already tripped in a prior call; the
		// scheduler is already skipping this proxy. No additional action.
	}
	return class
}

// IsProxyUnhealthy returns true when the proxy has been circuit-broken by
// either the cron probe or the account-level accumulator. Returns false on
// error or missing proxy (fail-open: don't block scheduling on infra hiccup).
func (b *ProxyCircuitBreaker) IsProxyUnhealthy(ctx context.Context, proxyID int64) bool {
	if b == nil || b.proxyHealth == nil || proxyID <= 0 {
		return false
	}
	snap, err := b.proxyHealth.LoadHealth(ctx, proxyID)
	if err != nil || snap == nil {
		return false
	}
	return snap.IsUnhealthy()
}

// FilterHealthyProxyIDs returns the subset of ids whose proxies are NOT
// currently unhealthy. Used by the scheduler for batch account filtering.
// On any error, returns the input unchanged (fail-open).
func (b *ProxyCircuitBreaker) FilterHealthyProxyIDs(ctx context.Context, ids []int64) []int64 {
	if b == nil || b.proxyHealth == nil || len(ids) == 0 {
		return ids
	}
	snaps, err := b.proxyHealth.LoadHealthByIDs(ctx, ids)
	if err != nil {
		return ids
	}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if s, ok := snaps[id]; ok && s.IsUnhealthy() {
			continue
		}
		out = append(out, id)
	}
	return out
}

// marshalTempUnschedState serializes a TempUnschedState the same way
// triggerTempUnschedulable does, falling back to the raw error message on
// serialization failure to preserve the existing admin UI contract.
func marshalTempUnschedState(s *TempUnschedState) string {
	if s == nil {
		return ""
	}
	if raw, err := json.Marshal(s); err == nil {
		return string(raw)
	}
	return s.ErrorMessage
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
