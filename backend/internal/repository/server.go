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
		`SELECT id, name, endpoint, description, owner, auth_type, tags, active, created_at
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
		); err != nil {
			return nil, err
		}
		servers = append(servers, s)
	}
	return servers, rows.Err()
}

func (r *ServerRepository) Create(ctx context.Context, s *entity.Server) error {
	return r.db.QueryRowContext(ctx,
		`INSERT INTO servers (name, endpoint, description, owner, auth_type, tags, active)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, created_at`,
		s.Name, s.Endpoint, s.Description, s.Owner, s.AuthType, pq.Array(s.Tags), s.Active,
	).Scan(&s.ID, &s.CreatedAt)
}

// GetEndpoint returns the endpoint, name, and active status for a server by ID.
func (r *ServerRepository) GetEndpoint(ctx context.Context, serverID int64) (endpoint, name string, active bool, err error) {
	err = r.db.QueryRowContext(ctx,
		`SELECT endpoint, name, active FROM servers WHERE id = $1`, serverID,
	).Scan(&endpoint, &name, &active)
	return
}
