# MCP Registry — Feature Summary

## Backend

### REST API (`cmd/server`, port 8080)
- `GET /api/servers` — list all registered MCP servers
- `POST /api/servers` — register a new MCP server (name, endpoint, description, owner, authType, tags)
- `POST /api/servers/{id}/sync` — connect to a remote MCP server, fetch its tools via `tools/list`, cache them in DB

### MCP Hub Gateway (`cmd/hub`, port 8081)
- `POST /mcp` — MCP protocol endpoint (Streamable HTTP, JSON-RPC 2.0, protocol version 2025-03-26)
- Exposes two tools to MCP clients (e.g. Claude Code):
  - `discover_tools` — semantic vector search (pgvector cosine similarity) with ILIKE fallback
  - `call_tool` — proxy a tool call to the real MCP server (initialize session → notify → call)

### Semantic Search (`internal/embedding/`)
- Pluggable embedding providers via `Embedder` interface
  - **Ollama** (local, default) — `nomic-embed-text`, no API key required
  - **OpenAI** — `text-embedding-3-small`, requires `EMBEDDING_API_KEY`
- Composite embedding text: tool name (humanized) + description + parameter names
- Embeddings generated during `POST /api/servers/{id}/sync` and stored in pgvector
- Graceful degradation: if embedder is unavailable, falls back to ILIKE text search
- Config via env: `EMBEDDING_PROVIDER`, `EMBEDDING_URL`, `EMBEDDING_MODEL`, `EMBEDDING_DIMS`, `EMBEDDING_API_KEY`

### Authentication & Authorization (`internal/auth/`)
- **Keycloak** as IdP/Auth Server (docker-compose with pre-configured realm)
- JWT validation middleware — validates Bearer tokens against Keycloak JWKS
- JWKS caching with 5-minute TTL, automatic refresh
- **OAuth2 Token Exchange (RFC 8693)** — when Hub proxies `call_tool` to downstream MCP servers:
  - Extracts user's access token from context
  - Exchanges it via Keycloak for a service-specific token (audience = server name)
  - Passes exchanged token to downstream MCP server
- Reduces N×M auth config to N+M: agents auth to Hub, Hub exchanges to downstream services
- Graceful: auth can be disabled (`AUTH_ENABLED=false`) for local dev
- Config via env: `KEYCLOAK_URL`, `KEYCLOAK_REALM`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `AUTH_ENABLED`

### Architecture
- Clean architecture: entity → repository → usecase → transport
- All SQL queries live in `repository/` (ServerRepository, ToolRepository)
- Hub uses repository interfaces (`ServerRepo`, `ToolRepo`), no direct DB access

### Database (PostgreSQL 17 + pgvector)
- `servers` — id, name, endpoint, description, owner, auth_type, tags[], active, created_at
- `tools` — id, server_id (FK CASCADE), name, description, input_schema (JSONB), embedding vector(768), embedding_text, embedding_model, created_at; unique (server_id, name); HNSW index for vector search, GIN index for full-text search
- Migrations in `backend/migrations/`

### Infrastructure
- `docker-compose.yml` — PostgreSQL (pgvector/pgvector:pg17) + Keycloak 26.0, auto-migration via initdb.d
- Keycloak realm auto-imported with Hub client + test user
- Environment-based config with defaults (HTTP_PORT, HUB_PORT, DB_*, EMBEDDING_*, KEYCLOAK_*, AUTH_*)

## Frontend (React + Vite)

- Two-screen SPA: server list and registration form
- Components: ServerList, RegisterServer, SearchBar, FilterChips, StatusBadge, TagBadge, Button, InputField, SelectField
- Proxied API calls to backend (`/api/servers`)

## What's NOT implemented yet

- Role-based tool filtering (RBAC on discover_tools)
- Automatic tool sync (currently manual per-server)
- Server health checks / heartbeat
- Tool usage analytics
- Edit / delete server
- User accounts
