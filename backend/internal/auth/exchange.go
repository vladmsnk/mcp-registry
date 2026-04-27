package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"mcp-registry/internal/audit"
)

// ErrAudienceNotAllowed is returned when the requested audience does not match
// the configured prefix or is not bound to a registered MCP server.
var ErrAudienceNotAllowed = errors.New("token-exchange audience not allowed")

// ErrInsufficientRoles is returned when the caller has none of the audience's
// required roles. The hub already enforces this at the tool layer; the
// exchanger re-checks as defence-in-depth (P1.6).
var ErrInsufficientRoles = errors.New("token-exchange caller lacks required roles")

// audiencePattern restricts exchange targets to clients we provisioned for
// MCP servers. Anything else (e.g. realm-management, account, the hub itself)
// must never be reachable via this code path.
var audiencePattern = regexp.MustCompile(`^mcp-server-[A-Za-z0-9._-]{1,64}$`)

// ExchangeRequest carries the user's intent. UserRoles and RequiredRoles are
// non-optional — pass empty slices for unrestricted callers / public tools.
type ExchangeRequest struct {
	SubjectToken  string
	Audience      string
	UserRoles     []string
	RequiredRoles []string // union of required_roles across tools on the audience server
	// ActorSub / ActorUsername are used only for audit emission.
	ActorSub      string
	ActorUsername string
}

// DPoPSigner is the subset of dpop.Signer the exchanger needs. nil → no DPoP
// binding (legacy bearer flow).
type DPoPSigner interface {
	JKT() string
	AttachToRequest(r *http.Request, accessToken string) error
}

// TokenExchanger exchanges a user's access token for a service-specific token via Keycloak.
type TokenExchanger struct {
	tokenURL     string // Keycloak token endpoint
	clientID     string // Hub's client ID
	clientSecret string // Hub's client secret
	httpClient   *http.Client
	auditLog     *audit.Logger
	dpop         DPoPSigner // optional: when non-nil, exchanged tokens are bound to this key
}

func NewTokenExchanger(keycloakURL, realm, clientID, clientSecret string, auditLog *audit.Logger) *TokenExchanger {
	return &TokenExchanger{
		tokenURL:     fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", keycloakURL, realm),
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   http.DefaultClient,
		auditLog:     auditLog,
	}
}

// WithDPoP returns a copy of the exchanger that binds issued tokens to the
// given DPoP signer. The proxy must then attach a DPoP proof on every
// downstream call (P1.7).
func (te *TokenExchanger) WithDPoP(s DPoPSigner) *TokenExchanger {
	cp := *te
	cp.dpop = s
	return &cp
}

// Exchange performs an OAuth2 Token Exchange (RFC 8693) via Keycloak V2 with
// audience binding (P1.6) and role-overlap defence-in-depth.
//
// Order of checks:
//  1. Audience format — must match audiencePattern.
//  2. Role overlap — UserRoles ∩ RequiredRoles must be non-empty when
//     RequiredRoles is non-empty. Empty RequiredRoles means "public" — the
//     caller already cleared the per-tool RBAC gate, so we accept it here.
//  3. Forward to Keycloak.
//
// Every outcome (allowed/denied/error) is audited with the audience and
// caller identity so SIEM can spot anomalous exchange volume per audience.
func (te *TokenExchanger) Exchange(ctx context.Context, req ExchangeRequest) (string, error) {
	if !audiencePattern.MatchString(req.Audience) {
		te.audit(ctx, req, audit.StatusDenied, "audience format not allowed")
		return "", fmt.Errorf("%w: %q", ErrAudienceNotAllowed, req.Audience)
	}

	if len(req.RequiredRoles) > 0 && !rolesOverlap(req.UserRoles, req.RequiredRoles) {
		te.audit(ctx, req, audit.StatusDenied, "missing required role for audience")
		return "", ErrInsufficientRoles
	}

	form := url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {req.SubjectToken},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"audience":           {req.Audience},
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, te.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create exchange request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.SetBasicAuth(te.clientID, te.clientSecret)
	// DPoP-bound exchange (P1.7). The proof tells Keycloak to embed `cnf.jkt`
	// in the issued token, binding it to our private key.
	if te.dpop != nil {
		if err := te.dpop.AttachToRequest(httpReq, ""); err != nil {
			return "", fmt.Errorf("attach dpop proof: %w", err)
		}
	}

	resp, err := te.httpClient.Do(httpReq)
	if err != nil {
		te.audit(ctx, req, audit.StatusError, err.Error())
		return "", fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		te.audit(ctx, req, audit.StatusError, err.Error())
		return "", fmt.Errorf("read exchange response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		te.audit(ctx, req, audit.StatusError, fmt.Sprintf("kc %d", resp.StatusCode))
		return "", fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode, string(data))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		te.audit(ctx, req, audit.StatusError, "parse: "+err.Error())
		return "", fmt.Errorf("parse exchange response: %w", err)
	}

	if result.AccessToken == "" {
		te.audit(ctx, req, audit.StatusError, "empty access_token")
		return "", fmt.Errorf("token exchange returned empty access_token")
	}

	te.audit(ctx, req, audit.StatusAllowed, "")
	return result.AccessToken, nil
}

func (te *TokenExchanger) audit(ctx context.Context, req ExchangeRequest, status, errMsg string) {
	if te.auditLog == nil {
		return
	}
	te.auditLog.Log(ctx, audit.Event{
		Action:        "token.exchange",
		Status:        status,
		ActorSub:      req.ActorSub,
		ActorUsername: req.ActorUsername,
		ActorRoles:    req.UserRoles,
		Error:         errMsg,
		Metadata: map[string]any{
			"audience":       req.Audience,
			"required_roles": req.RequiredRoles,
		},
	})
}

func rolesOverlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, r := range a {
		set[r] = struct{}{}
	}
	for _, r := range b {
		if _, ok := set[r]; ok {
			return true
		}
	}
	return false
}
