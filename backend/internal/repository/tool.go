package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/pgvector/pgvector-go"

	"mcp-registry/internal/entity"
)

type ToolRepository struct {
	db *sql.DB
}

func NewToolRepository(db *sql.DB) *ToolRepository {
	return &ToolRepository{db: db}
}

// Search finds tools matching a text query across tool names, descriptions, and server metadata.
func (r *ToolRepository) Search(ctx context.Context, query string, limit int) ([]entity.DiscoveredTool, error) {
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

	sqlQuery := `
		SELECT s.id, s.name, s.description, s.owner,
		       t.name, t.description, t.input_schema
		FROM tools t
		JOIN servers s ON s.id = t.server_id
		WHERE s.active = true
		  AND ` + strings.Join(conditions, " AND ") + `
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

// ReplaceForServer deletes all tools for a server and inserts new ones in a transaction.
func (r *ToolRepository) ReplaceForServer(ctx context.Context, serverID int64, tools []entity.Tool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM tools WHERE server_id = $1`, serverID); err != nil {
		return err
	}

	for _, t := range tools {
		schemaBytes, _ := json.Marshal(t.InputSchema)

		var embeddingVal any
		if len(t.Embedding) > 0 {
			embeddingVal = pgvector.NewVector(t.Embedding)
		}

		_, err := tx.ExecContext(ctx,
			`INSERT INTO tools (server_id, name, description, input_schema, embedding, embedding_text, embedding_model)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			serverID, t.Name, t.Description, schemaBytes,
			embeddingVal, t.EmbeddingText, t.EmbeddingModel,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// SearchByVector finds tools by cosine similarity to a query embedding.
func (r *ToolRepository) SearchByVector(ctx context.Context, queryEmbedding []float32, limit int) ([]entity.DiscoveredTool, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.name, s.description, s.owner,
		       t.name, t.description, t.input_schema
		FROM tools t
		JOIN servers s ON s.id = t.server_id
		WHERE s.active = true
		  AND t.embedding IS NOT NULL
		ORDER BY t.embedding <=> $1
		LIMIT $2`,
		pgvector.NewVector(queryEmbedding), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanDiscoveredTools(rows)
}

func scanDiscoveredTools(rows *sql.Rows) ([]entity.DiscoveredTool, error) {
	var results []entity.DiscoveredTool
	for rows.Next() {
		var d entity.DiscoveredTool
		var schema []byte
		if err := rows.Scan(
			&d.ServerID, &d.ServerName, &d.ServerDescription, &d.ServerOwner,
			&d.ToolName, &d.ToolDescription, &schema,
		); err != nil {
			return nil, err
		}
		d.InputSchema = json.RawMessage(schema)
		results = append(results, d)
	}
	if results == nil {
		results = []entity.DiscoveredTool{}
	}
	return results, rows.Err()



}
