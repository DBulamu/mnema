package recall

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

type spyActivator struct {
	calls    int
	gotUser  string
	gotIDs   []string
	gotDelta float32
	gotNow   time.Time
	err      error
}

func (s *spyActivator) BumpActivation(_ context.Context, userID string, ids []string, delta float32, now time.Time) error {
	s.calls++
	s.gotUser = userID
	s.gotIDs = append([]string(nil), ids...)
	s.gotDelta = delta
	s.gotNow = now
	return s.err
}

// pinned ranking lives in SQL (covered by integration tests) — this
// suite focuses on the orchestrator's revival behavior.

func TestRun_BumpsReferencedNodes(t *testing.T) {
	clock := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	cands := []domain.Node{node("u-1"), node("u-2"), node("u-3")}
	a := &fakeAnchors{}
	c := &fakeCands{out: cands}
	g := &fakeAnswers{draft: AnswerDraft{
		Answer: "abcdef",
		Spans: []Span{
			span(0, 3, "u-1"),
			span(3, 6, "u-3"),
		},
	}}
	act := &spyActivator{}
	uc := &Recall{
		Anchors:     a,
		Candidates:  c,
		Answers:     g,
		Activations: act,
		Clock:       func() time.Time { return clock },
	}

	out, err := uc.Run(context.Background(), Input{UserID: "user-x", Text: "hello"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(out.Nodes) != 2 {
		t.Fatalf("expected 2 referenced nodes, got %d", len(out.Nodes))
	}
	if act.calls != 1 {
		t.Fatalf("activator called %d times, want 1", act.calls)
	}
	if act.gotUser != "user-x" {
		t.Errorf("user = %q", act.gotUser)
	}
	if !equalIDs(act.gotIDs, []string{"u-1", "u-3"}) {
		t.Errorf("ids = %v, want [u-1 u-3]", act.gotIDs)
	}
	if act.gotDelta != activationDelta {
		t.Errorf("delta = %v, want %v", act.gotDelta, activationDelta)
	}
	if !act.gotNow.Equal(clock) {
		t.Errorf("now = %v, want %v", act.gotNow, clock)
	}
}

func TestRun_NoActivatorIsNoop(t *testing.T) {
	cands := []domain.Node{node("u-1")}
	uc := &Recall{
		Anchors:    &fakeAnchors{},
		Candidates: &fakeCands{out: cands},
		Answers: &fakeAnswers{draft: AnswerDraft{
			Answer: "abc", Spans: []Span{span(0, 3, "u-1")},
		}},
		// Activations: nil
	}
	if _, err := uc.Run(context.Background(), Input{UserID: "u", Text: "x"}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestRun_NoSpansSkipsActivator(t *testing.T) {
	// Pipeline ran but the model produced no valid spans (or the validator
	// dropped them all). Nothing to revive — activator must not be called
	// or it would touch arbitrary candidates.
	cands := []domain.Node{node("u-1"), node("u-2")}
	act := &spyActivator{}
	uc := &Recall{
		Anchors:     &fakeAnchors{},
		Candidates:  &fakeCands{out: cands},
		Answers:     &fakeAnswers{draft: AnswerDraft{Answer: "abc"}}, // no spans
		Activations: act,
	}
	if _, err := uc.Run(context.Background(), Input{UserID: "u", Text: "x"}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if act.calls != 0 {
		t.Errorf("activator called %d times, want 0", act.calls)
	}
}

func TestRun_SwallowsActivatorError(t *testing.T) {
	// User already got their answer; an activator failure must not
	// reverse that. We log and move on — caller-visible error would
	// punish the wrong action.
	cands := []domain.Node{node("u-1")}
	act := &spyActivator{err: errors.New("db hiccup")}
	uc := &Recall{
		Anchors:     &fakeAnchors{},
		Candidates:  &fakeCands{out: cands},
		Answers:     &fakeAnswers{draft: AnswerDraft{Answer: "abc", Spans: []Span{span(0, 3, "u-1")}}},
		Activations: act,
	}
	out, err := uc.Run(context.Background(), Input{UserID: "u", Text: "x"})
	if err != nil {
		t.Fatalf("got error from happy path: %v", err)
	}
	if out.Answer != "abc" {
		t.Errorf("answer mutated by activator failure: %q", out.Answer)
	}
}

func TestRunStream_BumpsReferencedNodes(t *testing.T) {
	cands := []domain.Node{node("u-1"), node("u-2")}
	stream := &fakeStream{
		pieces: []string{"hi"},
		draft:  AnswerDraft{Answer: "ab", Spans: []Span{span(0, 2, "u-2")}},
	}
	act := &spyActivator{}
	uc := &Recall{
		Anchors:       &fakeAnchors{},
		Candidates:    &fakeCands{out: cands},
		Answers:       &fakeAnswers{},
		AnswersStream: stream,
		Activations:   act,
	}
	err := uc.RunStream(context.Background(), Input{UserID: "u", Text: "x"}, func(StreamEvent) error {
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !equalIDs(act.gotIDs, []string{"u-2"}) {
		t.Errorf("ids = %v, want [u-2]", act.gotIDs)
	}
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
