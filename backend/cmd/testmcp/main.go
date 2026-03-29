// testmcp is a minimal MCP server for e2e testing.
// It exposes two tools: "echo" and "add_numbers".
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

func main() {
	port := "9090"
	if p := os.Getenv("TEST_MCP_PORT"); p != "" {
		port = p
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", handleMCP)

	log.Printf("testmcp: listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}

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

func handleMCP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, rpcResponse{JSONRPC: "2.0", Error: map[string]any{"code": -32700, "message": "parse error"}})
		return
	}

	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	var resp rpcResponse
	switch req.Method {
	case "initialize":
		resp = rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "testmcp", "version": "1.0.0"},
		}}
	case "tools/list":
		resp = rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"tools": []map[string]any{
				{
					"name":        "echo",
					"description": "Echoes back the input message",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"message": map[string]any{"type": "string", "description": "Message to echo"},
						},
						"required": []string{"message"},
					},
				},
				{
					"name":        "add_numbers",
					"description": "Adds two numbers together and returns the result",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"a": map[string]any{"type": "number", "description": "First number"},
							"b": map[string]any{"type": "number", "description": "Second number"},
						},
						"required": []string{"a", "b"},
					},
				},
			},
		}}
	case "tools/call":
		resp = handleToolCall(req)
	case "ping":
		resp = rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	default:
		resp = rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32601, "message": "method not found"}}
	}

	writeJSON(w, resp)
}

func handleToolCall(req rpcRequest) rpcResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32602, "message": "invalid params"}}
	}

	switch params.Name {
	case "echo":
		var args struct {
			Message string `json:"message"`
		}
		json.Unmarshal(params.Arguments, &args)
		return toolResult(req.ID, fmt.Sprintf("Echo: %s", args.Message))

	case "add_numbers":
		var args struct {
			A float64 `json:"a"`
			B float64 `json:"b"`
		}
		json.Unmarshal(params.Arguments, &args)
		return toolResult(req.ID, fmt.Sprintf("Result: %g", args.A+args.B))

	default:
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"isError": true,
			"content": []map[string]any{{"type": "text", "text": "unknown tool: " + params.Name}},
		}}
	}
}

func toolResult(id *int64, text string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
	}}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
