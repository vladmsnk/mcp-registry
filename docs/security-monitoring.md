# Security monitoring — SIEM contract & anomaly rules

This document describes the audit event stream produced by the registry and a
starter set of detection rules to apply on it (NHI #10).

## Event source

The hub and the API server emit one JSON line per audit event to `stdout`
via `audit.Logger`. Each line has:

| Field          | Type     | Notes                                            |
| -------------- | -------- | ------------------------------------------------ |
| `event`        | string   | constant `"audit"` — discriminator for SIEM      |
| `action`       | string   | see below                                        |
| `status`       | string   | `allowed` / `denied` / `error`                   |
| `actor_sub`    | string   | Keycloak `sub` claim                             |
| `actor_username` | string | Keycloak `preferred_username`                    |
| `actor_roles`  | []string | realm roles at request time                     |
| `server_id`    | int      | 0 when N/A                                       |
| `tool_name`    | string   | empty when N/A                                   |
| `latency_ms`   | int      | server-side processing                           |
| `request_id`   | string   | from `X-Request-ID`                              |
| `ip`           | string   | from `X-Forwarded-For` / `X-Real-IP` / RemoteAddr |
| `error`        | string   | only on non-OK statuses                          |
| (free-form)    | object   | additional fields via `Metadata`                 |

In Postgres the same payload is written to `audit_log`. Either source is
authoritative; pick whichever your SIEM ingests more cheaply.

### Actions

| Action                  | Meaning                                              |
| ----------------------- | ---------------------------------------------------- |
| `server.list`           | GET /api/servers                                     |
| `server.register`       | POST /api/servers                                    |
| `server.delete`         | DELETE /api/servers/{id} (also fired by retry worker) |
| `server.sync`           | POST /api/servers/{id}/sync                          |
| `server.health`         | manual probe                                         |
| `server.deactivated`    | health checker tripped                               |
| `server.reactivated`    | health checker recovered                             |
| `server.repin`          | TLS pin replaced (P3.12)                             |
| `tool.discover`         | hub `discover_tools`                                 |
| `tool.call`             | hub `call_tool`                                      |
| `tool.set_roles`        | PUT tool RBAC                                        |
| `auth.deny`             | `RequireRole` middleware rejected the request        |
| `token.exchange`        | RFC 8693 exchange (allowed/denied/error)             |
| `keycloak.admin_token`  | hub fetched/refreshed its admin token (P1.4)         |

## Anomaly rules

The rules below are written as PromQL/Sigma-flavoured pseudocode. Adjust the
windows, thresholds, and severity to fit your tenancy.

### A1 — Bulk delete burst
```
count(action == "server.delete" AND status == "allowed") by (actor_sub) > 5
within 5m
=> alert "actor X deleted N servers in 5m"
```
Why: a malicious admin or a stolen admin token will wipe registrations.

### A2 — Off-hours admin write
```
action in {server.register, server.delete, server.sync, tool.set_roles, server.repin}
AND status == "allowed"
AND hour_local(actor_sub) in [22..06]
=> page on first hit
```
Why: human-driven changes outside business hours are the strongest signal of
account compromise we can observe without behavioural baselines.

### A3 — Repeated tool denials
```
count(action == "tool.call" AND status == "denied") by (actor_sub) > 10
within 1m
=> alert "actor X enumerating tools they cannot call"
```
Use `metadata.required_roles` and `metadata.caller_roles` for the body of the
alert; together they show exactly which RBAC gap was probed.

### A4 — Token exchange to unexpected audience
```
action == "token.exchange" AND status == "denied"
AND metadata.audience !~ "^mcp-server-.+"
=> page (P0 — should be impossible)
```
The audience pattern is enforced in code; a `denied` row here means a caller
attempted to escape the audience binding. Investigate the request_id.

### A5 — Permission denied with mcp-admin role
```
action == "auth.deny"
AND "mcp-admin" in actor_roles
=> alert
```
Why: an mcp-admin should never be denied. A hit here means a route lost its
admin role config or someone inserted a stricter check without updating the
admin tier.

### A6 — Offboarding retry stuck
```
count(action == "server.delete"
      AND status == "error"
      AND metadata.phase == "queued_for_retry") by (server_id) > 3
within 30m
=> alert "Keycloak offboarding stuck for server N"
```
Why: a row that keeps failing to revoke means a real KC-side blocker
(network, IAM ACL change, or the prefix guard rejected an unmanaged client).

### A7 — Unusual admin-token cadence
```
rate(keycloak.admin_token, mode == "background") > 1 per minute
=> alert
```
Why: the 80%-TTL refresh fires at most once per token lifetime (default ~5m
for Keycloak). Sustained higher cadence implies something keeps invalidating
the cached token (clock skew, stolen token causing notBefore push, etc.).

### A8 — Stale TLS pin
Sourced from `/internal/metrics` (P3.15) rather than the audit stream:
```
GET /internal/metrics
.pins[] | select(.stale_warning) >= 1
=> ticket per server: "TLS pin >90 days old; run POST /api/servers/{id}/repin"
```

### A9 — Geo-unusual admin action
If your SIEM enriches `ip` with geoip, alert when:
```
action in {server.register, server.delete, server.sync, server.repin}
AND geo.country(ip) NOT IN org_country_allowlist
```

## Ingestion

- **Splunk / Elastic / Loki**: tail container `stdout`, index by `event=audit`.
- **Postgres-only**: `SELECT * FROM audit_log WHERE ts > now() - interval '5m'`
  is cheap (`idx_audit_log_ts`).
- **Per-actor lookups**: `idx_audit_log_actor_ts` makes
  `WHERE actor_sub = $1 ORDER BY ts DESC LIMIT 100` fast.

## Triage checklist

1. Pull the matching `request_id` from both hub and API server logs — they
   share the ID end-to-end when callers send `X-Request-ID`.
2. Correlate `actor_sub` against `keycloak.admin_token` events to confirm
   whether the token was refreshed near the suspicious activity.
3. For server.delete anomalies, check `offboarding_queue` for residual rows.
