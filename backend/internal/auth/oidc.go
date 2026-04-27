package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// OIDCHandler implements the Authorization Code flow with PKCE using Keycloak.
type OIDCHandler struct {
	keycloakURL  string
	realm        string
	clientID     string
	clientSecret string
	callbackURL  string
	frontendURL  string
	validator    *Validator
	cookieSecure bool
}

func NewOIDCHandler(keycloakURL, realm, clientID, clientSecret, callbackURL string, validator *Validator, cookieSecure bool, frontendURL string) *OIDCHandler {
	return &OIDCHandler{
		keycloakURL:  strings.TrimRight(keycloakURL, "/"),
		realm:        realm,
		clientID:     clientID,
		clientSecret: clientSecret,
		callbackURL:  callbackURL,
		frontendURL:  frontendURL,
		validator:    validator,
		cookieSecure: cookieSecure,
	}
}

func (h *OIDCHandler) authorizeURL() string {
	return fmt.Sprintf("%s/realms/%s/protocol/openid-connect/auth", h.keycloakURL, h.realm)
}

func (h *OIDCHandler) tokenURL() string {
	return fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", h.keycloakURL, h.realm)
}

func (h *OIDCHandler) endSessionURL() string {
	return fmt.Sprintf("%s/realms/%s/protocol/openid-connect/logout", h.keycloakURL, h.realm)
}

// HandleLogin generates state + PKCE, stores them in a cookie, and redirects to Keycloak.
func (h *OIDCHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := generateState()
	if err != nil {
		http.Error(w, `{"error":"failed to generate state"}`, http.StatusInternalServerError)
		return
	}

	verifier, err := generateCodeVerifier()
	if err != nil {
		http.Error(w, `{"error":"failed to generate PKCE verifier"}`, http.StatusInternalServerError)
		return
	}

	challenge := computeCodeChallenge(verifier)

	stateData, _ := json.Marshal(oidcStateCookie{State: state, CodeVerifier: verifier})
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    base64.RawURLEncoding.EncodeToString(stateData),
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		Path:     "/auth/callback",
		MaxAge:   300,
	})

	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {h.clientID},
		"redirect_uri":          {h.callbackURL},
		"scope":                 {"openid profile email"},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}

	http.Redirect(w, r, h.authorizeURL()+"?"+params.Encode(), http.StatusFound)
}

// HandleCallback exchanges the authorization code for tokens and sets session cookies.
func (h *OIDCHandler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		http.Error(w, `{"error":"missing code or state"}`, http.StatusBadRequest)
		return
	}

	stateCookie, err := r.Cookie("oidc_state")
	if err != nil {
		http.Error(w, `{"error":"missing state cookie"}`, http.StatusBadRequest)
		return
	}

	// Clear the state cookie immediately.
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    "",
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		Path:     "/auth/callback",
		MaxAge:   -1,
	})

	decoded, err := base64.RawURLEncoding.DecodeString(stateCookie.Value)
	if err != nil {
		http.Error(w, `{"error":"invalid state cookie encoding"}`, http.StatusBadRequest)
		return
	}

	var saved oidcStateCookie
	if err := json.Unmarshal(decoded, &saved); err != nil {
		http.Error(w, `{"error":"invalid state cookie"}`, http.StatusBadRequest)
		return
	}

	if saved.State != state {
		http.Error(w, `{"error":"state mismatch"}`, http.StatusBadRequest)
		return
	}

	// Exchange authorization code for tokens.
	tokens, err := h.exchangeCode(code, saved.CodeVerifier)
	if err != nil {
		log.Printf("OIDC token exchange failed: %v", err)
		http.Error(w, `{"error":"token exchange failed"}`, http.StatusBadGateway)
		return
	}

	// Validate the access token before trusting it.
	if _, err := h.validator.Validate(r.Context(), tokens.AccessToken); err != nil {
		log.Printf("OIDC access token validation failed: %v", err)
		http.Error(w, `{"error":"token validation failed"}`, http.StatusBadGateway)
		return
	}

	h.setSessionCookies(w, tokens)
	http.Redirect(w, r, h.frontendURL, http.StatusFound)
}

// HandleLogout clears session cookies and redirects to Keycloak logout.
func (h *OIDCHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: "access_token", Value: "", HttpOnly: true,
		Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode,
		Path: "/", MaxAge: -1,
	})
	http.SetCookie(w, &http.Cookie{
		Name: "refresh_token", Value: "", HttpOnly: true,
		Secure: h.cookieSecure, SameSite: http.SameSiteStrictMode,
		Path: "/auth/refresh", MaxAge: -1,
	})

	params := url.Values{
		"client_id":                {h.clientID},
		"post_logout_redirect_uri": {h.frontendURL},
	}

	http.Redirect(w, r, h.endSessionURL()+"?"+params.Encode(), http.StatusFound)
}

// HandleMe returns the current user's info from the validated token in context.
func (h *OIDCHandler) HandleMe(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFromContext(r.Context())
	if claims == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"not authenticated"}`)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"sub":                claims.Subject,
		"preferred_username": claims.PreferredUsername,
		"email":              claims.Email,
		"realm_roles":        claims.RealmRoles,
	})
}

// HandleRefresh uses the refresh_token cookie to obtain a new access token.
func (h *OIDCHandler) HandleRefresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("refresh_token")
	if err != nil || cookie.Value == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"no refresh token"}`)
		return
	}

	tokens, err := h.refreshToken(cookie.Value)
	if err != nil {
		log.Printf("OIDC token refresh failed: %v", err)
		// Clear stale cookies on refresh failure.
		http.SetCookie(w, &http.Cookie{
			Name: "access_token", Value: "", HttpOnly: true,
			Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode,
			Path: "/", MaxAge: -1,
		})
		http.SetCookie(w, &http.Cookie{
			Name: "refresh_token", Value: "", HttpOnly: true,
			Secure: h.cookieSecure, SameSite: http.SameSiteStrictMode,
			Path: "/auth/refresh", MaxAge: -1,
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"refresh failed"}`)
		return
	}

	if _, err := h.validator.Validate(r.Context(), tokens.AccessToken); err != nil {
		log.Printf("OIDC refreshed token validation failed: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `{"error":"token validation failed"}`)
		return
	}

	h.setSessionCookies(w, tokens)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"expires_in": tokens.ExpiresIn,
	})
}

// --- Token exchange helpers ---

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
}

func (h *OIDCHandler) exchangeCode(code, codeVerifier string) (*tokenResponse, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {h.callbackURL},
		"client_id":     {h.clientID},
		"client_secret": {h.clientSecret},
		"code_verifier": {codeVerifier},
	}
	return h.postToken(data)
}

func (h *OIDCHandler) refreshToken(refreshToken string) (*tokenResponse, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {h.clientID},
		"client_secret": {h.clientSecret},
	}
	return h.postToken(data)
}

func (h *OIDCHandler) postToken(data url.Values) (*tokenResponse, error) {
	resp, err := http.PostForm(h.tokenURL(), data)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, body)
	}

	var tokens tokenResponse
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	return &tokens, nil
}

func (h *OIDCHandler) setSessionCookies(w http.ResponseWriter, tokens *tokenResponse) {
	http.SetCookie(w, &http.Cookie{
		Name:     "access_token",
		Value:    tokens.AccessToken,
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		MaxAge:   tokens.ExpiresIn,
	})
	if tokens.RefreshToken != "" {
		maxAge := tokens.RefreshExpiresIn
		if maxAge == 0 {
			maxAge = 1800
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "refresh_token",
			Value:    tokens.RefreshToken,
			HttpOnly: true,
			Secure:   h.cookieSecure,
			SameSite: http.SameSiteStrictMode,
			Path:     "/auth/refresh",
			MaxAge:   maxAge,
		})
	}
}

// --- PKCE helpers ---

type oidcStateCookie struct {
	State        string `json:"state"`
	CodeVerifier string `json:"code_verifier"`
}

func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func generateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func computeCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

