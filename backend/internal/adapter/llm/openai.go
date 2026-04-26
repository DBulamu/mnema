package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// openaiDefaultBaseURL is the public OpenAI v1 root. Overridable via
// WithOpenAIBaseURL for OpenAI-compatible endpoints (OpenRouter, Azure,
// self-hosted vLLM).
const openaiDefaultBaseURL = "https://api.openai.com/v1"

// openaiDefaultTimeout caps a single LLM call. LLMs are slow but we
// still need a ceiling so a stuck request doesn't pin a goroutine
// forever — the chat path is sync, so this is also the user's wait.
const openaiDefaultTimeout = 60 * time.Second

// openaiChatCompletionsPath is the chat endpoint relative to baseURL.
const openaiChatCompletionsPath = "/chat/completions"

// openaiErrorBodyLogLimit truncates upstream error bodies in our error
// strings — enough to debug, short enough to not flood logs.
const openaiErrorBodyLogLimit = 500

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
		baseURL: openaiDefaultBaseURL,
		http:    &http.Client{Timeout: openaiDefaultTimeout},
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
		msgs = append(msgs, chatMessage{Role: string(domain.RoleSystem), Content: o.system})
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
		o.baseURL+openaiChatCompletionsPath,
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
		return "", fmt.Errorf("openai: %d: %s", resp.StatusCode, truncateForError(string(raw), openaiErrorBodyLogLimit))
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

// ReplyStream is the streaming variant of Reply. We POST the same
// body with stream=true; OpenAI returns text/event-stream where each
// chunk is "data: {...}\n\n", terminated by "data: [DONE]\n\n". Each
// frame's choices[0].delta.content carries an incremental piece of
// the assistant text. Plain UTF-8, so unlike the recall JSON-mode
// streamer we do not need to buffer for half-runes — OpenAI never
// splits a rune across SSE chunks.
func (o *OpenAI) ReplyStream(ctx context.Context, history []Turn, emit func(string) error) (string, error) {
	msgs := make([]chatMessage, 0, len(history)+1)
	if o.system != "" {
		msgs = append(msgs, chatMessage{Role: string(domain.RoleSystem), Content: o.system})
	}
	for _, t := range history {
		msgs = append(msgs, chatMessage{Role: t.Role, Content: t.Content})
	}

	body, err := json.Marshal(streamingChatRequest{
		Model:    o.model,
		Messages: msgs,
		Stream:   true,
	})
	if err != nil {
		return "", fmt.Errorf("openai: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		o.baseURL+openaiChatCompletionsPath,
		bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("openai: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		var parsed chatResponse
		if jsonErr := json.Unmarshal(raw, &parsed); jsonErr == nil && parsed.Error != nil {
			return "", fmt.Errorf("openai: %d %s: %s", resp.StatusCode, parsed.Error.Type, parsed.Error.Message)
		}
		return "", fmt.Errorf("openai: %d: %s", resp.StatusCode, truncateForError(string(raw), openaiErrorBodyLogLimit))
	}

	var full strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(line[len("data:"):])
		if len(payload) == 0 {
			continue
		}
		if bytes.Equal(payload, []byte("[DONE]")) {
			break
		}
		var frame streamingChatChunk
		if err := json.Unmarshal(payload, &frame); err != nil {
			return "", fmt.Errorf("openai: decode stream frame: %w", err)
		}
		if len(frame.Choices) == 0 {
			continue
		}
		piece := frame.Choices[0].Delta.Content
		if piece == "" {
			continue
		}
		full.WriteString(piece)
		if err := emit(piece); err != nil {
			return "", err
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("openai: read stream: %w", err)
	}
	out := strings.TrimSpace(full.String())
	if out == "" {
		return "", errors.New("openai: empty assistant content")
	}
	return out, nil
}

// streamingChatRequest mirrors chatRequest but adds the stream flag.
// We keep the two types separate so that the non-streaming path's
// JSON shape stays minimal — adding a `stream:false` field there
// would also work, but using separate types makes the request
// intent obvious in the call sites.
type streamingChatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

// streamingChatChunk decodes the slice of the SSE frame that we use.
// Each frame can carry zero or more choices, and the final frame
// carries finish_reason — which we ignore here in favour of the
// "[DONE]" sentinel.
type streamingChatChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
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
