CREATE TABLE IF NOT EXISTS audit_log (
    id              BIGSERIAL PRIMARY KEY,
    ts              TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_sub       TEXT NOT NULL DEFAULT '',
    actor_username  TEXT NOT NULL DEFAULT '',
    actor_roles     TEXT[] NOT NULL DEFAULT '{}',
    action          TEXT NOT NULL,
    status          TEXT NOT NULL,
    server_id       BIGINT,
    tool_name       TEXT NOT NULL DEFAULT '',
    latency_ms      INTEGER NOT NULL DEFAULT 0,
    request_id      TEXT NOT NULL DEFAULT '',
    ip              TEXT NOT NULL DEFAULT '',
    user_agent      TEXT NOT NULL DEFAULT '',
    error           TEXT NOT NULL DEFAULT '',
    metadata        JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_audit_log_ts ON audit_log (ts DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_actor_ts ON audit_log (actor_sub, ts DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_action_ts ON audit_log (action, ts DESC);
