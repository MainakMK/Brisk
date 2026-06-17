-- +goose Up
-- Per-zone access toggles (Bunny-style "Block root path access" + "Block POST requests").
-- Both default FALSE => every existing zone (incl. live ones) renders byte-identical nginx
-- until a tenant flips one. The agent emits each flag only when true (omitempty), so a
-- default zone's agent-config payload + ETag are unchanged. Enforced at the edge by a gated
-- server-level `if` (return 403 for any path ending in "/"; return 405 for POST) on the
-- :443 vhost — /healthz (no trailing slash, GET) and the :80 ACME challenge are untouched.
ALTER TABLE zones
  ADD COLUMN block_root_path BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN block_post      BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE zones
  DROP COLUMN block_root_path,
  DROP COLUMN block_post;
