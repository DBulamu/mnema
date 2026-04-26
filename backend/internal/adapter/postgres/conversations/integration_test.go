//go:build integration

package conversations_test

import (
	"context"
	"testing"
	"time"

	"github.com/DBulamu/mnema/backend/internal/adapter/postgres/conversations"
	"github.com/DBulamu/mnema/backend/internal/adapter/postgres/users"
	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/DBulamu/mnema/backend/internal/testutil/pgcontainer"
)

// TestListByUser_KeysetCursor pins the keyset pagination contract:
// (1) first page returns the freshest rows, (2) the cursor anchored at
// the last row of page N skips exactly those rows on page N+1, and
// (3) the pagination is exhaustive — paging through eventually returns
// every row exactly once.
//
// The fixture uses Touch() to spread updated_at across distinct
// timestamps so the (updated_at, id) tuple comparison can be observed
// independently of insert order.
func TestListByUser_KeysetCursor(t *testing.T) {
	stack := pgcontainer.Start(t)
	convRepo := conversations.New(stack.Pool)
	userRepo := users.New(stack.Pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	u, err := userRepo.FindOrCreateByEmail(ctx, "pager@example.com")
	if err != nil {
		t.Fatalf("user: %v", err)
	}

	// Insert 5 conversations and force distinct updated_at timestamps
	// so the order is deterministic. We use the same tenant on purpose —
	// the cursor's row-comparison must be tenant-scoped through the
	// WHERE user_id clause, never relying on global uniqueness of
	// updated_at.
	base := time.Now().UTC().Truncate(time.Second)
	created := make([]domain.Conversation, 0, 5)
	for i := 0; i < 5; i++ {
		c, err := convRepo.Create(ctx, u.ID)
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		// Older updated_at for older index → freshest is i=4.
		if err := convRepo.Touch(ctx, c.ID, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("touch %d: %v", i, err)
		}
		c.UpdatedAt = base.Add(time.Duration(i) * time.Minute)
		created = append(created, c)
	}

	// Page size 2 over 5 rows → pages: [c4,c3], [c2,c1], [c0]
	page1, err := convRepo.ListByUser(ctx, u.ID, 2, nil)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 || page1[0].ID != created[4].ID || page1[1].ID != created[3].ID {
		t.Fatalf("page1 wrong: %+v", page1)
	}

	cur1 := &domain.ConversationCursor{UpdatedAt: page1[1].UpdatedAt, ID: page1[1].ID}
	page2, err := convRepo.ListByUser(ctx, u.ID, 2, cur1)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 || page2[0].ID != created[2].ID || page2[1].ID != created[1].ID {
		t.Fatalf("page2 wrong: %+v", page2)
	}

	cur2 := &domain.ConversationCursor{UpdatedAt: page2[1].UpdatedAt, ID: page2[1].ID}
	page3, err := convRepo.ListByUser(ctx, u.ID, 2, cur2)
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3) != 1 || page3[0].ID != created[0].ID {
		t.Fatalf("page3 wrong: %+v", page3)
	}

	// Past-the-last page must be empty, not erroneous.
	cur3 := &domain.ConversationCursor{UpdatedAt: page3[0].UpdatedAt, ID: page3[0].ID}
	page4, err := convRepo.ListByUser(ctx, u.ID, 2, cur3)
	if err != nil {
		t.Fatalf("page4: %v", err)
	}
	if len(page4) != 0 {
		t.Fatalf("expected empty trailing page, got %+v", page4)
	}
}

// TestListByUser_TenantIsolation locks the keyset's WHERE user_id
// guard: a cursor minted from user A's row must never expose user B's
// rows even when their updated_at falls "below" the anchor. This is
// the rule that keeps us safe if a cursor leaks across accounts.
func TestListByUser_TenantIsolation(t *testing.T) {
	stack := pgcontainer.Start(t)
	convRepo := conversations.New(stack.Pool)
	userRepo := users.New(stack.Pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	a, err := userRepo.FindOrCreateByEmail(ctx, "alice@example.com")
	if err != nil {
		t.Fatalf("user A: %v", err)
	}
	b, err := userRepo.FindOrCreateByEmail(ctx, "bob@example.com")
	if err != nil {
		t.Fatalf("user B: %v", err)
	}

	cA, err := convRepo.Create(ctx, a.ID)
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	if _, err := convRepo.Create(ctx, b.ID); err != nil {
		t.Fatalf("create B: %v", err)
	}

	// A's cursor against B must list only B's rows; A's row stays
	// invisible regardless of how the (updated_at, id) tuple compares.
	cur := &domain.ConversationCursor{
		UpdatedAt: cA.UpdatedAt.Add(time.Hour),
		ID:        cA.ID,
	}
	got, err := convRepo.ListByUser(ctx, b.ID, 10, cur)
	if err != nil {
		t.Fatalf("list B with A's cursor: %v", err)
	}
	for _, row := range got {
		if row.UserID != b.ID {
			t.Errorf("leaked row from %s while listing for %s", row.UserID, b.ID)
		}
	}
}
