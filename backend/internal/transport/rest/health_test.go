package rest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// stubPinger is the minimal readiness fake. nil err = healthy DB,
// non-nil = DB down — covers both branches of /readyz without needing
// a real pgx pool.
type stubPinger struct {
	err error
}

func (s stubPinger) Ping(_ context.Context) error { return s.err }

func newTestAPI(t *testing.T) (huma.API, *http.ServeMux) {
	t.Helper()
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.0"))
	return api, mux
}

func TestReadyz_OkWhenDBReachable(t *testing.T) {
	api, mux := newTestAPI(t)
	RegisterReady(api, stubPinger{err: nil})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestReadyz_503WhenDBDown(t *testing.T) {
	api, mux := newTestAPI(t)
	RegisterReady(api, stubPinger{err: errors.New("connection refused")})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHealthz_AlwaysOk(t *testing.T) {
	api, mux := newTestAPI(t)
	RegisterHealth(api)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}
