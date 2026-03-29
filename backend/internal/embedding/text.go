package embedding

import (
	"encoding/json"
	"strings"
)

// BuildEmbeddingText creates a composite text from tool metadata for embedding.
// Combines tool name, description, and parameter names to capture full semantic signal.
func BuildEmbeddingText(toolName, toolDescription string, inputSchema json.RawMessage) string {
	var b strings.Builder

	b.WriteString("Tool: ")
	b.WriteString(humanize(toolName))
	b.WriteString("\n")

	if toolDescription != "" {
		b.WriteString("Description: ")
		b.WriteString(toolDescription)
		b.WriteString("\n")
	}

	params := extractParamNames(inputSchema)
	if len(params) > 0 {
		b.WriteString("Parameters: ")
		b.WriteString(strings.Join(params, ", "))
	}

	return b.String()
}

// humanize converts snake_case or camelCase to space-separated words.
func humanize(name string) string {
	// Replace underscores and hyphens with spaces.
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, "-", " ")

	// Insert spaces before uppercase letters (camelCase → camel Case).
	var b strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := rune(name[i-1])
			if prev >= 'a' && prev <= 'z' {
				b.WriteRune(' ')
			}
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// extractParamNames pulls the top-level property names from a JSON Schema.
func extractParamNames(schema json.RawMessage) []string {
	if len(schema) == 0 {
		return nil
	}

	var s struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		return nil
	}

	names := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		names = append(names, name)
	}
	return names
}
