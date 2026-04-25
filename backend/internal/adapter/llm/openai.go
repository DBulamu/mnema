package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAI is a chat-completions adapter. It speaks the public OpenAI
// HTTP protocol directly via net/http — no SDK — to keep dependencies
// tight and the request shape obvious in the diff.
//
// The same struct is wired in prod for any OpenAI-compatible endpoint
// (including OpenRouter and self-hosted vLLM): override BaseURL.
type OpenAI struct {
	apiKey  string
	model   string
	baseURL string
	system  string
	http    *http.Client
}

// OpenAIOption configures an OpenAI client at construction time.
type OpenAIOption func(*OpenAI)

// WithOpenAIBaseURL points the client at a different
// chat-completions-compatible endpoint (OpenRouter, Azure, local).
func WithOpenAIBaseURL(u string) OpenAIOption {
	return func(o *OpenAI) { o.baseURL = strings.TrimRight(u, "/") }
}

// WithOpenAISystemPrompt prepends a system message to every request.
// Kept on the adapter because the chat usecase intentionally does not
// know about prompts — that's a provider concern.
func WithOpenAISystemPrompt(prompt string) OpenAIOption {
	return func(o *OpenAI) { o.system = prompt }
}

// WithOpenAIHTTPClient overrides the default http.Client. Useful in
// tests (httptest.Server) and to plug in a tracing transport.
func WithOpenAIHTTPClient(c *http.Client) OpenAIOption {
	return func(o *OpenAI) { o.http = c }
}

// NewOpenAI builds a chat-completions client. apiKey and model are
// required; everything else has sensible defaults aimed at the prod
// OpenAI endpoint with a 60s timeout (LLMs can be slow, but we still
// need a ceiling so a stuck request doesn't pin a goroutine forever).
func NewOpenAI(apiKey, model string, opts ...OpenAIOption) (*OpenAI, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("openai: api key is required")
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("openai: model is required")
	}
	o := &OpenAI{
		apiKey:  apiKey,
		model:   model,
		baseURL: "https://api.openai.com/v1",
		http:    &http.Client{Timeout: 60 * time.Second},
	}
	for _, opt := range opts {
		opt(o)
	}
	return o, nil
}

// chatRequest mirrors the subset of the OpenAI chat-completions schema
// we actually use. Adding fields (temperature, max_tokens) means
// extending this struct — no other layer changes.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse only decodes the path we read. Everything else is
// ignored — letting OpenAI add fields without breaking us.
type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Reply takes the running conversation and returns the assistant's
// next turn. History is mapped 1:1 onto chat-completions messages; an
// optional system prompt is prepended. Non-2xx responses surface as
// errors with the upstream message included so logs are debuggable
// without re-running the request.
func (o *OpenAI) Reply(ctx context.Context, history []Turn) (string, error) {
	msgs := make([]chatMessage, 0, len(history)+1)
	if o.system != "" {
		msgs = append(msgs, chatMessage{Role: "system", Content: o.system})
	}
	for _, t := range history {
		msgs = append(msgs, chatMessage{Role: t.Role, Content: t.Content})
	}

	body, err := json.Marshal(chatRequest{Model: o.model, Messages: msgs})
	if err != nil {
		return "", fmt.Errorf("openai: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		o.baseURL+"/chat/completions",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("openai: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai: do request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("openai: read response: %w", err)
	}

	if resp.StatusCode/100 != 2 {
		// Try to surface upstream's error.message; fall back to the
		// raw body so we never silently swallow useful diagnostics.
		var parsed chatResponse
		if jsonErr := json.Unmarshal(raw, &parsed); jsonErr == nil && parsed.Error != nil {
			return "", fmt.Errorf("openai: %d %s: %s", resp.StatusCode, parsed.Error.Type, parsed.Error.Message)
		}
		return "", fmt.Errorf("openai: %d: %s", resp.StatusCode, truncateForError(string(raw), 500))
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("openai: decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("openai: response has no choices")
	}
	reply := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if reply == "" {
		return "", errors.New("openai: empty assistant content")
	}
	return reply, nil
}

// truncateForError keeps error logs from blowing up on huge response
// bodies. Byte-based slicing is acceptable here: this output is only
// for logs/errors, not user-visible text.
func truncateForError(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
