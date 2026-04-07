package repository

import (
	"context"
	"database/sql"

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
		        keycloak_client_id, keycloak_internal_id
		 FROM servers ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []entity.Server
	for rows.Next() {
		var s entity.Server
		if err := rows.Scan(
			&s.ID, &s.Name, &s.Endpoint, &s.Description,
			&s.Owner, &s.AuthType, pq.Array(&s.Tags), &s.Active, &s.CreatedAt,
			&s.KeycloakClientID, &s.KeycloakInternalID,
		); err != nil {
			return nil, err
		}
		servers = append(servers, s)
	}
	return servers, rows.Err()
}

func (r *ServerRepository) Create(ctx context.Context, s *entity.Server) error {
	return r.db.QueryRowContext(ctx,
		`INSERT INTO servers (name, endpoint, description, owner, auth_type, tags, active, keycloak_client_id, keycloak_internal_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id, created_at`,
		s.Name, s.Endpoint, s.Description, s.Owner, s.AuthType, pq.Array(s.Tags), s.Active,
		s.KeycloakClientID, s.KeycloakInternalID,
	).Scan(&s.ID, &s.CreatedAt)
}

// GetEndpoint returns the endpoint, name, keycloak client ID, and active status for a server by ID.
func (r *ServerRepository) GetEndpoint(ctx context.Context, serverID int64) (endpoint, name, keycloakClientID string, active bool, err error) {
	err = r.db.QueryRowContext(ctx,
		`SELECT endpoint, name, keycloak_client_id, active FROM servers WHERE id = $1`, serverID,
	).Scan(&endpoint, &name, &keycloakClientID, &active)
	return
}

// UpdateKeycloakInternalID sets the Keycloak internal UUID for a server.
func (r *ServerRepository) UpdateKeycloakInternalID(ctx context.Context, serverID int64, keycloakInternalID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE servers SET keycloak_internal_id = $1 WHERE id = $2`,
		keycloakInternalID, serverID,
	)
	return err
}

// Delete removes a server and returns its keycloak_internal_id for cleanup.
func (r *ServerRepository) Delete(ctx context.Context, serverID int64) (keycloakInternalID string, err error) {
	err = r.db.QueryRowContext(ctx,
		`DELETE FROM servers WHERE id = $1 RETURNING keycloak_internal_id`, serverID,
	).Scan(&keycloakInternalID)
	return
}
