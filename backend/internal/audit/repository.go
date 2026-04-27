package audit

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/lib/pq"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Insert(ctx context.Context, e Event) error {
	metadata, err := json.Marshal(e.Metadata)
	if err != nil || len(metadata) == 0 {
		metadata = []byte(`{}`)
	}

	var serverID any
	if e.ServerID != 0 {
		serverID = e.ServerID
	}

	roles := e.ActorRoles
	if roles == nil {
		roles = []string{}
	}

	_, err = r.db.ExecContext(ctx,
		`INSERT INTO audit_log (
			actor_sub, actor_username, actor_roles, action, status,
			server_id, tool_name, latency_ms, request_id, ip,
			user_agent, error, metadata
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		e.ActorSub, e.ActorUsername, pq.Array(roles), e.Action, e.Status,
		serverID, e.ToolName, e.LatencyMS, e.RequestID, e.IP,
		e.UserAgent, e.Error, metadata,
	)
	return err
}
