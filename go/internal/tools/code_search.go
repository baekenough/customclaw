package tools

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
)

const (
	codeSearchModel   = "claude-sonnet-4-20250514"
	codeSearchMaxLen  = 4000
	codeSearchMaxToks = 2048
)

// SearchCodeTool performs code-aware search using the Anthropic SDK directly.
// It creates a focused LLM request with the query as the user message.
type SearchCodeTool struct {
	client anthropic.Client
}

// NewSearchCodeTool constructs a SearchCodeTool.
// The ANTHROPIC_API_KEY environment variable is read automatically.
func NewSearchCodeTool() *SearchCodeTool {
	return &SearchCodeTool{
		client: anthropic.NewClient(),
	}
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

	msg, err := t.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(codeSearchModel),
		MaxTokens: codeSearchMaxToks,
		System: []anthropic.TextBlockParam{
			{Text: system},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(query)),
		},
	})
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("code search LLM call failed: %v", err)}
	}

	var text string
	for _, block := range msg.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			text += tb.Text
		}
	}

	if len(text) > codeSearchMaxLen {
		text = text[:codeSearchMaxLen] + "\n...[truncated]"
	}

	return ToolResult{Content: text}
}
