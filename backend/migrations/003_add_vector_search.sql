CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE tools
    ADD COLUMN IF NOT EXISTS embedding       vector(768),
    ADD COLUMN IF NOT EXISTS embedding_text   TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS embedding_model  TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_tools_embedding
    ON tools USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);
