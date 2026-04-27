package repository

import (
	"context"
	"database/sql"
	"time"

	"mcp-registry/internal/offboarding"
)

type OffboardingRepository struct {
	db *sql.DB
}

func NewOffboardingRepository(db *sql.DB) *OffboardingRepository {
	return &OffboardingRepository{db: db}
}

func (r *OffboardingRepository) Enqueue(ctx context.Context, j offboarding.Job) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO offboarding_queue
			(op, keycloak_internal_id, keycloak_client_id, server_id, server_name, last_error, next_attempt_at)
		 VALUES ($1, $2, $3, NULLIF($4, 0), $5, $6, $7)
		 RETURNING id`,
		string(j.Op), j.KeycloakInternalID, j.KeycloakClientID, j.ServerID, j.ServerName,
		j.LastError, j.NextAttemptAt,
	).Scan(&id)
	return id, err
}

// ClaimDue returns up to `limit` due jobs and atomically defers them so a
// concurrent worker won't pick them up. Failure path resets next_attempt_at.
func (r *OffboardingRepository) ClaimDue(ctx context.Context, now time.Time, limit int) ([]offboarding.Job, error) {
	// SKIP LOCKED keeps multiple workers safe; the UPDATE ... RETURNING pattern
	// avoids a separate SELECT+UPDATE race.
	rows, err := r.db.QueryContext(ctx, `
		UPDATE offboarding_queue
		SET next_attempt_at = $1
		WHERE id IN (
			SELECT id FROM offboarding_queue
			WHERE completed_at IS NULL AND next_attempt_at <= $1
			ORDER BY next_attempt_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, op, keycloak_internal_id, keycloak_client_id,
		          COALESCE(server_id, 0), server_name,
		          attempts, last_error, next_attempt_at, created_at
	`, now.Add(5*time.Minute), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []offboarding.Job
	for rows.Next() {
		var j offboarding.Job
		var op string
		if err := rows.Scan(
			&j.ID, &op, &j.KeycloakInternalID, &j.KeycloakClientID,
			&j.ServerID, &j.ServerName,
			&j.Attempts, &j.LastError, &j.NextAttemptAt, &j.CreatedAt,
		); err != nil {
			return nil, err
		}
		j.Op = offboarding.Op(op)
		out = append(out, j)
	}
	return out, rows.Err()
}

func (r *OffboardingRepository) MarkSuccess(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE offboarding_queue
		   SET completed_at = now(), last_error = ''
		 WHERE id = $1`, id)
	return err
}

func (r *OffboardingRepository) MarkFailed(ctx context.Context, id int64, errMsg string, nextAttempt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE offboarding_queue
		   SET attempts = attempts + 1,
		       last_error = $2,
		       next_attempt_at = $3
		 WHERE id = $1`,
		id, errMsg, nextAttempt)
	return err
}
