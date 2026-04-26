package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// TestRunStream_HappyPath asserts the event order: user_stored,
// then deltas in arrival order, then final with both messages.
// The order is the contract — clients render the user echo before
// any delta, and replace the delta accumulator with the final
// authoritative assistant text.
func TestRunStream_HappyPath(t *testing.T) {
	convs := &fakeConvs{owns: map[string]bool{"c1": true}}
	msgs := &fakeMsgs{
		appended: []domain.Message{
			{ID: "u-1", ConversationID: "c1", Role: domain.RoleUser, Content: "hello"},
			{ID: "a-1", ConversationID: "c1", Role: domain.RoleAssistant, Content: "Hi there"},
		},
	}
	stream := &fakeStreamLLM{pieces: []string{"Hi", " there"}, full: "Hi there"}

	uc := &SendMessage{
		Conversations: convs,
		Messages:      msgs,
		History:       msgs,
		Toucher:       convs,
		LLM:           &fakeSyncLLM{reply: "Hi there"},
		LLMStream:     stream,
		Clock:         frozenClock{},
	}

	var got []string
	err := uc.RunStream(context.Background(), SendMessageInput{
		ConversationID: "c1",
		UserID:         "u1",
		Content:        "hello",
	}, func(ev SendMessageStreamEvent) error {
		switch {
		case ev.UserStored != nil:
			got = append(got, "user:"+ev.UserStored.Message.ID)
		case ev.Delta != nil:
			got = append(got, "delta:"+ev.Delta.Text)
		case ev.Final != nil:
			got = append(got, "final:"+ev.Final.AssistantMessage.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	want := []string{"user:u-1", "delta:Hi", "delta: there", "final:a-1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("event order:\n got %v\nwant %v", got, want)
	}
}

// TestRunStream_FallbackWithoutStreamLLM: when LLMStream is nil the
// usecase must still emit one synthetic delta so the wire shape
// stays uniform for the client.
func TestRunStream_FallbackWithoutStreamLLM(t *testing.T) {
	convs := &fakeConvs{owns: map[string]bool{"c1": true}}
	msgs := &fakeMsgs{
		appended: []domain.Message{
			{ID: "u-1"},
			{ID: "a-1"},
		},
	}
	uc := &SendMessage{
		Conversations: convs,
		Messages:      msgs,
		History:       msgs,
		Toucher:       convs,
		LLM:           &fakeSyncLLM{reply: "fallback reply"},
		Clock:         frozenClock{},
	}
	var deltas []string
	err := uc.RunStream(context.Background(), SendMessageInput{
		ConversationID: "c1", UserID: "u1", Content: "x",
	}, func(ev SendMessageStreamEvent) error {
		if ev.Delta != nil {
			deltas = append(deltas, ev.Delta.Text)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(deltas) != 1 || deltas[0] != "fallback reply" {
		t.Errorf("want one synthetic delta carrying the full reply, got %v", deltas)
	}
}

// TestRunStream_PropagatesEmitError: the usecase must abort when the
// transport emit callback returns an error (typical case: client
// disconnected).
func TestRunStream_PropagatesEmitError(t *testing.T) {
	convs := &fakeConvs{owns: map[string]bool{"c1": true}}
	msgs := &fakeMsgs{appended: []domain.Message{{ID: "u-1"}}}
	uc := &SendMessage{
		Conversations: convs,
		Messages:      msgs,
		History:       msgs,
		Toucher:       convs,
		LLM:           &fakeSyncLLM{},
		LLMStream:     &fakeStreamLLM{},
		Clock:         frozenClock{},
	}
	stop := errors.New("client gone")
	err := uc.RunStream(context.Background(), SendMessageInput{
		ConversationID: "c1", UserID: "u1", Content: "x",
	}, func(ev SendMessageStreamEvent) error {
		if ev.UserStored != nil {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("want emit error, got %v", err)
	}
}

// --- fakes --------------------------------------------------------------

type fakeConvs struct {
	owns map[string]bool
}

func (f *fakeConvs) GetByID(_ context.Context, id, _ string) (domain.Conversation, error) {
	if !f.owns[id] {
		return domain.Conversation{}, domain.ErrConversationNotFound
	}
	return domain.Conversation{ID: id}, nil
}

func (f *fakeConvs) Touch(_ context.Context, _ string, _ time.Time) error { return nil }

type fakeMsgs struct {
	appended []domain.Message
	idx      int
}

func (f *fakeMsgs) Append(_ context.Context, _ string, _ domain.MessageRole, _ string) (domain.Message, error) {
	if f.idx >= len(f.appended) {
		return domain.Message{}, errors.New("fakeMsgs: ran out of canned rows")
	}
	m := f.appended[f.idx]
	f.idx++
	return m, nil
}

func (f *fakeMsgs) ListByConversation(_ context.Context, _ string, _ int) ([]domain.Message, error) {
	return nil, nil
}

type fakeSyncLLM struct{ reply string }

func (f *fakeSyncLLM) Reply(_ context.Context, _ []Turn) (string, error) {
	return f.reply, nil
}

type fakeStreamLLM struct {
	pieces []string
	full   string
}

func (f *fakeStreamLLM) ReplyStream(_ context.Context, _ []Turn, emit func(string) error) (string, error) {
	for _, p := range f.pieces {
		if err := emit(p); err != nil {
			return "", err
		}
	}
	return f.full, nil
}

type frozenClock struct{}

func (frozenClock) Now() time.Time { return time.Unix(0, 0).UTC() }
