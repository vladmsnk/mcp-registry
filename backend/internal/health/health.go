package health

import (
	"context"
	"database/sql"
	"time"
)

// Status reports the latest health observation for a single server.
type Status struct {
	ServerID            int64      `json:"server_id"`
	LastCheckAt         *time.Time `json:"last_check_at,omitempty"`
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LastError           string     `json:"last_error,omitempty"`
}

// Repository persists health status per server.
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// RecordSuccess marks a successful probe and clears the failure counter.
func (r *Repository) RecordSuccess(ctx context.Context, serverID int64) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO server_health (server_id, last_check_at, last_success_at, consecutive_failures, last_error)
		VALUES ($1, now(), now(), 0, '')
		ON CONFLICT (server_id) DO UPDATE
		SET last_check_at = now(),
		    last_success_at = now(),
		    consecutive_failures = 0,
		    last_error = ''`,
		serverID)
	return err
}

// RecordFailure increments the failure counter and returns the new value.
func (r *Repository) RecordFailure(ctx context.Context, serverID int64, errMsg string) (int, error) {
	var failures int
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO server_health (server_id, last_check_at, consecutive_failures, last_error)
		VALUES ($1, now(), 1, $2)
		ON CONFLICT (server_id) DO UPDATE
		SET last_check_at = now(),
		    consecutive_failures = server_health.consecutive_failures + 1,
		    last_error = $2
		RETURNING consecutive_failures`,
		serverID, errMsg).Scan(&failures)
	return failures, err
}

// Gate adapts the Repository to the hub's HealthGate interface (P3.13). It
// reports a server unhealthy when it has crossed `failureThreshold`
// consecutive failures. Servers that have never been probed are treated as
// healthy so a cold-start hub doesn't block all calls.
type Gate struct {
	repo             *Repository
	failureThreshold int
}

func NewGate(repo *Repository, failureThreshold int) *Gate {
	if failureThreshold <= 0 {
		failureThreshold = 3
	}
	return &Gate{repo: repo, failureThreshold: failureThreshold}
}

func (g *Gate) IsHealthy(ctx context.Context, serverID int64) bool {
	if g == nil {
		return true
	}
	s, err := g.repo.Get(ctx, serverID)
	if err == sql.ErrNoRows {
		return true
	}
	if err != nil {
		// Storage failure: do not block traffic on a transient DB hiccup.
		return true
	}
	return s.ConsecutiveFailures < g.failureThreshold
}

// Get returns the current Status, or zero-valued Status with sql.ErrNoRows if never probed.
func (r *Repository) Get(ctx context.Context, serverID int64) (Status, error) {
	var s Status
	s.ServerID = serverID
	err := r.db.QueryRowContext(ctx, `
		SELECT last_check_at, last_success_at, consecutive_failures, last_error
		FROM server_health WHERE server_id = $1`,
		serverID,
	).Scan(&s.LastCheckAt, &s.LastSuccessAt, &s.ConsecutiveFailures, &s.LastError)
	return s, err
}
