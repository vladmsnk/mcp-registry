# MCP Registry — Roadmap

Security-first roadmap aligned with [OWASP Non-Human Identities Top 10 (2025)](https://owasp.org/www-project-non-human-identities-top-10/).

## What's already done

| OWASP NHI | Status | Implementation |
|-----------|--------|----------------|
| NHI3 — Vulnerable Third-Party NHI | Solved | Hub is the single trust boundary — agents never get direct credentials to downstream services |
| NHI4 — Insecure Authentication | Covered | Keycloak OIDC, JWT validation (RS256 + JWKS), OAuth2 Token Exchange (RFC 8693) |
| NHI9 — NHI Reuse | Solved | Token Exchange creates per-service scoped tokens — each downstream gets its own token |

---

## Phase 1 — Audit & Access Control

Covers: **NHI5 (Overprivileged NHI)**, **NHI10 (Human Use of NHI)**

### 1.1 Audit Logging
- New `audit_log` table: timestamp, user_sub, username, action (discover/call), server_name, tool_name, success/failure, latency_ms
- Log every `discover_tools` and `call_tool` invocation
- Structured JSON log output for SIEM ingestion (Elasticsearch, Splunk, Loki)
- Distinguish human-delegated actions (via Token Exchange) from service-to-service calls

### 1.2 RBAC on Tool Discovery
- Add `allowed_roles TEXT[]` column to `tools` table
- Migration: `ALTER TABLE tools ADD COLUMN allowed_roles TEXT[]`
- Filter `discover_tools` results by user's `realm_access.roles` from JWT claims

- Admin API endpoint to set role requirements per tool/server
- Default: all tools visible (backward compatible)

### 1.3 Scope-Limited Token Exchange
- When exchanging tokens, request only the scopes the user's role allows
- Add `scope` parameter to Token Exchange request based on role mapping
- Reject overly broad scope requests at the Hub level

---

## Phase 2 — Secret Management & Lifecycle

Covers: **NHI2 (Secret Leakage)**, **NHI7 (Long-Lived Secrets)**

### 2.1 Vault Integration
- Support HashiCorp Vault (or Kubernetes secrets) as credential backend
- Hub's `client_secret` fetched from vault at startup, not env var
- New config: `SECRET_BACKEND=vault|env`, `VAULT_ADDR`, `VAULT_TOKEN`
- Vault path convention: `secret/mcp-registry/hub` for Hub credentials

### 2.2 Short-Lived Token Enforcement
- Validate that exchanged tokens have TTL ≤ configurable max (e.g. 5 min)
- Reject tokens with `exp` too far in the future
- Log warnings for long-lived tokens

### 2.3 Credential Rotation Support
- Hub client secret rotation without downtime (dual-secret window)
- API endpoint to trigger credential rotation: `POST /api/admin/rotate-credentials`
- Keycloak client secret update via Admin API

---

## Phase 3 — Lifecycle & Offboarding

Covers: **NHI1 (Improper Offboarding)**, **NHI8 (Environment Isolation)**

### 3.1 Server Health Checks
- Background goroutine: periodic MCP `ping` to all active servers
- Auto-set `active=false` after N consecutive failures (configurable, default 3)
- Re-activate on successful health check
- Health status visible in REST API and frontend

### 3.2 Automated Offboarding
- When a server is deactivated: revoke all cached tokens, clear synced tools
- Webhook notification on server deactivation (Slack, email)
- `DELETE /api/servers/{id}` endpoint with cascading cleanup
- Audit log entry for every deactivation/deletion

### 3.3 Environment Isolation
- Per-environment Keycloak realm configuration: `KEYCLOAK_REALM=mcp-registry-{env}`
- Validate token `iss` claim matches expected environment
- Reject cross-environment tokens (prod token can't access staging Hub)
- Separate database per environment (or schema-based isolation)

---

## Phase 4 — Supply Chain & Deployment Security

Covers: **NHI3 (Vulnerable Third-Party NHI)**, **NHI6 (Insecure Cloud Deployment)**

### 4.1 MCP Server Verification
- TLS certificate validation for all downstream MCP server connections
- Allowlist of permitted server endpoints (prevent SSRF via registration)
- Server fingerprinting: store and verify MCP server identity across syncs
- Alert on server identity change (name/version mismatch)

### 4.2 Registration Controls
- Admin-only server registration (require admin role in JWT)
- Approval workflow: registered servers start as `pending`, admin activates
- Rate limiting on registration and sync endpoints

### 4.3 Secure CI/CD
- No static credentials in CI/CD — use OIDC workload identity for deployments
- Container image signing and verification
- Pre-commit hooks for secret scanning in the repo

---

## Phase 5 — Observability & Analytics

Covers: **NHI5 (Overprivileged NHI)**, **NHI10 (Human Use of NHI)**

### 5.1 Usage Analytics
- Dashboard: most-called tools, most-active users, error rates per server
- Detect anomalies: unusual tool access patterns, off-hours usage, sudden spikes
- Identify overprivileged roles: roles that have access but never use certain tools

### 5.2 Permission Recommendations
- Analyze audit logs to suggest least-privilege role assignments
- Report: "User X has access to 50 tools but only uses 5"
- Automated quarterly access review reports

### 5.3 Token Exchange Monitoring
- Track exchange success/failure rates per downstream service
- Alert on repeated exchange failures (indicates misconfigured Keycloak policies)
- Monitor exchanged token TTLs and flag long-lived outliers

---

## Priority Order

| Priority | Phase | Key NHI Risks | Effort |
|----------|-------|---------------|--------|
| P0 | 1.1 Audit Logging | NHI10 | Small |
| P0 | 1.2 RBAC on Tools | NHI5 | Medium |
| P1 | 3.1 Health Checks | NHI1 | Small |
| P1 | 4.1 TLS + Server Verification | NHI3 | Medium |
| P1 | 3.3 Environment Isolation | NHI8 | Medium |
| P2 | 2.1 Vault Integration | NHI2 | Medium |
| P2 | 2.2 Short-Lived Token Enforcement | NHI7 | Small |
| P2 | 3.2 Automated Offboarding | NHI1 | Medium |
| P2 | 4.2 Registration Controls | NHI3, NHI6 | Small |
| P3 | 1.3 Scope-Limited Exchange | NHI5 | Medium |
| P3 | 2.3 Credential Rotation | NHI7 | Medium |
| P3 | 5.x Observability | NHI5, NHI10 | Large |
