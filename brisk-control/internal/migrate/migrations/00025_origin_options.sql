-- +goose Up
-- Per-zone Origin connection options (Bunny-style "Origin" panel), mirroring
-- bunny.net's pull-zone origin settings. DEFAULTS are OFF, reproducing today's exact
-- edge behavior, so every existing zone (incl. live ones) renders byte-identical nginx
-- until a tenant deliberately enables one. The agent emits each flag only when true
-- (omitempty), so a default zone's agent-config payload + ETag are unchanged.
ALTER TABLE zones
  -- Verify the origin's TLS certificate (nginx proxy_ssl_verify). Off (today) => the
  -- edge trusts the origin's cert blindly; on => it validates against the system CA
  -- bundle. Only meaningful for https origins.
  ADD COLUMN origin_ssl_verify BOOLEAN NOT NULL DEFAULT false,

  -- Follow origin redirects server-side (one hop) instead of passing/caching the 3xx
  -- to the client. Off (today) => a 301/302 from the origin is returned to the end
  -- user as-is. Applies to the direct-origin path (non-shielded zones).
  ADD COLUMN origin_follow_redirects BOOLEAN NOT NULL DEFAULT false,

  -- Forward the END USER's Host header to the origin (nginx Host=$host) instead of the
  -- per-zone upstream Host. Off (today) => the edge sends the configured/derived
  -- upstream Host. Overrides the host_header field when on.
  ADD COLUMN forward_host_header BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE zones
  DROP COLUMN origin_ssl_verify,
  DROP COLUMN origin_follow_redirects,
  DROP COLUMN forward_host_header;
