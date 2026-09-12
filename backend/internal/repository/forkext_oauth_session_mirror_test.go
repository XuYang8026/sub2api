//go:build unit

package repository

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/oauth"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// <fork:oauth-session>

func disableAllOAuthMirrors() {
	openai.ForkDisableRedisSessionMirror()
	oauth.ForkDisableRedisSessionMirror()
	geminicli.ForkDisableRedisSessionMirror()
	antigravity.ForkDisableRedisSessionMirror()
}

func TestProvideForkOAuthSessionMirrors_NilRedisLeavesEveryPlatformDisabled(t *testing.T) {
	t.Cleanup(disableAllOAuthMirrors)

	ProvideForkOAuthSessionMirrors(nil)

	require.False(t, openai.ForkRedisSessionMirrorEnabled())
	require.False(t, oauth.ForkRedisSessionMirrorEnabled())
	require.False(t, geminicli.ForkRedisSessionMirrorEnabled())
	require.False(t, antigravity.ForkRedisSessionMirrorEnabled())
}

func TestProvideForkOAuthSessionMirrors_EnablesAllFourPlatforms(t *testing.T) {
	t.Cleanup(disableAllOAuthMirrors)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	ProvideForkOAuthSessionMirrors(rdb)

	require.True(t, openai.ForkRedisSessionMirrorEnabled())
	require.True(t, oauth.ForkRedisSessionMirrorEnabled())
	require.True(t, geminicli.ForkRedisSessionMirrorEnabled())
	require.True(t, antigravity.ForkRedisSessionMirrorEnabled())
}

// End-to-end at the service boundary through the real wiring provider:
// pod A issues the auth URL, pod B receives the exchange-code call.

var errStopAtExchange = errors.New("stop: reached upstream token exchange")

type forkRecordingOAuthClient struct {
	code, verifier, redirectURI, clientID string
}

func (c *forkRecordingOAuthClient) ExchangeCode(_ context.Context, code, codeVerifier, redirectURI, _ string, clientID string) (*openai.TokenResponse, error) {
	c.code, c.verifier, c.redirectURI, c.clientID = code, codeVerifier, redirectURI, clientID
	return nil, errStopAtExchange
}

func (c *forkRecordingOAuthClient) RefreshToken(context.Context, string, string) (*openai.TokenResponse, error) {
	return nil, errors.New("not used")
}

func (c *forkRecordingOAuthClient) RefreshTokenWithClientID(context.Context, string, string, string) (*openai.TokenResponse, error) {
	return nil, errors.New("not used")
}

func stateFromAuthURL(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u.Query().Get("state")
}

func TestForkOAuthSessionMirror_OpenAIExchangeCodeSucceedsOnAnotherReplica(t *testing.T) {
	t.Cleanup(disableAllOAuthMirrors)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ProvideForkOAuthSessionMirrors(rdb)

	client := &forkRecordingOAuthClient{}
	podA := service.NewOpenAIOAuthService(nil, client)
	podB := service.NewOpenAIOAuthService(nil, client)
	t.Cleanup(func() { podA.Stop(); podB.Stop() })

	res, err := podA.GenerateAuthURL(context.Background(), nil, "", "openai")
	require.NoError(t, err)

	_, err = podB.ExchangeCode(context.Background(), &service.OpenAIExchangeCodeInput{
		SessionID: res.SessionID,
		Code:      "auth-code",
		State:     stateFromAuthURL(t, res.AuthURL),
	})

	require.ErrorIs(t, err, errStopAtExchange, "pod B must reach the token exchange, i.e. it found the session created on pod A")
	require.Equal(t, "auth-code", client.code)
	require.NotEmpty(t, client.verifier, "PKCE verifier must travel with the session")
	require.Equal(t, openai.DefaultRedirectURI, client.redirectURI)
}

func TestForkOAuthSessionMirror_WithoutRedisAnotherReplicaStillFails(t *testing.T) {
	t.Cleanup(disableAllOAuthMirrors)
	client := &forkRecordingOAuthClient{}
	podA := service.NewOpenAIOAuthService(nil, client)
	podB := service.NewOpenAIOAuthService(nil, client)
	t.Cleanup(func() { podA.Stop(); podB.Stop() })

	res, err := podA.GenerateAuthURL(context.Background(), nil, "", "openai")
	require.NoError(t, err)

	_, err = podB.ExchangeCode(context.Background(), &service.OpenAIExchangeCodeInput{
		SessionID: res.SessionID, Code: "auth-code", State: stateFromAuthURL(t, res.AuthURL),
	})

	require.Error(t, err)
	require.Equal(t, "OPENAI_OAUTH_SESSION_NOT_FOUND", infraerrors.Reason(err), "documents the upstream multi-replica bug this fork works around")
}
