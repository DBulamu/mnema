package chat

import (
	"context"
	"testing"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

type fakeConvOwner struct {
	conv domain.Conversation
}

func (f *fakeConvOwner) GetByID(_ context.Context, _, _ string) (domain.Conversation, error) {
	return f.conv, nil
}

type fakeMsgLister struct {
	rows      []domain.Message
	gotLimit  int
	gotBefore *domain.MessageCursor
}

func (f *fakeMsgLister) ListByConversation(_ context.Context, _ string, limit int, before *domain.MessageCursor) ([]domain.Message, error) {
	f.gotLimit = limit
	f.gotBefore = before
	// Adapter contract: ASCending by created_at. The "newest tail"
	// semantics mean we deliver the youngest `limit` rows in ASC order.
	if limit >= len(f.rows) {
		return f.rows, nil
	}
	return f.rows[len(f.rows)-limit:], nil
}

func mkMsg(i int) domain.Message {
	return domain.Message{
		ID:        "m" + string(rune('0'+i)),
		Role:      domain.RoleUser,
		Content:   "hi",
		CreatedAt: time.Date(2026, 4, 26, 12, i, 0, 0, time.UTC),
	}
}

func TestGetConversation_FullPage_AnchorIsOldestVisible(t *testing.T) {
	t.Parallel()

	// 5 rows in DB, limit=2. Adapter returns the youngest 3 (ASC):
	// [m2, m3, m4]. Usecase drops the over-fetched oldest sentinel
	// (m2) and exposes [m3, m4]; NextCursor = m3 — the OLDEST visible
	// row, the anchor for "load older".
	rows := []domain.Message{mkMsg(0), mkMsg(1), mkMsg(2), mkMsg(3), mkMsg(4)}
	uc := &GetConversation{
		Conversations: &fakeConvOwner{},
		Messages:      &fakeMsgLister{rows: rows},
	}

	out, err := uc.Run(context.Background(), "c1", "u", 2, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out.Messages))
	}
	if out.Messages[0].ID != "m3" || out.Messages[1].ID != "m4" {
		t.Errorf("page slicing wrong: got %v", []string{out.Messages[0].ID, out.Messages[1].ID})
	}
	if out.NextCursor == nil {
		t.Fatal("expected NextCursor")
	}
	if out.NextCursor.ID != "m3" {
		t.Errorf("cursor anchor must be oldest visible (m3), got %q", out.NextCursor.ID)
	}
}

func TestGetConversation_LastPage_NoCursor(t *testing.T) {
	t.Parallel()

	rows := []domain.Message{mkMsg(0), mkMsg(1)}
	fl := &fakeMsgLister{rows: rows}
	uc := &GetConversation{
		Conversations: &fakeConvOwner{},
		Messages:      fl,
	}

	out, err := uc.Run(context.Background(), "c1", "u", 5, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out.Messages))
	}
	if out.NextCursor != nil {
		t.Errorf("no further page expected, got cursor %+v", out.NextCursor)
	}
	if fl.gotLimit != 6 {
		t.Errorf("usecase must over-fetch by 1: got %d", fl.gotLimit)
	}
}

func TestGetConversation_ForwardsCursor(t *testing.T) {
	t.Parallel()

	fl := &fakeMsgLister{}
	uc := &GetConversation{
		Conversations: &fakeConvOwner{},
		Messages:      fl,
	}

	cur := &domain.MessageCursor{
		CreatedAt: time.Now(),
		ID:        "anchor",
	}
	if _, err := uc.Run(context.Background(), "c1", "u", 10, cur); err != nil {
		t.Fatalf("run: %v", err)
	}
	if fl.gotBefore != cur {
		t.Errorf("cursor not forwarded: got %+v", fl.gotBefore)
	}
}
