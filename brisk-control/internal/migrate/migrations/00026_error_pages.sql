-- +goose Up
-- Per-zone custom 502/504 error page (Bunny-style "502/504 error pages").
-- A single TEXT column holds the branded HTML. EMPTY (the default) => OFF, so every
-- existing zone (incl. live ones) renders byte-identical nginx until a tenant pastes a
-- page. The agent emits `error_5xx_html` only when non-empty (omitempty), so a default
-- zone's agent-config payload + ETag are unchanged. Served by the edge as an internal
-- static file via `error_page 502 504` — 503 is intentionally excluded so the edge's
-- self-protection / rate-limit 503s are untouched.
ALTER TABLE zones
  ADD COLUMN error_5xx_html TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE zones
  DROP COLUMN error_5xx_html;
