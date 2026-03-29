package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync/atomic"

	"mcp-registry/internal/auth"
)

const (
	mcpProtocolVersion = "2025-03-26"
	mcpServerName      = "mcp-registry-hub"
	mcpServerVersion   = "0.1.0"

	methodInitialize  = "initialize"
	methodToolsList   = "tools/list"
	methodToolsCall   = "tools/call"
	methodPing        = "ping"
	notifyInitialized = "notifications/initialized"
)

var reqID atomic.Int64

// mcpSession holds the state for a single MCP connection to a downstream server.
type mcpSession struct {
	endpoint    string
	sessionID   string
	bearerToken string
}

func newMCPSession(endpoint, bearerToken string) *mcpSession {
	return &mcpSession{endpoint: endpoint, bearerToken: bearerToken}
}

func (s *mcpSession) initialize(ctx context.Context) error {
	id := nextID()
	resp, err := s.post(ctx, rpcRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  methodInitialize,
		Params: marshalRaw(map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    mcpServerName,
				"version": mcpServerVersion,
			},
		}),
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	s.sessionID = resp.Header.Get("Mcp-Session-Id")

	_, err = decodeRPCResponse(resp)
	return err
}

func (s *mcpSession) notify(ctx context.Context, method string) {
	req := rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
	}
	resp, err := s.post(ctx, req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func (s *mcpSession) callTool(ctx context.Context, toolName string, arguments json.RawMessage) (json.RawMessage, error) {
	id := nextID()
	resp, err := s.post(ctx, rpcRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  methodToolsCall,
		Params: marshalRaw(map[string]any{
			"name":      toolName,
			"arguments": arguments,
		}),
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return decodeRPCResponse(resp)
}

func (s *mcpSession) listTools(ctx context.Context) ([]remoteTool, error) {
	id := nextID()
	resp, err := s.post(ctx, rpcRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  methodToolsList,
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := decodeRPCResponse(resp)
	if err != nil {
		return nil, err
	}

	var result struct {
		Tools []remoteTool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse tools list: %w", err)
	}
	return result.Tools, nil
}

func (s *mcpSession) post(ctx context.Context, req rpcRequest) (*http.Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if s.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", s.sessionID)
	}
	if s.bearerToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+s.bearerToken)
	}

	return http.DefaultClient.Do(httpReq)
}

// decodeRPCResponse reads and parses a JSON-RPC response, returning the result.
func decodeRPCResponse(resp *http.Response) (json.RawMessage, error) {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var rpcResp struct {
		Result json.RawMessage `json:"result,omitempty"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(data, &rpcResp); err != nil {
		return nil, fmt.Errorf("invalid response: %s", string(data))
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}

func nextID() int64 {
	return reqID.Add(1)
}

func marshalRaw(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

type remoteTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// CallRemoteTool connects to a registered MCP server and calls a tool on it.
func CallRemoteTool(ctx context.Context, servers ServerRepo, exchanger TokenExchanger, serverID int64, toolName string, arguments json.RawMessage) (json.RawMessage, error) {
	endpoint, name, active, err := servers.GetEndpoint(ctx, serverID)
	if err != nil {
		return nil, fmt.Errorf("lookup server: %w", err)
	}
	if !active {
		return nil, fmt.Errorf("server %q is not active", name)
	}

	// Exchange user token for a downstream service token.
	var bearerToken string
	if exchanger != nil {
		userToken := auth.TokenFromContext(ctx)
		if userToken != "" {
			exchanged, err := exchanger.Exchange(ctx, userToken, name)
			if err != nil {
				log.Printf("hub: token exchange for %q failed: %v", name, err)
			} else {
				bearerToken = exchanged
			}
		}
	}

	sess := newMCPSession(endpoint, bearerToken)
	if err := sess.initialize(ctx); err != nil {
		return nil, fmt.Errorf("initialize %q: %w", name, err)
	}
	sess.notify(ctx, notifyInitialized)

	result, err := sess.callTool(ctx, toolName, arguments)
	if err != nil {
		return nil, fmt.Errorf("call %q on %q: %w", toolName, name, err)
	}

	return result, nil
}
