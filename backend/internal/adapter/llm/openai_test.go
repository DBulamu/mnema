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

// TestOpenAIReply_HappyPath verifies that history is forwarded 1:1, the
// optional system prompt is prepended, and the assistant content is
// trimmed of surrounding whitespace.
func TestOpenAIReply_HappyPath(t *testing.T) {
	var captured chatRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"  привет!  "}}]}`))
	}))
	defer srv.Close()

	client, err := NewOpenAI("test-key", "gpt-4o-mini",
		WithOpenAIBaseURL(srv.URL),
		WithOpenAISystemPrompt("be brief"),
	)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}

	reply, err := client.Reply(context.Background(), []Turn{
		{Role: "user", Content: "привет"},
	})
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "привет!" {
		t.Errorf("reply = %q, want %q (trimmed)", reply, "привет!")
	}

	if captured.Model != "gpt-4o-mini" {
		t.Errorf("model = %q, want gpt-4o-mini", captured.Model)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2 (system + user)", len(captured.Messages))
	}
	if captured.Messages[0].Role != "system" || captured.Messages[0].Content != "be brief" {
		t.Errorf("system prompt missing or wrong: %+v", captured.Messages[0])
	}
	if captured.Messages[1].Role != "user" || captured.Messages[1].Content != "привет" {
		t.Errorf("user message wrong: %+v", captured.Messages[1])
	}
}

// TestOpenAIReply_UpstreamError surfaces the upstream error.message
// instead of a generic "non-2xx" so logs are debuggable.
func TestOpenAIReply_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_api_key","message":"bad key"}}`))
	}))
	defer srv.Close()

	client, _ := NewOpenAI("k", "m", WithOpenAIBaseURL(srv.URL))
	_, err := client.Reply(context.Background(), []Turn{{Role: "user", Content: "x"}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "bad key") {
		t.Errorf("error = %v, want to contain 401 and upstream message", err)
	}
}

// TestOpenAIReply_EmptyChoices guards against the case where the model
// returns 200 with no choices — caller should see a clear error, not
// an empty string masquerading as a successful reply.
func TestOpenAIReply_EmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	client, _ := NewOpenAI("k", "m", WithOpenAIBaseURL(srv.URL))
	_, err := client.Reply(context.Background(), []Turn{{Role: "user", Content: "x"}})
	if err == nil {
		t.Fatal("expected error on empty choices")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Errorf("error = %v, want 'no choices'", err)
	}
}

// TestOpenAIReply_MaxTokensWired confirms the cost-floor option ends up
// on the wire, while zero (the default) leaves the field absent so we
// don't accidentally pin every reply to 0 tokens.
func TestOpenAIReply_MaxTokensWired(t *testing.T) {
	cases := []struct {
		name      string
		opts      []OpenAIOption
		wantWire  string // exact substring expected in the JSON body
		bannedKey string // empty means we don't care; otherwise must NOT appear
	}{
		{
			name:      "explicit cap",
			opts:      []OpenAIOption{WithOpenAIMaxTokens(128)},
			wantWire:  `"max_tokens":128`,
			bannedKey: "",
		},
		{
			name:      "default omits field",
			opts:      nil,
			wantWire:  "",
			bannedKey: "max_tokens",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var seen string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				seen = string(body)
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
			}))
			defer srv.Close()

			opts := append([]OpenAIOption{WithOpenAIBaseURL(srv.URL)}, tc.opts...)
			client, err := NewOpenAI("k", "m", opts...)
			if err != nil {
				t.Fatalf("NewOpenAI: %v", err)
			}
			if _, err := client.Reply(context.Background(), []Turn{{Role: "user", Content: "x"}}); err != nil {
				t.Fatalf("Reply: %v", err)
			}
			if tc.wantWire != "" && !strings.Contains(seen, tc.wantWire) {
				t.Errorf("body %q does not contain %q", seen, tc.wantWire)
			}
			if tc.bannedKey != "" && strings.Contains(seen, tc.bannedKey) {
				t.Errorf("body %q unexpectedly contains %q", seen, tc.bannedKey)
			}
		})
	}
}

// TestNewOpenAI_ValidatesArgs ensures construction fails fast rather
// than letting a misconfigured client reach a real network call.
func TestNewOpenAI_ValidatesArgs(t *testing.T) {
	cases := []struct{ name, key, model string }{
		{"empty key", "", "gpt-4o-mini"},
		{"whitespace key", "   ", "gpt-4o-mini"},
		{"empty model", "k", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewOpenAI(tc.key, tc.model); err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}
