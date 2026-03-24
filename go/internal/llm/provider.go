// Package llm defines the LLM provider interface and shared request/response types.
package llm

import "context"

// Request encapsulates a single LLM call.
type Request struct {
	// SystemPrompt is prepended as the system message.
	SystemPrompt string
	// UserMessage is the human turn content.
	UserMessage  string
	// Model is a short alias: "opus", "sonnet", or "haiku".
	Model        string
	MaxTurns     int
	FullAgent    bool
	// WorkDir sets the working directory for agent-mode tool execution.
	WorkDir      string
}

// Response holds the LLM result and optional billing metadata.
type Response struct {
	Text  string
	Usage *UsageInfo
}

// UsageInfo contains token and cost metadata from the LLM API.
type UsageInfo struct {
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
	// CostUSD is nil when the provider does not report cost directly.
	CostUSD             *float64
	// Model is the resolved model identifier (may differ from the requested alias).
	Model               string
}

// Provider is the interface that all LLM backends must implement.
type Provider interface {
	// Name returns a short identifier such as "anthropic" or "openai".
	Name() string
	// Complete sends the request to the LLM and returns the response.
	Complete(ctx context.Context, req *Request) (*Response, error)
}
