-- +goose NO TRANSACTION
-- +goose Up
-- Phase 4 Step 6 — the real edge request log (replaces the Logs placeholder).
-- High-volume structured access logs shipped by the agent. Timescale hypertable
-- with SHORT retention (7 days) + columnstore compression after 6h — logs are
-- voluminous, so we never keep them forever (Pitfall #2). zone_id/server_id are
-- plain BIGINT (no FK) like stats/security_events so chunk drops never conflict.
CREATE TABLE request_logs (
  ts            TIMESTAMPTZ NOT NULL,
  server_id     BIGINT,
  edge_id       TEXT,
  zone_id       BIGINT,
  client_ip     INET,
  country       TEXT,
  method        TEXT,
  host          TEXT,
  path          TEXT,
  status        INTEGER,
  bytes         BIGINT,
  cache_status  TEXT,                -- HIT | MISS | BYPASS | EXPIRED | STALE | -
  request_time  DOUBLE PRECISION,    -- total edge time (s)
  upstream_time DOUBLE PRECISION,    -- origin/shield time (s)
  referer       TEXT,
  user_agent    TEXT,
  request_id    TEXT                 -- X-Brisk-Request-Id (correlation)
);
SELECT create_hypertable('request_logs', 'ts', chunk_time_interval => INTERVAL '6 hours');
CREATE INDEX request_logs_zone_ts ON request_logs (zone_id, ts DESC);
CREATE INDEX request_logs_status_ts ON request_logs (status, ts DESC);

SELECT add_retention_policy('request_logs', drop_after => INTERVAL '7 days');
ALTER TABLE request_logs SET (timescaledb.enable_columnstore = true, timescaledb.segmentby = 'zone_id');
CALL add_columnstore_policy('request_logs', after => INTERVAL '6 hours', schedule_interval => INTERVAL '1 hour');

-- +goose Down
-- remove_columnstore_policy is a PROCEDURE in TimescaleDB 2.x (CALL, not SELECT);
-- remove_retention_policy is a FUNCTION (SELECT). Matches the Up direction.
CALL remove_columnstore_policy('request_logs', if_exists => true);
SELECT remove_retention_policy('request_logs', if_exists => true);
DROP TABLE IF EXISTS request_logs;
