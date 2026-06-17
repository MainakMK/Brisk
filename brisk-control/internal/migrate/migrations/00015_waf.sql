-- +goose Up
-- Phase 4 Step 4 — per-zone WAF + rate limiting. Mirrors how the big CDNs layer
-- security: managed OWASP CRS + custom rules + rate limits, per tenant zone, with
-- a detect (log-only) vs block mode and a security-event firewall log.

-- Per-zone WAF settings. WAF is OFF by default (live zones unaffected); enabling a
-- zone defaults to DETECT mode so a tenant tunes false positives before enforcing.
ALTER TABLE zones
  ADD COLUMN waf_enabled         BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN waf_mode            TEXT    NOT NULL DEFAULT 'detect',     -- detect | block
  ADD COLUMN waf_managed_ruleset TEXT    NOT NULL DEFAULT 'owasp_crs',  -- off | owasp_crs
  ADD COLUMN waf_paranoia        INTEGER NOT NULL DEFAULT 1,            -- CRS paranoia 1..4
  ADD COLUMN waf_wp_preset       BOOLEAN NOT NULL DEFAULT false,        -- WordPress hardening preset
  ADD COLUMN waf_fail_open       BOOLEAN NOT NULL DEFAULT true;         -- on engine error: fail-open (avail) vs closed

-- Custom rules: ordered per zone, evaluated before the managed ruleset. A
-- terminating action (block/challenge/allow) short-circuits later evaluation.
CREATE TABLE waf_custom_rules (
  id          BIGSERIAL PRIMARY KEY,
  zone_id     BIGINT NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
  priority    INTEGER NOT NULL DEFAULT 0,                 -- lower runs first
  field       TEXT NOT NULL,                              -- ip | country | path | method | header | user_agent
  op          TEXT NOT NULL DEFAULT 'eq',                 -- eq | prefix | regex | cidr
  value       TEXT NOT NULL,
  header_name TEXT,                                       -- when field='header'
  action      TEXT NOT NULL,                              -- block | challenge | log | allow
  enabled     BOOLEAN NOT NULL DEFAULT true,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX waf_custom_rules_zone ON waf_custom_rules (zone_id, priority);

-- Rate limits: per zone, scoped to a path + client key. Enforced by Nginx native
-- limit_req at the edge (per-edge counters, approximate — documented in the UI).
CREATE TABLE waf_rate_limits (
  id             BIGSERIAL PRIMARY KEY,
  zone_id        BIGINT NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
  path_match     TEXT NOT NULL,                           -- e.g. /wp-login.php or /
  match_type     TEXT NOT NULL DEFAULT 'exact',           -- exact | prefix
  requests       INTEGER NOT NULL,                        -- N requests ...
  period_seconds INTEGER NOT NULL,                        -- ... per this period
  burst          INTEGER NOT NULL DEFAULT 0,              -- allowed short burst
  key            TEXT NOT NULL DEFAULT 'ip',              -- ip | ip_path
  action         TEXT NOT NULL DEFAULT 'block',           -- block (429) | challenge
  count_mode     TEXT NOT NULL DEFAULT 'all',             -- all | errors_only (edge-approximated)
  enabled        BOOLEAN NOT NULL DEFAULT true,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX waf_rate_limits_zone ON waf_rate_limits (zone_id);

-- security_events: the firewall log (what was blocked/logged, which rule, from
-- where). Timescale hypertable like stats; zone_id/server_id are plain BIGINT (no
-- FK) so chunk drops + zone deletes never conflict (same pattern as stats).
CREATE TABLE security_events (
  ts         TIMESTAMPTZ NOT NULL,
  zone_id    BIGINT,
  server_id  BIGINT,
  edge_id    TEXT,
  client_ip  INET,
  country    TEXT,
  rule_id    TEXT,                                        -- CRS rule id, custom:<id>, or ratelimit
  rule_type  TEXT,                                        -- managed | custom | ratelimit
  action     TEXT,                                        -- block | detect | log | challenge
  mode       TEXT,                                        -- zone mode at the time (detect|block)
  path       TEXT,
  method     TEXT,
  ua         TEXT,
  message    TEXT
);
SELECT create_hypertable('security_events', 'ts', chunk_time_interval => INTERVAL '1 day');
CREATE INDEX security_events_zone_ts ON security_events (zone_id, ts DESC);
CREATE INDEX security_events_action  ON security_events (action, ts DESC);

-- +goose Down
DROP TABLE IF EXISTS security_events;
DROP TABLE IF EXISTS waf_rate_limits;
DROP TABLE IF EXISTS waf_custom_rules;
ALTER TABLE zones
  DROP COLUMN IF EXISTS waf_enabled,
  DROP COLUMN IF EXISTS waf_mode,
  DROP COLUMN IF EXISTS waf_managed_ruleset,
  DROP COLUMN IF EXISTS waf_paranoia,
  DROP COLUMN IF EXISTS waf_wp_preset,
  DROP COLUMN IF EXISTS waf_fail_open;
