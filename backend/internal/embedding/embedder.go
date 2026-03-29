package embedding

import (
	"context"
	"fmt"
)

// Embedder generates vector embeddings from text.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	ModelName() string
	Dimensions() int
}

// New creates an Embedder for the given provider ("ollama" or "openai").
func New(provider, url, apiKey, model string, dims int) (Embedder, error) {
	switch provider {
	case "ollama":
		return newOllama(url, model, dims), nil
	case "openai":
		if apiKey == "" {
			return nil, fmt.Errorf("EMBEDDING_API_KEY is required for openai provider")
		}
		return newOpenAI(url, apiKey, model, dims), nil
	default:
		return nil, fmt.Errorf("unknown embedding provider: %q (supported: ollama, openai)", provider)
	}
}
