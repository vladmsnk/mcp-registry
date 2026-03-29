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
	transport "mcp-registry/internal/transport/http"
	"mcp-registry/internal/usecase"
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
	log.Println("connected to database")

	serverRepo := repository.NewServerRepository(db)
	toolRepo := repository.NewToolRepository(db)
	uc := usecase.NewServerUsecase(serverRepo)

	var embedder hub.Embedder
	embedder, err = embedding.New(cfg.EmbeddingProvider, cfg.EmbeddingURL, cfg.EmbeddingAPIKey, cfg.EmbeddingModel, cfg.EmbeddingDims)
	if err != nil {
		log.Printf("WARNING: embedding disabled: %v", err)
		embedder = nil
	} else {
		log.Printf("embedding enabled (provider=%s, model=%s, dims=%d)", cfg.EmbeddingProvider, cfg.EmbeddingModel, cfg.EmbeddingDims)
	}

	handler := transport.NewHandler(uc, serverRepo, toolRepo, embedder)

	mux := http.NewServeMux()
	handler.Register(mux)

	// Wrap with auth middleware if enabled.
	var rootHandler http.Handler = mux
	if cfg.AuthEnabled {
		validator := auth.NewValidator(cfg.KeycloakURL, cfg.KeycloakRealm, cfg.OIDCClientID)
		rootHandler = auth.Middleware(validator)(mux)
		log.Println("JWT auth enabled for REST API")
	} else {
		log.Println("WARNING: auth disabled (AUTH_ENABLED=false)")
	}

	addr := ":" + cfg.HTTPPort
	log.Printf("server listening on %s", addr)
	if err := http.ListenAndServe(addr, corsMiddleware(rootHandler)); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
