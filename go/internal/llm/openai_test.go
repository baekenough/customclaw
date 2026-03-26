package llm

import "testing"

// TestNewOpenAIProvider_Name verifies that NewOpenAIProvider returns a provider
// whose Name() is "openai".
func TestNewOpenAIProvider_Name(t *testing.T) {
	t.Parallel()

	p := NewOpenAIProvider()
	if p == nil {
		t.Fatal("NewOpenAIProvider() returned nil")
	}
	if p.Name() != "openai" {
		t.Errorf("Name() = %q, want %q", p.Name(), "openai")
	}
}
