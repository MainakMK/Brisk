# Brisk CDN — Phase 3 / Step 3 Build Prompt (GeoDNS Routing — Nearest PoP)

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + all Phase‑2 prompts + `docs/Brisk_Phase3_Step1_Prompt.md` + `docs/Brisk_Phase3_Step2_Prompt.md` + `dashboard-reference/`. **Phases 1 & 2 + Phase 3 Steps 1–2 are complete:** live edge serves `brisk.mainakghosh.com`; admin dashboard manages everything; `brisk-control` has a Bunny DNS client (`internal/dns`) with CRUD + Smart‑Record/monitor fields modeled, a deletion‑protection lock, and a **reconciler** that makes DNS follow the `servers` table (online = enabled A record in the `cdn.<zone>` set, off/drained = disabled‑but‑kept, deleted = auto‑removed), with short‑TTL records (60s default, clamp 30–120) and an audit log. Verified against test zone `a2zjav.com`.

> **Read `CLAUDE.md`, the Phase‑3 Step‑1/Step‑2 prompts, and the Phase‑2 prompts first.** This is **Step 3 of 6 in Phase 3**. Build **geographic/latency Smart‑Record routing** so users get the nearest edge. **No health‑based failover loop yet** (that's Step 4) and **no routing UI beyond surfacing state** (Step 5). Pass the acceptance tests, stop before Step 4.

## Step 3.3 goal (one line)
Turn the flat `cdn.<zone>` record set into a **smart geo/latency‑routed set**: each edge's A record carries a **Smart‑Record type (geographic or latency) + its location**, driven by `servers.region`, so a query returns the **closest** edge — configurable network‑wide with per‑server overrides.

## ✅ Test against the test zone
`brisk-control` runs locally; routing is configured on the **test zone `a2zjav.com`** (live domain untouched). Geo routing is hard to fully prove from one location, so testing leans on: the Bunny dashboard showing the smart config, `dig` via resolvers/EDNS in different regions where possible, and Bunny's own routing behavior. Verify the *configuration* is correct even where you can't physically test every geography.

---

## How Bunny Smart Records work (build to this)
- A/AAAA records in a **set** (same name, e.g. `cdn.<zone>`) can each be a **Smart Record** with a **routing type**: **Geographic** or **Latency**.
  - **Geographic:** routes by the end user's location; in the set, the user's location is evaluated against all records and **the record with the closest range to the user is returned**. Location is derived from the resolver location, query remote IP, or EDNS0 client‑subnet for accuracy.
  - **Latency:** routes by estimated latency from the user (country/state) to the **nearest Bunny datacenter region** you select for the record — sometimes more accurate than raw geographic distance since distance ≠ network latency. Bunny advises **selecting the Bunny region closest to your server**.
- **Per‑coordinate subsets:** records sharing the same coordinates form a sub‑group (so two edges in the same city load‑balance between each other, then geo picks the city).
- **Weights (0–100)** still apply within a chosen location group (e.g. two edges in the same region split load by weight).
- **Offline records auto‑drop:** if monitoring marks a record offline, it's removed from routing and traffic fails over to others in the set — **if all are offline, filtering is disabled and all are returned** (prevents a total blackhole). *(The fast self‑driven failover loop is Step 4; here we just set the smart‑routing config and rely on Bunny's built‑in behavior.)*

## Part 1 — Region → location mapping
- Each server has a `region` (e.g. `IN-DEL`, `US-IL`, `EU-FRA`). Build a **region → geo‑coordinates (lat/long) and/or Bunny latency‑region** mapping table (a Go map or a small `dns_regions` config/table). Cover the regions Brisk will use; make it easy to extend.
- Decide the **routing mode** (geographic vs latency) as a **network‑wide config** (`BRISK_DNS_ROUTING_MODE = geographic|latency`, default geographic — simplest mental model: nearest by location), with an optional **per‑server override** (a server could be pinned to a specific routing behavior). This per‑server‑configurable‑across‑all‑PoPs design is what the user asked for.
- For **latency mode**, map each `region` to the closest **Bunny datacenter region** (per Bunny's guidance to pick the region nearest your server).

## Part 2 — Extend the reconciler to set Smart‑Record fields
Build on the Step‑2 reconciler. When it creates/updates each edge's A record in the `cdn.<zone>` set, it now also sets:
- **`SmartRoutingType`** = geographic or latency (from the network‑wide mode / per‑server override).
- **The location**: geo coordinates (lat/long from the region map) for geographic mode, or the chosen Bunny latency region for latency mode.
- **Weight** (default 100; expose as a per‑server field for later capacity‑based balancing — e.g. a 10 Gbps box could outweigh a 1 Gbps box).
- Keep everything else from Step 2 intact (enabled/disabled state, short TTL, `brisk:server:<edge_id>` comment, idempotency, only‑Brisk‑records, drift correction).
- **Idempotent:** changing a server's region (or the routing mode) updates the smart config on the next reconcile without duplicating records.

> Verify against the live Bunny API that the exact field names/enums for Smart‑Record type + coordinates match the current API (the `internal/dns` structs from Step 1 modeled these — confirm the real payload shape with a test write and a read‑back; adjust the client if Bunny's field names differ).

## Part 3 — Config + per‑server settings
- Network‑wide: `BRISK_DNS_ROUTING_MODE` (geographic|latency), the region→coords/region map.
- Per‑server (in the `servers` table / API): optional `routing_weight` (0–100, default 100) and optional `routing_override` (pin a server's mode/location if ever needed). Add the columns via a migration; default behavior = use the network‑wide mode + region map.
- Surface the **effective routing config per server** in the API (so Step 5's UI can show "this PoP routes IN/SA traffic, weight 100, geographic") — read‑only computed view is fine.

## Part 4 — Endpoints (thin; full UI is Step 5)
```
GET  /api/v1/dns/routing            # current mode + region map + per-server effective routing (read-only view)
POST /api/v1/dns/reconcile          # (from Step 2) now also applies smart-routing fields
GET  /api/v1/dns/reconcile/preview  # dry-run diff now includes smart-routing changes
```

---

## Acceptance tests (Step 3.3 definition of done — test zone)
```bash
docker compose up --build -d        # brisk-control, test zone, BRISK_DNS_ROUTING_MODE=geographic
# 1) Two online edges in different regions (e.g. IN-DEL + US-IL) -> reconcile
#    -> both A records in cdn.a2zjav.com set, each with SmartRoutingType=geographic + its region's coordinates
curl -s localhost:8080/api/v1/dns/routing      # shows mode + per-server effective routing (region, coords, weight)
# 2) Verify in Bunny dashboard: the set shows geographic smart routing with the right locations per record
# 3) Read-back: GET /dns/records shows the smart-routing fields populated correctly (matches what was written)
# 4) Geo behavior (best-effort): query via resolvers/EDNS client-subnet for different regions ->
#    closest edge's IP is returned (where testable); document which you could verify
# 5) Switch BRISK_DNS_ROUTING_MODE=latency -> reconcile -> records update to latency type mapped to nearest Bunny region (no dup records)
# 6) Change a server's region -> reconcile -> its record's coordinates update; idempotent (second run = no change)
# 7) Weight: set one edge weight=100, another=50 in the same region -> reflected in the set
# 8) Drain one edge (Step 2) -> it's disabled/out of rotation; the set still geo-routes among the rest
# 9) Only-Brisk + drift safety still hold (non-Brisk records untouched; manual smart-field change re-converged)
```
**Done when:** the `cdn.<zone>` set is a working **smart geo/latency‑routed set** — each edge tagged with its routing type + location from `servers.region`, network‑wide mode configurable with per‑server weight/override, applied idempotently by the reconciler, verified in the Bunny dashboard + via read‑back (and geo behavior where physically testable) — live domain untouched.

---

## Pitfalls (do not skip)
1. **Confirm the real Bunny field names/enums** for Smart‑Record type + coordinates with a write+read‑back; don't assume the Step‑1 struct guesses are exact — adjust `internal/dns` if needed.
2. **Geographic vs latency** — default geographic (nearest by location); offer latency mode mapped to the **nearest Bunny region** per server (Bunny's guidance). Make it network‑wide configurable + per‑server overridable.
3. **Don't break Step‑2 semantics** — enabled/disabled state, short TTL, `brisk:` tagging, only‑Brisk‑records, idempotency, drift correction all still hold after adding smart fields.
4. **Idempotent smart updates** — changing region/mode updates fields in place; never duplicate records in the set.
5. **All‑offline safety is Bunny's** — if every record is offline Bunny returns all (no blackhole); don't fight that. Real fast failover = Step 4.
6. **Geo testing is inherently partial** from one location — verify *configuration correctness* (dashboard + read‑back) even where you can't test every geography; use EDNS client‑subnet/regional resolvers where possible and document coverage.
7. **Weights are within a location group** — geo picks the location first, then weight splits among same‑location edges. Don't expect weight to override geography across regions.
8. **Test zone only** — live `mainakghosh.com` cutover is later.
9. **Scope** — smart‑routing config only. The self‑driven ~30s health/failover loop is Step 4; routing UI is Step 5.

## Next — Step 3.4 (do NOT start) — fast health checks + failover (~30s target)
Build a **self‑driven health‑check loop in `brisk-control`** (configurable interval, e.g. ~10s, low failure threshold) that flips a dead edge's record to `Disabled` **immediately** rather than waiting on Bunny's ~30s monitor — combined with a **low TTL (~15s)** — to target **~30s end‑to‑end failover**, all **per‑PoP configurable across the fleet** (network‑wide defaults + per‑server overrides). Will also document the TTL/resolver caveat and the in‑flight‑viewer (HLS retry) behavior. Wait for the user's go‑ahead and a Step 3.4 prompt.
