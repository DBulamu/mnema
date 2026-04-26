package rest

import (
	"context"
	"net/http"

	recalluc "github.com/DBulamu/mnema/backend/internal/usecase/recall"
	"github.com/danielgtaylor/huma/v2"
)

// recallRunner is the consumer-side port for the handler. The recall
// usecase satisfies it structurally — same pattern as the graph
// handlers.
type recallRunner interface {
	Run(ctx context.Context, in recalluc.Input) (recalluc.Output, error)
}

type recallInput struct {
	Body struct {
		Text string `json:"text" minLength:"1" maxLength:"2000" doc:"Free-form recall query."`
		Lang string `json:"lang,omitempty" maxLength:"16" doc:"Two-letter or BCP-47 tag. Defaults to ru."`
	}
}

// recallSpanDTO mirrors recall.Span. Offsets are RUNE indices into
// answer (Unicode code points), not bytes and not UTF-16 code units.
// See wiki/recall-mechanics.md for the rationale.
type recallSpanDTO struct {
	Start   int      `json:"start" minimum:"0" doc:"Rune offset (inclusive)."`
	End     int      `json:"end" minimum:"0" doc:"Rune offset (exclusive)."`
	NodeIDs []string `json:"node_ids"`
}

type recallOutput struct {
	Body struct {
		Answer string          `json:"answer"`
		Spans  []recallSpanDTO `json:"spans"`
		Nodes  []nodeDTO       `json:"nodes"`
		Lang   string          `json:"lang"`
	}
}

// RegisterRecall wires POST /v1/recall. Synchronous (no SSE on the
// first pass — see Phase 4.5 in the roadmap). Empty Spans is a valid
// response: it means the model could not anchor any part of the
// answer to a candidate node, and the UX falls back to the answer
// alone.
func RegisterRecall(api huma.API, run recallRunner) {
	huma.Register(api, huma.Operation{
		OperationID: "recall-post",
		Method:      http.MethodPost,
		Path:        "/v1/recall",
		Summary:     "Recall from the user's graph (free-form text → answer with span attribution)",
		Tags:        []string{"recall"},
		Security:    []map[string][]string{{BearerSecurityName: {}}},
	}, func(ctx context.Context, in *recallInput) (*recallOutput, error) {
		userID := UserIDFromContext(ctx)
		if userID == "" {
			return nil, toHTTP(errUnauthenticated)
		}

		res, err := run.Run(ctx, recalluc.Input{
			UserID: userID,
			Text:   in.Body.Text,
			Lang:   in.Body.Lang,
		})
		if err != nil {
			return nil, toHTTP(err)
		}

		out := &recallOutput{}
		out.Body.Answer = res.Answer
		out.Body.Lang = res.Lang
		out.Body.Spans = make([]recallSpanDTO, 0, len(res.Spans))
		for _, s := range res.Spans {
			out.Body.Spans = append(out.Body.Spans, recallSpanDTO{
				Start:   s.Start,
				End:     s.End,
				NodeIDs: s.NodeIDs,
			})
		}
		out.Body.Nodes = make([]nodeDTO, 0, len(res.Nodes))
		for _, n := range res.Nodes {
			out.Body.Nodes = append(out.Body.Nodes, toNodeDTO(n))
		}
		return out, nil
	})
}
