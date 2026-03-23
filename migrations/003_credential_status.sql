-- LLM provider credential status tracking
CREATE TABLE IF NOT EXISTS credential_status (
    provider VARCHAR(32) PRIMARY KEY,
    status VARCHAR(16) NOT NULL DEFAULT 'unknown',
    error TEXT,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
