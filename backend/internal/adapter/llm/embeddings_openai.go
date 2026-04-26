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
)

// openaiEmbeddingsPath is the embeddings endpoint relative to baseURL.
// Same authentication and base URL as chat-completions; only the path
// and request/response schemas differ.
const openaiEmbeddingsPath = "/embeddings"

// EmbedderOpenAI calls OpenAI's embeddings API. Like the other llm-
// package adapters it speaks the public HTTP protocol directly via
// net/http — no SDK — so the request shape stays visible in diffs.
type EmbedderOpenAI struct {
	apiKey  string
	model   string
	baseURL string
	http    *http.Client
}

// EmbedderOpenAIOption configures the client at construction.
type EmbedderOpenAIOption func(*EmbedderOpenAI)

// WithEmbedderOpenAIBaseURL points the client at a different
// embeddings-compatible endpoint (Azure, OpenRouter).
func WithEmbedderOpenAIBaseURL(u string) EmbedderOpenAIOption {
	return func(e *EmbedderOpenAI) { e.baseURL = strings.TrimRight(u, "/") }
}

// WithEmbedderOpenAIHTTPClient overrides the default http.Client.
// Useful in httptest-based unit tests.
func WithEmbedderOpenAIHTTPClient(c *http.Client) EmbedderOpenAIOption {
	return func(e *EmbedderOpenAI) { e.http = c }
}

// NewEmbedderOpenAI builds the embeddings client. apiKey and model are
// required; defaults match the chat client (60s timeout, public OpenAI
// base URL) so deploys only override what they need.
func NewEmbedderOpenAI(apiKey, model string, opts ...EmbedderOpenAIOption) (*EmbedderOpenAI, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("openai embedder: api key is required")
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("openai embedder: model is required")
	}
	e := &EmbedderOpenAI{
		apiKey:  apiKey,
		model:   model,
		baseURL: openaiDefaultBaseURL,
		http:    &http.Client{Timeout: openaiDefaultTimeout},
	}
	for _, opt := range opts {
		opt(e)
	}
	return e, nil
}

// Model returns the configured model id, written alongside the vector
// in the database so a future provider/dimension change can drive a
// re-embed migration.
func (e *EmbedderOpenAI) Model() string { return e.model }

// embeddingsRequest mirrors the subset of the OpenAI embeddings schema
// we use. We keep input as a single string (not a batch) because the
// extraction usecase already iterates per node — batching would couple
// callers to the wire shape and we save little since most messages
// produce one or two nodes.
type embeddingsRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

// embeddingsResponse decodes the path we read; everything else is
// ignored so OpenAI can add fields without breaking us.
type embeddingsResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Embed returns the vector for text. Empty / whitespace-only input
// yields a nil vector and no API call — saves money and matches the
// stub's behaviour so callers can treat "no embedding produced" the
// same way regardless of provider.
func (e *EmbedderOpenAI) Embed(ctx context.Context, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}

	body, err := json.Marshal(embeddingsRequest{Model: e.model, Input: text})
	if err != nil {
		return nil, fmt.Errorf("openai embedder: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		e.baseURL+openaiEmbeddingsPath,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("openai embedder: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai embedder: do request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai embedder: read response: %w", err)
	}

	if resp.StatusCode/100 != 2 {
		var parsed embeddingsResponse
		if jsonErr := json.Unmarshal(raw, &parsed); jsonErr == nil && parsed.Error != nil {
			return nil, fmt.Errorf("openai embedder: %d %s: %s", resp.StatusCode, parsed.Error.Type, parsed.Error.Message)
		}
		return nil, fmt.Errorf("openai embedder: %d: %s", resp.StatusCode, truncateForError(string(raw), openaiErrorBodyLogLimit))
	}

	var parsed embeddingsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("openai embedder: decode response: %w", err)
	}
	if len(parsed.Data) == 0 {
		return nil, errors.New("openai embedder: response has no data")
	}
	if len(parsed.Data[0].Embedding) == 0 {
		return nil, errors.New("openai embedder: empty embedding vector")
	}
	return parsed.Data[0].Embedding, nil
}
