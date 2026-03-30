package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/baekenough/customclaw/internal/config"
)

// dbConnect opens a short-lived PostgreSQL connection using DATABASE_DSN.
func dbConnect(ctx context.Context) (*pgx.Conn, error) {
	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_DSN not set")
	}
	return pgx.Connect(ctx, dsn)
}

// redisPublish publishes a hot-reload event for the given bot ID.
// It uses config.ConfigChannel and publishes the bare bot ID string (not JSON)
// to match the format expected by config.StartConfigSubscriber.
func redisPublish(ctx context.Context, botID string) error {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("parse REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(opt)
	defer func() { _ = rdb.Close() }()

	return rdb.Publish(ctx, config.ConfigChannel, botID).Err()
}

// CreateBotTool inserts a new bot record into the database.
type CreateBotTool struct{}

func (t *CreateBotTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "create_bot",
		Description: "Create a new bot and register it in the system. " +
			"Supports Slack, Discord, and Mattermost. " +
			"Provide the platform and credentials dict for the chosen platform. " +
			"Slack: credentials must include app_token (xapp-...) and bot_token (xoxb-...). " +
			"Mattermost: credentials must include url and token. " +
			"Discord: credentials must include token.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Unique bot identifier (slug)",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Human-readable bot name",
				},
				"platform": map[string]any{
					"type":        "string",
					"description": "Target platform for the bot",
					"enum":        []string{"slack", "discord", "mattermost"},
				},
				"credentials": map[string]any{
					"type": "object",
					"description": "Platform credentials. " +
						"Slack: {app_token, bot_token}. " +
						"Mattermost: {url, token, port?}. " +
						"Discord: {token, guild_id?}.",
				},
				"personality": map[string]any{
					"type":        "string",
					"description": "Bot personality description for the LLM",
				},
			},
			"required": []string{"id", "name", "platform"},
		},
	}
}

func (t *CreateBotTool) Execute(args map[string]any) ToolResult {
	return t.ExecuteWithContext(context.Background(), args)
}

func (t *CreateBotTool) ExecuteWithContext(ctx context.Context, args map[string]any) ToolResult {
	id, _ := args["id"].(string)
	name, _ := args["name"].(string)
	if id == "" || name == "" {
		return ToolResult{IsError: true, Content: "id and name are required"}
	}
	platform, _ := args["platform"].(string)
	if platform == "" {
		return ToolResult{IsError: true, Content: "platform is required (slack, discord, or mattermost)"}
	}
	personality, _ := args["personality"].(string)

	credentials, _ := args["credentials"].(map[string]any)
	if credentials == nil {
		credentials = map[string]any{}
	}
	credentialsJSON, _ := json.Marshal(credentials)

	conn, err := dbConnect(ctx)
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("db connect: %v", err)}
	}
	defer func() { _ = conn.Close(ctx) }()

	botConfig := map[string]any{
		"id":       id,
		"name":     name,
		"platform": platform,
		"persona":  map[string]any{"personality": personality},
	}
	configJSON, _ := json.Marshal(botConfig)

	_, err = conn.Exec(ctx,
		`INSERT INTO bots (id, name, platform, credentials, config, is_active, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, true, $6, $6)`,
		id, name, platform, string(credentialsJSON), string(configJSON), time.Now().UTC(),
	)
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("insert bot: %v", err)}
	}

	_ = redisPublish(ctx, id)
	return ToolResult{Content: fmt.Sprintf("Bot '%s' (%s) created successfully on %s", name, id, platform)}
}

// ListBotsTool lists all active bots from the database.
type ListBotsTool struct{}

func (t *ListBotsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "list_bots",
		Description: "List all registered bots in the system.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t *ListBotsTool) Execute(args map[string]any) ToolResult {
	return t.ExecuteWithContext(context.Background(), args)
}

func (t *ListBotsTool) ExecuteWithContext(ctx context.Context, _ map[string]any) ToolResult {
	conn, err := dbConnect(ctx)
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("db connect: %v", err)}
	}
	defer func() { _ = conn.Close(ctx) }()

	rows, err := conn.Query(ctx,
		`SELECT id, name, platform, is_active FROM bots ORDER BY name`)
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("query bots: %v", err)}
	}
	defer rows.Close()

	var sb strings.Builder
	sb.WriteString("Registered bots:\n")
	count := 0
	for rows.Next() {
		var id, name, platform string
		var isActive bool
		if err := rows.Scan(&id, &name, &platform, &isActive); err != nil {
			continue
		}
		status := "active"
		if !isActive {
			status = "inactive"
		}
		fmt.Fprintf(&sb, "  - %s (%s) [%s] %s\n", name, id, platform, status)
		count++
	}
	if count == 0 {
		return ToolResult{Content: "No bots found."}
	}
	return ToolResult{Content: sb.String()}
}

// UpdateBotTool updates specific fields of a bot's configuration.
type UpdateBotTool struct{}

func (t *UpdateBotTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "update_bot",
		Description: "Update a bot's configuration fields.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Bot identifier to update",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "New display name",
				},
				"personality": map[string]any{
					"type":        "string",
					"description": "New personality description",
				},
				"is_active": map[string]any{
					"type":        "boolean",
					"description": "Set active/inactive state",
				},
			},
			"required": []string{"id"},
		},
	}
}

func (t *UpdateBotTool) Execute(args map[string]any) ToolResult {
	return t.ExecuteWithContext(context.Background(), args)
}

func (t *UpdateBotTool) ExecuteWithContext(ctx context.Context, args map[string]any) ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return ToolResult{IsError: true, Content: "id is required"}
	}

	conn, err := dbConnect(ctx)
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("db connect: %v", err)}
	}
	defer func() { _ = conn.Close(ctx) }()

	var setClauses []string
	var params []any
	paramIdx := 1

	if name, ok := args["name"].(string); ok && name != "" {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", paramIdx))
		params = append(params, name)
		paramIdx++
	}
	if isActive, ok := args["is_active"].(bool); ok {
		setClauses = append(setClauses, fmt.Sprintf("is_active = $%d", paramIdx))
		params = append(params, isActive)
		paramIdx++
	}

	setClauses = append(setClauses, fmt.Sprintf("updated_at = $%d", paramIdx))
	params = append(params, time.Now().UTC())
	paramIdx++

	params = append(params, id)

	if len(setClauses) == 1 {
		// Only updated_at was added — check for personality in config JSON.
		if personality, ok := args["personality"].(string); ok && personality != "" {
			_, err = conn.Exec(ctx,
				fmt.Sprintf("UPDATE bots SET config = jsonb_set(config, '{persona,personality}', to_jsonb($1::text)), updated_at = $2 WHERE id = $%d", paramIdx),
				personality, time.Now().UTC(), id,
			)
			if err != nil {
				return ToolResult{IsError: true, Content: fmt.Sprintf("update bot: %v", err)}
			}
			_ = redisPublish(ctx, id)
			return ToolResult{Content: fmt.Sprintf("Bot %s personality updated.", id)}
		}
		return ToolResult{IsError: true, Content: "no updatable fields provided"}
	}

	query := fmt.Sprintf("UPDATE bots SET %s WHERE id = $%d",
		strings.Join(setClauses, ", "), paramIdx)
	tag, err := conn.Exec(ctx, query, params...)
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("update bot: %v", err)}
	}
	if tag.RowsAffected() == 0 {
		return ToolResult{IsError: true, Content: fmt.Sprintf("bot %s not found", id)}
	}

	_ = redisPublish(ctx, id)
	return ToolResult{Content: fmt.Sprintf("Bot %s updated successfully.", id)}
}

// DeleteBotTool deactivates a bot (soft delete: sets is_active=false).
type DeleteBotTool struct{}

func (t *DeleteBotTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "delete_bot",
		Description: "Deactivate a bot (soft delete). The bot will no longer respond to messages.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Bot identifier to deactivate",
				},
			},
			"required": []string{"id"},
		},
	}
}

func (t *DeleteBotTool) Execute(args map[string]any) ToolResult {
	return t.ExecuteWithContext(context.Background(), args)
}

func (t *DeleteBotTool) ExecuteWithContext(ctx context.Context, args map[string]any) ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return ToolResult{IsError: true, Content: "id is required"}
	}

	conn, err := dbConnect(ctx)
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("db connect: %v", err)}
	}
	defer func() { _ = conn.Close(ctx) }()

	tag, err := conn.Exec(ctx,
		"UPDATE bots SET is_active = false, updated_at = $1 WHERE id = $2",
		time.Now().UTC(), id,
	)
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("deactivate bot: %v", err)}
	}
	if tag.RowsAffected() == 0 {
		return ToolResult{IsError: true, Content: fmt.Sprintf("bot %s not found", id)}
	}

	_ = redisPublish(ctx, id)
	return ToolResult{Content: fmt.Sprintf("Bot %s has been deactivated.", id)}
}

// RestartRuntimeTool publishes a restart request for a bot via Redis.
type RestartRuntimeTool struct{}

func (t *RestartRuntimeTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "restart_runtime",
		Description: "Request a runtime restart for a specific bot.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"bot_id": map[string]any{
					"type":        "string",
					"description": "Bot identifier to restart",
				},
			},
			"required": []string{"bot_id"},
		},
	}
}

func (t *RestartRuntimeTool) Execute(args map[string]any) ToolResult {
	return t.ExecuteWithContext(context.Background(), args)
}

func (t *RestartRuntimeTool) ExecuteWithContext(ctx context.Context, args map[string]any) ToolResult {
	botID, _ := args["bot_id"].(string)
	if botID == "" {
		return ToolResult{IsError: true, Content: "bot_id is required"}
	}

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("parse REDIS_URL: %v", err)}
	}
	rdb := redis.NewClient(opt)
	defer func() { _ = rdb.Close() }()

	payload, _ := json.Marshal(map[string]string{
		"bot_id": botID,
		"action": "restart",
	})
	if err := rdb.Publish(ctx, "customclaw:runtime-control", payload).Err(); err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf("publish restart: %v", err)}
	}

	return ToolResult{Content: fmt.Sprintf("Restart request sent for bot %s.", botID)}
}
