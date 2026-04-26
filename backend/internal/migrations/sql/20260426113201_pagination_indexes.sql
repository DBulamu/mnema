-- +goose Up

-- Keyset pagination on (updated_at DESC, id DESC) needs an index that
-- carries the tiebreaker; the prior idx_conversations_user
-- (user_id, updated_at DESC) was correct for plain LIMIT but cannot
-- resolve row-comparison ((updated_at, id) < (..., ...)) cheaply when
-- many rows share an updated_at.
DROP INDEX IF EXISTS idx_conversations_user;
CREATE INDEX idx_conversations_user
    ON conversations(user_id, updated_at DESC, id DESC);

-- Same reasoning for messages: keyset is (created_at ASC, id ASC) for
-- "load older" semantics. Old idx_messages_conv lacked the id column.
DROP INDEX IF EXISTS idx_messages_conv;
CREATE INDEX idx_messages_conv
    ON messages(conversation_id, created_at, id);

-- +goose Down
DROP INDEX IF EXISTS idx_messages_conv;
CREATE INDEX idx_messages_conv ON messages(conversation_id, created_at);

DROP INDEX IF EXISTS idx_conversations_user;
CREATE INDEX idx_conversations_user ON conversations(user_id, updated_at DESC);
