package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Claims represents the relevant JWT claims from a Keycloak access token.
type Claims struct {
	Subject          string   `json:"sub"`
	PreferredUsername string   `json:"preferred_username"`
	Email            string   `json:"email"`
	RealmRoles       []string `json:"-"`
	Issuer           string   `json:"iss"`
	ExpiresAt        int64    `json:"exp"`
	IssuedAt         int64    `json:"iat"`
	AuthorizedParty  string   `json:"azp"`
}

// Validator validates JWTs using Keycloak's JWKS.
type Validator struct {
	jwks     *JWKS
	issuer   string
	clientID string
}

func NewValidator(keycloakURL, realm, clientID string) *Validator {
	return &Validator{
		jwks:     NewJWKS(keycloakURL, realm),
		issuer:   fmt.Sprintf("%s/realms/%s", keycloakURL, realm),
		clientID: clientID,
	}
}

// Validate parses and validates a JWT token string, returning the claims.
func (v *Validator) Validate(ctx context.Context, tokenStr string) (*Claims, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}

	var header struct {
		Kid string `json:"kid"`
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}
	if header.Alg != "RS256" {
		return nil, fmt.Errorf("unsupported algorithm: %s", header.Alg)
	}

	key, err := v.jwks.GetKey(ctx, header.Kid)
	if err != nil {
		return nil, fmt.Errorf("get signing key: %w", err)
	}

	signedContent := parts[0] + "." + parts[1]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	hash := sha256.Sum256([]byte(signedContent))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, hash[:], signature); err != nil {
		return nil, fmt.Errorf("invalid signature")
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode claims: %w", err)
	}

	// Single parse into a raw map, then extract typed fields and realm roles.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(claimsJSON, &raw); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}

	var claims Claims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}

	if realmAccess, ok := raw["realm_access"]; ok {
		var ra struct {
			Roles []string `json:"roles"`
		}
		if json.Unmarshal(realmAccess, &ra) == nil {
			claims.RealmRoles = ra.Roles
		}
	}

	if time.Now().Unix() > claims.ExpiresAt {
		return nil, fmt.Errorf("token expired")
	}

	if claims.Issuer != v.issuer {
		return nil, fmt.Errorf("invalid issuer: got %q, want %q", claims.Issuer, v.issuer)
	}

	return &claims, nil
}
