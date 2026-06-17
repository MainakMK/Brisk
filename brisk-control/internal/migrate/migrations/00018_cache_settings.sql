-- +goose Up
-- Per-zone Cache Settings (Phase 4 follow-up — Bunny-style Smart Cache surface).
-- Every DEFAULT here reproduces TODAY's hard-coded edge behavior EXACTLY, so all
-- existing zones (incl. the live cdn.a2zjav.com) render byte-identical nginx until a
-- tenant deliberately changes a control. The agent only emits non-default settings
-- (omitempty), so the agent-config payload + ETag are unchanged for default zones.
ALTER TABLE zones
  -- Smart Cache: cache by file extension/MIME regardless of origin Cache-Control.
  -- Off by default (today's static-asset location already long-caches by extension;
  -- this extends that decision to the dynamic location when on).
  ADD COLUMN cache_smart BOOLEAN NOT NULL DEFAULT false,

  -- Edge (proxy) cache expiration. 'default' renders today's per-class TTLs
  -- (30d static / 10m html / video TTLs); 'respect_origin' honors origin
  -- Cache-Control; 'override' uses cache_edge_ttl; 'no_cache' disables edge caching.
  ADD COLUMN cache_edge_mode TEXT NOT NULL DEFAULT 'default'
    CHECK (cache_edge_mode IN ('default','respect_origin','override','no_cache')),
  ADD COLUMN cache_edge_ttl TEXT NOT NULL DEFAULT '',   -- nginx time (e.g. '1h','7d') when mode=override

  -- Browser cache expiration (Cache-Control to the client). 'default' keeps today's
  -- `public, max-age=2592000` on static assets (nothing extra on html); 'match_server'
  -- mirrors the edge TTL; 'override' uses cache_browser_ttl; 'no_cache' sends no-store.
  ADD COLUMN cache_browser_mode TEXT NOT NULL DEFAULT 'default'
    CHECK (cache_browser_mode IN ('default','match_server','override','no_cache')),
  ADD COLUMN cache_browser_ttl TEXT NOT NULL DEFAULT '',

  -- Query-string sort: normalize ?b=2&a=1 == ?a=1&b=2 into one cache entry.
  ADD COLUMN cache_query_sort BOOLEAN NOT NULL DEFAULT false,

  -- Briefly cache origin 5xx (~5s) to shield the origin from retry storms.
  ADD COLUMN cache_error_responses BOOLEAN NOT NULL DEFAULT false,

  -- Vary Cache dimensions — each adds a dimension to proxy_cache_key (a separate
  -- cached variant). $host is ALWAYS in the key (tenant isolation), independent of these.
  ADD COLUMN cache_vary_webp BOOLEAN NOT NULL DEFAULT false,        -- browser WebP/AVIF support (Accept)
  ADD COLUMN cache_vary_device BOOLEAN NOT NULL DEFAULT false,      -- desktop/mobile (UA class)
  ADD COLUMN cache_vary_country BOOLEAN NOT NULL DEFAULT false,     -- GeoIP country (needs GeoLite2 on edge)
  ADD COLUMN cache_vary_cookie TEXT NOT NULL DEFAULT '',            -- vary on this cookie's value (empty=off)
  ADD COLUMN cache_vary_querystring BOOLEAN NOT NULL DEFAULT false, -- fold the full query string into the key
  ADD COLUMN cache_vary_headers TEXT NOT NULL DEFAULT '',           -- comma-separated request header names
  ADD COLUMN cache_query_whitelist TEXT NOT NULL DEFAULT '',        -- comma-separated args; only these count

  -- Strip Set-Cookie from responses so a cookie-setting origin is still cacheable
  -- on the dynamic location (the static location already strips Set-Cookie).
  ADD COLUMN cache_strip_cookies BOOLEAN NOT NULL DEFAULT false,

  -- Optimize for large objects = the slice module. Default false; the edge renders
  -- slicing when (video OR this) is on, so video zones keep slicing (parity) and a
  -- non-video zone can opt in for big files / byte-range.
  ADD COLUMN cache_large_object BOOLEAN NOT NULL DEFAULT false,

  -- Stale cache. Both ON by default = today's proxy_cache_use_stale (which already
  -- includes the http_5xx + updating flags). Turning one off narrows the stale set.
  ADD COLUMN cache_stale_offline BOOLEAN NOT NULL DEFAULT true,     -- serve stale while origin is down (5xx/error/timeout)
  ADD COLUMN cache_stale_updating BOOLEAN NOT NULL DEFAULT true;    -- serve stale while revalidating

-- +goose Down
ALTER TABLE zones
  DROP COLUMN cache_smart,
  DROP COLUMN cache_edge_mode,
  DROP COLUMN cache_edge_ttl,
  DROP COLUMN cache_browser_mode,
  DROP COLUMN cache_browser_ttl,
  DROP COLUMN cache_query_sort,
  DROP COLUMN cache_error_responses,
  DROP COLUMN cache_vary_webp,
  DROP COLUMN cache_vary_device,
  DROP COLUMN cache_vary_country,
  DROP COLUMN cache_vary_cookie,
  DROP COLUMN cache_vary_querystring,
  DROP COLUMN cache_vary_headers,
  DROP COLUMN cache_query_whitelist,
  DROP COLUMN cache_strip_cookies,
  DROP COLUMN cache_large_object,
  DROP COLUMN cache_stale_updating,
  DROP COLUMN cache_stale_offline;
