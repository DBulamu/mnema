package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// sessionRevoker is what Logout needs. We declare a separate interface
// from sessionLooker (instead of merging into "SessionStore") so each
// usecase depends on the smallest possible surface.
type sessionRevoker interface {
	LookupByTokenHash(ctx context.Context, tokenHash string, now time.Time) (domain.Session, error)
	Revoke(ctx context.Context, sessionID string, now time.Time) error
}

// Logout revokes the session whose refresh token is supplied.
//
// Idempotent: revoking an unknown / already-revoked / already-expired
// session returns success rather than an error. Returning an error
// would let clients probe whether a refresh token is still active.
//
// Cross-user safety: the access token's subject (UserID) must match the
// session's owner. Without this check, a user with a valid access
// token could revoke arbitrary sessions of other users.
type Logout struct {
	Sessions sessionRevoker
	Clock    clock
}

type LogoutInput struct {
	UserID       string // from access JWT subject
	RefreshToken domain.Token
}

func (uc *Logout) Run(ctx context.Context, in LogoutInput) error {
	if in.UserID == "" {
		return fmt.Errorf("%w: user_id required", domain.ErrInvalidArgument)
	}
	token := domain.Token(strings.TrimSpace(string(in.RefreshToken)))
	if token == "" {
		return fmt.Errorf("%w: refresh_token is required", domain.ErrInvalidArgument)
	}

	now := uc.Clock.Now()

	session, err := uc.Sessions.LookupByTokenHash(ctx, domain.HashToken(token), now)
	if err != nil {
		if errors.Is(err, domain.ErrSessionInvalid) {
			return nil // already gone — idempotent success
		}
		return err
	}

	// Silent success on cross-user attempt — see godoc above.
	if session.UserID != in.UserID {
		return nil
	}

	return uc.Sessions.Revoke(ctx, session.ID, now)
}
