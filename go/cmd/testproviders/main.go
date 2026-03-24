package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/baekenough/customclaw/internal/llm"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	home, _ := os.UserHomeDir()

	req := &llm.Request{
		SystemPrompt: "You are a test bot. Reply in exactly one short sentence.",
		UserMessage:  "Say hello in Korean.",
	}

	// 1. Claude CLI subprocess — reads OAuth credentials from ~/.claude/
	fmt.Println("=== 1. Claude (CLI subprocess) ===")
	claudeCliPath := filepath.Join(home, ".claude")
	if _, err := os.Stat(claudeCliPath); err != nil {
		fmt.Println("  SKIP: ~/.claude not found (Claude CLI not configured)")
	} else {
		provider := llm.NewClaudeProvider()
		req.Model = "haiku"
		resp, err := provider.Complete(ctx, req)
		if err != nil {
			fmt.Printf("  ERROR: %v\n", err)
		} else {
			text := resp.Text
			if len(text) > 80 {
				text = text[:80]
			}
			fmt.Printf("  OK: %s\n", text)
		}
	}

	// 2. Codex CLI subprocess — uses ChatGPT OAuth, incompatible with REST API SDK
	fmt.Println("\n=== 2. Codex (CLI subprocess) ===")
	codexAuthPath := filepath.Join(home, ".codex", "auth.json")
	if _, err := os.Stat(codexAuthPath); err != nil {
		fmt.Println("  SKIP: no Codex auth (~/.codex/auth.json not found)")
	} else {
		provider := llm.NewCodexProvider()
		req.Model = "gpt-5.4"
		resp, err := provider.Complete(ctx, req)
		if err != nil {
			fmt.Printf("  ERROR: %v\n", err)
		} else {
			text := resp.Text
			if len(text) > 80 {
				text = text[:80]
			}
			fmt.Printf("  OK: %s\n", text)
		}
	}

	// 3. Gemini CLI subprocess — reads Google OAuth credentials from ~/.gemini/
	fmt.Println("\n=== 3. Gemini (CLI subprocess) ===")
	geminiConfigPath := filepath.Join(home, ".gemini")
	if _, err := os.Stat(geminiConfigPath); err != nil {
		fmt.Println("  SKIP: ~/.gemini not found (Gemini CLI not configured)")
	} else {
		provider := llm.NewGeminiProvider()
		req.Model = "gemini-2-flash"
		resp, err := provider.Complete(ctx, req)
		if err != nil {
			fmt.Printf("  ERROR: %v\n", err)
		} else {
			text := resp.Text
			if len(text) > 80 {
				text = text[:80]
			}
			fmt.Printf("  OK: %s\n", text)
		}
	}

	fmt.Println("\n=== Done ===")
}
