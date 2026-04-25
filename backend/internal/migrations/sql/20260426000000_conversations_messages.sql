-- +goose Up

-- conversations: stateful chat threads, one user owns many.
-- updated_at is bumped whenever a new message is appended so that
-- listing "recent first" is a single index scan (idx_conversations_user).
CREATE TABLE conversations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_conversations_user ON conversations(user_id, updated_at DESC);

-- messages: enum role kept narrow to what the chat engine actually
-- emits — user, assistant, system. LLM/cost telemetry fields from the
-- v0 draft schema (llm_provider, audio_url, ...) are intentionally
-- omitted for MVP and will be added when extraction lands.
CREATE TYPE message_role AS ENUM ('user', 'assistant', 'system');

CREATE TABLE messages (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role message_role NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_messages_conv ON messages(conversation_id, created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_messages_conv;
DROP TABLE IF EXISTS messages;
DROP TYPE IF EXISTS message_role;
DROP INDEX IF EXISTS idx_conversations_user;
DROP TABLE IF EXISTS conversations;
