# Brisk CDN — Phase 2 / Step 5 Build Prompt (Instant Purge via NATS)

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + the Phase‑2 Step‑1…4 prompts. **Phase 2 Steps 1–4 are complete and verified locally:** `brisk-control` (Go + chi + pgx + TimescaleDB) with token auth + SSH add‑server; the agent pulls config (ETag/304, jitter, last‑known‑good), heartbeats, and **ships stats** to TimescaleDB (continuous aggregate + retention + compression); query endpoints `/overview`, `/servers/{id}/live`, `/stats` work. `purge.Purger` is still a **local‑only stub** — no network channel yet.

> **Read `CLAUDE.md` and the Phase‑1 + Phase‑2 prompts first.** This is **Step 5 of 7 in Phase 2**. Build only what's in scope, commit in pieces, pass the acceptance tests, and stop before Step 6.

## Step 5 goal (one line)
Make **purge instant**: a purge issued from the API/dashboard reaches the edge in **milliseconds** (not the config poll interval) over a **NATS JetStream** channel, and the agent removes the matching content from the open‑source Nginx cache — **including sliced video** — via the **`ngx_cache_purge`** module. `purge.Purger` goes live.

## ✅ Test everything LOCALLY in Docker
Docker Desktop is installed. Add **NATS** to the compose stack and test the full loop on the laptop: `brisk-control` + TimescaleDB + **NATS** + a local agent/Nginx edge. Cache a file (HIT) → purge via API → confirm the next request is a **MISS** within milliseconds. No VPS or paid resources needed.

---

## Why this is non‑trivial (read first)
Open‑source Nginx has **no built‑in purge directive** (that's NGINX Plus). And our **video cache is sliced** — `proxy_cache_key` for `.ts/.m4s/.mp4` includes `$slice_range`, so **one video URL maps to many cache entries** (one per 1 MB slice). So purge must:
1. travel over a **real‑time channel** (NATS) for millisecond delivery, durable so a briefly‑disconnected agent doesn't miss it, and
2. use **wildcard purge** so a single logical URL clears **all its slices**.

---

## Part 1 — NATS JetStream (durable real‑time channel)

Add **NATS with JetStream** to `docker-compose.yml`:
```yaml
  nats:
    image: nats:2.10-alpine
    command: ["-js"]                 # enable JetStream
    ports: ["127.0.0.1:4222:4222"]
    volumes: ["brisk_nats:/data"]
```
**Use JetStream, NOT core NATS.** Core NATS is **at‑most‑once** (fire‑and‑forget — a message is lost if the subscriber is offline). JetStream adds persistence + **at‑least‑once** delivery with replay, so an agent that was briefly disconnected **receives missed purges on reconnect** (a missed purge = stale content served — unacceptable for a CDN). Use the modern `github.com/nats-io/nats.go/jetstream` API.

- **Stream:** `BRISK_PURGE`, subjects `brisk.purge.>`.
- **Subjects:** publish per zone/server, e.g. `brisk.purge.zone.<zone_id>` (and/or `brisk.purge.server.<server_id>`).
- **Consumer:** each agent creates a **durable** consumer (`Durable: "agent-<server_id>"`, `AckPolicy: AckExplicit`) **filtered** to the subjects for the zones it serves. It acks each message only after applying the purge — so unacked purges are redelivered.

## Part 2 — Nginx open‑source purge via `ngx_cache_purge`

Build the **`ngx_cache_purge`** dynamic module (maintained fork `github.com/nginx-modules/ngx_cache_purge`) — same pattern as `ngx_brotli`/`headers-more`, **ABI‑locked to the installed Nginx version** (rebuild on upgrade; `bootstrap.go` owns this via the version‑stamp). It brings NGINX‑Plus‑style purge to open‑source Nginx: **same‑location purging, wildcard purge with `*`, bulk `purge_all`, IP‑restricted access, and cache‑key method substitution**.

**Config changes the agent emits:**
1. Enable the cache‑purger process on the cache so wildcard purges actually delete from disk (without it, wildcard‑matched entries linger until inactive/accessed):
   ```nginx
   proxy_cache_path /var/cache/brisk levels=1:2 keys_zone=brisk_cache:512m
       max_size=200g inactive=7d use_temp_path=off
       purger=on purger_files=10 purger_threshold=50ms purger_sleep=50ms;
   ```
2. A **localhost‑only** purge location whose purge key **mirrors the cache key**:
   ```nginx
   # restrict to the agent only
   location ~ ^/__brisk_purge(/.*)$ {
       allow 127.0.0.1; deny all;
       proxy_cache_purge brisk_cache "$host$1$is_args$args";   # mirrors $host$uri$is_args$args
   }
   ```
   The agent issues a localhost request with `Host: <zone hostname>` and the target path; a trailing `*` triggers **wildcard** purge.

**Key rule for sliced video:** because video keys end in `$slice_range`, **always purge with a wildcard prefix** `"$host$uri*"` so all slices of a URL are cleared. So the agent's purge translates:
- single URL → `<host><uri>*` (clears all slices of that file),
- path prefix → `<host><prefix>*`,
- whole zone → `<host>*`,
- entire cache → `purge_all`.

## Part 3 — Control plane: purge API + publish

Endpoints:
```
POST /api/v1/zones/{id}/purge        # body {type:"url"|"prefix"|"zone", target:"/video/movie.mp4"}
POST /api/v1/purge/all               # purge entire cache (purge_all) on selected/all servers
GET  /api/v1/purge/jobs?zone_id=     # purge history/status (for the dashboard later)
```
Flow: validate → insert a `purge_jobs` row (status `pending`) → **publish** a JSON purge message to the JetStream subject(s) for the zone's server(s) → return **202 Accepted** with the job id. (Optionally, agents report completion to flip status `done` + record which edges purged.)

Schema (`migrations/00004_purge_jobs.sql`):
```sql
-- +goose Up
CREATE TABLE purge_jobs (
  id           BIGSERIAL PRIMARY KEY,
  account_id   BIGINT NOT NULL DEFAULT 1,
  zone_id      BIGINT REFERENCES zones(id) ON DELETE CASCADE,
  type         TEXT NOT NULL,          -- url | prefix | zone | all
  target       TEXT NOT NULL,
  status       TEXT NOT NULL DEFAULT 'pending',  -- pending | done | partial | failed
  edges_total  INTEGER DEFAULT 0,
  edges_done   INTEGER DEFAULT 0,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ
);
-- +goose Down
DROP TABLE IF EXISTS purge_jobs;
```

## Part 4 — Agent: `purge.Purger` goes live

- On startup, create the **durable JetStream consumer** filtered to this server's zone subjects; consume in its **own goroutine** (must not block serving, heartbeat, config‑pull, or stats).
- On a purge message: translate to the wildcard purge key, issue the **localhost `PURGE`** request to `/__brisk_purge/...` (with `Host: <zone host>`), confirm success, then **ack** the JetStream message. Purging absent content is fine (idempotent — treat 404/204 as success).
- **Durability:** if the agent was disconnected, JetStream **redelivers** the unacked purges on reconnect — apply them then ack. This guarantees no missed purge.
- Optionally POST completion back to the control plane to update `purge_jobs`.
- Keep the existing local `purge.Purger` path working for standalone mode (no NATS) — purge then becomes a direct localhost call without the queue.

---

## Acceptance tests (Step 5 definition of done — all LOCAL in Docker)
```bash
docker compose up --build -d        # brisk-control + timescaledb + NATS + (local agent/edge)

# 1) Warm the cache (MISS -> HIT)
curl -ksI https://edge/style.css | grep -i x-brisk-cache    # MISS
curl -ksI https://edge/style.css | grep -i x-brisk-cache    # HIT

# 2) Instant purge by URL -> next request is MISS within milliseconds
time curl -s -X POST localhost:8080/api/v1/zones/1/purge -H 'Content-Type: application/json' -d '{"type":"url","target":"/style.css"}'   # 202
curl -ksI https://edge/style.css | grep -i x-brisk-cache    # MISS (re-fetched) — verify the gap was ms, not the poll interval

# 3) Sliced video wildcard purge clears ALL slices
curl -ksI -H 'Range: bytes=0-1048575'      https://edge/video/movie.mp4 | head -1   # 206 (cache it)
curl -ksI -H 'Range: bytes=1048576-2097151' https://edge/video/movie.mp4 | head -1  # 206 (another slice)
curl -s -X POST localhost:8080/api/v1/zones/1/purge -d '{"type":"url","target":"/video/movie.mp4"}' -H 'Content-Type: application/json'
curl -ksI -H 'Range: bytes=0-1048575'      https://edge/video/movie.mp4 | grep -i x-brisk-cache  # MISS (all slices gone)

# 4) Prefix + zone purge
curl -s -X POST localhost:8080/api/v1/zones/1/purge -d '{"type":"prefix","target":"/assets/"}' -H 'Content-Type: application/json'
curl -s -X POST localhost:8080/api/v1/zones/1/purge -d '{"type":"zone","target":"/"}' -H 'Content-Type: application/json'

# 5) Durability: agent offline during purge -> gets it on reconnect (JetStream replay)
#   stop the agent; issue a purge; restart the agent; verify the purge is applied after reconnect (cache MISS)

# 6) Job tracking
curl -s "localhost:8080/api/v1/purge/jobs?zone_id=1"        # shows the jobs + status

# 7) Other loops unaffected
curl -s localhost:8080/api/v1/servers/1/live                # stats still flowing; heartbeat + config-pull intact
```
**Done when:** a purge from the API clears the edge cache **within milliseconds** over NATS (verified faster than the config poll), **wildcard purge clears all slices of a video**, prefix/zone/all purges work, a purge issued while the agent was **offline is applied on reconnect** (JetStream durability), purge jobs are tracked, and heartbeat/config‑pull/stats are unaffected — all **locally in Docker**.

---

## Pitfalls (do not skip)
1. **JetStream, not core NATS** — durability so missed purges replay on reconnect; core NATS would silently drop them.
2. **Sliced video ⇒ wildcard prefix purge** (`<host><uri>*`) — a single‑key purge misses most slices. Always wildcard for video.
3. **`purger=on` on `proxy_cache_path`** — required for wildcard purges to actually delete files from disk (otherwise entries linger until inactive/accessed).
4. **Purge location restricted to `127.0.0.1`** — never publicly reachable; the agent is the only caller.
5. **Purge key must mirror the cache key** (`$host$uri$is_args$args`) or nothing matches.
6. **Module ABI‑lock** — `ngx_cache_purge` is tied to the exact Nginx version; rebuild on upgrade (bootstrap handles via version‑stamp).
7. **Ack only after applying** the purge — so JetStream redelivers on failure/disconnect. Idempotent (re‑purging absent content is success).
8. **Purge runs in its own goroutine** — never block serving, heartbeat, config‑pull, or stats.
9. **Purge is a separate channel from config pull** — don't fold purge into the poll loop; that's what makes it millisecond‑fast.
10. **Standalone mode** — with no NATS configured, purge falls back to a direct localhost call; Phase‑1 behavior preserved.

## Forward hooks (ready, not built)
- **Dashboard (Step 6):** `POST .../purge`, `POST /purge/all`, and `GET /purge/jobs` are the endpoints the dashboard's purge UI will call. Keep JSON shapes stable.
- **Multi‑PoP (Phase 3):** the per‑server/zone subject design already fans a purge out to every edge serving the zone.

## Next — Step 6 (do NOT start) — the dashboard (design happens here)
React + TypeScript + **Tailwind + shadcn/ui + Tremor**, building Brisk's **own** design (inspired by Bunny/CDN77/Cloudflare *patterns*, not copied): Overview, Servers (live PoP tiles + Add Server), Zones (+ cache rules), Analytics graphs, Logs, and Purge — all wired to the Go API, built role‑aware so the customer portal slots in later. Wait for the user's go‑ahead and a Step 6 prompt (we'll capture the design/IA reference then).
