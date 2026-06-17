# Brisk CDN — Phase 4 / Step 3 Build Prompt (Origin Shield — Mid-Tier Cache, multi-tenant)

**For Claude Code.** Context: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + all Phase‑2/3/3.7 prompts + `docs/Brisk_Phase4_Step1_MultiTenant_Prompt.md` + `docs/Brisk_Phase4_Step2_Prompt.md` + `docs/Control_Plane_Ops.md` + `dashboard-reference/`. **Phase 4 Steps 1–2 are complete:** the 3 live edges are **multi‑tenant** — one nginx `server` block per zone (`server_name = cdn_hostname`, per‑zone `proxy_pass` origin + `host_header` override, `$host` cache isolation, `default_server` that 444s unknown hosts + answers `/healthz` 200 by IP), and customers can add **their own domain with automatic per‑domain TLS** (lego HTTP‑01 via the edges' `:80` challenge proxy, certs fanned over config‑pull, served via SNI, auto‑renew). Live: `cdn.a2zjav.com` + verified custom‑domain flow. Each zone now has **its own `origin_url`**.

> **Read `CLAUDE.md`, the Phase‑4 Step‑1 (multi‑tenant routing) + Phase‑1 (proxy_cache/`proxy_cache_lock`/slice) prompts first.** This is **Step 3 of Phase 4 — Origin Shield**, reconciled to the **multi‑tenant** reality (a drafted single‑tenant version existed earlier — this supersedes it). Test locally in Docker. Pass the acceptance tests, stop before Step 4 (WAF).

## Step 4.3 goal (one line)
Add a **mid‑tier shield cache**: per zone, all edge cache‑misses route through **one designated shield PoP** (instead of each edge hitting the origin), so **many edges missing the same object collapse to ~one origin fetch** — reducing origin load/egress and raising hit‑ratio, configurable per zone, with graceful fallback if the shield is down.

## ✅ Test locally in Docker
Stand up locally: `brisk-control` + TimescaleDB + NATS + **2+ edge containers** + **1 shield container** + **2 origin containers** (to prove per‑zone shield routing for two tenants). Verify multiple edges missing the same object produce **one** origin fetch per object. No VPS/cost needed.

---

## How origin shield works (build to this — researched)
Origin Shield is a **centralized caching layer in front of the origin**. When multiple edge servers receive requests for uncached content, instead of all of them hitting the origin, **edge misses forward to the shield; if the shield also lacks it, only one request proceeds to the origin**, then the content is cached at the shield and shared to the edges. This **collapses redundant requests** ("as few as one request goes to the origin per object"), raises cache‑hit ratio, and prevents thundering‑herd on the origin during spikes — providers report up to ~57% origin‑load reduction for live‑video/image/multi‑PoP workloads (exactly Brisk's HLS case). Best practice: **choose the shield location closest to the origin**.

**Two honest caveats from the research (design around them):**
- **An extra hop on true cold misses** (edge → shield → origin) adds a little latency; the win is on the *aggregate* origin load, not single cold objects.
- **Shield is poor for uncacheable/dynamic/rarely‑requested content** — for those, routing through the shield just adds a hop with no consolidation benefit. So shield is **per‑zone opt‑in** (great for static/video tenants), and within a zone it naturally only helps cacheable objects.

**Brisk mapping:** the shield is **another Brisk edge** flagged `role=shield` (role column from the earlier draft). With multi‑tenancy, **shield routing is per‑zone**: for a shielded zone, the edge's `proxy_pass` for that zone's `server` block targets the **shield** (which caches that zone under the same key) instead of the zone's origin; the **shield's** block for that zone targets the **real origin**. We already have the mechanics — `proxy_cache` + **`proxy_cache_lock`** (request coalescing) — the shield is a second tier using them.

## Part 1 — Shield topology (schema + control plane)
- Server **`role`**: `edge` (default) | `shield` (one or more PoPs can be shields; pick the one nearest each zone's origin as best practice).
- Per‑zone: `origin_shield_enabled` (bool) + `shield_server_id` (which PoP shields this zone) — migration. A network‑wide default shield, overridable per zone. **Per‑zone** is the key multi‑tenant change vs the old single‑tenant draft.
- The control plane computes each edge's **upstream for each zone** in its pulled config:
  - zone shield ON → that zone's upstream on a normal edge = the **shield PoP**.
  - zone shield OFF → upstream = the zone's **real origin** (today's behavior).
  - on the **shield PoP itself**, that zone's upstream = the zone's **real origin**.
- Reuse the **config‑pull + `config_version`** channel — no new transport.

## Part 2 — Agent/nginx: per‑zone two‑tier proxy
- The multi‑tenant template (Step 1) already emits a `server` block per zone. Extend it so each zone's `proxy_pass` target is **shield‑or‑origin** per the computed upstream, **preserving the cache key + the upstream Host header end‑to‑end** (critical: the shield must cache under the **same key** the edge uses, or consolidation breaks and you get zero offload — this is the #1 origin‑shield correctness rule).
- **`proxy_cache_lock` on at BOTH tiers** so concurrent misses coalesce: many edges → shield collapses to one origin fetch; many users on one edge → that edge collapses to one shield fetch.
- Preserve per‑zone Phase‑1/Step‑1 behavior at both tiers: slice video (shield caches slices — big win for HLS), Brotli, branded headers, `aio threads`, `$host` isolation, the per‑zone `host_header`. Add **`X-Brisk-Shield`** (HIT/MISS at the shield tier, distinct from the edge‑tier `X-Brisk-Cache`) for observability.
- **Loop/self‑reference guard:** an edge must never shield through itself; the shield must go to origin, not back to an edge; a shield zone whose `shield_server_id` == this server → use origin directly. Reject/skip self‑referential config (infinite‑loop prevention).
- **Graceful shield‑failure fallback:** if the shield PoP is unhealthy/unreachable, edges **fall back to pulling the origin directly** for that zone (degrade, don't blackhole) — tie into the Phase‑3 health system (a dead shield must not take a zone down). Nginx `proxy_next_upstream` / a backup upstream (origin as backup) is a clean way; document the behavior.

## Part 3 — Dashboard: per‑zone shield controls + visibility
- **Zone settings:** an **Origin Shield** toggle + shield‑PoP selector (default = network shield, ideally nearest the origin). Honest hint: "Edge misses pull through the shield; your origin sees ~one request per object. Best for cacheable/static/video; little benefit for dynamic content."
- **Servers:** mark shield PoP(s) with a **"Shield"** role badge; show shield‑tier hit‑ratio.
- **Analytics:** surface **origin offload** — origin requests/bandwidth with vs without shield + shield hit‑ratio (the "look how much origin load we saved" metric — the selling point). Use real stats where the schema supports; **flag any metric the stats schema can't yet provide** (Phase‑4 cleanup will add origin‑tier counters).
- Voltage design, role‑aware (a customer later toggles shield for their own zone), honest empty states.

## Part 4 — Config + safety
- Network‑wide default shield + per‑zone overrides; changing shield config bumps `config_version` → edges re‑pull.
- **Live‑site safety:** enabling/disabling shield on a zone is a config change over the poll interval — confirm the zone (incl. `cdn.a2zjav.com`) keeps serving through an enable/disable; never point a zone's edges at a shield that isn't actually serving that zone.
- Multi‑tenant integrity: shield routing for zone A must never mix with zone B's cache/origin (per‑zone keys + per‑zone upstream); a shielded zone and a non‑shielded zone coexist on the same edges.

---

## Acceptance tests (Step 4.3 definition of done — local Docker)
```bash
docker compose up --build -d        # control + db + nats + shield + 2 edges + 2 origins
# 1) Topology: one role=shield; two role=edge; zone A (origin A) shield ON; zone B (origin B) shield OFF
# 2) Collapse (the core win): request the SAME cold object on zone A from BOTH edges ->
#    origin A logs ONE fetch total (collapsed at the shield), not two; edges show X-Brisk-Cache MISS->HIT, shield X-Brisk-Shield MISS->HIT
# 3) Concurrency: hit the same cold zone-A object concurrently from both edges -> proxy_cache_lock collapses to ONE origin fetch
# 4) Video: sliced video on zone A from both edges -> shield caches slices -> origin sees one pull per slice (not per edge)
# 5) Per-zone isolation: zone B (shield OFF) still pulls origin B directly; zone A shielded; no cross-zone cache/origin mixing ($host keys distinct)
# 6) Shield failure fallback: stop the shield -> zone A edges fall back to origin A directly (zone stays served, degraded) per health system -> restart -> shield resumes
# 7) Loop guard: shield_server_id == an edge itself, or shield->edge, is rejected/skipped (no infinite loop)
# 8) Dashboard: per-zone shield toggle works (bumps config_version -> re-pull); shield PoP shows role badge;
#    analytics shows origin offload (requests with vs without shield) [flag metrics the stats schema lacks]
# 9) Live-site safety: enabling/disabling shield on a zone keeps it serving throughout (cdn.a2zjav.com unaffected)
# 10) Cache key parity: confirm the shield caches under the SAME key the edge uses (mismatch would mean no offload) - verify via X-Brisk-* + origin fetch count
```
**Done when:** with per‑zone origin shield enabled, **multiple edges missing the same object collapse to ~one origin fetch via the shield** (verified in origin logs), video slices cache at the shield tier, the per‑zone edge↔shield↔origin two‑tier proxy preserves cache keys + Host + Phase‑1 behavior, a **dead shield degrades gracefully to direct origin** (no blackhole), loop misconfig is guarded, shielded and non‑shielded zones coexist cleanly, and the dashboard exposes the per‑zone toggle + **origin‑offload metric** — all verified locally.

---

## Pitfalls (do not skip)
1. **Same cache key + Host across tiers** — if the shield caches under a different key than the edge, consolidation breaks → zero offload. This is the #1 correctness rule; verify it explicitly.
2. **`proxy_cache_lock` at BOTH tiers** — that's what collapses concurrent misses into one upstream fetch.
3. **Per‑zone shield routing** — with multi‑tenancy, shield is a per‑zone decision; each zone's block targets shield‑or‑origin independently; never mix zones' caches/origins.
4. **Graceful shield‑failure fallback** — dead shield → edges pull origin directly (tie to Phase‑3 health, e.g. origin as backup upstream + `proxy_next_upstream`); never blackhole a zone because the shield died.
5. **No loops** — edge must not shield through itself; shield must go to origin; reject self‑referential config.
6. **Shield = concentration point** — choose the PoP nearest the origin; it carries that zone's origin pulls; health‑monitor it; losing it = direct‑origin fallback.
7. **Don't shield uncacheable/dynamic** — opt‑in per zone; the hint should steer dynamic‑heavy tenants away (extra hop, no benefit).
8. **Reuse config_version pull** — push shield routing via the existing channel; don't invent a new one.
9. **Honest analytics** — real origin‑offload; flag metrics the stats schema can't yet provide (cleanup step adds origin‑tier counters).
10. **Live‑site safety + scope** — shield enable/disable never drops a live zone; this step is origin shield only. WAF = Step 4; Lua/cache‑rule enforcement = Step 5; cleanup = Step 6.

## Next — Step 4.4 (do NOT start) — WAF / rate-limiting
Per‑zone web‑application‑firewall + rate limiting at the edge: IP allow/deny, request‑rate limits, bad‑bot/abuse protection, and managed rule presets (OWASP CRS‑style), wired into the dashboard — building on the multi‑tenant per‑zone model and the tiered topology. Wait for the user's go‑ahead and a Step 4.4 prompt.
