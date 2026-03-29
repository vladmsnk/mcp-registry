package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// TokenExchanger exchanges a user's access token for a service-specific token via Keycloak.
type TokenExchanger struct {
	tokenURL     string // Keycloak token endpoint
	clientID     string // Hub's client ID
	clientSecret string // Hub's client secret
}

func NewTokenExchanger(keycloakURL, realm, clientID, clientSecret string) *TokenExchanger {
	return &TokenExchanger{
		tokenURL:     fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", keycloakURL, realm),
		clientID:     clientID,
		clientSecret: clientSecret,
	}
}

// Exchange performs an OAuth2 Token Exchange (RFC 8693) via Keycloak V2.
// It exchanges the subject token (user's access token) for a new token
// with the given audience (downstream service's client ID in Keycloak).
//
// Requirements:
//   - The mcp-hub client must have "Standard token exchange" enabled in Keycloak.
//   - The subject_token must have the requester client (mcp-hub) in the aud claim.
//   - The target audience client must exist in the same Keycloak realm.
func (te *TokenExchanger) Exchange(ctx context.Context, subjectToken, audience string) (string, error) {
	form := url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {subjectToken},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"audience":           {audience},
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, te.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create exchange request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.SetBasicAuth(te.clientID, te.clientSecret)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read exchange response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode, string(data))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parse exchange response: %w", err)
	}

	if result.AccessToken == "" {
		return "", fmt.Errorf("token exchange returned empty access_token")
	}

	return result.AccessToken, nil
}
