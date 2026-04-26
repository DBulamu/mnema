package auth

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// All ports are declared at the consumer (this package), so tests rely on
// structural typing — adapters live elsewhere; here we drop in plain
// fakes. Keeping fakes minimal makes failure modes (one-method-bad)
// trivial to express in a single test case.

type fakeLinks struct {
	wantTokenHash string
	want          domain.MagicLink
	err           error
	gotTokenHash  string
	gotNow        time.Time
}

func (f *fakeLinks) Consume(_ context.Context, tokenHash string, now time.Time) (domain.MagicLink, error) {
	f.gotTokenHash = tokenHash
	f.gotNow = now
	if f.err != nil {
		return domain.MagicLink{}, f.err
	}
	return f.want, nil
}

type fakeUsers struct {
	want domain.User
	err  error
	got  string
}

func (f *fakeUsers) FindOrCreateByEmail(_ context.Context, email string) (domain.User, error) {
	f.got = email
	if f.err != nil {
		return domain.User{}, f.err
	}
	return f.want, nil
}

type fakeSessions struct {
	id            string
	err           error
	gotUser       string
	gotHash       string
	gotExpires    time.Time
	gotUserAgent  string
	gotIPAddress  *netip.Addr
}

func (f *fakeSessions) Create(_ context.Context, userID, hash string, expiresAt time.Time, ua string, ip *netip.Addr) (string, error) {
	f.gotUser = userID
	f.gotHash = hash
	f.gotExpires = expiresAt
	f.gotUserAgent = ua
	f.gotIPAddress = ip
	if f.err != nil {
		return "", f.err
	}
	return f.id, nil
}

type fakeIssuer struct {
	token   string
	exp     time.Time
	err     error
	gotUser string
	gotNow  time.Time
}

func (f *fakeIssuer) Issue(userID string, now time.Time) (string, time.Time, error) {
	f.gotUser = userID
	f.gotNow = now
	if f.err != nil {
		return "", time.Time{}, f.err
	}
	return f.token, f.exp, nil
}

type fakeTokens struct {
	tok domain.Token
	err error
}

func (f *fakeTokens) NewToken() (domain.Token, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.tok, nil
}

type fakeClock struct{ now time.Time }

func (f fakeClock) Now() time.Time { return f.now }

// happyDeps assembles a consume usecase wired to in-memory fakes that
// always succeed. Individual tests override specific fakes to express
// the failure mode under test.
func happyDeps() (*ConsumeMagicLink, *fakeLinks, *fakeUsers, *fakeSessions, *fakeIssuer, *fakeTokens, fakeClock) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	links := &fakeLinks{want: domain.MagicLink{ID: "link-1", Email: "u@example.com"}}
	users := &fakeUsers{want: domain.User{ID: "user-1", Email: "u@example.com"}}
	sessions := &fakeSessions{id: "sess-1"}
	issuer := &fakeIssuer{token: "access.jwt", exp: now.Add(15 * time.Minute)}
	tokens := &fakeTokens{tok: "rrrrrrrrrrrrrrrr"}
	clock := fakeClock{now: now}
	uc := &ConsumeMagicLink{
		Links:    links,
		Users:    users,
		Sessions: sessions,
		Tokens:   tokens,
		Issuer:   issuer,
		Clock:    clock,
	}
	return uc, links, users, sessions, issuer, tokens, clock
}

func TestConsumeMagicLink_Happy(t *testing.T) {
	uc, links, users, sessions, issuer, _, clock := happyDeps()

	ip := netip.MustParseAddr("203.0.113.7")
	out, err := uc.Run(context.Background(), ConsumeMagicLinkInput{
		Token:     "raw-magic-token",
		UserAgent: "go-test/1.0",
		IPAddress: &ip,
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	// Hash must match the deterministic sha256 used everywhere; we don't
	// recompute it here — domain.HashToken is the single source of truth.
	if links.gotTokenHash != domain.HashToken("raw-magic-token") {
		t.Errorf("link consume got hash %q, want %q", links.gotTokenHash, domain.HashToken("raw-magic-token"))
	}
	if !links.gotNow.Equal(clock.now) {
		t.Errorf("link consume `now` = %v, want %v", links.gotNow, clock.now)
	}
	if users.got != "u@example.com" {
		t.Errorf("user upsert email = %q", users.got)
	}
	if issuer.gotUser != "user-1" {
		t.Errorf("issuer userID = %q", issuer.gotUser)
	}
	if sessions.gotUser != "user-1" {
		t.Errorf("session userID = %q", sessions.gotUser)
	}
	if sessions.gotHash != domain.HashToken("rrrrrrrrrrrrrrrr") {
		t.Errorf("session refresh hash mismatched")
	}
	if got := sessions.gotExpires.Sub(clock.now); got != defaultRefreshTTL {
		t.Errorf("session TTL = %v, want %v", got, defaultRefreshTTL)
	}
	if sessions.gotUserAgent != "go-test/1.0" {
		t.Errorf("session UA = %q", sessions.gotUserAgent)
	}
	if sessions.gotIPAddress == nil || sessions.gotIPAddress.String() != "203.0.113.7" {
		t.Errorf("session IP = %v", sessions.gotIPAddress)
	}

	if out.AccessToken != "access.jwt" || out.RefreshToken != "rrrrrrrrrrrrrrrr" {
		t.Errorf("output tokens wrong: %+v", out)
	}
	if out.SessionID != "sess-1" || out.User.ID != "user-1" {
		t.Errorf("output ids wrong: %+v", out)
	}
}

func TestConsumeMagicLink_TrimsAndRejectsEmpty(t *testing.T) {
	uc, _, _, _, _, _, _ := happyDeps()
	_, err := uc.Run(context.Background(), ConsumeMagicLinkInput{Token: "   "})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("want ErrInvalidArgument, got %v", err)
	}
}

func TestConsumeMagicLink_PropagatesLinkInvalid(t *testing.T) {
	// Repository says expired/used/missing. Usecase must surface the
	// sentinel verbatim so transport renders 401.
	uc, links, _, _, _, _, _ := happyDeps()
	links.err = domain.ErrLinkInvalid
	_, err := uc.Run(context.Background(), ConsumeMagicLinkInput{Token: "tok"})
	if !errors.Is(err, domain.ErrLinkInvalid) {
		t.Fatalf("want ErrLinkInvalid, got %v", err)
	}
}

func TestConsumeMagicLink_AbortsOnUserUpsertFailure(t *testing.T) {
	uc, _, users, sessions, issuer, _, _ := happyDeps()
	users.err = errors.New("db down")
	_, err := uc.Run(context.Background(), ConsumeMagicLinkInput{Token: "tok"})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	// No session must be created if user upsert fails.
	if sessions.gotUser != "" {
		t.Error("session unexpectedly created")
	}
	if issuer.gotUser != "" {
		t.Error("issuer unexpectedly invoked")
	}
}

func TestConsumeMagicLink_AbortsOnIssuerFailure(t *testing.T) {
	uc, _, _, sessions, issuer, _, _ := happyDeps()
	issuer.err = errors.New("kid not found")
	_, err := uc.Run(context.Background(), ConsumeMagicLinkInput{Token: "tok"})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if sessions.gotUser != "" {
		t.Error("session created despite issuer failure — order wrong")
	}
}

func TestConsumeMagicLink_AbortsOnTokenGenFailure(t *testing.T) {
	uc, _, _, sessions, _, tokens, _ := happyDeps()
	tokens.err = errors.New("rng broken")
	_, err := uc.Run(context.Background(), ConsumeMagicLinkInput{Token: "tok"})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if sessions.gotUser != "" {
		t.Error("session created despite refresh-token gen failure")
	}
}

func TestConsumeMagicLink_HonorsExplicitRefreshTTL(t *testing.T) {
	uc, _, _, sessions, _, _, clock := happyDeps()
	uc.RefreshTTL = 7 * 24 * time.Hour
	if _, err := uc.Run(context.Background(), ConsumeMagicLinkInput{Token: "tok"}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got := sessions.gotExpires.Sub(clock.now); got != 7*24*time.Hour {
		t.Errorf("session TTL = %v, want 7d", got)
	}
}
