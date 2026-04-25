package auth

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

const defaultRefreshTTL = 30 * 24 * time.Hour

// Consumer-declared ports for ConsumeMagicLink.

type magicLinkConsumer interface {
	Consume(ctx context.Context, tokenHash string, now time.Time) (domain.MagicLink, error)
}

type userUpserter interface {
	FindOrCreateByEmail(ctx context.Context, email string) (domain.User, error)
}

type sessionCreator interface {
	Create(
		ctx context.Context,
		userID string,
		refreshTokenHash string,
		expiresAt time.Time,
		userAgent string,
		ipAddress *netip.Addr,
	) (string, error)
}

type accessIssuer interface {
	Issue(userID string, now time.Time) (token string, expiresAt time.Time, err error)
}

// ConsumeMagicLink atomically marks the link as used, finds-or-creates
// the user, and issues an access JWT plus an opaque refresh token.
//
// The three steps are independently idempotent (consume is atomic,
// find-or-create is upsert-on-conflict, session insert generates a
// fresh row), so we do not wrap them in a transaction. A failure
// between steps leaves a consumed-but-unused link — acceptable.
type ConsumeMagicLink struct {
	Links      magicLinkConsumer
	Users      userUpserter
	Sessions   sessionCreator
	Tokens     tokenGenerator
	Issuer     accessIssuer
	Clock      clock
	RefreshTTL time.Duration
}

type ConsumeMagicLinkInput struct {
	Token     domain.Token
	UserAgent string
	IPAddress *netip.Addr
}

type ConsumeMagicLinkOutput struct {
	User             domain.User
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     domain.Token
	RefreshExpiresAt time.Time
	SessionID        string
}

func (uc *ConsumeMagicLink) Run(ctx context.Context, in ConsumeMagicLinkInput) (ConsumeMagicLinkOutput, error) {
	token := domain.Token(strings.TrimSpace(string(in.Token)))
	if token == "" {
		return ConsumeMagicLinkOutput{}, fmt.Errorf("%w: token is required", domain.ErrInvalidArgument)
	}

	now := uc.Clock.Now()

	link, err := uc.Links.Consume(ctx, domain.HashToken(token), now)
	if err != nil {
		return ConsumeMagicLinkOutput{}, err
	}

	user, err := uc.Users.FindOrCreateByEmail(ctx, link.Email)
	if err != nil {
		return ConsumeMagicLinkOutput{}, fmt.Errorf("user upsert: %w", err)
	}

	accessToken, accessExp, err := uc.Issuer.Issue(user.ID, now)
	if err != nil {
		return ConsumeMagicLinkOutput{}, fmt.Errorf("issue access: %w", err)
	}

	refreshTTL := uc.RefreshTTL
	if refreshTTL == 0 {
		refreshTTL = defaultRefreshTTL
	}

	refreshToken, err := uc.Tokens.NewToken()
	if err != nil {
		return ConsumeMagicLinkOutput{}, fmt.Errorf("generate refresh: %w", err)
	}
	refreshExp := now.Add(refreshTTL)

	sessionID, err := uc.Sessions.Create(
		ctx,
		user.ID,
		domain.HashToken(refreshToken),
		refreshExp,
		in.UserAgent,
		in.IPAddress,
	)
	if err != nil {
		return ConsumeMagicLinkOutput{}, fmt.Errorf("create session: %w", err)
	}

	return ConsumeMagicLinkOutput{
		User:             user,
		AccessToken:      accessToken,
		AccessExpiresAt:  accessExp,
		RefreshToken:     refreshToken,
		RefreshExpiresAt: refreshExp,
		SessionID:        sessionID,
	}, nil
}
