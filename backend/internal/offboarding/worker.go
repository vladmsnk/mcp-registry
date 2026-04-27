package offboarding

import (
	"context"
	"log"
	"math"
	"time"

	"mcp-registry/internal/audit"
)

// Provisioner is the subset of keycloak.AdminClient the worker needs.
type Provisioner interface {
	RevokeAllTokens(ctx context.Context, keycloakInternalID string) error
	DeleteClient(ctx context.Context, keycloakInternalID string) error
}

// Worker periodically retries offboarding jobs until they succeed or hit MaxAttempts.
type Worker struct {
	queue        Queue
	provisioner  Provisioner
	audit        *audit.Logger
	pollInterval time.Duration
	maxAttempts  int
	baseBackoff  time.Duration
	maxBackoff   time.Duration
}

type Config struct {
	PollInterval time.Duration // how often to poll the queue (default 30s)
	MaxAttempts  int           // give up after this many failures (default 12)
	BaseBackoff  time.Duration // first retry delay (default 30s)
	MaxBackoff   time.Duration // cap on exponential backoff (default 1h)
}

func NewWorker(q Queue, p Provisioner, a *audit.Logger, cfg Config) *Worker {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 30 * time.Second
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 12
	}
	if cfg.BaseBackoff == 0 {
		cfg.BaseBackoff = 30 * time.Second
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = time.Hour
	}
	return &Worker{
		queue:        q,
		provisioner:  p,
		audit:        a,
		pollInterval: cfg.PollInterval,
		maxAttempts:  cfg.MaxAttempts,
		baseBackoff:  cfg.BaseBackoff,
		maxBackoff:   cfg.MaxBackoff,
	}
}

// Run blocks until ctx is cancelled, polling and processing due jobs.
func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(w.pollInterval)
	defer t.Stop()

	// Drain on startup so a crash during a previous tick doesn't leave jobs idle
	// for a full poll interval.
	w.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

func (w *Worker) tick(ctx context.Context) {
	jobs, err := w.queue.ClaimDue(ctx, time.Now(), 16)
	if err != nil {
		log.Printf("offboarding: claim failed: %v", err)
		return
	}
	for _, j := range jobs {
		w.process(ctx, j)
	}
}

func (w *Worker) process(ctx context.Context, j Job) {
	var opErr error
	switch j.Op {
	case OpRevokeTokens:
		opErr = w.provisioner.RevokeAllTokens(ctx, j.KeycloakInternalID)
	case OpDeleteClient:
		opErr = w.provisioner.DeleteClient(ctx, j.KeycloakInternalID)
	default:
		opErr = errUnknownOp(j.Op)
	}

	if opErr == nil {
		if err := w.queue.MarkSuccess(ctx, j.ID); err != nil {
			log.Printf("offboarding: mark success failed for job %d: %v", j.ID, err)
		}
		w.emit(ctx, j, audit.StatusAllowed, "")
		log.Printf("offboarding: %s succeeded for kc_client_id=%s (server=%s, attempts=%d)",
			j.Op, j.KeycloakClientID, j.ServerName, j.Attempts+1)
		return
	}

	attempts := j.Attempts + 1
	next := w.backoff(attempts)
	if attempts >= w.maxAttempts {
		// Park the job at +24h so it stops churning, but keep it visible for ops.
		// Marking it "completed" would hide a real cleanup failure.
		next = time.Now().Add(24 * time.Hour)
		log.Printf("offboarding: %s exceeded max attempts (%d) for kc_client_id=%s — parking 24h: %v",
			j.Op, w.maxAttempts, j.KeycloakClientID, opErr)
		w.emit(ctx, j, audit.StatusError, "max attempts exceeded: "+opErr.Error())
	}
	if err := w.queue.MarkFailed(ctx, j.ID, opErr.Error(), next); err != nil {
		log.Printf("offboarding: mark failed for job %d: %v", j.ID, err)
	}
}

func (w *Worker) backoff(attempts int) time.Time {
	d := time.Duration(math.Pow(2, float64(attempts))) * w.baseBackoff
	if d > w.maxBackoff || d <= 0 {
		d = w.maxBackoff
	}
	return time.Now().Add(d)
}

func (w *Worker) emit(ctx context.Context, j Job, status, errMsg string) {
	if w.audit == nil {
		return
	}
	w.audit.Log(ctx, audit.Event{
		Action:   audit.ActionServerDelete,
		Status:   status,
		ServerID: j.ServerID,
		Error:    errMsg,
		Metadata: map[string]any{
			"phase":                "offboarding_worker",
			"op":                   string(j.Op),
			"keycloak_client_id":   j.KeycloakClientID,
			"keycloak_internal_id": j.KeycloakInternalID,
			"server_name":          j.ServerName,
			"attempts":             j.Attempts + 1,
		},
	})
}

type errUnknownOp Op

func (e errUnknownOp) Error() string { return "offboarding: unknown op: " + string(e) }
