package entity

import (
	"encoding/json"
	"time"
)

type Tool struct {
	ID          int64           `json:"id"`
	ServerID    int64           `json:"serverId"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	CreatedAt   time.Time       `json:"createdAt"`

	Embedding      []float32 `json:"-"`
	EmbeddingText  string    `json:"-"`
	EmbeddingModel string    `json:"-"`
}

// DiscoveredTool is a tool with its server context — returned by discover_tools.
type DiscoveredTool struct {
	ServerID          int64           `json:"server_id"`
	ServerName        string          `json:"server_name"`
	ServerDescription string          `json:"server_description"`
	ServerOwner       string          `json:"server_owner"`
	ToolName          string          `json:"tool_name"`
	ToolDescription   string          `json:"tool_description"`
	InputSchema       json.RawMessage `json:"input_schema"`
}
