package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

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

// chatJSONStream is the streaming variant of chatJSON. It calls onDelta
// for every chunk of the assistant's content as it arrives and returns
// the full concatenated content once the stream completes. Ollama's
// stream=true protocol is NDJSON: one ollamaChatResponse per line, with
// `done:true` on the final frame.
//
// We do not enforce json-mode here on purpose. Forcing json + streaming
// in ollama can produce a single big chunk on `done:true` rather than
// per-token deltas (the model fills the JSON structure once it knows
// the shape). Callers that need JSON in a streaming setting can ask
// the model to emit text and parse client-side, or call chatJSON.
func (c *ollamaClient) chatJSONStream(ctx context.Context, system, user string, onDelta func(string) error) (string, error) {
	reqBody, err := json.Marshal(ollamaChatRequest{
		Model:  c.model,
		Stream: true,
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

	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		var parsed ollamaChatResponse
		if jsonErr := json.Unmarshal(raw, &parsed); jsonErr == nil && parsed.Error != "" {
			return "", fmt.Errorf("ollama: %d: %s", resp.StatusCode, parsed.Error)
		}
		return "", fmt.Errorf("ollama: %d: %s", resp.StatusCode, truncateForError(string(raw), ollamaErrorBodyLogLimit))
	}

	// Buffer can grow: ollama's `done:true` frame includes per-call
	// statistics (eval_count, prompt_eval_duration, ...). 1 MiB max
	// line size is overkill for chat tokens but keeps us safe if a
	// future server packs a long final frame.
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)

	var full strings.Builder
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var frame ollamaChatResponse
		if err := json.Unmarshal(line, &frame); err != nil {
			return "", fmt.Errorf("ollama: decode stream frame: %w", err)
		}
		if frame.Error != "" {
			return "", fmt.Errorf("ollama stream: %s", frame.Error)
		}
		piece := frame.Message.Content
		if piece == "" {
			continue
		}
		full.WriteString(piece)
		if onDelta != nil {
			if err := onDelta(piece); err != nil {
				return "", err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("ollama: read stream: %w", err)
	}
	out := strings.TrimSpace(full.String())
	if out == "" {
		return "", errors.New("ollama: empty assistant content")
	}
	return out, nil
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

// Prompts are locked in code, not YAML — promptsmithing happens in PRs.
//
// We ship one prompt per supported UI language. The selector is lenient
// (prefix match on lang) because BCP-47 tags vary in shape ("en", "en-US")
// and the recall contract treats lang as a hint, not a whitelist. Anything
// we don't recognise falls back to Russian — that matches the project's
// default and keeps the model on familiar ground.

const anchorPromptRu = `Ты извлекаешь "якоря" из текста запроса пользователя для поиска в его персональном графе мыслей и воспоминаний.

Верни строго один JSON-объект с полями place, person, event, topic, time. Каждое поле — короткая строка (одно-два слова) или пустая строка "" если в запросе нет такого якоря. Не выдумывай якоря — если в тексте нет, например, места, ставь "".

Пример входа: "вспомни поездку в Питер с мамой летом 2024".
Пример выхода: {"place":"Питер","person":"мама","event":"поездка","topic":"","time":"лето 2024"}

Ничего кроме JSON не выводи.`

const anchorPromptEn = `You extract "anchors" from a user's recall query for searching their personal graph of thoughts and memories.

Return strictly one JSON object with fields place, person, event, topic, time. Each field is a short string (one or two words) or an empty string "" if the query does not contain such an anchor. Do not invent anchors — if there is no place, for example, set "".

Example input: "remind me of the trip to Saint Petersburg with mum in summer 2024".
Example output: {"place":"Saint Petersburg","person":"mum","event":"trip","topic":"","time":"summer 2024"}

Output nothing but JSON. Anchor values must be in the same language as the query.`

// anchorPromptFor picks the system prompt for the request language. The
// match is a case-insensitive prefix so "en", "en-US", "en_GB" all hit
// the English prompt. Anything unknown falls back to Russian.
func anchorPromptFor(lang string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "en") {
		return anchorPromptEn
	}
	return anchorPromptRu
}

// rawAnchors mirrors the JSON ollama is asked to return.
type rawAnchors struct {
	Place  string `json:"place"`
	Person string `json:"person"`
	Event  string `json:"event"`
	Topic  string `json:"topic"`
	Time   string `json:"time"`
}

// ExtractAnchors satisfies recall.AnchorExtractor. The system prompt is
// picked per request language so the model emits anchors in the same
// language as the query — text-search is case-sensitive on language
// (Cyrillic vs Latin), so a Russian anchor against an English query
// finds nothing.
func (a *RecallAnchorsOllama) ExtractAnchors(ctx context.Context, text, lang string) ([]recall.Anchor, error) {
	content, err := a.client.chatJSON(ctx, anchorPromptFor(lang), text)
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

// Answer prompts are per-language on purpose. We tried a single bilingual
// prompt and qwen2.5:7b sometimes ignored the "respond in the request
// language" line and wrote the answer in Russian regardless of lang —
// pinning the prompt to the target language is a more reliable lever than
// asking the model to follow a meta-instruction. The rune-offset clause
// is identical in both: the usecase's validateDraft is what actually
// enforces it, the prompt only nudges the model toward producing offsets
// in the right shape.

const answerPromptRu = `Ты помогаешь пользователю вспомнить события из его персонального графа мыслей и воспоминаний.

Тебе дан запрос пользователя и список узлов-кандидатов из его графа (каждый — id, тип, заголовок, содержание, дата). Используя ТОЛЬКО эти узлы, напиши короткий ответ ПО-РУССКИ. Не выдумывай факты, которых нет в кандидатах. Если данных не хватает — скажи это прямо.

Затем выдели в своём ответе подстроки ("спаны"), которые ссылаются на конкретных кандидатов. Спаны — это диапазоны символов (Unicode code points / руны) в твоём ответе: start включительно, end исключительно. Каждый спан указывает один или несколько id кандидатов, которые он подтверждает.

Если кандидатов нет — верни короткий ответ "Не помню" и пустой список spans.

Верни строго один JSON-объект:
{"answer":"...","spans":[{"start":0,"end":5,"node_ids":["uuid1"]}]}

Ничего кроме JSON.`

const answerPromptEn = `You help the user recall events from their personal graph of thoughts and memories.

You are given the user's query and a list of candidate nodes from their graph (each — id, type, title, content, date). Using ONLY these nodes, write a short answer IN ENGLISH. Do not invent facts that are not in the candidates. If there is not enough information, say so plainly.

Then mark substrings in your answer ("spans") that reference specific candidates. Spans are character ranges (Unicode code points / runes) in your answer: start inclusive, end exclusive. Each span lists one or more candidate ids it supports.

If there are no candidates, return a short answer "I don't remember" and an empty spans list.

Return strictly one JSON object:
{"answer":"...","spans":[{"start":0,"end":5,"node_ids":["uuid1"]}]}

Nothing but JSON.`

// answerPromptFor mirrors anchorPromptFor for the answer step.
func answerPromptFor(lang string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "en") {
		return answerPromptEn
	}
	return answerPromptRu
}

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
		return recall.AnswerDraft{Answer: noCandidatesAnswer(lang)}, nil
	}

	user := buildAnswerUserMessage(text, lang, candidates)

	content, err := g.client.chatJSON(ctx, answerPromptFor(lang), user)
	if err != nil {
		return recall.AnswerDraft{}, err
	}
	return parseAnswerJSON(content)
}

// GenerateAnswerStream satisfies recall.AnswerStreamGenerator. The
// model is asked to emit the same JSON shape as the sync path; on the
// wire we receive incremental NDJSON frames, each carrying a chunk of
// the assistant's serialized JSON. We feed those chunks into a small
// state machine that pulls out the value of the "answer" field and
// emits it via the recall.AnswerEmitter callback as plain text. The
// JSON itself is reassembled and parsed once the stream completes —
// that is where spans come from.
//
// Why a state machine and not waiting for the full JSON: the whole
// point of streaming on this path is to mask the 5–15s answer-step
// latency. The user wants to see the answer text appear as it is
// generated; spans only matter at the very end.
func (g *RecallAnswersOllama) GenerateAnswerStream(ctx context.Context, text, lang string, candidates []domain.Node, emit recall.AnswerEmitter) (recall.AnswerDraft, error) {
	if len(candidates) == 0 {
		ans := noCandidatesAnswer(lang)
		if emit != nil {
			if err := emit(ans); err != nil {
				return recall.AnswerDraft{}, err
			}
		}
		return recall.AnswerDraft{Answer: ans}, nil
	}

	user := buildAnswerUserMessage(text, lang, candidates)

	extractor := &answerFieldStreamer{}
	if emit != nil {
		extractor.emit = emit
	}

	content, err := g.client.chatJSONStream(ctx, answerPromptFor(lang), user, extractor.feed)
	if err != nil {
		return recall.AnswerDraft{}, err
	}
	return parseAnswerJSON(content)
}

// parseAnswerJSON decodes the LLM's JSON-mode output into our domain
// shape. Shared by sync and streaming paths so a malformed JSON is a
// single error message in logs regardless of how it was produced.
func parseAnswerJSON(content string) (recall.AnswerDraft, error) {
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

// noCandidatesAnswer is the hand-written response when the recall
// pipeline produced an empty candidate set. Localised so the streaming
// and sync paths agree byte-for-byte.
func noCandidatesAnswer(lang string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "en") {
		return "I don't remember — nothing in your graph matches."
	}
	return "Не помню — в графе нет подходящих воспоминаний."
}

// answerFieldStreamer is an incremental extractor of the "answer"
// field from a JSON-mode stream. The model emits the JSON object one
// token at a time; we follow it character by character and emit just
// the contents of the answer string to the caller, decoded from JSON
// escape sequences. Other fields (spans) are ignored — they are
// parsed once the full JSON arrives.
//
// State machine:
//
//   waitingKey — accumulating bytes until we see `"answer"` followed
//                by a colon. We tolerate whitespace and any preceding
//                fields, because we cannot rely on JSON-mode to emit
//                "answer" first.
//   inString   — inside the value of "answer". Plain bytes are
//                forwarded immediately. Backslash escapes are decoded
//                (\n, \t, \", \\, \uXXXX) and the decoded character
//                is forwarded.
//   done       — we saw the closing quote. Subsequent bytes are
//                discarded.
type answerFieldStreamer struct {
	emit recall.AnswerEmitter

	state      answerStreamState
	keyBuf     []byte // sliding tail of recent bytes for matching `"answer"`
	escape     bool   // true after a backslash inside the value
	unicodeN   int    // remaining hex digits to consume after \u
	unicodeAcc rune
	// pendingUTF8 buffers raw bytes inside an unescaped value that
	// have not yet formed a complete UTF-8 rune. The streaming
	// transport emits text fragments — handing it half a rune
	// produces U+FFFD on the wire, which is what we saw in the
	// first live test of /v1/recall/stream against qwen2.5:7b.
	pendingUTF8 []byte
}

type answerStreamState int

const (
	streamStateWaitingKey answerStreamState = iota
	streamStateAfterKey                     // saw `"answer"`, waiting for `:`
	streamStateBeforeValue                  // saw `:`, waiting for opening `"`
	streamStateInString
	streamStateDone
)

// answerKey is the literal token we are scanning for in the
// pre-string portion of the JSON.
const answerKey = `"answer"`

// keyBufWindow keeps just enough trailing bytes around to detect the
// answerKey token even when it is split across stream frames. 16 is
// comfortably more than `"answer"` (8) plus surrounding whitespace.
const keyBufWindow = 16

// feed consumes a chunk of streamed JSON and emits decoded answer
// characters via the emitter. Returns whatever error the emitter
// returns; otherwise nil.
func (a *answerFieldStreamer) feed(chunk string) error {
	for i := 0; i < len(chunk); i++ {
		ch := chunk[i]
		switch a.state {
		case streamStateWaitingKey:
			a.keyBuf = append(a.keyBuf, ch)
			if len(a.keyBuf) > keyBufWindow {
				a.keyBuf = a.keyBuf[len(a.keyBuf)-keyBufWindow:]
			}
			if bytes.Contains(a.keyBuf, []byte(answerKey)) {
				a.state = streamStateAfterKey
				a.keyBuf = nil
			}
		case streamStateAfterKey:
			if ch == ':' {
				a.state = streamStateBeforeValue
			} else if ch != ' ' && ch != '\t' && ch != '\n' && ch != '\r' {
				// Unexpected byte between key and colon — JSON-mode
				// shouldn't produce this, but if it does we abandon
				// the streaming attempt and let the final parse step
				// surface the answer.
				a.state = streamStateDone
			}
		case streamStateBeforeValue:
			switch ch {
			case '"':
				a.state = streamStateInString
			case ' ', '\t', '\n', '\r':
				// skip whitespace before the opening quote
			default:
				a.state = streamStateDone
			}
		case streamStateInString:
			if a.unicodeN > 0 {
				a.unicodeAcc = a.unicodeAcc<<4 | rune(hexNibble(ch))
				a.unicodeN--
				if a.unicodeN == 0 {
					if err := a.emitRune(a.unicodeAcc); err != nil {
						return err
					}
					a.unicodeAcc = 0
				}
				continue
			}
			if a.escape {
				a.escape = false
				switch ch {
				case '"':
					if err := a.emitByte('"'); err != nil {
						return err
					}
				case '\\':
					if err := a.emitByte('\\'); err != nil {
						return err
					}
				case '/':
					if err := a.emitByte('/'); err != nil {
						return err
					}
				case 'b':
					if err := a.emitByte('\b'); err != nil {
						return err
					}
				case 'f':
					if err := a.emitByte('\f'); err != nil {
						return err
					}
				case 'n':
					if err := a.emitByte('\n'); err != nil {
						return err
					}
				case 'r':
					if err := a.emitByte('\r'); err != nil {
						return err
					}
				case 't':
					if err := a.emitByte('\t'); err != nil {
						return err
					}
				case 'u':
					a.unicodeN = 4
					a.unicodeAcc = 0
				default:
					// Unknown escape — emit the raw character so we
					// don't drop user text on the floor.
					if err := a.emitByte(ch); err != nil {
						return err
					}
				}
				continue
			}
			switch ch {
			case '\\':
				a.escape = true
			case '"':
				a.state = streamStateDone
			default:
				if err := a.emitByte(ch); err != nil {
					return err
				}
			}
		case streamStateDone:
			return nil
		}
	}
	return nil
}

// emitByte buffers a single raw byte inside the value. A whole UTF-8
// rune may span 1–4 bytes; we only flush to the consumer once the
// buffer holds a valid prefix. JSON forbids unescaped bytes that are
// not part of a UTF-8 rune, so the only failure mode here is "stream
// frame split a rune in the middle" — exactly what we want to mask.
func (a *answerFieldStreamer) emitByte(b byte) error {
	a.pendingUTF8 = append(a.pendingUTF8, b)
	return a.flushPendingUTF8()
}

func (a *answerFieldStreamer) flushPendingUTF8() error {
	if a.emit == nil {
		a.pendingUTF8 = a.pendingUTF8[:0]
		return nil
	}
	// Walk the buffer rune by rune. On a leading utf8.RuneError with
	// only 1 byte consumed we leave the trailing bytes in the buffer
	// for the next call — that is the "incomplete rune" case.
	out := a.pendingUTF8
	for len(out) > 0 {
		r, size := utf8.DecodeRune(out)
		if r == utf8.RuneError && size <= 1 {
			// Either 0 bytes (empty), or 1 byte that begins a longer
			// rune that has not arrived yet. Wait for more bytes.
			break
		}
		if err := a.emit(string(out[:size])); err != nil {
			a.pendingUTF8 = a.pendingUTF8[:0]
			return err
		}
		out = out[size:]
	}
	// Compact: copy the remaining tail to the front so the slice
	// header doesn't grow indefinitely on long answers.
	if len(out) == 0 {
		a.pendingUTF8 = a.pendingUTF8[:0]
	} else {
		a.pendingUTF8 = append(a.pendingUTF8[:0], out...)
	}
	return nil
}

func (a *answerFieldStreamer) emitRune(r rune) error {
	// Flush any half-rune in the byte buffer before sending an
	// already-decoded code point (escape sequences come as whole
	// runes via this path).
	if err := a.flushPendingUTF8(); err != nil {
		return err
	}
	if a.emit == nil {
		return nil
	}
	return a.emit(string(r))
}


// hexNibble decodes one hex character. We accept a–f and A–F; any
// other byte yields 0, which is fine because the surrounding stream
// validation will catch a malformed unicode escape downstream.
func hexNibble(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10
	default:
		return 0
	}
}

// buildAnswerUserMessage formats the prompt's user turn. We hand the
// model a compact representation rather than full JSON nodes — every
// extra field the model has to reason about increases the chance it
// confuses content with metadata. id, type, title, content, date is
// enough to write a citation-bound answer.
//
// Headers are localised so the user-turn looks like one coherent
// conversation in the target language — mixed-language prompts seem to
// nudge qwen back toward Russian even when the system prompt insists on
// English.
func buildAnswerUserMessage(text, lang string, candidates []domain.Node) string {
	en := strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "en")
	var b strings.Builder
	if en {
		b.WriteString("User query (language: ")
	} else {
		b.WriteString("Запрос пользователя (язык: ")
	}
	if strings.TrimSpace(lang) == "" {
		b.WriteString("ru")
	} else {
		b.WriteString(lang)
	}
	b.WriteString("):\n")
	b.WriteString(text)
	if en {
		b.WriteString("\n\nCandidates (id | type | title | content | date):\n")
	} else {
		b.WriteString("\n\nКандидаты (id | тип | title | content | дата):\n")
	}
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
