-- 010_platform_neutral_credentials.sql
-- Adds unified credentials JSONB column and backfills from existing platform-specific fields.
-- Part of #77: migrate bot config/schema from Slack-shaped fields to platform-neutral model.

-- Step 1: Add credentials column
ALTER TABLE bots ADD COLUMN IF NOT EXISTS credentials JSONB NOT NULL DEFAULT '{}';

-- Step 2: Backfill credentials from existing platform-specific fields
-- For Slack bots: move slack_app_token and slack_bot_token into credentials
UPDATE bots
SET credentials = jsonb_build_object(
    'app_token', slack_app_token,
    'bot_token', slack_bot_token
)
WHERE platform = 'slack'
  AND (slack_app_token != '' OR slack_bot_token != '')
  AND credentials = '{}';

-- For Discord bots: copy discord token into credentials
UPDATE bots
SET credentials = jsonb_build_object(
    'token', discord->>'token',
    'guild_id', discord->>'guild_id'
)
WHERE platform = 'discord'
  AND discord != '{}'
  AND credentials = '{}';

-- For Mattermost bots: copy mattermost credentials into credentials
UPDATE bots
SET credentials = jsonb_build_object(
    'url', mattermost->>'url',
    'token', mattermost->>'token',
    'port', COALESCE((mattermost->>'port')::int, 8065)
)
WHERE platform = 'mattermost'
  AND mattermost != '{}'
  AND credentials = '{}';

-- Step 3: Remove silent Slack default from platform column
-- Change default from 'slack' to empty string so platform must be explicitly set.
-- NOTE: This is a breaking change for new inserts without explicit platform.
-- Existing rows are unaffected.
ALTER TABLE bots ALTER COLUMN platform SET DEFAULT '';

-- Step 4: Add comments for documentation
COMMENT ON COLUMN bots.credentials IS 'Platform-agnostic credentials JSON. Structure varies by platform. Legacy slack_app_token/slack_bot_token columns kept for backward compatibility.';
COMMENT ON COLUMN bots.platform IS 'Target platform identifier. Must be explicitly set (no silent default).';
