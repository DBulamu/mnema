package httpapi

import (
	"context"
	"strings"

	"github.com/DBulamu/mnema/backend/internal/jwtauth"
	"github.com/danielgtaylor/huma/v2"
)

// Context keys are package-private to prevent collisions with other packages
// and to force callers through the typed accessors below.
type ctxKey int

const (
	ctxKeyUserID ctxKey = iota
)

// UserIDFromContext returns the authenticated user ID set by the JWT
// middleware, or "" if the request was unauthenticated. Handlers that
// require auth should check the operation's security definition rather
// than this empty-string sentinel.
func UserIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyUserID).(string); ok {
		return v
	}
	return ""
}

// BearerSecurityName is the security scheme key used in OpenAPI ops to
// declare that an endpoint requires a JWT. Keep in sync with the API
// security registration.
const BearerSecurityName = "BearerAuth"

// JWTMiddleware enforces the BearerAuth scheme on operations that declare
// it. Operations without the scheme are passed through untouched, which
// keeps /healthz and /v1/auth/* open without per-route plumbing.
func JWTMiddleware(api huma.API, issuer *jwtauth.Issuer) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if !operationRequiresAuth(ctx.Operation()) {
			next(ctx)
			return
		}

		header := ctx.Header("Authorization")
		token, ok := bearerToken(header)
		if !ok {
			_ = huma.WriteErr(api, ctx, 401, "missing or malformed Authorization header")
			return
		}

		claims, err := issuer.Verify(token)
		if err != nil {
			_ = huma.WriteErr(api, ctx, 401, "invalid token")
			return
		}

		ctx = huma.WithValue(ctx, ctxKeyUserID, claims.Subject)
		next(ctx)
	}
}

// operationRequiresAuth returns true when the OpenAPI operation declares
// the BearerAuth security scheme. Multiple security entries are OR-ed in
// OpenAPI; we treat any reference to BearerAuth as "auth required".
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
	if header == "" {
		return "", false
	}
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}
