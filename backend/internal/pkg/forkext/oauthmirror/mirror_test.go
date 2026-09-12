//go:build unit

package oauthmirror

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type payload struct {
	State string `json:"state"`
}

func TestMirror_EnableWithNilClientStaysDisabled(t *testing.T) {
	m := New("test")
	m.Enable(nil, time.Minute)

	require.False(t, m.Enabled())
	require.False(t, m.Store("id", payload{State: "s"}))
	var out payload
	found, handled := m.Load("id", &out)
	require.False(t, found)
	require.False(t, handled, "a disabled mirror must defer to the caller's local store")
}

func TestMirror_StoreThenLoadRoundTripsAcrossInstances(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	a, b := New("test"), New("test")
	a.Enable(rdb, time.Minute)
	b.Enable(rdb, time.Minute)

	require.True(t, a.Store("id", payload{State: "s"}))
	var out payload
	found, handled := b.Load("id", &out)
	require.True(t, handled)
	require.True(t, found)
	require.Equal(t, "s", out.State)
	require.True(t, mr.Exists("oauth:session:test:id"), "key must be namespaced per platform")
}

func TestMirror_FailedWriteMarksIdLocalOnlyUntilNextSuccessfulWrite(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	m := New("test")
	m.Enable(rdb, time.Minute)

	mr.Close()
	require.False(t, m.Store("id", payload{State: "s"}))
	_, handled := m.Load("id", &payload{})
	require.False(t, handled, "local-only ids must defer to the caller's memory even though the mirror is enabled")

	require.NoError(t, mr.Restart())
	require.True(t, m.Store("id", payload{State: "s2"}))
	var out payload
	found, handled := m.Load("id", &out)
	require.True(t, handled)
	require.True(t, found)
	require.Equal(t, "s2", out.State)
}

func TestMirror_DeleteRemovesRemoteKey(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	m := New("test")
	m.Enable(rdb, time.Minute)

	require.True(t, m.Store("id", payload{State: "s"}))
	m.Delete("id")

	found, handled := m.Load("id", &payload{})
	require.True(t, handled)
	require.False(t, found)
}
