package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// resolveEnv
// ---------------------------------------------------------------------------

func TestResolveEnv(t *testing.T) {
	// t.Setenv requires the test not to be parallel at the parent level.
	t.Setenv("TEST_RESOLVE_VAR", "hello")

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"known env var", "${TEST_RESOLVE_VAR}", "hello"},
		{"nonexistent var", "${NONEXISTENT_XYZ_123}", ""},
		{"plain string", "just-a-string", "just-a-string"},
		{"empty string", "", ""},
		{"partial prefix only", "${incomplete", "${incomplete"},
		{"partial suffix only", "incomplete}", "incomplete}"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := resolveEnv(tc.input)
			if got != tc.want {
				t.Errorf("resolveEnv(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Helper field functions
// ---------------------------------------------------------------------------

func TestStringField(t *testing.T) {
	t.Parallel()

	t.Run("nil map", func(t *testing.T) {
		t.Parallel()
		if got := stringField(nil, "key"); got != "" {
			t.Errorf("stringField(nil, key) = %q, want empty", got)
		}
	})

	t.Run("existing key", func(t *testing.T) {
		t.Parallel()
		m := map[string]any{"name": "alice"}
		if got := stringField(m, "name"); got != "alice" {
			t.Errorf("got %q, want %q", got, "alice")
		}
	})

	t.Run("missing key", func(t *testing.T) {
		t.Parallel()
		m := map[string]any{"name": "alice"}
		if got := stringField(m, "missing"); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("non-string value", func(t *testing.T) {
		t.Parallel()
		m := map[string]any{"count": 42}
		if got := stringField(m, "count"); got != "" {
			t.Errorf("got %q, want empty for non-string", got)
		}
	})
}

func TestIntField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		m    map[string]any
		key  string
		def  int
		want int
	}{
		{"nil map", nil, "k", 7, 7},
		{"int value", map[string]any{"k": 42}, "k", 0, 42},
		{"float64 value (YAML unmarshals ints as float64)", map[string]any{"k": float64(99)}, "k", 0, 99},
		{"missing key uses default", map[string]any{}, "k", 5, 5},
		{"wrong type uses default", map[string]any{"k": "text"}, "k", 3, 3},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := intField(tc.m, tc.key, tc.def); got != tc.want {
				t.Errorf("intField() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestBoolField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		m    map[string]any
		key  string
		def  bool
		want bool
	}{
		{"nil map uses default true", nil, "k", true, true},
		{"nil map uses default false", nil, "k", false, false},
		{"explicit true", map[string]any{"k": true}, "k", false, true},
		{"explicit false", map[string]any{"k": false}, "k", true, false},
		{"missing key uses default", map[string]any{}, "k", true, true},
		{"wrong type uses default", map[string]any{"k": "yes"}, "k", true, true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := boolField(tc.m, tc.key, tc.def); got != tc.want {
				t.Errorf("boolField() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStringSliceField(t *testing.T) {
	t.Parallel()

	t.Run("nil map", func(t *testing.T) {
		t.Parallel()
		if got := stringSliceField(nil, "k"); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("missing key", func(t *testing.T) {
		t.Parallel()
		m := map[string]any{}
		// stringSliceField returns an empty (possibly nil) slice when the key is absent.
		got := stringSliceField(m, "k")
		if len(got) != 0 {
			t.Errorf("got %v (len=%d), want empty", got, len(got))
		}
	})

	t.Run("slice of strings", func(t *testing.T) {
		t.Parallel()
		m := map[string]any{"items": []any{"a", "b", "c"}}
		got := stringSliceField(m, "items")
		want := []string{"a", "b", "c"}
		if len(got) != len(want) {
			t.Fatalf("len = %d, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("mixed types skips non-strings", func(t *testing.T) {
		t.Parallel()
		m := map[string]any{"items": []any{"x", 42, "y"}}
		got := stringSliceField(m, "items")
		if len(got) != 2 || got[0] != "x" || got[1] != "y" {
			t.Errorf("got %v, want [x y]", got)
		}
	})
}

// ---------------------------------------------------------------------------
// LoadBotFromYAML
// ---------------------------------------------------------------------------

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "bot.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp yaml: %v", err)
	}
	return path
}

const validSlackYAML = `
name: test-bot
platform: slack
slack:
  app_token: xapp-test
  bot_token: xoxb-test
channels:
  - C001
persona:
  display_name: TestBot
  personality: helpful
  description: A test bot
claude:
  provider: claude
  model: haiku
  max_turns: 3
memory:
  context_window: 5
  auto_extract: true
security:
  dangerous_tools:
    - shell_exec
`

func TestLoadBotFromYAML_ValidSlack(t *testing.T) {
	t.Parallel()

	path := writeTempYAML(t, validSlackYAML)
	cfg, err := LoadBotFromYAML(path)
	if err != nil {
		t.Fatalf("LoadBotFromYAML error: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"Name", cfg.Name, "test-bot"},
		{"Platform", cfg.Platform, "slack"},
		{"SlackAppToken", cfg.SlackAppToken, "xapp-test"},
		{"SlackBotToken", cfg.SlackBotToken, "xoxb-test"},
		{"Persona.DisplayName", cfg.Persona.DisplayName, "TestBot"},
		{"Persona.Personality", cfg.Persona.Personality, "helpful"},
		{"Persona.Description", cfg.Persona.Description, "A test bot"},
		{"Claude.Provider", cfg.Claude.Provider, "claude"},
		{"Claude.Model", cfg.Claude.Model, "haiku"},
		{"Claude.MaxTurns", cfg.Claude.MaxTurns, 3},
		{"Memory.ContextWindow", cfg.Memory.ContextWindow, 5},
		{"Memory.AutoExtract", cfg.Memory.AutoExtract, true},
	}
	for _, tc := range checks {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}

	if len(cfg.Channels) != 1 || cfg.Channels[0] != "C001" {
		t.Errorf("Channels = %v, want [C001]", cfg.Channels)
	}
	if len(cfg.Security.DangerousTools) != 1 || cfg.Security.DangerousTools[0] != "shell_exec" {
		t.Errorf("DangerousTools = %v, want [shell_exec]", cfg.Security.DangerousTools)
	}
}

func TestLoadBotFromYAML_EnvVarResolution(t *testing.T) {
	// t.Setenv cannot be used with t.Parallel().
	t.Setenv("TEST_APP_TOKEN", "xapp-from-env")
	t.Setenv("TEST_BOT_TOKEN", "xoxb-from-env")

	yaml := `
name: env-bot
platform: slack
slack:
  app_token: ${TEST_APP_TOKEN}
  bot_token: ${TEST_BOT_TOKEN}
`
	path := writeTempYAML(t, yaml)
	cfg, err := LoadBotFromYAML(path)
	if err != nil {
		t.Fatalf("LoadBotFromYAML error: %v", err)
	}
	if cfg.SlackAppToken != "xapp-from-env" {
		t.Errorf("SlackAppToken = %q, want %q", cfg.SlackAppToken, "xapp-from-env")
	}
	if cfg.SlackBotToken != "xoxb-from-env" {
		t.Errorf("SlackBotToken = %q, want %q", cfg.SlackBotToken, "xoxb-from-env")
	}
}

func TestLoadBotFromYAML_FileNotFound(t *testing.T) {
	t.Parallel()

	_, err := LoadBotFromYAML("/nonexistent/path/bot.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadBotFromYAML_InvalidYAML(t *testing.T) {
	t.Parallel()

	path := writeTempYAML(t, ":\ninvalid: [yaml: content")
	_, err := LoadBotFromYAML(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

// ---------------------------------------------------------------------------
// validateBotConfig
// ---------------------------------------------------------------------------

func TestValidateBotConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     *BotConfig
		wantErr bool
		errFrag string // substring expected in error message
	}{
		{
			name: "valid slack",
			cfg: &BotConfig{
				ID:            "bot1",
				Platform:      "slack",
				SlackAppToken: "xapp-1",
				SlackBotToken: "xoxb-1",
			},
			wantErr: false,
		},
		{
			name: "slack missing app token",
			cfg: &BotConfig{
				ID:            "bot1",
				Platform:      "slack",
				SlackBotToken: "xoxb-1",
			},
			wantErr: true,
			errFrag: "slack_app_token",
		},
		{
			name: "slack missing bot token",
			cfg: &BotConfig{
				ID:            "bot1",
				Platform:      "slack",
				SlackAppToken: "xapp-1",
			},
			wantErr: true,
			errFrag: "slack_bot_token",
		},
		{
			name: "slack missing both tokens",
			cfg: &BotConfig{
				ID:       "bot1",
				Platform: "slack",
			},
			wantErr: true,
			errFrag: "slack_app_token",
		},
		{
			name: "valid mattermost",
			cfg: &BotConfig{
				ID:       "bot2",
				Platform: "mattermost",
				Mattermost: MattermostConfig{
					URL:   "http://mm.example.com",
					Token: "mm-token",
				},
			},
			wantErr: false,
		},
		{
			name: "mattermost missing token",
			cfg: &BotConfig{
				ID:       "bot2",
				Platform: "mattermost",
				Mattermost: MattermostConfig{
					URL: "http://mm.example.com",
				},
			},
			wantErr: true,
			errFrag: "mattermost.token",
		},
		{
			name: "valid discord",
			cfg: &BotConfig{
				ID:       "bot3",
				Platform: "discord",
				Discord:  DiscordConfig{Token: "discord-tok"},
			},
			wantErr: false,
		},
		{
			name: "discord missing token",
			cfg: &BotConfig{
				ID:       "bot3",
				Platform: "discord",
			},
			wantErr: true,
			errFrag: "discord.token",
		},
		{
			name: "unsupported platform",
			cfg: &BotConfig{
				ID:       "bot4",
				Platform: "teams",
			},
			wantErr: true,
			errFrag: "unsupported platform",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateBotConfig(tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.errFrag != "" && !strings.Contains(err.Error(), tc.errFrag) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.errFrag)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}
