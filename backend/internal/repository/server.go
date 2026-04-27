package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"

	"mcp-registry/internal/entity"
)

type ServerRepository struct {
	db *sql.DB
}

func NewServerRepository(db *sql.DB) *ServerRepository {
	return &ServerRepository{db: db}
}

func (r *ServerRepository) List(ctx context.Context) ([]entity.Server, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, endpoint, description, owner, auth_type, tags, active, created_at,
		        keycloak_client_id, keycloak_internal_id, tls_cert_sha256, tls_cert_captured_at
		 FROM servers ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []entity.Server
	for rows.Next() {
		var s entity.Server
		var capturedAt sql.NullTime
		if err := rows.Scan(
			&s.ID, &s.Name, &s.Endpoint, &s.Description,
			&s.Owner, &s.AuthType, pq.Array(&s.Tags), &s.Active, &s.CreatedAt,
			&s.KeycloakClientID, &s.KeycloakInternalID, &s.TLSCertSHA256, &capturedAt,
		); err != nil {
			return nil, err
		}
		if capturedAt.Valid {
			t := capturedAt.Time
			s.TLSCertCapturedAt = &t
		}
		servers = append(servers, s)
	}
	return servers, rows.Err()
}

func (r *ServerRepository) Create(ctx context.Context, s *entity.Server) error {
	var capturedAt any
	if s.TLSCertSHA256 != "" {
		capturedAt = time.Now()
	}
	return r.db.QueryRowContext(ctx,
		`INSERT INTO servers (name, endpoint, description, owner, auth_type, tags, active, keycloak_client_id, keycloak_internal_id, tls_cert_sha256, tls_cert_captured_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 RETURNING id, created_at`,
		s.Name, s.Endpoint, s.Description, s.Owner, s.AuthType, pq.Array(s.Tags), s.Active,
		s.KeycloakClientID, s.KeycloakInternalID, s.TLSCertSHA256, capturedAt,
	).Scan(&s.ID, &s.CreatedAt)
}

// UpdateTLSPin replaces the pinned cert hash and stamps tls_cert_captured_at.
// Used by the operator-driven re-pin flow (P3.12). Returns sql.ErrNoRows when
// the server doesn't exist.
func (r *ServerRepository) UpdateTLSPin(ctx context.Context, serverID int64, sha256Hex string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE servers SET tls_cert_sha256 = $1, tls_cert_captured_at = now() WHERE id = $2`,
		sha256Hex, serverID,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetEndpoint returns endpoint metadata needed for outbound calls,
// including the pinned leaf-cert SHA256 (empty when unpinned).
func (r *ServerRepository) GetEndpoint(ctx context.Context, serverID int64) (endpoint, name, keycloakClientID, tlsCertSHA256 string, active bool, err error) {
	err = r.db.QueryRowContext(ctx,
		`SELECT endpoint, name, keycloak_client_id, tls_cert_sha256, active FROM servers WHERE id = $1`, serverID,
	).Scan(&endpoint, &name, &keycloakClientID, &tlsCertSHA256, &active)
	return
}

// HealthTarget is a minimal projection of a server for health probing.
type HealthTarget struct {
	ID            int64
	Name          string
	Endpoint      string
	Active        bool
	TLSCertSHA256 string
}

// ListAllForHealth returns all servers (active and inactive) for the health checker.
func (r *ServerRepository) ListAllForHealth(ctx context.Context) ([]HealthTarget, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, endpoint, active, tls_cert_sha256 FROM servers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []HealthTarget
	for rows.Next() {
		var t HealthTarget
		if err := rows.Scan(&t.ID, &t.Name, &t.Endpoint, &t.Active, &t.TLSCertSHA256); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetActive toggles the active flag for a server.
func (r *ServerRepository) SetActive(ctx context.Context, serverID int64, active bool) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE servers SET active = $1 WHERE id = $2`, active, serverID)
	return err
}

// UpdateKeycloakInternalID sets the Keycloak internal UUID for a server.
func (r *ServerRepository) UpdateKeycloakInternalID(ctx context.Context, serverID int64, keycloakInternalID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE servers SET keycloak_internal_id = $1 WHERE id = $2`,
		keycloakInternalID, serverID,
	)
	return err
}

// GetKeycloakInternalID returns the Keycloak UUID for a server. Returns sql.ErrNoRows
// if the server doesn't exist.
func (r *ServerRepository) GetKeycloakInternalID(ctx context.Context, serverID int64) (string, error) {
	var id string
	err := r.db.QueryRowContext(ctx,
		`SELECT keycloak_internal_id FROM servers WHERE id = $1`, serverID,
	).Scan(&id)
	return id, err
}

// GetOffboardingMetadata returns the data we need to enqueue retries and emit
// audit events when offboarding fails. Returns sql.ErrNoRows if the server
// doesn't exist.
func (r *ServerRepository) GetOffboardingMetadata(ctx context.Context, serverID int64) (name, keycloakClientID, keycloakInternalID string, err error) {
	err = r.db.QueryRowContext(ctx,
		`SELECT name, keycloak_client_id, keycloak_internal_id FROM servers WHERE id = $1`, serverID,
	).Scan(&name, &keycloakClientID, &keycloakInternalID)
	return
}

// Delete removes a server and returns its keycloak_internal_id for cleanup.
func (r *ServerRepository) Delete(ctx context.Context, serverID int64) (keycloakInternalID string, err error) {
	err = r.db.QueryRowContext(ctx,
		`DELETE FROM servers WHERE id = $1 RETURNING keycloak_internal_id`, serverID,
	).Scan(&keycloakInternalID)
	return
}
