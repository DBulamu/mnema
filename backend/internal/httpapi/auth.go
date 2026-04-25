// Package httpapi wires huma operation handlers to internal services.
//
// Handlers live here, business logic lives in domain packages (auth/, etc.).
// The split keeps OpenAPI shape decisions (input/output structs, error
// codes, examples) out of the domain layer so the same services can be
// reused later (CLI, background workers, gRPC).
package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/DBulamu/mnema/backend/internal/auth"
	"github.com/DBulamu/mnema/backend/internal/email"
	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog"
)

type AuthDeps struct {
	MagicLinks *auth.MagicLinkStore
	Email      email.Sender
	Logger     zerolog.Logger
	AppBaseURL string
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

