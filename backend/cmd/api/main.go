// Command api is the Mnema HTTP API server.
//
// This is the composition root: it wires concrete adapters (postgres,
// jwt, smtp, system clock) into usecases, and usecases into transport
// handlers. Layers below depend only on the interfaces declared in the
// usecase packages — never on each other directly.
//
// Startup order matters: config first (so we can fail fast on missing
// env), then logger, then DB pool, then migrations (which need the
// pool), then HTTP. Anything that can fail during boot must fail before
// we accept connections.
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

	emailadapter "github.com/DBulamu/mnema/backend/internal/adapter/email"
	jwtadapter "github.com/DBulamu/mnema/backend/internal/adapter/jwt"
	pgmagiclinks "github.com/DBulamu/mnema/backend/internal/adapter/postgres/magiclinks"
	pgsessions "github.com/DBulamu/mnema/backend/internal/adapter/postgres/sessions"
	pgusers "github.com/DBulamu/mnema/backend/internal/adapter/postgres/users"
	"github.com/DBulamu/mnema/backend/internal/adapter/system"
	"github.com/DBulamu/mnema/backend/internal/config"
	"github.com/DBulamu/mnema/backend/internal/db"
	"github.com/DBulamu/mnema/backend/internal/logger"
	"github.com/DBulamu/mnema/backend/internal/migrations"
	"github.com/DBulamu/mnema/backend/internal/transport/rest"
	authuc "github.com/DBulamu/mnema/backend/internal/usecase/auth"
	profileuc "github.com/DBulamu/mnema/backend/internal/usecase/profile"
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

	// --- Adapters (the only place that knows about concrete tech). ---
	clock := system.Clock{}
	tokens := system.TokenGenerator{}
	jwtIssuer := jwtadapter.NewIssuer(cfg.JWT.Secret, cfg.JWT.AccessTTL)

	usersRepo := pgusers.New(pool)
	sessionsRepo := pgsessions.New(pool)
	magicLinksRepo := pgmagiclinks.New(pool)

	mailer := selectMailer(cfg)

	// --- Usecases (composed from adapters). --------------------------
	requestLink := &authuc.RequestMagicLink{
		Links:   magicLinksRepo,
		Tokens:  tokens,
		Mailer:  mailer,
		Clock:   clock,
		BaseURL: cfg.AppBaseURL,
	}
	consumeLink := &authuc.ConsumeMagicLink{
		Links:      magicLinksRepo,
		Users:      usersRepo,
		Sessions:   sessionsRepo,
		Tokens:     tokens,
		Issuer:     jwtIssuer,
		Clock:      clock,
		RefreshTTL: cfg.JWT.RefreshTTL,
	}
	refresh := &authuc.RefreshAccess{
		Sessions: sessionsRepo,
		Issuer:   jwtIssuer,
		Clock:    clock,
	}
	logout := &authuc.Logout{
		Sessions: sessionsRepo,
		Clock:    clock,
	}
	getMe := &profileuc.GetMe{Users: usersRepo}

	// --- Transport (handlers + middleware). --------------------------
	api, mux := rest.NewAPI(
		"Mnema API",
		"0.1.0",
		"Backend API for Mnema — a digital brain for thoughts, ideas, and memories.",
	)
	api.UseMiddleware(rest.JWTMiddleware(api, jwtIssuer))

	rest.RegisterHealth(api)
	rest.RegisterRequestMagicLink(api, requestLink)
	rest.RegisterConsumeMagicLink(api, consumeLink)
	rest.RegisterRefresh(api, refresh)
	rest.RegisterLogout(api, logout)
	rest.RegisterMe(api, getMe)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
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

// mailer abstracts which email adapter to wire — kept private so the
// rest of the binary doesn't see provider-specific types.
type mailer interface {
	Send(ctx context.Context, to, subject, text string) error
}

// selectMailer picks the email adapter based on environment. Test wires
// a captor (in-memory). Local and prod both use SMTP — the difference
// is just the host: mailpit locally, Resend in prod.
func selectMailer(cfg config.Config) mailer {
	if cfg.Env == config.EnvTest {
		return emailadapter.NewCaptor()
	}
	return emailadapter.NewSMTPSender(cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.From)
}
