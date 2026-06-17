-- LOCAL TEST DATA ONLY — synthetic 24h stats so the Analytics charts have data
-- (no live edge is shipping to this local control plane). Server 3 = BLR1-01.
-- zone_id NULL = server totals; zone 1/2 = per-zone splits. system metrics
-- (cpu/ram/disk) live only on the server-total rows, matching the real agent.
-- Removed again after testing (see cleanup step).
WITH series AS (
  SELECT t,
    GREATEST(200, (3200 + 2200*sin(extract(epoch from t)/3600.0)
                        + 1000*sin(extract(epoch from t)/600.0)
                        + (random()-0.5)*900))::bigint AS req,
    LEAST(0.995, GREATEST(0.70, 0.93 + 0.045*sin(extract(epoch from t)/2400.0)
                                      + (random()-0.5)*0.03)) AS hr,
    30 + 25*sin(extract(epoch from t)/3600.0) + random()*8 AS cpu,
    45 + 12*sin(extract(epoch from t)/5400.0) + random()*5 AS ram,
    58 + random()*4 AS disk
  FROM generate_series(now() - interval '24 hours', now(), interval '2 minutes') AS t
)
INSERT INTO stats (time, server_id, zone_id, requests, hits, misses, bytes_sent,
                   bandwidth_bps, cpu_pct, ram_pct, disk_pct, hit_ratio)
SELECT
  t, 3, z.zone_id,
  (req*z.frac)::bigint                                          AS requests,
  round(req*z.frac*hr)::bigint                                 AS hits,
  (req*z.frac)::bigint - round(req*z.frac*hr)::bigint          AS misses,
  (req*z.frac*52000)::bigint                                    AS bytes_sent,
  ((req*z.frac*52000)*8/120)::bigint                            AS bandwidth_bps,
  CASE WHEN z.zone_id IS NULL THEN cpu END                      AS cpu_pct,
  CASE WHEN z.zone_id IS NULL THEN ram END                      AS ram_pct,
  CASE WHEN z.zone_id IS NULL THEN disk END                     AS disk_pct,
  hr                                                            AS hit_ratio
FROM series
CROSS JOIN (VALUES (NULL::bigint, 1.0::float),
                   (1::bigint, 0.4::float),
                   (2::bigint, 0.6::float)) AS z(zone_id, frac);

CALL refresh_continuous_aggregate('stats_1m', now() - interval '25 hours', now());
