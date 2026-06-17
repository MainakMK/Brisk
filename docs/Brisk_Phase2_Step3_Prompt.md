# Brisk CDN — Phase 2 / Step 3 Build Prompt (Agent Pull-Config)

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + `docs/Brisk_Phase2_Step1_Prompt.md` + `docs/Brisk_Phase2_Step2_Prompt.md`. **Phase 2 Steps 1 & 2 are complete and verified on the real VPS:** `brisk-control` (Go + chi + pgx + TimescaleDB) is up; per‑agent token auth works (SHA‑256 hashed, indexed prefix, constant‑time, rotation); the add‑server SSH provisioning flow works (TOFU host key, CP ed25519 key installed, agent SFTP'd + bootstrapped); the agent heartbeats with bearer auth and flips the server to `online`. `GET /api/v1/agent/config` exists but returns **501** behind token auth. The live edge `brisk.mainakghosh.com` is still serving from its **local `agent.yaml`** (Phase‑1 standalone mode).

> **Read `CLAUDE.md` and the Phase‑1 + Phase‑2 Step‑1/Step‑2 prompts first.** This is **Step 3 of 7 in Phase 2**. Build only what's in scope, commit in pieces, pass the acceptance tests, and stop before Step 4.

## Step 3 goal (one line)
Make the agent **pull its config from the control plane** instead of reading a local YAML: implement `GET /api/v1/agent/config` (return the zones assigned to the calling server + a version/ETag), and flip the agent's `config.Source` to a **control‑plane source** that polls efficiently, **persists a local last‑known‑good copy**, regenerates Nginx on change, and **keeps serving if the control plane is down** (dashboard down ≠ edge down).

---

## ⚠️ Safety first — do NOT drop the live site
`brisk.mainakghosh.com` is live and served from the agent's local `agent.yaml`. Switching the agent to pull‑mode must **not** take it down. Required order:
1. **First**, create that zone in the control plane (same `cdn_hostname`/`custom_domain` = `brisk.mainakghosh.com`, `origin_url`, `tls_mode: letsencrypt`, video/profile/ttls matching the current local config) and **assign it to the server**.
2. **Then** flip the agent to pull‑mode. On startup the agent must **load its local last‑known‑good first** and only replace it after a successful pull that includes the zone — so there's no window where the site disappears.
3. Test on a throwaway zone first if unsure. Keep the Phase‑1 local `agent.yaml` as the fallback.

---

## Part 1 — Control plane: zone↔server assignment

The `server_zones` table already exists. Add endpoints so a server has zones to pull:
```
GET    /api/v1/servers/{id}/zones            # list zones assigned to this server
POST   /api/v1/servers/{id}/zones            # assign a zone {zone_id}
DELETE /api/v1/servers/{id}/zones/{zoneId}   # unassign
```
(Phase 2 is single‑edge, but model it properly — Phase 3 multi‑PoP relies on this mapping.)

## Part 2 — Control plane: implement `GET /api/v1/agent/config`

Replace the 501 stub. It runs **behind the token‑auth middleware** (Step 2), so the calling **server's ID is already in the request context**. Logic:
1. Load the zones **assigned to this server** (via `server_zones`), each with the full set of fields the agent needs to render Nginx: `cdn_hostname`, `custom_domain`, `origin_url`, `tls_mode`, `video`, `profile`, `playlist_ttl`, `segment_ttl`, `cors_origin`, `brotli_level`, `status`, and each zone's **`cache_rules`**.
2. Assemble a stable JSON document (deterministic field/zone ordering).
3. Compute a **config ETag** = a hash over the assigned zones' `(zone_id, config_version)` tuples **plus** the assignment set. This changes when *either* a zone is edited (its `config_version` bumps) *or* assignments change — so the agent always notices. Set the `ETag` response header.
4. **Honor conditional requests:** if the agent sends `If-None-Match: "<etag>"` and it matches the current ETag → return **`304 Not Modified`** with an empty body. Otherwise return **`200`** with the full config JSON + the new `ETag`.

> **Why conditional GET / 304:** the agent polls frequently; most polls find nothing changed. With `If-None-Match`/`ETag`, an unchanged poll returns a tiny 304 with **no body** — far less bandwidth and work — and the agent short‑circuits (nothing to regenerate). This is the standard efficient‑polling pattern and scales cleanly to many PoPs later.

Response shape (200):
```json
{
  "config_version": "<etag>",
  "zones": [
    {
      "cdn_hostname": "brisk.mainakghosh.com",
      "custom_domain": "brisk.mainakghosh.com",
      "origin_url": "http://127.0.0.1:8000",
      "tls_mode": "letsencrypt",
      "video": true, "profile": "vod",
      "playlist_ttl": "2s", "segment_ttl": "12h",
      "cors_origin": "*", "brotli_level": 5,
      "cache_rules": [ { "priority":0,"match_type":"extension","match_value":"m3u8","action":"override_cache_ttl","action_value":"2s" } ]
    }
  ]
}
```

## Part 3 — Agent: the control‑plane config source

Implement the real `config.Source` (call it `ControlPlaneSource`) that replaces `FileSource` when `control_plane_url` + `agent_token` are set. (`FileSource`/standalone stays the fallback when they're empty — **preserve Phase‑1 behavior**.)

**Poll loop:**
- Every **`poll_interval`** (default 15s) **plus jitter** (a small random offset, e.g. ±20%), GET `{control_plane_url}/api/v1/agent/config` with `Authorization: Bearer <token>` and `If-None-Match: "<last_etag>"`.
- **304** → nothing changed; do nothing.
- **200** → new config: (a) **atomically write** it to a local last‑known‑good file (e.g. `/etc/brisk/config.cache.json`, write‑temp‑then‑rename, perms `600`); (b) store the new ETag; (c) hand the zones to the existing Nginx renderer → `nginx -t` → graceful `nginx -s reload`; keep the previous good config for **rollback** if `nginx -t` fails.
- **Error / control plane unreachable** → **keep serving** from the current/last‑known‑good config; log a warning; retry with **exponential backoff + jitter** (cap the backoff, e.g. 2s→4s→…→max 2m).

**Startup order (critical for resilience):**
1. Load the **local last‑known‑good** config from disk first and render Nginx from it immediately — the edge serves even if the control plane is unreachable at boot.
2. Then start the poll loop to converge to the latest.

**Why jitter:** if many agents poll on the same fixed interval they all hit the control plane at the same instant, causing load spikes; a small random offset spreads requests evenly across the interval. Build it in now so it scales to many PoPs.

**Agent config additions (`agent.yaml`):**
```yaml
control_plane_url: "https://control.example.com"   # empty => standalone/local mode
agent_token_file: "/etc/brisk/token"               # 600 perms
poll_interval: "15s"
config_cache: "/etc/brisk/config.cache.json"
```

## Acceptance tests (Step 3 definition of done)
```bash
# 0) Migrate the live zone into control plane + assign it (SAFETY)
curl -s -X POST localhost:8080/api/v1/zones -d '{"name":"mainak","cdn_hostname":"brisk.mainakghosh.com","origin_url":"http://127.0.0.1:8000","tls_mode":"letsencrypt","video":true,"profile":"vod"}' -H 'Content-Type: application/json'
curl -s -X POST localhost:8080/api/v1/servers/1/zones -d '{"zone_id":1}' -H 'Content-Type: application/json'

# 1) agent/config returns the assigned zone (authenticated) + an ETag
curl -si -H 'Authorization: Bearer <AGENT_TOKEN>' localhost:8080/api/v1/agent/config | grep -i 'etag\|cdn_hostname'

# 2) Conditional GET -> 304 when unchanged
ETAG=$(curl -s -i -H 'Authorization: Bearer <AGENT_TOKEN>' localhost:8080/api/v1/agent/config | awk -F'"' '/[Ee][Tt]ag/{print $2}')
curl -s -o /dev/null -w '%{http_code}\n' -H 'Authorization: Bearer <AGENT_TOKEN>' -H "If-None-Match: \"$ETAG\"" localhost:8080/api/v1/agent/config   # 304

# 3) Agent picks up a change within the poll interval
curl -s -X PUT localhost:8080/api/v1/zones/1 -d '{"segment_ttl":"24h"}' -H 'Content-Type: application/json'   # config_version + ETag change
#   within ~15s the agent re-pulls (200), regenerates nginx, reloads; verify on the edge:
curl -sI https://brisk.mainakghosh.com/video/seg1.ts   # reflects new behavior; site stays up the whole time

# 4) Live edge never dropped
curl -sI https://brisk.mainakghosh.com/ | grep -i 'server:'    # Server: Brisk, 200, throughout

# 5) Control-plane-down resilience
#   stop brisk-control -> agent logs warnings but KEEPS serving from local last-known-good
curl -sI https://brisk.mainakghosh.com/                        # still 200 while control plane is down
#   restart brisk-control -> agent resumes pulling, converges

# 6) Standalone mode preserved
#   empty control_plane_url -> agent runs purely from local agent.yaml (Phase-1 behavior)
```
**Done when:** the agent pulls its config from the control plane, an unchanged poll returns **304**, a zone edit propagates to the live edge **within the poll interval** via a graceful reload, the **live site is never dropped**, the edge **keeps serving from local last‑known‑good when the control plane is down**, and **standalone mode still works**.

---

## Pitfalls (do not skip)
1. **Don't drop the live site** — create + assign the existing zone in the control plane *before* flipping to pull‑mode; load local last‑known‑good on startup before the first pull.
2. **Atomic config writes** — write temp + rename; never leave a half‑written cache file that could brick a reload.
3. **`nginx -t` before reload, rollback on failure** — a bad pulled config must never take the edge down (keep the `.bak`, revert, keep serving the old good config, log loudly).
4. **Use ETag/304** — don't re‑download and re‑render unchanged config every poll; honor `If-None-Match`.
5. **Jitter + capped backoff** — avoid synchronized polling spikes and avoid hammering a down control plane.
6. **Preserve standalone mode** — empty `control_plane_url` ⇒ exact Phase‑1 behavior.
7. **Deterministic config + ETag** — stable ordering so the ETag is stable across identical states (no false 200s).
8. **Token from a `600` file**, never logged; all control‑plane traffic over HTTPS in production.
9. **Don't break heartbeat** (Step 2) — config pull and heartbeat run alongside each other.

## Forward hooks (ready, not built)
- **Stats (Step 4):** the agent already heartbeats; Step 4 adds periodic stats shipping to the `stats` hypertable. Same auth + client.
- **Purge (Step 5):** still the local `purge.Purger`; the instant network channel (NATS) comes in Step 5. The pull loop is for config, not purge — purge needs the real‑time channel.
- **Dashboard (Step 6):** the assignment + config endpoints built here are what the dashboard's zone/server screens will call.

## Next — Step 4 (do NOT start)
Agent **stats shipping**: collect every few seconds (cache HIT ratio, req/s, bandwidth, CPU/RAM/disk) via Nginx `stub_status` + access‑log parsing + system metrics, and ship to the control plane → stored in the `stats` TimescaleDB hypertable (`stats.Reporter` goes live). Add a TimescaleDB retention + compression policy. Wait for the user's go‑ahead and a Step 4 prompt.
