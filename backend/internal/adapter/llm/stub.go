// Package llm contains adapters that produce assistant replies for the
// chat usecase. A "real" provider (OpenAI, Anthropic) implements the
// same shape; the stub below is wired in MVP so the chat plumbing can
// be exercised end-to-end before any external dependency is added.
//
// The consumer-side interface lives in the chat usecase — this package
// only needs to provide a struct with a Reply method that matches.
package llm

import (
	"context"
	"strings"
	"unicode/utf8"
)

// Turn is a single message visible to the model. Identical in shape to
// domain.Message but kept local so the chat usecase can pass primitives
// across the boundary without leaking domain types into adapters.
type Turn struct {
	Role    string
	Content string
}

// Stub is a deterministic reply generator used until a real LLM is
// wired in. It does just enough to be useful in the UI: echoes a short
// summary of what the user said so the chat feels alive without making
// promises about quality.
type Stub struct{}

func NewStub() *Stub { return &Stub{} }

// Reply takes the running conversation and returns the assistant's
// next turn. The stub looks at the most recent user message and
// produces a single-line acknowledgement. We intentionally keep the
// output Russian-friendly and short — the chat will be replaced before
// any real user sees this text.
func (s *Stub) Reply(_ context.Context, history []Turn) (string, error) {
	last := lastUserContent(history)
	if last == "" {
		return "Расскажи, что у тебя на уме.", nil
	}
	return "Записал: " + truncate(last, 200), nil
}

// lastUserContent returns the trimmed content of the most recent user
// turn, or "" if none. The chat usecase already guarantees a user turn
// is present — this is defensive, not load-bearing.
func lastUserContent(history []Turn) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			return strings.TrimSpace(history[i].Content)
		}
	}
	return ""
}

// truncate cuts s to at most n runes (not bytes) and appends an ellipsis
// if anything was dropped. Rune-aware so we don't slice mid-codepoint
// on Cyrillic/UTF-8 input.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	cut := 0
	for i := range s {
		if cut == n {
			return s[:i] + "…"
		}
		cut++
	}
	return s
}
