package llm

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/DBulamu/mnema/backend/internal/usecase/recall"
)

// Stub adapters for the recall pipeline. Wired in local/test where no
// LLM is available — they let the endpoint, DTOs, and span validation
// be exercised end-to-end without an API key. Real implementations
// (LLM in JSON-mode) come in later roadmap items, see Phase 4.5 in
// wiki/eng/roadmap.md.
//
// Determinism matters here: tests want byte-identical responses.

// RecallAnchorsStub returns anchors heuristically — first three
// non-empty whitespace-separated tokens become topic anchors. Good
// enough to verify that anchors flow into the candidate finder.
type RecallAnchorsStub struct{}

func NewRecallAnchorsStub() *RecallAnchorsStub { return &RecallAnchorsStub{} }

// stubAnchorBudget is small on purpose: anchor extraction is a noisy
// step and the real LLM rarely emits more than a handful of slots.
const stubAnchorBudget = 3

func (s *RecallAnchorsStub) ExtractAnchors(_ context.Context, text, _ string) ([]recall.Anchor, error) {
	out := make([]recall.Anchor, 0, stubAnchorBudget)
	for _, tok := range strings.Fields(text) {
		if len(out) >= stubAnchorBudget {
			break
		}
		out = append(out, recall.Anchor{Kind: recall.AnchorTopic, Text: tok})
	}
	return out, nil
}

// RecallCandidatesStub returns an empty candidate set. Real wiring
// will fan out anchors to graph.Search and merge with phrasal
// embedding top-K — the stub just lets the pipeline complete, which
// keeps the smoke test honest: the answer-generator stub below
// returns "не знаю" because there is nothing to cite.
type RecallCandidatesStub struct{}

func NewRecallCandidatesStub() *RecallCandidatesStub { return &RecallCandidatesStub{} }

func (s *RecallCandidatesStub) FindCandidates(_ context.Context, _, _ string, _ []recall.Anchor) ([]domain.Node, error) {
	return nil, nil
}

// RecallAnswersStub yields a fixed apology. It never produces spans —
// the real generator carries the only legitimate path to non-empty
// spans. This way a stubbed deployment cannot accidentally serve
// node-attributed answers from synthesized content.
type RecallAnswersStub struct{}

func NewRecallAnswersStub() *RecallAnswersStub { return &RecallAnswersStub{} }

const (
	stubAnswerRu = "Пока не могу вспомнить — модель ещё не подключена."
	stubAnswerEn = "I can't recall yet — the model isn't connected."
)

func (s *RecallAnswersStub) GenerateAnswer(_ context.Context, _, lang string, _ []domain.Node) (recall.AnswerDraft, error) {
	answer := stubAnswerRu
	if strings.HasPrefix(strings.ToLower(lang), "en") {
		answer = stubAnswerEn
	}
	// Defence in depth: cap to a sensible rune length even though the
	// constants above are short. Keeps this stub safe to extend.
	answer = stubTruncateRunes(answer, 256)
	return recall.AnswerDraft{Answer: answer}, nil
}

// stubTruncateRunes mirrors the helper used by the extractor stub.
// Local copy avoids exporting the other one across the package.
func stubTruncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	cut := 0
	for i := range s {
		if cut == n {
			return s[:i]
		}
		cut++
	}
	return s
}
