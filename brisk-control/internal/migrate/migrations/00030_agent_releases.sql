-- +goose Up
-- Signed agent release binaries the fleet can self-update to. `bin_data` is the static
-- linux-amd64 brisk-agent (~20MB; `binary` is a reserved word in Postgres, hence bin_data);
-- `sha256` + ed25519 `signature` are verified on upload AND again by each agent before it swaps
-- (the signature, not the channel, is the trust anchor).
-- signed_by names which trusted key signed it (k1 primary / k2 rotation spare).
-- uploaded_by = account id (audit). Newest by created_at is "latest".
CREATE TABLE IF NOT EXISTS agent_releases (
  version     TEXT PRIMARY KEY,
  bin_data    BYTEA NOT NULL,
  sha256      TEXT NOT NULL,
  signature   TEXT NOT NULL,
  signed_by   TEXT NOT NULL DEFAULT 'k1',
  size_bytes  BIGINT NOT NULL,
  notes       TEXT NOT NULL DEFAULT '',
  uploaded_by TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS agent_releases;
