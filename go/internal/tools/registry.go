package tools

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Registry is a thread-safe store of registered tools.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds tool to the registry, keyed by its definition name.
// If a tool with the same name already exists it is replaced.
func (r *Registry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Definition().Name] = tool
}

// Get returns the tool with the given name, or nil if not found.
func (r *Registry) Get(name string) Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// GetDefinitions returns Anthropic-compatible tool definitions.
// When enabled is non-nil, only tools whose names appear in the slice are included.
func (r *Registry) GetDefinitions(enabled []string) []ToolDefinition {
	filter := make(map[string]bool, len(enabled))
	for _, name := range enabled {
		filter[name] = true
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	defs := make([]ToolDefinition, 0, len(r.tools))
	for name, tool := range r.tools {
		if len(enabled) == 0 || filter[name] {
			defs = append(defs, tool.Definition())
		}
	}
	return defs
}

// ListNames returns the names of all registered tools.
func (r *Registry) ListNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// BuildToolDescriptions generates the natural-language tool descriptions
// injected into the LLM system prompt for limited (non-full-agent) mode.
// If enabled is non-nil, only the named tools are included.
func (r *Registry) BuildToolDescriptions(enabled []string) string {
	filter := make(map[string]bool, len(enabled))
	for _, name := range enabled {
		filter[name] = true
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	// Collect matching tools and sort for deterministic output.
	type entry struct {
		name string
		tool Tool
	}
	var entries []entry
	for name, tool := range r.tools {
		if len(enabled) == 0 || filter[name] {
			entries = append(entries, entry{name, tool})
		}
	}
	if len(entries) == 0 {
		return ""
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	var sb strings.Builder
	sb.WriteString("You have access to the following tools. To use a tool, include a JSON block in your response:\n")
	sb.WriteString("```json\n{\"tool_call\": {\"name\": \"tool_name\", \"arguments\": {\"arg1\": \"value1\"}}}\n```\n\n")
	sb.WriteString("Available tools:\n")

	for _, e := range entries {
		def := e.tool.Definition()
		fmt.Fprintf(&sb, "### %s\n%s\n", def.Name, def.Description)

		// Extract parameter descriptions from the JSON Schema.
		if props, ok := def.InputSchema["properties"].(map[string]any); ok {
			required := make(map[string]bool)
			if req, ok := def.InputSchema["required"].([]string); ok {
				for _, r := range req {
					required[r] = true
				}
			} else if req, ok := def.InputSchema["required"].([]any); ok {
				for _, r := range req {
					if s, ok := r.(string); ok {
						required[s] = true
					}
				}
			}

			// Sort parameter names for deterministic output.
			paramNames := make([]string, 0, len(props))
			for name := range props {
				paramNames = append(paramNames, name)
			}
			sort.Strings(paramNames)

			if len(paramNames) > 0 {
				sb.WriteString("Parameters:\n")
				for _, param := range paramNames {
					propMap, _ := props[param].(map[string]any)
					desc, _ := propMap["description"].(string)
					req := ""
					if required[param] {
						req = " (required)"
					}
					fmt.Fprintf(&sb, "  - %s: %s%s\n", param, desc, req)
				}
			}
		}
		sb.WriteString("\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}
