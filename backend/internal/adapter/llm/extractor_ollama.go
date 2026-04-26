package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/DBulamu/mnema/backend/internal/usecase/extraction"
)

// ExtractorOllama satisfies extraction.Extractor against a local ollama
// daemon (qwen2.5:7b on the MVP). It mirrors the OpenAI extractor's
// JSON-mode contract: the system prompt locks the wire shape, the model
// returns a single JSON object, the recall validator catches the
// remaining lies.
//
// Why a separate adapter instead of an OpenAI-compatible shim: ollama's
// /api/chat is the supported path and already speaks json-mode via
// `format:"json"`. Routing through OpenAI's chat-completions wrapper
// would mean an extra translation layer with no upside — the prompt
// is the only thing this adapter actually owns.
type ExtractorOllama struct {
	client *ollamaClient
}

// NewExtractorOllama builds the ollama-backed extractor. Model is
// required (we never default — picking a model is a deployment
// decision); base URL falls back to ollamaDefaultBaseURL when blank.
func NewExtractorOllama(model string, opts ...OllamaOption) (*ExtractorOllama, error) {
	c, err := newOllamaClient(model, opts...)
	if err != nil {
		return nil, fmt.Errorf("extractor ollama: %w", err)
	}
	return &ExtractorOllama{client: c}, nil
}

// Extract sends the message (plus optional existing-node shortlist) to
// ollama in JSON-mode and decodes the structured reply. Validation of
// node/edge types is left to the extraction usecase, by design — the
// adapter only owns transport and wire-shape decoding.
func (e *ExtractorOllama) Extract(ctx context.Context, content string, existing []extraction.ExistingNode) (extraction.Extraction, error) {
	if strings.TrimSpace(content) == "" {
		return extraction.Extraction{}, nil
	}

	user := buildExtractionUserMessage(content, existing)

	raw, err := e.client.chatJSON(ctx, extractionSystemPrompt, user)
	if err != nil {
		return extraction.Extraction{}, fmt.Errorf("ollama extractor: %w", err)
	}

	var parsed rawExtraction
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return extraction.Extraction{}, fmt.Errorf("ollama extractor: decode payload: %w", err)
	}

	out := extraction.Extraction{
		Nodes: make([]extraction.ExtractedNode, 0, len(parsed.Nodes)),
		Edges: make([]extraction.ExtractedEdge, 0, len(parsed.Edges)),
	}
	for _, rn := range parsed.Nodes {
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
	for _, re := range parsed.Edges {
		out.Edges = append(out.Edges, extraction.ExtractedEdge{
			SourceLocalID:    re.SourceLocalID,
			SourceExistingID: re.SourceExistingID,
			TargetLocalID:    re.TargetLocalID,
			TargetExistingID: re.TargetExistingID,
			Type:             domain.EdgeType(re.Type),
		})
	}
	return out, nil
}
