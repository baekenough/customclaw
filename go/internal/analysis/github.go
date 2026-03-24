package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

const githubAPIBase = "https://api.github.com"

// ghClient is a thin wrapper around the GitHub REST API.
type ghClient struct {
	token      string
	httpClient *http.Client
}

// _ghOnce guards singleton initialisation of _ghClientSingleton.
var (
	_ghOnce            sync.Once
	_ghClientSingleton *ghClient
)

// newGHClient returns the package-level singleton ghClient.
// The GITHUB_TOKEN environment variable is read once on first call.
func newGHClient() *ghClient {
	_ghOnce.Do(func() {
		_ghClientSingleton = &ghClient{
			token:      os.Getenv("GITHUB_TOKEN"),
			httpClient: &http.Client{Timeout: 30 * time.Second},
		}
	})
	return _ghClientSingleton
}

func (c *ghClient) headers() map[string]string {
	return map[string]string{
		"Authorization":        "Bearer " + c.token,
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
	}
}

func (c *ghClient) doJSON(ctx context.Context, method, url string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return nil, err
	}
	for k, v := range c.headers() {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.httpClient.Do(req)
}

// postGithubComment posts a comment on a GitHub issue or PR.
// Before posting it deletes any existing comment that shares the same footer
// signature, so re-triggered analyses replace rather than duplicate.
func postGithubComment(ctx context.Context, repo, issueNumber, body string) error {
	c := newGHClient()
	url := fmt.Sprintf("%s/repos/%s/issues/%s/comments", githubAPIBase, repo, issueNumber)

	// Extract footer signature for duplicate detection.
	footer := ""
	if idx := strings.LastIndex(body, "---\n_"); idx >= 0 {
		footer = body[idx+5:]
	}

	// Fetch existing comments and delete duplicates.
	if footer != "" {
		if err := deleteMatchingComments(ctx, c, url, footer); err != nil {
			slog.Warn("github: cleanup old comments failed (non-blocking)", "error", err)
		}
	}

	// Post new comment.
	resp, err := c.doJSON(ctx, http.MethodPost, url, map[string]string{"body": body})
	if err != nil {
		return fmt.Errorf("post comment: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("post comment status %d: %s", resp.StatusCode, b)
	}
	slog.Info("github: posted comment", "repo", repo, "issue", issueNumber)
	return nil
}

// deleteMatchingComments removes existing analysis comments that match the
// footer signature or contain "Analysis could not be completed".
func deleteMatchingComments(ctx context.Context, c *ghClient, listURL, footer string) error {
	resp, err := c.doJSON(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	var comments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
		return fmt.Errorf("decode comments: %w", err)
	}

	for _, cm := range comments {
		shouldDelete := (footer != "" && strings.Contains(cm.Body, footer)) ||
			strings.Contains(cm.Body, "Analysis could not be completed")
		if !shouldDelete {
			continue
		}
		delURL := fmt.Sprintf("%s/repos/issues/comments/%d", githubAPIBase, cm.ID)
		// Reconstruct from list URL: https://api.github.com/repos/{owner}/{repo}/issues/{num}/comments
		// → delete URL: https://api.github.com/repos/{owner}/{repo}/issues/comments/{id}
		parts := strings.Split(listURL, "/issues/")
		if len(parts) == 2 {
			delURL = parts[0] + fmt.Sprintf("/issues/comments/%d", cm.ID)
		}
		r, err := c.doJSON(ctx, http.MethodDelete, delURL, nil)
		if err != nil {
			slog.Warn("github: delete comment failed", "id", cm.ID, "error", err)
			continue
		}
		_ = r.Body.Close()
		slog.Info("github: deleted old analysis comment", "id", cm.ID)
	}
	return nil
}

// addLabel adds a label to a GitHub issue or PR.
func addLabel(ctx context.Context, repo, issueNumber, label string) error {
	c := newGHClient()
	url := fmt.Sprintf("%s/repos/%s/issues/%s/labels", githubAPIBase, repo, issueNumber)
	resp, err := c.doJSON(ctx, http.MethodPost, url, map[string][]string{"labels": {label}})
	if err != nil {
		return fmt.Errorf("add label: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("add label status %d: %s", resp.StatusCode, b)
	}
	slog.Info("github: added label", "label", label, "repo", repo, "issue", issueNumber)
	return nil
}

// fetchIssueAnalysis retrieves existing analysis comments from a GitHub issue
// and returns a map with keys "architect", "colleague", "professor".
func fetchIssueAnalysis(ctx context.Context, repo, issueNumber string) map[string]string {
	c := newGHClient()
	url := fmt.Sprintf("%s/repos/%s/issues/%s/comments", githubAPIBase, repo, issueNumber)
	return fetchIssueAnalysisFromURL(ctx, url, c.token)
}

// fetchIssueAnalysisFromURL is the testable core of fetchIssueAnalysis.
// It accepts a raw URL and token so tests can point to a local server.
func fetchIssueAnalysisFromURL(ctx context.Context, url, token string) map[string]string {
	result := map[string]string{
		"architect": "",
		"colleague": "",
		"professor": "",
	}

	c := &ghClient{
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	resp, err := c.doJSON(ctx, http.MethodGet, url, nil)
	if err != nil {
		slog.Warn("github: fetch issue analysis failed", "url", url, "error", err)
		return result
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("github: fetch comments non-200", "status", resp.StatusCode)
		return result
	}

	var comments []struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
		slog.Warn("github: decode comments failed", "error", err)
		return result
	}

	const (
		architectHeader = "## 🏛️ Senior Architect Analysis\n\n"
		colleagueHeader = "## 🤝 Project Colleague Review\n\n"
		professorHeader = "## 🎓 Professor Synthesis\n\n"
		footerSep       = "\n\n---\n_"
	)

	for _, cm := range comments {
		b := cm.Body
		switch {
		case strings.Contains(b, architectHeader):
			content := strings.SplitN(b, architectHeader, 2)
			if len(content) == 2 {
				result["architect"] = strings.SplitN(content[1], footerSep, 2)[0]
			}
		case strings.Contains(b, colleagueHeader):
			content := strings.SplitN(b, colleagueHeader, 2)
			if len(content) == 2 {
				result["colleague"] = strings.SplitN(content[1], footerSep, 2)[0]
			}
		case strings.Contains(b, professorHeader):
			content := strings.SplitN(b, professorHeader, 2)
			if len(content) == 2 {
				result["professor"] = strings.SplitN(content[1], footerSep, 2)[0]
			}
		}
	}
	return result
}

// keywordIssueRe matches "fixes #123", "closes #456", "resolves #789" etc.
var keywordIssueRe = regexp.MustCompile(`(?i)(?:fix(?:es|ed)?|close[sd]?|resolve[sd]?)\s+#(\d+)`)

// bareIssueRe matches a bare "#123" reference.
var bareIssueRe = regexp.MustCompile(`#(\d+)`)

// extractLinkedIssue extracts a linked issue number from a PR title or body.
// Keyword-prefixed patterns (fixes/closes/resolves) take priority over bare #N.
// Returns "" if no issue reference is found.
func extractLinkedIssue(prTitle, prBody string) string {
	for _, text := range []string{prTitle, prBody} {
		if m := keywordIssueRe.FindStringSubmatch(text); m != nil {
			return m[1]
		}
	}
	for _, text := range []string{prTitle, prBody} {
		if m := bareIssueRe.FindStringSubmatch(text); m != nil {
			return m[1]
		}
	}
	return ""
}
