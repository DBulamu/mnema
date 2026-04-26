package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/DBulamu/mnema/backend/internal/usecase/recall"
)

// Recall pipeline adapters backed by a local ollama daemon.
//
// Why ollama and not OpenAI on the MVP: see project memory
// "project_recall_llm.md". Privacy + zero per-request cost beat the
// extra latency on the first version; SSE will mask the wait later.
//
// Both adapters speak ollama's `/api/chat` endpoint with `format:
// "json"` so the model is forced into JSON-mode. Output is then parsed
// with the project's domain types and handed back to the recall
// usecase, which validates spans and node_id whitelists itself —
// the LLM is allowed to lie, the usecase keeps it honest.

const (
	// ollamaDefaultBaseURL is the canonical local ollama endpoint.
	ollamaDefaultBaseURL = "http://localhost:11434"

	// ollamaChatPath is the chat endpoint relative to baseURL.
	ollamaChatPath = "/api/chat"

	// ollamaDefaultTimeout caps a single recall LLM call. qwen2.5:7b
	// on Apple Silicon spends ~10–20s on the answer step; we leave
	// some headroom but cap so a stuck request can't pin the handler
	// goroutine forever — the recall path is sync today.
	ollamaDefaultTimeout = 120 * time.Second

	// ollamaErrorBodyLogLimit truncates upstream error bodies in our
	// error strings so logs stay readable.
	ollamaErrorBodyLogLimit = 500
)

// ollamaClient is the shared HTTP plumbing the two adapters reuse. It
// is intentionally not exported — both Anchors and Answers wrap it
// with their own prompts and parsers.
type ollamaClient struct {
	baseURL string
	model   string
	http    *http.Client
}

// OllamaOption configures the shared client at construction time.
type OllamaOption func(*ollamaClient)

// WithOllamaBaseURL points the client at a non-default ollama daemon
// (remote box, alternate port). Trailing slash is trimmed.
func WithOllamaBaseURL(u string) OllamaOption {
	return func(c *ollamaClient) { c.baseURL = strings.TrimRight(u, "/") }
}

// WithOllamaHTTPClient overrides the default http.Client. Used by the
// httptest-driven unit tests.
func WithOllamaHTTPClient(h *http.Client) OllamaOption {
	return func(c *ollamaClient) { c.http = h }
}

func newOllamaClient(model string, opts ...OllamaOption) (*ollamaClient, error) {
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("ollama: model is required")
	}
	c := &ollamaClient{
		baseURL: ollamaDefaultBaseURL,
		model:   model,
		http:    &http.Client{Timeout: ollamaDefaultTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// chat is the request shape ollama accepts on /api/chat. Format=json
// forces the model to emit a single JSON object — when set, ollama
// validates the output server-side and re-rolls until it parses.
type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
	Format   string              `json:"format,omitempty"`
}

type ollamaChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ollamaChatResponse decodes only the path we care about — letting
// ollama add fields without breaking us.
type ollamaChatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Error string `json:"error,omitempty"`
}

// chatJSON sends one chat turn and returns the assistant's content
// string. Caller is responsible for unmarshalling that string into a
// concrete shape — ollama already guarantees it parses as JSON when
// format=json is set.
func (c *ollamaClient) chatJSON(ctx context.Context, system, user string) (string, error) {
	reqBody, err := json.Marshal(ollamaChatRequest{
		Model:  c.model,
		Stream: false,
		Format: "json",
		Messages: []ollamaChatMessage{
			{Role: string(domain.RoleSystem), Content: system},
			{Role: string(domain.RoleUser), Content: user},
		},
	})
	if err != nil {
		return "", fmt.Errorf("ollama: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+ollamaChatPath, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("ollama: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama: do request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("ollama: read response: %w", err)
	}

	if resp.StatusCode/100 != 2 {
		var parsed ollamaChatResponse
		if jsonErr := json.Unmarshal(raw, &parsed); jsonErr == nil && parsed.Error != "" {
			return "", fmt.Errorf("ollama: %d: %s", resp.StatusCode, parsed.Error)
		}
		return "", fmt.Errorf("ollama: %d: %s", resp.StatusCode, truncateForError(string(raw), ollamaErrorBodyLogLimit))
	}

	var parsed ollamaChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("ollama: decode envelope: %w", err)
	}
	content := strings.TrimSpace(parsed.Message.Content)
	if content == "" {
		return "", errors.New("ollama: empty assistant content")
	}
	return content, nil
}

// =============================================================================
// Anchor extractor
// =============================================================================

// RecallAnchorsOllama implements recall.AnchorExtractor against ollama.
// The system prompt locks a closed JSON shape — empty strings for
// unfilled slots so the model never invents anchors to "fill the form".
type RecallAnchorsOllama struct {
	client *ollamaClient
}

// NewRecallAnchorsOllama builds the anchor adapter. Model name is the
// only required value — base URL defaults to localhost:11434.
func NewRecallAnchorsOllama(model string, opts ...OllamaOption) (*RecallAnchorsOllama, error) {
	c, err := newOllamaClient(model, opts...)
	if err != nil {
		return nil, fmt.Errorf("recall anchors: %w", err)
	}
	return &RecallAnchorsOllama{client: c}, nil
}

// anchorPrompt is locked here, not in YAML. Promptsmithing happens in
// PRs; iterating on a system prompt by editing a deploy artefact is
// not a workflow we want.
const anchorPrompt = `Ты извлекаешь "якоря" из текста запроса пользователя для поиска в его персональном графе мыслей и воспоминаний.

Верни строго один JSON-объект с полями place, person, event, topic, time. Каждое поле — короткая строка (одно-два слова) или пустая строка "" если в запросе нет такого якоря. Не выдумывай якоря — если в тексте нет, например, места, ставь "".

Пример входа: "вспомни поездку в Питер с мамой летом 2024".
Пример выхода: {"place":"Питер","person":"мама","event":"поездка","topic":"","time":"лето 2024"}

Ничего кроме JSON не выводи.`

// rawAnchors mirrors the JSON ollama is asked to return.
type rawAnchors struct {
	Place  string `json:"place"`
	Person string `json:"person"`
	Event  string `json:"event"`
	Topic  string `json:"topic"`
	Time   string `json:"time"`
}

// ExtractAnchors satisfies recall.AnchorExtractor. The lang field is
// unused for now: the prompt is Russian-centric and qwen handles
// English just fine without a per-language switch. Keeping the param
// in the signature keeps swapping providers a no-op for callers.
func (a *RecallAnchorsOllama) ExtractAnchors(ctx context.Context, text, _ string) ([]recall.Anchor, error) {
	content, err := a.client.chatJSON(ctx, anchorPrompt, text)
	if err != nil {
		return nil, err
	}
	var raw rawAnchors
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, fmt.Errorf("ollama anchors: decode model output: %w", err)
	}
	out := make([]recall.Anchor, 0, 5)
	if v := strings.TrimSpace(raw.Place); v != "" {
		out = append(out, recall.Anchor{Kind: recall.AnchorPlace, Text: v})
	}
	if v := strings.TrimSpace(raw.Person); v != "" {
		out = append(out, recall.Anchor{Kind: recall.AnchorPerson, Text: v})
	}
	if v := strings.TrimSpace(raw.Event); v != "" {
		out = append(out, recall.Anchor{Kind: recall.AnchorEvent, Text: v})
	}
	if v := strings.TrimSpace(raw.Topic); v != "" {
		out = append(out, recall.Anchor{Kind: recall.AnchorTopic, Text: v})
	}
	if v := strings.TrimSpace(raw.Time); v != "" {
		out = append(out, recall.Anchor{Kind: recall.AnchorTime, Text: v})
	}
	return out, nil
}

// =============================================================================
// Answer generator
// =============================================================================

// RecallAnswersOllama implements recall.AnswerGenerator against ollama.
// The candidate nodes are passed in the user message as a compact
// `id|type|title|content|occurred_at` table — the prompt instructs the
// model to attribute every cited fact to a candidate id. The usecase's
// validateDraft is the actual enforcement of the contract.
type RecallAnswersOllama struct {
	client *ollamaClient
}

// NewRecallAnswersOllama builds the answer adapter.
func NewRecallAnswersOllama(model string, opts ...OllamaOption) (*RecallAnswersOllama, error) {
	c, err := newOllamaClient(model, opts...)
	if err != nil {
		return nil, fmt.Errorf("recall answers: %w", err)
	}
	return &RecallAnswersOllama{client: c}, nil
}

// answerPrompt: bilingual on purpose. The model is told to write the
// answer in the request language and to attribute via UTF-8 rune
// offsets — those are also what the usecase validates. We do not let
// the model "explain" rune offsets back to us; the prompt asks for
// integers measured in characters of its own answer string.
const answerPrompt = `Ты помогаешь пользователю вспомнить события из его персонального графа мыслей и воспоминаний.

Тебе дан запрос пользователя и список узлов-кандидатов из его графа (каждый — id, тип, заголовок, содержание, дата). Используя ТОЛЬКО эти узлы, напиши короткий ответ на языке запроса (по умолчанию русский). Не выдумывай факты, которых нет в кандидатах. Если данных не хватает — скажи это прямо.

Затем выдели в своём ответе подстроки ("спаны"), которые ссылаются на конкретных кандидатов. Спаны — это диапазоны символов (Unicode code points / руны) в твоём ответе: start включительно, end исключительно. Каждый спан указывает один или несколько id кандидатов, которые он подтверждает.

Если кандидатов нет — верни короткий ответ "Не помню" (или эквивалент на языке запроса) и пустой список spans.

Верни строго один JSON-объект:
{"answer":"...","spans":[{"start":0,"end":5,"node_ids":["uuid1"]}]}

Ничего кроме JSON.`

// rawAnswer mirrors the JSON the model is asked to return.
type rawAnswer struct {
	Answer string         `json:"answer"`
	Spans  []rawAnswerSpan `json:"spans"`
}

type rawAnswerSpan struct {
	Start   int      `json:"start"`
	End     int      `json:"end"`
	NodeIDs []string `json:"node_ids"`
}

// GenerateAnswer satisfies recall.AnswerGenerator. With zero candidates
// we short-circuit: there is nothing to cite, so calling the LLM only
// burns latency for a hand-written "не помню". The recall validator
// would also dump every span (no candidates means an empty whitelist),
// so the call is genuinely wasted.
func (g *RecallAnswersOllama) GenerateAnswer(ctx context.Context, text, lang string, candidates []domain.Node) (recall.AnswerDraft, error) {
	if len(candidates) == 0 {
		answer := "Не помню — в графе нет подходящих воспоминаний."
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "en") {
			answer = "I don't remember — nothing in your graph matches."
		}
		return recall.AnswerDraft{Answer: answer}, nil
	}

	user := buildAnswerUserMessage(text, lang, candidates)

	content, err := g.client.chatJSON(ctx, answerPrompt, user)
	if err != nil {
		return recall.AnswerDraft{}, err
	}
	var raw rawAnswer
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return recall.AnswerDraft{}, fmt.Errorf("ollama answers: decode model output: %w", err)
	}
	spans := make([]recall.Span, 0, len(raw.Spans))
	for _, s := range raw.Spans {
		spans = append(spans, recall.Span{Start: s.Start, End: s.End, NodeIDs: append([]string(nil), s.NodeIDs...)})
	}
	return recall.AnswerDraft{Answer: raw.Answer, Spans: spans}, nil
}

// buildAnswerUserMessage formats the prompt's user turn. We hand the
// model a compact representation rather than full JSON nodes — every
// extra field the model has to reason about increases the chance it
// confuses content with metadata. id, type, title, content, date is
// enough to write a citation-bound answer.
func buildAnswerUserMessage(text, lang string, candidates []domain.Node) string {
	var b strings.Builder
	b.WriteString("Запрос пользователя (язык: ")
	if strings.TrimSpace(lang) == "" {
		b.WriteString("ru")
	} else {
		b.WriteString(lang)
	}
	b.WriteString("):\n")
	b.WriteString(text)
	b.WriteString("\n\nКандидаты (id | тип | title | content | дата):\n")
	for _, n := range candidates {
		b.WriteString("- ")
		b.WriteString(n.ID)
		b.WriteString(" | ")
		b.WriteString(string(n.Type))
		b.WriteString(" | ")
		if n.Title != nil {
			b.WriteString(*n.Title)
		}
		b.WriteString(" | ")
		if n.Content != nil {
			b.WriteString(strings.ReplaceAll(*n.Content, "\n", " "))
		}
		b.WriteString(" | ")
		if n.OccurredAt != nil {
			b.WriteString(n.OccurredAt.Format(time.RFC3339))
		}
		b.WriteString("\n")
	}
	return b.String()
}
