package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Env identifies which environment the binary is running in.
// Provider implementations (email, LLM, storage) are selected per Env.
type Env string

const (
	EnvLocal Env = "local"
	EnvTest  Env = "test"
	EnvProd  Env = "prod"
)

func (e Env) Valid() bool {
	switch e {
	case EnvLocal, EnvTest, EnvProd:
		return true
	}
	return false
}

type Config struct {
	Env      Env
	HTTPPort int
	LogLevel string

	DatabaseURL string

	JWT  JWTConfig
	SMTP SMTPConfig
	S3   S3Config
	LLM  LLMConfig
}

type JWTConfig struct {
	Secret     string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

type SMTPConfig struct {
	Host string
	Port int
	From string
}

type S3Config struct {
	Endpoint      string
	AccessKey     string
	SecretKey     string
	Bucket        string
	UsePathStyle  bool
}

type LLMConfig struct {
	Provider        string
	OpenAIAPIKey    string
	ExtractionModel string
	EmbeddingModel  string
}

func Load() (Config, error) {
	env := Env(getenv("APP_ENV", "local"))
	if !env.Valid() {
		return Config{}, fmt.Errorf("invalid APP_ENV %q (want: local|test|prod)", env)
	}

	cfg := Config{
		Env:         env,
		HTTPPort:    getenvInt("HTTP_PORT", 8080),
		LogLevel:    getenv("LOG_LEVEL", "info"),
		DatabaseURL: getenv("DATABASE_URL", ""),
		JWT: JWTConfig{
			Secret:     getenv("JWT_SECRET", ""),
			AccessTTL:  getenvDuration("JWT_ACCESS_TTL", 15*time.Minute),
			RefreshTTL: getenvDuration("JWT_REFRESH_TTL", 30*24*time.Hour),
		},
		SMTP: SMTPConfig{
			Host: getenv("SMTP_HOST", "localhost"),
			Port: getenvInt("SMTP_PORT", 1025),
			From: getenv("SMTP_FROM", "noreply@mnema.local"),
		},
		S3: S3Config{
			Endpoint:     getenv("S3_ENDPOINT", ""),
			AccessKey:    getenv("S3_ACCESS_KEY", ""),
			SecretKey:    getenv("S3_SECRET_KEY", ""),
			Bucket:       getenv("S3_BUCKET", ""),
			UsePathStyle: getenvBool("S3_USE_PATH_STYLE", false),
		},
		LLM: LLMConfig{
			Provider:        getenv("LLM_PROVIDER", "stub"),
			OpenAIAPIKey:    getenv("OPENAI_API_KEY", ""),
			ExtractionModel: getenv("LLM_EXTRACTION_MODEL", "gpt-4o-mini"),
			EmbeddingModel:  getenv("LLM_EMBEDDING_MODEL", "text-embedding-3-small"),
		},
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if c.Env == EnvProd {
		if c.JWT.Secret == "" || c.JWT.Secret == "change-me-locally" {
			return fmt.Errorf("JWT_SECRET must be set to a real value in prod")
		}
	}
	return nil
}

func getenv(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

func getenvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getenvBool(key string, def bool) bool {
	v := strings.ToLower(os.Getenv(key))
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes"
}

func getenvDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
