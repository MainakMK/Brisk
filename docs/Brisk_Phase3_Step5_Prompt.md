# Brisk CDN — Phase 3 / Step 5 Build Prompt (Maintenance/Drain Mode UI + Status Surfacing)

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + all Phase‑2 prompts + `docs/Brisk_Phase3_Step1/2/3/4_Prompt.md` + `dashboard-reference/`. **Phases 1 & 2 + Phase 3 Steps 1–4 are complete:** `brisk-control` has the Bunny DNS client + lock + reconciler (online = enabled, off/drained = disabled‑but‑kept, deleted = auto‑removed, short TTL), **geo/latency Smart‑Record routing** (`servers.region` → coords/latency‑zone, per‑server weight/override), and a **self‑driven health checker** (~10s probes, fail‑2/rise‑3, immediate `Disabled` flip → **~32s measured failover**, per‑PoP configurable, all‑down blackhole guard, write‑on‑change). The **dashboard** (`brisk-dashboard`, React + TS + Vite + Tailwind v4 + shadcn + Recharts, **Voltage palette, dark default**) has Overview, Servers (live tiles + Add Server), Zones, Analytics, Logs (placeholder), Purge — but **none of the new DNS/health/routing state is shown in the UI yet** (it's all backend + API so far).

> **Read `CLAUDE.md`, the Phase‑3 Step‑1→4 prompts, and `dashboard-reference/brisk-design-spec.md` + `brisk-design-tokens.md` first.** This is **Step 5 of 6 in Phase 3**. Build the **maintenance/drain UI + surface DNS/health/routing status in the dashboard**. **No multi‑PoP deploy yet** (Step 6). Pass the acceptance tests, stop before Step 6.

## Step 3.5 goal (one line)
Give the dashboard **operational control + visibility over routing**: a per‑server (and per‑region) **drain/maintenance toggle** that pulls a PoP from rotation (and brings it back), plus **DNS/health/rotation status** surfaced across the **Servers** and **Overview** screens, and the **per‑PoP health/routing config** exposed in the UI.

## ✅ Test locally in Docker
Dashboard + `brisk-control` + TimescaleDB + NATS run locally; DNS/health act on the **test zone `a2zjav.com`**. Drain a PoP from the UI → watch its record disable + traffic reroute (via `dig`); undo → it returns. Live domain untouched.

---

## What "drain / maintenance mode" means here (build to this)
Across load balancers (AWS ELB/ALB, GCP, Apache Traffic Server, MinIO cordon, Rackspace), **drain = stop sending *new* traffic to the node while letting *existing* connections finish gracefully**, then it's safe to take offline. In Brisk's **DNS‑based** model that maps to:
- **Drain a PoP** → set its A record **`Disabled=true`** (out of rotation: no *new* DNS resolutions point to it) — but the **edge keeps serving** requests it already has and any clients still holding its cached IP until the TTL expires. The box isn't killed; it just stops getting new users. This is exactly the **"disabled‑but‑kept"** state from Step 2 — drain reuses it, just user‑initiated instead of health‑initiated.
- **Undo drain** → `Disabled=false` → back in rotation.
- **Distinct from delete** (off≠delete) and **distinct from a health failure** (same DNS effect, different *cause* + different status label so operators can tell "I drained this" from "this died").

So a server's **effective rotation state** is now driven by three inputs: **health** (Step 4), **heartbeat** (Step 2), and **admin drain** (this step). Define clear precedence: **admin drain wins** (an explicitly drained PoP stays out even if healthy), then health, then heartbeat. Surface *why* a PoP is out of rotation (drained vs unhealthy vs offline).

## Part 1 — Backend: drain state (small additions)
- Add a server **`maintenance`/`drained`** flag (migration; distinct from `status` and from health) + `drained_at`, `drain_reason` (optional).
- Endpoints:
  ```
  POST /api/v1/servers/{id}/drain        # body {reason?}  -> set drained=true -> reconcile -> record Disabled=true
  POST /api/v1/servers/{id}/undrain      # drained=false -> reconcile -> record Disabled=false (subject to health)
  POST /api/v1/regions/{region}/drain    # drain ALL servers in a region (bulk)
  POST /api/v1/regions/{region}/undrain
  GET  /api/v1/servers/{id}/rotation      # effective state + reason: in_rotation | drained | unhealthy | offline
  ```
- The **reconciler precedence**: `drained` → force `Disabled=true` regardless of health; else health/heartbeat decide. Undrain doesn't force‑enable a *sick* box — it returns it to health‑governed rotation (if it's healthy it re‑enters; if still unhealthy it stays out as unhealthy, not drained).
- Record drain/undrain in `dns_audit` (who/when/reason).
- **All‑region / all‑PoP guard:** draining the *last* in‑rotation PoP (or a whole region that would empty the pool) requires an explicit confirm and a warning — don't let an operator accidentally drain the entire CDN to zero. (Pairs with the Step‑4 all‑down guard.)

## Part 2 — Servers page: drain controls + status (extend Step 6.2)
On the **Servers** tiles + detail (from Phase‑2 Step 6.2), add:
- A **rotation/health badge** on each tile: **In rotation** (green), **Draining** (amber/“maintenance”), **Unhealthy** (red), **Offline** (muted) — text + icon, not color‑only. Show the *reason* on hover/detail.
- A **Drain / Resume toggle** (or button) per server, with a confirm: "Drain <PoP>? New traffic will route to other PoPs; existing connections finish; the box stays up." Undo = "Resume <PoP>".
- In server **detail**: health info (last probe, latency, consecutive fails/successes from `/health/status`), DNS info (its record, enabled/disabled, TTL, smart‑routing type + location + weight from `/dns/routing`), and the rotation state + reason.
- **Per‑PoP config UI:** expose the per‑server health overrides (interval/threshold/health‑enabled from Step 4) and routing weight/override (Step 3) as editable fields → `POST /servers/{id}/health` + `POST /servers/{id}/routing`. Network‑wide defaults shown as the inherited baseline.

## Part 3 — Region view / bulk drain
- A way to see PoPs **grouped by region** and **drain/resume a whole region** (your "put a region into maintenance → its traffic moves to nearby PoPs" ask). A simple region grouping on the Servers page (or a small Regions panel) with a per‑region drain action + the all‑region guard from Part 1.
- Make the effect legible: after draining a region, show that those PoPs are "Draining (maintenance)" and that traffic now routes to the remaining regions (geo will send users to the next‑closest in‑rotation PoP).

## Part 4 — Overview: network routing health (extend the hub)
On **Overview**, add a compact **routing/health summary** using real data:
- PoPs **in rotation vs total**, count **draining**, count **unhealthy/offline**.
- A small map or region list showing which regions are healthy / draining / down (use `servers.region` + rotation state; a full geo map is optional — a clean region list is fine).
- Recent routing events from `dns_audit` (drained, recovered, failed‑over) in the recent‑activity feed.
- Keep it honest: only real state; flag anything not yet available.

## Part 5 — DNS section (extend the Step‑1 DNS UI)
- Show the live **`cdn.<zone>` record set**: each edge's record, enabled/disabled, smart‑routing type + location, weight, health/monitor status, TTL — so an operator sees the whole routing picture in one place.
- Surface the **deletion‑protection lock** state (from Step 1) and the **routing mode** (geographic/latency, network‑wide TTL) here, editable where safe.
- Reflect that drain/health changes show up here live (polling on a sane interval).

## Part 6 — States & polish
- Live updates via TanStack Query polling (rotation/health badges refresh ~5–10s; stop hammering); keep stale data during refetch (no flicker).
- Skeleton/empty/error everywhere; confirms on drain (and strong confirm on region/last‑PoP drain); accessibility (badges text+icon, keyboard, focus‑trapped dialogs, contrast); responsive; Voltage tokens, dark/light.

---

## Acceptance tests (Step 3.5 definition of done — local Docker + test zone)
```bash
docker compose up --build -d
open http://localhost:5173/servers
# 1) Each PoP tile shows a correct rotation/health badge (In rotation / Draining / Unhealthy / Offline) with reason
# 2) Drain a PoP from the UI -> confirm -> its DNS record goes Disabled=true -> dig shows it dropped from cdn.a2zjav.com;
#    badge -> "Draining (maintenance)"; existing/box still up (not deleted)
# 3) Resume the PoP -> record re-enabled (if healthy) -> dig shows it back; badge -> "In rotation"
# 4) Drain vs unhealthy are distinguishable: a health-failed PoP shows "Unhealthy"; a drained one shows "Draining" — different reasons
# 5) Region drain: drain a whole region -> all its PoPs Draining -> traffic routes to remaining regions; resume restores them
# 6) All-PoP guard: attempting to drain the last in-rotation PoP / a region that empties the pool -> strong warning + confirm
# 7) Precedence: drain a healthy PoP -> stays out (drained beats healthy); undrain a still-sick PoP -> stays out as "Unhealthy" (not falsely "in rotation")
# 8) Per-PoP config UI: edit a server's health threshold + routing weight -> persists -> reflected in /health/config + /dns/routing
# 9) Overview shows in-rotation/total, draining, unhealthy counts + recent routing events (real data)
# 10) DNS section shows the full cdn set (per-record enabled/disabled, smart type, weight, health, TTL) + lock state + routing mode
# 11) Live badges refresh without manual reload; skeleton/empty/error states; responsive + dark/light correct
npm run build      # type-check + prod build pass
```
**Done when:** an operator can **drain/resume a PoP or a whole region from the dashboard** (record disabled‑but‑kept → traffic reroutes over the TTL → resume returns it), every screen **shows the real rotation/health/DNS state** with clear reasons (drained vs unhealthy vs offline), **per‑PoP health/routing config is editable in the UI**, the Overview hub summarizes network routing health, and the all‑PoP guard prevents accidentally draining the CDN to zero — all in the Voltage design, verified locally against the test zone.

---

## Pitfalls (do not skip)
1. **Drain = disabled‑but‑kept, not delete** — reuse the Step‑2 state; the box keeps serving in‑flight requests; off≠delete.
2. **Distinguish drain vs unhealthy vs offline** — same DNS effect (record disabled) but different *cause*; show the reason so operators aren't confused.
3. **Precedence: drain > health > heartbeat** — an explicitly drained PoP stays out even if healthy; undrain returns to *health‑governed* rotation (doesn't force a sick box in).
4. **All‑PoP/region guard** — never let an accidental drain empty the whole pool; strong confirm + warning (pairs with Step‑4 all‑down guard).
5. **Reroute is TTL‑bound** — be honest in the UI that drained traffic moves over the TTL (~15–60s), not instantly; existing viewers rely on HLS retry (consistent with Step 4 messaging).
6. **Live but gentle** — poll badges ~5–10s, keep stale during refetch, don't hammer the API/Bunny.
7. **Backend frozen except the small drain additions** — use existing health/routing/DNS endpoints; only add the drain flag/endpoints; flag any gap.
8. **Voltage + Recharts theming**, accessibility (text+icon badges), responsive — match the rest of the dashboard.
9. **Test zone only**; scope = drain UI + status surfacing. Multi‑PoP deploy = Step 6.

## Next — Step 3.6 (do NOT start) — multi‑PoP end‑to‑end + deploy
Provision 2–3 real edges in different regions, run the whole loop live (add → auto‑DNS → geo‑route → health/failover → drain), confirm purge/config/stats fan out across the fleet, then deliberately **cut over the live `mainakghosh.com` routing** to the multi‑PoP set. This completes Phase 3 (Brisk = a true distributed CDN). Wait for the user's go‑ahead and a Step 3.6 prompt.
