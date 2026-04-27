package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/lib/pq"

	"mcp-registry/internal/audit"
	"mcp-registry/internal/auth"
	"mcp-registry/internal/config"
	"mcp-registry/internal/embedding"
	"mcp-registry/internal/health"
	"mcp-registry/internal/hub"
	"mcp-registry/internal/keycloak"
	"mcp-registry/internal/metrics"
	"mcp-registry/internal/offboarding"
	"mcp-registry/internal/repository"
	"mcp-registry/internal/security"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverRepo := repository.NewServerRepository(db)
	toolRepo := repository.NewToolRepository(db)
	offboardingRepo := repository.NewOffboardingRepository(db)
	auditRepo := audit.NewRepository(db)
	auditLog := audit.NewLogger(auditRepo)

	var provisioner usecase.ClientProvisioner
	var kcAdmin *keycloak.AdminClient
	if cfg.AuthEnabled {
		if cfg.OIDCClientSecret == "" {
			log.Fatal("OIDC_CLIENT_SECRET is required when AUTH_ENABLED=true; set it in your environment")
		}
		if cfg.OIDCClientSecret == "mcp-hub-secret" {
			log.Fatal("OIDC_CLIENT_SECRET is set to the documented placeholder; rotate it before starting")
		}
		kcAdmin = keycloak.NewAdminClient(cfg.KeycloakURL, cfg.KeycloakRealm, cfg.OIDCClientID, cfg.OIDCClientSecret)
		kcAdmin.SetAuditFn(func(c context.Context, fields map[string]any) {
			auditLog.Log(c, audit.Event{
				Action:   "keycloak.admin_token",
				Status:   audit.StatusAllowed,
				Metadata: fields,
			})
		})
		provisioner = kcAdmin
		log.Println("keycloak client auto-provisioning enabled")
	}

	// AllowedHosts pre-filters candidate registrations on the *user-supplied*
	// hostname; the egress allowlist is enforced post-DNS at dial time.
	urlValidator := &security.URLValidator{
		RequireHTTPS:    cfg.MCPRequireHTTPS,
		AllowedHosts:    cfg.MCPAllowedHosts,
		BlockPrivateIPs: cfg.MCPBlockPrivateIPs,
	}
	httpOpts := security.ClientOptions{
		BlockPrivateIPs: cfg.MCPBlockPrivateIPs,
		EgressAllowlist: cfg.MCPEgressAllowlist,
	}

	logInsecureOverrides(cfg)
	log.Printf("MCP endpoint policy: require_https=%t block_private_ips=%t allowed_hosts=%v tls_pin=%t cookie_secure=%t",
		cfg.MCPRequireHTTPS, cfg.MCPBlockPrivateIPs, cfg.MCPAllowedHosts, cfg.MCPTLSPin, cfg.CookieSecure)

	uc := usecase.NewServerUsecase(usecase.Deps{
		Repo:             serverRepo,
		Provisioner:      provisioner,
		OffboardingQueue: offboardingRepo,
		AuditLog:         auditLog,
		URLValidator:     urlValidator,
		TLSPin:           cfg.MCPTLSPin,
	})

	if kcAdmin != nil {
		worker := offboarding.NewWorker(offboardingRepo, kcAdmin, auditLog, offboarding.Config{})
		go worker.Run(ctx)
		log.Println("offboarding retry worker started")
	}

	var embedder hub.Embedder
	embedder, err = embedding.New(cfg.EmbeddingProvider, cfg.EmbeddingURL, cfg.EmbeddingAPIKey, cfg.EmbeddingModel, cfg.EmbeddingDims)
	if err != nil {
		log.Printf("WARNING: embedding disabled: %v", err)
		embedder = nil
	} else {
		log.Printf("embedding enabled (provider=%s, model=%s, dims=%d)", cfg.EmbeddingProvider, cfg.EmbeddingModel, cfg.EmbeddingDims)
	}

	healthRepo := health.NewRepository(db)

	// Probes use the hub's service-account token when KC is wired up (P1.8).
	// A downstream MCP server can choose to validate the bearer; even if it
	// doesn't, the probe is no longer anonymous and is correlatable in logs.
	var probeTokenSource hub.ProbeTokenSource
	if kcAdmin != nil {
		probeTokenSource = func(c context.Context) (string, error) {
			return kcAdmin.ServiceAccountToken(c)
		}
	}
	healthChecker := health.NewChecker(
		health.Config{
			Interval:         cfg.HealthCheckInterval,
			Timeout:          cfg.HealthCheckTimeout,
			FailureThreshold: cfg.HealthFailureThreshold,
		},
		serverRepo, healthRepo,
		health.ProberFunc(func(ctx context.Context, t repository.HealthTarget) error {
			return hub.ProbeMCPServer(ctx, t.Endpoint, httpOpts, hub.ProbeOptions{
				PinSHA256:   t.TLSCertSHA256,
				TokenSource: probeTokenSource,
			})
		}),
		auditLog,
	)

	if cfg.HealthEnabled {
		go healthChecker.Run(ctx)
		log.Printf("health checker enabled (interval=%s, timeout=%s, failures_threshold=%d)",
			cfg.HealthCheckInterval, cfg.HealthCheckTimeout, cfg.HealthFailureThreshold)
	} else {
		log.Println("health checker disabled (HEALTH_ENABLED=false)")
	}

	handler := transport.NewHandler(uc, serverRepo, toolRepo, toolRepo, embedder, auditLog, healthChecker, healthRepo, httpOpts)

	mux := http.NewServeMux()

	if cfg.AuthEnabled {
		validator := auth.NewValidator(cfg.KeycloakURL, cfg.KeycloakRealm, cfg.OIDCClientID)
		authMw := auth.Middleware(validator)

		oidc := auth.NewOIDCHandler(
			cfg.KeycloakURL, cfg.KeycloakRealm,
			cfg.OIDCClientID, cfg.OIDCClientSecret,
			cfg.CallbackURL, validator, cfg.CookieSecure, cfg.FrontendURL,
		)

		// Public OIDC endpoints (no auth required).
		mux.HandleFunc("GET /auth/login", oidc.HandleLogin)
		mux.HandleFunc("GET /auth/callback", oidc.HandleCallback)
		mux.HandleFunc("GET /auth/logout", oidc.HandleLogout)
		mux.HandleFunc("POST /auth/refresh", oidc.HandleRefresh)

		// /auth/me needs the token in context.
		mux.Handle("GET /auth/me", authMw(http.HandlerFunc(oidc.HandleMe)))

		// API routes behind auth.
		apiMux := http.NewServeMux()
		handler.Register(apiMux)
		mux.Handle("/api/", authMw(apiMux))

		log.Println("OIDC SSO enabled (login at /auth/login)")
	} else {
		handler.Register(mux)
		log.Println("WARNING: auth disabled (AUTH_ENABLED=false)")
	}

	if cfg.MetricsEnabled {
		var adminStatus metrics.AdminClientStatus
		if kcAdmin != nil {
			adminStatus = kcAdmin
		}
		mux.Handle("GET /internal/metrics", metrics.NewHandler(db, adminStatus, cfg.MetricsStalePinDays, cfg.MetricsToken))
		if cfg.MetricsToken == "" {
			log.Println("WARNING: /internal/metrics has no METRICS_TOKEN — restrict access via network policy")
		} else {
			log.Println("metrics endpoint exposed at /internal/metrics (token-gated)")
		}
	}

	addr := ":" + cfg.HTTPPort
	log.Printf("server listening on %s", addr)
	if err := http.ListenAndServe(addr, corsMiddleware(cfg.FrontendURL, mux)); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

// logInsecureOverrides surfaces any operator opt-out from secure-by-default settings.
// Triggers loudly so dev convenience flags do not silently ship to production.
func logInsecureOverrides(cfg config.Config) {
	var overrides []string
	if !cfg.CookieSecure {
		overrides = append(overrides, "COOKIE_SECURE=false")
	}
	if !cfg.MCPRequireHTTPS {
		overrides = append(overrides, "MCP_REQUIRE_HTTPS=false")
	}
	if !cfg.MCPBlockPrivateIPs {
		overrides = append(overrides, "MCP_BLOCK_PRIVATE_IPS=false")
	}
	if !cfg.MCPTLSPin {
		overrides = append(overrides, "MCP_TLS_PIN=false")
	}
	if len(overrides) > 0 {
		log.Printf("WARNING: insecure overrides active (%v) — do not run this configuration in production", overrides)
	}
}

func corsMiddleware(allowedOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
