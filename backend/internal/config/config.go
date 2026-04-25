// Package config loads runtime configuration from per-environment YAML
// files embedded into the binary.
//
// Mnema runs in three environments — local, test, prod — selected via
// APP_ENV. The matching config/<env>.yaml file is parsed; any value of
// the form ${ENV_VAR} is then expanded from the process environment so
// secrets stay out of the repo.
//
// Provider implementations (email sender, LLM client, S3 storage) are
// wired based on Env so that the same binary behaves correctly in each
// environment without per-env build tags. This file is the single
// source of truth for what each environment requires; keep validate()
// in sync when adding env-sensitive deps.
package config

import (
	"embed"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// configFS holds the YAML files for every supported environment.
// Embedding keeps deployments to a single binary — no config files to
// ship alongside the executable.
//
//go:embed all:files
var configFS embed.FS

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
	Env      Env    `yaml:"env"`
	HTTPPort int    `yaml:"-"`
	LogLevel string `yaml:"-"`

	HTTP HTTPConfig `yaml:"http"`
	Log  LogConfig  `yaml:"log"`

	AppBaseURL  string `yaml:"-"`
	DatabaseURL string `yaml:"-"`

	App      AppConfig      `yaml:"app"`
	Database DatabaseConfig `yaml:"database"`

	JWT  JWTConfig  `yaml:"jwt"`
	SMTP SMTPConfig `yaml:"smtp"`
	S3   S3Config   `yaml:"s3"`
	LLM  LLMConfig  `yaml:"llm"`
}

type HTTPConfig struct {
	Port int `yaml:"port"`
}

type LogConfig struct {
	Level string `yaml:"level"`
}

type AppConfig struct {
	BaseURL string `yaml:"base_url"`
}

type DatabaseConfig struct {
	URL string `yaml:"url"`
}

type JWTConfig struct {
	Secret     string        `yaml:"secret"`
	AccessTTL  time.Duration `yaml:"access_ttl"`
	RefreshTTL time.Duration `yaml:"refresh_ttl"`
}

type SMTPConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	From string `yaml:"from"`
}

type S3Config struct {
	Endpoint     string `yaml:"endpoint"`
	AccessKey    string `yaml:"access_key"`
	SecretKey    string `yaml:"secret_key"`
	Bucket       string `yaml:"bucket"`
	UsePathStyle bool   `yaml:"use_path_style"`
}

type LLMConfig struct {
	Provider        string `yaml:"provider"`
	OpenAIAPIKey    string `yaml:"openai_api_key"`
	ExtractionModel string `yaml:"extraction_model"`
	EmbeddingModel  string `yaml:"embedding_model"`
}

// Load reads config/<APP_ENV>.yaml from the embedded FS, expands
// ${ENV_VAR} placeholders against the process environment, and
// validates the result.
func Load() (Config, error) {
	envName := strings.TrimSpace(os.Getenv("APP_ENV"))
	if envName == "" {
		envName = "local"
	}
	env := Env(envName)
	if !env.Valid() {
		return Config{}, fmt.Errorf("invalid APP_ENV %q (want: local|test|prod)", envName)
	}

	raw, err := configFS.ReadFile("files/" + envName + ".yaml")
	if err != nil {
		return Config{}, fmt.Errorf("read embedded config %s.yaml: %w", envName, err)
	}

	// os.ExpandEnv replaces $VAR and ${VAR}; missing vars become "".
	// We do this on the raw bytes before parsing so duration / int
	// fields can themselves come from env (e.g. HTTP_PORT=9090).
	expanded := os.ExpandEnv(string(raw))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s.yaml: %w", envName, err)
	}

	// Mirror nested fields onto the flat fields the rest of the code
	// already uses. Lets us migrate to YAML without rewriting every
	// callsite in one go.
	cfg.Env = env
	cfg.HTTPPort = cfg.HTTP.Port
	cfg.LogLevel = cfg.Log.Level
	cfg.AppBaseURL = cfg.App.BaseURL
	cfg.DatabaseURL = cfg.Database.URL

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validate enforces invariants that depend on the environment.
// Local and test get sensible defaults; prod must fail fast on missing
// secrets so we never accidentally ship the placeholder JWT secret.
func (c Config) validate() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("database.url is required (APP_ENV=%s)", c.Env)
	}
	if c.HTTPPort <= 0 {
		return fmt.Errorf("http.port must be > 0 (APP_ENV=%s)", c.Env)
	}
	if c.JWT.Secret == "" {
		return fmt.Errorf("jwt.secret is required (APP_ENV=%s)", c.Env)
	}
	if c.Env == EnvProd {
		if c.JWT.Secret == "change-me-locally" {
			return fmt.Errorf("jwt.secret must be set to a real value in prod")
		}
		if c.LLM.Provider == "openai" && c.LLM.OpenAIAPIKey == "" {
			return fmt.Errorf("llm.openai_api_key is required when llm.provider=openai")
		}
	}
	return nil
}
