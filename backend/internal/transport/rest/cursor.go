package rest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// Cursors are base64url(JSON). The encoding is opaque from the
// client's perspective — a client that decodes and tweaks a cursor
// gets undefined behavior. base64url is URL-safe (no '+' or '/'); we
// strip padding to keep the wire format compact.

// conversationCursorJSON is the on-wire shape of a conversations cursor.
// Field names are abbreviated to keep the encoded string short — these
// run in URLs.
type conversationCursorJSON struct {
	U time.Time `json:"u"`
	I string    `json:"i"`
}

func encodeConversationCursor(c *domain.ConversationCursor) string {
	if c == nil {
		return ""
	}
	b, _ := json.Marshal(conversationCursorJSON{U: c.UpdatedAt.UTC(), I: c.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeConversationCursor returns nil when s is empty (first page).
// Any non-empty malformed input is a client error — the caller maps it
// to errInvalidArgument so the API answers 400 instead of silently
// returning the first page.
func decodeConversationCursor(s string) (*domain.ConversationCursor, error) {
	if s == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid cursor encoding", domain.ErrInvalidArgument)
	}
	var j conversationCursorJSON
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("%w: invalid cursor payload", domain.ErrInvalidArgument)
	}
	if j.I == "" || j.U.IsZero() {
		return nil, fmt.Errorf("%w: cursor missing fields", domain.ErrInvalidArgument)
	}
	return &domain.ConversationCursor{UpdatedAt: j.U, ID: j.I}, nil
}

type messageCursorJSON struct {
	C time.Time `json:"c"`
	I string    `json:"i"`
}

func encodeMessageCursor(c *domain.MessageCursor) string {
	if c == nil {
		return ""
	}
	b, _ := json.Marshal(messageCursorJSON{C: c.CreatedAt.UTC(), I: c.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeMessageCursor(s string) (*domain.MessageCursor, error) {
	if s == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid cursor encoding", domain.ErrInvalidArgument)
	}
	var j messageCursorJSON
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("%w: invalid cursor payload", domain.ErrInvalidArgument)
	}
	if j.I == "" || j.C.IsZero() {
		return nil, fmt.Errorf("%w: cursor missing fields", domain.ErrInvalidArgument)
	}
	return &domain.MessageCursor{CreatedAt: j.C, ID: j.I}, nil
}
