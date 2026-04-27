package health

import (
	"context"
	"log"
	"sync"
	"time"

	"mcp-registry/internal/audit"
	"mcp-registry/internal/repository"
)

// Prober runs a single liveness check against an MCP endpoint. The target
// carries the pinned cert SHA256 so the prober can apply the same TLS guard
// the user-driven path applies (P1.8).
type Prober interface {
	Probe(ctx context.Context, target repository.HealthTarget) error
}

// ProberFunc adapts a function to the Prober interface.
type ProberFunc func(ctx context.Context, target repository.HealthTarget) error

func (f ProberFunc) Probe(ctx context.Context, target repository.HealthTarget) error {
	return f(ctx, target)
}

// ServerStore is the projection of ServerRepository the checker needs.
type ServerStore interface {
	ListAllForHealth(ctx context.Context) ([]repository.HealthTarget, error)
	SetActive(ctx context.Context, serverID int64, active bool) error
}

// HealthStore is the projection of the health Repository the checker needs.
type HealthStore interface {
	RecordSuccess(ctx context.Context, serverID int64) error
	RecordFailure(ctx context.Context, serverID int64, errMsg string) (int, error)
}

// Config controls checker cadence and deactivation policy.
type Config struct {
	Interval         time.Duration
	Timeout          time.Duration
	FailureThreshold int
}

// Checker probes registered servers periodically and toggles their active flag.
type Checker struct {
	cfg     Config
	servers ServerStore
	health  HealthStore
	prober  Prober
	audit   *audit.Logger
}

func NewChecker(cfg Config, servers ServerStore, health HealthStore, prober Prober, auditLog *audit.Logger) *Checker {
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	return &Checker{cfg: cfg, servers: servers, health: health, prober: prober, audit: auditLog}
}

// Run blocks until ctx is cancelled, executing checkAll on the configured interval.
// The first run happens immediately so the system has a baseline at startup.
func (c *Checker) Run(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()

	c.checkAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.checkAll(ctx)
		}
	}
}

func (c *Checker) checkAll(ctx context.Context) {
	targets, err := c.servers.ListAllForHealth(ctx)
	if err != nil {
		log.Printf("health: list servers failed: %v", err)
		return
	}

	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(t repository.HealthTarget) {
			defer wg.Done()
			c.CheckOne(ctx, t)
		}(t)
	}
	wg.Wait()
}

// CheckOne probes a single target and records the outcome. Exported so the manual
// probe endpoint can reuse exactly the same logic as the periodic checker.
func (c *Checker) CheckOne(ctx context.Context, t repository.HealthTarget) Status {
	probeCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	probeErr := c.prober.Probe(probeCtx, t)

	if probeErr == nil {
		if err := c.health.RecordSuccess(ctx, t.ID); err != nil {
			log.Printf("health: record success for server %d failed: %v", t.ID, err)
		}
		// Auto-reactivate previously-deactivated servers that come back online.
		if !t.Active {
			if err := c.servers.SetActive(ctx, t.ID, true); err != nil {
				log.Printf("health: reactivate server %d failed: %v", t.ID, err)
			} else {
				c.audit.Log(ctx, audit.Event{
					Action:   audit.ActionServerReactivated,
					Status:   audit.StatusAllowed,
					ServerID: t.ID,
					Metadata: map[string]any{"server_name": t.Name},
				})
			}
		}
		return Status{ServerID: t.ID}
	}

	failures, err := c.health.RecordFailure(ctx, t.ID, probeErr.Error())
	if err != nil {
		log.Printf("health: record failure for server %d failed: %v", t.ID, err)
	}

	// Cross the threshold and the server is currently active → deactivate.
	if t.Active && failures >= c.cfg.FailureThreshold {
		if err := c.servers.SetActive(ctx, t.ID, false); err != nil {
			log.Printf("health: deactivate server %d failed: %v", t.ID, err)
		} else {
			c.audit.Log(ctx, audit.Event{
				Action:   audit.ActionServerDeactivated,
				Status:   audit.StatusAllowed,
				ServerID: t.ID,
				Error:    probeErr.Error(),
				Metadata: map[string]any{
					"server_name":          t.Name,
					"consecutive_failures": failures,
				},
			})
		}
	}

	return Status{ServerID: t.ID, ConsecutiveFailures: failures, LastError: probeErr.Error()}
}
