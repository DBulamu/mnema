package rest

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/danielgtaylor/huma/v2"
)

// requestIDHeader is the conventional header used to propagate a
// correlation ID through proxies and clients. We honor the incoming
// value when present so traces stitched together by an upstream LB or
// gateway are preserved end-to-end.
const requestIDHeader = "X-Request-ID"

// RequestIDFromContext returns the request ID attached by
// RequestIDMiddleware. Empty string means the middleware did not run or
// the value was overwritten.
func RequestIDFromContext(ctx interface {
	Value(any) any
}) string {
	if v, ok := ctx.Value(ctxKeyRequestID).(string); ok {
		return v
	}
	return ""
}

// RequestIDMiddleware ensures every request carries a stable ID for the
// duration of its lifecycle. If the client supplied X-Request-ID we
// trust it; otherwise we generate a fresh 16-hex-char token. The ID is
// echoed back in the response header so clients can reference it in bug
// reports.
//
// Why crypto/rand: we don't need cryptographic strength here, but the
// stdlib RNG is goroutine-safe and unbiased — and the server already
// depends on it elsewhere, so no new package surface.
func RequestIDMiddleware() func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		id := ctx.Header(requestIDHeader)
		if id == "" {
			id = newRequestID()
		}
		ctx.SetHeader(requestIDHeader, id)
		next(huma.WithValue(ctx, ctxKeyRequestID, id))
	}
}

// newRequestID returns 16 hex chars from 8 random bytes. Short enough
// to scan in logs, long enough to make collisions effectively zero
// across realistic traffic volumes.
func newRequestID() string {
	var b [8]byte
	// crypto/rand.Read on modern Go cannot fail; ignoring the error is
	// the documented idiom (see the stdlib comment on rand.Read).
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
