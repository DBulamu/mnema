package logger

import (
	"os"
	"strings"
	"time"

	"github.com/DBulamu/mnema/backend/internal/config"
	"github.com/rs/zerolog"
)

func New(cfg config.Config) zerolog.Logger {
	level := zerolog.InfoLevel
	if l, err := zerolog.ParseLevel(strings.ToLower(cfg.LogLevel)); err == nil {
		level = l
	}

	zerolog.TimeFieldFormat = time.RFC3339Nano

	var l zerolog.Logger
	if cfg.Env == config.EnvLocal {
		l = zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.Kitchen}).
			With().Timestamp().Logger()
	} else {
		l = zerolog.New(os.Stdout).With().Timestamp().Logger()
	}

	return l.Level(level).With().Str("env", string(cfg.Env)).Logger()
}
