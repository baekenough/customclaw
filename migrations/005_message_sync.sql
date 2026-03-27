-- 005_message_sync.sql: Add soft delete and edit tracking for messages.
-- Supports platform message deletion/edit synchronization.
-- Rollback safe: all columns are nullable/additive.

-- Step 1: Add new columns to messages table
ALTER TABLE messages ADD COLUMN IF NOT EXISTS platform_message_id VARCHAR(128);
ALTER TABLE messages ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS edited_at TIMESTAMPTZ;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS original_content TEXT;

-- Step 2: Index for platform message lookups (per bot)
CREATE INDEX IF NOT EXISTS idx_messages_platform_msg_id
ON messages (bot_id, platform_message_id)
WHERE platform_message_id IS NOT NULL;

-- Step 3: Partial index for active messages (used by the view)
CREATE INDEX IF NOT EXISTS idx_messages_active
ON messages (bot_id, channel_id, created_at)
WHERE deleted_at IS NULL;

-- Step 4: Rename table and create updatable view
-- The view filters out soft-deleted messages, preserving all existing query behavior.
ALTER TABLE messages RENAME TO messages_data;

CREATE VIEW messages AS
SELECT * FROM messages_data WHERE deleted_at IS NULL;

-- Step 5: Update FK constraint on memories table
-- Drop the existing RESTRICT FK so soft-deleting a message doesn't fail.
-- Re-add as SET NULL so that deleting a message_data row nullifies the reference.
ALTER TABLE memories DROP CONSTRAINT IF EXISTS memories_source_message_id_fkey;
ALTER TABLE memories
    ADD CONSTRAINT memories_source_message_id_fkey
    FOREIGN KEY (source_message_id) REFERENCES messages_data(id) ON DELETE SET NULL;
