// Package recall implements the "вспомнить из графа" pipeline:
// free-form text → anchors → per-anchor graph search + phrasal
// embedding top-K → answer LLM with span-attribution → validated
// {answer, spans, nodes}.
//
// This file is the skeleton: input/output shapes, output validation
// (rune-offsets and node-id whitelist), and the orchestrator with
// stubbed pipeline steps. The LLM-driven steps (anchor extraction,
// answer generation) live behind ports that are wired from the
// composition root in later roadmap items — see Phase 4.5 in
// wiki/eng/roadmap.md.
//
// Span offsets are RUNE indices into the answer string, not bytes
// and not UTF-16 code units. Cyrillic is two bytes in UTF-8; JS works
// in UTF-16; Go works in bytes. Runes are the only encoding-neutral
// representation, and the decision is locked in
// wiki/recall-mechanics.md.
package recall

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// Default and max language codes. The MVP only ships Russian; the
// field is on the wire so the contract does not change when we add
// English. We accept anything reasonable here and let downstream
// prompts decide what to do with it — no hard whitelist, because
// new languages should not require a deploy of this package.
const (
	defaultLang = "ru"
	maxLangLen  = 16
	maxTextLen  = 2000
	maxAnswer   = 4000
)

// AnchorKind enumerates the slot names the anchor extractor fills in.
// Values intentionally match the JSON keys used in the LLM prompt so
// the adapter can hand us back the parsed object 1:1.
type AnchorKind string

const (
	AnchorPlace  AnchorKind = "place"
	AnchorPerson AnchorKind = "person"
	AnchorEvent  AnchorKind = "event"
	AnchorTopic  AnchorKind = "topic"
	AnchorTime   AnchorKind = "time"
)

// Anchor is one extracted slot. Empty Text means the LLM left that
// slot unfilled — we drop those before searching.
type Anchor struct {
	Kind AnchorKind
	Text string
}

// Span is a rune-indexed range inside the answer string with the set
// of node ids it points at. End is exclusive, matching []rune slicing
// semantics.
type Span struct {
	Start   int
	End     int
	NodeIDs []string
}

// Output is the validated recall response. Nodes is the union of all
// nodes referenced by surviving spans plus any "supporting" candidates
// the answer-generator found relevant — the transport layer chooses
// what to put on the wire from there.
type Output struct {
	Answer string
	Spans  []Span
	Nodes  []domain.Node
	Lang   string
}

// AnchorExtractor turns the user's text into typed anchors. Stubbed in
// tests; real implementations call an LLM in JSON-mode. Errors are
// fatal here — without anchors the rest of the pipeline cannot run.
type AnchorExtractor interface {
	ExtractAnchors(ctx context.Context, text, lang string) ([]Anchor, error)
}

// CandidateFinder runs the per-anchor and phrasal-embedding searches
// and returns the deduplicated candidate set. Implemented as a thin
// wrapper around graph.Search in the composition root — declared as a
// port so this package never imports the graph usecase.
type CandidateFinder interface {
	FindCandidates(ctx context.Context, userID, text string, anchors []Anchor) ([]domain.Node, error)
}

// AnswerGenerator runs the synthesis LLM call. It receives the user's
// text and the candidate nodes; it returns an answer string plus
// proposed spans. Spans are validated by Recall.Run after this call
// returns — implementations do not need to enforce range or whitelist.
type AnswerGenerator interface {
	GenerateAnswer(ctx context.Context, text, lang string, candidates []domain.Node) (AnswerDraft, error)
}

// AnswerStreamGenerator is the streaming variant of AnswerGenerator.
// Implementations push partial answer text via deltas and a single
// terminal AnswerDraft via final. The channel is closed when the
// generator is done; an error is reported by closing early and
// returning a non-nil error from the call site (see RunStream).
//
// Why a separate interface and not a generic "streaming" wrapper:
// adapters that cannot stream (stub, OpenAI on this codebase today)
// stay simple by not implementing this. Composition root falls back
// to the sync generator and emits a single delta+final pair.
type AnswerStreamGenerator interface {
	GenerateAnswerStream(ctx context.Context, text, lang string, candidates []domain.Node, emit AnswerEmitter) (AnswerDraft, error)
}

// AnswerEmitter is the callback the streaming adapter calls for each
// text fragment as it arrives from the model. Returning an error
// aborts the stream — typically because the client disconnected.
type AnswerEmitter func(delta string) error

// AnswerDraft is the unvalidated answer-generator output. Span offsets
// are rune indices; NodeIDs may include ids the model hallucinated —
// the validator drops those entries.
type AnswerDraft struct {
	Answer string
	Spans  []Span
}

// Recall is the orchestrator. Each port is required; nil ports cause
// run-time errors rather than a silent half-pipeline.
//
// AnswersStream is optional: when nil RunStream falls back to the sync
// AnswerGenerator and emits one synthetic delta carrying the full
// answer. That keeps the SSE contract uniform for clients regardless of
// which provider is wired (stub, ollama, future openai).
type Recall struct {
	Anchors       AnchorExtractor
	Candidates    CandidateFinder
	Answers       AnswerGenerator
	AnswersStream AnswerStreamGenerator
}

// StreamEvent is the union type RunStream pushes to the transport
// layer. Exactly one of the *Event fields is set. Order is fixed:
// Meta, then Candidates, then zero or more Delta, then Final.
//
// We don't model an explicit Error event: errors short-circuit
// RunStream and are returned as Go errors — the transport renders
// them as SSE error events of its own choosing. Mixing in-band errors
// with the contract events confused early prototypes.
type StreamEvent struct {
	Meta       *MetaEvent
	Candidates *CandidatesEvent
	Delta      *DeltaEvent
	Final      *FinalEvent
}

type MetaEvent struct {
	Lang       string
	NumAnchors int
	NumCands   int
}

type CandidatesEvent struct {
	Nodes []domain.Node
}

type DeltaEvent struct {
	Text string
}

type FinalEvent struct {
	Answer string
	Spans  []Span
	Nodes  []domain.Node
}

// Input is what the transport hands the usecase.
type Input struct {
	UserID string
	Text   string
	Lang   string
}

// Run executes the pipeline. The validation block at the top is
// authoritative — transport-layer validation exists for OpenAPI hints
// and 400-friendly errors but the usecase re-checks because a future
// caller may bypass that surface (RPC, tests).
func (uc *Recall) Run(ctx context.Context, in Input) (Output, error) {
	if in.UserID == "" {
		return Output{}, fmt.Errorf("%w: user_id is required", domain.ErrInvalidArgument)
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return Output{}, fmt.Errorf("%w: text is required", domain.ErrInvalidArgument)
	}
	if utf8.RuneCountInString(text) > maxTextLen {
		return Output{}, fmt.Errorf("%w: text exceeds %d runes", domain.ErrInvalidArgument, maxTextLen)
	}
	lang := strings.TrimSpace(in.Lang)
	if lang == "" {
		lang = defaultLang
	}
	if len(lang) > maxLangLen {
		return Output{}, fmt.Errorf("%w: lang too long", domain.ErrInvalidArgument)
	}

	if uc.Anchors == nil || uc.Candidates == nil || uc.Answers == nil {
		// Composition-root mistake, not a caller error — surface as 500
		// (default mapping for an unwrapped error).
		return Output{}, fmt.Errorf("recall: pipeline not fully wired")
	}

	anchors, err := uc.Anchors.ExtractAnchors(ctx, text, lang)
	if err != nil {
		return Output{}, fmt.Errorf("extract anchors: %w", err)
	}
	anchors = dropEmptyAnchors(anchors)

	candidates, err := uc.Candidates.FindCandidates(ctx, in.UserID, text, anchors)
	if err != nil {
		return Output{}, fmt.Errorf("find candidates: %w", err)
	}

	draft, err := uc.Answers.GenerateAnswer(ctx, text, lang, candidates)
	if err != nil {
		return Output{}, fmt.Errorf("generate answer: %w", err)
	}

	answer, spans := validateDraft(draft, candidates)
	nodes := selectReferencedNodes(candidates, spans)

	return Output{
		Answer: answer,
		Spans:  spans,
		Nodes:  nodes,
		Lang:   lang,
	}, nil
}

// RunStream is the streaming variant of Run. The pipeline is
// identical (anchor → candidates → answer → validate), but the
// transport sees four classes of events instead of one final
// response. The emit callback is called synchronously on the
// caller's goroutine — there is no buffering. The transport is
// responsible for not blocking on a slow client (huma/sse handles
// that for us via a write deadline).
//
// Validation runs once at the end, on the assembled answer string.
// We never validate spans against a partial answer because spans
// are rune offsets into the final string, not into the stream so
// far. Streaming the answer is purely a UX latency mask; the
// authoritative output is the Final event.
func (uc *Recall) RunStream(ctx context.Context, in Input, emit func(StreamEvent) error) error {
	if uc.Anchors == nil || uc.Candidates == nil || uc.Answers == nil {
		return fmt.Errorf("recall: pipeline not fully wired")
	}
	if in.UserID == "" {
		return fmt.Errorf("%w: user_id is required", domain.ErrInvalidArgument)
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return fmt.Errorf("%w: text is required", domain.ErrInvalidArgument)
	}
	if utf8.RuneCountInString(text) > maxTextLen {
		return fmt.Errorf("%w: text exceeds %d runes", domain.ErrInvalidArgument, maxTextLen)
	}
	lang := strings.TrimSpace(in.Lang)
	if lang == "" {
		lang = defaultLang
	}
	if len(lang) > maxLangLen {
		return fmt.Errorf("%w: lang too long", domain.ErrInvalidArgument)
	}

	anchors, err := uc.Anchors.ExtractAnchors(ctx, text, lang)
	if err != nil {
		return fmt.Errorf("extract anchors: %w", err)
	}
	anchors = dropEmptyAnchors(anchors)

	candidates, err := uc.Candidates.FindCandidates(ctx, in.UserID, text, anchors)
	if err != nil {
		return fmt.Errorf("find candidates: %w", err)
	}

	if err := emit(StreamEvent{Meta: &MetaEvent{
		Lang:       lang,
		NumAnchors: len(anchors),
		NumCands:   len(candidates),
	}}); err != nil {
		return err
	}
	if err := emit(StreamEvent{Candidates: &CandidatesEvent{Nodes: candidates}}); err != nil {
		return err
	}

	var draft AnswerDraft
	if uc.AnswersStream != nil {
		draft, err = uc.AnswersStream.GenerateAnswerStream(ctx, text, lang, candidates, func(delta string) error {
			if delta == "" {
				return nil
			}
			return emit(StreamEvent{Delta: &DeltaEvent{Text: delta}})
		})
	} else {
		// Fallback for adapters without native streaming. Emit one
		// synthetic delta with the full answer so the wire shape stays
		// uniform.
		draft, err = uc.Answers.GenerateAnswer(ctx, text, lang, candidates)
		if err == nil && draft.Answer != "" {
			if eerr := emit(StreamEvent{Delta: &DeltaEvent{Text: draft.Answer}}); eerr != nil {
				return eerr
			}
		}
	}
	if err != nil {
		return fmt.Errorf("generate answer: %w", err)
	}

	answer, spans := validateDraft(draft, candidates)
	nodes := selectReferencedNodes(candidates, spans)

	return emit(StreamEvent{Final: &FinalEvent{
		Answer: answer,
		Spans:  spans,
		Nodes:  nodes,
	}})
}

func dropEmptyAnchors(in []Anchor) []Anchor {
	out := in[:0]
	for _, a := range in {
		if strings.TrimSpace(a.Text) == "" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// validateDraft enforces the two contract rules from
// wiki/recall-mechanics.md:
//
//  1. Span offsets must lie within the answer's rune length and
//     satisfy 0 ≤ start < end ≤ runeLen. Invalid spans are dropped,
//     the answer text survives.
//  2. node_ids that are not in the candidate set are dropped from a
//     span's NodeIDs. If that empties the span, the span is dropped
//     too — pointing at nothing has no UX value.
//
// We do not currently merge overlapping spans: that is a UX choice
// (do we OR the node_ids? union the highlight?) and the spec leaves
// it as TBD. Two spans covering the same range stay as two spans.
func validateDraft(draft AnswerDraft, candidates []domain.Node) (string, []Span) {
	answer := draft.Answer
	if utf8.RuneCountInString(answer) > maxAnswer {
		answer = truncateRunes(answer, maxAnswer)
	}
	runeLen := utf8.RuneCountInString(answer)

	allowed := make(map[string]struct{}, len(candidates))
	for _, n := range candidates {
		allowed[n.ID] = struct{}{}
	}

	out := make([]Span, 0, len(draft.Spans))
	for _, s := range draft.Spans {
		if s.Start < 0 || s.End <= s.Start || s.End > runeLen {
			continue
		}
		ids := make([]string, 0, len(s.NodeIDs))
		seen := make(map[string]struct{}, len(s.NodeIDs))
		for _, id := range s.NodeIDs {
			if _, ok := allowed[id]; !ok {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			continue
		}
		out = append(out, Span{Start: s.Start, End: s.End, NodeIDs: ids})
	}
	return answer, out
}

// truncateRunes cuts the string at n runes. Used as a defensive cap;
// real answers should be far shorter.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// selectReferencedNodes returns the candidates referenced by at least
// one surviving span, preserving the candidate ordering — which is
// already "best first" because the searcher ranks them. Candidates
// not referenced by any span are intentionally not returned: the
// client's "show the cards behind this answer" view should not be
// padded with nodes the model decided not to cite.
func selectReferencedNodes(candidates []domain.Node, spans []Span) []domain.Node {
	if len(spans) == 0 {
		return nil
	}
	used := make(map[string]struct{})
	for _, s := range spans {
		for _, id := range s.NodeIDs {
			used[id] = struct{}{}
		}
	}
	out := make([]domain.Node, 0, len(used))
	for _, n := range candidates {
		if _, ok := used[n.ID]; ok {
			out = append(out, n)
		}
	}
	return out
}
