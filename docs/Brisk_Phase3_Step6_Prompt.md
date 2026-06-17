# Brisk CDN — Phase 3 / Step 6 Build Prompt (Multi‑PoP End‑to‑End + Deploy) — completes Phase 3

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + all Phase‑2 prompts + `docs/Brisk_Phase3_Step1/2/3/4/5_Prompt.md` + `dashboard-reference/`. **Phases 1 & 2 + Phase 3 Steps 1–5 are complete:** Brisk has a live edge (`brisk.mainakghosh.com`, BLR1‑01), the full control plane + dashboard, and the complete DNS routing stack — Bunny DNS client + lock + reconciler (online=enabled, off/drained=disabled‑but‑kept, deleted=auto‑removed, short TTL), **geo/latency Smart‑Record routing** from `servers.region`, a **self‑driven health checker** (~32s failover, per‑PoP configurable), and the **drain/maintenance UI + rotation/health status** across the dashboard. Everything so far was validated against **test zone `a2zjav.com`** — the live domain's routing has **not** been cut over to the multi‑PoP set yet.

> **Read `CLAUDE.md` and the full Phase‑3 Step‑1→5 prompts first.** This is **Step 6 of 6 in Phase 3 — the final step.** Provision real multi‑region edges, validate the whole loop live, then **deliberately cut over the live domain**. This is largely an **operational/deploy + end‑to‑end‑verification** step (less new code, more wiring real infra + careful cutover). After this, **Phase 3 is complete.**

## Step 3.6 goal (one line)
Stand up **2–3 real edges in different regions**, run the entire pipeline live (add → auto‑DNS → geo‑route → health/failover → drain), confirm **purge/config/stats fan out across the fleet**, then **cut the live `mainakghosh.com` routing over to the multi‑PoP smart set** with a safe rollback — making Brisk a true distributed CDN.

## ⚠️ This step touches PRODUCTION — go carefully
Unlike Steps 1–5 (test zone only), Step 6 deliberately moves the **live domain** onto multi‑PoP routing. Treat it as a controlled cutover: validate everything on the new PoPs **before** repointing live traffic, keep BLR1‑01 in the set, and have a **one‑step rollback** ready. Test the full loop on a **staging hostname first** (e.g. `cdn-staging.mainakghosh.com` or keep using `a2zjav.com`), then cut over.

---

## Part 1 — Provision 2–3 real regional edges
- Spin up **2–3 cheap VPSs in distinct regions** (e.g. an EU + a US + the existing IN/BLR1‑01) — the user provisions the VPSs (DigitalOcean or similar) and supplies IP + SSH creds.
- Add each via the **dashboard Add‑Server flow** (Phase‑2 Step 6.2): it SSH‑provisions the edge (nginx + agent + TLS + the full Phase‑1 stack), the agent registers, pulls config, heartbeats, and ships stats — exactly as BLR1‑01 does. Set each server's correct **`region`** so geo‑routing places it.
- Confirm each new edge independently serves cached content over HTTPS (Phase‑1 acceptance: cache HIT/MISS, video slices, branded headers) before it matters for routing.

## Part 2 — Auto‑DNS + geo‑routing across the fleet (validate live behavior)
- As each edge comes **online**, the reconciler (Steps 2–3) should auto‑add its IP to the `cdn.<zone>` set as a **geographic/latency Smart Record** at its region's location, short TTL. Verify in the Bunny dashboard + via `dig`/DoH with EDNS client‑subnet from different regions → **users get the nearest PoP**.
- Verify **weights** (a higher‑capacity box can carry more) and that same‑region edges load‑balance.

## Part 3 — Health/failover + drain across the fleet (the real test)
- **Failover:** kill one regional edge → the health checker pulls it (~32s) → `dig` from that region now returns the next‑nearest healthy PoP → restore → it returns (rise‑3). Measure real cross‑region failover.
- **Drain:** from the dashboard, drain one PoP and a whole region (Step 5) → traffic reroutes to nearby PoPs → resume → restored. Confirm the rotation/health badges + Overview routing summary reflect reality across all PoPs.
- **All‑down/last‑PoP guards** behave correctly with a real fleet.

## Part 4 — Fan‑out: purge, config, stats across all PoPs
Confirm the Phase‑1/2 mechanisms all work fleet‑wide (not just on one edge):
- **Purge** (Phase‑2 Step 5/6.5): a purge fans out over NATS to **every** edge serving the zone → all PoPs drop the object (verify each edge independently goes MISS). Sliced‑video prefix purge clears slices on all PoPs.
- **Config** (Phase‑2 Step 3): a zone/cache‑rule change bumps `config_version` → **every** assigned edge re‑pulls within the poll interval; all converge.
- **Stats** (Phase‑2 Step 4): each edge ships stats → Overview/Analytics show **per‑PoP** and **aggregate** data; the "All PoPs" merge reflects the real fleet.

## Part 5 — Live cutover (the deliberate production change)
1. **Pre‑flight on staging:** run Parts 2–4 against a staging hostname (or `a2zjav.com`) with the full multi‑PoP set; confirm green.
2. **Cut over `cdn.mainakghosh.com` (or the live hostname):** point the live CDN hostname at the multi‑PoP smart set (BLR1‑01 + new edges) instead of the single A record. Keep TTL short during cutover for fast rollback.
3. **Watch:** confirm the live site serves from the nearest PoP, stays up, cache works, video plays; watch health/stats in the dashboard.
4. **Rollback plan (must exist before cutover):** a documented one‑step revert (re‑point the hostname back to BLR1‑01 / disable the new records) if anything regresses. The deletion‑protection lock + disabled‑but‑kept design make this safe.
5. **Honor the live‑site rule:** never leave the live hostname with zero in‑rotation PoPs; do the cutover when you can watch it.

## Part 6 — Docs + Phase‑3 close‑out
- Update `CLAUDE.md` / README: the network is now multi‑PoP; document the regions, the routing mode + TTL, the failover characteristics (~32s typical), the drain runbook, and the rollback procedure.
- Capture the **Phase‑3 cleanup backlog** (anything deferred) and confirm the **Phase‑2 backlog** items still tracked.
- A short **multi‑PoP runbook**: how to add a region, drain for maintenance, interpret rotation/health badges, and roll back a bad edge.

---

## Acceptance tests (Step 3.6 definition of done — completes Phase 3)
```bash
# (run against staging first, then the live hostname after cutover)
# 1) 2-3 real edges in distinct regions, each serving HTTPS with cache HIT/MISS + video + branded headers (Phase-1 green per edge)
# 2) Auto-DNS: each online edge auto-appears in the cdn set as a geo/latency Smart Record at its region; dig/DoH+ECS from
#    different regions returns the NEAREST PoP
# 3) Failover (real): kill a regional edge -> ~32s -> that region's users get the next-nearest healthy PoP -> restore -> returns
# 4) Drain (real): drain a PoP and a region from the dashboard -> reroutes to nearby PoPs -> resume -> restored; badges/Overview correct
# 5) Purge fan-out: one purge -> ALL PoPs go MISS (verify each edge); sliced video cleared on all
# 6) Config fan-out: a cache-rule/zone change -> ALL assigned edges re-pull + converge (config_version)
# 7) Stats fan-out: per-PoP + aggregate stats correct in Overview/Analytics for the real fleet
# 8) Guards: last-PoP/region drain warns; all-down doesn't blackhole
# 9) LIVE CUTOVER: live hostname now resolves to the multi-PoP smart set; site stays up, nearest-PoP, cache+video work;
#    rollback procedure documented + verified (can revert in one step)
# 10) Docs/runbook updated; Phase-2 + Phase-3 backlogs captured
```
**Done when:** Brisk runs as a **real multi‑PoP CDN** — multiple regional edges auto‑registered into geo/latency DNS, nearest‑PoP routing confirmed across regions, **~32s health failover and dashboard drain both working on the live fleet**, and **purge/config/stats fanning out to all PoPs** — with the **live domain safely cut over** to the multi‑PoP set and a verified one‑step rollback. **Phase 3 is complete.**

---

## Pitfalls (do not skip)
1. **Validate on staging before the live cutover** — never repoint production until the full multi‑PoP loop is green on a staging hostname / test zone.
2. **Always have a one‑step rollback** ready and documented *before* cutover; keep TTL short during cutover; never leave the live hostname with zero in‑rotation PoPs.
3. **Each edge must pass Phase‑1 on its own** before it joins routing — don't route users to a half‑provisioned box.
4. **Verify fan‑out per edge, not just globally** — purge/config/stats must be confirmed on *every* PoP independently.
5. **Real geo testing is still partial** — use DoH/EDNS client‑subnet + multiple regions; document what you could and couldn't verify physically.
6. **Failover is ~32s + resolver caching** — set expectations honestly; in‑flight viewers rely on HLS retry (consistent with Steps 4–5).
7. **Cost awareness** — 2–3 extra VPSs cost real money; the user controls how many/how long; tear down staging boxes after.
8. **Secrets** — new edges' SSH creds used once, not stored (Phase‑2 Step 2); Bunny key stays out of logs/repo.
9. **This is the production step** — move deliberately, watch dashboards, and stop if anything regresses.

## After Step 6 — Phase 3 is DONE ✅
Brisk is now a **true distributed CDN**: multiple regional PoPs, nearest‑PoP geo/latency DNS routing, ~32s automatic health failover, dashboard‑driven maintenance/drain, and instant purge + config + stats fanning out across the whole fleet — all self‑hosted, on the Go control plane, in Brisk's own Voltage dashboard.

**Next phases (preview, do NOT start):**
- **Phase 4 — Security + productization:** WAF/rate‑limiting, origin shield (reduce origin load via a mid‑tier cache), Lua/OpenResty edge logic, custom‑domain CNAMEs for customers, and eventually **Anycast** (own IP space + BGP) as the path to *instant* (sub‑second) failover beyond DNS TTL — the "discuss in future" item.
- **Customer Portal:** the role‑aware API (`accounts.role`, built since Phase 2) powers a customer‑facing dashboard where buyers manage only their own zones/stats/purge — the commercial layer that makes Brisk sellable.
- **Phase‑2/3 cleanup backlog:** `PUT /rules/{id}` + bulk reorder, `GET /zones/{id}/servers`, network‑aggregate `/stats`, status‑code/geo/top‑paths/latency in stats, real logs API, edge enforcement of custom cache rules, admin auth for the dashboard.

Wait for the user's go‑ahead and a Phase 4 plan.
