package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/oauth"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// <fork:oauth-session>
//
// ProvideForkOAuthSessionMirrors turns on the Redis mirror for every
// platform whose upstream OAuth SessionStore is process-local (OpenAI, Claude,
// Gemini CLI, Antigravity), so generate-auth-url and exchange-code may land on
// different replicas. It lives in repository because service must not import
// go-redis (depguard). A nil client leaves every store exactly as upstream.
func ProvideForkOAuthSessionMirrors(rdb *redis.Client) service.ForkOAuthSessionMirrorRegistration {
	if rdb != nil {
		openai.ForkEnableRedisSessionMirror(rdb)
		oauth.ForkEnableRedisSessionMirror(rdb)
		geminicli.ForkEnableRedisSessionMirror(rdb)
		antigravity.ForkEnableRedisSessionMirror(rdb)
	}
	return service.ForkOAuthSessionMirrorRegistration{}
}

// </fork>
