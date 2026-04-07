# MCP Registry

Enterprise registry for MCP servers with Keycloak-based auth and semantic tool search.

## Quick Start

```bash
docker-compose up -d          # Postgres + Keycloak
cd backend && go run ./cmd/server  # Main server on :8080
```

Auth is controlled by `AUTH_ENABLED` env var (default `true`). Set to `false` for local dev without Keycloak.

## Project Structure

```
backend/
  cmd/server/         # Main API server entrypoint
  internal/
    auth/             # JWT validation middleware, token context helpers
    config/           # Env-based config (Config struct, envOrDefault)
    embedding/        # Vector embedding providers (Ollama, OpenAI)
    entity/           # Domain entities: Server
    hub/              # MCP hub: tool discovery, remote tool calls, token exchange
    keycloak/         # Keycloak admin client (DCR, client management)
    repository/       # Postgres repositories (servers, tools)
    transport/http/   # HTTP handlers (REST API)
    usecase/          # Business logic layer
  migrations/         # SQL migrations (auto-applied via docker-entrypoint-initdb.d)
src/                  # React frontend (Vite + JSX)
  components/         # UI: ServerList, ServerDetail, RegisterServer
```

## Architecture Conventions

- **Clean architecture**: entity -> usecase -> repository -> transport. Dependencies point inward.
- **Interfaces defined at consumer**: e.g. `usecase.ServerRepo` is defined in the usecase package, implemented by repository.
- **Config**: All config via env vars, loaded in `config.Load()`. No config files.
- **DB**: Raw `database/sql` with `lib/pq`. No ORM. Use `pq.Array()` for Postgres arrays.
- **HTTP routing**: Go 1.22+ `http.ServeMux` with method patterns (e.g. `"GET /api/servers"`).
- **Error responses**: Always JSON via `writeJSON`/`writeError` helpers in `transport/http/`.
- **Auth**: Keycloak JWT validation middleware wraps the entire mux. Token exchange for downstream MCP servers.

## Key API Endpoints

### MCP Server Registry
- `GET /api/servers` — list registered MCP servers
- `POST /api/servers` — register server (auto-provisions Keycloak client if auth enabled)
- `DELETE /api/servers/{id}` — delete server (cleans up Keycloak client)
- `POST /api/servers/{id}/sync` — sync tools from remote MCP server

## Frontend

React + Vite. Single-page MCP server management UI.

## Code Style

- Go: standard library preferred, minimal dependencies
- No ORM, no framework — just `net/http` and `database/sql`
- JSON tags use `snake_case` for API compatibility
- Entity fields use `json:"-"` for secrets (e.g. `APIKey`)
