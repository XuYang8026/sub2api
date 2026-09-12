//go:build unit

package geminicli

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// <fork:oauth-session> tests: two SessionStore instances stand in for two pods.

func newMirrorTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func enableMirrorForTest(t *testing.T, rdb *redis.Client) {
	t.Helper()
	ForkEnableRedisSessionMirror(rdb)
	t.Cleanup(ForkDisableRedisSessionMirror)
}

func newStoreForTest(t *testing.T) *SessionStore {
	t.Helper()
	s := NewSessionStore()
	t.Cleanup(s.Stop)
	return s
}

func TestForkSessionMirror_SessionSetOnOneInstanceIsVisibleOnAnother(t *testing.T) {
	_, rdb := newMirrorTestRedis(t)
	enableMirrorForTest(t, rdb)
	podA, podB := newStoreForTest(t), newStoreForTest(t)

	podA.Set("sid-1", &OAuthSession{State: "state-1", CodeVerifier: "verifier-1", RedirectURI: "http://localhost:8085/oauth2callback", ProjectID: "proj", OAuthType: "code_assist", CreatedAt: time.Now()})

	got, ok := podB.Get("sid-1")
	require.True(t, ok, "exchange-code on another pod must find the session")
	require.Equal(t, "state-1", got.State)
	require.Equal(t, "verifier-1", got.CodeVerifier)
	require.Equal(t, "proj", got.ProjectID)
	require.Equal(t, "code_assist", got.OAuthType)
}

func TestForkSessionMirror_DeleteOnOneInstancePropagates(t *testing.T) {
	_, rdb := newMirrorTestRedis(t)
	enableMirrorForTest(t, rdb)
	podA, podB := newStoreForTest(t), newStoreForTest(t)

	podA.Set("sid-2", &OAuthSession{State: "s", CodeVerifier: "v", CreatedAt: time.Now()})
	podB.Delete("sid-2")

	_, ok := podA.Get("sid-2")
	require.False(t, ok, "session consumed on pod B must not be reusable on pod A")
}

func TestForkSessionMirror_ExpiredSessionIsNotReturned(t *testing.T) {
	_, rdb := newMirrorTestRedis(t)
	enableMirrorForTest(t, rdb)
	podA, podB := newStoreForTest(t), newStoreForTest(t)

	podA.Set("sid-3", &OAuthSession{State: "s", CodeVerifier: "v", CreatedAt: time.Now().Add(-SessionTTL - time.Minute)})

	_, ok := podB.Get("sid-3")
	require.False(t, ok)
}

func TestForkSessionMirror_DisabledKeepsProcessLocalBehavior(t *testing.T) {
	podA, podB := newStoreForTest(t), newStoreForTest(t)

	podA.Set("sid-4", &OAuthSession{State: "s", CodeVerifier: "v", CreatedAt: time.Now()})

	_, okLocal := podA.Get("sid-4")
	_, okRemote := podB.Get("sid-4")
	require.True(t, okLocal)
	require.False(t, okRemote, "without Redis the store stays process-local, exactly like upstream")
}

func TestForkSessionMirror_RedisWriteFailureFallsBackToLocalStore(t *testing.T) {
	mr, rdb := newMirrorTestRedis(t)
	enableMirrorForTest(t, rdb)
	podA, podB := newStoreForTest(t), newStoreForTest(t)
	mr.Close()

	podA.Set("sid-5", &OAuthSession{State: "s", CodeVerifier: "v", CreatedAt: time.Now()})

	_, okLocal := podA.Get("sid-5")
	_, okRemote := podB.Get("sid-5")
	require.True(t, okLocal, "creator pod must still serve the session from memory")
	require.False(t, okRemote)
}
