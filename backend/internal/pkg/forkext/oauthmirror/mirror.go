// Package oauthmirror is a fork-only helper that mirrors process-local OAuth
// authorization sessions into Redis.
//
// <fork:oauth-session>
//
// Upstream keeps every platform's OAuth SessionStore in process memory, so
// with several replicas behind a round-robin Ingress the generate-auth-url
// request creates the session on one pod while exchange-code lands on another
// and fails with "session not found or expired" (upstream issue #3307).
//
// A Mirror sits beside the upstream in-memory map: Set writes through to
// Redis, Get consults Redis first, Delete removes both. When no Redis client is
// configured, or a Redis write fails for a given session, the caller keeps
// using its local map exactly as upstream does. Redis wiring lives in the
// repository ForkExtSet; this package is imported only from pkg-level
// sidecars, keeping service/handler free of go-redis (depguard).
package oauthmirror

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/redissession"
	"github.com/redis/go-redis/v9"
)

const (
	keyPrefix      = "oauth:session:"
	defaultTimeout = 2 * time.Second
)

// Mirror is a per-platform Redis write-through for OAuth sessions.
type Mirror struct {
	platform string
	remote   atomic.Pointer[redissession.Store]

	mu        sync.Mutex
	ttl       time.Duration
	localOnly map[string]time.Time
}

// New returns a disabled Mirror for the given platform key segment.
func New(platform string) *Mirror {
	return &Mirror{platform: platform, localOnly: make(map[string]time.Time)}
}

// Enable turns the mirror on. A nil client leaves it disabled.
func (m *Mirror) Enable(rdb *redis.Client, ttl time.Duration) {
	if m == nil || rdb == nil {
		return
	}
	m.mu.Lock()
	m.ttl = ttl
	m.mu.Unlock()
	m.remote.Store(redissession.New(rdb, keyPrefix+m.platform, ttl))
}

// Disable turns the mirror off and forgets local-only markers.
func (m *Mirror) Disable() {
	if m == nil {
		return
	}
	m.remote.Store(nil)
	m.mu.Lock()
	m.localOnly = make(map[string]time.Time)
	m.mu.Unlock()
}

// Enabled reports whether a Redis backend is configured.
func (m *Mirror) Enabled() bool {
	return m != nil && m.remote.Load() != nil
}

// Store writes the session to Redis. It returns false when the mirror is
// disabled or the write failed; in the latter case the id is marked local-only
// so Load defers to the caller's memory map for it.
func (m *Mirror) Store(id string, value any) bool {
	if m == nil {
		return false
	}
	remote := m.remote.Load()
	if remote == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	if err := remote.Set(ctx, id, value); err != nil {
		m.markLocalOnly(id)
		slog.Warn("forkext oauth session Redis write failed; using process-local fallback",
			"platform", m.platform, "error", err)
		return false
	}
	m.clearLocalOnly(id)
	return true
}

// Load reads the session for id into dest.
//
// handled=false means the mirror has no opinion (disabled, id is local-only,
// or Redis errored) and the caller must consult its own memory map.
// handled=true means found is authoritative.
func (m *Mirror) Load(id string, dest any) (found bool, handled bool) {
	if m == nil {
		return false, false
	}
	remote := m.remote.Load()
	if remote == nil || m.isLocalOnly(id) {
		return false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	ok, err := remote.Get(ctx, id, dest)
	if err != nil {
		slog.Warn("forkext oauth session Redis read failed; falling back to process-local store",
			"platform", m.platform, "error", err)
		return false, false
	}
	return ok, true
}

// Delete removes the session from Redis (best effort) and clears any
// local-only marker.
func (m *Mirror) Delete(id string) {
	if m == nil {
		return
	}
	m.clearLocalOnly(id)
	remote := m.remote.Load()
	if remote == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	if err := remote.Delete(ctx, id); err != nil {
		slog.Warn("forkext oauth session Redis delete failed", "platform", m.platform, "error", err)
	}
}

func (m *Mirror) markLocalOnly(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if m.ttl > 0 {
		for k, at := range m.localOnly {
			if now.Sub(at) > m.ttl {
				delete(m.localOnly, k)
			}
		}
	}
	m.localOnly[id] = now
}

func (m *Mirror) clearLocalOnly(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.localOnly, id)
}

func (m *Mirror) isLocalOnly(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.localOnly[id]
	return ok
}
