// Package logger configures the application-wide zerolog logger.
//
// Local development gets a colored, human-readable console writer; test and
// prod get structured JSON suitable for log aggregators. Every line is tagged
// with the env name so logs from multiple deployments cannot be confused.
package logger

import (
	"os"
	"strings"
	"time"

	"github.com/DBulamu/mnema/backend/internal/config"
	"github.com/rs/zerolog"
)

// New returns a zerolog.Logger ready for use across the app.
// An invalid LOG_LEVEL silently falls back to Info — we prefer "noisy but
// running" over "configured to log nothing" during incident debugging.
func New(cfg config.Config) zerolog.Logger {
	level := zerolog.InfoLevel
	if l, err := zerolog.ParseLevel(strings.ToLower(cfg.LogLevel)); err == nil {
		level = l
	}

	zerolog.TimeFieldFormat = time.RFC3339Nano

	var l zerolog.Logger
	if cfg.Env == config.EnvLocal {
		// Pretty console output speeds up local dev; never use this in prod
		// because aggregators (Loki, Datadog, etc.) expect JSON.
		l = zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.Kitchen}).
			With().Timestamp().Logger()
	} else {
		l = zerolog.New(os.Stdout).With().Timestamp().Logger()
	}

	return l.Level(level).With().Str("env", string(cfg.Env)).Logger()
}
