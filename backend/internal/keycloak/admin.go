package keycloak

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ManagedClientIDPrefix is the only clientId prefix the AdminClient is willing
// to mutate. Any operation against a client outside this prefix is rejected
// before hitting Keycloak — defence-in-depth (P1.5) against the SA role being
// over-broad (currently realm-management:manage-clients).
const ManagedClientIDPrefix = "mcp-server-"

// ErrClientOutOfScope is returned when an operation targets a Keycloak client
// whose clientId does not match ManagedClientIDPrefix.
var ErrClientOutOfScope = errors.New("keycloak: client outside managed prefix")

// AdminClient talks to the Keycloak Admin REST API using the hub's service account.
type AdminClient struct {
	baseURL      string
	realm        string
	clientID     string
	clientSecret string

	mu          sync.Mutex
	cachedToken string
	// tokenIssuedAt + tokenLifetime are used for the 80%-TTL refresh decision
	// (P1.4). tokenExpiry is the hard deadline.
	tokenIssuedAt time.Time
	tokenLifetime time.Duration
	tokenExpiry   time.Time

	refreshOnce sync.Mutex // serialises background refresh attempts
	refreshing  atomic.Bool

	// auditFn (optional) records each successful refresh so SIEM can spot
	// abnormal cadences. nil → no-op. Must be safe for concurrent use.
	auditFn func(ctx context.Context, fields map[string]any)

	// secretCapturedAt is when the operator-supplied client secret was loaded.
	// Used by the secret-age metric (P3.15).
	secretCapturedAt time.Time
	// refreshCount tracks how often we have re-fetched the admin token (P3.15).
	refreshCount atomic.Int64
}

func NewAdminClient(baseURL, realm, clientID, clientSecret string) *AdminClient {
	return &AdminClient{
		baseURL:          strings.TrimRight(baseURL, "/"),
		realm:            realm,
		clientID:         clientID,
		clientSecret:     clientSecret,
		secretCapturedAt: time.Now(),
	}
}

// SetAuditFn registers a callback invoked on each successful token (re)fetch.
// Pass a closure that emits an audit.Event from your audit.Logger.
func (c *AdminClient) SetAuditFn(fn func(ctx context.Context, fields map[string]any)) {
	c.auditFn = fn
}

// SecretCapturedAt is when the configured client secret was loaded into memory.
// Approximates secret age until we add proper rotation tracking (P3.15).
func (c *AdminClient) SecretCapturedAt() time.Time { return c.secretCapturedAt }

// ServiceAccountToken returns a valid client-credentials access token. Reuses
// the cache and 80% TTL refresh path so callers (e.g. the probe loop) don't
// each hammer Keycloak. Exposed for non-admin uses of the SA token.
func (c *AdminClient) ServiceAccountToken(ctx context.Context) (string, error) {
	return c.getToken(ctx)
}

// RefreshCount is the number of times the admin token has been (re)fetched (P3.15).
func (c *AdminClient) RefreshCount() int64 { return c.refreshCount.Load() }

// getToken obtains (or reuses) a service-account access token for the Admin API.
// At 80% of the token's lifetime, a background refresh is kicked off so that the
// in-flight request keeps using the still-valid cached token while a new one
// is being fetched (P1.4: pre-expiry refresh, no request stalls).
func (c *AdminClient) getToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	cached := c.cachedToken
	expiry := c.tokenExpiry
	issued := c.tokenIssuedAt
	lifetime := c.tokenLifetime
	c.mu.Unlock()

	now := time.Now()
	if cached != "" && now.Before(expiry) {
		// Pre-expiry refresh at 80% TTL. Best-effort, in the background, so we
		// never block a request waiting for KC.
		if lifetime > 0 && now.Sub(issued) >= time.Duration(float64(lifetime)*0.8) {
			c.scheduleBackgroundRefresh()
		}
		return cached, nil
	}

	// Cache empty or expired — synchronous fetch.
	token, _, _, err := c.fetchAndStore(ctx, "sync")
	return token, err
}

// scheduleBackgroundRefresh kicks off a refresh in a goroutine if one is not
// already in-flight. CompareAndSwap on `refreshing` makes this idempotent
// across concurrent callers crossing the 80% threshold simultaneously.
func (c *AdminClient) scheduleBackgroundRefresh() {
	if !c.refreshing.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer c.refreshing.Store(false)
		// Use a fresh context — the caller that observed 80% TTL may have a
		// short deadline, but our refresh should not be tied to its lifetime.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, _, _, err := c.fetchAndStore(ctx, "background"); err != nil {
			log.Printf("keycloak: background admin-token refresh failed (cached token still valid): %v", err)
		}
	}()
}

// fetchAndStore performs the token request and updates the cache. The mode
// argument is used purely for audit/telemetry to distinguish synchronous
// (a request blocked on us) from background (pre-expiry) refreshes.
func (c *AdminClient) fetchAndStore(ctx context.Context, mode string) (string, time.Duration, time.Time, error) {
	// Serialise so two simultaneous synchronous misses don't double-fetch.
	c.refreshOnce.Lock()
	defer c.refreshOnce.Unlock()

	// Re-check after acquiring the lock — another caller may have refreshed.
	c.mu.Lock()
	cached := c.cachedToken
	expiry := c.tokenExpiry
	c.mu.Unlock()
	if cached != "" && time.Now().Before(expiry) && mode == "sync" {
		return cached, c.tokenLifetime, expiry, nil
	}

	token, lifetime, expiry, err := c.fetchToken(ctx)
	if err != nil {
		return "", 0, time.Time{}, err
	}

	now := time.Now()
	c.mu.Lock()
	c.cachedToken = token
	c.tokenIssuedAt = now
	c.tokenLifetime = lifetime
	c.tokenExpiry = expiry
	c.mu.Unlock()

	c.refreshCount.Add(1)
	if c.auditFn != nil {
		c.auditFn(ctx, map[string]any{
			"phase":      "admin_token_refresh",
			"mode":       mode,
			"lifetime_s": int(lifetime / time.Second),
			"refresh_n":  c.refreshCount.Load(),
		})
	}
	slog.Default().Debug("keycloak admin token refreshed",
		slog.String("mode", mode),
		slog.Int64("lifetime_s", int64(lifetime/time.Second)))

	return token, lifetime, expiry, nil
}

func (c *AdminClient) fetchToken(ctx context.Context) (string, time.Duration, time.Time, error) {
	tokenURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", c.baseURL, c.realm)

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, time.Time{}, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, time.Time{}, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", 0, time.Time{}, fmt.Errorf("token request failed (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", 0, time.Time{}, fmt.Errorf("parse token response: %w", err)
	}

	lifetime := time.Duration(result.ExpiresIn) * time.Second
	// Hard deadline trims 30s for clock skew & in-flight retries.
	expiry := time.Now().Add(lifetime - 30*time.Second)
	return result.AccessToken, lifetime, expiry, nil
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
	if !strings.HasPrefix(clientID, ManagedClientIDPrefix) {
		return "", "", fmt.Errorf("%w: %q (expected prefix %q)", ErrClientOutOfScope, clientID, ManagedClientIDPrefix)
	}
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

// RevokeAllTokens sets notBefore=now on the client and pushes the revocation, invalidating
// outstanding access/refresh tokens for that client. Should be called before DeleteClient
// to ensure cached tokens cannot be reused before the client row disappears.
func (c *AdminClient) RevokeAllTokens(ctx context.Context, keycloakInternalID string) error {
	if err := c.assertManagedClient(ctx, keycloakInternalID); err != nil {
		return err
	}
	token, err := c.getToken(ctx)
	if err != nil {
		return fmt.Errorf("get admin token: %w", err)
	}

	adminURL := fmt.Sprintf("%s/admin/realms/%s/clients/%s/push-revocation", c.baseURL, c.realm, keycloakInternalID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, adminURL, nil)
	if err != nil {
		return fmt.Errorf("build push-revocation request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("push-revocation request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("push-revocation failed (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// DeleteClient removes a Keycloak client by its internal UUID.
func (c *AdminClient) DeleteClient(ctx context.Context, keycloakInternalID string) error {
	if err := c.assertManagedClient(ctx, keycloakInternalID); err != nil {
		return err
	}
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

// assertManagedClient looks up the clientId for the given internal UUID and
// fails the operation if it does not start with ManagedClientIDPrefix. This
// is the runtime guard for P1.5 — if the SA's KC permissions are too broad,
// the AdminClient itself still refuses to touch unrelated clients.
//
// One round-trip per call is acceptable on revoke/delete (rare ops). We
// could cache, but a stale cache could mis-authorise a renamed/recreated
// client; the safety bias points to "no caching".
func (c *AdminClient) assertManagedClient(ctx context.Context, keycloakInternalID string) error {
	token, err := c.getToken(ctx)
	if err != nil {
		return fmt.Errorf("get admin token: %w", err)
	}

	adminURL := fmt.Sprintf("%s/admin/realms/%s/clients/%s", c.baseURL, c.realm, keycloakInternalID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, adminURL, nil)
	if err != nil {
		return fmt.Errorf("build lookup request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("lookup client: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		// Already gone — nothing to delete; let the caller continue. Returning
		// nil here would be wrong (the actual op might still 404 us), but we
		// also don't want to crash a worker over an already-cleaned client.
		return fmt.Errorf("client %s not found in keycloak", keycloakInternalID)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("lookup client failed (%d): %s", resp.StatusCode, string(body))
	}

	var rep struct {
		ClientID string `json:"clientId"`
	}
	if err := json.Unmarshal(body, &rep); err != nil {
		return fmt.Errorf("parse client representation: %w", err)
	}
	if !strings.HasPrefix(rep.ClientID, ManagedClientIDPrefix) {
		return fmt.Errorf("%w: %q (expected prefix %q)", ErrClientOutOfScope, rep.ClientID, ManagedClientIDPrefix)
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
