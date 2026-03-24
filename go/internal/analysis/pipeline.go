package analysis

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"golang.org/x/sync/errgroup"

	"github.com/baekenough/customclaw/internal/llm"
)

// AnalysisRequest is a decoded Redis Stream message for the analysis pipeline.
type AnalysisRequest struct {
	Type        string // "issue_analysis" or "pr_analysis"
	IssueNumber string
	PRNumber    string
	Repo        string
	RepoPath    string
	IssueTitle  string
	IssueBody   string
	IssueLabels string
	PRTitle     string
	PRBody      string
}

// processAnalysis runs the 3-role issue analysis pipeline:
//  1. RAG search for relevant code context
//  2. Architect + Colleague analyses in parallel
//  3. Post architect and colleague GitHub comments
//  4. Professor synthesis (if both succeed)
//  5. Add "professor" label and notify Slack
func processAnalysis(ctx context.Context, req AnalysisRequest, provider llm.Provider) error {
	repo := req.Repo
	if repo == "" {
		repo = os.Getenv("DEFAULT_TARGET_REPO")
	}
	issueNumber := req.IssueNumber
	if issueNumber == "" {
		issueNumber = "0"
	}

	slog.Info("analysis: processing issue", "issue", issueNumber, "title", truncate(req.IssueTitle, 50))

	// Phase 1: RAG — retrieve relevant code snippets.
	ragContext := searchRelevantCode(ctx, req.IssueTitle, req.IssueBody)

	// Phase 2: Run architect + colleague in parallel.
	architectPromptText := architectPrompt(issueNumber, req.IssueTitle, req.IssueBody, req.IssueLabels, ragContext)
	colleaguePromptText := colleaguePrompt(issueNumber, req.IssueTitle, req.IssueBody, req.IssueLabels, ragContext)

	var architectResult, colleagueResult string
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		resp, err := provider.Complete(gctx, &llm.Request{
			UserMessage: architectPromptText,
			Model:       "opus",
			MaxTurns:    10,
			FullAgent:   true,
			WorkDir:     req.RepoPath,
		})
		if err != nil {
			slog.Warn("analysis: architect failed", "issue", issueNumber, "error", err)
			return nil // non-fatal: log and continue
		}
		architectResult = resp.Text
		return nil
	})

	g.Go(func() error {
		resp, err := provider.Complete(gctx, &llm.Request{
			UserMessage: colleaguePromptText,
			Model:       "opus",
			MaxTurns:    10,
			FullAgent:   true,
			WorkDir:     req.RepoPath,
		})
		if err != nil {
			slog.Warn("analysis: colleague failed", "issue", issueNumber, "error", err)
			return nil // non-fatal: log and continue
		}
		colleagueResult = resp.Text
		return nil
	})

	if err := g.Wait(); err != nil {
		return fmt.Errorf("parallel analysis: %w", err)
	}

	// Log warnings for empty results (e.g. LLM returned empty text without error).
	if architectResult == "" {
		slog.Warn("analysis: architect result is empty after wait", "issue", issueNumber)
	}
	if colleagueResult == "" {
		slog.Warn("analysis: colleague result is empty after wait", "issue", issueNumber)
	}

	// Bail out early if the context was cancelled during parallel analysis.
	if err := ctx.Err(); err != nil {
		return err
	}

	// Phase 3: Post comments to GitHub sequentially.
	if architectResult != "" {
		body := fmt.Sprintf(
			"## 🏛️ Senior Architect Analysis\n\n%s\n\n---\n_Senior Architect analysis by Claude Code — omc_issue_analyzer_",
			architectResult,
		)
		if err := postGithubComment(ctx, repo, issueNumber, body); err != nil {
			slog.Warn("analysis: post architect comment failed", "issue", issueNumber, "error", err)
		}
	} else {
		slog.Warn("analysis: architect returned no output", "issue", issueNumber)
	}

	if colleagueResult != "" {
		body := fmt.Sprintf(
			"## 🤝 Project Colleague Review\n\n%s\n\n---\n_Project Colleague review by Claude Code — omc_issue_analyzer_",
			colleagueResult,
		)
		if err := postGithubComment(ctx, repo, issueNumber, body); err != nil {
			slog.Warn("analysis: post colleague comment failed", "issue", issueNumber, "error", err)
		}
	} else {
		slog.Warn("analysis: colleague returned no output", "issue", issueNumber)
	}

	// Phase 4: Professor synthesis.
	if architectResult != "" && colleagueResult != "" {
		slog.Info("analysis: running professor synthesis", "issue", issueNumber)
		profPrompt := professorPrompt(
			issueNumber, req.IssueTitle, req.IssueBody, req.IssueLabels,
			architectResult, colleagueResult,
		)
		profResp, err := provider.Complete(ctx, &llm.Request{
			UserMessage: profPrompt,
			Model:       "opus",
			MaxTurns:    10,
			FullAgent:   true,
			WorkDir:     req.RepoPath,
		})
		if err != nil {
			slog.Warn("analysis: professor failed", "issue", issueNumber, "error", err)
		} else if profResp.Text != "" {
			profBody := fmt.Sprintf(
				"## 🎓 Professor Synthesis\n\n%s\n\n---\n_Professor synthesis by Claude Code (opus) — omc_issue_analyzer_",
				profResp.Text,
			)
			if err := postGithubComment(ctx, repo, issueNumber, profBody); err != nil {
				slog.Warn("analysis: post professor comment failed", "issue", issueNumber, "error", err)
			}

			// Phase 5: label + Slack.
			if err := addLabel(ctx, repo, issueNumber, "professor"); err != nil {
				slog.Warn("analysis: add label failed", "issue", issueNumber, "error", err)
			}
			notifySlack(ctx,
				fmt.Sprintf("🎓 이슈 #%s 교수 종합 분석 완료", issueNumber),
				issueNumber, repo, "", "microscope",
			)
		} else {
			slog.Warn("analysis: professor returned no output", "issue", issueNumber)
		}
	}

	slog.Info("analysis: complete", "issue", issueNumber)
	return nil
}

// processPRAnalysis runs the PR analysis pipeline:
//  1. Extract linked issue + fetch existing analysis
//  2. RAG search
//  3. PR Architect + Colleague in parallel
//  4. Post comments
//  5. Professor PR synthesis
//  6. Slack notifications
func processPRAnalysis(ctx context.Context, req AnalysisRequest, provider llm.Provider) error {
	repo := req.Repo
	if repo == "" {
		repo = os.Getenv("DEFAULT_TARGET_REPO")
	}
	prNumber := req.PRNumber
	if prNumber == "" {
		prNumber = "0"
	}

	slog.Info("analysis: processing PR", "pr", prNumber, "title", truncate(req.PRTitle, 50))

	startTS := notifySlack(ctx,
		fmt.Sprintf("🔍 PR #%s 정합성 분석 시작", prNumber),
		prNumber, repo, "", "",
	)

	// Step 1: Find linked issue and fetch its analysis.
	issueAnalysisContext := ""
	linkedIssue := extractLinkedIssue(req.PRTitle, req.PRBody)
	if linkedIssue != "" {
		slog.Info("analysis: PR linked to issue", "pr", prNumber, "issue", linkedIssue)
		issueAnalysis := fetchIssueAnalysis(ctx, repo, linkedIssue)
		var parts []string
		if v := issueAnalysis["architect"]; v != "" {
			parts = append(parts, "### Architect Analysis\n"+v)
		}
		if v := issueAnalysis["colleague"]; v != "" {
			parts = append(parts, "### Colleague Review\n"+v)
		}
		if v := issueAnalysis["professor"]; v != "" {
			parts = append(parts, "### Professor Synthesis\n"+v)
		}
		if len(parts) > 0 {
			issueAnalysisContext = fmt.Sprintf(
				"Issue #%s was analyzed. Key findings:\n\n%s",
				linkedIssue, joinStrings(parts, "\n\n"),
			)
		}
	} else {
		slog.Info("analysis: no linked issue found", "pr", prNumber)
	}

	// Step 2: RAG search.
	ragContext := searchRelevantCode(ctx, req.PRTitle, req.PRBody)
	combinedContext := issueAnalysisContext
	if ragSection := buildRAGSection(ragContext); ragSection != "" {
		if combinedContext != "" {
			combinedContext += "\n" + ragSection
		} else {
			combinedContext = ragSection
		}
	}

	// Step 3: Run architect + colleague PR analyses in parallel.
	archPrompt := prArchitectPrompt(prNumber, req.PRTitle, req.PRBody, repo, combinedContext)
	collPrompt := prColleaguePrompt(prNumber, req.PRTitle, req.PRBody, repo, combinedContext)

	var architectResult, colleagueResult string
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		resp, err := provider.Complete(gctx, &llm.Request{
			UserMessage: archPrompt,
			Model:       "opus",
			MaxTurns:    10,
			FullAgent:   true,
			WorkDir:     req.RepoPath,
		})
		if err != nil {
			slog.Warn("analysis: PR architect failed", "pr", prNumber, "error", err)
			return nil
		}
		architectResult = resp.Text
		return nil
	})

	g.Go(func() error {
		resp, err := provider.Complete(gctx, &llm.Request{
			UserMessage: collPrompt,
			Model:       "opus",
			MaxTurns:    10,
			FullAgent:   true,
			WorkDir:     req.RepoPath,
		})
		if err != nil {
			slog.Warn("analysis: PR colleague failed", "pr", prNumber, "error", err)
			return nil
		}
		colleagueResult = resp.Text
		return nil
	})

	if err := g.Wait(); err != nil {
		return fmt.Errorf("parallel PR analysis: %w", err)
	}

	// Log warnings for empty results.
	if architectResult == "" {
		slog.Warn("analysis: PR architect result is empty after wait", "pr", prNumber)
	}
	if colleagueResult == "" {
		slog.Warn("analysis: PR colleague result is empty after wait", "pr", prNumber)
	}

	// Bail out early if the context was cancelled during parallel analysis.
	if err := ctx.Err(); err != nil {
		return err
	}

	// Step 4: Post architect + colleague PR comments.
	if architectResult != "" {
		body := fmt.Sprintf(
			"## 🏛️ Senior Architect Analysis\n\n%s\n\n---\n_Senior Architect PR review by Claude Code — omc_pr_analyzer_",
			architectResult,
		)
		if err := postGithubComment(ctx, repo, prNumber, body); err != nil {
			slog.Warn("analysis: post PR architect comment failed", "pr", prNumber, "error", err)
		}
	} else {
		slog.Warn("analysis: PR architect returned no output", "pr", prNumber)
	}

	if colleagueResult != "" {
		body := fmt.Sprintf(
			"## 🤝 Project Colleague Review\n\n%s\n\n---\n_Project Colleague PR review by Claude Code — omc_pr_analyzer_",
			colleagueResult,
		)
		if err := postGithubComment(ctx, repo, prNumber, body); err != nil {
			slog.Warn("analysis: post PR colleague comment failed", "pr", prNumber, "error", err)
		}
	} else {
		slog.Warn("analysis: PR colleague returned no output", "pr", prNumber)
	}

	// Notify Slack mid-way.
	notifySlack(ctx,
		fmt.Sprintf("📝 PR #%s Architect + Colleague 분석 완료, Professor 종합 중...", prNumber),
		prNumber, repo, startTS, "",
	)

	// Step 5: Professor PR synthesis.
	if architectResult != "" && colleagueResult != "" {
		slog.Info("analysis: running professor PR synthesis", "pr", prNumber)
		profPrompt := prProfessorPrompt(
			prNumber, req.PRTitle, req.PRBody,
			issueAnalysisContext, architectResult, colleagueResult,
		)
		profResp, err := provider.Complete(ctx, &llm.Request{
			UserMessage: profPrompt,
			Model:       "opus",
			MaxTurns:    10,
			FullAgent:   true,
			WorkDir:     req.RepoPath,
		})
		if err != nil {
			slog.Warn("analysis: PR professor failed", "pr", prNumber, "error", err)
		} else if profResp.Text != "" {
			profBody := fmt.Sprintf(
				"## 🎓 Professor Synthesis\n\n%s\n\n---\n_Professor PR synthesis by Claude Code (opus) — omc_pr_analyzer_",
				profResp.Text,
			)
			if err := postGithubComment(ctx, repo, prNumber, profBody); err != nil {
				slog.Warn("analysis: post PR professor comment failed", "pr", prNumber, "error", err)
			}
			notifySlack(ctx,
				fmt.Sprintf("🎓 PR #%s 교수 종합 분석 완료", prNumber),
				prNumber, repo, startTS, "white_check_mark",
			)
		} else {
			slog.Warn("analysis: PR professor returned no output", "pr", prNumber)
		}
	}

	slog.Info("analysis: PR complete", "pr", prNumber)
	return nil
}

// truncate shortens s to at most n runes.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// joinStrings joins a slice of strings with sep.
func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += sep + s
	}
	return result
}
