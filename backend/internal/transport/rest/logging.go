package rest

import (
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog"
)

// LoggingMiddleware emits one structured line per request with method,
// path, status, latency, and the request/user IDs set by upstream
// middleware. Errors (5xx) are logged at Warn so they surface in default
// dashboards without pulling in stack traces — the handlers themselves
// are responsible for the actual error payload returned to the client.
//
// Order matters: this middleware must run AFTER RequestIDMiddleware (so
// the ID is in context) and before JWTMiddleware is fine — when JWT is
// successful it stores the user ID, which we pick up after next()
// returns. When JWT rejects the request, user_id is simply absent.
func LoggingMiddleware(log zerolog.Logger) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		start := time.Now()
		next(ctx)
		elapsed := time.Since(start)

		status := ctx.Status()
		evt := log.Info()
		if status >= 500 {
			evt = log.Warn()
		}

		op := ctx.Operation()
		var operationID string
		if op != nil {
			operationID = op.OperationID
		}

		evt = evt.
			Str("method", ctx.Method()).
			Str("path", ctx.URL().Path).
			Int("status", status).
			Dur("latency", elapsed).
			Str("operation", operationID).
			Str("request_id", RequestIDFromContext(ctx.Context()))

		if uid := UserIDFromContext(ctx.Context()); uid != "" {
			evt = evt.Str("user_id", uid)
		}

		evt.Msg("http")
	}
}
