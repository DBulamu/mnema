package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/DBulamu/mnema/backend/internal/usecase/recall"
)

// captureRequest reads the request body once and stores it for asserts.
type captured struct {
	path string
	body []byte
}

func newCapturingServer(t *testing.T, response any, status int) (*httptest.Server, *captured) {
	t.Helper()
	c := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		c.body = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func TestRecallAnchorsOllama_ParsesAndDropsEmpty(t *testing.T) {
	srv, cap := newCapturingServer(t, ollamaChatResponse{
		Message: struct {
			Content string `json:"content"`
		}{
			Content: `{"place":"Питер","person":"мама","event":"","topic":" ","time":"лето 2024"}`,
		},
	}, http.StatusOK)

	a, err := NewRecallAnchorsOllama("qwen2.5:7b",
		WithOllamaBaseURL(srv.URL),
		WithOllamaHTTPClient(srv.Client()),
	)
	if err != nil {
		t.Fatal(err)
	}

	out, err := a.ExtractAnchors(context.Background(), "вспомни Питер с мамой летом 2024", "ru")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("want 3 anchors (empty/whitespace dropped), got %d: %+v", len(out), out)
	}

	// Order should match the prompt's slot order: place, person, time.
	want := []recall.Anchor{
		{Kind: recall.AnchorPlace, Text: "Питер"},
		{Kind: recall.AnchorPerson, Text: "мама"},
		{Kind: recall.AnchorTime, Text: "лето 2024"},
	}
	for i, w := range want {
		if out[i] != w {
			t.Errorf("anchor[%d]: want %+v, got %+v", i, w, out[i])
		}
	}

	// Sanity: request hit the right path and used format=json.
	if cap.path != ollamaChatPath {
		t.Errorf("path: want %q got %q", ollamaChatPath, cap.path)
	}
	var sent ollamaChatRequest
	if err := json.Unmarshal(cap.body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Format != "json" {
		t.Errorf("want format=json, got %q", sent.Format)
	}
	if sent.Stream {
		t.Errorf("want stream=false")
	}
	if sent.Model != "qwen2.5:7b" {
		t.Errorf("model not propagated: %q", sent.Model)
	}
}

func TestRecallAnchorsOllama_BadJSONErrors(t *testing.T) {
	srv, _ := newCapturingServer(t, ollamaChatResponse{
		Message: struct {
			Content string `json:"content"`
		}{Content: "not json"},
	}, http.StatusOK)

	a, _ := NewRecallAnchorsOllama("qwen2.5:7b",
		WithOllamaBaseURL(srv.URL),
		WithOllamaHTTPClient(srv.Client()),
	)
	_, err := a.ExtractAnchors(context.Background(), "x", "ru")
	if err == nil || !strings.Contains(err.Error(), "decode model output") {
		t.Fatalf("want decode error, got %v", err)
	}
}

func TestRecallAnchorsOllama_UpstreamErrorIsSurfaced(t *testing.T) {
	srv, _ := newCapturingServer(t, map[string]any{"error": "model not loaded"}, http.StatusInternalServerError)
	a, _ := NewRecallAnchorsOllama("qwen2.5:7b",
		WithOllamaBaseURL(srv.URL),
		WithOllamaHTTPClient(srv.Client()),
	)
	_, err := a.ExtractAnchors(context.Background(), "x", "ru")
	if err == nil || !strings.Contains(err.Error(), "model not loaded") {
		t.Fatalf("want upstream error message, got %v", err)
	}
}

func TestRecallAnswersOllama_ShortCircuitsOnZeroCandidates(t *testing.T) {
	// Server should not be reached; a hard-fail handler proves it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request: %s", r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	g, _ := NewRecallAnswersOllama("qwen2.5:7b",
		WithOllamaBaseURL(srv.URL),
		WithOllamaHTTPClient(srv.Client()),
	)
	got, err := g.GenerateAnswer(context.Background(), "anything", "ru", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Answer, "Не помню") {
		t.Errorf("unexpected ru fallback: %q", got.Answer)
	}
	if len(got.Spans) != 0 {
		t.Errorf("want no spans on zero candidates, got %+v", got.Spans)
	}

	// English fallback when lang=en.
	gotEN, _ := g.GenerateAnswer(context.Background(), "x", "en", nil)
	if !strings.Contains(strings.ToLower(gotEN.Answer), "don't remember") {
		t.Errorf("unexpected en fallback: %q", gotEN.Answer)
	}
}

func TestRecallAnswersOllama_HappyPath(t *testing.T) {
	title := "Поездка в Питер"
	candidates := []domain.Node{
		{ID: "uuid-1", Type: domain.NodeEvent, Title: &title},
	}
	srv, cap := newCapturingServer(t, ollamaChatResponse{
		Message: struct {
			Content string `json:"content"`
		}{
			Content: `{"answer":"Ты ездил в Питер.","spans":[{"start":3,"end":15,"node_ids":["uuid-1"]}]}`,
		},
	}, http.StatusOK)

	g, _ := NewRecallAnswersOllama("qwen2.5:7b",
		WithOllamaBaseURL(srv.URL),
		WithOllamaHTTPClient(srv.Client()),
	)
	out, err := g.GenerateAnswer(context.Background(), "когда был в Питере?", "ru", candidates)
	if err != nil {
		t.Fatal(err)
	}
	if out.Answer != "Ты ездил в Питер." {
		t.Errorf("answer: %q", out.Answer)
	}
	if len(out.Spans) != 1 || out.Spans[0].NodeIDs[0] != "uuid-1" {
		t.Errorf("spans: %+v", out.Spans)
	}

	// User message must contain the candidate id and title so the model
	// can cite by id.
	var sent ollamaChatRequest
	if err := json.Unmarshal(cap.body, &sent); err != nil {
		t.Fatal(err)
	}
	if len(sent.Messages) != 2 {
		t.Fatalf("want system+user, got %d", len(sent.Messages))
	}
	if !strings.Contains(sent.Messages[1].Content, "uuid-1") || !strings.Contains(sent.Messages[1].Content, "Поездка в Питер") {
		t.Errorf("user message missing candidate fields:\n%s", sent.Messages[1].Content)
	}
}

func TestRecallAnchorsOllama_PicksLangPrompt(t *testing.T) {
	// We assert the system message text is the language-specific prompt,
	// not the bilingual fallback — qwen2.5:7b would silently pick a
	// language for us if we left the prompt vague.
	tests := []struct {
		name      string
		lang      string
		wantSnip  string
	}{
		{name: "ru explicit", lang: "ru", wantSnip: "якоря"},
		{name: "ru default empty", lang: "", wantSnip: "якоря"},
		{name: "en short", lang: "en", wantSnip: "anchors"},
		{name: "en BCP-47", lang: "en-US", wantSnip: "anchors"},
		{name: "unknown falls back to ru", lang: "fr", wantSnip: "якоря"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, cap := newCapturingServer(t, ollamaChatResponse{
				Message: struct {
					Content string `json:"content"`
				}{Content: `{"place":"","person":"","event":"","topic":"","time":""}`},
			}, http.StatusOK)
			a, _ := NewRecallAnchorsOllama("qwen2.5:7b",
				WithOllamaBaseURL(srv.URL),
				WithOllamaHTTPClient(srv.Client()),
			)
			if _, err := a.ExtractAnchors(context.Background(), "x", tt.lang); err != nil {
				t.Fatal(err)
			}
			var sent ollamaChatRequest
			if err := json.Unmarshal(cap.body, &sent); err != nil {
				t.Fatal(err)
			}
			if len(sent.Messages) < 1 || !strings.Contains(sent.Messages[0].Content, tt.wantSnip) {
				t.Fatalf("system prompt missing %q for lang=%q:\n%s", tt.wantSnip, tt.lang, sent.Messages[0].Content)
			}
		})
	}
}

func TestRecallAnswersOllama_PicksLangPrompt(t *testing.T) {
	title := "Trip to St. Petersburg"
	candidates := []domain.Node{{ID: "uuid-1", Type: domain.NodeEvent, Title: &title}}

	tests := []struct {
		name           string
		lang           string
		wantSysSnip    string
		wantHeaderSnip string
	}{
		{name: "ru", lang: "ru", wantSysSnip: "ПО-РУССКИ", wantHeaderSnip: "Запрос пользователя"},
		{name: "en", lang: "en", wantSysSnip: "IN ENGLISH", wantHeaderSnip: "User query"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, cap := newCapturingServer(t, ollamaChatResponse{
				Message: struct {
					Content string `json:"content"`
				}{Content: `{"answer":"x","spans":[]}`},
			}, http.StatusOK)
			g, _ := NewRecallAnswersOllama("qwen2.5:7b",
				WithOllamaBaseURL(srv.URL),
				WithOllamaHTTPClient(srv.Client()),
			)
			if _, err := g.GenerateAnswer(context.Background(), "remind me", tt.lang, candidates); err != nil {
				t.Fatal(err)
			}
			var sent ollamaChatRequest
			if err := json.Unmarshal(cap.body, &sent); err != nil {
				t.Fatal(err)
			}
			if len(sent.Messages) != 2 {
				t.Fatalf("want system+user, got %d", len(sent.Messages))
			}
			if !strings.Contains(sent.Messages[0].Content, tt.wantSysSnip) {
				t.Errorf("system prompt missing %q:\n%s", tt.wantSysSnip, sent.Messages[0].Content)
			}
			if !strings.Contains(sent.Messages[1].Content, tt.wantHeaderSnip) {
				t.Errorf("user header missing %q:\n%s", tt.wantHeaderSnip, sent.Messages[1].Content)
			}
		})
	}
}

func TestNewRecallAnchorsOllama_RequiresModel(t *testing.T) {
	if _, err := NewRecallAnchorsOllama(""); err == nil {
		t.Fatal("expected error on empty model")
	}
	if _, err := NewRecallAnswersOllama("   "); err == nil {
		t.Fatal("expected error on whitespace model")
	}
}
