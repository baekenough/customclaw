package llm

import (
	"context"
	"errors"
)

// OpenAIProvider is a placeholder implementation of Provider for the OpenAI API.
// It will be used as the Codex replacement in a future phase.
type OpenAIProvider struct{}

// NewOpenAIProvider constructs an OpenAIProvider.
func NewOpenAIProvider() *OpenAIProvider {
	return &OpenAIProvider{}
}

// Name returns "openai".
func (p *OpenAIProvider) Name() string { return "openai" }

// Complete is not yet implemented and always returns an error.
func (p *OpenAIProvider) Complete(_ context.Context, _ *Request) (*Response, error) {
	return nil, errors.New("openai provider not yet implemented")
}
