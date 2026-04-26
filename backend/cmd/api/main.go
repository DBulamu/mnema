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
	llmadapter "github.com/DBulamu/mnema/backend/internal/adapter/llm"
	pgconversations "github.com/DBulamu/mnema/backend/internal/adapter/postgres/conversations"
	pgedges "github.com/DBulamu/mnema/backend/internal/adapter/postgres/edges"
	pgmagiclinks "github.com/DBulamu/mnema/backend/internal/adapter/postgres/magiclinks"
	pgmessages "github.com/DBulamu/mnema/backend/internal/adapter/postgres/messages"
	pgnodes "github.com/DBulamu/mnema/backend/internal/adapter/postgres/nodes"
	pgsessions "github.com/DBulamu/mnema/backend/internal/adapter/postgres/sessions"
	pgusers "github.com/DBulamu/mnema/backend/internal/adapter/postgres/users"
	"github.com/DBulamu/mnema/backend/internal/adapter/system"
	"github.com/DBulamu/mnema/backend/internal/config"
	"github.com/DBulamu/mnema/backend/internal/db"
	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/DBulamu/mnema/backend/internal/logger"
	"github.com/DBulamu/mnema/backend/internal/migrations"
	"github.com/DBulamu/mnema/backend/internal/transport/rest"
	authuc "github.com/DBulamu/mnema/backend/internal/usecase/auth"
	chatuc "github.com/DBulamu/mnema/backend/internal/usecase/chat"
	extractionuc "github.com/DBulamu/mnema/backend/internal/usecase/extraction"
	graphuc "github.com/DBulamu/mnema/backend/internal/usecase/graph"
	profileuc "github.com/DBulamu/mnema/backend/internal/usecase/profile"
	recalluc "github.com/DBulamu/mnema/backend/internal/usecase/recall"
	"github.com/rs/zerolog"
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
	conversationsRepo := pgconversations.New(pool)
	messagesRepo := pgmessages.New(pool)
	nodesRepo := pgnodes.New(pool)
	edgesRepo := pgedges.New(pool)

	mailer := selectMailer(cfg)
	chatLLM, err := selectChatLLM(cfg)
	if err != nil {
		return fmt.Errorf("select chat llm: %w", err)
	}
	extractorLLM, err := selectExtractor(cfg)
	if err != nil {
		return fmt.Errorf("select extractor: %w", err)
	}
	embedder, err := selectEmbedder(cfg)
	if err != nil {
		return fmt.Errorf("select embedder: %w", err)
	}

	extract := &extractionuc.Extract{
		Extractor:  extractorLLM,
		Nodes:      nodesBridge{repo: nodesRepo},
		Edges:      edgesBridge{repo: edgesRepo},
		Embedder:   embedder,
		Embeddings: nodesRepo,
	}
	extractorBridge := messageExtractorBridge{
		extract: extract,
		log:     log.With().Str("component", "extractor").Logger(),
	}

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

	startConversation := &chatuc.StartConversation{Conversations: conversationsRepo}
	listConversations := &chatuc.ListConversations{Conversations: conversationsRepo}
	getConversation := &chatuc.GetConversation{
		Conversations: conversationsRepo,
		Messages:      messagesRepo,
	}
	sendMessage := &chatuc.SendMessage{
		Conversations: conversationsRepo,
		Messages:      messagesRepo,
		History:       messagesRepo,
		Toucher:       conversationsRepo,
		LLM:           chatLLM,
		Extractor:     extractorBridge,
		Clock:         clock,
	}

	getGraph := &graphuc.GetGraph{
		Nodes: graphNodesBridge{repo: nodesRepo},
		Edges: edgesRepo,
	}
	searchGraph := &graphuc.Search{
		Nodes:    graphSearchNodesBridge{repo: nodesRepo},
		Embedder: embedder,
	}

	recall, err := selectRecall(cfg)
	if err != nil {
		return fmt.Errorf("select recall: %w", err)
	}

	// --- Transport (handlers + middleware). --------------------------
	api, mux := rest.NewAPI(
		"Mnema API",
		"0.1.0",
		"Backend API for Mnema — a digital brain for thoughts, ideas, and memories.",
	)
	// Middleware order: request_id first (so every downstream layer can
	// attach it to logs/errors), then logging (wraps the rest of the
	// chain to capture status + latency), then JWT (sets user_id which
	// the logging middleware reads after next()).
	api.UseMiddleware(rest.RequestIDMiddleware())
	api.UseMiddleware(rest.LoggingMiddleware(log))
	api.UseMiddleware(rest.JWTMiddleware(api, jwtIssuer))

	rest.RegisterHealth(api)
	rest.RegisterRequestMagicLink(api, requestLink)
	rest.RegisterConsumeMagicLink(api, consumeLink)
	rest.RegisterRefresh(api, refresh)
	rest.RegisterLogout(api, logout)
	rest.RegisterMe(api, getMe)
	rest.RegisterStartConversation(api, startConversation)
	rest.RegisterListConversations(api, listConversations)
	rest.RegisterGetConversation(api, getConversation)
	rest.RegisterSendMessage(api, sendMessage)
	rest.RegisterGetGraph(api, getGraph)
	rest.RegisterSearchGraph(api, searchGraph)
	rest.RegisterRecall(api, recall)

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

// llmReplier is what every llm-package adapter (Stub, OpenAI, future
// Anthropic) already satisfies structurally. Declared here so the
// bridge below is agnostic to the concrete provider — the only place
// in the binary that picks which one is selectChatLLM.
type llmReplier interface {
	Reply(ctx context.Context, history []llmadapter.Turn) (string, error)
}

// chatLLMBridge adapts any llm-package adapter to the chat.LLMReplier
// interface the usecase expects. It exists because the usecase
// declares Turn as its own type per the consumer-side-interface rule,
// so the adapter cannot be passed directly even though the field shape
// is identical.
type chatLLMBridge struct {
	provider llmReplier
}

func (b chatLLMBridge) Reply(ctx context.Context, history []chatuc.Turn) (string, error) {
	turns := make([]llmadapter.Turn, len(history))
	for i, t := range history {
		turns[i] = llmadapter.Turn{Role: t.Role, Content: t.Content}
	}
	return b.provider.Reply(ctx, turns)
}

// selectChatLLM picks the LLM adapter based on config.LLM.Provider.
// Stub is always available and deterministic — useful in local and
// test. OpenAI requires OPENAI_API_KEY and hits the real API; we fail
// at startup if the key is missing, so a misconfigured deploy never
// silently falls back to the stub.
func selectChatLLM(cfg config.Config) (chatuc.LLMReplier, error) {
	switch cfg.LLM.Provider {
	case config.LLMProviderUnset, config.LLMProviderStub:
		return chatLLMBridge{provider: llmadapter.NewStub()}, nil
	case config.LLMProviderOpenAI:
		client, err := llmadapter.NewOpenAI(
			cfg.LLM.OpenAIAPIKey,
			cfg.LLM.ExtractionModel,
			llmadapter.WithOpenAISystemPrompt(chatSystemPrompt),
		)
		if err != nil {
			return nil, fmt.Errorf("llm openai: %w", err)
		}
		return chatLLMBridge{provider: client}, nil
	default:
		return nil, fmt.Errorf("unknown llm provider %q", cfg.LLM.Provider)
	}
}

// chatSystemPrompt sets the assistant's voice for the conversation
// usecase. Kept in the binary (not in the YAML) because it ships with
// the code — promptsmithing happens in PRs, not in environment.
const chatSystemPrompt = `Ты — внимательный собеседник в приложении Mnema, помогающем человеку сохранять мысли, идеи и воспоминания. Отвечай по-русски, кратко (1–3 предложения). Никаких списков, заголовков или эмодзи. Если человек поделился воспоминанием или идеей — мягко подтверди, при необходимости задай один уточняющий вопрос. Не давай советов, если их не просили.`

// selectMailer picks the email adapter based on environment. Test wires
// a captor (in-memory). Local and prod both use SMTP — the difference
// is just the host: mailpit locally, Resend in prod.
func selectMailer(cfg config.Config) mailer {
	if cfg.Env == config.EnvTest {
		return emailadapter.NewCaptor()
	}
	return emailadapter.NewSMTPSender(cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.From)
}

// selectExtractor picks the extractor adapter the same way as
// selectChatLLM. Both sides of the LLM workload (chat reply +
// extraction) follow cfg.LLM.Provider so an environment is either
// "fully stubbed" or "fully OpenAI" — we don't currently mix.
func selectExtractor(cfg config.Config) (extractionuc.Extractor, error) {
	switch cfg.LLM.Provider {
	case config.LLMProviderUnset, config.LLMProviderStub:
		return llmadapter.NewExtractorStub(), nil
	case config.LLMProviderOpenAI:
		client, err := llmadapter.NewExtractorOpenAI(cfg.LLM.OpenAIAPIKey, cfg.LLM.ExtractionModel)
		if err != nil {
			return nil, fmt.Errorf("extractor openai: %w", err)
		}
		return client, nil
	default:
		return nil, fmt.Errorf("unknown llm provider %q", cfg.LLM.Provider)
	}
}

// selectEmbedder picks the embeddings adapter following the same
// stub/openai split as the chat and extraction LLMs. Stub vectors are
// deterministic and dimension-matched to text-embedding-3-small (1536),
// so a stubbed environment exercises the same DB column without any
// schema branching.
func selectEmbedder(cfg config.Config) (extractionuc.Embedder, error) {
	switch cfg.LLM.Provider {
	case config.LLMProviderUnset, config.LLMProviderStub:
		return llmadapter.NewEmbedderStub(), nil
	case config.LLMProviderOpenAI:
		client, err := llmadapter.NewEmbedderOpenAI(cfg.LLM.OpenAIAPIKey, cfg.LLM.EmbeddingModel)
		if err != nil {
			return nil, fmt.Errorf("embedder openai: %w", err)
		}
		return client, nil
	default:
		return nil, fmt.Errorf("unknown llm provider %q", cfg.LLM.Provider)
	}
}

// selectRecall builds the recall pipeline. Today every provider lands
// on the stub triplet — the LLM-driven anchor extractor, candidate
// finder, and answer generator are scheduled in Phase 4.5 of the
// roadmap. Branching on cfg.LLM.Provider already, so the OpenAI/local
// LLM wiring will only need to flip the cases below without touching
// the call site.
func selectRecall(cfg config.Config) (*recalluc.Recall, error) {
	switch cfg.LLM.Provider {
	case config.LLMProviderUnset, config.LLMProviderStub, config.LLMProviderOpenAI:
		return &recalluc.Recall{
			Anchors:    llmadapter.NewRecallAnchorsStub(),
			Candidates: llmadapter.NewRecallCandidatesStub(),
			Answers:    llmadapter.NewRecallAnswersStub(),
		}, nil
	default:
		return nil, fmt.Errorf("unknown llm provider %q", cfg.LLM.Provider)
	}
}

// nodesBridge adapts the postgres nodes Repo to the consumer-side port
// declared by extraction. The two CreateParams structs are structurally
// identical but nominally distinct (Go is nominal), so we copy field
// for field. Same pattern as chatLLMBridge.
type nodesBridge struct {
	repo *pgnodes.Repo
}

func (b nodesBridge) Create(ctx context.Context, p extractionuc.NodeCreateParams) (domain.Node, error) {
	return b.repo.Create(ctx, pgnodes.CreateParams{
		UserID:              p.UserID,
		Type:                p.Type,
		Title:               p.Title,
		Content:             p.Content,
		Metadata:            p.Metadata,
		OccurredAt:          p.OccurredAt,
		OccurredAtPrecision: p.OccurredAtPrecision,
		SourceMessageID:     p.SourceMessageID,
	})
}

// edgesBridge mirrors nodesBridge for the edges adapter.
type edgesBridge struct {
	repo *pgedges.Repo
}

func (b edgesBridge) Create(ctx context.Context, p extractionuc.EdgeCreateParams) (domain.Edge, error) {
	return b.repo.Create(ctx, pgedges.CreateParams{
		UserID:   p.UserID,
		SourceID: p.SourceID,
		TargetID: p.TargetID,
		Type:     p.Type,
	})
}

// graphNodesBridge bridges graphuc.NodeListParams (consumer-side) to the
// adapter's pgnodes.ListForGraphParams. The two structs are field-for-
// field identical, but Go nominal typing treats them as distinct — so
// we copy. Same pattern as nodesBridge / edgesBridge.
type graphNodesBridge struct {
	repo *pgnodes.Repo
}

func (b graphNodesBridge) ListForGraph(ctx context.Context, p graphuc.NodeListParams) ([]domain.Node, error) {
	return b.repo.ListForGraph(ctx, pgnodes.ListForGraphParams{
		UserID: p.UserID,
		Types:  p.Types,
		Since:  p.Since,
		Limit:  p.Limit,
	})
}

// graphSearchNodesBridge bridges graphuc.NodeSearchParams (consumer-side)
// to the adapter's pgnodes.SearchParams. Same nominal-vs-structural copy
// as the other postgres bridges.
type graphSearchNodesBridge struct {
	repo *pgnodes.Repo
}

func (b graphSearchNodesBridge) Search(ctx context.Context, p graphuc.NodeSearchParams) ([]domain.Node, error) {
	return b.repo.Search(ctx, pgnodes.SearchParams{
		UserID: p.UserID,
		Query:  p.Query,
		Vector: p.Vector,
		Types:  p.Types,
		Limit:  p.Limit,
	})
}

// messageExtractorBridge satisfies chatuc.MessageExtractor by running
// the extraction usecase and swallowing errors into the log. The
// non-fatal contract lives here, not in the usecase — the chat path
// stays simple and a broken extractor never blocks chat.
type messageExtractorBridge struct {
	extract *extractionuc.Extract
	log     zerolog.Logger
}

func (b messageExtractorBridge) ExtractFromMessage(ctx context.Context, userID, messageID, content string) {
	out, err := b.extract.Run(ctx, extractionuc.ExtractInput{
		UserID:    userID,
		MessageID: messageID,
		Content:   content,
	})
	if err != nil {
		b.log.Warn().
			Err(err).
			Str("user_id", userID).
			Str("message_id", messageID).
			Msg("extraction failed")
		return
	}
	b.log.Debug().
		Str("user_id", userID).
		Str("message_id", messageID).
		Int("nodes", len(out.NodeIDs)).
		Int("edges", len(out.EdgeIDs)).
		Int("embedded", out.Embedded).
		Int("embed_failures", out.EmbedFailures).
		Msg("extraction stored")
}
