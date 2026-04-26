package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOpenAIReplyStream_HappyPath drives the streaming path against an
// httptest server that emits OpenAI-shaped SSE frames. We assert the
// emitter sees each chunk in order and the assembled return value is
// the trimmed full text.
func TestOpenAIReplyStream_HappyPath(t *testing.T) {
	frames := []string{
		`data: {"choices":[{"delta":{"content":"Hello"}}]}` + "\n\n",
		`data: {"choices":[{"delta":{"content":" world"}}]}` + "\n\n",
		`data: {"choices":[{"delta":{"content":"!"}}]}` + "\n\n",
		`data: [DONE]` + "\n\n",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Fatalf("auth missing: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, f := range frames {
			_, _ = w.Write([]byte(f))
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
	}))
	defer srv.Close()

	c, _ := NewOpenAI("test", "gpt-4o-mini",
		WithOpenAIBaseURL(srv.URL),
		WithOpenAIHTTPClient(srv.Client()),
	)
	var deltas []string
	out, err := c.ReplyStream(context.Background(), []Turn{{Role: "user", Content: "x"}}, func(d string) error {
		deltas = append(deltas, d)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "Hello world!" {
		t.Errorf("assembled = %q, want 'Hello world!'", out)
	}
	if strings.Join(deltas, "") != "Hello world!" {
		t.Errorf("deltas = %v, want concat = 'Hello world!'", deltas)
	}
}

func TestOpenAIReplyStream_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_api_key","message":"bad"}}`))
	}))
	defer srv.Close()

	c, _ := NewOpenAI("k", "m", WithOpenAIBaseURL(srv.URL))
	_, err := c.ReplyStream(context.Background(), []Turn{{Role: "user", Content: "x"}}, func(string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("want upstream error message, got %v", err)
	}
}
