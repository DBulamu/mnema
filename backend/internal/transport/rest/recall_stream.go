package rest

import (
	"context"
	"net/http"

	recalluc "github.com/DBulamu/mnema/backend/internal/usecase/recall"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"
)

// recallStreamRunner is the consumer-side port for the SSE handler.
// We declare it explicitly even though Recall.RunStream matches it
// structurally so the handler reads as a usecase contract, not a
// reach into a struct method.
type recallStreamRunner interface {
	RunStream(ctx context.Context, in recalluc.Input, emit func(recalluc.StreamEvent) error) error
}

// recallStreamInput mirrors recallInput; we keep them as separate types
// so a future divergence (e.g. ?include= flags only valid on stream)
// stays local to one file.
type recallStreamInput struct {
	Body struct {
		Text string `json:"text" minLength:"1" maxLength:"2000" doc:"Free-form recall query."`
		Lang string `json:"lang,omitempty" maxLength:"16" doc:"Two-letter or BCP-47 tag. Defaults to ru."`
	}
}

// SSE event payloads. They mirror the recall.StreamEvent variants but
// flatten into separate Go types so huma's sse.Register can map each
// to a distinct `event:` line.
//
// Field shapes are intentionally narrow: the wire is a UI contract,
// not a debugging dump. Anything the client doesn't render (e.g. raw
// candidate counts) is omitted.

type recallMetaEvent struct {
	Lang       string `json:"lang"`
	NumAnchors int    `json:"num_anchors"`
	NumCands   int    `json:"num_candidates"`
}

type recallCandidatesEvent struct {
	Nodes []nodeDTO `json:"nodes"`
}

type recallDeltaEvent struct {
	Text string `json:"text"`
}

type recallFinalEvent struct {
	Answer string          `json:"answer"`
	Spans  []recallSpanDTO `json:"spans"`
	Nodes  []nodeDTO       `json:"nodes"`
}

type recallErrorEvent struct {
	Message string `json:"message"`
}

// RegisterRecallStream wires POST /v1/recall/stream as an SSE endpoint.
// The event sequence is:
//
//	meta       — {lang, num_anchors, num_candidates}, exactly once.
//	candidates — full candidate node list, exactly once. Sent before
//	             the answer step starts so the UI can render cards
//	             while the LLM is still running.
//	delta      — zero or more answer fragments. Concatenation of all
//	             delta.text values equals the final answer string up
//	             to validation truncation.
//	final      — {answer, spans, nodes}, exactly once. The
//	             authoritative output; the UI replaces accumulated
//	             delta text with answer and renders spans.
//	error      — at most once, terminates the stream.
//
// We keep the synchronous /v1/recall endpoint alive: SSE adds
// complexity on the client and the API is sometimes called from
// non-browser contexts (curl, scripts) where one POST → one JSON
// response is the simpler contract.
func RegisterRecallStream(api huma.API, run recallStreamRunner) {
	sse.Register(api, huma.Operation{
		OperationID: "recall-stream",
		Method:      http.MethodPost,
		Path:        "/v1/recall/stream",
		Summary:     "Recall from the user's graph (streaming).",
		Tags:        []string{"recall"},
		Security:    []map[string][]string{{BearerSecurityName: {}}},
	}, map[string]any{
		"meta":       recallMetaEvent{},
		"candidates": recallCandidatesEvent{},
		"delta":      recallDeltaEvent{},
		"final":      recallFinalEvent{},
		"error":      recallErrorEvent{},
	}, func(ctx context.Context, in *recallStreamInput, send sse.Sender) {
		userID := UserIDFromContext(ctx)
		if userID == "" {
			_ = send.Data(recallErrorEvent{Message: "unauthenticated"})
			return
		}

		err := run.RunStream(ctx, recalluc.Input{
			UserID: userID,
			Text:   in.Body.Text,
			Lang:   in.Body.Lang,
		}, func(ev recalluc.StreamEvent) error {
			switch {
			case ev.Meta != nil:
				return send.Data(recallMetaEvent{
					Lang:       ev.Meta.Lang,
					NumAnchors: ev.Meta.NumAnchors,
					NumCands:   ev.Meta.NumCands,
				})
			case ev.Candidates != nil:
				out := make([]nodeDTO, 0, len(ev.Candidates.Nodes))
				for _, n := range ev.Candidates.Nodes {
					out = append(out, toNodeDTO(n))
				}
				return send.Data(recallCandidatesEvent{Nodes: out})
			case ev.Delta != nil:
				return send.Data(recallDeltaEvent{Text: ev.Delta.Text})
			case ev.Final != nil:
				spans := make([]recallSpanDTO, 0, len(ev.Final.Spans))
				for _, s := range ev.Final.Spans {
					spans = append(spans, recallSpanDTO{Start: s.Start, End: s.End, NodeIDs: s.NodeIDs})
				}
				nodes := make([]nodeDTO, 0, len(ev.Final.Nodes))
				for _, n := range ev.Final.Nodes {
					nodes = append(nodes, toNodeDTO(n))
				}
				return send.Data(recallFinalEvent{
					Answer: ev.Final.Answer,
					Spans:  spans,
					Nodes:  nodes,
				})
			}
			return nil
		})
		if err != nil {
			_ = send.Data(recallErrorEvent{Message: err.Error()})
		}
	})
}
