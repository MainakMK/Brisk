-- +goose Up
-- Per-server health-check state + overrides (Phase 3 Step 4 — fast failover).
-- The self-driven health checker probes each online edge; on failure it disables
-- the edge's DNS record immediately (off != delete). These columns persist the
-- last-known health (so a brisk-control restart doesn't blackhole or thrash) and
-- let an operator tune one PoP's checking without touching the network default.
--   health_status            unknown | healthy | unhealthy (last probe verdict)
--   health_checked_at        timestamp of the last probe (for /health/status)
--   health_enabled           false = skip probing this PoP (treated as healthy)
--   health_interval_seconds  0 = inherit BRISK_HEALTH_INTERVAL
--   health_fail_threshold    0 = inherit BRISK_HEALTH_FAIL_THRESHOLD (fail fast)
--   health_rise_threshold    0 = inherit BRISK_HEALTH_RISE_THRESHOLD (recover careful)
ALTER TABLE servers
  ADD COLUMN health_status           TEXT        NOT NULL DEFAULT 'unknown',
  ADD COLUMN health_checked_at       TIMESTAMPTZ,
  ADD COLUMN health_enabled          BOOLEAN     NOT NULL DEFAULT true,
  ADD COLUMN health_interval_seconds INTEGER     NOT NULL DEFAULT 0,
  ADD COLUMN health_fail_threshold   INTEGER     NOT NULL DEFAULT 0,
  ADD COLUMN health_rise_threshold   INTEGER     NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE servers
  DROP COLUMN IF EXISTS health_rise_threshold,
  DROP COLUMN IF EXISTS health_fail_threshold,
  DROP COLUMN IF EXISTS health_interval_seconds,
  DROP COLUMN IF EXISTS health_enabled,
  DROP COLUMN IF EXISTS health_checked_at,
  DROP COLUMN IF EXISTS health_status;
