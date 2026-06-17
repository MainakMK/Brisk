-- +goose Up
-- Per-zone Hotlink Protection (Referer allowlist), mirroring Bunny/KeyCDN.
-- DEFAULTS are OFF, so every existing zone (incl. live ones) renders byte-identical
-- nginx until a tenant deliberately enables it. The agent emits the `hotlink` block
-- only when enabled (omitempty), so a default zone's agent-config payload + ETag are
-- unchanged. Enforced at the edge via nginx's native `valid_referers` + a 403 guard
-- on CONTENT locations only (never /healthz or the ACME challenge).
ALTER TABLE zones
  -- Master switch. Off => no referer check (today's behavior).
  ADD COLUMN hotlink_enabled BOOLEAN NOT NULL DEFAULT false,

  -- Comma-separated allowed referer hostnames (validated on write). May include
  -- wildcards like *.example.com. Empty + enabled => only same-host (server_names)
  -- referers (and empty, if allowed) pass.
  ADD COLUMN hotlink_allowed_referrers TEXT NOT NULL DEFAULT '',

  -- Whether to allow requests with NO Referer header (direct hits, email clients,
  -- some mobile apps, privacy browsers). Default true (the safe, Bunny-style default):
  -- empty referers are allowed so we don't break legitimate direct access. Set false
  -- to also block direct URL access (the stricter "Block Direct URL Access" option).
  ADD COLUMN hotlink_allow_empty_referer BOOLEAN NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE zones
  DROP COLUMN hotlink_enabled,
  DROP COLUMN hotlink_allowed_referrers,
  DROP COLUMN hotlink_allow_empty_referer;
