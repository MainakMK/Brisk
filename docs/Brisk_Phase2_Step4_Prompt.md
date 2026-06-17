# Brisk CDN — Phase 2 / Step 4 Build Prompt (Stats Shipping → TimescaleDB)

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + the Phase‑2 Step‑1/2/3 prompts. **Phase 2 Steps 1–3 are complete and verified:** `brisk-control` (Go + chi + pgx + TimescaleDB in Docker) with token auth + SSH add‑server provisioning; the agent pulls config from the control plane (ETag/304, jitter+backoff, local last‑known‑good) and heartbeats with bearer auth; the live edge `brisk.mainakghosh.com` is managed from the control plane. The `stats` TimescaleDB hypertable exists but is **empty** — `stats.Reporter` is still a no‑op stub.

> **Read `CLAUDE.md` and the Phase‑1 + Phase‑2 prompts first.** This is **Step 4 of 7 in Phase 2**. Build only what's in scope, commit in pieces, pass the acceptance tests, and stop before Step 5.

## Step 4 goal (one line)
Make the agent **collect real metrics** (cache HIT ratio, requests, bandwidth, CPU/RAM/disk) every few seconds and **ship them to the control plane**, which stores them in the **`stats` TimescaleDB hypertable** with **continuous aggregates + retention + compression**, and exposes **query endpoints** the dashboard (Step 6) will use. `stats.Reporter` goes live.

## ✅ Test everything LOCALLY in Docker
Docker Desktop is installed. The **entire loop is testable on the laptop**: `brisk-control` + TimescaleDB via docker‑compose, plus a **local agent + Nginx container** serving a test origin. Generate traffic with `curl`/`hey`, watch stats flow into TimescaleDB, and query them back through the API. The VPS agent can also ship stats, but the **acceptance tests target local Docker**. No VPS or paid resources needed for this step.

---

## Part 1 — Agent: metric collection

Collect every **`stats_interval`** (default **10s**) and assemble a `Stats` sample. Three sources:

### 1a. Nginx `stub_status` (connection/request counters)
The agent's Nginx config must expose `stub_status` **on localhost only**:
```nginx
server {
    listen 127.0.0.1:8081;
    location /brisk_status { stub_status; allow 127.0.0.1; deny all; }
}
```
Scrape `http://127.0.0.1:8081/brisk_status` → parse `Active connections`, `accepts`, `handled`, `requests`, `Reading/Writing/Waiting`. Track deltas between samples to derive **requests/sec** and active connections.

### 1b. Access‑log parsing (cache hit ratio, bytes, per‑zone)
The `brisk.access.log` already includes `$upstream_cache_status` and bytes. **Tail it incrementally** (remember the byte offset between samples; handle **log rotation** by detecting truncation/inode change). Over each interval aggregate, per **zone (host)**: `requests`, `hits` (status HIT), `misses`, `bytes_sent`, status‑code counts. Compute **bandwidth_bps** from bytes over the interval.
> Don't recompute from the whole file each time — read only what's new since the last offset (cheap, scales to busy edges).

### 1c. System metrics (CPU/RAM/disk)
Use **`github.com/shirou/gopsutil/v4`**: `cpu.Percent()`, `mem.VirtualMemory()` (RAM %), `disk.Usage("/var/cache/brisk")` (cache‑disk %). Lightweight, cross‑platform.

### Assemble + ship
Build per‑interval `Stats` records (one per server‑level summary, plus one per active zone). Implement the real `stats.Reporter`:
- `Collect() ([]Stats, error)` — gather the above.
- `Ship([]Stats) error` — `POST /api/v1/agent/stats` with `Authorization: Bearer <token>`, body = JSON array (batched).
- **Resilience:** keep a **bounded in‑memory buffer** (e.g. last ~5 min). If the control plane is unreachable, buffer and retry with backoff; if the buffer fills, **drop oldest** — never block request serving and never grow unbounded. On reconnect, flush the buffer.
- Runs **alongside** the heartbeat + config‑pull loops (separate goroutine/ticker).

---

## Part 2 — Control plane: ingest + storage

### Ingest endpoint
`POST /api/v1/agent/stats` (behind token auth → `server_id` from context). Accepts a JSON array of samples; **bulk‑insert** into the `stats` hypertable using pgx **`CopyFrom`** (or a batched insert) for efficiency. Validate timestamps/values; clamp/ignore garbage.

### TimescaleDB policies (`migrations/00003_stats_policies.sql`)
Add real‑time analytics features on the `stats` hypertable (current TimescaleDB 2.24 syntax):
```sql
-- +goose Up
-- 1-minute continuous aggregate (rollup) — powers dashboard graphs
CREATE MATERIALIZED VIEW stats_1m
WITH (timescaledb.continuous) AS
SELECT time_bucket('1 minute', time) AS bucket,
       server_id, zone_id,
       sum(requests)        AS requests,
       sum(hits)            AS hits,
       sum(misses)          AS misses,
       sum(bytes_sent)      AS bytes_sent,
       avg(bandwidth_bps)   AS bandwidth_bps,
       avg(cpu_pct)         AS cpu_pct,
       avg(ram_pct)         AS ram_pct,
       avg(disk_pct)        AS disk_pct
FROM stats
GROUP BY bucket, server_id, zone_id
WITH NO DATA;

SELECT add_continuous_aggregate_policy('stats_1m',
  start_offset      => INTERVAL '1 hour',
  end_offset        => INTERVAL '1 minute',
  schedule_interval => INTERVAL '1 minute');

-- Retention: keep RAW stats 30 days (do NOT set this shorter than the CAGG window,
-- or the aggregate loses data). CAGGs persist longer.
SELECT add_retention_policy('stats', drop_after => INTERVAL '30 days');

-- Columnstore compression (Hypercore) on raw stats older than 1 day
ALTER TABLE stats SET (timescaledb.enable_columnstore, timescaledb.segmentby = 'server_id');
CALL add_columnstore_policy('stats', after => INTERVAL '1 day', schedule_interval => INTERVAL '1 hour');
-- +goose Down
-- (drop policies + materialized view; goose down)
```
> **Why continuous aggregates:** they pre‑compute the rollups and refresh automatically as new data arrives, so dashboard graphs query a small materialized view instead of scanning raw rows — sub‑second reads. **Hit ratio is computed at read time** as `hits / NULLIF(hits+misses,0)` (don't average stored ratios — sum hits/misses, then divide).
> **Retention caution:** a retention window shorter than the continuous‑aggregate refresh range would delete the source rows before they're aggregated, emptying the CAGG. Keep raw at 30 days.

### Query endpoints (the dashboard reuses these in Step 6)
```
GET /api/v1/overview
    -> network totals: online servers, total req/s, total bandwidth, global hit ratio (latest minute)
GET /api/v1/servers/{id}/live
    -> latest sample for one PoP: cpu/ram/disk %, req/s, bandwidth, hit ratio
GET /api/v1/stats?server_id=&zone_id=&from=&to=&resolution=raw|1m
    -> time-series array for graphs (reads stats_1m for 1m, raw table for raw)
```
Reads use `stats_1m` for ranges (fast) and the raw table for the most‑recent live values. Scope all queries by `account_id` where relevant (future customer portal).

---

## Acceptance tests (Step 4 definition of done — all LOCAL in Docker)
```bash
# 1) Bring up control plane + TimescaleDB, and a local agent+nginx edge (test origin)
docker compose up --build -d        # brisk-control + timescaledb
#   (run the local agent container serving a test origin, configured to ship to the local control plane)

# 2) Generate traffic with a mix of HIT/MISS
hey -n 2000 -c 50 http://localhost:8080-or-edge/style.css   # warm then repeat for HITs
seq 5 | xargs -I{} curl -s http://edge/video/seg{}.ts -o /dev/null

# 3) Within ~10-20s, live stats appear for the PoP
curl -s localhost:8080/api/v1/servers/1/live   # cpu/ram/disk %, req/s, bandwidth, hit_ratio (non-zero)

# 4) Raw rows landed in the hypertable
docker compose exec timescaledb psql -U brisk -d brisk -c "SELECT count(*) FROM stats;"   # > 0

# 5) Continuous aggregate populates (after a refresh / manual refresh in test)
docker compose exec timescaledb psql -U brisk -d brisk -c "CALL refresh_continuous_aggregate('stats_1m', NULL, NULL); SELECT count(*) FROM stats_1m;"   # > 0

# 6) Time-series query works
curl -s "localhost:8080/api/v1/stats?server_id=1&resolution=1m&from=$(date -u -d '1 hour ago' +%FT%TZ)&to=$(date -u +%FT%TZ)"   # array of buckets

# 7) Network overview aggregates across servers
curl -s localhost:8080/api/v1/overview   # online count, total req/s, bandwidth, global hit ratio

# 8) Policies exist
docker compose exec timescaledb psql -U brisk -d brisk -c "SELECT proc_name FROM timescaledb_information.jobs;"   # retention + columnstore + cagg refresh jobs

# 9) Resilience: control plane down -> agent buffers, keeps serving; on restart -> buffered stats flush
#   stop brisk-control; generate traffic (edge still serves); restart; verify a backfill of buffered samples lands
```
**Done when:** the agent collects real cache‑hit/bandwidth/CPU/RAM/disk metrics, ships them authenticated to the control plane, they land in the `stats` hypertable, the `stats_1m` continuous aggregate + retention + compression policies are active, the query endpoints return live + historical data, and the agent **buffers and flushes** when the control plane was briefly down — all verified **locally in Docker**.

---

## Pitfalls (do not skip)
1. **Incremental log reads** — track the byte offset; handle rotation (inode/size shrink). Never re‑parse the whole log each interval.
2. **stub_status bound to 127.0.0.1 only** — never expose it publicly.
3. **Compute hit ratio at read time** (`hits/NULLIF(hits+misses,0)`), don't average ratios.
4. **Retention ≥ CAGG window** — a too‑short retention empties the continuous aggregate. Keep raw 30 days.
5. **Bounded buffer on the agent** — drop oldest when full; never block serving or grow unbounded if the control plane is down.
6. **Bulk insert** (pgx `CopyFrom`/batch) — per‑row inserts won't keep up at fleet scale.
7. **Don't disturb Steps 1–3** — heartbeat, config‑pull, and stats run as independent loops; a stats failure must not affect config or serving.
8. **gopsutil disk usage of the cache mount** (`/var/cache/brisk`), not `/`.
9. **All control‑plane traffic over HTTPS in production**; tokens never logged.

## Forward hooks (ready, not built)
- **Dashboard (Step 6):** `/overview`, `/servers/{id}/live`, and `/stats` are exactly the endpoints the dashboard's Overview + per‑PoP tiles + graphs will call. Keep their JSON shapes clean/stable.
- **Purge (Step 5):** independent of stats — next step adds the real‑time NATS purge channel.
- **Customer portal (future):** keep stats queries `account_id`‑scopable.

## Next — Step 5 (do NOT start)
**Instant purge** over a real‑time channel (NATS): `purge.Purger` goes live so a purge from the API/dashboard reaches the edge in milliseconds (vs the config poll interval), with open‑source Nginx cache‑file deletion (no Plus). Wait for the user's go‑ahead and a Step 5 prompt.
