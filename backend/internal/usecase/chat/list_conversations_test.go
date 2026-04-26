package chat

import (
	"context"
	"testing"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

type fakeConvLister struct {
	rows     []domain.Conversation
	gotLimit int
	gotAfter *domain.ConversationCursor
}

func (f *fakeConvLister) ListByUser(_ context.Context, _ string, limit int, after *domain.ConversationCursor) ([]domain.Conversation, error) {
	f.gotLimit = limit
	f.gotAfter = after
	if limit > len(f.rows) {
		return f.rows, nil
	}
	return f.rows[:limit], nil
}

func mkConv(i int) domain.Conversation {
	t := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC).Add(-time.Duration(i) * time.Minute)
	return domain.Conversation{
		ID:        "id-" + string(rune('a'+i)),
		UserID:    "u",
		UpdatedAt: t,
		CreatedAt: t,
	}
}

func TestListConversations_FullPage_HasNextCursor(t *testing.T) {
	t.Parallel()

	// Fixture: more rows than the requested page can hold; usecase asks
	// the adapter for limit+1 to detect "more pages exist".
	rows := []domain.Conversation{mkConv(0), mkConv(1), mkConv(2), mkConv(3)}
	uc := &ListConversations{Conversations: &fakeConvLister{rows: rows}}

	out, err := uc.Run(context.Background(), "u", 3, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(out.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(out.Items))
	}
	if out.NextCursor == nil {
		t.Fatal("expected NextCursor to be set")
	}
	// NextCursor anchors at the last RETURNED item (the 3rd), not the
	// over-fetched 4th — that one is dropped from the response.
	if out.NextCursor.ID != rows[2].ID {
		t.Errorf("cursor anchor mismatch: got %q, want %q", out.NextCursor.ID, rows[2].ID)
	}
}

func TestListConversations_LastPage_NoNextCursor(t *testing.T) {
	t.Parallel()

	rows := []domain.Conversation{mkConv(0), mkConv(1)}
	fl := &fakeConvLister{rows: rows}
	uc := &ListConversations{Conversations: fl}

	out, err := uc.Run(context.Background(), "u", 5, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(out.Items))
	}
	if out.NextCursor != nil {
		t.Errorf("expected nil cursor when fewer than limit rows returned")
	}
	if fl.gotLimit != 6 {
		t.Errorf("usecase must over-fetch by 1: got limit=%d, want 6", fl.gotLimit)
	}
}

func TestListConversations_ForwardsCursor(t *testing.T) {
	t.Parallel()

	fl := &fakeConvLister{}
	uc := &ListConversations{Conversations: fl}

	cur := &domain.ConversationCursor{
		UpdatedAt: time.Now(),
		ID:        "x",
	}
	if _, err := uc.Run(context.Background(), "u", 10, cur); err != nil {
		t.Fatalf("run: %v", err)
	}
	if fl.gotAfter != cur {
		t.Errorf("cursor not forwarded to adapter: got %+v, want %+v", fl.gotAfter, cur)
	}
}

func TestListConversations_RejectsEmptyUserID(t *testing.T) {
	t.Parallel()

	uc := &ListConversations{Conversations: &fakeConvLister{}}
	_, err := uc.Run(context.Background(), "", 10, nil)
	if err == nil {
		t.Fatal("expected error for empty user_id")
	}
}
