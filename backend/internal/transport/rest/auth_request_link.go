package rest

import (
	"context"
	"net/http"
	"net/netip"
	"strings"

	authuc "github.com/DBulamu/mnema/backend/internal/usecase/auth"
	"github.com/danielgtaylor/huma/v2"
)

// requestLinkRunner is what this handler needs from the usecase layer.
// The auth.RequestMagicLink struct satisfies it by having a matching
// Run method — neither side imports a shared interface.
type requestLinkRunner interface {
	Run(ctx context.Context, in authuc.RequestMagicLinkInput) error
}

type requestLinkInput struct {
	Body struct {
		Email string `json:"email" format:"email" example:"user@example.com" doc:"User email address"`
	}
	XForwardedFor string `header:"X-Forwarded-For"`
	XRealIP       string `header:"X-Real-IP"`
}

type requestLinkOutput struct {
	Body struct {
		Status string `json:"status" example:"sent" enum:"sent"`
	}
}

// RegisterRequestMagicLink wires POST /v1/auth/magic-link/request.
//
// We always return success — even on usecase errors that are non-fatal
// from the user's perspective (e.g. unknown email) — so the response
// cannot be used to enumerate registered users. Real failures still
// return 5xx because they are infrastructure problems, not user data.
func RegisterRequestMagicLink(api huma.API, run requestLinkRunner) {
	huma.Register(api, huma.Operation{
		OperationID: "auth-magic-link-request",
		Method:      http.MethodPost,
		Path:        "/v1/auth/magic-link/request",
		Summary:     "Request a magic-link email",
		Tags:        []string{"auth"},
	}, func(ctx context.Context, in *requestLinkInput) (*requestLinkOutput, error) {
		err := run.Run(ctx, authuc.RequestMagicLinkInput{
			Email:     in.Body.Email,
			IPAddress: parseClientIP(in.XForwardedFor, in.XRealIP),
		})
		if err != nil {
			return nil, toHTTP(err)
		}
		out := &requestLinkOutput{}
		out.Body.Status = "sent"
		return out, nil
	})
}

// parseClientIP best-effort extracts the client IP from proxy headers.
// X-Forwarded-For may contain a chain (client, proxy1, proxy2); the
// leftmost entry is the client but can be spoofed. For an MVP behind
// a single trusted proxy this is good enough; tighten when behind a CDN.
func parseClientIP(xff, xRealIP string) *netip.Addr {
	for _, candidate := range []string{firstHop(xff), xRealIP} {
		if candidate == "" {
			continue
		}
		if addr, err := netip.ParseAddr(strings.TrimSpace(candidate)); err == nil && addr.IsValid() {
			return &addr
		}
	}
	return nil
}

func firstHop(xff string) string {
	if i := strings.Index(xff, ","); i >= 0 {
		return xff[:i]
	}
	return xff
}
