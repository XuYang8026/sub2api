package geminicli

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/forkext/oauthmirror"
	"github.com/redis/go-redis/v9"
)

// <fork:oauth-session>
//
// Redis mirror for Gemini CLI OAuth authorization sessions so that
// generate-auth-url and exchange-code may hit different replicas. The three
// hooks below are called from one-line seams in oauth.go (Set / Get / Delete).
// Without ForkEnableRedisSessionMirror every hook is a no-op and behaviour is
// identical to upstream.

var forkSessionMirror = oauthmirror.New("gemini")

// ForkEnableRedisSessionMirror turns on the Redis mirror. Wired from the
// repository ForkExtSet at startup.
func ForkEnableRedisSessionMirror(rdb *redis.Client) {
	forkSessionMirror.Enable(rdb, SessionTTL)
}

// ForkDisableRedisSessionMirror turns the mirror off (tests).
func ForkDisableRedisSessionMirror() {
	forkSessionMirror.Disable()
}

// ForkRedisSessionMirrorEnabled reports whether the mirror is active.
func ForkRedisSessionMirrorEnabled() bool {
	return forkSessionMirror.Enabled()
}

func forkMirrorSet(sessionID string, session *OAuthSession) {
	if session == nil {
		return
	}
	forkSessionMirror.Store(sessionID, session)
}

// forkMirrorGet returns handled=false when the caller must fall back to its
// process-local map (mirror disabled, local-only session, or Redis error).
func forkMirrorGet(sessionID string) (session *OAuthSession, ok bool, handled bool) {
	var dto OAuthSession
	found, handled := forkSessionMirror.Load(sessionID, &dto)
	if !handled {
		return nil, false, false
	}
	if !found || time.Since(dto.CreatedAt) > SessionTTL {
		return nil, false, true
	}
	return &dto, true, true
}

func forkMirrorDelete(sessionID string) {
	forkSessionMirror.Delete(sessionID)
}

// </fork>
