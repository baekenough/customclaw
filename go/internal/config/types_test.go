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

// ---------------------------------------------------------------------------
// Credentials map tests (hexagonal platform adapter migration)
// ---------------------------------------------------------------------------

func TestBotConfig_CredentialsMapPopulated(t *testing.T) {
	t.Parallel()

	cfg := &BotConfig{
		SlackAppToken: "xapp-test",
		SlackBotToken: "xoxb-test",
	}
	cfg.defaults()

	if cfg.Credentials == nil {
		t.Fatal("Credentials map is nil after defaults()")
	}
	if cfg.Credentials["app_token"] != "xapp-test" {
		t.Errorf("Credentials[app_token] = %q, want %q", cfg.Credentials["app_token"], "xapp-test")
	}
	if cfg.Credentials["bot_token"] != "xoxb-test" {
		t.Errorf("Credentials[bot_token] = %q, want %q", cfg.Credentials["bot_token"], "xoxb-test")
	}
}

func TestBotConfig_CredentialsNotOverridden(t *testing.T) {
	t.Parallel()

	cfg := &BotConfig{
		SlackAppToken: "xapp-old",
		SlackBotToken: "xoxb-old",
		Credentials: map[string]string{
			"app_token": "xapp-explicit",
			"bot_token": "xoxb-explicit",
		},
	}
	cfg.defaults()

	// Explicit Credentials must not be overridden by legacy fields.
	if cfg.Credentials["app_token"] != "xapp-explicit" {
		t.Errorf("Credentials[app_token] = %q, want %q (should not be overridden)",
			cfg.Credentials["app_token"], "xapp-explicit")
	}
	if cfg.Credentials["bot_token"] != "xoxb-explicit" {
		t.Errorf("Credentials[bot_token] = %q, want %q (should not be overridden)",
			cfg.Credentials["bot_token"], "xoxb-explicit")
	}
}

func TestBotConfig_EmptySlackTokensNoCredentials(t *testing.T) {
	t.Parallel()

	cfg := &BotConfig{Platform: "discord"}
	cfg.defaults()

	if cfg.Credentials == nil {
		t.Fatal("Credentials map should be initialised even for non-Slack platforms")
	}
	if _, ok := cfg.Credentials["app_token"]; ok {
		t.Error("empty SlackAppToken should not populate Credentials[app_token]")
	}
	if _, ok := cfg.Credentials["bot_token"]; ok {
		t.Error("empty SlackBotToken should not populate Credentials[bot_token]")
	}
}

func TestBotConfig_CredentialsInitialisedWhenNil(t *testing.T) {
	t.Parallel()

	cfg := &BotConfig{}
	cfg.defaults()

	if cfg.Credentials == nil {
		t.Error("defaults() should initialise Credentials to a non-nil map")
	}
}

func TestBotConfig_PartialCredentialsOnlyFillsMissing(t *testing.T) {
	t.Parallel()

	// Only app_token is pre-populated; bot_token should be filled from legacy field.
	cfg := &BotConfig{
		SlackAppToken: "xapp-legacy",
		SlackBotToken: "xoxb-legacy",
		Credentials: map[string]string{
			"app_token": "xapp-explicit",
			// bot_token is intentionally absent
		},
	}
	cfg.defaults()

	// Pre-set app_token must not be overridden.
	if cfg.Credentials["app_token"] != "xapp-explicit" {
		t.Errorf("Credentials[app_token] = %q, want %q", cfg.Credentials["app_token"], "xapp-explicit")
	}
	// Missing bot_token should be populated from SlackBotToken.
	if cfg.Credentials["bot_token"] != "xoxb-legacy" {
		t.Errorf("Credentials[bot_token] = %q, want %q (filled from SlackBotToken)", cfg.Credentials["bot_token"], "xoxb-legacy")
	}
}
