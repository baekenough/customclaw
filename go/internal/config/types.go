// Package config provides bot configuration types, loading, and hot-reload support.
package config

// PersonaConfig defines the bot's personality and display characteristics.
type PersonaConfig struct {
	DisplayName     string `yaml:"display_name"     json:"display_name"`
	Description     string `yaml:"description"      json:"description"`
	Personality     string `yaml:"personality"      json:"personality"`
	ResponsePrefix  string `yaml:"response_prefix"  json:"response_prefix"`
}

// ProjectConfig holds version-control and repository settings.
type ProjectConfig struct {
	RepoPath       string `yaml:"repo_path"        json:"repo_path"`
	GithubRepo     string `yaml:"github_repo"      json:"github_repo"`
	GithubTokenVar string `yaml:"github_token_var" json:"github_token_var"`
}

// AirflowConfig holds Apache Airflow integration settings.
type AirflowConfig struct {
	DagPrefix string `yaml:"dag_prefix" json:"dag_prefix"`
}

// ClaudeConfig controls which LLM provider and model the bot uses.
type ClaudeConfig struct {
	// Provider selects the LLM backend: "claude" or "codex".
	Provider  string `yaml:"provider"   json:"provider"`
	// Model is a short alias: "opus", "sonnet", or "haiku".
	Model     string `yaml:"model"      json:"model"`
	MaxTurns  int    `yaml:"max_turns"  json:"max_turns"`
	FullAgent bool   `yaml:"full_agent" json:"full_agent"`
}

// MemoryConfig controls how much conversation history is retained.
type MemoryConfig struct {
	ContextWindow int  `yaml:"context_window" json:"context_window"`
	AutoExtract   bool `yaml:"auto_extract"   json:"auto_extract"`
}

// SecurityConfig restricts which channels and users can interact with the bot.
type SecurityConfig struct {
	AllowedChannels []string `yaml:"allowed_channels" json:"allowed_channels"`
	AllowedUsers    []string `yaml:"allowed_users"    json:"allowed_users"`
	DangerousTools  []string `yaml:"dangerous_tools"  json:"dangerous_tools"`
	MentionOnly     bool     `yaml:"mention_only"     json:"mention_only"`
}

// MattermostConfig holds credentials for the Mattermost platform.
type MattermostConfig struct {
	URL   string `yaml:"url"   json:"url"`
	Token string `yaml:"token" json:"token"`
	Port  int    `yaml:"port"  json:"port"`
}

// DiscordConfig holds credentials for the Discord platform.
type DiscordConfig struct {
	Token   string `yaml:"token"    json:"token"`
	GuildID string `yaml:"guild_id" json:"guild_id"`
}

// LLMKeys holds per-bot LLM API keys loaded from the database.
// Empty strings mean the bot uses the shared environment variable keys.
type LLMKeys struct {
	AnthropicKey string `yaml:"-" json:"-"`
	OpenAIKey    string `yaml:"-" json:"-"`
	GeminiKey    string `yaml:"-" json:"-"`
}

// BotConfig is the top-level configuration for a single bot instance.
// It aggregates platform credentials, persona, and feature settings.
type BotConfig struct {
	ID   string `yaml:"id"   json:"id"`
	Name string `yaml:"name" json:"name"`
	// SlackAppToken is the Slack Socket Mode app-level token.
	//
	// Deprecated: retained only for JSON/YAML backward compatibility.
	// New code must read from Credentials["app_token"] instead.
	// This field will be removed in a future release.
	SlackAppToken string `yaml:"slack_app_token" json:"slack_app_token"`
	// SlackBotToken is the Slack bot user OAuth token.
	//
	// Deprecated: retained only for JSON/YAML backward compatibility.
	// New code must read from Credentials["bot_token"] instead.
	// This field will be removed in a future release.
	SlackBotToken string `yaml:"slack_bot_token" json:"slack_bot_token"`
	// Platform selects the messaging backend: "slack", "mattermost", or "discord".
	// Must be set explicitly; there is no default.
	Platform string `yaml:"platform" json:"platform"`
	Mattermost    MattermostConfig `yaml:"mattermost"      json:"mattermost"`
	Discord       DiscordConfig    `yaml:"discord"         json:"discord"`
	Channels      []string         `yaml:"channels"        json:"channels"`
	Persona       PersonaConfig    `yaml:"persona"         json:"persona"`
	Project       ProjectConfig    `yaml:"project"         json:"project"`
	Airflow       AirflowConfig    `yaml:"airflow"         json:"airflow"`
	ToolsEnabled  []string         `yaml:"tools_enabled"   json:"tools_enabled"`
	Claude        ClaudeConfig     `yaml:"claude"          json:"claude"`
	Memory        MemoryConfig     `yaml:"memory"          json:"memory"`
	Security      SecurityConfig   `yaml:"security"        json:"security"`
	LLMKeys       LLMKeys          `yaml:"-"               json:"-"` // per-bot API keys, never serialized
	// Credentials holds platform-specific credentials as key-value pairs.
	// Preferred over SlackAppToken/SlackBotToken for new integrations.
	// Populated automatically from legacy fields by defaults().
	Credentials   map[string]string `yaml:"credentials" json:"credentials,omitempty"`
}

// defaults applies zero-value defaults.
// Platform is intentionally NOT defaulted here; validation will catch an empty value.
func (c *BotConfig) defaults() {
	if c.Claude.Provider == "" {
		c.Claude.Provider = "claude"
	}
	if c.Claude.Model == "" {
		c.Claude.Model = "sonnet"
	}
	if c.Claude.MaxTurns == 0 {
		c.Claude.MaxTurns = 5
	}
	if c.Memory.ContextWindow == 0 {
		c.Memory.ContextWindow = 20
	}
	// AutoExtract defaults to true at parse time via boolField(... , true).
	// Go's zero value (false) is indistinguishable from an explicit false in
	// YAML, so we cannot safely override it here after parsing has run.
	if c.Mattermost.Port == 0 {
		c.Mattermost.Port = 8065
	}

	// Initialise the Credentials map so callers never need to nil-check it.
	if c.Credentials == nil {
		c.Credentials = make(map[string]string)
	}

	// Forward backfill: legacy Slack fields → Credentials map.
	// Allows code that was written before Credentials existed to continue working.
	// New platform adapters must read from Credentials, not from SlackAppToken/SlackBotToken.
	if c.SlackAppToken != "" {
		if _, ok := c.Credentials["app_token"]; !ok {
			c.Credentials["app_token"] = c.SlackAppToken
		}
	}
	if c.SlackBotToken != "" {
		if _, ok := c.Credentials["bot_token"]; !ok {
			c.Credentials["bot_token"] = c.SlackBotToken
		}
	}

	// Reverse backfill: Credentials map → legacy Slack fields.
	// Keeps code that still reads SlackAppToken/SlackBotToken working when only
	// the Credentials map was populated (e.g. from the DB credentials column).
	if c.SlackAppToken == "" {
		c.SlackAppToken = c.Credentials["app_token"]
	}
	if c.SlackBotToken == "" {
		c.SlackBotToken = c.Credentials["bot_token"]
	}
}
