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
	"github.com/DBulamu/mnema/backend/internal/usecase/extraction"
)

// extractionSystemPrompt steers the model to produce a strict JSON
// payload matching the schema below. The Russian voice mirrors the chat
// system prompt — Mnema's UX is monolingual at MVP.
const extractionSystemPrompt = `Ты помогаешь приложению Mnema извлекать узлы графа жизни из реплики пользователя.
Верни строго JSON вида {"nodes":[...], "edges":[...]} без пояснений.

Каждый node:
  - "local_id": короткая строка вроде "n1", "n2" (нужна только для связей внутри ответа)
  - "type": один из "thought","idea","memory","dream","emotion","task","event","person","place","topic"
  - "title": краткое имя (для людей/мест/событий) или null
  - "content": основной текст или null
  - "occurred_at": ISO-8601 дата/время или null (только для событий и воспоминаний)
  - "occurred_at_precision": "day"|"month"|"year"|null

Каждый edge:
  - "source_local_id" и "target_local_id" ссылаются на local_id из nodes
  - "type": один из "part_of","mentions","related_to","triggered_by","evolved_into","about"

Если реплика — обычная мысль без структуры, верни один node типа "thought" с её содержимым в content и пустые edges.
Не выдумывай узлы которых нет в тексте.`

// ExtractorOpenAI calls OpenAI's chat-completions in JSON-mode and
// decodes the structured payload into the extractor.Extraction shape.
//
// The transport is deliberately separate from the chat OpenAI struct
// (Reply) — both share the bearer-auth POST pattern but the request
// bodies, response shapes and prompts are different enough that a
// shared helper would obscure more than it'd save.
type ExtractorOpenAI struct {
	apiKey  string
	model   string
	baseURL string
	http    *http.Client
}

// ExtractorOpenAIOption configures the extractor at construction.
type ExtractorOpenAIOption func(*ExtractorOpenAI)

func WithExtractorOpenAIBaseURL(u string) ExtractorOpenAIOption {
	return func(e *ExtractorOpenAI) { e.baseURL = strings.TrimRight(u, "/") }
}

func WithExtractorOpenAIHTTPClient(c *http.Client) ExtractorOpenAIOption {
	return func(e *ExtractorOpenAI) { e.http = c }
}

// NewExtractorOpenAI builds the JSON-mode extractor. apiKey and model
// are required. Defaults match the chat client (60s timeout, public
// OpenAI base URL) so deploys only set what they need to override.
func NewExtractorOpenAI(apiKey, model string, opts ...ExtractorOpenAIOption) (*ExtractorOpenAI, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("openai extractor: api key is required")
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("openai extractor: model is required")
	}
	e := &ExtractorOpenAI{
		apiKey:  apiKey,
		model:   model,
		baseURL: openaiDefaultBaseURL,
		http:    &http.Client{Timeout: openaiDefaultTimeout},
	}
	for _, opt := range opts {
		opt(e)
	}
	return e, nil
}

// extractionRequest mirrors the JSON-mode-enabled chat-completions body.
// response_format = json_object forces a syntactically valid JSON reply
// — schema validity is still our job.
type extractionRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	ResponseFormat responseFormat `json:"response_format"`
}

type responseFormat struct {
	Type string `json:"type"`
}

const responseFormatJSONObject = "json_object"

// rawExtraction is the wire shape we ask the model to produce. We keep
// it separate from extraction.Extraction so changes to the prompt /
// schema do not leak into the usecase types.
type rawExtraction struct {
	Nodes []rawNode `json:"nodes"`
	Edges []rawEdge `json:"edges"`
}

type rawNode struct {
	LocalID             string  `json:"local_id"`
	Type                string  `json:"type"`
	Title               *string `json:"title"`
	Content             *string `json:"content"`
	OccurredAt          *string `json:"occurred_at"`
	OccurredAtPrecision *string `json:"occurred_at_precision"`
}

type rawEdge struct {
	SourceLocalID string `json:"source_local_id"`
	TargetLocalID string `json:"target_local_id"`
	Type          string `json:"type"`
}

// Extract calls OpenAI in JSON-mode and parses the structured reply.
// Type / edge-type validity is checked by the extraction usecase, so
// this method only enforces schema-level invariants (parseable JSON,
// known fields, well-formed timestamps).
func (e *ExtractorOpenAI) Extract(ctx context.Context, content string) (extraction.Extraction, error) {
	if strings.TrimSpace(content) == "" {
		return extraction.Extraction{}, nil
	}

	body, err := json.Marshal(extractionRequest{
		Model: e.model,
		Messages: []chatMessage{
			{Role: string(domain.RoleSystem), Content: extractionSystemPrompt},
			{Role: string(domain.RoleUser), Content: content},
		},
		ResponseFormat: responseFormat{Type: responseFormatJSONObject},
	})
	if err != nil {
		return extraction.Extraction{}, fmt.Errorf("openai extractor: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		e.baseURL+openaiChatCompletionsPath,
		bytes.NewReader(body),
	)
	if err != nil {
		return extraction.Extraction{}, fmt.Errorf("openai extractor: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.http.Do(req)
	if err != nil {
		return extraction.Extraction{}, fmt.Errorf("openai extractor: do request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return extraction.Extraction{}, fmt.Errorf("openai extractor: read response: %w", err)
	}

	if resp.StatusCode/100 != 2 {
		var parsed chatResponse
		if jsonErr := json.Unmarshal(raw, &parsed); jsonErr == nil && parsed.Error != nil {
			return extraction.Extraction{}, fmt.Errorf("openai extractor: %d %s: %s", resp.StatusCode, parsed.Error.Type, parsed.Error.Message)
		}
		return extraction.Extraction{}, fmt.Errorf("openai extractor: %d: %s", resp.StatusCode, truncateForError(string(raw), openaiErrorBodyLogLimit))
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return extraction.Extraction{}, fmt.Errorf("openai extractor: decode envelope: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return extraction.Extraction{}, errors.New("openai extractor: response has no choices")
	}

	// JSON-mode guarantees the content is valid JSON; we still defend
	// against an empty string (model returned `""` somehow).
	contentStr := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if contentStr == "" {
		return extraction.Extraction{}, errors.New("openai extractor: empty content")
	}

	var raw2 rawExtraction
	if err := json.Unmarshal([]byte(contentStr), &raw2); err != nil {
		return extraction.Extraction{}, fmt.Errorf("openai extractor: decode payload: %w", err)
	}

	return rawToExtraction(raw2)
}

// rawToExtraction converts the wire payload into the usecase types,
// parsing timestamps along the way. Invalid timestamps are dropped
// (occurred_at goes nil) rather than fail — the model gets dates
// approximately right and we'd rather keep the node than reject it.
func rawToExtraction(r rawExtraction) (extraction.Extraction, error) {
	out := extraction.Extraction{
		Nodes: make([]extraction.ExtractedNode, 0, len(r.Nodes)),
		Edges: make([]extraction.ExtractedEdge, 0, len(r.Edges)),
	}
	for _, rn := range r.Nodes {
		en := extraction.ExtractedNode{
			LocalID: rn.LocalID,
			Type:    domain.NodeType(rn.Type),
			Title:   rn.Title,
			Content: rn.Content,
		}
		if rn.OccurredAt != nil {
			if t, err := parseFlexibleTime(*rn.OccurredAt); err == nil {
				en.OccurredAt = &t
			}
		}
		if rn.OccurredAtPrecision != nil {
			p := domain.OccurredAtPrecision(*rn.OccurredAtPrecision)
			en.OccurredAtPrecision = &p
		}
		out.Nodes = append(out.Nodes, en)
	}
	for _, re := range r.Edges {
		out.Edges = append(out.Edges, extraction.ExtractedEdge{
			SourceLocalID: re.SourceLocalID,
			TargetLocalID: re.TargetLocalID,
			Type:          domain.EdgeType(re.Type),
		})
	}
	return out, nil
}

// flexibleTimeLayouts lists the formats we accept from the model. The
// LLM sometimes gives a year-only or year-month string when precision
// is coarse — we parse those into the first day of the period and rely
// on OccurredAtPrecision to record the granularity.
var flexibleTimeLayouts = []string{
	time.RFC3339,
	"2006-01-02",
	"2006-01",
	"2006",
}

func parseFlexibleTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range flexibleTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable time %q", s)
}
