//go:build integration

package magiclinks_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DBulamu/mnema/backend/internal/adapter/postgres/magiclinks"
	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/DBulamu/mnema/backend/internal/testutil/pgcontainer"
)

// TestConsume_AtomicSingleUse pins the central guarantee of the consume
// path: a successfully-issued link can be redeemed exactly once. The
// second attempt — whatever the wall-clock — must fail with
// ErrLinkInvalid, so a stolen email link cannot be replayed after the
// real user signs in.
func TestConsume_AtomicSingleUse(t *testing.T) {
	stack := pgcontainer.Start(t)
	repo := magiclinks.New(stack.Pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	now := time.Now().UTC()
	hash := domain.HashToken("plain-token")

	id, err := repo.Create(ctx, "user@example.com", hash, now.Add(15*time.Minute), nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id == "" {
		t.Fatal("create returned empty id")
	}

	link, err := repo.Consume(ctx, hash, now)
	if err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if link.Email != "user@example.com" {
		t.Errorf("email = %q", link.Email)
	}
	if link.ID != id {
		t.Errorf("id = %q, want %q", link.ID, id)
	}

	// Second consume must fail. ErrLinkInvalid covers all rejection
	// reasons (already used / expired / unknown) — we don't probe further.
	if _, err := repo.Consume(ctx, hash, now); !errors.Is(err, domain.ErrLinkInvalid) {
		t.Fatalf("second consume: want ErrLinkInvalid, got %v", err)
	}
}

// TestConsume_ExpiredLink confirms the WHERE clause's expiry guard. We
// use an expiry strictly in the past so even tight clock skew cannot
// turn a stale link into a fresh one.
func TestConsume_ExpiredLink(t *testing.T) {
	stack := pgcontainer.Start(t)
	repo := magiclinks.New(stack.Pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	now := time.Now().UTC()
	hash := domain.HashToken("expired-token")

	if _, err := repo.Create(ctx, "u@example.com", hash, now.Add(-time.Minute), nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.Consume(ctx, hash, now); !errors.Is(err, domain.ErrLinkInvalid) {
		t.Fatalf("want ErrLinkInvalid for expired, got %v", err)
	}
}

// TestConsume_UnknownToken locks in the deny-by-default response: an
// arbitrary hash that was never inserted is rejected without leaking
// the existence-check via a different error.
func TestConsume_UnknownToken(t *testing.T) {
	stack := pgcontainer.Start(t)
	repo := magiclinks.New(stack.Pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := repo.Consume(ctx, domain.HashToken("never-existed"), time.Now().UTC()); !errors.Is(err, domain.ErrLinkInvalid) {
		t.Fatalf("want ErrLinkInvalid for unknown, got %v", err)
	}
}
