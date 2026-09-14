//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// <fork:codex-default-instructions>

func newCodexInstructionsOAuthAccount(passthrough bool) *Account {
	extra := map[string]any{}
	if passthrough {
		extra["openai_passthrough"] = true
	}
	return &Account{
		ID: 301, Name: "oauth", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
		Extra:       extra, Status: StatusActive, Schedulable: true, RateMultiplier: f64p(1),
	}
}

func newCodexInstructionsGroupCtx(skip bool) context.Context {
	return context.WithValue(context.Background(), ctxkey.Group, &Group{
		ID: 7, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true,
		SkipCodexDefaultInstructions: skip,
	})
}

func newCodexInstructionsSSEUpstream() *httpUpstreamRecorder {
	return &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
	}}
}

func forwardCodexInstructionsBody(t *testing.T, ctx context.Context, account *Account, body []byte) []byte {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	upstream := newCodexInstructionsSSEUpstream()
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	_, _ = svc.Forward(ctx, c, account, body)
	require.NotNil(t, upstream.lastReq, "upstream must be reached")
	return upstream.lastBody
}

func forwardCodexInstructionsChatBody(t *testing.T, ctx context.Context, account *Account, body []byte) []byte {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	upstream := newCodexInstructionsSSEUpstream()
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	_, _ = svc.ForwardAsChatCompletions(ctx, c, account, body, "", "gpt-5.5")
	require.NotNil(t, upstream.lastReq, "upstream must be reached")
	return upstream.lastBody
}

func TestGroupSkipsCodexDefaultInstructions_NilSafeAndPlatformScoped(t *testing.T) {
	require.False(t, groupSkipsCodexDefaultInstructions(nil))
	require.False(t, groupSkipsCodexDefaultInstructions(context.Background()))
	require.False(t, groupSkipsCodexDefaultInstructions(newCodexInstructionsGroupCtx(false)))
	require.True(t, groupSkipsCodexDefaultInstructions(newCodexInstructionsGroupCtx(true)))

	composite := context.WithValue(context.Background(), ctxkey.Group, &Group{
		ID: 9, Platform: PlatformComposite, Status: StatusActive, Hydrated: true, SkipCodexDefaultInstructions: true,
	})
	require.True(t, groupSkipsCodexDefaultInstructions(composite))

	anthropic := context.WithValue(context.Background(), ctxkey.Group, &Group{
		ID: 8, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true, SkipCodexDefaultInstructions: true,
	})
	require.False(t, groupSkipsCodexDefaultInstructions(anthropic))
}

func TestOpenAIForward_NativeResponses_GroupSkipsCodexDefaultInstructions(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":true,"input":"hi"}`)

	withSkip := forwardCodexInstructionsBody(t, newCodexInstructionsGroupCtx(true), newCodexInstructionsOAuthAccount(false), body)
	require.True(t, gjson.GetBytes(withSkip, "instructions").Exists(), "field must exist for Codex backend")
	require.Equal(t, "", gjson.GetBytes(withSkip, "instructions").String())

	withoutSkip := forwardCodexInstructionsBody(t, newCodexInstructionsGroupCtx(false), newCodexInstructionsOAuthAccount(false), body)
	require.Contains(t, gjson.GetBytes(withoutSkip, "instructions").String(), "You are Codex")
}

func TestOpenAIForward_NativeResponses_ClientInstructionsUntouchedWhenSkipping(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":true,"instructions":"client says","input":"hi"}`)
	out := forwardCodexInstructionsBody(t, newCodexInstructionsGroupCtx(true), newCodexInstructionsOAuthAccount(false), body)
	require.Equal(t, "client says", gjson.GetBytes(out, "instructions").String())
}

func TestOpenAIForward_ChatCompletionsResponsesShape_GroupSkipsCodexDefaultInstructions(t *testing.T) {
	// Responses 形态 body 从 /v1/chat/completions 入站：无 skip 时会补 base prompt。
	body := []byte(`{"model":"gpt-5.5","stream":true,"input":[{"role":"user","content":"hi"}]}`)

	withSkip := forwardCodexInstructionsChatBody(t, newCodexInstructionsGroupCtx(true), newCodexInstructionsOAuthAccount(false), body)
	require.True(t, gjson.GetBytes(withSkip, "instructions").Exists())
	require.Equal(t, "", gjson.GetBytes(withSkip, "instructions").String())

	withoutSkip := forwardCodexInstructionsChatBody(t, newCodexInstructionsGroupCtx(false), newCodexInstructionsOAuthAccount(false), body)
	require.Contains(t, gjson.GetBytes(withoutSkip, "instructions").String(), "You are Codex")
}

func TestOpenAIForward_Passthrough_GroupSkipsCodexDefaultInstructions(t *testing.T) {
	body := []byte(`{"model":"gpt-5-codex","stream":true,"input":"hi"}`)

	withSkip := forwardCodexInstructionsBody(t, newCodexInstructionsGroupCtx(true), newCodexInstructionsOAuthAccount(true), body)
	require.False(t, gjson.GetBytes(withSkip, "instructions").Exists(), "passthrough keeps body as sent")

	withoutSkip := forwardCodexInstructionsBody(t, newCodexInstructionsGroupCtx(false), newCodexInstructionsOAuthAccount(true), body)
	require.Contains(t, gjson.GetBytes(withoutSkip, "instructions").String(), "You are Codex")
}

// </fork:codex-default-instructions>
