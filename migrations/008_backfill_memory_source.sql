-- 008_backfill_memory_source.sql: Backfill source_message_id on memories that were
-- created before the FK was populated at write time.
--
-- Strategy: for each orphan memory, find the most recent non-deleted message from
-- the same bot whose timestamp is <= the memory's created_at.  Memory extraction
-- runs immediately after a message arrives, so this is the most likely trigger.
--
-- Idempotent: only updates rows where source_message_id IS NULL.
-- Some memories may remain unlinked if the originating message was hard-deleted.

-- Step 1: Index for efficient backfill lookup.
-- Covers the WHERE + ORDER BY pattern in the LATERAL subquery.
CREATE INDEX IF NOT EXISTS idx_messages_data_bot_timestamp
    ON messages_data (bot_id, timestamp DESC)
    WHERE deleted_at IS NULL;

-- Step 2: Index for CDC cascade — find all memories linked to a specific message.
CREATE INDEX IF NOT EXISTS idx_memories_source_message
    ON memories (source_message_id)
    WHERE source_message_id IS NOT NULL;

-- Step 3: Backfill.
-- JOIN LATERAL fires once per orphan memory row and fetches exactly one
-- candidate message (the newest non-deleted message from the same bot that
-- arrived no later than the memory's created_at).
UPDATE memories m
SET source_message_id = closest.id
FROM (
    SELECT mem.id AS mem_id, msg.id
    FROM memories mem
    JOIN LATERAL (
        SELECT id
        FROM messages_data
        WHERE bot_id = mem.bot_id
          AND timestamp <= mem.created_at
          AND deleted_at IS NULL
        ORDER BY timestamp DESC
        LIMIT 1
    ) msg ON TRUE
    WHERE mem.source_message_id IS NULL
) closest
WHERE m.id = closest.mem_id;

-- Step 4: Report results.
DO $$
DECLARE
    linked_count   INT;
    unlinked_count INT;
BEGIN
    SELECT COUNT(*) INTO linked_count   FROM memories WHERE source_message_id IS NOT NULL;
    SELECT COUNT(*) INTO unlinked_count FROM memories WHERE source_message_id IS NULL;
    RAISE NOTICE 'Backfill complete: % linked, % unlinked (no matching message)',
        linked_count, unlinked_count;
END $$;
