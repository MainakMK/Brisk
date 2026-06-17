# Brisk CDN — Phase 3 / Step 4 Build Prompt (Fast Health Checks + ~30s Failover)

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + all Phase‑2 prompts + `docs/Brisk_Phase3_Step1/2/3_Prompt.md` + `dashboard-reference/`. **Phases 1 & 2 + Phase 3 Steps 1–3 are complete:** `brisk-control` has the Bunny DNS client (`internal/dns`), the deletion‑protection lock, the reconciler that makes DNS follow the `servers` table (online = enabled, off/drained = disabled‑but‑kept, deleted = auto‑removed), short‑TTL records (60s, clamp 30–120), and **geo/latency Smart‑Record routing** (`SmartRoutingType` 2 = geographic + lat/long, 1 = latency + LatencyZone; full‑state writes since Bunny's update is a partial merge). Verified against test zone `a2zjav.com`; live domain untouched.

> **Read `CLAUDE.md` and the Phase‑3 Step‑1/2/3 prompts first.** This is **Step 4 of 6 in Phase 3** — the **fast failover loop** the user specifically asked for (target **~30s**). Build the self‑driven health checker + low‑TTL tuning + per‑PoP config. **No drain/maintenance UI** (that's Step 5) beyond exposing health state. Pass the acceptance tests, stop before Step 5.

## Step 3.4 goal (one line)
Add a **self‑driven health‑check loop in `brisk-control`** that probes every edge frequently and, on failure, **immediately disables that edge's DNS record** (instead of waiting on Bunny's ~30s monitor) — combined with a **low TTL** — to target **~30s end‑to‑end failover**, with **per‑PoP‑configurable** intervals/thresholds (network‑wide defaults + per‑server overrides) and **flap protection**.

## ✅ Test locally + test zone
`brisk-control` runs locally; health checks probe edges; DNS changes apply to test zone `a2zjav.com`. Simulate a PoP failure (stop an edge / block its port) and measure how fast its record goes out of rotation. Live domain untouched.

---

## The failover math (build to this)
End‑to‑end failover ≈ **detection + TTL**, where **detection = check_interval × failure_threshold**. This is the standard formula (AWS: `Failover = TTL + Interval×Threshold`). To hit ~30s:
- **check_interval ≈ 10s**, **failure_threshold = 2** → detection ≈ 20s.
- **TTL ≈ 15s** (low end of the 30–120 clamp from Step 2 — *lower the clamp floor to 10s for this step's records*, or set the routing record TTL to 15s).
- **~20s detection + ~15s TTL ≈ ~35s typical**, often faster for users who re‑resolve after detection. **Document honestly that ~30s is a typical target, not a hard guarantee** — some resolvers ignore low TTLs and cache longer, so a subset of users may take longer. A *guaranteed* sub‑second ceiling needs **Anycast** (own IP space + BGP) — future, not now.

> Critical advantage over Bunny's built‑in monitor: once *our* checker marks an edge down, we flip its record `Disabled=true` **immediately** — removing it from rotation takes effect in one place instantly (then only the TTL caches need to expire). We don't wait the ~30s for Bunny's monitor to notice.

## Part 1 — The health checker (`internal/health/`)
A health‑check service running in `brisk-control` as its own goroutine(s):
- **Probe each online/drained‑candidate edge** on a configurable interval. **Probe type:** HTTPS GET to a lightweight edge endpoint (prefer a dedicated `/healthz` on the edge if cheap to add to the agent/nginx; else a `HEAD /` to the edge over its real hostname/IP). Keep probes **shallow, fast, side‑effect‑free** (the well‑known pitfall is heavy probe logic turning a small incident into a big one).
- **Timeout < interval** (e.g. 3s timeout, 10s interval) so probes never pile up.
- **Asymmetric thresholds (flap protection — the core pattern):**
  - **Fail fast:** mark **unhealthy after `fail_threshold` consecutive failures** (default **2**) → detection ≈ interval×2.
  - **Recover carefully:** mark **healthy again only after `rise_threshold` consecutive successes** (default **3**) → avoids a flapping edge bouncing in and out of rotation.
- Track per‑edge consecutive‑fail / consecutive‑success counters + last‑probe time/latency/result. Hold state in memory + persist last‑known health to the DB so a `brisk-control` restart doesn't blackhole or thrash.
- **On unhealthy → disable the edge's DNS record immediately** (`Disabled=true`) via the reconciler/DNS client — *do not delete* (off≠delete from Step 2; the edge may recover). **On recovered → re‑enable** (`Disabled=false`).
- This **health signal is distinct from the heartbeat** (Step 2's `last_seen`): heartbeat = "agent talked to control plane"; health probe = "edge actually serves traffic from the outside." Use the health probe as the authoritative routing signal, but reconcile sensibly with heartbeat (a box with a fresh heartbeat but failing external probes should still be pulled — it's not serving users).
- **Don't hammer / thundering‑herd:** stagger probes across edges; back off on the Bunny API (only write on a real state *change*, not every probe); respect rate limits. (LinkedIn‑style lesson: overly aggressive 1s checks cause thundering‑herd on shared dependencies — keep it ~10s.)

## Part 2 — Per‑PoP configuration (network‑wide defaults + overrides)
The user asked for this to be **configurable across all PoPs**:
- **Network‑wide config** (env / settings): `BRISK_HEALTH_INTERVAL` (default 10s), `BRISK_HEALTH_TIMEOUT` (3s), `BRISK_HEALTH_FAIL_THRESHOLD` (2), `BRISK_HEALTH_RISE_THRESHOLD` (3), `BRISK_HEALTH_PATH` (`/healthz` or `/`), and the routing‑record **TTL** (set ~15s for fast failover; allow 10–120).
- **Per‑server overrides** (columns on `servers` + API, like Step‑3's `routing_weight`/`routing_override`): a PoP can have its own interval/threshold/enabled‑health‑check flag (e.g. a flaky region gets a higher threshold; a critical region gets tighter checks). Default = inherit network‑wide.
- Migration to add the per‑server health columns; `GET /dns/routing` (or a new `GET /health/config`) shows effective per‑PoP health settings.

## Part 3 — Endpoints + observability
```
GET  /api/v1/health/status        # per-edge: healthy|unhealthy, consecutive fails/successes, last probe ts/latency, in-rotation?
GET  /api/v1/health/config        # effective per-PoP health config (network defaults + overrides)
POST /api/v1/servers/{id}/health  # set per-server health overrides (interval/threshold/enabled) -> takes effect next cycle
```
- Record health transitions in the existing **`dns_audit`**/health log (edge X unhealthy after 2 fails → record disabled; recovered after 3 → re‑enabled), so Step 5's UI + humans can see the timeline.
- Expose enough for the Servers/Overview UI (Step 5) to show a health/rotation badge.

## Part 4 — Document the honest caveats (in code comments + README)
- **~30s is a typical target, not a guarantee** — resolver TTL caching varies; some ISPs cache longer than the set TTL.
- **In‑flight viewers** (mid‑video when a PoP dies) are recovered by the **HLS player's segment retry + re‑resolution**, not by DNS — recommend short TTL + a retry‑capable player (hls.js/native). DNS failover protects *new* requests and eventually moves everyone.
- **Anycast** (own IP space + BGP) is the only path to guaranteed instant failover — a future Phase‑4+ consideration, explicitly out of scope here.
- **All‑edges‑down safety:** if every record in the set is unhealthy, Bunny returns all (no blackhole) — our checker should likewise **not disable the last healthy record**; if all probes fail, prefer leaving records enabled (a probe‑side/network problem shouldn't black‑hole the whole CDN). Guard against "checker itself is partitioned" → don't mass‑disable.

---

## Acceptance tests (Step 3.4 definition of done — test zone + simulated failure)
```bash
docker compose up --build -d        # brisk-control with health checker on, TTL ~15s
# 1) Two healthy edges -> both probed every ~10s, both in rotation (enabled in the cdn set)
curl -s localhost:8080/api/v1/health/status     # both healthy, in_rotation=true, recent probe ts
# 2) Kill one edge (stop it / block its port) -> after 2 consecutive fails (~20s) it's marked unhealthy
#    -> its DNS record is disabled IMMEDIATELY (not waiting on Bunny's 30s monitor)
#    measure: time from kill -> record Disabled=true  (should be ~20s detection)
curl -s localhost:8080/api/v1/health/status     # that edge unhealthy, in_rotation=false
dig +short cdn.a2zjav.com                        # dead edge's IP no longer returned (after TTL ~15s)
# 3) End-to-end failover time: kill -> not-returned-by-dig ≈ detection(~20s) + TTL(~15s) ≈ ~30-35s. Record the measured number.
# 4) Recovery: bring the edge back -> needs 3 consecutive successes (~30s) before re-enabled (no flap)
# 5) Flap protection: an edge that fails 1 check then passes -> NOT removed (threshold 2); rapid up/down doesn't thrash DNS
# 6) Per-PoP config: set one server's fail_threshold=3 via POST /servers/{id}/health -> that edge needs 3 fails; others still 2
# 7) off!=delete: an unhealthy edge's record is DISABLED, never deleted; recovers -> re-enabled (same record id)
# 8) All-down safety: kill BOTH edges -> checker does NOT leave the zone black-holed (records remain / Bunny returns all); no mass-delete
# 9) No thundering herd / rate-limit abuse: Bunny writes happen only on state CHANGES, not every probe; probes staggered
# 10) Restart resilience: restart brisk-control -> health state restored from DB; no spurious disable/enable churn
```
**Done when:** a killed edge is detected in ~20s and **immediately pulled from DNS rotation** (disabled, not deleted), giving **~30–35s end‑to‑end failover** (measured), with **asymmetric flap protection** (fast fail, careful recover), **per‑PoP‑configurable** intervals/thresholds, all‑down blackhole protection, write‑on‑change‑only (no rate‑limit abuse), restart resilience, and the honest caveats (resolver caching, HLS‑retry for in‑flight viewers, Anycast = future) documented — verified on the test zone with a simulated failure.

---

## Pitfalls (do not skip)
1. **Detection = interval × fail_threshold** — tune to ~20s (10s × 2). Don't set 1s intervals (thundering herd); don't set high thresholds (slow failover).
2. **Asymmetric thresholds** — fail fast (2), recover careful (3+). Symmetric thresholds either flap or fail slow.
3. **Disable, never delete** — unhealthy = `Disabled=true` (off≠delete); deleting would lose the record + fight the lock. Recover = re‑enable same record.
4. **Write to Bunny only on state change** — not every probe; stagger probes; respect rate limits (the Bunny API is rate‑limited).
5. **All‑down blackhole guard** — never disable the last healthy record / mass‑disable on a checker‑side network blip; if everything fails, prefer leaving rotation intact (matches Bunny's own all‑offline behavior).
6. **Probe is shallow + side‑effect‑free + timeout < interval** — heavy probes turn small incidents into big ones; piled‑up probes corrupt timing.
7. **Health signal ≠ heartbeat** — external probe is the routing truth; reconcile with `last_seen` but don't rely on heartbeat alone (a box can heartbeat yet fail to serve users).
8. **~30s is typical, not guaranteed** — document resolver‑caching reality + HLS‑retry for in‑flight viewers + Anycast as the future "instant" path. Don't oversell.
9. **Restart resilience** — persist health state; a `brisk-control` restart must not blackhole or thrash the zone.
10. **Test zone only**; scope = health loop + failover. Drain/maintenance UI = Step 5; multi‑PoP deploy = Step 6.

## Next — Step 3.5 (do NOT start) — maintenance/drain mode UI + status surfacing
Dashboard controls: a per‑server (and per‑region) **drain/maintenance toggle** (flip → record disabled → traffic reroutes to nearby PoPs over the TTL → flip back → returns), plus **DNS/health/rotation status surfaced in the Servers + Overview screens** (which PoPs are in‑pool, draining, unhealthy, or failed), and the per‑PoP health/routing config exposed in the UI. Wait for the user's go‑ahead and a Step 3.5 prompt.
