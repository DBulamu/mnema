-- +goose Up
CREATE TABLE auth_magic_links (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email TEXT NOT NULL,
    token_hash TEXT UNIQUE NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    ip_address INET,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_magic_links_token ON auth_magic_links(token_hash) WHERE consumed_at IS NULL;
CREATE INDEX idx_magic_links_email ON auth_magic_links(email);

-- +goose Down
DROP INDEX IF EXISTS idx_magic_links_email;
DROP INDEX IF EXISTS idx_magic_links_token;
DROP TABLE IF EXISTS auth_magic_links;
