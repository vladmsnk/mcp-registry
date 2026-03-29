package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// JWKS fetches and caches JSON Web Key Sets from Keycloak.
type JWKS struct {
	url    string
	mu     sync.RWMutex
	keys   map[string]*rsa.PublicKey
	expiry time.Time

	// Prevents thundering herd on cache expiry.
	refreshMu sync.Mutex
}

func NewJWKS(keycloakURL, realm string) *JWKS {
	return &JWKS{
		url:  fmt.Sprintf("%s/realms/%s/protocol/openid-connect/certs", keycloakURL, realm),
		keys: make(map[string]*rsa.PublicKey),
	}
}

// GetKey returns the RSA public key for the given key ID, fetching/refreshing as needed.
func (j *JWKS) GetKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	j.mu.RLock()
	if key, ok := j.keys[kid]; ok && time.Now().Before(j.expiry) {
		j.mu.RUnlock()
		return key, nil
	}
	j.mu.RUnlock()

	if err := j.refreshOnce(ctx); err != nil {
		return nil, err
	}

	j.mu.RLock()
	defer j.mu.RUnlock()
	key, ok := j.keys[kid]
	if !ok {
		return nil, fmt.Errorf("key %q not found in JWKS", kid)
	}
	return key, nil
}

// refreshOnce ensures only one goroutine refreshes at a time.
func (j *JWKS) refreshOnce(ctx context.Context) error {
	j.refreshMu.Lock()
	defer j.refreshMu.Unlock()

	// Double-check after acquiring lock — another goroutine may have already refreshed.
	j.mu.RLock()
	if time.Now().Before(j.expiry) {
		j.mu.RUnlock()
		return nil
	}
	j.mu.RUnlock()

	return j.refresh(ctx)
}

func (j *JWKS) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.url, nil)
	if err != nil {
		return fmt.Errorf("create JWKS request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("JWKS returned %d: %s", resp.StatusCode, string(body))
	}

	var jwksResp struct {
		Keys []jwkKey `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwksResp); err != nil {
		return fmt.Errorf("parse JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(jwksResp.Keys))
	for _, k := range jwksResp.Keys {
		if k.Kty != "RSA" || k.Use != "sig" {
			continue
		}
		pub, err := parseRSAPublicKey(k.N, k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}

	j.mu.Lock()
	j.keys = keys
	j.expiry = time.Now().Add(5 * time.Minute)
	j.mu.Unlock()

	return nil
}

type jwkKey struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func parseRSAPublicKey(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nStr)
	if err != nil {
		return nil, fmt.Errorf("decode n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eStr)
	if err != nil {
		return nil, fmt.Errorf("decode e: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := int(new(big.Int).SetBytes(eBytes).Int64())

	return &rsa.PublicKey{N: n, E: e}, nil
}
