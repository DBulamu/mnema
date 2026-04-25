package rest

import (
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// ctxKey is a private type for context keys to prevent collisions.
type ctxKey int

const ctxKeyUserID ctxKey = iota

// UserIDFromContext returns the authenticated user ID set by the JWT
// middleware. Empty string means "request was unauthenticated".
func UserIDFromContext(ctx interface {
	Value(any) any
}) string {
	if v, ok := ctx.Value(ctxKeyUserID).(string); ok {
		return v
	}
	return ""
}

// accessTokenVerifier is the consumer-side port for the JWT middleware.
// Whatever passes (issuer/verifier from the adapter package) only needs
// to satisfy this single-method interface.
type accessTokenVerifier interface {
	Verify(token string) (userID string, err error)
}

// JWTMiddleware enforces the BearerAuth scheme on operations that
// declare it. Operations without the scheme pass through, which keeps
// /healthz and /v1/auth/* open without per-route plumbing.
func JWTMiddleware(api huma.API, verifier accessTokenVerifier) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if !operationRequiresAuth(ctx.Operation()) {
			next(ctx)
			return
		}

		token, ok := bearerToken(ctx.Header("Authorization"))
		if !ok {
			_ = huma.WriteErr(api, ctx, 401, "missing or malformed Authorization header")
			return
		}

		userID, err := verifier.Verify(token)
		if err != nil {
			_ = huma.WriteErr(api, ctx, 401, "invalid token")
			return
		}

		next(huma.WithValue(ctx, ctxKeyUserID, userID))
	}
}

// operationRequiresAuth returns true when the OpenAPI operation declares
// the BearerAuth security scheme. Multiple security entries are OR-ed
// in OpenAPI; we treat any reference to BearerAuth as "auth required".
func operationRequiresAuth(op *huma.Operation) bool {
	if op == nil {
		return false
	}
	for _, sec := range op.Security {
		if _, ok := sec[BearerSecurityName]; ok {
			return true
		}
	}
	return false
}

// bearerToken extracts the token from a "Bearer <token>" header value.
// Case-insensitive on the scheme per RFC 6750.
func bearerToken(header string) (string, bool) {
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	if token := strings.TrimSpace(header[len(prefix):]); token != "" {
		return token, true
	}
	return "", false
}
