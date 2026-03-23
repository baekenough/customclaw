-- Add platform and discord columns to bots table
ALTER TABLE bots ADD COLUMN IF NOT EXISTS platform VARCHAR(16) NOT NULL DEFAULT 'slack';
ALTER TABLE bots ADD COLUMN IF NOT EXISTS discord JSONB NOT NULL DEFAULT '{}';
ALTER TABLE bots ADD COLUMN IF NOT EXISTS mattermost JSONB NOT NULL DEFAULT '{}';

-- Allow slack tokens to be empty for non-Slack platforms
ALTER TABLE bots ALTER COLUMN slack_app_token SET DEFAULT '';
ALTER TABLE bots ALTER COLUMN slack_bot_token SET DEFAULT '';
