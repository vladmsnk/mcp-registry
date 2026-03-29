package config

import (
	"fmt"
	"os"
	"strconv"
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
		OIDCClientSecret: envOrDefault("OIDC_CLIENT_SECRET", "mcp-hub-secret"),
		AuthEnabled:      envOrDefault("AUTH_ENABLED", "true") == "true",

		EmbeddingProvider: envOrDefault("EMBEDDING_PROVIDER", "ollama"),
		EmbeddingURL:      envOrDefault("EMBEDDING_URL", "http://localhost:11434"),
		EmbeddingModel:    envOrDefault("EMBEDDING_MODEL", "nomic-embed-text"),
		EmbeddingAPIKey:   os.Getenv("EMBEDDING_API_KEY"),
		EmbeddingDims:     dims,
	}
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
