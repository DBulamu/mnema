package rest

import (
	"log/slog"
	"net/http"
)

// statusRecorder is a minimal http.ResponseWriter wrapper that captures
// the status code so the outer handler can decide whether the request
// was a route mismatch (404 from ServeMux) vs. a normal response.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.wrote = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	return r.ResponseWriter.Write(b)
}

// LogUnmatchedRoutes wraps an http.Handler and logs requests that the
// inner handler answered with 404 — typically a typo in the URL or a
// client targeting a stale endpoint. We log at Warn (not Info) because
// these don't appear in the per-operation access log: huma's middleware
// only fires for matched operations, so without this wrapper a 404 from
// ServeMux is silently invisible.
//
// The inner handler is unchanged; we only observe its response status.
func LogUnmatchedRoutes(inner http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}
		inner.ServeHTTP(rec, r)
		if rec.status == http.StatusNotFound {
			log.Warn("route not found",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("remote_addr", r.RemoteAddr),
			)
		}
	})
}
