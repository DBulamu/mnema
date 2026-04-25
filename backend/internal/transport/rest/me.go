package rest

import (
	"context"
	"net/http"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/danielgtaylor/huma/v2"
)

type meRunner interface {
	Run(ctx context.Context, userID string) (domain.User, error)
}

type meOutput struct {
	Body struct {
		ID               string  `json:"id" format:"uuid"`
		Email            string  `json:"email" format:"email"`
		DisplayName      *string `json:"display_name,omitempty"`
		AvatarURL        *string `json:"avatar_url,omitempty"`
		Timezone         string  `json:"timezone"`
		DailyPushTime    *string `json:"daily_push_time,omitempty"`
		DailyPushEnabled bool    `json:"daily_push_enabled"`
		CreatedAt        string  `json:"created_at" format:"date-time"`
	}
}

// RegisterMe wires GET /v1/me. Requires Bearer access.
func RegisterMe(api huma.API, run meRunner) {
	huma.Register(api, huma.Operation{
		OperationID: "me-get",
		Method:      http.MethodGet,
		Path:        "/v1/me",
		Summary:     "Get the authenticated user's profile",
		Tags:        []string{"profile"},
		Security:    []map[string][]string{{BearerSecurityName: {}}},
	}, func(ctx context.Context, _ *struct{}) (*meOutput, error) {
		userID := UserIDFromContext(ctx)
		if userID == "" {
			return nil, toHTTP(errUnauthenticated)
		}

		u, err := run.Run(ctx, userID)
		if err != nil {
			return nil, toHTTP(err)
		}

		out := &meOutput{}
		out.Body.ID = u.ID
		out.Body.Email = u.Email
		out.Body.DisplayName = u.DisplayName
		out.Body.AvatarURL = u.AvatarURL
		out.Body.Timezone = u.Timezone
		out.Body.DailyPushTime = u.DailyPushTime
		out.Body.DailyPushEnabled = u.DailyPushEnabled
		out.Body.CreatedAt = u.CreatedAt.UTC().Format(time.RFC3339)
		return out, nil
	})
}
