ALTER TABLE passkeys ADD COLUMN credential_id TEXT;
ALTER TABLE passkeys ADD COLUMN revoked_at TEXT;
CREATE INDEX IF NOT EXISTS idx_passkeys_credential_id ON passkeys(credential_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);
