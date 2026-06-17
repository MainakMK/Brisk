-- +goose Up
-- TLS realignment: "Automatic SSL" (tls_mode=managed) is the only certificate path
-- a user can set via the API. Make `managed` the column default (was 'letsencrypt',
-- the per-edge HTTP-01 mode that crash-looped edges) and formalize the valid value
-- set with a CHECK constraint (the audit found NONE). The CHECK keeps all four
-- historical values legal at the DB layer — the API is the managed-only gate — so
-- this never rejects an existing row. Existing rows are NOT mutated: zones 12/13 are
-- already 'managed'.
ALTER TABLE zones ALTER COLUMN tls_mode SET DEFAULT 'managed';
ALTER TABLE zones ADD CONSTRAINT zones_tls_mode_check
  CHECK (tls_mode IN ('managed', 'letsencrypt', 'selfsigned', 'mkcert'));

-- +goose Down
ALTER TABLE zones DROP CONSTRAINT IF EXISTS zones_tls_mode_check;
ALTER TABLE zones ALTER COLUMN tls_mode SET DEFAULT 'letsencrypt';
