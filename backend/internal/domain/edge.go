package domain

import "time"

// EdgeType is the discriminator for the typed edges between nodes (H17).
// Mirrors the edge_type Postgres enum 1:1. Closed taxonomy on purpose —
// the AI classifies into one of these, the user does not invent new types.
type EdgeType string

const (
	// EdgePartOf — node belongs to a containing event (H15).
	EdgePartOf EdgeType = "part_of"
	// EdgeMentions — text explicitly references another node.
	EdgeMentions EdgeType = "mentions"
	// EdgeRelatedTo — generic association, the default when nothing
	// more specific applies.
	EdgeRelatedTo EdgeType = "related_to"
	// EdgeTriggeredBy — node was caused by another (idea triggered by thought).
	EdgeTriggeredBy EdgeType = "triggered_by"
	// EdgeEvolvedInto — node grew out of another (thought → goal).
	EdgeEvolvedInto EdgeType = "evolved_into"
	// EdgeAbout — node is about a person/place/topic.
	EdgeAbout EdgeType = "about"
)

// Valid reports whether t is one of the six supported edge types.
func (t EdgeType) Valid() bool {
	switch t {
	case EdgePartOf, EdgeMentions, EdgeRelatedTo,
		EdgeTriggeredBy, EdgeEvolvedInto, EdgeAbout:
		return true
	}
	return false
}

// Edge is a directed, typed relationship between two nodes of the same
// user. Self-loops are rejected at the SQL layer (CHECK constraint).
type Edge struct {
	ID        string
	UserID    string
	SourceID  string
	TargetID  string
	Type      EdgeType
	Weight    float32
	CreatedAt time.Time
}
