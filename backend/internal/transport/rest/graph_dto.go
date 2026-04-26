package rest

import (
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// nodeDTO is the JSON shape for a single graph node. Pointer fields stay
// pointers so omitempty hides "the model didn't infer this" rather than
// surfacing the Go zero value (an empty string, the unix epoch).
//
// Metadata is `any` rather than a discriminated union because Go's JSON
// encoder already handles per-type metadata structs: the concrete struct
// (PersonMetadata, PlaceMetadata, …) carries its own `json:` tags, and
// nodes without type-specific fields (thought/idea/memory/dream/topic)
// emit nil — i.e. the field is absent from the response.
type nodeDTO struct {
	ID                  string  `json:"id" format:"uuid"`
	Type                string  `json:"type" enum:"thought,idea,memory,dream,emotion,task,event,person,place,topic"`
	Title               *string `json:"title,omitempty"`
	Content             *string `json:"content,omitempty"`
	Metadata            any     `json:"metadata,omitempty"`
	OccurredAt          *string `json:"occurred_at,omitempty" format:"date-time"`
	OccurredAtPrecision *string `json:"occurred_at_precision,omitempty" enum:"day,month,year"`
	Activation          float32 `json:"activation"`
	LastAccessedAt      string  `json:"last_accessed_at" format:"date-time"`
	Pinned              bool    `json:"pinned"`
	SourceMessageID     *string `json:"source_message_id,omitempty" format:"uuid"`
	CreatedAt           string  `json:"created_at" format:"date-time"`
	UpdatedAt           string  `json:"updated_at" format:"date-time"`
}

func toNodeDTO(n domain.Node) nodeDTO {
	dto := nodeDTO{
		ID:              n.ID,
		Type:            string(n.Type),
		Title:           n.Title,
		Content:         n.Content,
		Metadata:        n.Metadata,
		Activation:      n.Activation,
		LastAccessedAt:  n.LastAccessedAt.UTC().Format(time.RFC3339),
		Pinned:          n.Pinned,
		SourceMessageID: n.SourceMessageID,
		CreatedAt:       n.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:       n.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if n.OccurredAt != nil {
		s := n.OccurredAt.UTC().Format(time.RFC3339)
		dto.OccurredAt = &s
	}
	if n.OccurredAtPrecision != nil {
		s := string(*n.OccurredAtPrecision)
		dto.OccurredAtPrecision = &s
	}
	return dto
}

// edgeDTO is the JSON shape for a typed connection between two nodes.
// Source/Target are returned as plain string ids — the client already
// has the node objects so it can resolve them locally.
type edgeDTO struct {
	ID        string  `json:"id" format:"uuid"`
	SourceID  string  `json:"source_id" format:"uuid"`
	TargetID  string  `json:"target_id" format:"uuid"`
	Type      string  `json:"type" enum:"part_of,mentions,related_to,triggered_by,evolved_into,about"`
	Weight    float32 `json:"weight"`
	CreatedAt string  `json:"created_at" format:"date-time"`
}

func toEdgeDTO(e domain.Edge) edgeDTO {
	return edgeDTO{
		ID:        e.ID,
		SourceID:  e.SourceID,
		TargetID:  e.TargetID,
		Type:      string(e.Type),
		Weight:    e.Weight,
		CreatedAt: e.CreatedAt.UTC().Format(time.RFC3339),
	}
}
