package main

import (
	"database/sql"
	"log"
	"net/http"

	_ "github.com/lib/pq"

	"mcp-registry/internal/audit"
	"mcp-registry/internal/auth"
	"mcp-registry/internal/config"
	"mcp-registry/internal/dpop"
	"mcp-registry/internal/embedding"
	"mcp-registry/internal/health"
	"mcp-registry/internal/hub"
	"mcp-registry/internal/ratelimit"
	"mcp-registry/internal/repository"
	"mcp-registry/internal/security"
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
	auditLog := audit.NewLogger(audit.NewRepository(db))

	// Embedding.
	var embedder hub.Embedder
	embedder, err = embedding.New(cfg.EmbeddingProvider, cfg.EmbeddingURL, cfg.EmbeddingAPIKey, cfg.EmbeddingModel, cfg.EmbeddingDims)
	if err != nil {
		log.Printf("hub: WARNING: embedding disabled: %v", err)
		embedder = nil
	} else {
		log.Printf("hub: embedding enabled (provider=%s, model=%s, dims=%d)", cfg.EmbeddingProvider, cfg.EmbeddingModel, cfg.EmbeddingDims)
	}

	// DPoP signer (P1.7). When enabled, exchanged tokens are bound to the
	// hub's keypair and every downstream call carries a fresh DPoP proof.
	var dpopSigner *dpop.Signer
	if cfg.DPoPEnabled {
		s, err := dpop.LoadOrGenerate(cfg.DPoPKeyPath)
		if err != nil {
			log.Fatalf("hub: load dpop key: %v", err)
		}
		dpopSigner = s
		log.Printf("hub: DPoP enabled (jkt=%s)", s.JKT())
	}

	// Token Exchange.
	var exchanger hub.TokenExchanger
	if cfg.AuthEnabled {
		if cfg.OIDCClientSecret == "" || cfg.OIDCClientSecret == "mcp-hub-secret" {
			log.Fatal("hub: OIDC_CLIENT_SECRET must be set to a real value when AUTH_ENABLED=true")
		}
		te := auth.NewTokenExchanger(cfg.KeycloakURL, cfg.KeycloakRealm, cfg.OIDCClientID, cfg.OIDCClientSecret, auditLog)
		if dpopSigner != nil {
			te = te.WithDPoP(dpopSigner)
		}
		exchanger = te
		log.Println("hub: token exchange enabled")
	}

	httpOpts := security.ClientOptions{
		BlockPrivateIPs: cfg.MCPBlockPrivateIPs,
		EgressAllowlist: cfg.MCPEgressAllowlist,
	}
	if !cfg.MCPRequireHTTPS && !cfg.MCPBlockPrivateIPs && len(cfg.MCPAllowedHosts) == 0 {
		log.Println("hub: WARNING: insecure MCP defaults — set MCP_REQUIRE_HTTPS=true and MCP_BLOCK_PRIVATE_IPS=true for production")
	}

	limiter := ratelimit.New(ratelimit.Config{
		RPS:   cfg.MCPPerServerRPS,
		Burst: cfg.MCPPerServerBurst,
	})
	defer limiter.Stop()

	healthRepo := health.NewRepository(db)
	healthGate := health.NewGate(healthRepo, cfg.HealthFailureThreshold)

	deps := hub.Deps{
		Servers:     serverRepo,
		Tools:       toolRepo,
		Embedder:    embedder,
		Exchanger:   exchanger,
		AuditLog:    auditLog,
		HTTPOpts:    httpOpts,
		RateLimiter: limiter,
		HealthGate:  healthGate,
	}
	if dpopSigner != nil {
		deps.DPoPSigner = dpopSigner
	}
	h := hub.NewFromDeps(deps)

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
