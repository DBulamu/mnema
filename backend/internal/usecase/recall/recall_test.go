package recall

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// Compact builders so the table tests below stay readable.
func node(id string) domain.Node    { return domain.Node{ID: id} }
func span(s, e int, ids ...string) Span { return Span{Start: s, End: e, NodeIDs: ids} }

func TestRun_RejectsEmptyUserID(t *testing.T) {
	uc := &Recall{}
	_, err := uc.Run(context.Background(), Input{Text: "hi"})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("want ErrInvalidArgument, got %v", err)
	}
}

func TestRun_RejectsEmptyText(t *testing.T) {
	uc := &Recall{}
	_, err := uc.Run(context.Background(), Input{UserID: "u1", Text: "   "})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("want ErrInvalidArgument, got %v", err)
	}
}

func TestRun_RejectsTooLongText(t *testing.T) {
	uc := &Recall{}
	long := strings.Repeat("я", maxTextLen+1) // multi-byte runes — checks rune count not byte count
	_, err := uc.Run(context.Background(), Input{UserID: "u1", Text: long})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("want ErrInvalidArgument for over-long text, got %v", err)
	}
}

func TestRun_DefaultsLangToRu(t *testing.T) {
	a := &fakeAnchors{}
	c := &fakeCands{}
	g := &fakeAnswers{draft: AnswerDraft{Answer: "ok"}}
	uc := &Recall{Anchors: a, Candidates: c, Answers: g}

	out, err := uc.Run(context.Background(), Input{UserID: "u1", Text: "hello"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out.Lang != "ru" {
		t.Fatalf("default lang: want ru, got %q", out.Lang)
	}
	if a.gotLang != "ru" || g.gotLang != "ru" {
		t.Fatalf("default lang not propagated to ports: anchors=%q answers=%q", a.gotLang, g.gotLang)
	}
}

func TestRun_PreservesExplicitLang(t *testing.T) {
	a := &fakeAnchors{}
	c := &fakeCands{}
	g := &fakeAnswers{draft: AnswerDraft{Answer: "ok"}}
	uc := &Recall{Anchors: a, Candidates: c, Answers: g}

	out, err := uc.Run(context.Background(), Input{UserID: "u1", Text: "hello", Lang: "en"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out.Lang != "en" {
		t.Fatalf("lang: want en, got %q", out.Lang)
	}
}

func TestRun_FailsWhenPipelineNotWired(t *testing.T) {
	uc := &Recall{Anchors: &fakeAnchors{}}
	_, err := uc.Run(context.Background(), Input{UserID: "u1", Text: "hello"})
	if err == nil {
		t.Fatal("expected error on partial wiring")
	}
}

func TestValidateDraft_DropsOutOfRangeSpans(t *testing.T) {
	answer := "Питер" // 5 runes, 10 bytes
	candidates := []domain.Node{node("a"), node("b")}
	draft := AnswerDraft{
		Answer: answer,
		Spans: []Span{
			span(0, 5, "a"),     // ok — full string
			span(0, 6, "a"),     // end > runeLen → drop
			span(-1, 3, "a"),    // negative start → drop
			span(3, 3, "a"),     // empty range → drop
			span(4, 2, "a"),     // end < start → drop
		},
	}
	got, spans := validateDraft(draft, candidates)
	if got != answer {
		t.Fatalf("answer mutated: %q", got)
	}
	if len(spans) != 1 {
		t.Fatalf("want 1 surviving span, got %d: %+v", len(spans), spans)
	}
	if spans[0].Start != 0 || spans[0].End != 5 {
		t.Fatalf("wrong surviving span: %+v", spans[0])
	}
}

func TestValidateDraft_RuneOffsetsNotByteOffsets(t *testing.T) {
	// 5 cyrillic runes = 10 bytes in UTF-8. A byte-based validator
	// would accept end=10 here; a rune-based one must not.
	answer := "Питер"
	candidates := []domain.Node{node("a")}
	draft := AnswerDraft{
		Answer: answer,
		Spans: []Span{
			span(0, 10, "a"), // bytes-shaped → invalid as runes
			span(0, 5, "a"),  // runes-shaped → valid
		},
	}
	_, spans := validateDraft(draft, candidates)
	if len(spans) != 1 {
		t.Fatalf("expected exactly the rune-shaped span to survive, got %d", len(spans))
	}
	if spans[0].End != 5 {
		t.Fatalf("kept the wrong span: %+v", spans[0])
	}
}

func TestValidateDraft_DropsNonCandidateNodeIDs(t *testing.T) {
	candidates := []domain.Node{node("a"), node("b")}
	draft := AnswerDraft{
		Answer: "abcde",
		Spans: []Span{
			span(0, 3, "a", "ghost"), // ghost dropped, span keeps "a"
			span(0, 3, "ghost"),       // empties → drop span
			span(0, 3, "a", "a"),      // dedup
		},
	}
	_, spans := validateDraft(draft, candidates)
	if len(spans) != 2 {
		t.Fatalf("want 2 spans, got %d", len(spans))
	}
	if len(spans[0].NodeIDs) != 1 || spans[0].NodeIDs[0] != "a" {
		t.Fatalf("ghost id leaked: %+v", spans[0])
	}
	if len(spans[1].NodeIDs) != 1 {
		t.Fatalf("dedup failed: %+v", spans[1])
	}
}

func TestValidateDraft_TruncatesOversizedAnswer(t *testing.T) {
	long := strings.Repeat("я", maxAnswer+10)
	draft := AnswerDraft{Answer: long}
	got, _ := validateDraft(draft, nil)
	if got == long {
		t.Fatal("over-size answer not truncated")
	}
	if rs := []rune(got); len(rs) != maxAnswer {
		t.Fatalf("truncated to %d runes, want %d", len(rs), maxAnswer)
	}
}

func TestSelectReferencedNodes_PreservesCandidateOrder(t *testing.T) {
	candidates := []domain.Node{node("a"), node("b"), node("c")}
	spans := []Span{span(0, 1, "c"), span(2, 3, "a")}
	got := selectReferencedNodes(candidates, spans)
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "c" {
		t.Fatalf("order should follow candidate order, got %+v", got)
	}
}

func TestSelectReferencedNodes_NoSpans(t *testing.T) {
	if got := selectReferencedNodes([]domain.Node{node("a")}, nil); got != nil {
		t.Fatalf("want nil when no spans, got %+v", got)
	}
}

// --- fakes --------------------------------------------------------------

type fakeAnchors struct {
	gotLang string
	out     []Anchor
	err     error
}

func (f *fakeAnchors) ExtractAnchors(_ context.Context, _, lang string) ([]Anchor, error) {
	f.gotLang = lang
	return f.out, f.err
}

type fakeCands struct {
	out []domain.Node
	err error
}

func (f *fakeCands) FindCandidates(_ context.Context, _, _ string, _ []Anchor) ([]domain.Node, error) {
	return f.out, f.err
}

type fakeAnswers struct {
	gotLang string
	draft   AnswerDraft
	err     error
}

func (f *fakeAnswers) GenerateAnswer(_ context.Context, _, lang string, _ []domain.Node) (AnswerDraft, error) {
	f.gotLang = lang
	return f.draft, f.err
}

// fakeStream satisfies AnswerStreamGenerator. It emits the Pieces
// list as deltas in order and returns Draft as the final assembled
// answer.
type fakeStream struct {
	gotLang string
	pieces  []string
	draft   AnswerDraft
	err     error
}

func (f *fakeStream) GenerateAnswerStream(_ context.Context, _, lang string, _ []domain.Node, emit AnswerEmitter) (AnswerDraft, error) {
	f.gotLang = lang
	for _, p := range f.pieces {
		if err := emit(p); err != nil {
			return AnswerDraft{}, err
		}
	}
	return f.draft, f.err
}

func TestRunStream_EmitsMetaCandidatesDeltasFinal(t *testing.T) {
	a := &fakeAnchors{out: []Anchor{{Kind: AnchorPlace, Text: "Питер"}}}
	cands := []domain.Node{node("uuid-1"), node("uuid-2")}
	c := &fakeCands{out: cands}
	s := &fakeStream{
		pieces: []string{"Hello", " world"},
		draft:  AnswerDraft{Answer: "Hello world", Spans: []Span{span(0, 5, "uuid-1")}},
	}
	uc := &Recall{Anchors: a, Candidates: c, Answers: &fakeAnswers{}, AnswersStream: s}

	var got []StreamEvent
	err := uc.RunStream(context.Background(), Input{UserID: "u1", Text: "вспомни Питер", Lang: "ru"}, func(ev StreamEvent) error {
		got = append(got, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	// Expected: meta, candidates, delta, delta, final
	if len(got) != 5 {
		t.Fatalf("event count: want 5, got %d (%+v)", len(got), got)
	}
	if got[0].Meta == nil || got[0].Meta.NumCands != 2 || got[0].Meta.NumAnchors != 1 || got[0].Meta.Lang != "ru" {
		t.Errorf("meta wrong: %+v", got[0].Meta)
	}
	if got[1].Candidates == nil || len(got[1].Candidates.Nodes) != 2 {
		t.Errorf("candidates wrong: %+v", got[1])
	}
	if got[2].Delta == nil || got[2].Delta.Text != "Hello" {
		t.Errorf("delta[0]: %+v", got[2])
	}
	if got[3].Delta == nil || got[3].Delta.Text != " world" {
		t.Errorf("delta[1]: %+v", got[3])
	}
	if got[4].Final == nil || got[4].Final.Answer != "Hello world" || len(got[4].Final.Spans) != 1 {
		t.Errorf("final wrong: %+v", got[4].Final)
	}
}

func TestRunStream_FallbackWhenNoStreamGenerator(t *testing.T) {
	a := &fakeAnchors{out: nil}
	c := &fakeCands{out: nil}
	g := &fakeAnswers{draft: AnswerDraft{Answer: "fallback"}}
	uc := &Recall{Anchors: a, Candidates: c, Answers: g} // no AnswersStream

	var deltas []string
	var final *FinalEvent
	err := uc.RunStream(context.Background(), Input{UserID: "u1", Text: "x"}, func(ev StreamEvent) error {
		switch {
		case ev.Delta != nil:
			deltas = append(deltas, ev.Delta.Text)
		case ev.Final != nil:
			final = ev.Final
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(deltas) != 1 || deltas[0] != "fallback" {
		t.Errorf("want one synthetic delta, got %v", deltas)
	}
	if final == nil || final.Answer != "fallback" {
		t.Errorf("final wrong: %+v", final)
	}
}

func TestRunStream_PropagatesEmitError(t *testing.T) {
	a := &fakeAnchors{}
	c := &fakeCands{out: []domain.Node{node("x")}}
	s := &fakeStream{pieces: []string{"a", "b"}, draft: AnswerDraft{Answer: "ab"}}
	uc := &Recall{Anchors: a, Candidates: c, Answers: &fakeAnswers{}, AnswersStream: s}

	stop := errors.New("client disconnected")
	err := uc.RunStream(context.Background(), Input{UserID: "u1", Text: "x"}, func(ev StreamEvent) error {
		// Fail on the candidates event so the stream aborts before
		// the answer step starts.
		if ev.Candidates != nil {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("want emit error to surface, got %v", err)
	}
}
