CREATE TABLE IF NOT EXISTS tools (
    id           BIGSERIAL PRIMARY KEY,
    server_id    BIGINT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    input_schema JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (server_id, name)
);

CREATE INDEX IF NOT EXISTS idx_tools_search
    ON tools USING gin (to_tsvector('english', name || ' ' || description));
