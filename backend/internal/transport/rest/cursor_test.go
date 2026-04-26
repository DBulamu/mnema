package rest

import (
	"errors"
	"testing"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

func TestConversationCursor_RoundTrip(t *testing.T) {
	t.Parallel()

	in := &domain.ConversationCursor{
		UpdatedAt: time.Date(2026, 4, 26, 12, 34, 56, 789_000_000, time.UTC),
		ID:        "11111111-1111-1111-1111-111111111111",
	}
	enc := encodeConversationCursor(in)
	if enc == "" {
		t.Fatal("encode produced empty string")
	}

	got, err := decodeConversationCursor(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got == nil {
		t.Fatal("decoded cursor is nil")
	}
	if !got.UpdatedAt.Equal(in.UpdatedAt) {
		t.Errorf("UpdatedAt round-trip mismatch: got %v, want %v", got.UpdatedAt, in.UpdatedAt)
	}
	if got.ID != in.ID {
		t.Errorf("ID round-trip mismatch: got %q, want %q", got.ID, in.ID)
	}
}

func TestConversationCursor_EmptyIsFirstPage(t *testing.T) {
	t.Parallel()

	got, err := decodeConversationCursor("")
	if err != nil {
		t.Fatalf("empty cursor must not error: %v", err)
	}
	if got != nil {
		t.Errorf("empty cursor must decode to nil, got %+v", got)
	}
	if encodeConversationCursor(nil) != "" {
		t.Errorf("encoding nil cursor must produce empty string")
	}
}

func TestConversationCursor_GarbageIsClientError(t *testing.T) {
	t.Parallel()

	cases := []string{
		"!!!not-base64!!!",
		"YWJj",                                 // valid base64, invalid JSON ("abc")
		"e30",                                  // {} — missing fields
		"eyJ1IjoiMjAyNiJ9",                     // {"u":"2026"} — bad time
	}
	for _, in := range cases {
		_, err := decodeConversationCursor(in)
		if err == nil {
			t.Errorf("expected error for %q", in)
			continue
		}
		if !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("for %q: error must wrap ErrInvalidArgument, got %v", in, err)
		}
	}
}

func TestMessageCursor_RoundTrip(t *testing.T) {
	t.Parallel()

	in := &domain.MessageCursor{
		CreatedAt: time.Date(2026, 4, 26, 12, 34, 56, 789_000_000, time.UTC),
		ID:        "22222222-2222-2222-2222-222222222222",
	}
	enc := encodeMessageCursor(in)
	got, err := decodeMessageCursor(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got == nil || !got.CreatedAt.Equal(in.CreatedAt) || got.ID != in.ID {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, in)
	}
}

func TestMessageCursor_GarbageIsClientError(t *testing.T) {
	t.Parallel()

	_, err := decodeMessageCursor("!!!")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("error must wrap ErrInvalidArgument, got %v", err)
	}
}
