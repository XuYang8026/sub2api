//go:build unit

package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// The Codex backend delivers a moderation refusal (code invalid_prompt) inside an
// HTTP 200 stream as a bare `error` event followed by `response.failed`. Once the
// refusal no longer triggers account failover, the operator's error passthrough
// rule (e.g. invalid_prompt → 400) must still be applied before any client output
// is committed. Otherwise the client receives HTTP 200 carrying a failed terminal
// event, which downstream gateways classify as a retryable 5xx.
func TestResponsesStreamBareErrorThenFailedAppliesPassthroughRule(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const flagged = "Invalid prompt: your prompt was flagged as potentially violating our usage policy. " +
		"Please try again with a different prompt: https://platform.openai.com/docs/guides/reasoning#advice-on-prompting"
	bareError := `{"type":"error","sequence_number":2,"error":{"code":"invalid_prompt","type":"invalid_request_error","message":"` + flagged + `","param":null}}`
	failed := `{"type":"response.failed","sequence_number":3,"response":{"id":"resp_flagged","object":"response","status":"failed","error":{"code":"invalid_prompt","message":"` + flagged + `"},"output":[],"usage":{"input_tokens":10,"output_tokens":0,"total_tokens":10}}}`
	created := `{"type":"response.created","sequence_number":0,"response":{"id":"resp_flagged","object":"response","status":"in_progress","output":[]}}`
	inProgress := `{"type":"response.in_progress","sequence_number":1,"response":{"id":"resp_flagged","object":"response","status":"in_progress","output":[]}}`

	streams := []struct {
		name   string
		stream string
	}{
		{
			name: "bare error then response.failed",
			stream: "event: response.created\ndata: " + created + "\n\n" +
				"event: response.in_progress\ndata: " + inProgress + "\n\n" +
				"event: error\ndata: " + bareError + "\n\n" +
				"event: response.failed\ndata: " + failed + "\n\n",
		},
		{
			name: "bare error then EOF",
			stream: "event: response.created\ndata: " + created + "\n\n" +
				"event: response.in_progress\ndata: " + inProgress + "\n\n" +
				"event: error\ndata: " + bareError + "\n\n",
		},
	}
	handlers := []struct {
		name string
		run  func(*OpenAIGatewayService, *gin.Context, *http.Response, *Account) error
	}{
		{
			name: "native",
			run: func(svc *OpenAIGatewayService, c *gin.Context, resp *http.Response, account *Account) error {
				_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "gpt-6-astra", "gpt-6-astra")
				return err
			},
		},
		{
			name: "passthrough",
			run: func(svc *OpenAIGatewayService, c *gin.Context, resp *http.Response, account *Account) error {
				_, err := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-6-astra", "gpt-6-astra")
				return err
			},
		},
	}

	for _, st := range streams {
		for _, h := range handlers {
			t.Run(st.name+"/"+h.name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				bindPassthroughRule(c, PlatformOpenAI, []string{"invalid_prompt"}, http.StatusBadRequest)
				resp := &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader(st.stream)),
				}
				svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
				err := h.run(svc, c, resp, &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth})

				require.Error(t, err)
				var failoverErr *UpstreamFailoverError
				require.NotErrorAs(t, err, &failoverErr, "request-scoped refusal must not fail over")
				require.Contains(t, err.Error(), "passthrough rule matched")
				require.Equal(t, http.StatusBadRequest, rec.Code, "passthrough rule must set the client status before any output is committed")

				body := rec.Body.String()
				require.Equal(t, "upstream_error", gjson.Get(body, "error.type").String())
				require.Contains(t, gjson.Get(body, "error.message").String(), "flagged as potentially violating")
				require.NotContains(t, body, "response.failed", "client must receive a JSON error, not a 200 SSE terminal event")
			})
		}
	}
}
