package domain

import "time"

// NodeType is the discriminator for the single nodes table. Mirrors the
// node_type Postgres enum 1:1 — the SQL migration is the source of truth.
//
// The list is closed: a fixed taxonomy lets the LLM extractor target a
// known set, and the UI render type-specific affordances. Adding a type
// is a migration + code change, not a config switch.
type NodeType string

const (
	NodeThought NodeType = "thought"
	NodeIdea    NodeType = "idea"
	NodeMemory  NodeType = "memory"
	NodeDream   NodeType = "dream"
	NodeEmotion NodeType = "emotion"
	NodeTask    NodeType = "task"
	NodeEvent   NodeType = "event"
	NodePerson  NodeType = "person"
	NodePlace   NodeType = "place"
	NodeTopic   NodeType = "topic"
	// NodeTime is a time-period anchor — "2025", "2025-03", "2025-03-12".
	// Built deterministically by the extraction usecase from any node's
	// occurred_at, never proposed by the LLM. Connects regular nodes via
	// part_of edges so the graph carries a navigable time axis.
	NodeTime NodeType = "time"
)

// Valid reports whether t is one of the known node types. Used to
// reject extractor output and client input before we ever reach SQL.
func (t NodeType) Valid() bool {
	switch t {
	case NodeThought, NodeIdea, NodeMemory, NodeDream, NodeEmotion,
		NodeTask, NodeEvent, NodePerson, NodePlace, NodeTopic, NodeTime:
		return true
	}
	return false
}

// OccurredAtPrecision is the granularity of a node's occurred_at —
// some memories are dated to the day, others only to a year. We do not
// fabricate a fake day-precision when only the year is known.
type OccurredAtPrecision string

const (
	PrecisionDay   OccurredAtPrecision = "day"
	PrecisionMonth OccurredAtPrecision = "month"
	PrecisionYear  OccurredAtPrecision = "year"
)

// Valid reports whether p is one of the supported precisions.
func (p OccurredAtPrecision) Valid() bool {
	switch p {
	case PrecisionDay, PrecisionMonth, PrecisionYear:
		return true
	}
	return false
}

// Node is one entry in the user's life graph. Title and content are both
// optional individually but at least one must be set — enforced at the
// usecase layer, not at the DB, because the requirement is product-level
// (a node with no title and no content has no surface in the UI).
//
// Activation, LastAccessedAt, Pinned drive the decay system (H11). They
// are populated by the adapter on read and updated by revival/touch
// usecases — extraction creates nodes at activation=1.0.
type Node struct {
	ID       string
	UserID   string
	Type     NodeType
	Title   *string
	Content *string
	// Metadata holds type-specific fields. Concrete type depends on
	// Type: PersonMetadata for NodePerson, PlaceMetadata for NodePlace,
	// EventMetadata for NodeEvent, EmotionMetadata for NodeEmotion,
	// TaskMetadata for NodeTask, nil for the rest. The closed
	// NodeMetadata interface keeps the discriminator honest — callers
	// type-assert into the concrete struct, not into a free-form map.
	Metadata NodeMetadata

	OccurredAt          *time.Time
	OccurredAtPrecision *OccurredAtPrecision

	Activation     float32
	LastAccessedAt time.Time
	Pinned         bool

	// SourceMessageID points at the chat message from which the node was
	// extracted. NULL for hand-created nodes (none today, but the schema
	// is open).
	SourceMessageID *string

	// ImageURL is an optional picture displayed on the node in the graph
	// view: when set the node renders as a circular photo, otherwise as
	// a colored circle by type (H14). Held as a plain URL because a real
	// upload pipeline (S3 presigned + thumbnails) lives in Phase 5; for
	// now seed data and externally-hosted images are the only writers.
	ImageURL *string

	CreatedAt time.Time
	UpdatedAt time.Time
}
