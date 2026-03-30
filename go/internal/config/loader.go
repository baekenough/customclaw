package config

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"
)

// resolveEnv replaces ${ENV_VAR} references in value with the corresponding
// environment variable. Returns an empty string if the variable is not set.
func resolveEnv(value string) string {
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
		key := value[2 : len(value)-1]
		return os.Getenv(key)
	}
	return value
}

// yamlBotFile is the raw YAML structure as read from disk.
// Fields are mapped to BotConfig after env resolution.
type yamlBotFile struct {
	Name        string            `yaml:"name"`
	Platform    string            `yaml:"platform"`
	Slack       map[string]any    `yaml:"slack"`
	Mattermost  map[string]any    `yaml:"mattermost"`
	Discord     map[string]any    `yaml:"discord"`
	Channels    []string          `yaml:"channels"`
	Persona     map[string]any    `yaml:"persona"`
	Project     map[string]any    `yaml:"project"`
	Airflow     map[string]any    `yaml:"airflow"`
	Tools       map[string]any    `yaml:"tools"`
	Claude      map[string]any    `yaml:"claude"`
	Memory      map[string]any    `yaml:"memory"`
	Security    map[string]any    `yaml:"security"`
	// Credentials is the platform-neutral key-value credential store.
	// Values that begin with "${" are resolved as environment variables.
	Credentials map[string]string `yaml:"credentials"`
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

func intField(m map[string]any, key string, def int) int {
	if m == nil {
		return def
	}
	switch v := m[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return def
}

func boolField(m map[string]any, key string, def bool) bool {
	if m == nil {
		return def
	}
	v, ok := m[key].(bool)
	if !ok {
		return def
	}
	return v
}

func stringSliceField(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	raw, _ := m[key].([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// LoadBotFromYAML reads a single bot configuration from a YAML file.
func LoadBotFromYAML(path string) (*BotConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var raw yamlBotFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	enabledTools := stringSliceField(raw.Tools, "enabled")

	cfg := &BotConfig{
		ID:            raw.Name,
		Name:          raw.Name,
		Platform:      raw.Platform,
		SlackAppToken: resolveEnv(stringField(raw.Slack, "app_token")),
		SlackBotToken: resolveEnv(stringField(raw.Slack, "bot_token")),
		Mattermost: MattermostConfig{
			URL:   resolveEnv(stringField(raw.Mattermost, "url")),
			Token: resolveEnv(stringField(raw.Mattermost, "token")),
			Port:  intField(raw.Mattermost, "port", 8065),
		},
		Discord: DiscordConfig{
			Token:   resolveEnv(stringField(raw.Discord, "token")),
			GuildID: stringField(raw.Discord, "guild_id"),
		},
		Channels: raw.Channels,
		Persona: PersonaConfig{
			DisplayName:    stringField(raw.Persona, "display_name"),
			Description:    stringField(raw.Persona, "description"),
			Personality:    stringField(raw.Persona, "personality"),
			ResponsePrefix: stringField(raw.Persona, "response_prefix"),
		},
		Project: ProjectConfig{
			RepoPath:       stringField(raw.Project, "repo_path"),
			GithubRepo:     stringField(raw.Project, "github_repo"),
			GithubTokenVar: stringField(raw.Project, "github_token_var"),
		},
		Airflow: AirflowConfig{
			DagPrefix: stringField(raw.Airflow, "dag_prefix"),
		},
		ToolsEnabled: enabledTools,
		Claude: ClaudeConfig{
			Provider:  stringField(raw.Claude, "provider"),
			Model:     stringField(raw.Claude, "model"),
			MaxTurns:  intField(raw.Claude, "max_turns", 5),
			FullAgent: boolField(raw.Claude, "full_agent", false),
		},
		Memory: MemoryConfig{
			ContextWindow: intField(raw.Memory, "context_window", 20),
			AutoExtract:   boolField(raw.Memory, "auto_extract", true),
		},
		Security: SecurityConfig{
			AllowedChannels: stringSliceField(raw.Security, "allowed_channels"),
			AllowedUsers:    stringSliceField(raw.Security, "allowed_users"),
			DangerousTools:  stringSliceField(raw.Security, "dangerous_tools"),
			MentionOnly:     boolField(raw.Security, "mention_only", false),
		},
	}
	cfg.defaults()

	if err := validateBotConfig(cfg); err != nil {
		return nil, fmt.Errorf("validate %s: %w", path, err)
	}
	return cfg, nil
}

// LoadAllBotsFromDB loads all active bot configurations from PostgreSQL.
// It returns an empty slice (not an error) when DATABASE_DSN is unset.
func LoadAllBotsFromDB(ctx context.Context, dsn string) ([]*BotConfig, error) {
	if dsn == "" {
		return nil, nil
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	rows, err := conn.Query(ctx, `
		SELECT
			id, name, slack_app_token, slack_bot_token,
			channels, persona, project, airflow, tools,
			claude, memory, security,
			platform, discord, mattermost,
			anthropic_api_key, openai_api_key, gemini_api_key,
			credentials
		FROM bots
		WHERE is_active = true
		ORDER BY created_at, id
	`)
	if err != nil {
		return nil, fmt.Errorf("query bots: %w", err)
	}
	defer rows.Close()

	var configs []*BotConfig
	for rows.Next() {
		cfg, err := scanBotRow(rows)
		if err != nil {
			slog.Warn("skipping bot row", "error", err)
			continue
		}
		cfg.defaults()
		configs = append(configs, cfg)
	}
	return configs, rows.Err()
}

// scanBotRow scans a single database row into a BotConfig.
func scanBotRow(rows pgx.Rows) (*BotConfig, error) {
	var (
		id, name, slackApp, slackBot string
		channels                     []string
		persona, project, airflow    map[string]any
		tools, claude, memory        map[string]any
		security                     map[string]any
		platform                     string
		discord, mattermost          map[string]any
		anthropicKey, openaiKey, geminiKey *string // nullable per-bot LLM keys
		credentialsRaw               []byte
	)

	err := rows.Scan(
		&id, &name, &slackApp, &slackBot,
		&channels, &persona, &project, &airflow, &tools,
		&claude, &memory, &security,
		&platform, &discord, &mattermost,
		&anthropicKey, &openaiKey, &geminiKey,
		&credentialsRaw,
	)
	if err != nil {
		return nil, fmt.Errorf("scan row: %w", err)
	}

	enabledTools := stringSliceField(tools, "enabled")

	cfg := &BotConfig{
		ID:            id,
		Name:          name,
		SlackAppToken: resolveEnv(slackApp),
		SlackBotToken: resolveEnv(slackBot),
		Platform:      platform,
		Channels:      channels,
		Mattermost: MattermostConfig{
			URL:   resolveEnv(stringField(mattermost, "url")),
			Token: resolveEnv(stringField(mattermost, "token")),
			Port:  intField(mattermost, "port", 8065),
		},
		Discord: DiscordConfig{
			Token:   resolveEnv(stringField(discord, "token")),
			GuildID: stringField(discord, "guild_id"),
		},
		Persona: PersonaConfig{
			DisplayName:    stringField(persona, "display_name"),
			Description:    stringField(persona, "description"),
			Personality:    stringField(persona, "personality"),
			ResponsePrefix: stringField(persona, "response_prefix"),
		},
		Project: ProjectConfig{
			RepoPath:       stringField(project, "repo_path"),
			GithubRepo:     stringField(project, "github_repo"),
			GithubTokenVar: stringField(project, "github_token_var"),
		},
		Airflow: AirflowConfig{
			DagPrefix: stringField(airflow, "dag_prefix"),
		},
		ToolsEnabled: enabledTools,
		Claude: ClaudeConfig{
			Provider:  stringField(claude, "provider"),
			Model:     stringField(claude, "model"),
			MaxTurns:  intField(claude, "max_turns", 5),
			FullAgent: boolField(claude, "full_agent", false),
		},
		Memory: MemoryConfig{
			ContextWindow: intField(memory, "context_window", 20),
			AutoExtract:   boolField(memory, "auto_extract", true),
		},
		Security: SecurityConfig{
			AllowedChannels: stringSliceField(security, "allowed_channels"),
			AllowedUsers:    stringSliceField(security, "allowed_users"),
			DangerousTools:  stringSliceField(security, "dangerous_tools"),
			MentionOnly:     boolField(security, "mention_only", false),
		},
	}

	if anthropicKey != nil {
		cfg.LLMKeys.AnthropicKey = *anthropicKey
	}
	if openaiKey != nil {
		cfg.LLMKeys.OpenAIKey = *openaiKey
	}
	if geminiKey != nil {
		cfg.LLMKeys.GeminiKey = *geminiKey
	}

	if len(credentialsRaw) > 0 && string(credentialsRaw) != "{}" {
		if err := json.Unmarshal(credentialsRaw, &cfg.Credentials); err != nil {
			slog.Warn("failed to parse credentials", "bot", cfg.ID, "error", err)
		}
	}

	return cfg, nil
}

// LoadAllBots loads all bot configurations, preferring the database over YAML files.
// YAML files in botsDir act as a bootstrap fallback: a bot loaded from the database
// will not be overridden by a YAML file with the same ID.
func LoadAllBots(ctx context.Context, botsDir, dsn string) ([]*BotConfig, error) {
	byID := make(map[string]*BotConfig)

	dbConfigs, err := LoadAllBotsFromDB(ctx, dsn)
	if err != nil {
		slog.Warn("failed to load bots from database, falling back to YAML", "error", err)
	}
	for _, cfg := range dbConfigs {
		byID[cfg.ID] = cfg
	}

	entries, err := filepath.Glob(filepath.Join(botsDir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("glob bots dir: %w", err)
	}
	for _, path := range entries {
		cfg, err := LoadBotFromYAML(path)
		if err != nil {
			slog.Warn("skipping bot YAML", "path", path, "error", err)
			continue
		}
		if _, exists := byID[cfg.ID]; !exists {
			byID[cfg.ID] = cfg
		}
	}

	configs := make([]*BotConfig, 0, len(byID))
	for _, cfg := range byID {
		configs = append(configs, cfg)
	}
	return configs, nil
}

// validateBotConfig checks that the required platform-specific credentials are present.
func validateBotConfig(cfg *BotConfig) error {
	if cfg.Platform == "" {
		return fmt.Errorf("bot %q: platform must be explicitly set", cfg.ID)
	}
	switch cfg.Platform {
	case "slack":
		appToken := cfg.SlackAppToken
		botToken := cfg.SlackBotToken
		if appToken == "" {
			appToken = cfg.Credentials["app_token"]
		}
		if botToken == "" {
			botToken = cfg.Credentials["bot_token"]
		}
		if appToken == "" || botToken == "" {
			return fmt.Errorf("bot %q: slack requires app_token and bot_token (via credentials or legacy fields)", cfg.ID)
		}
	case "mattermost":
		url := cfg.Mattermost.URL
		token := cfg.Mattermost.Token
		if url == "" {
			url = cfg.Credentials["url"]
		}
		if token == "" {
			token = cfg.Credentials["token"]
		}
		if url == "" || token == "" {
			return fmt.Errorf("bot %q: mattermost requires url and token", cfg.ID)
		}
	case "discord":
		token := cfg.Discord.Token
		if token == "" {
			token = cfg.Credentials["token"]
		}
		if token == "" {
			return fmt.Errorf("bot %q: discord requires token", cfg.ID)
		}
	default:
		return fmt.Errorf("bot %q has unsupported platform %q; supported: slack, mattermost, discord",
			cfg.ID, cfg.Platform)
	}
	return nil
}
