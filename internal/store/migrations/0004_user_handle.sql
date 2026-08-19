-- The WebAuthn user handle is part of the credential, not an implementation
-- detail: an authenticator stores it at registration and returns it on every
-- assertion. It must therefore be stored and stable, never derived from a
-- row id that a fresh install would generate differently.
ALTER TABLE users ADD COLUMN webauthn_id BLOB;
