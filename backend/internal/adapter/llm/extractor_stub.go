package llm

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/DBulamu/mnema/backend/internal/usecase/extraction"
)

// ExtractorStub is the deterministic extractor wired in local/test
// environments. It never calls a model: every input becomes one node
// of type "thought" with the trimmed content as the body.
//
// This is enough for the chat → graph plumbing to be exercised E2E
// without an OpenAI key, and gives us a stable baseline so refactors of
// the extraction pipeline don't require eyeballing model output.
type ExtractorStub struct{}

func NewExtractorStub() *ExtractorStub { return &ExtractorStub{} }

// stubExtractContentLimit caps how much of the user's text we paste
// into the node content. Same 200-rune budget as the chat reply stub.
const stubExtractContentLimit = 200

// Extract returns one thought-node carrying a truncated copy of the
// content. Empty input yields an empty Extraction (the caller handles
// the no-op).
func (e *ExtractorStub) Extract(_ context.Context, content string) (extraction.Extraction, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return extraction.Extraction{}, nil
	}
	body := truncateRunes(trimmed, stubExtractContentLimit)
	return extraction.Extraction{
		Nodes: []extraction.ExtractedNode{
			{
				LocalID: "n1",
				Type:    domain.NodeThought,
				Content: &body,
			},
		},
	}, nil
}

// truncateRunes is the rune-aware sibling of the stub-reply truncate.
// Lives here too because adapter/llm/stub.go's helper is unexported and
// duplicating a five-liner is cheaper than carving out a third package.
func truncateRunes(s string, n int) string {
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
