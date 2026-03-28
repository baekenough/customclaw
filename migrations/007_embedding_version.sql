-- 007_embedding_version.sql: Add embedding version tracking for RAG pipeline (#54)
ALTER TABLE memories ADD COLUMN IF NOT EXISTS embedding_version TEXT DEFAULT 'v1';
ALTER TABLE memories ADD COLUMN IF NOT EXISTS embedded_at TIMESTAMPTZ DEFAULT NOW();
CREATE INDEX IF NOT EXISTS idx_memories_embedding_version ON memories(embedding_version);
