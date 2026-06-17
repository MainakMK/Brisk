-- +goose Up
-- Phase 4 Step 5 — per-zone request/response header transforms, enforced at the
-- edge by the Lua layer. (The custom cache_rules table already exists since 00001;
-- this step finally enforces those + adds header transforms.) Ordered per zone,
-- first applicable wins per header; a managed-header deny-list is enforced in the
-- API + Lua so a tenant can't clobber X-Brisk-*, HSTS/TLS, or internal headers.
CREATE TABLE header_transforms (
  id          BIGSERIAL PRIMARY KEY,
  zone_id     BIGINT NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
  priority    INTEGER NOT NULL DEFAULT 0,
  phase       TEXT NOT NULL,                    -- request | response
  op          TEXT NOT NULL,                    -- set | remove
  header      TEXT NOT NULL,
  value       TEXT,                             -- NULL for remove
  match_type  TEXT NOT NULL DEFAULT 'all',      -- all | path_prefix | path_regex | method
  match_value TEXT,
  enabled     BOOLEAN NOT NULL DEFAULT true,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX header_transforms_zone ON header_transforms (zone_id, priority);

-- +goose Down
DROP TABLE IF EXISTS header_transforms;
