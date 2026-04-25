package rest

import (
	"context"
	"net/http"

	authuc "github.com/DBulamu/mnema/backend/internal/usecase/auth"
	"github.com/danielgtaylor/huma/v2"
)

type logoutRunner interface {
	Run(ctx context.Context, in authuc.LogoutInput) error
}

type logoutInput struct {
	Body struct {
		// We accept the refresh token in the body so a single logout call
		// can revoke the session without a "list my sessions" step.
		// Bearer access is also required (Security below) so a stolen
		// refresh alone cannot trigger logout.
		RefreshToken string `json:"refresh_token" minLength:"10"`
	}
}

type logoutOutput struct {
	Body struct {
		Status string `json:"status" enum:"revoked"`
	}
}

// RegisterLogout wires POST /v1/auth/logout. Requires Bearer access.
func RegisterLogout(api huma.API, run logoutRunner) {
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
			return nil, toHTTP(errUnauthenticated)
		}

		if err := run.Run(ctx, authuc.LogoutInput{
			UserID:       userID,
			RefreshToken: domainToken(in.Body.RefreshToken),
		}); err != nil {
			return nil, toHTTP(err)
		}

		out := &logoutOutput{}
		out.Body.Status = "revoked"
		return out, nil
	})
}

