// Package dpop implements the proof-of-possession side of RFC 9449 (DPoP) so
// the hub can bind exchanged tokens to its long-lived keypair (NHI #7).
//
// What this package does:
//   - Generates / loads an ECDSA P-256 keypair (the DPoP signing key).
//   - Produces compact-JWS DPoP proofs for arbitrary HTTP requests.
//   - Computes the JWK thumbprint (`jkt`) used in audit and token binding.
//
// What it does NOT do:
//   - Validate inbound DPoP proofs. The hub never *receives* DPoP-bound
//     requests (clients use the SPA cookie). When the downstream MCP server
//     wants to validate, it does so against the access token's `cnf.jkt`
//     claim and the proof header — both of which we provide.
package dpop

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Signer wraps a private key and can produce DPoP proofs.
type Signer struct {
	priv *ecdsa.PrivateKey
	jwk  publicJWK
	jkt  string // RFC 7638 thumbprint of the public JWK
}

// publicJWK is the subset of fields we serialise into the proof header.
type publicJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// LoadOrGenerate returns a Signer. If pemPath points to an existing PEM file
// that file is used; otherwise a new keypair is generated and (when pemPath
// is non-empty) written there. Pass "" to use an ephemeral key — fine for
// dev, but the access tokens then go invalid on every restart.
func LoadOrGenerate(pemPath string) (*Signer, error) {
	if pemPath != "" {
		if data, err := os.ReadFile(pemPath); err == nil {
			return signerFromPEM(data)
		}
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate dpop key: %w", err)
	}
	if pemPath != "" {
		der, err := x509.MarshalECPrivateKey(priv)
		if err != nil {
			return nil, fmt.Errorf("marshal dpop key: %w", err)
		}
		buf := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
		// 0o600 — the hub process is the only legitimate reader.
		if err := os.WriteFile(pemPath, buf, 0o600); err != nil {
			return nil, fmt.Errorf("write dpop key: %w", err)
		}
	}
	return newSigner(priv)
}

func signerFromPEM(data []byte) (*Signer, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("dpop: pem decode failed")
	}
	priv, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse ec key: %w", err)
	}
	return newSigner(priv)
}

func newSigner(priv *ecdsa.PrivateKey) (*Signer, error) {
	if priv.Curve != elliptic.P256() {
		return nil, errors.New("dpop: only P-256 supported")
	}
	jwk := publicJWK{
		Kty: "EC",
		Crv: "P-256",
		X:   base64URL(padTo(priv.PublicKey.X.Bytes(), 32)),
		Y:   base64URL(padTo(priv.PublicKey.Y.Bytes(), 32)),
	}
	return &Signer{priv: priv, jwk: jwk, jkt: thumbprint(jwk)}, nil
}

// JKT returns the RFC 7638 SHA-256 thumbprint (base64url, no padding) of the
// public JWK. Use it to set `dpop_jkt` on token-exchange requests and to
// match the access token's `cnf.jkt` claim.
func (s *Signer) JKT() string { return s.jkt }

// ProofOptions controls a single proof generation.
type ProofOptions struct {
	Method string // HTTP method, uppercase
	URL    string // request URL — query params and trailing slash matter
	// AccessToken (optional). When set, the proof carries the `ath` claim
	// (sha256 of the token) per RFC 9449 §4.3. Required for proofs sent
	// alongside a DPoP-bound bearer; omit when proving to the token endpoint.
	AccessToken string
	// Now lets tests pin time.Now. Zero means "wallclock".
	Now time.Time
}

// Proof returns a DPoP proof JWT (compact JWS) covering the given request.
func (s *Signer) Proof(opts ProofOptions) (string, error) {
	if s == nil {
		return "", errors.New("dpop: nil signer")
	}
	if opts.Method == "" || opts.URL == "" {
		return "", errors.New("dpop: method and url required")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	htu, err := canonicalHTU(opts.URL)
	if err != nil {
		return "", err
	}

	header := map[string]any{
		"typ": "dpop+jwt",
		"alg": "ES256",
		"jwk": s.jwk,
	}
	jti, err := randomJTI()
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"jti": jti,
		"htm": strings.ToUpper(opts.Method),
		"htu": htu,
		"iat": now.Unix(),
	}
	if opts.AccessToken != "" {
		sum := sha256.Sum256([]byte(opts.AccessToken))
		payload["ath"] = base64URL(sum[:])
	}

	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(payload)
	signingInput := base64URL(hb) + "." + base64URL(pb)

	digest := sha256.Sum256([]byte(signingInput))
	r, sig, err := ecdsa.Sign(rand.Reader, s.priv, digest[:])
	if err != nil {
		return "", fmt.Errorf("ecdsa sign: %w", err)
	}
	// JOSE ES256 expects fixed-length R||S, not ASN.1 DER. Each integer is
	// padded/truncated to 32 bytes.
	jose := append(padTo(r.Bytes(), 32), padTo(sig.Bytes(), 32)...)
	return signingInput + "." + base64URL(jose), nil
}

// AttachToRequest sets the Authorization (DPoP scheme) and DPoP headers on
// the request. accessToken may be empty for unauthenticated proofs.
func (s *Signer) AttachToRequest(r *http.Request, accessToken string) error {
	if s == nil {
		return errors.New("dpop: nil signer")
	}
	proof, err := s.Proof(ProofOptions{Method: r.Method, URL: r.URL.String(), AccessToken: accessToken})
	if err != nil {
		return err
	}
	if accessToken != "" {
		r.Header.Set("Authorization", "DPoP "+accessToken)
	}
	r.Header.Set("DPoP", proof)
	return nil
}

// canonicalHTU strips fragments per §4.2 and lowercases the scheme/host.
func canonicalHTU(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("dpop: parse url: %w", err)
	}
	u.Fragment = ""
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return u.String(), nil
}

func thumbprint(j publicJWK) string {
	// Members must be in lexical order (RFC 7638): crv, kty, x, y.
	canonical := fmt.Sprintf(`{"crv":%q,"kty":%q,"x":%q,"y":%q}`, j.Crv, j.Kty, j.X, j.Y)
	sum := sha256.Sum256([]byte(canonical))
	return base64URL(sum[:])
}

func base64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func padTo(b []byte, n int) []byte {
	if len(b) == n {
		return b
	}
	out := make([]byte, n)
	copy(out[n-len(b):], b)
	return out
}

func randomJTI() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	// Use a stable big.Int → base32-ish encoding via base64url.
	_ = big.Int{} // keep math/big import in case we later want decimal jtis
	return base64URL(buf[:]), nil
}
