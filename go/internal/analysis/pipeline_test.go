package analysis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestExtractLinkedIssue verifies keyword-prefixed and bare issue reference patterns.
func TestExtractLinkedIssue(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		body     string
		expected string
	}{
		{
			name:     "fixes keyword in body",
			title:    "Some PR",
			body:     "This PR fixes #123",
			expected: "123",
		},
		{
			name:     "closes keyword with trailing text",
			title:    "fix: closes #456 some text",
			body:     "",
			expected: "456",
		},
		{
			name:     "resolves keyword in title",
			title:    "resolves #789",
			body:     "PR description",
			expected: "789",
		},
		{
			name:     "bare hash reference",
			title:    "Some PR",
			body:     "Related to #42",
			expected: "42",
		},
		{
			name:     "no issue reference",
			title:    "refactor: clean up code",
			body:     "No issue linked here.",
			expected: "",
		},
		{
			name:     "keyword takes priority over bare hash",
			title:    "#10 fixes #20",
			body:     "",
			expected: "20",
		},
		{
			name:     "fixed keyword variant",
			title:    "",
			body:     "fixed #99",
			expected: "99",
		},
		{
			name:     "closed keyword variant",
			title:    "closed #55",
			body:     "",
			expected: "55",
		},
		{
			name:     "resolved keyword variant",
			title:    "",
			body:     "resolved #77",
			expected: "77",
		},
		{
			name:     "case insensitive FIXES",
			title:    "FIXES #100",
			body:     "",
			expected: "100",
		},
		{
			name:     "title checked before body for keywords",
			title:    "fixes #11",
			body:     "fixes #22",
			expected: "11",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractLinkedIssue(tc.title, tc.body)
			if got != tc.expected {
				t.Errorf("extractLinkedIssue(%q, %q) = %q, want %q",
					tc.title, tc.body, got, tc.expected)
			}
		})
	}
}

// TestFetchIssueAnalysis verifies that comments are parsed into the correct buckets.
func TestFetchIssueAnalysis(t *testing.T) {
	const (
		architectContent = "Architecture notes here."
		colleagueContent = "Colleague notes here."
		professorContent = "Professor synthesis here."
	)

	// Build mock GitHub API response with three analysis comments.
	comments := []map[string]any{
		{
			"id":   1,
			"body": "## 🏛️ Senior Architect Analysis\n\n" + architectContent + "\n\n---\n_Senior Architect analysis by Claude Code — omc_issue_analyzer_",
		},
		{
			"id":   2,
			"body": "## 🤝 Project Colleague Review\n\n" + colleagueContent + "\n\n---\n_Project Colleague review by Claude Code — omc_issue_analyzer_",
		},
		{
			"id":   3,
			"body": "## 🎓 Professor Synthesis\n\n" + professorContent + "\n\n---\n_Professor synthesis by Claude Code (opus) — omc_issue_analyzer_",
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(comments)
	}))
	defer ts.Close()

	// Override the GitHub API base URL to point to test server.
	// We do this by constructing the URL ourselves and calling the internal helper.
	// Since githubAPIBase is a package-level const, we test via the exported path
	// by setting GITHUB_TOKEN to avoid auth failure.
	t.Setenv("GITHUB_TOKEN", "test-token")

	// We need to intercept the HTTP call. Patch the const by using the helper
	// directly with a custom URL via an internal test-only path.
	// Instead, we verify the parsing logic by calling the helper with a pre-built
	// comment slice via a small integration test server.
	result := fetchIssueAnalysisFromURL(context.Background(), ts.URL+"/comments", "test-token")

	if result["architect"] != architectContent {
		t.Errorf("architect = %q, want %q", result["architect"], architectContent)
	}
	if result["colleague"] != colleagueContent {
		t.Errorf("colleague = %q, want %q", result["colleague"], colleagueContent)
	}
	if result["professor"] != professorContent {
		t.Errorf("professor = %q, want %q", result["professor"], professorContent)
	}
}

// TestFetchIssueAnalysisEmpty verifies that missing keys return empty strings.
func TestFetchIssueAnalysisEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "body": "Just a regular comment."},
		})
	}))
	defer ts.Close()

	t.Setenv("GITHUB_TOKEN", "test-token")
	result := fetchIssueAnalysisFromURL(context.Background(), ts.URL+"/comments", "test-token")

	for _, key := range []string{"architect", "colleague", "professor"} {
		if result[key] != "" {
			t.Errorf("expected empty %q, got %q", key, result[key])
		}
	}
}

// TestFetchIssueAnalysisServerError verifies graceful degradation on HTTP error.
func TestFetchIssueAnalysisServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	t.Setenv("GITHUB_TOKEN", "test-token")
	_ = os.Setenv("GITHUB_TOKEN", "test-token")
	result := fetchIssueAnalysisFromURL(context.Background(), ts.URL+"/comments", "test-token")

	// Should return empty map, not panic.
	if len(result) != 3 {
		t.Errorf("expected 3 keys, got %d", len(result))
	}
}
