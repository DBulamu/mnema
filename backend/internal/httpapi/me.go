package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/DBulamu/mnema/backend/internal/users"
	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog"
)

type MeDeps struct {
	Users  *users.Store
	Logger zerolog.Logger
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

// RegisterMe wires GET /v1/me. The endpoint declares BearerAuth in its
// Security block so JWTMiddleware enforces a valid token before the
// handler runs; the handler itself can trust UserIDFromContext.
func RegisterMe(api huma.API, deps MeDeps) {
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
			// Defensive: middleware should have rejected this, but if it
			// somehow let an unauth request reach here we fail closed.
			return nil, huma.Error401Unauthorized("unauthenticated")
		}

		u, err := deps.Users.GetByID(ctx, userID)
		if err != nil {
			deps.Logger.Error().Err(err).Str("user_id", userID).Msg("get user")
			return nil, huma.Error404NotFound("user not found")
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
