-- +goose Up

-- nodes: единая таблица для всех типов узлов графа.
-- Решение хранить всё в одной таблице с дискриминатором `type` —
-- из glossary.md и db-schema.md: мысль может эволюционировать в
-- идею/цель/дело, и одна таблица делает такой переход тривиальным
-- (UPDATE type), а не межтабличной миграцией.
CREATE TYPE node_type AS ENUM (
    'thought',
    'idea',
    'memory',
    'dream',
    'emotion',
    'task',
    'event',
    'person',
    'place',
    'topic'
);

CREATE TABLE nodes (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type node_type NOT NULL,

    title TEXT,
    content TEXT,
    metadata JSONB NOT NULL DEFAULT '{}',

    -- Timeline (H16): occurred_at NULL для вечных сущностей (мечты, принципы, человек, тема).
    -- precision хранится строкой — 'day' | 'month' | 'year' — потому что иногда
    -- пользователь помнит только год, и UI должен показать "2024", а не "1 января 2024 00:00".
    occurred_at TIMESTAMPTZ,
    occurred_at_precision TEXT CHECK (occurred_at_precision IN ('day', 'month', 'year') OR occurred_at_precision IS NULL),

    -- Decay (H11, H18). activation 0..1, last_accessed_at бампается при revival.
    -- pinned=true исключает узел из decay-расчёта.
    activation REAL NOT NULL DEFAULT 1.0 CHECK (activation >= 0 AND activation <= 1),
    last_accessed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    pinned BOOLEAN NOT NULL DEFAULT false,

    -- Provenance: какое сообщение породило этот узел. NULL для узлов созданных вручную.
    source_message_id UUID REFERENCES messages(id) ON DELETE SET NULL,

    -- Embeddings — поле зарезервировано, генерация в отдельном шаге roadmap.
    -- Размерность 1536 = text-embedding-3-small. При смене модели с другим dim
    -- нужна re-embed миграция.
    embedding VECTOR(1536),
    embedding_model TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Soft-delete для биографии (H7): удалённое из активного слоя
    -- остаётся доступно для финального артефакта.
    deleted_at TIMESTAMPTZ
);

-- "Чьи узлы определённого типа" — частый запрос (списки людей, событий).
CREATE INDEX idx_nodes_user_type ON nodes(user_id, type) WHERE deleted_at IS NULL;
-- Timeline-запросы: узлы пользователя в диапазоне времени.
CREATE INDEX idx_nodes_user_timeline ON nodes(user_id, occurred_at) WHERE deleted_at IS NULL AND occurred_at IS NOT NULL;
-- Активный слой памяти: узлы по убыванию activation.
CREATE INDEX idx_nodes_user_activation ON nodes(user_id, activation DESC) WHERE deleted_at IS NULL;
-- Семантический поиск (revival по embedding). ivfflat — для < 1M векторов.
CREATE INDEX idx_nodes_embedding ON nodes USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
-- Текстовый поиск по содержимому (trigram).
CREATE INDEX idx_nodes_content_trgm ON nodes USING gin (content gin_trgm_ops);

-- edges: типизированные связи между узлами (H17).
-- 6 типов из glossary.md. Тип ставит AI при extraction, пользователь не выбирает.
CREATE TYPE edge_type AS ENUM (
    'part_of',
    'mentions',
    'related_to',
    'triggered_by',
    'evolved_into',
    'about'
);

CREATE TABLE edges (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    -- user_id денормализован для быстрых "все мои связи" запросов
    -- без двойного JOIN на nodes.
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_id UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    target_id UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    type edge_type NOT NULL DEFAULT 'related_to',

    -- decay для связей (H11): редкие связи затухают независимо от узлов.
    weight REAL NOT NULL DEFAULT 1.0 CHECK (weight >= 0 AND weight <= 1),

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,

    -- Защита от дубликатов: одна связь данного типа между двумя узлами.
    -- Если AI повторно извлечёт ту же пару — UPSERT на (source_id, target_id, type).
    UNIQUE (source_id, target_id, type),
    -- Граф направленный, петли запрещены.
    CHECK (source_id <> target_id)
);

CREATE INDEX idx_edges_source ON edges(source_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_edges_target ON edges(target_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_edges_user ON edges(user_id) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_edges_user;
DROP INDEX IF EXISTS idx_edges_target;
DROP INDEX IF EXISTS idx_edges_source;
DROP TABLE IF EXISTS edges;
DROP TYPE IF EXISTS edge_type;
DROP INDEX IF EXISTS idx_nodes_content_trgm;
DROP INDEX IF EXISTS idx_nodes_embedding;
DROP INDEX IF EXISTS idx_nodes_user_activation;
DROP INDEX IF EXISTS idx_nodes_user_timeline;
DROP INDEX IF EXISTS idx_nodes_user_type;
DROP TABLE IF EXISTS nodes;
DROP TYPE IF EXISTS node_type;
