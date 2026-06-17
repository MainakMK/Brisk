-- +goose NO TRANSACTION
-- +goose Up
-- 1-minute continuous aggregate (rollup) — powers dashboard graphs.
-- Hit ratio is computed at READ time from sum(hits)/(hits+misses); we store the
-- summed counts here, never averaged ratios.
CREATE MATERIALIZED VIEW IF NOT EXISTS stats_1m
WITH (timescaledb.continuous) AS
SELECT time_bucket('1 minute', time) AS bucket,
       server_id, zone_id,
       sum(requests)      AS requests,
       sum(hits)          AS hits,
       sum(misses)        AS misses,
       sum(bytes_sent)    AS bytes_sent,
       avg(bandwidth_bps) AS bandwidth_bps,
       avg(cpu_pct)       AS cpu_pct,
       avg(ram_pct)       AS ram_pct,
       avg(disk_pct)      AS disk_pct
FROM stats
GROUP BY bucket, server_id, zone_id
WITH NO DATA;

-- Refresh the rollup automatically as new data arrives.
SELECT add_continuous_aggregate_policy('stats_1m',
  start_offset      => INTERVAL '1 hour',
  end_offset        => INTERVAL '1 minute',
  schedule_interval => INTERVAL '1 minute');

-- Keep raw stats 30 days (>= the CAGG refresh window, or the aggregate loses data).
SELECT add_retention_policy('stats', drop_after => INTERVAL '30 days');

-- Columnstore (Hypercore) compression on raw stats older than 1 day.
ALTER TABLE stats SET (timescaledb.enable_columnstore = true, timescaledb.segmentby = 'server_id');
CALL add_columnstore_policy('stats', after => INTERVAL '1 day', schedule_interval => INTERVAL '1 hour');

-- +goose Down
SELECT remove_columnstore_policy('stats', if_exists => true);
SELECT remove_retention_policy('stats', if_exists => true);
SELECT remove_continuous_aggregate_policy('stats_1m', if_exists => true);
DROP MATERIALIZED VIEW IF EXISTS stats_1m;
