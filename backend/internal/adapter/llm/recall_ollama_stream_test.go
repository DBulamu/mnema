package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// TestAnswerFieldStreamer_PlainAscii checks the happy path: the model
// emits the JSON shape one chunk at a time and the streamer pulls just
// the answer text out, ignoring spans. We use ASCII-only text so we
// can assert byte-for-byte without worrying about UTF-8 splits.
func TestAnswerFieldStreamer_PlainAscii(t *testing.T) {
	chunks := []string{
		`{"answer":"Hello`,
		` world","spans":[`,
		`{"start":0,"end":5,"node_ids":["uuid-1"]}]}`,
	}
	var got strings.Builder
	a := &answerFieldStreamer{emit: func(s string) error {
		got.WriteString(s)
		return nil
	}}
	for _, c := range chunks {
		if err := a.feed(c); err != nil {
			t.Fatal(err)
		}
	}
	if got.String() != "Hello world" {
		t.Errorf("want 'Hello world', got %q", got.String())
	}
}

// TestAnswerFieldStreamer_KeySplitAcrossChunks: the literal `"answer"`
// is split across stream frames. The keyBuf window must hold enough
// trailing bytes to detect the token across that split.
func TestAnswerFieldStreamer_KeySplitAcrossChunks(t *testing.T) {
	chunks := []string{`{"ans`, `wer":"`, `ok"}`}
	var got strings.Builder
	a := &answerFieldStreamer{emit: func(s string) error {
		got.WriteString(s)
		return nil
	}}
	for _, c := range chunks {
		if err := a.feed(c); err != nil {
			t.Fatal(err)
		}
	}
	if got.String() != "ok" {
		t.Errorf("want 'ok', got %q", got.String())
	}
}

// TestAnswerFieldStreamer_Escapes: backslash escapes inside the answer
// are decoded before emission. We do not want the UI to render `\n` as
// the two-char sequence.
func TestAnswerFieldStreamer_Escapes(t *testing.T) {
	in := `{"answer":"line1\nline2\t\"q\"\\","spans":[]}`
	var got strings.Builder
	a := &answerFieldStreamer{emit: func(s string) error {
		got.WriteString(s)
		return nil
	}}
	if err := a.feed(in); err != nil {
		t.Fatal(err)
	}
	want := "line1\nline2\t\"q\"\\"
	if got.String() != want {
		t.Errorf("want %q, got %q", want, got.String())
	}
}

// TestAnswerFieldStreamer_UnicodeEscape: \uXXXX produces the proper
// runes (Cyrillic test).
func TestAnswerFieldStreamer_UnicodeEscape(t *testing.T) {
	// "Привет" = Привет
	in := `{"answer":"Привет"}`
	var got strings.Builder
	a := &answerFieldStreamer{emit: func(s string) error {
		got.WriteString(s)
		return nil
	}}
	if err := a.feed(in); err != nil {
		t.Fatal(err)
	}
	if got.String() != "Привет" {
		t.Errorf("want 'Привет', got %q", got.String())
	}
}

// TestAnswerFieldStreamer_HandlesUTF8AcrossChunks: a multi-byte
// Cyrillic rune is split between two stream frames. The emitter must
// see whole runes, never half-runes (which would render as U+FFFD on
// the wire). This is the regression we hit on the first live qwen
// stream: every Cyrillic character came out as `�`.
func TestAnswerFieldStreamer_HandlesUTF8AcrossChunks(t *testing.T) {
	// "Привет мир" UTF-8: each Cyrillic letter is 2 bytes.
	full := `{"answer":"Привет мир","spans":[]}`
	// Split byte-by-byte to maximise the chance of a mid-rune split.
	var got strings.Builder
	a := &answerFieldStreamer{emit: func(s string) error {
		got.WriteString(s)
		return nil
	}}
	for i := 0; i < len(full); i++ {
		if err := a.feed(full[i : i+1]); err != nil {
			t.Fatal(err)
		}
	}
	if got.String() != "Привет мир" {
		t.Errorf("want 'Привет мир', got %q", got.String())
	}
	for _, r := range got.String() {
		if r == '�' {
			t.Errorf("emitter produced U+FFFD: stream split a UTF-8 rune (%q)", got.String())
		}
	}
}

// TestAnswerFieldStreamer_StopsAtClosingQuote: bytes after the closing
// quote of "answer" are discarded — they belong to spans/etc.
func TestAnswerFieldStreamer_StopsAtClosingQuote(t *testing.T) {
	in := `{"answer":"first"}{"answer":"second"}`
	var got strings.Builder
	a := &answerFieldStreamer{emit: func(s string) error {
		got.WriteString(s)
		return nil
	}}
	if err := a.feed(in); err != nil {
		t.Fatal(err)
	}
	if got.String() != "first" {
		t.Errorf("want 'first', got %q", got.String())
	}
}

// TestRecallAnswersOllama_StreamHappyPath drives the streaming adapter
// against an httptest server that emits NDJSON frames just like ollama.
func TestRecallAnswersOllama_StreamHappyPath(t *testing.T) {
	frames := []string{
		`{"message":{"content":"{\"answer\":\"Hi"},"done":false}` + "\n",
		`{"message":{"content":" there\","},"done":false}` + "\n",
		`{"message":{"content":"\"spans\":[]}"},"done":true}` + "\n",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		for _, f := range frames {
			_, _ = w.Write([]byte(f))
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)

	g, _ := NewRecallAnswersOllama("qwen2.5:7b",
		WithOllamaBaseURL(srv.URL),
		WithOllamaHTTPClient(srv.Client()),
	)

	title := "T"
	cands := []domain.Node{{ID: "uuid-1", Type: domain.NodeEvent, Title: &title}}

	var deltas []string
	draft, err := g.GenerateAnswerStream(context.Background(), "x", "en", cands, func(d string) error {
		deltas = append(deltas, d)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if draft.Answer != "Hi there" {
		t.Errorf("draft answer: want 'Hi there', got %q", draft.Answer)
	}
	combined := strings.Join(deltas, "")
	if combined != "Hi there" {
		t.Errorf("delta sum: want 'Hi there', got %q", combined)
	}
}

// TestRecallAnswersOllama_StreamShortCircuit: zero candidates path
// emits the localised "I don't remember" once and never opens the
// HTTP connection.
func TestRecallAnswersOllama_StreamShortCircuit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request: %s", r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	g, _ := NewRecallAnswersOllama("qwen2.5:7b",
		WithOllamaBaseURL(srv.URL),
		WithOllamaHTTPClient(srv.Client()),
	)

	var deltas []string
	out, err := g.GenerateAnswerStream(context.Background(), "x", "en", nil, func(d string) error {
		deltas = append(deltas, d)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(out.Answer), "don't remember") {
		t.Errorf("unexpected en short-circuit: %q", out.Answer)
	}
	if len(deltas) != 1 || deltas[0] != out.Answer {
		t.Errorf("want one synthetic delta = answer; got %v", deltas)
	}
}
