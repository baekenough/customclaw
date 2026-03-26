// Package llm — openai.go is intentionally thin.
//
// The OpenAI provider implementation lives in codex.go (OpenAIProvider /
// CodexProvider). This file re-exports NewOpenAIProvider so that callers
// referencing the "openai" name by import path continue to work.
package llm
