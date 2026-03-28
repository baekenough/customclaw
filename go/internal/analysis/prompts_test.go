package analysis

import (
	"strings"
	"testing"
)

func TestArchitectPrompt(t *testing.T) {
	result := architectPrompt("42", "Add caching layer", "We need Redis caching.", "enhancement", "", "org/oh-my-customcode")
	if result == "" {
		t.Fatal("architectPrompt returned empty string")
	}
	if !strings.Contains(result, "#42") {
		t.Error("expected issue number #42 in architect prompt")
	}
	if !strings.Contains(result, "Add caching layer") {
		t.Error("expected issue title in architect prompt")
	}
}

func TestColleaguePrompt(t *testing.T) {
	result := colleaguePrompt("7", "Fix memory leak", "Memory grows unbounded.", "bug", "", "org/oh-my-customcode")
	if result == "" {
		t.Fatal("colleaguePrompt returned empty string")
	}
	if !strings.Contains(result, "#7") {
		t.Error("expected issue number #7 in colleague prompt")
	}
	if !strings.Contains(result, "Fix memory leak") {
		t.Error("expected issue title in colleague prompt")
	}
}

func TestProfessorPrompt(t *testing.T) {
	result := professorPrompt("99", "Refactor auth", "Auth module is messy.", "refactor",
		"architect says X", "colleague says Y", "org/oh-my-customcode")
	if result == "" {
		t.Fatal("professorPrompt returned empty string")
	}
	if !strings.Contains(result, "#99") {
		t.Error("expected issue number #99 in professor prompt")
	}
	if !strings.Contains(result, "Refactor auth") {
		t.Error("expected issue title in professor prompt")
	}
	if !strings.Contains(result, "architect says X") {
		t.Error("expected architect result in professor prompt")
	}
	if !strings.Contains(result, "colleague says Y") {
		t.Error("expected colleague result in professor prompt")
	}
}

func TestPRArchitectPrompt(t *testing.T) {
	result := prArchitectPrompt("15", "feat: new auth", "Implements OAuth2.", "org/repo", "issue context")
	if result == "" {
		t.Fatal("prArchitectPrompt returned empty string")
	}
	if !strings.Contains(result, "#15") {
		t.Error("expected PR number #15 in PR architect prompt")
	}
	if !strings.Contains(result, "feat: new auth") {
		t.Error("expected PR title in PR architect prompt")
	}
	if !strings.Contains(result, "org/repo") {
		t.Error("expected repo in PR architect prompt")
	}
}

func TestPRColleaguePrompt(t *testing.T) {
	result := prColleaguePrompt("22", "fix: nil deref", "Fixes crash.", "org/myrepo", "linked issue")
	if result == "" {
		t.Fatal("prColleaguePrompt returned empty string")
	}
	if !strings.Contains(result, "#22") {
		t.Error("expected PR number #22 in PR colleague prompt")
	}
	if !strings.Contains(result, "fix: nil deref") {
		t.Error("expected PR title in PR colleague prompt")
	}
}

func TestPRProfessorPrompt(t *testing.T) {
	result := prProfessorPrompt("33", "chore: update deps", "Bumps versions.", "org/oh-my-customcode", "issue ctx",
		"arch review", "coll review")
	if result == "" {
		t.Fatal("prProfessorPrompt returned empty string")
	}
	if !strings.Contains(result, "#33") {
		t.Error("expected PR number #33 in PR professor prompt")
	}
	if !strings.Contains(result, "chore: update deps") {
		t.Error("expected PR title in PR professor prompt")
	}
}

// TestBodyTruncation ensures prompts truncate long inputs without panicking.
func TestBodyTruncation(t *testing.T) {
	longBody := strings.Repeat("x", 5000)

	// None of these should panic.
	_ = architectPrompt("1", "title", longBody, "labels", "", "org/oh-my-customcode")
	_ = colleaguePrompt("2", "title", longBody, "labels", "", "org/oh-my-customcode")
	_ = professorPrompt("3", "title", longBody, "labels", "arch", "coll", "org/oh-my-customcode")
	_ = prArchitectPrompt("4", "title", longBody, "repo", "")
	_ = prColleaguePrompt("5", "title", longBody, "repo", "")
	_ = prProfessorPrompt("6", "title", longBody, "repo", "", "arch", "coll")
}

// TestBuildRAGSection verifies the section is empty when ragContext is empty.
func TestBuildRAGSection(t *testing.T) {
	if got := buildRAGSection(""); got != "" {
		t.Errorf("expected empty section for empty ragContext, got %q", got)
	}

	section := buildRAGSection("some code snippet")
	if !strings.Contains(section, "some code snippet") {
		t.Error("expected code snippet in RAG section")
	}
	if !strings.Contains(section, "RAG 검색 결과") {
		t.Error("expected Korean header in RAG section")
	}
}
