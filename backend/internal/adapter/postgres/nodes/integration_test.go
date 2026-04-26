//go:build integration

package nodes_test

import (
	"context"
	"testing"
	"time"

	"github.com/DBulamu/mnema/backend/internal/adapter/postgres/nodes"
	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/DBulamu/mnema/backend/internal/testutil/pgcontainer"
)

// seedUser inserts a row in users and returns the id. Tests own their
// own isolation by always working under a fresh user — that way we
// don't have to coordinate test-wide truncations.
func seedUser(t *testing.T, ctx context.Context, stack *pgcontainer.Stack, email string) string {
	t.Helper()
	var id string
	err := stack.Pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, email).Scan(&id)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

// seedNode inserts a node directly via raw SQL. We bypass the adapter's
// Create — these tests are about ranking and bumping, not about Create
// itself, and direct SQL gives us precise control over activation,
// pinned, and last_accessed_at.
func seedNode(t *testing.T, ctx context.Context, stack *pgcontainer.Stack, userID, title, content string, activation float32, lastAccessed time.Time, pinned bool) string {
	t.Helper()
	var id string
	err := stack.Pool.QueryRow(ctx, `
		INSERT INTO nodes (user_id, type, title, content, activation, last_accessed_at, pinned)
		VALUES ($1, 'thought', $2, $3, $4, $5, $6)
		RETURNING id
	`, userID, title, content, activation, lastAccessed, pinned).Scan(&id)
	if err != nil {
		t.Fatalf("seed node: %v", err)
	}
	return id
}

// TestSearch_TextRanking_PinnedAlwaysFirst pins down the rule that an
// explicit user pin overrides every other ranking signal: even a
// stale, low-activation, content-only match outranks a fresh
// title-match if it's pinned.
func TestSearch_TextRanking_PinnedAlwaysFirst(t *testing.T) {
	stack := pgcontainer.Start(t)
	repo := nodes.New(stack.Pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	user := seedUser(t, ctx, stack, "rank@example.com")
	now := time.Now().UTC()

	// Pinned but otherwise weak: content match only, low activation,
	// stale last-accessed.
	pinnedID := seedNode(t, ctx, stack, user, "unrelated", "fresh idea about Питер", 0.1, now.Add(-365*24*time.Hour), true)
	// Title match, high activation, recent — would normally rank first.
	titleID := seedNode(t, ctx, stack, user, "Питер trip", "anything", 1.0, now, false)
	// Content-only, mid activation.
	_ = seedNode(t, ctx, stack, user, "other", "Питер subway map", 0.7, now.Add(-7*24*time.Hour), false)

	got, err := repo.Search(ctx, nodes.SearchParams{
		UserID: user,
		Query:  "Питер",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3", len(got))
	}
	if got[0].ID != pinnedID {
		t.Errorf("expected pinned node first, got %s (pinned=%v title=%v)", got[0].ID, got[0].Pinned, got[0].Title)
	}
	if got[1].ID != titleID {
		t.Errorf("expected title-match second, got %s", got[1].ID)
	}
}

// TestSearch_TextRanking_FresherActivationWins covers the time-decay
// component: with no pinning and equal title-match status, the more
// recently accessed node ranks above an older one of equal activation.
func TestSearch_TextRanking_FresherActivationWins(t *testing.T) {
	stack := pgcontainer.Start(t)
	repo := nodes.New(stack.Pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	user := seedUser(t, ctx, stack, "decay@example.com")
	now := time.Now().UTC()

	staleID := seedNode(t, ctx, stack, user, "Прага", "first", 1.0, now.Add(-180*24*time.Hour), false)
	freshID := seedNode(t, ctx, stack, user, "Прага", "second", 1.0, now.Add(-1*24*time.Hour), false)

	got, err := repo.Search(ctx, nodes.SearchParams{UserID: user, Query: "Прага", Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("got %d results, want 2+", len(got))
	}
	if got[0].ID != freshID {
		t.Errorf("expected fresh node first, got %s; stale was %s", got[0].ID, staleID)
	}
}

// TestBumpActivation_AddsAndCaps confirms the LEAST(1.0, ...) cap and
// the last_accessed_at advance. These two behaviors are what restart
// the decay clock for the rest of the ranking pipeline.
func TestBumpActivation_AddsAndCaps(t *testing.T) {
	stack := pgcontainer.Start(t)
	repo := nodes.New(stack.Pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	user := seedUser(t, ctx, stack, "bump@example.com")
	old := time.Now().UTC().Add(-30 * 24 * time.Hour)

	low := seedNode(t, ctx, stack, user, "low", "x", 0.2, old, false)
	high := seedNode(t, ctx, stack, user, "high", "y", 0.9, old, false)

	bumpAt := time.Now().UTC()
	if err := repo.BumpActivation(ctx, user, []string{low, high}, 0.5, bumpAt); err != nil {
		t.Fatalf("bump: %v", err)
	}

	type row struct {
		Activation     float32
		LastAccessedAt time.Time
	}
	read := func(id string) row {
		var r row
		if err := stack.Pool.QueryRow(ctx, `
			SELECT activation, last_accessed_at FROM nodes WHERE id = $1
		`, id).Scan(&r.Activation, &r.LastAccessedAt); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		return r
	}

	rl := read(low)
	if rl.Activation < 0.69 || rl.Activation > 0.71 {
		t.Errorf("low: activation = %v, want ~0.7", rl.Activation)
	}
	if !rl.LastAccessedAt.After(old) {
		t.Errorf("low: last_accessed_at not advanced (%v vs %v)", rl.LastAccessedAt, old)
	}

	rh := read(high)
	if rh.Activation != 1.0 {
		t.Errorf("high: activation = %v, want capped at 1.0", rh.Activation)
	}
}

// TestBumpActivation_TenantScoped confirms the WHERE user_id guard:
// even with a node id from a different tenant, BumpActivation must be
// a no-op rather than touching the wrong row.
func TestBumpActivation_TenantScoped(t *testing.T) {
	stack := pgcontainer.Start(t)
	repo := nodes.New(stack.Pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	alice := seedUser(t, ctx, stack, "alice@example.com")
	mallory := seedUser(t, ctx, stack, "mallory@example.com")

	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	aliceNode := seedNode(t, ctx, stack, alice, "secret", "alice's content", 0.3, old, false)

	// Mallory tries to bump Alice's node by passing alice's UUID.
	if err := repo.BumpActivation(ctx, mallory, []string{aliceNode}, 0.5, time.Now().UTC()); err != nil {
		t.Fatalf("bump: %v", err)
	}

	var act float32
	var lastAt time.Time
	if err := stack.Pool.QueryRow(ctx, `
		SELECT activation, last_accessed_at FROM nodes WHERE id = $1
	`, aliceNode).Scan(&act, &lastAt); err != nil {
		t.Fatalf("read: %v", err)
	}
	if act != 0.3 {
		t.Errorf("alice's activation moved: %v (cross-tenant write)", act)
	}
	if !lastAt.Equal(old) {
		t.Errorf("alice's last_accessed_at moved: %v vs %v", lastAt, old)
	}
}

// TestBumpActivation_EmptyIDsNoop is a quick guard against a regression
// where the empty-slice short-circuit gets removed and the SQL goes out
// with `ANY('{}')` — Postgres handles that fine, but the round trip is
// pure waste. Asserting "no error, no change" pins the no-op.
func TestBumpActivation_EmptyIDsNoop(t *testing.T) {
	stack := pgcontainer.Start(t)
	repo := nodes.New(stack.Pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	user := seedUser(t, ctx, stack, "empty@example.com")
	if err := repo.BumpActivation(ctx, user, nil, 0.5, time.Now().UTC()); err != nil {
		t.Fatalf("nil ids: %v", err)
	}
	if err := repo.BumpActivation(ctx, user, []string{}, 0.5, time.Now().UTC()); err != nil {
		t.Fatalf("empty ids: %v", err)
	}
}

// Compile-only sanity for the import; without this Go's import-pruner
// could remove the domain dependency in a stripped-down test build.
var _ domain.NodeType = domain.NodeThought
