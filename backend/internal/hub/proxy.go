package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync/atomic"

	"mcp-registry/internal/auth"
	"mcp-registry/internal/security"
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

// ErrPermissionDenied is the sentinel returned by CallRemoteTool when the
// caller lacks the tool's required roles. The detail-rich PermissionDeniedError
// wraps it with the required/caller roles so the audit log can record exactly
// which intersection failed (P2.9).
var ErrPermissionDenied = errors.New("permission denied: missing required role for tool")

// PermissionDeniedError carries the data we want in the audit event for an
// RBAC denial: tool, server, the union of required roles, and the caller's
// roles. Use errors.Is(err, ErrPermissionDenied) for the categorical check
// and errors.As(err, &pdErr) to pull out the metadata.
type PermissionDeniedError struct {
	ServerID      int64
	ToolName      string
	RequiredRoles []string
	CallerRoles   []string
}

func (e *PermissionDeniedError) Error() string {
	return ErrPermissionDenied.Error()
}

func (e *PermissionDeniedError) Unwrap() error { return ErrPermissionDenied }

var reqID atomic.Int64

// dpopAttacher attaches a DPoP proof + Authorization header to a request. nil
// means "no DPoP binding for this session" (legacy bearer flow).
type dpopAttacher interface {
	AttachToRequest(r *http.Request, accessToken string) error
}

// mcpSession holds the state for a single MCP connection to a downstream server.
type mcpSession struct {
	endpoint    string
	sessionID   string
	bearerToken string
	httpClient  *http.Client
	dpop        dpopAttacher
}

func newMCPSession(endpoint, bearerToken string, httpClient *http.Client) *mcpSession {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &mcpSession{endpoint: endpoint, bearerToken: bearerToken, httpClient: httpClient}
}

func (s *mcpSession) withDPoP(d dpopAttacher) *mcpSession {
	s.dpop = d
	return s
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
	switch {
	case s.dpop != nil:
		// DPoP rewrites the Authorization scheme from Bearer → DPoP and adds
		// a fresh proof JWT covering this exact request URL+method.
		if err := s.dpop.AttachToRequest(httpReq, s.bearerToken); err != nil {
			return nil, err
		}
	case s.bearerToken != "":
		httpReq.Header.Set("Authorization", "Bearer "+s.bearerToken)
	}

	return s.httpClient.Do(httpReq)
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

// buildClient combines the hub's base options with a per-server pin (may be empty).
func buildClient(opts security.ClientOptions, pin string) *http.Client {
	merged := opts
	merged.PinSHA256 = pin
	return security.NewClient(merged)
}

// CallRemoteTool connects to a registered MCP server and calls a tool on it.
// When the caller has validated JWT claims in ctx, the tool's required_roles are enforced —
// callers without overlapping roles get ErrPermissionDenied. Tools with empty required_roles
// are unrestricted. When no claims are present (auth disabled), no role check is performed.
//
// The httpOpts are merged with the server's pinned fingerprint to build a TLS-verifying
// client for this call. When dpopSigner is non-nil the outbound request uses the DPoP
// scheme instead of plain Bearer (P1.7).
func CallRemoteTool(ctx context.Context, servers ServerRepo, tools ToolRepo, exchanger TokenExchanger, httpOpts security.ClientOptions, serverID int64, toolName string, arguments json.RawMessage, dpopSigner dpopAttacher) (json.RawMessage, error) {
	endpoint, name, keycloakClientID, tlsCertSHA256, active, err := servers.GetEndpoint(ctx, serverID)
	if err != nil {
		return nil, fmt.Errorf("lookup server: %w", err)
	}
	if !active {
		return nil, fmt.Errorf("server %q is not active", name)
	}

	// Tool-level RBAC: deny if caller has claims but lacks any required role.
	if claims := auth.ClaimsFromContext(ctx); claims != nil && tools != nil {
		required, rolesErr := tools.GetRequiredRoles(ctx, serverID, toolName)
		if rolesErr != nil {
			return nil, fmt.Errorf("lookup tool roles: %w", rolesErr)
		}
		if !auth.HasAnyRole(claims, required) {
			return nil, &PermissionDeniedError{
				ServerID:      serverID,
				ToolName:      toolName,
				RequiredRoles: required,
				CallerRoles:   claims.RealmRoles,
			}
		}
	}

	// Exchange user token for a downstream service token. Audience binding (P1.6):
	// the exchanger validates the audience format and re-checks the role overlap
	// against the union of required_roles for tools on this server.
	var bearerToken string
	if exchanger != nil && keycloakClientID != "" {
		userToken := auth.TokenFromContext(ctx)
		if userToken != "" {
			serverRoles, rolesErr := tools.GetServerRequiredRoles(ctx, serverID)
			if rolesErr != nil {
				return nil, fmt.Errorf("lookup server roles: %w", rolesErr)
			}
			claims := auth.ClaimsFromContext(ctx)
			req := auth.ExchangeRequest{
				SubjectToken:  userToken,
				Audience:      keycloakClientID,
				RequiredRoles: serverRoles,
			}
			if claims != nil {
				req.UserRoles = claims.RealmRoles
				req.ActorSub = claims.Subject
				req.ActorUsername = claims.PreferredUsername
			}
			exchanged, err := exchanger.Exchange(ctx, req)
			if err != nil {
				if errors.Is(err, auth.ErrAudienceNotAllowed) || errors.Is(err, auth.ErrInsufficientRoles) {
					return nil, fmt.Errorf("token exchange denied: %w", err)
				}
				log.Printf("hub: token exchange for %q (audience=%s) failed: %v", name, keycloakClientID, err)
			} else {
				bearerToken = exchanged
			}
		}
	}

	client := buildClient(httpOpts, tlsCertSHA256)
	sess := newMCPSession(endpoint, bearerToken, client)
	if dpopSigner != nil && bearerToken != "" {
		sess.withDPoP(dpopSigner)
	}
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
