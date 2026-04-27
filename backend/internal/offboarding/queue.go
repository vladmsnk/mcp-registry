// Package offboarding owns the retry queue for Keycloak cleanup operations
// that failed during DELETE /api/servers/{id}. The local DB row is gone by
// the time we get here, so we persist enough metadata (keycloak_internal_id,
// client_id, server_name) to resume the cleanup from a worker.
package offboarding

import (
	"context"
	"time"
)

// Op identifies which Keycloak call to retry.
type Op string

const (
	OpRevokeTokens Op = "revoke_tokens"
	OpDeleteClient Op = "delete_client"
)

// Job is a queued retry. Attempts is incremented on each failure; NextAttemptAt
// implements exponential backoff. CompletedAt!=nil means the job is done.
type Job struct {
	ID                 int64
	Op                 Op
	KeycloakInternalID string
	KeycloakClientID   string
	ServerID           int64
	ServerName         string
	Attempts           int
	LastError          string
	NextAttemptAt      time.Time
	CreatedAt          time.Time
}

// Queue is the durable store of pending offboarding work.
type Queue interface {
	Enqueue(ctx context.Context, j Job) (int64, error)
	ClaimDue(ctx context.Context, now time.Time, limit int) ([]Job, error)
	MarkSuccess(ctx context.Context, id int64) error
	MarkFailed(ctx context.Context, id int64, err string, nextAttempt time.Time) error
}
