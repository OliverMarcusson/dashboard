-- Identity. Single-user today, but keyed so that stays an assumption and not a constraint.
CREATE TABLE users (
  id            TEXT PRIMARY KEY,
  username      TEXT NOT NULL UNIQUE,
  display_name  TEXT NOT NULL,
  created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Credentials are stored decomposed rather than as an opaque library blob, so a future
-- library change is a mapping problem and not a re-enrollment.
CREATE TABLE passkeys (
  id               TEXT PRIMARY KEY,
  user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name             TEXT NOT NULL,
  credential_id    BLOB NOT NULL UNIQUE,
  public_key       BLOB NOT NULL,
  attestation_type TEXT NOT NULL DEFAULT '',
  transports       TEXT NOT NULL DEFAULT '[]',
  aaguid           BLOB,
  sign_count       INTEGER NOT NULL DEFAULT 0,
  user_verified    INTEGER NOT NULL DEFAULT 0,
  backup_eligible  INTEGER NOT NULL DEFAULT 0,
  backup_state     INTEGER NOT NULL DEFAULT 0,
  created_at       TEXT NOT NULL DEFAULT (datetime('now')),
  last_used_at     TEXT,
  revoked_at       TEXT
);
CREATE INDEX idx_passkeys_user ON passkeys(user_id) WHERE revoked_at IS NULL;

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  token_hash BLOB NOT NULL UNIQUE,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  user_agent TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  expires_at TEXT NOT NULL
);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

CREATE TABLE enrollment_codes (
  id         TEXT PRIMARY KEY,
  code_hash  BLOB NOT NULL UNIQUE,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  expires_at TEXT NOT NULL,
  used_at    TEXT
);

-- Short-lived WebAuthn ceremony state. Rows are single-use and swept on read.
CREATE TABLE webauthn_states (
  id         TEXT PRIMARY KEY,
  kind       TEXT NOT NULL,
  state_json TEXT NOT NULL,
  expires_at TEXT NOT NULL
);

CREATE TABLE audit_events (
  id          TEXT PRIMARY KEY,
  event       TEXT NOT NULL,
  actor       TEXT NOT NULL DEFAULT '',
  detail_json TEXT NOT NULL DEFAULT '{}',
  created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_audit_created ON audit_events(created_at DESC);
