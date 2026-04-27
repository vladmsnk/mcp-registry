-- TLS pin lifecycle (NHI #12): track when each leaf-cert fingerprint was captured
-- so operators can be warned about stale pins and trigger a re-pin with audit.
ALTER TABLE servers
    ADD COLUMN IF NOT EXISTS tls_cert_captured_at TIMESTAMPTZ;

-- Backfill: assume rows that already carry a non-empty pin were captured at row creation.
UPDATE servers
   SET tls_cert_captured_at = created_at
 WHERE tls_cert_sha256 <> ''
   AND tls_cert_captured_at IS NULL;
