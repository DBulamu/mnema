package domain

import "time"

// MessageRole is the speaker behind a chat message. Mirrors the
// message_role Postgres enum 1:1 — keep the constants and the SQL enum
// in sync; a mismatch surfaces only at runtime via the pgx scanner.
type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleSystem    MessageRole = "system"
)

// Valid reports whether r is one of the three known roles. Used by the
// transport layer when deserializing client input — we never trust an
// arbitrary string into the DB.
func (r MessageRole) Valid() bool {
	switch r {
	case RoleUser, RoleAssistant, RoleSystem:
		return true
	}
	return false
}

// Conversation is a chat thread owned by a user. Title is optional —
// MVP leaves it blank, an LLM may auto-name it later.
type Conversation struct {
	ID        string
	UserID    string
	Title     *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Message is a single utterance inside a conversation. Content is
// always plaintext for MVP; audio and tool-call metadata come later.
type Message struct {
	ID             string
	ConversationID string
	Role           MessageRole
	Content        string
	CreatedAt      time.Time
}
