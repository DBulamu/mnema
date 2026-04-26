package rest

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLogUnmatchedRoutes_LogsOn404(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	// Inner handler always replies 404, simulating a ServeMux miss.
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	})
	wrapped := LogUnmatchedRoutes(inner, log)

	req := httptest.NewRequest(http.MethodGet, "/v1/no-such-thing", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
	out := buf.String()
	if !strings.Contains(out, `"path":"/v1/no-such-thing"`) {
		t.Fatalf("expected path in log, got: %s", out)
	}
	if !strings.Contains(out, `"method":"GET"`) {
		t.Fatalf("expected method in log, got: %s", out)
	}
	if !strings.Contains(out, "route not found") {
		t.Fatalf("expected message in log, got: %s", out)
	}
}

func TestLogUnmatchedRoutes_QuietOn200(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	wrapped := LogUnmatchedRoutes(inner, log)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no log on 200, got: %s", buf.String())
	}
}
