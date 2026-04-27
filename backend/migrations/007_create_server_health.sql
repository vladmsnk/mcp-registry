CREATE TABLE IF NOT EXISTS server_health (
    server_id            BIGINT PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    last_check_at        TIMESTAMPTZ,
    last_success_at      TIMESTAMPTZ,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_error           TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_server_health_failures ON server_health (consecutive_failures);
