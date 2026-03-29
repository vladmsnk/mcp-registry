#!/usr/bin/env bash
set -euo pipefail

# E2E test for MCP Registry: auth → register → sync → discover → call
#
# Prerequisites:
#   docker compose up -d   (postgres + keycloak)
#   Wait ~30s for Keycloak to start and import the realm
#
# This script starts the test MCP server, REST API, and Hub, runs the full
# flow, then cleans up.

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

PIDS=()
cleanup() {
    echo -e "\n${YELLOW}Cleaning up...${NC}"
    for pid in "${PIDS[@]+"${PIDS[@]}"}"; do
        kill "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
    done
}
trap cleanup EXIT

pass() { echo -e "${GREEN}✓ $1${NC}"; }
fail() { echo -e "${RED}✗ $1${NC}"; echo "$2"; exit 1; }

cd "$(dirname "$0")"

# ── Build ──────────────────────────────────────────────
echo -e "${YELLOW}Building binaries...${NC}"
(cd backend && go build -o ../bin/server ./cmd/server)
(cd backend && go build -o ../bin/hub ./cmd/hub)
(cd backend && go build -o ../bin/testmcp ./cmd/testmcp)
pass "Binaries built"

# ── Check infra ────────────────────────────────────────
echo -e "\n${YELLOW}Checking infrastructure...${NC}"

# Postgres — simple TCP check
if ! (echo > /dev/tcp/localhost/6521) 2>/dev/null; then
    fail "PostgreSQL not running on port 6521" "Run: docker compose up -d"
fi
pass "PostgreSQL is running"

# Keycloak
KC_URL="http://localhost:8180"
if ! curl -sf "${KC_URL}/realms/mcp-registry/.well-known/openid-configuration" >/dev/null 2>&1; then
    fail "Keycloak not ready" "Run: docker compose up -d  (wait ~30s for startup)"
fi
pass "Keycloak is running"

# ── Start services ─────────────────────────────────────
echo -e "\n${YELLOW}Starting services...${NC}"

AUTH_ENABLED=false ./bin/server &
PIDS+=($!)

AUTH_ENABLED=false EMBEDDING_PROVIDER=ollama ./bin/hub &
PIDS+=($!)

./bin/testmcp &
PIDS+=($!)

sleep 2
pass "Services started (REST :8080, Hub :8081, TestMCP :9090)"

# ── Step 1: Get token from Keycloak ───────────────────
echo -e "\n${YELLOW}Step 1: Authenticate with Keycloak...${NC}"

TOKEN_RESPONSE=$(curl -sf -X POST \
    "${KC_URL}/realms/mcp-registry/protocol/openid-connect/token" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "grant_type=password" \
    -d "client_id=mcp-hub" \
    -d "client_secret=mcp-hub-secret" \
    -d "username=testuser" \
    -d "password=testpass" 2>&1) || fail "Token request failed" "$TOKEN_RESPONSE"

ACCESS_TOKEN=$(echo "$TOKEN_RESPONSE" | jq -r '.access_token')
if [ -z "$ACCESS_TOKEN" ] || [ "$ACCESS_TOKEN" = "null" ]; then
    fail "No access_token in response" "$TOKEN_RESPONSE"
fi
pass "Got access token (${#ACCESS_TOKEN} chars)"

# ── Step 2: Register test MCP server ──────────────────
echo -e "\n${YELLOW}Step 2: Register test MCP server...${NC}"

REGISTER_RESPONSE=$(curl -sf -X POST http://localhost:8080/api/servers \
    -H "Content-Type: application/json" \
    -d '{
        "name": "testmcp",
        "endpoint": "http://localhost:9090/mcp",
        "description": "Test MCP server for e2e testing",
        "owner": "test",
        "authType": "None",
        "tags": ["test", "echo", "math"]
    }' 2>&1) || fail "Register failed" "$REGISTER_RESPONSE"

SERVER_ID=$(echo "$REGISTER_RESPONSE" | jq -r '.id')
if [ -z "$SERVER_ID" ] || [ "$SERVER_ID" = "null" ]; then
    fail "No server ID in response" "$REGISTER_RESPONSE"
fi
pass "Server registered (id=$SERVER_ID)"

# ── Step 3: Sync tools ────────────────────────────────
echo -e "\n${YELLOW}Step 3: Sync tools from test server...${NC}"

SYNC_RESPONSE=$(curl -sf -X POST "http://localhost:8080/api/servers/${SERVER_ID}/sync" 2>&1) \
    || fail "Sync failed" "$SYNC_RESPONSE"

SYNCED=$(echo "$SYNC_RESPONSE" | jq -r '.synced')
if [ "$SYNCED" != "2" ]; then
    fail "Expected 2 synced tools, got $SYNCED" "$SYNC_RESPONSE"
fi
pass "Synced $SYNCED tools (echo, add_numbers)"

# ── Step 4: MCP initialize (with auth) ────────────────
echo -e "\n${YELLOW}Step 4: MCP initialize (Hub, with token)...${NC}"

# First test that Hub returns 401 without token when auth is enabled.
# (We started with AUTH_ENABLED=false, so skip 401 test and just initialize.)

INIT_RESPONSE=$(curl -sf -X POST http://localhost:8081/mcp \
    -H "Content-Type: application/json" \
    -d '{
        "jsonrpc": "2.0",
        "id": 1,
        "method": "initialize",
        "params": {
            "protocolVersion": "2025-03-26",
            "capabilities": {},
            "clientInfo": {"name": "e2e-test", "version": "1.0.0"}
        }
    }' 2>&1) || fail "MCP initialize failed" "$INIT_RESPONSE"

HUB_NAME=$(echo "$INIT_RESPONSE" | jq -r '.result.serverInfo.name')
if [ "$HUB_NAME" != "mcp-registry-hub" ]; then
    fail "Unexpected server name: $HUB_NAME" "$INIT_RESPONSE"
fi
pass "MCP initialized (server=$HUB_NAME)"

# ── Step 5: MCP tools/list ────────────────────────────
echo -e "\n${YELLOW}Step 5: List Hub tools...${NC}"

LIST_RESPONSE=$(curl -sf -X POST http://localhost:8081/mcp \
    -H "Content-Type: application/json" \
    -d '{
        "jsonrpc": "2.0",
        "id": 2,
        "method": "tools/list"
    }' 2>&1) || fail "tools/list failed" "$LIST_RESPONSE"

TOOL_COUNT=$(echo "$LIST_RESPONSE" | jq '.result.tools | length')
if [ "$TOOL_COUNT" != "2" ]; then
    fail "Expected 2 tools (discover_tools, call_tool), got $TOOL_COUNT" "$LIST_RESPONSE"
fi
pass "Hub exposes $TOOL_COUNT tools: discover_tools, call_tool"

# ── Step 6: discover_tools ────────────────────────────
echo -e "\n${YELLOW}Step 6: Discover tools (search for 'echo')...${NC}"

DISCOVER_RESPONSE=$(curl -sf -X POST http://localhost:8081/mcp \
    -H "Content-Type: application/json" \
    -d '{
        "jsonrpc": "2.0",
        "id": 3,
        "method": "tools/call",
        "params": {
            "name": "discover_tools",
            "arguments": {"query": "echo message", "limit": 5}
        }
    }' 2>&1) || fail "discover_tools failed" "$DISCOVER_RESPONSE"

DISCOVER_TEXT=$(echo "$DISCOVER_RESPONSE" | jq -r '.result.content[0].text')
if echo "$DISCOVER_TEXT" | grep -q '"tool_name":"echo"'; then
    pass "Found 'echo' tool in search results"
elif echo "$DISCOVER_TEXT" | grep -q 'echo'; then
    pass "Found 'echo' tool in search results"
else
    fail "echo tool not found in discover results" "$DISCOVER_TEXT"
fi

# ── Step 7: call_tool (echo) ──────────────────────────
echo -e "\n${YELLOW}Step 7: Call 'echo' tool via Hub proxy...${NC}"

CALL_RESPONSE=$(curl -sf -X POST http://localhost:8081/mcp \
    -H "Content-Type: application/json" \
    -d "{
        \"jsonrpc\": \"2.0\",
        \"id\": 4,
        \"method\": \"tools/call\",
        \"params\": {
            \"name\": \"call_tool\",
            \"arguments\": {
                \"server_id\": $SERVER_ID,
                \"tool_name\": \"echo\",
                \"arguments\": {\"message\": \"Hello from e2e test!\"}
            }
        }
    }" 2>&1) || fail "call_tool failed" "$CALL_RESPONSE"

CALL_TEXT=$(echo "$CALL_RESPONSE" | jq -r '.result.content[0].text')
if echo "$CALL_TEXT" | grep -q "Hello from e2e test"; then
    pass "Echo returned: $CALL_TEXT"
else
    fail "Unexpected echo response" "$CALL_TEXT"
fi

# ── Step 8: call_tool (add_numbers) ───────────────────
echo -e "\n${YELLOW}Step 8: Call 'add_numbers' tool via Hub proxy...${NC}"

ADD_RESPONSE=$(curl -sf -X POST http://localhost:8081/mcp \
    -H "Content-Type: application/json" \
    -d "{
        \"jsonrpc\": \"2.0\",
        \"id\": 5,
        \"method\": \"tools/call\",
        \"params\": {
            \"name\": \"call_tool\",
            \"arguments\": {
                \"server_id\": $SERVER_ID,
                \"tool_name\": \"add_numbers\",
                \"arguments\": {\"a\": 17, \"b\": 25}
            }
        }
    }" 2>&1) || fail "call_tool add_numbers failed" "$ADD_RESPONSE"

ADD_TEXT=$(echo "$ADD_RESPONSE" | jq -r '.result.content[0].text')
if echo "$ADD_TEXT" | grep -q "42"; then
    pass "Add returned: $ADD_TEXT"
else
    fail "Expected 42 in response" "$ADD_TEXT"
fi

# ── Step 9: Auth test (restart Hub with auth enabled) ─
echo -e "\n${YELLOW}Step 9: Test authentication (restart Hub with AUTH_ENABLED=true)...${NC}"

# Kill the unauthenticated hub
kill "${PIDS[1]}" 2>/dev/null || true
wait "${PIDS[1]}" 2>/dev/null || true
sleep 1

AUTH_ENABLED=true ./bin/hub &
PIDS[1]=$!
sleep 2
pass "Hub restarted with auth enabled"

# 9a: Request without token should return 401
echo -e "${YELLOW}  9a: Request without token...${NC}"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST http://localhost:8081/mcp \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","id":10,"method":"initialize","params":{}}')

if [ "$HTTP_CODE" = "401" ]; then
    pass "Got 401 without token"
else
    fail "Expected 401, got $HTTP_CODE" ""
fi

# 9b: Request with valid token should work
echo -e "${YELLOW}  9b: Request with valid token...${NC}"
AUTH_INIT=$(curl -sf -X POST http://localhost:8081/mcp \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $ACCESS_TOKEN" \
    -d '{
        "jsonrpc": "2.0",
        "id": 11,
        "method": "initialize",
        "params": {
            "protocolVersion": "2025-03-26",
            "capabilities": {},
            "clientInfo": {"name": "e2e-test", "version": "1.0.0"}
        }
    }' 2>&1) || fail "Authenticated initialize failed" "$AUTH_INIT"

AUTH_HUB=$(echo "$AUTH_INIT" | jq -r '.result.serverInfo.name')
if [ "$AUTH_HUB" = "mcp-registry-hub" ]; then
    pass "Authenticated request succeeded"
else
    fail "Unexpected response" "$AUTH_INIT"
fi

# 9c: Request with invalid token should return 401
echo -e "${YELLOW}  9c: Request with invalid token...${NC}"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST http://localhost:8081/mcp \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer invalid.token.here" \
    -d '{"jsonrpc":"2.0","id":12,"method":"initialize","params":{}}')

if [ "$HTTP_CODE" = "401" ]; then
    pass "Got 401 with invalid token"
else
    fail "Expected 401, got $HTTP_CODE" ""
fi

# ── Done ──────────────────────────────────────────────
echo -e "\n${GREEN}════════════════════════════════════${NC}"
echo -e "${GREEN}  All e2e tests passed!${NC}"
echo -e "${GREEN}════════════════════════════════════${NC}"
