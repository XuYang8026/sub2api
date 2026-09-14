package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// OpenAI content moderation rejects a prompt with a request-scoped
// invalid_request_error (code invalid_prompt). The Codex backend delivers it
// inside an HTTP 200 stream, first as a bare `error` event and then as
// `response.failed`. The refusal is deterministic for the prompt: replaying the
// same request on another account only reproduces the same rejection, so it
// must never trigger account failover — whichever terminal shape carries it.
func TestOpenAIStreamInvalidPromptRefusalDoesNotFailover(t *testing.T) {
	const flagged = "Invalid prompt: your prompt was flagged as potentially violating our usage policy. " +
		"Please try again with a different prompt: https://platform.openai.com/docs/guides/reasoning#advice-on-prompting"

	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "bare error event with invalid_prompt code",
			payload: `{"error":{"code":"invalid_prompt","message":"` + flagged + `","param":null,"type":"invalid_request_error"},"sequence_number":2,"type":"error"}`,
		},
		{
			name:    "response.failed with invalid_prompt code",
			payload: `{"type":"response.failed","response":{"status":"failed","error":{"code":"invalid_prompt","message":"` + flagged + `"}}}`,
		},
		{
			name:    "bare error event with only invalid_request type and retry wording",
			payload: `{"type":"error","error":{"type":"invalid_request_error","message":"Your request was rejected. Please try again with different content."}}`,
		},
		{
			name:    "bare error event with content policy code and retry wording",
			payload: `{"type":"error","error":{"code":"content_policy_violation","message":"Request blocked by content policy. Please try again with a different prompt."}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(tt.payload)
			message := extractOpenAISSEErrorMessage(payload)
			require.NotEmpty(t, message)
			require.False(t, openAIStreamErrorEventShouldFailover(payload, message), "error event must not fail over")
			require.False(t, openAIStreamFailedEventShouldFailover(payload, message), "response.failed must not fail over")
		})
	}
}

// Retry wording alone still marks a transient upstream failure as retryable.
// The invalid_prompt guard must not swallow these.
func TestOpenAIStreamRetryWordingStillFailsOverForTransientErrors(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "overloaded message without code",
			payload: `{"type":"error","error":{"message":"Our servers are currently overloaded. Please try again later."}}`,
		},
		{
			name:    "server_is_overloaded code",
			payload: `{"type":"error","error":{"code":"server_is_overloaded","type":"server_error","message":"The server is overloaded, slow down"}}`,
		},
		{
			name:    "temporary failure wording",
			payload: `{"type":"error","error":{"type":"server_error","message":"A temporary error occurred, please retry"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(tt.payload)
			message := extractOpenAISSEErrorMessage(payload)
			require.True(t, openAIStreamErrorEventShouldFailover(payload, message))
		})
	}
}
