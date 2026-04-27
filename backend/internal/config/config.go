package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPPort   string
	HubPort    string
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string

	EmbeddingProvider string // "ollama" or "openai"
	EmbeddingURL      string
	EmbeddingModel    string
	EmbeddingAPIKey   string
	EmbeddingDims     int

	KeycloakURL      string // e.g. "http://localhost:8180"
	KeycloakRealm    string // e.g. "mcp-registry"
	OIDCClientID     string // Hub's own client ID
	OIDCClientSecret string // Hub's own client secret
	AuthEnabled      bool   // set to false to disable auth
	CookieSecure     bool   // require HTTPS for cookies (default false for dev)
	CallbackURL      string // OIDC callback URL, e.g. "http://localhost:8080/auth/callback"
	FrontendURL      string // where to redirect after login/logout

	HealthEnabled          bool
	HealthCheckInterval    time.Duration
	HealthCheckTimeout     time.Duration
	HealthFailureThreshold int

	MCPRequireHTTPS    bool
	MCPAllowedHosts    []string
	MCPBlockPrivateIPs bool
	MCPTLSPin          bool

	// Per-server outbound rate limit (P3.13). Zero/negative disables.
	MCPPerServerRPS   float64
	MCPPerServerBurst int

	// Egress allowlist (P3.14). When non-empty, outbound MCP calls only succeed
	// if the resolved host or IP is in the list. Allows host names, exact IPs,
	// and CIDR blocks.
	MCPEgressAllowlist []string

	// Internal metrics (P3.15).
	MetricsEnabled      bool
	MetricsToken        string
	MetricsStalePinDays int

	// DPoP token binding for outbound MCP calls (P1.7).
	DPoPEnabled bool
	DPoPKeyPath string
}

func Load() Config {
	dims := 768
	if v := os.Getenv("EMBEDDING_DIMS"); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d > 0 {
			dims = d
		}
	}

	return Config{
		HTTPPort:   envOrDefault("HTTP_PORT", "8080"),
		HubPort:    envOrDefault("HUB_PORT", "8081"),
		DBHost:     envOrDefault("DB_HOST", "localhost"),
		DBPort:     envOrDefault("DB_PORT", "6521"),
		DBUser:     envOrDefault("DB_USER", "mcp"),
		DBPassword: envOrDefault("DB_PASSWORD", "mcp_secret"),
		DBName:     envOrDefault("DB_NAME", "mcp_registry"),

		KeycloakURL:      envOrDefault("KEYCLOAK_URL", "http://localhost:8180"),
		KeycloakRealm:    envOrDefault("KEYCLOAK_REALM", "mcp-registry"),
		OIDCClientID:     envOrDefault("OIDC_CLIENT_ID", "mcp-hub"),
		OIDCClientSecret: os.Getenv("OIDC_CLIENT_SECRET"),
		AuthEnabled:      envOrDefault("AUTH_ENABLED", "true") == "true",
		CookieSecure:     envOrDefault("COOKIE_SECURE", "true") == "true",
		CallbackURL:      envOrDefault("OIDC_CALLBACK_URL", "http://localhost:5173/auth/callback"),
		FrontendURL:      envOrDefault("FRONTEND_URL", "http://localhost:5173"),

		EmbeddingProvider: envOrDefault("EMBEDDING_PROVIDER", "ollama"),
		EmbeddingURL:      envOrDefault("EMBEDDING_URL", "http://localhost:11434"),
		EmbeddingModel:    envOrDefault("EMBEDDING_MODEL", "nomic-embed-text"),
		EmbeddingAPIKey:   os.Getenv("EMBEDDING_API_KEY"),
		EmbeddingDims:     dims,

		HealthEnabled:          envOrDefault("HEALTH_ENABLED", "true") == "true",
		HealthCheckInterval:    envDuration("HEALTH_CHECK_INTERVAL", 30*time.Second),
		HealthCheckTimeout:     envDuration("HEALTH_CHECK_TIMEOUT", 10*time.Second),
		HealthFailureThreshold: envInt("HEALTH_FAILURE_THRESHOLD", 3),

		MCPRequireHTTPS:    envOrDefault("MCP_REQUIRE_HTTPS", "true") == "true",
		MCPAllowedHosts:    splitCSV(os.Getenv("MCP_ALLOWED_HOSTS")),
		MCPBlockPrivateIPs: envOrDefault("MCP_BLOCK_PRIVATE_IPS", "true") == "true",
		MCPTLSPin:          envOrDefault("MCP_TLS_PIN", "true") == "true",

		MCPPerServerRPS:    envFloat("MCP_PER_SERVER_RPS", 20),
		MCPPerServerBurst:  envInt("MCP_PER_SERVER_BURST", 40),
		MCPEgressAllowlist: splitCSV(os.Getenv("MCP_EGRESS_ALLOWLIST")),

		MetricsEnabled:      envOrDefault("METRICS_ENABLED", "true") == "true",
		MetricsToken:        os.Getenv("METRICS_TOKEN"),
		MetricsStalePinDays: envInt("METRICS_STALE_PIN_DAYS", 90),

		DPoPEnabled: envOrDefault("DPOP_ENABLED", "false") == "true",
		DPoPKeyPath: envOrDefault("DPOP_KEY_PATH", ""),
	}
}

func splitCSV(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func (c Config) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName,
	)
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
