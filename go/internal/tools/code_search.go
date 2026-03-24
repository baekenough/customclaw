package tools

import (
	"context"
	"fmt"

	"github.com/baekenough/customclaw/internal/llm"
)

const (
	codeSearchMaxLen = 4000
)

// SearchCodeTool performs code-aware search using the configured LLM provider.
// It creates a focused LLM request with the query as the user message.
type SearchCodeTool struct {
	provider llm.Provider
}

// NewSearchCodeTool constructs a SearchCodeTool backed by the Claude CLI
// subprocess provider. The CLI binary path is read from CLAUDE_CLI_PATH
// (default: "claude").
func NewSearchCodeTool() *SearchCodeTool {
	return &SearchCodeTool{
		provider: llm.NewClaudeProvider(),
	}
}

// NewSearchCodeToolWithProvider constructs a SearchCodeTool with an explicit
// provider. This is primarily useful for testing.
func NewSearchCodeToolWithProvider(p llm.Provider) *SearchCodeTool {
	return &SearchCodeTool{provider: p}
}

func (t *SearchCodeTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "search_code",
		Description: "Search and analyse code in the repository using AI. Returns a summary and relevant code snippets.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The code search query or question to answer about the codebase",
				},
				"repo_path": map[string]any{
					"type":        "string",
					"description": "Path to the repository root (overrides config)",
				},
			},
			"required": []string{"query"},
		},
	}
}

func (t *SearchCodeTool) Execute(args map[string]any) ToolResult {
	return t.ExecuteWithContext(context.Background(), args, "")
}

// ExecuteWithContext performs the code search with the given repository path.
func (t *SearchCodeTool) ExecuteWithContext(ctx context.Context, args map[string]any, repoPath string) ToolResult {
	query, _ := args["query"].(string)
	if query == "" {
		return ToolResult{IsError: true, Content: "query is required"}
	}

	// Allow per-call repo_path override.
	if rp, ok := args["repo_path"].(string); ok && rp != "" {
		repoPath = rp
	}

	system := "You are a code search assistant. Answer questions about code concisely. " +
		"Provide relevant code snippets and explain what you find."
	if repoPath != "" {
		system += fmt.Sprintf(" The repository is located at: %s", repoPath)
	}

	resp, err := t.provider.Complete(ctx, &llm.Request{
		SystemPrompt: system,
		UserMessage:  query,
		Model:        "sonnet",
	})
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("code search LLM call failed: %v", err)}
	}

	text := resp.Text
	if len(text) > codeSearchMaxLen {
		text = text[:codeSearchMaxLen] + "\n...[truncated]"
	}

	return ToolResult{Content: text}
}
