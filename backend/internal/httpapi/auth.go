// Package httpapi wires huma operation handlers to internal services.
//
// Handlers live here, business logic lives in domain packages (auth/, etc.).
// The split keeps OpenAPI shape decisions (input/output structs, error
// codes, examples) out of the domain layer so the same services can be
// reused later (CLI, background workers, gRPC).
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/DBulamu/mnema/backend/internal/auth"
	"github.com/DBulamu/mnema/backend/internal/email"
	"github.com/DBulamu/mnema/backend/internal/jwtauth"
	"github.com/DBulamu/mnema/backend/internal/users"
	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog"
)

type AuthDeps struct {
	MagicLinks *auth.MagicLinkStore
	Sessions   *auth.SessionStore
	Users      *users.Store
	JWT        *jwtauth.Issuer
	Email      email.Sender
	Logger     zerolog.Logger
	AppBaseURL string
	RefreshTTL time.Duration
}

type magicLinkRequestInput struct {
	Body struct {
		Email string `json:"email" format:"email" example:"user@example.com" doc:"User email address"`
	}
	XForwardedFor string `header:"X-Forwarded-For"`
	RemoteIP      string `header:"X-Real-IP"`
}

type magicLinkRequestOutput struct {
	Body struct {
		Status string `json:"status" example:"sent" enum:"sent"`
	}
}

func RegisterAuth(api huma.API, deps AuthDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "auth-magic-link-request",
		Method:      http.MethodPost,
		Path:        "/v1/auth/magic-link/request",
		Summary:     "Request a magic-link email",
		Description: "Generates a single-use token, stores its hash, and emails the magic link to the user. Always returns success regardless of whether the email is registered (prevents enumeration).",
		Tags:        []string{"auth"},
	}, func(ctx context.Context, in *magicLinkRequestInput) (*magicLinkRequestOutput, error) {
		emailAddr := strings.TrimSpace(strings.ToLower(in.Body.Email))
		if emailAddr == "" {
			return nil, huma.Error400BadRequest("email is required")
		}

		ip := parseClientIP(in.XForwardedFor, in.RemoteIP)

		issued, err := deps.MagicLinks.Issue(ctx, auth.IssueArgs{
			Email:     emailAddr,
			IPAddress: ip,
		})
		if err != nil {
			deps.Logger.Error().Err(err).Str("email", emailAddr).Msg("issue magic link")
			return nil, huma.Error500InternalServerError("could not issue magic link")
		}

		link := fmt.Sprintf("%s/auth/magic-link?token=%s", strings.TrimRight(deps.AppBaseURL, "/"), issued.Token)
		body := fmt.Sprintf(
			"Hi,\n\nClick the link below to sign in to Mnema. It expires in 15 minutes.\n\n%s\n\nIf you didn't request this, ignore this email.\n",
			link,
		)

		if err := deps.Email.Send(ctx, email.Message{
			To:      emailAddr,
			Subject: "Your Mnema sign-in link",
			Text:    body,
		}); err != nil {
			deps.Logger.Error().Err(err).Str("email", emailAddr).Msg("send magic link email")
			return nil, huma.Error500InternalServerError("could not send email")
		}

		deps.Logger.Info().Str("email", emailAddr).Str("link_id", issued.ID).Msg("magic link sent")

		out := &magicLinkRequestOutput{}
		out.Body.Status = "sent"
		return out, nil
	})

	registerMagicLinkConsume(api, deps)
	registerRefresh(api, deps)
	registerLogout(api, deps)
}

type magicLinkConsumeInput struct {
	Body struct {
		Token string `json:"token" minLength:"10" doc:"Token received in the magic-link email"`
	}
	UserAgent     string `header:"User-Agent"`
	XForwardedFor string `header:"X-Forwarded-For"`
	RemoteIP      string `header:"X-Real-IP"`
}

type magicLinkConsumeOutput struct {
	Body struct {
		AccessToken      string `json:"access_token" doc:"Short-lived JWT for API calls"`
		AccessExpiresAt  string `json:"access_expires_at" format:"date-time"`
		RefreshToken     string `json:"refresh_token" doc:"Opaque refresh token; present on /v1/auth/refresh to renew the access token"`
		RefreshExpiresAt string `json:"refresh_expires_at" format:"date-time"`
		User             struct {
			ID    string `json:"id" format:"uuid"`
			Email string `json:"email" format:"email"`
		} `json:"user"`
	}
}

// registerMagicLinkConsume wires POST /v1/auth/magic-link/consume.
//
// Flow: validate token via MagicLinkStore.Consume (atomic, single-use) →
// look up or create user by email → issue JWT access + opaque refresh →
// return both. We do not wrap this in a DB transaction because each step
// is independently idempotent (consume is atomic, FindOrCreate is
// upsert-on-conflict, session insert generates a fresh row).
func registerMagicLinkConsume(api huma.API, deps AuthDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "auth-magic-link-consume",
		Method:      http.MethodPost,
		Path:        "/v1/auth/magic-link/consume",
		Summary:     "Exchange a magic-link token for access + refresh tokens",
		Description: "Validates the single-use token, finds or creates the user, and returns a JWT access token plus an opaque refresh token.",
		Tags:        []string{"auth"},
	}, func(ctx context.Context, in *magicLinkConsumeInput) (*magicLinkConsumeOutput, error) {
		token := strings.TrimSpace(in.Body.Token)
		if token == "" {
			return nil, huma.Error400BadRequest("token is required")
		}

		consumed, err := deps.MagicLinks.Consume(ctx, auth.Token(token))
		if err != nil {
			if errors.Is(err, auth.ErrLinkInvalid) {
				return nil, huma.Error401Unauthorized("link expired or already used")
			}
			deps.Logger.Error().Err(err).Msg("consume magic link")
			return nil, huma.Error500InternalServerError("could not consume link")
		}

		user, err := deps.Users.FindOrCreateByEmail(ctx, consumed.Email)
		if err != nil {
			deps.Logger.Error().Err(err).Str("email", consumed.Email).Msg("find or create user")
			return nil, huma.Error500InternalServerError("could not establish user")
		}

		now := time.Now().UTC()

		accessToken, accessExp, err := deps.JWT.Issue(user.ID, now)
		if err != nil {
			deps.Logger.Error().Err(err).Str("user_id", user.ID).Msg("issue access token")
			return nil, huma.Error500InternalServerError("could not issue access token")
		}

		ip := parseClientIP(in.XForwardedFor, in.RemoteIP)
		session, err := deps.Sessions.Create(ctx, auth.CreateSessionArgs{
			UserID:    user.ID,
			UserAgent: in.UserAgent,
			IPAddress: ip,
			TTL:       deps.RefreshTTL,
			Now:       now,
		})
		if err != nil {
			deps.Logger.Error().Err(err).Str("user_id", user.ID).Msg("create session")
			return nil, huma.Error500InternalServerError("could not create session")
		}

		deps.Logger.Info().
			Str("user_id", user.ID).
			Str("session_id", session.ID).
			Bool("new_user", user.CreatedAt.After(now.Add(-time.Minute))).
			Msg("login")

		out := &magicLinkConsumeOutput{}
		out.Body.AccessToken = accessToken
		out.Body.AccessExpiresAt = accessExp.Format(time.RFC3339)
		out.Body.RefreshToken = string(session.RefreshToken)
		out.Body.RefreshExpiresAt = session.ExpiresAt.Format(time.RFC3339)
		out.Body.User.ID = user.ID
		out.Body.User.Email = user.Email
		return out, nil
	})
}

type refreshInput struct {
	Body struct {
		RefreshToken string `json:"refresh_token" minLength:"10" doc:"Opaque refresh token returned at login"`
	}
}

type refreshOutput struct {
	Body struct {
		AccessToken     string `json:"access_token"`
		AccessExpiresAt string `json:"access_expires_at" format:"date-time"`
	}
}

// registerRefresh wires POST /v1/auth/refresh.
//
// Trade-off note: this MVP does NOT rotate the refresh token on use. A
// rotating scheme (issue new refresh on each refresh, revoke old) catches
// stolen tokens earlier but adds bookkeeping (token reuse detection,
// session families). We accept the simpler scheme here and revisit when
// we have real users and a threat to defend against.
func registerRefresh(api huma.API, deps AuthDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "auth-refresh",
		Method:      http.MethodPost,
		Path:        "/v1/auth/refresh",
		Summary:     "Exchange a refresh token for a new access token",
		Tags:        []string{"auth"},
	}, func(ctx context.Context, in *refreshInput) (*refreshOutput, error) {
		token := strings.TrimSpace(in.Body.RefreshToken)
		if token == "" {
			return nil, huma.Error400BadRequest("refresh_token is required")
		}

		session, err := deps.Sessions.LookupByToken(ctx, auth.Token(token))
		if err != nil {
			if errors.Is(err, auth.ErrSessionInvalid) {
				return nil, huma.Error401Unauthorized("refresh token invalid or expired")
			}
			deps.Logger.Error().Err(err).Msg("lookup session")
			return nil, huma.Error500InternalServerError("could not refresh")
		}

		access, accessExp, err := deps.JWT.Issue(session.UserID, time.Now().UTC())
		if err != nil {
			deps.Logger.Error().Err(err).Str("user_id", session.UserID).Msg("issue access token")
			return nil, huma.Error500InternalServerError("could not issue access token")
		}

		out := &refreshOutput{}
		out.Body.AccessToken = access
		out.Body.AccessExpiresAt = accessExp.Format(time.RFC3339)
		return out, nil
	})
}

type logoutInput struct {
	Body struct {
		// We accept the refresh token in the body so a single logout call
		// can revoke the session without a separate "list my sessions"
		// step. Bearer access is also required (Security below) so a
		// stolen refresh alone cannot trigger a logout.
		RefreshToken string `json:"refresh_token" minLength:"10"`
	}
}

type logoutOutput struct {
	Body struct {
		Status string `json:"status" enum:"revoked"`
	}
}

// registerLogout wires POST /v1/auth/logout. Idempotent: revoking an
// already-revoked or already-expired session returns success.
func registerLogout(api huma.API, deps AuthDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "auth-logout",
		Method:      http.MethodPost,
		Path:        "/v1/auth/logout",
		Summary:     "Revoke the current refresh token",
		Tags:        []string{"auth"},
		Security:    []map[string][]string{{BearerSecurityName: {}}},
	}, func(ctx context.Context, in *logoutInput) (*logoutOutput, error) {
		userID := UserIDFromContext(ctx)
		if userID == "" {
			return nil, huma.Error401Unauthorized("unauthenticated")
		}

		token := strings.TrimSpace(in.Body.RefreshToken)
		if token == "" {
			return nil, huma.Error400BadRequest("refresh_token is required")
		}

		session, err := deps.Sessions.LookupByToken(ctx, auth.Token(token))
		if err != nil {
			// Be lenient: if the token is already invalid, treat logout
			// as a no-op success. Returning 401 here would let an
			// attacker probe whether a token is still active.
			if errors.Is(err, auth.ErrSessionInvalid) {
				out := &logoutOutput{}
				out.Body.Status = "revoked"
				return out, nil
			}
			deps.Logger.Error().Err(err).Msg("lookup session for logout")
			return nil, huma.Error500InternalServerError("could not logout")
		}

		// Bind: the access token's subject must match the session's owner.
		// Without this check, a user with a valid access token could
		// revoke arbitrary sessions of other users.
		if session.UserID != userID {
			out := &logoutOutput{}
			out.Body.Status = "revoked"
			return out, nil
		}

		if err := deps.Sessions.Revoke(ctx, session.ID); err != nil {
			deps.Logger.Error().Err(err).Str("session_id", session.ID).Msg("revoke session")
			return nil, huma.Error500InternalServerError("could not logout")
		}

		deps.Logger.Info().Str("user_id", userID).Str("session_id", session.ID).Msg("logout")

		out := &logoutOutput{}
		out.Body.Status = "revoked"
		return out, nil
	})
}

// parseClientIP best-effort extracts the client IP from proxy headers.
// Returns nil if nothing valid was found — caller stores NULL.
//
// X-Forwarded-For can contain a chain (client, proxy1, proxy2). The
// leftmost entry is the originating client, but it can be spoofed by the
// client itself. For an MVP behind a single trusted reverse proxy this is
// good enough; tighten when we ship behind a CDN.
func parseClientIP(xff, xRealIP string) *netip.Addr {
	for _, candidate := range []string{firstHop(xff), xRealIP} {
		if candidate == "" {
			continue
		}
		addr, err := netip.ParseAddr(strings.TrimSpace(candidate))
		if err == nil && addr.IsValid() {
			return &addr
		}
	}
	return nil
}

func firstHop(xff string) string {
	if xff == "" {
		return ""
	}
	if i := strings.Index(xff, ","); i >= 0 {
		return xff[:i]
	}
	return xff
}

