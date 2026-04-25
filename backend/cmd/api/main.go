// Command api is the Mnema HTTP API server.
//
// Startup order matters: config first (so we can fail fast on missing env),
// then logger (so subsequent errors are observable), then DB pool, then
// migrations (which need the pool), then HTTP. Anything that can fail
// during boot must fail before we start accepting connections.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DBulamu/mnema/backend/internal/auth"
	"github.com/DBulamu/mnema/backend/internal/config"
	"github.com/DBulamu/mnema/backend/internal/db"
	"github.com/DBulamu/mnema/backend/internal/email"
	"github.com/DBulamu/mnema/backend/internal/httpapi"
	"github.com/DBulamu/mnema/backend/internal/jwtauth"
	"github.com/DBulamu/mnema/backend/internal/logger"
	"github.com/DBulamu/mnema/backend/internal/migrations"
	"github.com/DBulamu/mnema/backend/internal/users"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	log := logger.New(cfg)
	log.Info().Int("port", cfg.HTTPPort).Msg("mnema api starting")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	defer pool.Close()
	log.Info().Msg("postgres connected")

	if err := migrations.Run(ctx, pool); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	log.Info().Msg("migrations applied")

	magicLinks := auth.NewMagicLinkStore(pool)
	sessions := auth.NewSessionStore(pool)
	usersStore := users.NewStore(pool)
	jwtIssuer := jwtauth.NewIssuer(cfg.JWT.Secret, cfg.JWT.AccessTTL)
	emailSender := email.New(cfg)

	mux := http.NewServeMux()
	api := humago.New(mux, humaConfig())
	api.UseMiddleware(httpapi.JWTMiddleware(api, jwtIssuer))

	registerHealth(api)
	httpapi.RegisterAuth(api, httpapi.AuthDeps{
		MagicLinks: magicLinks,
		Sessions:   sessions,
		Users:      usersStore,
		JWT:        jwtIssuer,
		Email:      emailSender,
		Logger:     log,
		AppBaseURL: cfg.AppBaseURL,
		RefreshTTL: cfg.JWT.RefreshTTL,
	})
	httpapi.RegisterMe(api, httpapi.MeDeps{
		Users:  usersStore,
		Logger: log,
	})

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info().Str("addr", srv.Addr).Msg("listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info().Msg("shutdown signal received")
	case err := <-errCh:
		return fmt.Errorf("http: %w", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("graceful shutdown failed")
	}
	log.Info().Msg("bye")
	return nil
}

func humaConfig() huma.Config {
	c := huma.DefaultConfig("Mnema API", "0.1.0")
	c.Info.Description = "Backend API for Mnema — a digital brain for thoughts, ideas, and memories."
	// Declare the bearer scheme so the generated OpenAPI lists it and
	// Swagger UI shows the "Authorize" button. The middleware enforces it.
	if c.Components == nil {
		c.Components = &huma.Components{}
	}
	if c.Components.SecuritySchemes == nil {
		c.Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	c.Components.SecuritySchemes[httpapi.BearerSecurityName] = &huma.SecurityScheme{
		Type:         "http",
		Scheme:       "bearer",
		BearerFormat: "JWT",
	}
	return c
}

type healthOutput struct {
	Body struct {
		Status string `json:"status" example:"ok"`
		Time   string `json:"time"   example:"2026-04-25T20:00:00Z"`
	}
}

func registerHealth(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "healthz",
		Method:      http.MethodGet,
		Path:        "/healthz",
		Summary:     "Liveness probe",
		Tags:        []string{"system"},
	}, func(ctx context.Context, _ *struct{}) (*healthOutput, error) {
		out := &healthOutput{}
		out.Body.Status = "ok"
		out.Body.Time = time.Now().UTC().Format(time.RFC3339)
		return out, nil
	})
}
