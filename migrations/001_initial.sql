-- Enable pgvector extension
CREATE EXTENSION IF NOT EXISTS vector;

-- Conversation messages
CREATE TABLE IF NOT EXISTS messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bot_id VARCHAR(64) NOT NULL,
    channel_id VARCHAR(64) NOT NULL,
    thread_ts VARCHAR(64),
    user_id VARCHAR(64) NOT NULL,
    role VARCHAR(16) NOT NULL,
    content TEXT NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    embedding vector(1024),
    metadata JSONB DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_messages_bot_channel ON messages(bot_id, channel_id, thread_ts);
CREATE INDEX IF NOT EXISTS idx_messages_embedding ON messages USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);

-- Long-term memories
CREATE TABLE IF NOT EXISTS memories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bot_id VARCHAR(64) NOT NULL,
    user_id VARCHAR(64),
    category VARCHAR(32) NOT NULL,
    content TEXT NOT NULL,
    source_message_id UUID REFERENCES messages(id),
    embedding vector(1024) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    metadata JSONB DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_memories_bot ON memories(bot_id);
CREATE INDEX IF NOT EXISTS idx_memories_embedding ON memories USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);

-- Bot configurations
CREATE TABLE IF NOT EXISTS bots (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    slack_app_token TEXT NOT NULL,
    slack_bot_token TEXT NOT NULL,
    channels JSONB NOT NULL DEFAULT '[]',
    persona JSONB NOT NULL DEFAULT '{}',
    project JSONB NOT NULL DEFAULT '{}',
    airflow JSONB DEFAULT '{}',
    tools JSONB NOT NULL DEFAULT '{}',
    claude JSONB NOT NULL DEFAULT '{}',
    memory JSONB NOT NULL DEFAULT '{}',
    security JSONB NOT NULL DEFAULT '{}',
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- API usage tracking
CREATE TABLE IF NOT EXISTS api_usage_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bot_id VARCHAR(64) NOT NULL,
    user_id VARCHAR(64),
    model VARCHAR(64) NOT NULL,
    input_tokens INTEGER NOT NULL,
    output_tokens INTEGER NOT NULL,
    cost_usd NUMERIC(10, 6),
    tool_name VARCHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_api_usage_bot_date ON api_usage_logs(bot_id, created_at);
