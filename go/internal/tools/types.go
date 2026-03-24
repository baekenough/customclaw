// Package tools provides the tool interface and registry for LLM tool_use integration.
package tools

// ToolDefinition describes a tool in the format expected by the Anthropic API.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ToolResult is the outcome of a single tool execution.
type ToolResult struct {
	// Content is the text payload returned to the LLM.
	Content string
	// IsError signals that the tool call failed; the LLM receives Content as an error description.
	IsError bool
}

// Tool is the interface that all runnable tools must implement.
type Tool interface {
	// Definition returns the metadata used to advertise this tool to the LLM.
	Definition() ToolDefinition
	// Execute runs the tool with the provided arguments.
	Execute(args map[string]any) ToolResult
}
