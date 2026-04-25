package rest

import (
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// conversationDTO is the JSON shape for a thread.
type conversationDTO struct {
	ID        string  `json:"id" format:"uuid"`
	Title     *string `json:"title,omitempty"`
	CreatedAt string  `json:"created_at" format:"date-time"`
	UpdatedAt string  `json:"updated_at" format:"date-time"`
}

func toConversationDTO(c domain.Conversation) conversationDTO {
	return conversationDTO{
		ID:        c.ID,
		Title:     c.Title,
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: c.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// messageDTO is the JSON shape for a single chat message.
type messageDTO struct {
	ID             string `json:"id" format:"uuid"`
	ConversationID string `json:"conversation_id" format:"uuid"`
	Role           string `json:"role" enum:"user,assistant,system"`
	Content        string `json:"content"`
	CreatedAt      string `json:"created_at" format:"date-time"`
}

func toMessageDTO(m domain.Message) messageDTO {
	return messageDTO{
		ID:             m.ID,
		ConversationID: m.ConversationID,
		Role:           string(m.Role),
		Content:        m.Content,
		CreatedAt:      m.CreatedAt.UTC().Format(time.RFC3339),
	}
}
