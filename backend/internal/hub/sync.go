package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"mcp-registry/internal/embedding"
	"mcp-registry/internal/entity"
)

// SyncServerTools connects to a registered MCP server, lists its tools, and caches them in the DB.
func SyncServerTools(ctx context.Context, servers ServerRepo, tools ToolRepo, embedder Embedder, serverID int64) (int, error) {
	endpoint, name, _, active, err := servers.GetEndpoint(ctx, serverID)
	if err != nil {
		return 0, fmt.Errorf("lookup server: %w", err)
	}
	if !active {
		return 0, fmt.Errorf("server %q is not active", name)
	}

	sess := newMCPSession(endpoint, "")
	if err := sess.initialize(ctx); err != nil {
		return 0, fmt.Errorf("initialize %q: %w", name, err)
	}
	sess.notify(ctx, notifyInitialized)

	remoteTools, err := sess.listTools(ctx)
	if err != nil {
		return 0, fmt.Errorf("list tools on %q: %w", name, err)
	}

	entityTools := make([]entity.Tool, len(remoteTools))
	for i, t := range remoteTools {
		schemaBytes, _ := json.Marshal(t.InputSchema)
		entityTools[i] = entity.Tool{
			ServerID:    serverID,
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schemaBytes,
		}
	}

	if embedder != nil {
		texts := make([]string, len(entityTools))
		for i, t := range entityTools {
			texts[i] = embedding.BuildEmbeddingText(t.Name, t.Description, t.InputSchema)
			entityTools[i].EmbeddingText = texts[i]
		}

		vectors, embErr := embedder.EmbedBatch(ctx, texts)
		if embErr != nil {
			log.Printf("hub: embedding failed, tools stored without embeddings: %v", embErr)
		} else {
			for i := range entityTools {
				entityTools[i].Embedding = vectors[i]
				entityTools[i].EmbeddingModel = embedder.ModelName()
			}
		}
	}

	if err := tools.ReplaceForServer(ctx, serverID, entityTools); err != nil {
		return 0, fmt.Errorf("save tools: %w", err)
	}

	return len(remoteTools), nil
}
