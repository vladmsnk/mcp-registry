package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/lib/pq"
	"github.com/pgvector/pgvector-go"

	"mcp-registry/internal/entity"
)

type ToolRepository struct {
	db *sql.DB
}

func NewToolRepository(db *sql.DB) *ToolRepository {
	return &ToolRepository{db: db}
}

// roleFilterClause returns "" when userRoles == nil (no filtering — admin/internal call).
// For a non-nil slice (including empty), it appends an arg and returns a SQL fragment
// allowing tools with empty required_roles or whose required_roles overlap the user's.
func roleFilterClause(userRoles []string, args *[]any, argIdx *int) string {
	if userRoles == nil {
		return ""
	}
	*args = append(*args, pq.Array(userRoles))
	clause := " AND (cardinality(t.required_roles) = 0 OR t.required_roles && $" + strconv.Itoa(*argIdx) + "::text[])"
	*argIdx++
	return clause
}

// Search finds tools matching a text query. If userRoles is non-nil, results are filtered
// to tools the user is permitted to call (empty required_roles or overlapping role).
func (r *ToolRepository) Search(ctx context.Context, query string, limit int, userRoles []string) ([]entity.DiscoveredTool, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}

	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return []entity.DiscoveredTool{}, nil
	}

	var conditions []string
	var args []any
	argIdx := 1
	for _, w := range words {
		pattern := "%" + w + "%"
		p := strconv.Itoa(argIdx)
		cond := `(t.name ILIKE $` + p +
			` OR t.description ILIKE $` + p +
			` OR s.name ILIKE $` + p +
			` OR s.description ILIKE $` + p +
			` OR array_to_string(s.tags, ' ') ILIKE $` + p + `)`
		conditions = append(conditions, cond)
		args = append(args, pattern)
		argIdx++
	}

	roleClause := roleFilterClause(userRoles, &args, &argIdx)

	sqlQuery := `
		SELECT s.id, s.name, s.description, s.owner,
		       t.name, t.description, t.input_schema, t.required_roles
		FROM tools t
		JOIN servers s ON s.id = t.server_id
		WHERE s.active = true
		  AND ` + strings.Join(conditions, " AND ") + roleClause + `
		ORDER BY t.name
		LIMIT $` + strconv.Itoa(argIdx)
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanDiscoveredTools(rows)
}

// SearchByVector finds tools by cosine similarity to a query embedding, optionally filtered by user roles.
func (r *ToolRepository) SearchByVector(ctx context.Context, queryEmbedding []float32, limit int, userRoles []string) ([]entity.DiscoveredTool, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}

	args := []any{pgvector.NewVector(queryEmbedding)}
	argIdx := 2
	roleClause := roleFilterClause(userRoles, &args, &argIdx)

	sqlQuery := `
		SELECT s.id, s.name, s.description, s.owner,
		       t.name, t.description, t.input_schema, t.required_roles
		FROM tools t
		JOIN servers s ON s.id = t.server_id
		WHERE s.active = true
		  AND t.embedding IS NOT NULL` + roleClause + `
		ORDER BY t.embedding <=> $1
		LIMIT $` + strconv.Itoa(argIdx)
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanDiscoveredTools(rows)
}

// ReplaceForServer replaces tools for a server. Existing required_roles are preserved by tool name.
func (r *ToolRepository) ReplaceForServer(ctx context.Context, serverID int64, tools []entity.Tool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Snapshot existing required_roles by tool name so we don't lose RBAC config on re-sync.
	rows, err := tx.QueryContext(ctx,
		`SELECT name, required_roles FROM tools WHERE server_id = $1`, serverID)
	if err != nil {
		return err
	}
	existingRoles := map[string][]string{}
	for rows.Next() {
		var name string
		var roles []string
		if err := rows.Scan(&name, pq.Array(&roles)); err != nil {
			rows.Close()
			return err
		}
		existingRoles[name] = roles
	}
	rows.Close()

	if _, err := tx.ExecContext(ctx, `DELETE FROM tools WHERE server_id = $1`, serverID); err != nil {
		return err
	}

	for _, t := range tools {
		schemaBytes, _ := json.Marshal(t.InputSchema)

		var embeddingVal any
		if len(t.Embedding) > 0 {
			embeddingVal = pgvector.NewVector(t.Embedding)
		}

		roles := t.RequiredRoles
		if roles == nil {
			roles = existingRoles[t.Name]
		}
		if roles == nil {
			roles = []string{}
		}

		_, err := tx.ExecContext(ctx,
			`INSERT INTO tools (server_id, name, description, input_schema, required_roles, embedding, embedding_text, embedding_model)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			serverID, t.Name, t.Description, schemaBytes, pq.Array(roles),
			embeddingVal, t.EmbeddingText, t.EmbeddingModel,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetServerRequiredRoles returns the union of required_roles across all tools
// on a server — used for audience-bound token exchange (P1.6). An empty slice
// means at least one tool is unrestricted; the caller must decide whether
// "public" is acceptable for the exchange.
func (r *ToolRepository) GetServerRequiredRoles(ctx context.Context, serverID int64) ([]string, error) {
	var roles []string
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(array_agg(DISTINCT r), '{}')
		   FROM tools t, unnest(t.required_roles) r
		  WHERE t.server_id = $1`,
		serverID,
	).Scan(pq.Array(&roles))
	if err != nil {
		return nil, err
	}
	if roles == nil {
		roles = []string{}
	}
	return roles, nil
}

// GetRequiredRoles returns the required roles for a single tool. Empty slice = unrestricted.
func (r *ToolRepository) GetRequiredRoles(ctx context.Context, serverID int64, toolName string) ([]string, error) {
	var roles []string
	err := r.db.QueryRowContext(ctx,
		`SELECT required_roles FROM tools WHERE server_id = $1 AND name = $2`,
		serverID, toolName,
	).Scan(pq.Array(&roles))
	if err != nil {
		return nil, err
	}
	if roles == nil {
		roles = []string{}
	}
	return roles, nil
}

// SetRequiredRoles updates the required roles for a tool. Returns sql.ErrNoRows if not found.
func (r *ToolRepository) SetRequiredRoles(ctx context.Context, serverID int64, toolName string, roles []string) error {
	if roles == nil {
		roles = []string{}
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE tools SET required_roles = $1 WHERE server_id = $2 AND name = $3`,
		pq.Array(roles), serverID, toolName,
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

func scanDiscoveredTools(rows *sql.Rows) ([]entity.DiscoveredTool, error) {
	var results []entity.DiscoveredTool
	for rows.Next() {
		var d entity.DiscoveredTool
		var schema []byte
		var roles []string
		if err := rows.Scan(
			&d.ServerID, &d.ServerName, &d.ServerDescription, &d.ServerOwner,
			&d.ToolName, &d.ToolDescription, &schema, pq.Array(&roles),
		); err != nil {
			return nil, err
		}
		d.InputSchema = json.RawMessage(schema)
		d.RequiredRoles = roles
		results = append(results, d)
	}
	if results == nil {
		results = []entity.DiscoveredTool{}
	}
	return results, rows.Err()
}
