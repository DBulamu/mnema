package rest

import (
	"context"
	"log/slog"
	"time"

	"github.com/danielgtaylor/huma/v2"
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
func LoggingMiddleware(log *slog.Logger) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		start := time.Now()
		next(ctx)
		elapsed := time.Since(start)

		status := ctx.Status()

		op := ctx.Operation()
		var operationID string
		if op != nil {
			operationID = op.OperationID
		}

		attrs := []slog.Attr{
			slog.String("method", ctx.Method()),
			slog.String("path", ctx.URL().Path),
			slog.Int("status", status),
			slog.Duration("latency", elapsed),
			slog.String("operation", operationID),
			slog.String("request_id", RequestIDFromContext(ctx.Context())),
		}
		if uid := UserIDFromContext(ctx.Context()); uid != "" {
			attrs = append(attrs, slog.String("user_id", uid))
		}

		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelWarn
		}
		log.LogAttrs(context.Background(), level, "http", attrs...)
	}
}
