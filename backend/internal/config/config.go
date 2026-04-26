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

// LLMProvider names a backing LLM implementation. Closed list — adding
// a provider is a code change (new const + selectChatLLM case + adapter).
type LLMProvider string

const (
	// LLMProviderUnset is the zero value; callers treat it the same as
	// LLMProviderStub so an empty config still boots in local/test.
	LLMProviderUnset LLMProvider = ""
	// LLMProviderStub is the deterministic, dependency-free reply
	// generator. Default in local and test.
	LLMProviderStub LLMProvider = "stub"
	// LLMProviderOpenAI is the public OpenAI chat-completions API
	// (also used for OpenAI-compatible endpoints via WithOpenAIBaseURL).
	LLMProviderOpenAI LLMProvider = "openai"
	// LLMProviderOllama is a locally-hosted ollama daemon. Today only
	// the recall pipeline opts in — chat / extraction / embeddings
	// keep going through stub or openai. Recall's first MVP runs
	// fully local (qwen2.5:7b) for privacy and zero per-request cost.
	LLMProviderOllama LLMProvider = "ollama"
)

func (p LLMProvider) Valid() bool {
	switch p {
	case LLMProviderUnset, LLMProviderStub, LLMProviderOpenAI, LLMProviderOllama:
		return true
	}
	return false
}

type LLMConfig struct {
	Provider        LLMProvider `yaml:"provider"`
	OpenAIAPIKey    string      `yaml:"openai_api_key"`
	ExtractionModel string      `yaml:"extraction_model"`
	EmbeddingModel  string      `yaml:"embedding_model"`

	// Recall uses its own provider switch — running the chat path on
	// OpenAI while recall stays on a local ollama is a supported and
	// expected combo on the MVP.
	Recall RecallLLMConfig `yaml:"recall"`
}

// RecallLLMConfig configures the recall pipeline's LLM steps separately
// from the chat / extraction / embedding paths.
type RecallLLMConfig struct {
	Provider   LLMProvider `yaml:"provider"`
	OllamaURL  string      `yaml:"ollama_url"`
	OllamaModel string     `yaml:"ollama_model"`
}

// envVarName is the OS environment variable that selects which YAML
// file to load. Kept here so callers don't sprinkle "APP_ENV" string
// literals through the codebase.
const envVarName = "APP_ENV"

// jwtSecretPlaceholder is the literal we ship in local.yaml / test.yaml
// and refuse to accept in prod. Centralising the magic value is the
// only way the validate() check stays in sync with the YAML files.
const jwtSecretPlaceholder = "change-me-locally"

// Load reads config/<APP_ENV>.yaml from the embedded FS, expands
// ${ENV_VAR} placeholders against the process environment, and
// validates the result.
func Load() (Config, error) {
	envName := strings.TrimSpace(os.Getenv(envVarName))
	if envName == "" {
		envName = string(EnvLocal)
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
	if !c.LLM.Provider.Valid() {
		return fmt.Errorf("invalid llm.provider %q (want: stub|openai|ollama)", c.LLM.Provider)
	}
	if !c.LLM.Recall.Provider.Valid() {
		return fmt.Errorf("invalid llm.recall.provider %q (want: stub|openai|ollama)", c.LLM.Recall.Provider)
	}
	if c.Env == EnvProd {
		if c.JWT.Secret == jwtSecretPlaceholder {
			return fmt.Errorf("jwt.secret must be set to a real value in prod")
		}
		if c.LLM.Provider == LLMProviderOpenAI {
			if c.LLM.OpenAIAPIKey == "" {
				return fmt.Errorf("llm.openai_api_key is required when llm.provider=openai")
			}
			if c.LLM.ExtractionModel == "" {
				return fmt.Errorf("llm.extraction_model is required when llm.provider=openai")
			}
			if c.LLM.EmbeddingModel == "" {
				return fmt.Errorf("llm.embedding_model is required when llm.provider=openai")
			}
		}
	}
	return nil
}
