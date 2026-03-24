package config

import "testing"

func TestBotConfigDefaults_ZeroValue(t *testing.T) {
	t.Parallel()

	var cfg BotConfig
	cfg.defaults()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"Platform", cfg.Platform, "slack"},
		{"Claude.Provider", cfg.Claude.Provider, "claude"},
		{"Claude.Model", cfg.Claude.Model, "sonnet"},
		{"Claude.MaxTurns", cfg.Claude.MaxTurns, 5},
		{"Memory.ContextWindow", cfg.Memory.ContextWindow, 20},
		{"Mattermost.Port", cfg.Mattermost.Port, 8065},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Errorf("defaults() %s = %v, want %v", tc.name, tc.got, tc.want)
			}
		})
	}
}

func TestBotConfigDefaults_PartiallyFilled(t *testing.T) {
	t.Parallel()

	cfg := BotConfig{
		Platform: "discord",
		Claude: ClaudeConfig{
			Provider: "codex",
			Model:    "opus",
			MaxTurns: 5,
		},
		Memory: MemoryConfig{
			ContextWindow: 50,
		},
		Mattermost: MattermostConfig{
			Port: 9090,
		},
	}
	cfg.defaults()

	// Existing values must be preserved.
	if cfg.Platform != "discord" {
		t.Errorf("Platform = %q, want %q", cfg.Platform, "discord")
	}
	if cfg.Claude.Provider != "codex" {
		t.Errorf("Claude.Provider = %q, want %q", cfg.Claude.Provider, "codex")
	}
	if cfg.Claude.Model != "opus" {
		t.Errorf("Claude.Model = %q, want %q", cfg.Claude.Model, "opus")
	}
	if cfg.Claude.MaxTurns != 5 {
		t.Errorf("Claude.MaxTurns = %d, want 5", cfg.Claude.MaxTurns)
	}
	if cfg.Memory.ContextWindow != 50 {
		t.Errorf("Memory.ContextWindow = %d, want 50", cfg.Memory.ContextWindow)
	}
	if cfg.Mattermost.Port != 9090 {
		t.Errorf("Mattermost.Port = %d, want 9090", cfg.Mattermost.Port)
	}
}

// TestBotConfigDefaults_AutoExtract verifies that boolField's default-true
// behaviour is the intended mechanism for AutoExtract. The defaults() method
// intentionally leaves AutoExtract untouched (see comment in types.go).
func TestBotConfigDefaults_AutoExtractNotOverridden(t *testing.T) {
	t.Parallel()

	// defaults() does NOT touch AutoExtract — boolField(m, "auto_extract", true)
	// in the loader is responsible for the true default. So after defaults(), the
	// zero value (false) stays false.
	var cfg BotConfig
	cfg.defaults()

	// Zero value is false; defaults() must not change it.
	if cfg.Memory.AutoExtract {
		t.Error("defaults() must not set AutoExtract=true; boolField in loader owns that default")
	}
}
