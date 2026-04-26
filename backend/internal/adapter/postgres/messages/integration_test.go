//go:build integration

package messages_test

import (
	"context"
	"testing"
	"time"

	"github.com/DBulamu/mnema/backend/internal/adapter/postgres/conversations"
	"github.com/DBulamu/mnema/backend/internal/adapter/postgres/messages"
	"github.com/DBulamu/mnema/backend/internal/adapter/postgres/users"
	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/DBulamu/mnema/backend/internal/testutil/pgcontainer"
)

// TestListByConversation_KeysetCursor pins the "load older" semantics:
// a nil cursor returns the freshest tail (in ASC order); supplying the
// first row of that tail as `before` returns the page just below it,
// and so on until we run out. The pages must be contiguous — each call
// continues exactly where the previous one stopped.
//
// We INSERT messages with explicit created_at via raw SQL so the order
// is deterministic; Append() relies on now() which can collide at
// sub-microsecond resolution.
func TestListByConversation_KeysetCursor(t *testing.T) {
	stack := pgcontainer.Start(t)
	msgRepo := messages.New(stack.Pool)
	convRepo := conversations.New(stack.Pool)
	userRepo := users.New(stack.Pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	u, err := userRepo.FindOrCreateByEmail(ctx, "msg-pager@example.com")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	conv, err := convRepo.Create(ctx, u.ID)
	if err != nil {
		t.Fatalf("create conv: %v", err)
	}

	// 5 messages, t0 oldest, t4 newest.
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	created := make([]domain.Message, 0, 5)
	for i := 0; i < 5; i++ {
		var m domain.Message
		var roleStr string
		row := stack.Pool.QueryRow(ctx, `
			INSERT INTO messages (conversation_id, role, content, created_at)
			VALUES ($1, 'user', $2, $3)
			RETURNING id, conversation_id, role, content, created_at
		`, conv.ID, "msg "+string(rune('0'+i)), base.Add(time.Duration(i)*time.Minute))
		if err := row.Scan(&m.ID, &m.ConversationID, &roleStr, &m.Content, &m.CreatedAt); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
		m.Role = domain.MessageRole(roleStr)
		created = append(created, m)
	}

	// First page (no cursor): freshest 2 rows, ASC. → [m3, m4]
	page1, err := msgRepo.ListByConversation(ctx, conv.ID, 2, nil)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 || page1[0].ID != created[3].ID || page1[1].ID != created[4].ID {
		t.Fatalf("page1 wrong: got %v", ids(page1))
	}

	// Second page anchored before the OLDEST visible (m3). → [m1, m2]
	cur1 := &domain.MessageCursor{CreatedAt: page1[0].CreatedAt, ID: page1[0].ID}
	page2, err := msgRepo.ListByConversation(ctx, conv.ID, 2, cur1)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 || page2[0].ID != created[1].ID || page2[1].ID != created[2].ID {
		t.Fatalf("page2 wrong: got %v", ids(page2))
	}

	// Third page → [m0]
	cur2 := &domain.MessageCursor{CreatedAt: page2[0].CreatedAt, ID: page2[0].ID}
	page3, err := msgRepo.ListByConversation(ctx, conv.ID, 2, cur2)
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3) != 1 || page3[0].ID != created[0].ID {
		t.Fatalf("page3 wrong: got %v", ids(page3))
	}

	// Past the last → empty.
	cur3 := &domain.MessageCursor{CreatedAt: page3[0].CreatedAt, ID: page3[0].ID}
	page4, err := msgRepo.ListByConversation(ctx, conv.ID, 2, cur3)
	if err != nil {
		t.Fatalf("page4: %v", err)
	}
	if len(page4) != 0 {
		t.Fatalf("trailing page must be empty, got %v", ids(page4))
	}
}

func ids(ms []domain.Message) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Content)
	}
	return out
}
