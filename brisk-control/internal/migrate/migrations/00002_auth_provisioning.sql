-- +goose Up
ALTER TABLE servers ADD COLUMN ssh_user       TEXT;
ALTER TABLE servers ADD COLUMN ssh_port       INTEGER NOT NULL DEFAULT 22;
ALTER TABLE servers ADD COLUMN host_key       TEXT;            -- TOFU-captured SSH host key
ALTER TABLE servers ADD COLUMN provisioned_at TIMESTAMPTZ;

ALTER TABLE agent_tokens ADD COLUMN token_prefix TEXT;         -- indexed lookup key
CREATE INDEX idx_agent_tokens_prefix ON agent_tokens(token_prefix) WHERE revoked_at IS NULL;

CREATE TABLE provision_logs (
  id         BIGSERIAL PRIMARY KEY,
  server_id  BIGINT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  ts         TIMESTAMPTZ NOT NULL DEFAULT now(),
  level      TEXT NOT NULL DEFAULT 'info',
  message    TEXT NOT NULL
);
CREATE INDEX idx_provision_logs_server ON provision_logs(server_id, ts);

-- Control plane's own SSH identity (single row). Generated on first use; the
-- public key is installed into each edge's authorized_keys so later ops use key
-- auth and the operator's password is needed only once.
CREATE TABLE control_plane (
  id              SMALLINT PRIMARY KEY DEFAULT 1,
  ssh_private_key TEXT,
  ssh_public_key  TEXT,
  CONSTRAINT control_plane_singleton CHECK (id = 1)
);
INSERT INTO control_plane (id) VALUES (1);

-- +goose Down
DROP TABLE IF EXISTS control_plane;
DROP TABLE IF EXISTS provision_logs;
DROP INDEX IF EXISTS idx_agent_tokens_prefix;
ALTER TABLE agent_tokens DROP COLUMN token_prefix;
ALTER TABLE servers DROP COLUMN provisioned_at;
ALTER TABLE servers DROP COLUMN host_key;
ALTER TABLE servers DROP COLUMN ssh_port;
ALTER TABLE servers DROP COLUMN ssh_user;
