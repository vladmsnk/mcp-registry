package main

import (
	"database/sql"
	"log"
	"net/http"

	_ "github.com/lib/pq"

	"mcp-registry/internal/auth"
	"mcp-registry/internal/config"
	"mcp-registry/internal/embedding"
	"mcp-registry/internal/hub"
	"mcp-registry/internal/repository"
)

func main() {
	cfg := config.Load()

	db, err := sql.Open("postgres", cfg.DSN())
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("ping db: %v", err)
	}
	log.Println("hub: connected to database")

	serverRepo := repository.NewServerRepository(db)
	toolRepo := repository.NewToolRepository(db)

	// Embedding.
	var embedder hub.Embedder
	embedder, err = embedding.New(cfg.EmbeddingProvider, cfg.EmbeddingURL, cfg.EmbeddingAPIKey, cfg.EmbeddingModel, cfg.EmbeddingDims)
	if err != nil {
		log.Printf("hub: WARNING: embedding disabled: %v", err)
		embedder = nil
	} else {
		log.Printf("hub: embedding enabled (provider=%s, model=%s, dims=%d)", cfg.EmbeddingProvider, cfg.EmbeddingModel, cfg.EmbeddingDims)
	}

	// Token Exchange.
	var exchanger hub.TokenExchanger
	if cfg.AuthEnabled {
		exchanger = auth.NewTokenExchanger(cfg.KeycloakURL, cfg.KeycloakRealm, cfg.OIDCClientID, cfg.OIDCClientSecret)
		log.Println("hub: token exchange enabled")
	}

	h := hub.New(serverRepo, toolRepo, embedder, exchanger)

	// Build handler chain.
	var handler http.Handler = h
	if cfg.AuthEnabled {
		validator := auth.NewValidator(cfg.KeycloakURL, cfg.KeycloakRealm, cfg.OIDCClientID)
		handler = auth.Middleware(validator)(h)
		log.Println("hub: JWT auth enabled")
	} else {
		log.Println("hub: WARNING: auth disabled (AUTH_ENABLED=false)")
	}

	mux := http.NewServeMux()
	mux.Handle("POST /mcp", handler)

	addr := ":" + cfg.HubPort
	log.Printf("hub: MCP server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("hub: %v", err)
	}
}
