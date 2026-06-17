-- +goose Up
-- Per-zone upstream Host header (Phase 4 Step 1 — multi-tenant origin routing).
--
-- Each tenant zone proxies to its OWN origin. Many origins serve content keyed on
-- the Host header the request carries; that header is often the origin's own
-- hostname, NOT the Brisk CDN hostname. host_header lets an operator set the exact
-- Host to send upstream per zone. Empty (the default) means "derive it from
-- origin_url" — which preserves the existing single-tenant behavior byte-for-byte.
ALTER TABLE zones
  ADD COLUMN host_header TEXT NOT NULL DEFAULT '';

-- cdn_hostname is already UNIQUE (00001_init) — one zone = one routing hostname =
-- one nginx server block. No change needed; noted here for the multi-tenant intent.

-- +goose Down
ALTER TABLE zones
  DROP COLUMN IF EXISTS host_header;
