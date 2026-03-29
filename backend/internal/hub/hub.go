package hub

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"

	"mcp-registry/internal/entity"
)

// ServerRepo is the interface the hub needs for server lookups.
type ServerRepo interface {
	GetEndpoint(ctx context.Context, serverID int64) (endpoint, name string, active bool, err error)
}

// ToolRepo is the interface the hub needs for tool search and storage.
type ToolRepo interface {
	Search(ctx context.Context, query string, limit int) ([]entity.DiscoveredTool, error)
	SearchByVector(ctx context.Context, queryEmbedding []float32, limit int) ([]entity.DiscoveredTool, error)
	ReplaceForServer(ctx context.Context, serverID int64, tools []entity.Tool) error
}

// Embedder generates vector embeddings from text.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	ModelName() string
	Dimensions() int
}

// TokenExchanger exchanges a user token for a service-specific token.
type TokenExchanger interface {
	Exchange(ctx context.Context, subjectToken, audience string) (string, error)
}

// Hub is an MCP server (Streamable HTTP transport) that exposes discover_tools and call_tool.
type Hub struct {
	servers   ServerRepo
	tools     ToolRepo
	embedder  Embedder
	exchanger TokenExchanger
}

// New creates a Hub. embedder and exchanger may be nil.
func New(servers ServerRepo, tools ToolRepo, embedder Embedder, exchanger TokenExchanger) *Hub {
	return &Hub{servers: servers, tools: tools, embedder: embedder, exchanger: exchanger}
}

// ServeHTTP handles POST /mcp — each request is a JSON-RPC 2.0 message.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20)) // 1 MB limit
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeHTTPError(w, -32700, "parse error")
		return
	}

	// Notifications (no ID) — acknowledge with 202.
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	resp := h.dispatch(r.Context(), req)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("hub: write response: %v", err)
	}
}

func (h *Hub) dispatch(ctx context.Context, req rpcRequest) rpcResponse {
	switch req.Method {
	case methodInitialize:
		return h.handleInitialize(req)
	case methodToolsList:
		return h.handleToolsList(req)
	case methodToolsCall:
		return h.handleToolsCall(ctx, req)
	case methodPing:
		return rpcSuccess(req.ID, map[string]any{})
	default:
		return rpcError(req.ID, -32601, "method not found: "+req.Method)
	}
}

// --- MCP method handlers ---

func (h *Hub) handleInitialize(req rpcRequest) rpcResponse {
	return rpcSuccess(req.ID, map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    mcpServerName,
			"version": mcpServerVersion,
		},
	})
}

func (h *Hub) handleToolsList(req rpcRequest) rpcResponse {
	tools := []map[string]any{
		{
			"name":        "discover_tools",
			"description": "Search the MCP registry for tools matching a query. Returns tools with their server info so you can call them via call_tool.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query — keywords to match against tool names, descriptions, and server tags",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max number of results (default 5, max 20)",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "call_tool",
			"description": "Call a tool on a registered MCP server. Use discover_tools first to find the server_id and tool_name.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server_id": map[string]any{
						"type":        "integer",
						"description": "ID of the MCP server (from discover_tools result)",
					},
					"tool_name": map[string]any{
						"type":        "string",
						"description": "Name of the tool to call",
					},
					"arguments": map[string]any{
						"type":        "object",
						"description": "Arguments to pass to the tool",
					},
				},
				"required": []string{"server_id", "tool_name", "arguments"},
			},
		},
	}

	return rpcSuccess(req.ID, map[string]any{"tools": tools})
}

func (h *Hub) handleToolsCall(ctx context.Context, req rpcRequest) rpcResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return rpcError(req.ID, -32602, "invalid params")
	}

	switch params.Name {
	case "discover_tools":
		return h.execDiscoverTools(ctx, req.ID, params.Arguments)
	case "call_tool":
		return h.execCallTool(ctx, req.ID, params.Arguments)
	default:
		return rpcError(req.ID, -32602, "unknown tool: "+params.Name)
	}
}

func (h *Hub) execDiscoverTools(ctx context.Context, id *int64, raw json.RawMessage) rpcResponse {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return rpcError(id, -32602, "invalid arguments for discover_tools")
	}

	var results []entity.DiscoveredTool
	var err error

	if h.embedder != nil {
		queryVec, embErr := h.embedder.Embed(ctx, args.Query)
		if embErr != nil {
			log.Printf("hub: embedding query failed, falling back to text search: %v", embErr)
			results, err = h.tools.Search(ctx, args.Query, args.Limit)
		} else {
			results, err = h.tools.SearchByVector(ctx, queryVec, args.Limit)
		}
	} else {
		results, err = h.tools.Search(ctx, args.Query, args.Limit)
	}
	if err != nil {
		return toolError(id, "search failed: "+err.Error())
	}

	text, _ := json.Marshal(results)

	var hint string
	if len(results) > 0 {
		hint = "\n\nTo call any tool, use call_tool with server_id, tool_name, and arguments."
	} else {
		hint = "\n\nNo tools found matching your query. Try different keywords."
	}

	return toolSuccess(id, string(text)+hint)
}

func (h *Hub) execCallTool(ctx context.Context, id *int64, raw json.RawMessage) rpcResponse {
	var args struct {
		ServerID  int64           `json:"server_id"`
		ToolName  string          `json:"tool_name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return rpcError(id, -32602, "invalid arguments for call_tool")
	}

	result, err := CallRemoteTool(ctx, h.servers, h.exchanger, args.ServerID, args.ToolName, args.Arguments)
	if err != nil {
		return toolError(id, "call_tool failed: "+err.Error())
	}

	return toolSuccess(id, string(result))
}

// --- JSON-RPC types ---

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

func rpcSuccess(id *int64, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func rpcError(id *int64, code int, msg string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: map[string]any{"code": code, "message": msg}}
}

func toolSuccess(id *int64, text string) rpcResponse {
	return rpcSuccess(id, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
	})
}

func toolError(id *int64, text string) rpcResponse {
	return rpcSuccess(id, map[string]any{
		"isError": true,
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
	})
}

func writeHTTPError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(rpcResponse{
		JSONRPC: "2.0",
		Error:   map[string]any{"code": code, "message": msg},
	})
}
