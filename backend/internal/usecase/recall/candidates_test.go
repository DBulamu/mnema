package recall

import (
	"context"
	"errors"
	"testing"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

type stubText struct {
	calls int
	hits  map[string][]domain.Node
	err   error
}

func (s *stubText) SearchByText(_ context.Context, _, q string, _ int) ([]domain.Node, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.hits[q], nil
}

type stubVec struct {
	calls int
	hits  []domain.Node
	err   error
}

func (s *stubVec) SearchByVector(_ context.Context, _ string, _ []float32, _ int) ([]domain.Node, error) {
	s.calls++
	return s.hits, s.err
}

type stubEmbed struct{ vec []float32; err error }

func (s *stubEmbed) Embed(_ context.Context, _ string) ([]float32, error) {
	return s.vec, s.err
}

func TestFindCandidates_MergesAndDedups(t *testing.T) {
	textHits := map[string][]domain.Node{
		"Питер": {{ID: "a"}, {ID: "b"}},
		"мама":  {{ID: "b"}, {ID: "c"}}, // b duplicates first hit
	}
	finder := &SearchCandidatesFinder{
		SearchByText:   &stubText{hits: textHits},
		SearchByVector: &stubVec{hits: []domain.Node{{ID: "c"}, {ID: "d"}}},
		Embed:          &stubEmbed{vec: []float32{0.1, 0.2}},
	}
	got, err := finder.FindCandidates(context.Background(), "u1", "вспомни Питер с мамой", []Anchor{
		{Kind: AnchorPlace, Text: "Питер"},
		{Kind: AnchorPerson, Text: "мама"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "b", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("len: want %d got %d (%+v)", len(want), len(got), got)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("[%d]: want %s, got %s", i, id, got[i].ID)
		}
	}
}

func TestFindCandidates_AnchorOrderPreservedFirst(t *testing.T) {
	// Vector hits arrive after anchor hits — anchors keep their slot.
	finder := &SearchCandidatesFinder{
		SearchByText: &stubText{hits: map[string][]domain.Node{"a": {{ID: "n-anchor"}}}},
		SearchByVector: &stubVec{hits: []domain.Node{{ID: "n-vec"}, {ID: "n-anchor"}}},
		Embed:          &stubEmbed{vec: []float32{1}},
	}
	got, _ := finder.FindCandidates(context.Background(), "u1", "anything", []Anchor{{Kind: AnchorTopic, Text: "a"}})
	if got[0].ID != "n-anchor" {
		t.Fatalf("anchor must come first, got %+v", got)
	}
}

func TestFindCandidates_SkipsEmptyAnchors(t *testing.T) {
	st := &stubText{hits: map[string][]domain.Node{}}
	finder := &SearchCandidatesFinder{SearchByText: st}
	_, err := finder.FindCandidates(context.Background(), "u1", "x", []Anchor{
		{Kind: AnchorPlace, Text: ""},
		{Kind: AnchorPlace, Text: "   "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.calls != 0 {
		t.Errorf("empty anchors must not hit the searcher, got %d calls", st.calls)
	}
}

func TestFindCandidates_RespectsMaxCap(t *testing.T) {
	hits := []domain.Node{
		{ID: "1"}, {ID: "2"}, {ID: "3"}, {ID: "4"},
	}
	finder := &SearchCandidatesFinder{
		SearchByText:  &stubText{hits: map[string][]domain.Node{"a": hits}},
		MaxCandidates: 2,
	}
	got, _ := finder.FindCandidates(context.Background(), "u1", "x", []Anchor{{Kind: AnchorTopic, Text: "a"}})
	if len(got) != 2 {
		t.Fatalf("cap=2 not enforced, got %d", len(got))
	}
}

func TestFindCandidates_SemanticOptional(t *testing.T) {
	// No vector searcher / embedder — text-only path still works.
	finder := &SearchCandidatesFinder{
		SearchByText: &stubText{hits: map[string][]domain.Node{"a": {{ID: "x"}}}},
	}
	got, err := finder.FindCandidates(context.Background(), "u1", "free text", []Anchor{{Kind: AnchorTopic, Text: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "x" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestFindCandidates_FailsOnlyWhenBothPathsFail(t *testing.T) {
	finder := &SearchCandidatesFinder{
		SearchByText:   &stubText{err: errors.New("boom-text")},
		SearchByVector: &stubVec{err: errors.New("boom-vec")},
		Embed:          &stubEmbed{vec: []float32{0.1}},
	}
	_, err := finder.FindCandidates(context.Background(), "u1", "free text", []Anchor{{Kind: AnchorTopic, Text: "a"}})
	if err == nil {
		t.Fatal("expected error when both searches fail")
	}
}

func TestFindCandidates_TextWorksEvenIfVectorErrors(t *testing.T) {
	finder := &SearchCandidatesFinder{
		SearchByText:   &stubText{hits: map[string][]domain.Node{"a": {{ID: "ok"}}}},
		SearchByVector: &stubVec{err: errors.New("boom")},
		Embed:          &stubEmbed{vec: []float32{0.1}},
	}
	got, err := finder.FindCandidates(context.Background(), "u1", "x", []Anchor{{Kind: AnchorTopic, Text: "a"}})
	if err != nil {
		t.Fatalf("text-only path should succeed, got %v", err)
	}
	if len(got) != 1 || got[0].ID != "ok" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestFindCandidates_RejectsEmptyUserID(t *testing.T) {
	finder := &SearchCandidatesFinder{}
	_, err := finder.FindCandidates(context.Background(), "", "x", nil)
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("want ErrInvalidArgument, got %v", err)
	}
}
