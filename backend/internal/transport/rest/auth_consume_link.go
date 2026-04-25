package rest

import (
	"context"
	"net/http"
	"time"

	authuc "github.com/DBulamu/mnema/backend/internal/usecase/auth"
	"github.com/danielgtaylor/huma/v2"
)

type consumeLinkRunner interface {
	Run(ctx context.Context, in authuc.ConsumeMagicLinkInput) (authuc.ConsumeMagicLinkOutput, error)
}

type consumeLinkInput struct {
	Body struct {
		Token string `json:"token" minLength:"10" doc:"Token received in the magic-link email"`
	}
	UserAgent     string `header:"User-Agent"`
	XForwardedFor string `header:"X-Forwarded-For"`
	XRealIP       string `header:"X-Real-IP"`
}

type consumeLinkOutput struct {
	Body struct {
		AccessToken      string `json:"access_token" doc:"Short-lived JWT for API calls"`
		AccessExpiresAt  string `json:"access_expires_at" format:"date-time"`
		RefreshToken     string `json:"refresh_token" doc:"Opaque refresh token"`
		RefreshExpiresAt string `json:"refresh_expires_at" format:"date-time"`
		User             struct {
			ID    string `json:"id" format:"uuid"`
			Email string `json:"email" format:"email"`
		} `json:"user"`
	}
}

// RegisterConsumeMagicLink wires POST /v1/auth/magic-link/consume.
func RegisterConsumeMagicLink(api huma.API, run consumeLinkRunner) {
	huma.Register(api, huma.Operation{
		OperationID: "auth-magic-link-consume",
		Method:      http.MethodPost,
		Path:        "/v1/auth/magic-link/consume",
		Summary:     "Exchange a magic-link token for access + refresh tokens",
		Tags:        []string{"auth"},
	}, func(ctx context.Context, in *consumeLinkInput) (*consumeLinkOutput, error) {
		// The token type is just a typed string in the domain layer;
		// converting at the boundary keeps transport ignorant of
		// hashing / generation details.
		result, err := run.Run(ctx, authuc.ConsumeMagicLinkInput{
			Token:     domainToken(in.Body.Token),
			UserAgent: in.UserAgent,
			IPAddress: parseClientIP(in.XForwardedFor, in.XRealIP),
		})
		if err != nil {
			return nil, toHTTP(err)
		}

		out := &consumeLinkOutput{}
		out.Body.AccessToken = result.AccessToken
		out.Body.AccessExpiresAt = result.AccessExpiresAt.UTC().Format(time.RFC3339)
		out.Body.RefreshToken = string(result.RefreshToken)
		out.Body.RefreshExpiresAt = result.RefreshExpiresAt.UTC().Format(time.RFC3339)
		out.Body.User.ID = result.User.ID
		out.Body.User.Email = result.User.Email
		return out, nil
	})
}
