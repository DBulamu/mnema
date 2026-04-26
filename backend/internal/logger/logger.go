// Package logger configures the application-wide structured logger.
//
// Local development gets a human-readable text writer; test and prod
// get JSON suitable for log aggregators. Every line is tagged with the
// env name so logs from multiple deployments cannot be confused.
//
// Built on stdlib log/slog (Go 1.21+) — no third-party dependency.
package logger

import (
	"log/slog"
	"os"
	"strings"

	"github.com/DBulamu/mnema/backend/internal/config"
)

// New returns a *slog.Logger ready for use across the app.
// An invalid LOG_LEVEL silently falls back to Info — we prefer "noisy
// but running" over "configured to log nothing" during incident
// debugging.
func New(cfg config.Config) *slog.Logger {
	level := parseLevel(cfg.LogLevel)
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if cfg.Env == config.EnvLocal {
		// Text output is friendlier in a terminal; never use this in
		// prod because aggregators (Loki, Datadog, etc.) expect JSON.
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler).With(slog.String("env", string(cfg.Env)))
}

// parseLevel maps the textual LOG_LEVEL value to a slog.Level. Anything
// we don't recognise (typo, empty) becomes Info — see the "noisy but
// running" preference above.
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
