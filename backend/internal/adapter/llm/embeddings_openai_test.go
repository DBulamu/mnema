package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestEmbedderOpenAIEmbed_HappyPath verifies that the request shape
// (path, method, auth, body) matches OpenAI's embeddings contract and
// that the returned vector is forwarded to the caller verbatim.
func TestEmbedderOpenAIEmbed_HappyPath(t *testing.T) {
	var captured embeddingsRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/embeddings" {
			t.Fatalf("path = %s, want /embeddings", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,-0.2,0.3]}]}`))
	}))
	defer srv.Close()

	client, err := NewEmbedderOpenAI("test-key", "text-embedding-3-small",
		WithEmbedderOpenAIBaseURL(srv.URL),
	)
	if err != nil {
		t.Fatalf("NewEmbedderOpenAI: %v", err)
	}

	vec, err := client.Embed(context.Background(), "привет")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 3 || vec[0] != 0.1 || vec[1] != -0.2 || vec[2] != 0.3 {
		t.Errorf("vec = %v, want [0.1 -0.2 0.3]", vec)
	}

	if captured.Model != "text-embedding-3-small" {
		t.Errorf("model = %q, want text-embedding-3-small", captured.Model)
	}
	if captured.Input != "привет" {
		t.Errorf("input = %q, want %q", captured.Input, "привет")
	}
}

// TestEmbedderOpenAIEmbed_EmptyInput skips the network entirely on
// whitespace-only input — same contract as the stub.
func TestEmbedderOpenAIEmbed_EmptyInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("unexpected request — empty input should not hit the network")
	}))
	defer srv.Close()

	client, _ := NewEmbedderOpenAI("k", "m", WithEmbedderOpenAIBaseURL(srv.URL))
	vec, err := client.Embed(context.Background(), "   ")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if vec != nil {
		t.Errorf("vec = %v, want nil for empty input", vec)
	}
}

// TestEmbedderOpenAIEmbed_UpstreamError surfaces the upstream
// error.message so logs are debuggable without re-running the request.
func TestEmbedderOpenAIEmbed_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"type":"rate_limit","message":"slow down"}}`))
	}))
	defer srv.Close()

	client, _ := NewEmbedderOpenAI("k", "m", WithEmbedderOpenAIBaseURL(srv.URL))
	_, err := client.Embed(context.Background(), "text")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "slow down") {
		t.Errorf("error = %v, want to contain 429 and upstream message", err)
	}
}

// TestNewEmbedderOpenAI_ValidatesArgs ensures construction fails fast
// rather than letting a misconfigured client reach a real network call.
func TestNewEmbedderOpenAI_ValidatesArgs(t *testing.T) {
	cases := []struct{ name, key, model string }{
		{"empty key", "", "text-embedding-3-small"},
		{"empty model", "k", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewEmbedderOpenAI(tc.key, tc.model); err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}
