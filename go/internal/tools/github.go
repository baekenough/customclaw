package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const githubAPIBase = "https://api.github.com"

// CreateIssueTool creates a GitHub issue via the REST API.
type CreateIssueTool struct{}

func (t *CreateIssueTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "create_issue",
		Description: "Create a new GitHub issue in the configured repository.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "Issue title",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "Issue body (markdown supported)",
				},
				"labels": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Labels to apply to the issue",
				},
			},
			"required": []string{"title"},
		},
	}
}

func (t *CreateIssueTool) Execute(args map[string]any) ToolResult {
	return ToolResult{IsError: true, Content: "use ExecuteWithContext for GitHub tools"}
}

// ExecuteWithContext creates a GitHub issue using the provided repo and token.
func (t *CreateIssueTool) ExecuteWithContext(_ context.Context, args map[string]any, repo, token string) ToolResult {
	if repo == "" || token == "" {
		return ToolResult{IsError: true, Content: "github repo and token are required"}
	}

	title, _ := args["title"].(string)
	if title == "" {
		return ToolResult{IsError: true, Content: "title is required"}
	}

	body := map[string]any{"title": title}
	if v, ok := args["body"].(string); ok {
		body["body"] = v
	}
	if labels, ok := args["labels"].([]any); ok {
		strs := make([]string, 0, len(labels))
		for _, l := range labels {
			if s, ok := l.(string); ok {
				strs = append(strs, s)
			}
		}
		body["labels"] = strs
	}

	result, err := githubRequest(token, http.MethodPost,
		fmt.Sprintf("%s/repos/%s/issues", githubAPIBase, repo), body)
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("create issue failed: %v", err)}
	}

	number, _ := result["number"].(float64)
	htmlURL, _ := result["html_url"].(string)
	return ToolResult{Content: fmt.Sprintf("Issue #%.0f created: %s", number, htmlURL)}
}

// QueryIssuesTool lists GitHub issues with optional filters.
type QueryIssuesTool struct{}

func (t *QueryIssuesTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "query_issues",
		Description: "Query GitHub issues in the configured repository.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"state": map[string]any{
					"type":        "string",
					"description": "Filter by state: open, closed, or all (default: open)",
					"enum":        []string{"open", "closed", "all"},
				},
				"labels": map[string]any{
					"type":        "string",
					"description": "Comma-separated list of label names to filter by",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "Text to search for in issue title or body",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of issues to return (default: 10)",
				},
			},
		},
	}
}

func (t *QueryIssuesTool) Execute(args map[string]any) ToolResult {
	return ToolResult{IsError: true, Content: "use ExecuteWithContext for GitHub tools"}
}

// ExecuteWithContext queries issues from GitHub using the provided repo and token.
func (t *QueryIssuesTool) ExecuteWithContext(_ context.Context, args map[string]any, repo, token string) ToolResult {
	if repo == "" || token == "" {
		return ToolResult{IsError: true, Content: "github repo and token are required"}
	}

	state := "open"
	if s, ok := args["state"].(string); ok && s != "" {
		state = s
	}
	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	params := url.Values{}
	params.Set("state", state)
	params.Set("per_page", fmt.Sprintf("%d", limit))
	if labels, ok := args["labels"].(string); ok && labels != "" {
		params.Set("labels", labels)
	}

	apiURL := fmt.Sprintf("%s/repos/%s/issues?%s", githubAPIBase, repo, params.Encode())
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("build request: %v", err)}
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("query issues: %v", err)}
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return ToolResult{IsError: true, Content: fmt.Sprintf("github API %d: %s", resp.StatusCode, data)}
	}

	var issues []map[string]any
	if err := json.Unmarshal(data, &issues); err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("parse response: %v", err)}
	}

	// Filter by query text if provided.
	query, _ := args["query"].(string)
	var out strings.Builder
	count := 0
	for _, issue := range issues {
		title, _ := issue["title"].(string)
		body, _ := issue["body"].(string)
		if query != "" {
			if !strings.Contains(strings.ToLower(title), strings.ToLower(query)) &&
				!strings.Contains(strings.ToLower(body), strings.ToLower(query)) {
				continue
			}
		}
		number, _ := issue["number"].(float64)
		htmlURL, _ := issue["html_url"].(string)
		stateStr, _ := issue["state"].(string)
		fmt.Fprintf(&out, "#%.0f [%s] %s\n  %s\n", number, stateStr, title, htmlURL)
		count++
	}

	if count == 0 {
		return ToolResult{Content: "No issues found matching the criteria."}
	}
	return ToolResult{Content: out.String()}
}

// githubRequest performs an authenticated GitHub API call with a JSON body.
func githubRequest(token, method, apiURL string, body map[string]any) (map[string]any, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, apiURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("github API %d: %s", resp.StatusCode, data)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return result, nil
}
