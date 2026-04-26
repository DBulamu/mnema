package domain

import "time"

// ConversationCursor positions a keyset page on (updated_at DESC, id DESC).
// The id tiebreaker keeps the order total when many threads share an
// updated_at timestamp (e.g. backfills with a single now()).
type ConversationCursor struct {
	UpdatedAt time.Time
	ID        string
}

// MessageCursor positions a keyset page on (created_at ASC, id ASC) for
// "load older" semantics. The cursor points at the oldest row already
// shown to the client; the next page contains rows strictly older than
// it.
type MessageCursor struct {
	CreatedAt time.Time
	ID        string
}
