// Package llm is a provider-agnostic client for OpenAI-compatible chat
// completion endpoints (NVIDIA NIM, OpenAI, OpenRouter, vLLM, …). Only the
// surface the Ozon AI manager needs is implemented: non-streaming chat with
// tool calling and token usage accounting.
//
// The default provider (NVIDIA NIM free tier serving a reasoning model) has
// multi-minute latency per request and a ~40 RPM global budget, so the client
// is deliberately synchronous, retries conservatively (max 3 attempts) and
// runs with a long per-request timeout (LLM_TIMEOUT, default 10m).
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog"
)

const (
	// DefaultBaseURL is the NVIDIA NIM OpenAI-compatible endpoint.
	DefaultBaseURL = "https://integrate.api.nvidia.com/v1"
	// DefaultModel is GLM-5.2 served through NVIDIA NIM.
	DefaultModel = "z-ai/glm-5.2"
	// DefaultTimeout accommodates the reasoning model's multi-minute thinking.
	DefaultTimeout = 10 * time.Minute
	// DefaultMaxTokens bounds one completion (reasoning + final tool call).
	DefaultMaxTokens = 8192

	// maxAttempts caps retries on 429/5xx/network errors. NVIDIA free tier
	// routinely 504s while its queue drains — give it real chances.
	maxAttempts = 5
	// defaultRetryDelay is the base backoff when no Retry-After arrives.
	defaultRetryDelay = 20 * time.Second
	// maxRetryDelay caps a server-provided Retry-After.
	maxRetryDelay = 2 * time.Minute
)

// ErrTransient marks failures worth retrying later (provider overload,
// timeouts) — callers can present them as "повторим позже", not as errors.
var ErrTransient = errors.New("transient llm failure")

// ErrModelUnavailable marks a model the provider is not serving right now
// (HTTP 404 on chat/completions). Distinct from ErrTransient: retrying the same
// model does not help, switching models does.
var ErrModelUnavailable = errors.New("llm model unavailable")

// Config configures the client; zero fields fall back to the defaults above.
// An empty APIKey means "AI disabled": Enabled() reports false and every call
// fails fast without touching the network.
type Config struct {
	BaseURL   string
	APIKey    string
	Model     string
	// FallbackModels are tried, in order, when the primary model is not
	// available. NVIDIA's free tier unloads models and answers 404 for hours:
	// half of the AI runs died on «returned 404» while the same model worked
	// again later. A 404 is not a bad request here — it means «this model is
	// not being served right now», and the only useful answer is another model.
	FallbackModels []string
	Timeout        time.Duration
	MaxTokens      int
}

// Client is a minimal OpenAI-compatible chat completions client.
type Client struct {
	cfg        Config
	httpClient *http.Client
	logger     zerolog.Logger
	// retryDelay is defaultRetryDelay in production; tests shrink it.
	retryDelay time.Duration
}

// NewClient builds a client, applying defaults for unset config fields.
func NewClient(cfg Config, logger zerolog.Logger) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = DefaultMaxTokens
	}
	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout},
		logger:     logger.With().Str("component", "llm_client").Logger(),
		retryDelay: defaultRetryDelay,
	}
}

// Enabled reports whether an API key is configured.
func (c *Client) Enabled() bool { return c.cfg.APIKey != "" }

// Model returns the configured model identifier (for logging/audit).
func (c *Client) Model() string { return c.cfg.Model }

// --- wire types (OpenAI chat completions format) ---

// Message is one chat message. Tool results use Role "tool" + ToolCallID.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// Tool declares one callable function in OpenAI function-calling format.
type Tool struct {
	Type     string       `json:"type"` // always "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction is the function part of a tool declaration; Parameters is a
// raw JSON Schema object.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// NewFunctionTool is a small helper for declaring a function tool.
func NewFunctionTool(name, description string, parameters string) Tool {
	return Tool{Type: "function", Function: ToolFunction{
		Name: name, Description: description, Parameters: json.RawMessage(parameters),
	}}
}

// ToolCall is one function invocation the model requested.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction carries the invoked function name and its JSON arguments.
// With a reasoning model the arguments arrive only after (possibly long)
// thinking — callers rely solely on this final JSON.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ForceTool is the tool_choice value that forces one specific function call.
func ForceTool(name string) any {
	return map[string]any{"type": "function", "function": map[string]any{"name": name}}
}

// Usage is the token accounting of one completion.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatRequest is one chat completion request. ToolChoice is nil, "auto",
// "none" or the ForceTool(...) object.
type ChatRequest struct {
	Messages   []Message
	Tools      []Tool
	ToolChoice any
}

// ChatResponse is the parsed first choice plus token usage.
type ChatResponse struct {
	Message      Message
	FinishReason string
	Usage        Usage
}

// FirstToolCall returns the first tool call of the response, if any.
func (r *ChatResponse) FirstToolCall() *ToolCall {
	if r == nil || len(r.Message.ToolCalls) == 0 {
		return nil
	}
	return &r.Message.ToolCalls[0]
}

// --- request execution ---

type wireRequest struct {
	Model      string    `json:"model"`
	Messages   []Message `json:"messages"`
	Tools      []Tool    `json:"tools,omitempty"`
	ToolChoice any       `json:"tool_choice,omitempty"`
	MaxTokens  int       `json:"max_tokens,omitempty"`
	Stream     bool      `json:"stream"`
}

type wireResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// ChatCompletion executes POST {base}/chat/completions and returns the first
// choice. 429 and 5xx responses are retried with exponential backoff (max 3
// attempts total), honoring Retry-After when the server sends one.
// ChatCompletion sends one completion, walking the model list when a model is
// unavailable. Retries on 429/5xx happen inside chatOnce per model.
func (c *Client) ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("llm: client is disabled (no API key configured)")
	}
	models := append([]string{c.cfg.Model}, c.cfg.FallbackModels...)
	var lastErr error
	for i, model := range models {
		resp, err := c.chatOnce(ctx, model, req)
		if err == nil {
			if i > 0 {
				c.logger.Warn().
					Str("primary", c.cfg.Model).
					Str("used", model).
					Msg("primary llm model unavailable; answered by fallback")
			}
			return resp, nil
		}
		lastErr = err
		// Дальше по списку идём только когда модель именно недоступна.
		// Прочие 4xx — наша ошибка в запросе, её сменой модели не исправить.
		if !errors.Is(err, ErrModelUnavailable) {
			return nil, err
		}
		c.logger.Warn().Err(err).Str("model", model).Msg("llm model unavailable; trying next")
	}
	return nil, lastErr
}

func (c *Client) chatOnce(ctx context.Context, model string, req ChatRequest) (*ChatResponse, error) {
	body, err := json.Marshal(wireRequest{
		Model:      model,
		Messages:   req.Messages,
		Tools:      req.Tools,
		ToolChoice: req.ToolChoice,
		MaxTokens:  c.cfg.MaxTokens,
		Stream:     false,
	})
	if err != nil {
		return nil, fmt.Errorf("llm: marshal request: %w", err)
	}

	url := c.cfg.BaseURL + "/chat/completions"
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("llm: create request: %w", err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "application/json")

		start := time.Now()
		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			lastErr = fmt.Errorf("llm: request failed: %w", err)
			c.logger.Warn().Err(err).Int("attempt", attempt).Msg("llm request transport error")
			if attempt < maxAttempts {
				if serr := sleepWithContext(ctx, c.backoff(attempt)); serr != nil {
					return nil, serr
				}
			}
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("llm: read response: %w", readErr)
		}

		switch {
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			delay := c.backoff(attempt)
			if retryAfter := parseRetryAfter(resp.Header.Get("Retry-After")); retryAfter > 0 {
				delay = retryAfter
			}
			lastErr = fmt.Errorf("llm: %s returned %d: %s", url, resp.StatusCode, truncate(string(respBody), 300))
			c.logger.Warn().
				Int("status", resp.StatusCode).
				Int("attempt", attempt).
				Dur("retry_in", delay).
				Msg("llm request retryable failure")
			if attempt < maxAttempts {
				if serr := sleepWithContext(ctx, delay); serr != nil {
					return nil, serr
				}
			}
			continue
		case resp.StatusCode == http.StatusNotFound:
			// Провайдер выгрузил модель. Повторять тот же запрос бессмысленно —
			// отвечает часами; помогает только другая модель.
			return nil, fmt.Errorf("%w: %s returned 404: %s", ErrModelUnavailable, url, truncate(string(respBody), 300))
		case resp.StatusCode >= 400:
			return nil, fmt.Errorf("llm: %s returned %d: %s", url, resp.StatusCode, truncate(string(respBody), 500))
		}

		var wire wireResponse
		if err := json.Unmarshal(respBody, &wire); err != nil {
			return nil, fmt.Errorf("llm: decode response: %w", err)
		}
		if wire.Error != nil {
			return nil, fmt.Errorf("llm: api error: %s", wire.Error.Message)
		}
		if len(wire.Choices) == 0 {
			return nil, fmt.Errorf("llm: response has no choices")
		}
		c.logger.Info().
			Dur("latency", time.Since(start)).
			Int("prompt_tokens", wire.Usage.PromptTokens).
			Int("completion_tokens", wire.Usage.CompletionTokens).
			Int("tool_calls", len(wire.Choices[0].Message.ToolCalls)).
			Msg("llm chat completion done")
		return &ChatResponse{
			Message:      wire.Choices[0].Message,
			FinishReason: wire.Choices[0].FinishReason,
			Usage:        wire.Usage,
		}, nil
	}
	return nil, fmt.Errorf("llm: all %d attempts exhausted: %w: %w", maxAttempts, ErrTransient, lastErr)
}

// backoff grows linearly on the base retry delay: d, 2d, 3d… (exponential on
// a 20s base would overshoot the provider queue's typical drain time).
func (c *Client) backoff(attempt int) time.Duration {
	return c.retryDelay * time.Duration(attempt)
}

// parseRetryAfter understands delay-seconds Retry-After values (the only form
// the target providers send), capped at maxRetryDelay.
func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return 0
	}
	seconds, err := strconv.Atoi(header)
	if err != nil || seconds < 0 {
		return 0
	}
	delay := time.Duration(seconds) * time.Second
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
