-- +goose Up
-- Admin authentication (Phase 3.7 Step 3). Human (dashboard/API) auth, kept
-- entirely separate from the agent token path (/agent/* — internal/auth, untouched).
--
-- Threat-model split: agent tokens are 256-bit random -> SHA-256 (fast) is fine.
-- Human passwords are low-entropy -> argon2id (slow KDF). Admin API tokens are
-- 256-bit random -> SHA-256 like agent tokens but in a SEPARATE table.

-- accounts gains login credentials. email is the login handle (unique, case-folded
-- by the app). password_hash is argon2id (NULL until bootstrapped).
ALTER TABLE accounts
  ADD COLUMN email         TEXT UNIQUE,
  ADD COLUMN password_hash TEXT,
  ADD COLUMN updated_at    TIMESTAMPTZ NOT NULL DEFAULT now();

-- sessions: server-side, opaque session id (hashed at rest). The cookie holds the
-- plaintext id; we store only its SHA-256, so a DB leak can't mint sessions.
-- csrf_hash backs double-submit CSRF (bound to the session, not just a cookie).
CREATE TABLE sessions (
  id           BIGSERIAL   PRIMARY KEY,
  token_hash   TEXT        NOT NULL UNIQUE,            -- sha256(session id)
  csrf_hash    TEXT        NOT NULL,                   -- sha256(csrf token)
  account_id   BIGINT      NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  user_agent   TEXT        NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_sessions_account ON sessions (account_id);
CREATE INDEX idx_sessions_expires ON sessions (expires_at);

-- admin_api_tokens: opaque bearer tokens for scripts/automation (created in the
-- dashboard, shown once, hashed at rest, revocable). Separate from agent_tokens.
CREATE TABLE admin_api_tokens (
  id           BIGSERIAL   PRIMARY KEY,
  account_id   BIGINT      NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  name         TEXT        NOT NULL DEFAULT '',
  prefix       TEXT        NOT NULL,                   -- indexed lookup prefix
  token_hash   TEXT        NOT NULL,                   -- sha256(full token)
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at TIMESTAMPTZ,
  revoked_at   TIMESTAMPTZ
);
CREATE INDEX idx_admin_tokens_prefix ON admin_api_tokens (prefix);
CREATE INDEX idx_admin_tokens_account ON admin_api_tokens (account_id);

-- +goose Down
DROP TABLE IF EXISTS admin_api_tokens;
DROP TABLE IF EXISTS sessions;
ALTER TABLE accounts
  DROP COLUMN IF EXISTS updated_at,
  DROP COLUMN IF EXISTS password_hash,
  DROP COLUMN IF EXISTS email;
