package rest

import (
	"context"
	"net/http"
	"time"

	authuc "github.com/DBulamu/mnema/backend/internal/usecase/auth"
	"github.com/danielgtaylor/huma/v2"
)

type refreshRunner interface {
	Run(ctx context.Context, in authuc.RefreshAccessInput) (authuc.RefreshAccessOutput, error)
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

// RegisterRefresh wires POST /v1/auth/refresh.
func RegisterRefresh(api huma.API, run refreshRunner) {
	huma.Register(api, huma.Operation{
		OperationID: "auth-refresh",
		Method:      http.MethodPost,
		Path:        "/v1/auth/refresh",
		Summary:     "Exchange a refresh token for a new access token",
		Tags:        []string{"auth"},
	}, func(ctx context.Context, in *refreshInput) (*refreshOutput, error) {
		result, err := run.Run(ctx, authuc.RefreshAccessInput{
			RefreshToken: domainToken(in.Body.RefreshToken),
		})
		if err != nil {
			return nil, toHTTP(err)
		}
		out := &refreshOutput{}
		out.Body.AccessToken = result.AccessToken
		out.Body.AccessExpiresAt = result.AccessExpiresAt.UTC().Format(time.RFC3339)
		return out, nil
	})
}
