package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChatCompletion_ToolCallRoundTripWithRetry drives the full happy path
// through a mocked server: attempt 1 is rate limited (429 + Retry-After),
// attempt 2 returns a forced tool call whose arguments must round-trip.
func TestChatCompletion_ToolCallRoundTripWithRetry(t *testing.T) {
	attempts := 0
	var gotRequest wireRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		require.Equal(t, "/chat/completions", r.URL.Path)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
			return
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotRequest))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{
				"finish_reason": "tool_calls",
				"message": {
					"role": "assistant",
					"content": "",
					"tool_calls": [{
						"id": "call_1",
						"type": "function",
						"function": {
							"name": "submit_proposals",
							"arguments": "{\"summary\":\"ok\",\"proposals\":[{\"action_type\":\"bid_change\",\"new_value\":42}]}"
						}
					}]
				}
			}],
			"usage": {"prompt_tokens": 120, "completion_tokens": 34, "total_tokens": 154}
		}`))
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL, APIKey: "test-key", Model: "test-model"}, zerolog.Nop())
	client.retryDelay = time.Millisecond // keep the test fast

	resp, err := client.ChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{
			{Role: "system", Content: "ты менеджер рекламы"},
			{Role: "user", Content: "контекст"},
		},
		Tools:      []Tool{NewFunctionTool("submit_proposals", "submit", `{"type":"object"}`)},
		ToolChoice: ForceTool("submit_proposals"),
	})
	require.NoError(t, err)
	assert.Equal(t, 2, attempts, "429 must be retried exactly once here")

	// Request wire shape.
	assert.Equal(t, "test-model", gotRequest.Model)
	assert.False(t, gotRequest.Stream)
	assert.Equal(t, DefaultMaxTokens, gotRequest.MaxTokens)
	require.Len(t, gotRequest.Tools, 1)
	assert.Equal(t, "submit_proposals", gotRequest.Tools[0].Function.Name)

	// Response round trip.
	call := resp.FirstToolCall()
	require.NotNil(t, call)
	assert.Equal(t, "submit_proposals", call.Function.Name)
	var args struct {
		Summary   string `json:"summary"`
		Proposals []struct {
			ActionType string  `json:"action_type"`
			NewValue   float64 `json:"new_value"`
		} `json:"proposals"`
	}
	require.NoError(t, json.Unmarshal([]byte(call.Function.Arguments), &args))
	assert.Equal(t, "ok", args.Summary)
	require.Len(t, args.Proposals, 1)
	assert.Equal(t, "bid_change", args.Proposals[0].ActionType)
	assert.Equal(t, 42.0, args.Proposals[0].NewValue)
	assert.Equal(t, 120, resp.Usage.PromptTokens)
	assert.Equal(t, 34, resp.Usage.CompletionTokens)
}

// TestChatCompletion_ExhaustsRetries verifies the attempt cap on persistent 5xx.
func TestChatCompletion_ExhaustsRetries(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL, APIKey: "k"}, zerolog.Nop())
	client.retryDelay = time.Millisecond

	_, err := client.ChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	require.Error(t, err)
	assert.Equal(t, maxAttempts, attempts)
}

// TestChatCompletion_ClientErrorNotRetried: 4xx (other than 429) fails fast.
func TestChatCompletion_ClientErrorNotRetried(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad schema"}}`))
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL, APIKey: "k"}, zerolog.Nop())
	client.retryDelay = time.Millisecond

	_, err := client.ChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	require.Error(t, err)
	assert.Equal(t, 1, attempts)
}

// TestChatCompletion_Disabled fails fast without network when no key is set.
func TestChatCompletion_Disabled(t *testing.T) {
	client := NewClient(Config{}, zerolog.Nop())
	assert.False(t, client.Enabled())
	_, err := client.ChatCompletion(context.Background(), ChatRequest{})
	require.Error(t, err)
}
