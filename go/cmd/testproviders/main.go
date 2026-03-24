package main

import (
	"context"
	"encoding/json"
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

	// 1. Anthropic (OAuth) — read from .claude/.credentials.json
	fmt.Println("=== 1. Anthropic (Claude OAuth) ===")
	credPath := filepath.Join(home, ".claude", ".credentials.json")
	if _, err := os.Stat(credPath); err != nil {
		// macOS desktop app might not have .credentials.json — skip
		fmt.Println("  SKIP: .credentials.json not found (desktop app uses different auth)")
	} else {
		ts, err := llm.NewOAuthTokenSource(credPath)
		if err != nil {
			fmt.Printf("  ERROR loading OAuth: %v\n", err)
		} else {
			provider := llm.NewAnthropicProviderWithOAuth(ts)
			req.Model = "haiku"
			resp, err := provider.Complete(ctx, req)
			if err != nil {
				fmt.Printf("  ERROR: %v\n", err)
			} else {
				fmt.Printf("  OK: %s\n", resp.Text[:min(len(resp.Text), 80)])
			}
		}
	}

	// 2. Codex (CLI subprocess — uses ChatGPT OAuth, incompatible with REST API SDK)
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

	// 3. Gemini (Google OAuth token)
	fmt.Println("\n=== 3. Gemini (Google OAuth) ===")
	geminiToken := readGeminiToken(filepath.Join(home, ".gemini", "oauth_creds.json"))
	if geminiToken == "" {
		fmt.Println("  SKIP: no Gemini auth token")
	} else {
		// Set as API key to test — Google's genai SDK can use access_token
		os.Setenv("GEMINI_API_KEY", geminiToken)
		provider := llm.NewGeminiProvider(geminiToken)
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

func readGeminiToken(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var creds struct {
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(data, &creds) != nil {
		return ""
	}
	return creds.AccessToken
}
