package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// sessionLooker locates an active session by refresh token hash. Misses
// (unknown, expired, revoked) collapse to domain.ErrSessionInvalid.
type sessionLooker interface {
	LookupByTokenHash(ctx context.Context, tokenHash string, now time.Time) (domain.Session, error)
}

// RefreshAccess exchanges a refresh token for a fresh access JWT.
//
// MVP trade-off: refresh token is NOT rotated on use.
type RefreshAccess struct {
	Sessions sessionLooker
	Issuer   accessIssuer
	Clock    clock
}

type RefreshAccessInput struct {
	RefreshToken domain.Token
}

type RefreshAccessOutput struct {
	AccessToken     string
	AccessExpiresAt time.Time
}

func (uc *RefreshAccess) Run(ctx context.Context, in RefreshAccessInput) (RefreshAccessOutput, error) {
	token := domain.Token(strings.TrimSpace(string(in.RefreshToken)))
	if token == "" {
		return RefreshAccessOutput{}, fmt.Errorf("%w: refresh_token is required", domain.ErrInvalidArgument)
	}

	now := uc.Clock.Now()

	session, err := uc.Sessions.LookupByTokenHash(ctx, domain.HashToken(token), now)
	if err != nil {
		return RefreshAccessOutput{}, err
	}

	access, exp, err := uc.Issuer.Issue(session.UserID, now)
	if err != nil {
		return RefreshAccessOutput{}, fmt.Errorf("issue access: %w", err)
	}

	return RefreshAccessOutput{
		AccessToken:     access,
		AccessExpiresAt: exp,
	}, nil
}
