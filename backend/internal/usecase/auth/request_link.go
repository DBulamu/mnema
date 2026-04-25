// Package auth contains authentication usecases: request/consume magic
// link, refresh access token, logout.
package auth

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

const defaultLinkTTL = 15 * time.Minute

// Each port below is declared at the consumer (this file). Adapters
// satisfy them by structural typing — there is no shared registry.

// magicLinkCreator persists a hashed magic-link record. Primitive params
// (rather than a struct) so the port stays a Go-structural fit no matter
// where the adapter lives — neither side imports the other.
type magicLinkCreator interface {
	Create(
		ctx context.Context,
		email string,
		tokenHash string,
		expiresAt time.Time,
		ipAddress *netip.Addr,
	) (string, error)
}

// tokenGenerator produces opaque random tokens. Injected so tests can
// supply deterministic values.
type tokenGenerator interface {
	NewToken() (domain.Token, error)
}

// emailSender dispatches a transactional email. Primitives only — no
// shared struct — so adapters do not import this package.
type emailSender interface {
	Send(ctx context.Context, to, subject, text string) error
}

// clock returns "now". Pinned in tests.
type clock interface {
	Now() time.Time
}

// RequestMagicLink emits a sign-in link for the given email.
//
// We deliberately always return success to the caller (even for unknown
// emails) so the response cannot be used to enumerate registered users.
type RequestMagicLink struct {
	Links   magicLinkCreator
	Tokens  tokenGenerator
	Mailer  emailSender
	Clock   clock
	BaseURL string
	LinkTTL time.Duration
}

type RequestMagicLinkInput struct {
	Email     string
	IPAddress *netip.Addr
}

// Run validates the email, persists a hashed token, and dispatches the email.
func (uc *RequestMagicLink) Run(ctx context.Context, in RequestMagicLinkInput) error {
	email := strings.TrimSpace(strings.ToLower(in.Email))
	if email == "" {
		return fmt.Errorf("%w: email is required", domain.ErrInvalidArgument)
	}

	ttl := uc.LinkTTL
	if ttl == 0 {
		ttl = defaultLinkTTL
	}

	token, err := uc.Tokens.NewToken()
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	now := uc.Clock.Now()

	if _, err := uc.Links.Create(
		ctx,
		email,
		domain.HashToken(token),
		now.Add(ttl),
		in.IPAddress,
	); err != nil {
		return fmt.Errorf("persist link: %w", err)
	}

	link := buildMagicLinkURL(uc.BaseURL, token)
	body := fmt.Sprintf(
		"Hi,\n\nClick the link below to sign in to Mnema. It expires in %d minutes.\n\n%s\n\nIf you didn't request this, ignore this email.\n",
		int(ttl.Minutes()),
		link,
	)

	return uc.Mailer.Send(ctx, email, "Your Mnema sign-in link", body)
}

func buildMagicLinkURL(baseURL string, token domain.Token) string {
	return fmt.Sprintf("%s/auth/magic-link?token=%s", strings.TrimRight(baseURL, "/"), token)
}
