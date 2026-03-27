-- 006_bot_llm_keys.sql: Per-bot LLM API keys (nullable — falls back to env vars when NULL)
ALTER TABLE bots ADD COLUMN IF NOT EXISTS anthropic_api_key TEXT;
ALTER TABLE bots ADD COLUMN IF NOT EXISTS openai_api_key TEXT;
ALTER TABLE bots ADD COLUMN IF NOT EXISTS gemini_api_key TEXT;
