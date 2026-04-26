package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

// NodeMetadata is the strict counterpart to the JSONB metadata column.
// Each NodeType has at most one concrete metadata struct; types with no
// type-specific fields (thought, idea, memory, dream, topic) leave the
// field nil.
//
// The interface is closed via the unexported nodeMetadataMarker — adding
// a new metadata struct is a deliberate code change, not a structural
// match by anyone outside this package. JSON encoding is provided by
// each concrete type; decoding goes through DecodeNodeMetadata, which
// dispatches on NodeType.
type NodeMetadata interface {
	// nodeMetadataMarker is unexported on purpose: third parties cannot
	// add their own NodeMetadata implementations.
	nodeMetadataMarker()
}

// PersonMetadata holds fields specific to person nodes.
//
// Relationship is a free-form string today ("friend", "mother",
// "colleague"). Once the product fixes a closed list it should become a
// dedicated enum type with Valid(), like OccurredAtPrecision.
type PersonMetadata struct {
	FirstName    string     `json:"first_name,omitempty"`
	LastName     string     `json:"last_name,omitempty"`
	Relationship string     `json:"relationship,omitempty"`
	Birthday     *time.Time `json:"birthday,omitempty"`
}

func (PersonMetadata) nodeMetadataMarker() {}

// PlaceMetadata holds fields specific to place nodes. Latitude /
// Longitude are pointers so we can distinguish "not provided" from
// "0,0" (which is a valid spot in the Atlantic Ocean).
type PlaceMetadata struct {
	Name      string   `json:"name,omitempty"`
	Country   string   `json:"country,omitempty"`
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
}

func (PlaceMetadata) nodeMetadataMarker() {}

// EventMetadata describes an event-container node (H15). DateRange.To
// equals DateRange.From for single-day events.
type EventMetadata struct {
	DateRange   *DateRange `json:"date_range,omitempty"`
	IsRecurring bool       `json:"is_recurring,omitempty"`
}

func (EventMetadata) nodeMetadataMarker() {}

// DateRange is a closed interval [From, To]. Both ends inclusive.
// Stored as an array in JSON to match the v0 schema example
// `"date_range": ["2026-04-15","2026-04-15"]`.
type DateRange struct {
	From time.Time
	To   time.Time
}

func (d DateRange) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]time.Time{d.From, d.To})
}

func (d *DateRange) UnmarshalJSON(data []byte) error {
	var arr [2]time.Time
	if err := json.Unmarshal(data, &arr); err != nil {
		return fmt.Errorf("date_range: %w", err)
	}
	d.From = arr[0]
	d.To = arr[1]
	return nil
}

// EmotionValence and EmotionIntensity are bounded floats. We do not
// hard-clamp on read (the LLM may produce 1.01); validation lives in
// the extraction usecase.
type EmotionMetadata struct {
	// Valence: -1.0 (negative) … +1.0 (positive).
	Valence *float64 `json:"valence,omitempty"`
	// Intensity: 0.0 … 1.0.
	Intensity *float64 `json:"intensity,omitempty"`
	// Label is free-form today ("joy", "anxiety", …). Candidate to
	// become a closed enum once the product picks the canonical list.
	Label string `json:"label,omitempty"`
}

func (EmotionMetadata) nodeMetadataMarker() {}

// TaskMetadata holds open-loop fields. DueDate may be nil for "someday".
type TaskMetadata struct {
	Completed bool       `json:"completed,omitempty"`
	DueDate   *time.Time `json:"due_date,omitempty"`
}

func (TaskMetadata) nodeMetadataMarker() {}

// jsonEmptyObject and jsonNull are the JSON literals we treat as
// "no metadata" on the read path. Centralised so the marshal/unmarshal
// halves agree on the same sentinel values.
const (
	jsonEmptyObject = "{}"
	jsonNull        = "null"
)

// EncodeNodeMetadata serialises any NodeMetadata to the JSONB bytes
// stored in the database. nil maps to the empty object `{}` so the
// column's NOT NULL DEFAULT '{}' constraint is satisfied without a
// special case at the SQL layer.
func EncodeNodeMetadata(m NodeMetadata) ([]byte, error) {
	if m == nil {
		return []byte(jsonEmptyObject), nil
	}
	return json.Marshal(m)
}

// DecodeNodeMetadata is the read-side counterpart. The dispatch on
// NodeType is what makes the field strongly typed at the call site:
// callers receive a concrete struct (PersonMetadata, …) wrapped in the
// NodeMetadata interface, not a free-form map.
//
// For node types that carry no metadata (thought, idea, memory, dream,
// topic) the function returns nil even if the column happened to have
// fields — extra fields are silently discarded. This is the right
// trade-off: the type schema is the source of truth, not the bytes.
func DecodeNodeMetadata(t NodeType, raw []byte) (NodeMetadata, error) {
	if len(raw) == 0 || string(raw) == jsonEmptyObject || string(raw) == jsonNull {
		return nil, nil
	}
	switch t {
	case NodePerson:
		var v PersonMetadata
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("decode person metadata: %w", err)
		}
		return v, nil
	case NodePlace:
		var v PlaceMetadata
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("decode place metadata: %w", err)
		}
		return v, nil
	case NodeEvent:
		var v EventMetadata
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("decode event metadata: %w", err)
		}
		return v, nil
	case NodeEmotion:
		var v EmotionMetadata
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("decode emotion metadata: %w", err)
		}
		return v, nil
	case NodeTask:
		var v TaskMetadata
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("decode task metadata: %w", err)
		}
		return v, nil
	default:
		// Types without dedicated metadata (thought, idea, memory,
		// dream, topic): ignore the bytes.
		return nil, nil
	}
}
