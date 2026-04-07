package keycloak

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

// AdminClient talks to the Keycloak Admin REST API using the hub's service account.
type AdminClient struct {
	baseURL      string
	realm        string
	clientID     string
	clientSecret string

	mu          sync.Mutex
	cachedToken string
	tokenExpiry time.Time
}

func NewAdminClient(baseURL, realm, clientID, clientSecret string) *AdminClient {
	return &AdminClient{
		baseURL:      strings.TrimRight(baseURL, "/"),
		realm:        realm,
		clientID:     clientID,
		clientSecret: clientSecret,
	}
}

// getToken obtains (or reuses) a service-account access token for the Admin API.
func (c *AdminClient) getToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.cachedToken != "" && time.Now().Before(c.tokenExpiry) {
		token := c.cachedToken
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	// Fetch new token without holding the lock.
	token, expiry, err := c.fetchToken(ctx)
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	c.cachedToken = token
	c.tokenExpiry = expiry
	c.mu.Unlock()

	return token, nil
}

func (c *AdminClient) fetchToken(ctx context.Context) (string, time.Time, error) {
	tokenURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", c.baseURL, c.realm)

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("token request failed (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", time.Time{}, fmt.Errorf("parse token response: %w", err)
	}

	expiry := time.Now().Add(time.Duration(result.ExpiresIn)*time.Second - 30*time.Second)
	return result.AccessToken, expiry, nil
}

// clientRepresentation is the JSON body sent to Keycloak when creating a client.
type clientRepresentation struct {
	ClientID                  string `json:"clientId"`
	Name                      string `json:"name"`
	Enabled                   bool   `json:"enabled"`
	Protocol                  string `json:"protocol"`
	PublicClient              bool   `json:"publicClient"`
	ServiceAccountsEnabled    bool   `json:"serviceAccountsEnabled"`
	StandardFlowEnabled       bool   `json:"standardFlowEnabled"`
	DirectAccessGrantsEnabled bool   `json:"directAccessGrantsEnabled"`
}

// CreateClient creates a new OIDC client in Keycloak.
// Returns the Keycloak-internal UUID (from the Location header).
// If the client already exists (409), it fetches the existing UUID.
func (c *AdminClient) CreateClient(ctx context.Context, clientID string) (keycloakInternalID string, secret string, err error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return "", "", fmt.Errorf("get admin token: %w", err)
	}

	rep := clientRepresentation{
		ClientID:                  clientID,
		Name:                      "MCP Server: " + clientID,
		Enabled:                   true,
		Protocol:                  "openid-connect",
		PublicClient:              false,
		ServiceAccountsEnabled:    false,
		StandardFlowEnabled:       false,
		DirectAccessGrantsEnabled: false,
	}

	body, _ := json.Marshal(rep)
	adminURL := fmt.Sprintf("%s/admin/realms/%s/clients", c.baseURL, c.realm)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, adminURL, bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("build create-client request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("create-client request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated:
		loc := resp.Header.Get("Location")
		keycloakInternalID = path.Base(loc)
		return keycloakInternalID, "", nil

	case http.StatusConflict:
		// Client already exists — look it up.
		existingID, lookupErr := c.getClientIDByClientID(ctx, token, clientID)
		if lookupErr != nil {
			return "", "", fmt.Errorf("client %q already exists but lookup failed: %w", clientID, lookupErr)
		}
		return existingID, "", nil

	default:
		respBody, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("create client failed (%d): %s", resp.StatusCode, string(respBody))
	}
}

// DeleteClient removes a Keycloak client by its internal UUID.
func (c *AdminClient) DeleteClient(ctx context.Context, keycloakInternalID string) error {
	token, err := c.getToken(ctx)
	if err != nil {
		return fmt.Errorf("get admin token: %w", err)
	}

	adminURL := fmt.Sprintf("%s/admin/realms/%s/clients/%s", c.baseURL, c.realm, keycloakInternalID)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, adminURL, nil)
	if err != nil {
		return fmt.Errorf("build delete-client request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete-client request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete client failed (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// getClientIDByClientID looks up a client by its logical clientId and returns the Keycloak UUID.
func (c *AdminClient) getClientIDByClientID(ctx context.Context, token, clientID string) (string, error) {
	adminURL := fmt.Sprintf("%s/admin/realms/%s/clients?clientId=%s", c.baseURL, c.realm, url.QueryEscape(clientID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, adminURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("lookup client failed (%d): %s", resp.StatusCode, string(body))
	}

	var clients []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &clients); err != nil {
		return "", fmt.Errorf("parse clients response: %w", err)
	}
	if len(clients) == 0 {
		return "", fmt.Errorf("client %q not found", clientID)
	}
	return clients[0].ID, nil
}
