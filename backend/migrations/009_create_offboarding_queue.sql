-- Durable retry queue for Keycloak offboarding ops (token revocation + client deletion).
-- The local server row is removed during DELETE /api/servers/{id}; the Keycloak side
-- is best-effort and may fail (network, KC down, transient 5xx). Failed ops are enqueued
-- here so a worker can retry until success, preventing orphaned KC clients.
--
-- op:
--   revoke_tokens — push notBefore=now to the client (RevokeAllTokens)
--   delete_client — remove the KC client (DeleteClient)
--
-- A "delete" attempt may enqueue both ops; the worker processes them in order so
-- revocation runs even if the row gets re-claimed before deletion.
CREATE TABLE IF NOT EXISTS offboarding_queue (
    id                   BIGSERIAL PRIMARY KEY,
    op                   TEXT NOT NULL CHECK (op IN ('revoke_tokens', 'delete_client')),
    keycloak_internal_id TEXT NOT NULL,
    keycloak_client_id   TEXT NOT NULL DEFAULT '',
    server_id            BIGINT,
    server_name          TEXT NOT NULL DEFAULT '',
    attempts             INTEGER NOT NULL DEFAULT 0,
    last_error           TEXT NOT NULL DEFAULT '',
    next_attempt_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at         TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_offboarding_queue_due
    ON offboarding_queue (next_attempt_at)
    WHERE completed_at IS NULL;
