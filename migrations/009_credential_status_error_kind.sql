-- Add error classification to credential_status
ALTER TABLE credential_status ADD COLUMN IF NOT EXISTS error_kind VARCHAR(16);
COMMENT ON COLUMN credential_status.error_kind IS 'Error classification: auth, quota, transient, network';
