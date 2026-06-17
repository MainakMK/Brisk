# Brisk CDN — Phase 2 / Step 6.5 Build Prompt (Purge UI + Final Polish) — completes Phase 2

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + the Phase‑2 prompts + `dashboard-reference/` + the 6.1–6.4 prompts. **Steps 1–5 + 6.0–6.4 are complete:** `brisk-control` exposes the full API; `brisk-dashboard` (React + TS + Vite + Tailwind v4 + shadcn primitives + Recharts, **Voltage palette, dark default**) has the app shell, Overview, Servers (live tiles + Add Server), Zones (+ cache rules + assignments), and Analytics + an honest Logs placeholder. **Purge is the last placeholder.**

> **Read `CLAUDE.md`, `dashboard-reference/brisk-design-spec.md` + `brisk-design-tokens.md`, and the 6.1–6.4 prompts first.** This is **Step 6.5 of Phase 2 — the final step.** Build the **Purge UI** + a **cross‑app polish pass**. Pass the acceptance tests. After this, **Phase 2 is complete.**

## Step 6.5 goal (one line)
Build the **Purge UI** (purge by URL / prefix / whole‑zone / purge‑all, with live job status) wired to the real NATS‑backed purge API, then do a **final polish sweep** across the whole dashboard (consistency, ⌘K, empty/error/loading states, accessibility, responsive) so Phase 2 ships as a coherent, professional admin dashboard.

## ✅ Test locally in Docker
Dashboard + `brisk-control` + TimescaleDB + NATS + a local agent/edge run locally. Purge is the instant path (Step 5: NATS JetStream → agent deletes cache files by KEY prefix), so a purge should flip a cached object to MISS within ~milliseconds–seconds on the local edge.

---

## API this screen uses (already built — backend frozen)
```
POST /api/v1/zones/{id}/purge      # body {type:"url"|"prefix"|"zone", target:"/path"}  -> 202 + job id
POST /api/v1/purge/all             # purge entire cache (purge_all) on selected/all servers -> 202 + job id
GET  /api/v1/purge/jobs?zone_id=   # purge history/status: id, zone_id, type, target, status (pending|done|partial|failed), edges_total, edges_done, created_at, completed_at
GET  /api/v1/zones                 # to pick the zone to purge
```
(From Step 5: purge fans out over NATS to every edge serving the zone; agents ack after applying; `purge_jobs` tracks `edges_done/edges_total`.) If a field is missing, **flag it** — don't invent API.

## Part 1 — Purge screen
Per `brisk-design-spec.md` (Voltage), a dedicated **Purge** page (and a quick "Purge" action on each zone's detail). Layout:

### 1a. New purge (the action panel)
- **Zone selector** (from `/zones`) — which zone to purge.
- **Purge type** (radio/segmented), matching the standard CDN model:
  - **By URL** — exact path(s); one per line. Note: include query params if the cached key has them.
  - **By prefix** — e.g. `/assets/` or `/video/movie.mp4` (clears all slices of a video — Brisk uses wildcard‑prefix purge for sliced content, per Step 5). Show a hint that prefix purge clears everything under that path.
  - **Whole zone** — purge everything for this zone.
  - **Purge everything** (purge‑all) — separate, visually distinct, **danger‑styled** with an extra confirm (this clears the entire cache and causes an origin‑load surge as everything re‑fetches).
- **Submit** → the matching endpoint → `202` + job id. Use `useMutation`; on success, surface the new job in the jobs list and invalidate `/purge/jobs`.
- **Confirmation:** all purges get a review/confirm step; **purge‑all and whole‑zone get a stronger confirm** (type‑to‑confirm or an explicit "Yes, purge everything" dialog). Make destructive scope obvious before the click — this is the one screen where an accidental click is expensive.

### 1b. Purge jobs (history + live status)
- A **jobs table** from `/purge/jobs`: time, zone, type, target, **status pill** (pending = amber, done = green, partial = amber/blue, failed = red), and **edges progress** (`edges_done / edges_total`).
- **Live status:** poll `/purge/jobs` with a short `refetchInterval` (e.g. 2–3s) **only while there are pending/partial jobs**; stop polling when all jobs are settled (return `false` from the interval fn once nothing is pending — same pattern as the provisioning log in 6.2). A just‑submitted job should visibly progress pending → done within seconds on the local edge.
- Empty state ("no purges yet") + skeleton + error/retry.

### 1c. Honesty
- Be clear that purge propagates over the **instant NATS channel** (fast — unlike the ~15s config pull), but isn't always perfectly instantaneous across many edges (a brief window can exist; an offline edge gets it on reconnect via JetStream durability, per Step 5). Reflect partial status honestly via `edges_done/edges_total`.
- Optionally surface the post‑purge reality (a purge drops cache hit‑ratio briefly as objects re‑fetch from origin) as a subtle note — don't over‑warn, but it's professional context.

## Part 2 — Final cross‑app polish sweep
Now make the whole dashboard feel like one finished product (per the design principles in `brisk-design-spec.md`):
- **⌘K command palette:** wire the top‑bar search placeholder into a working command palette (shadcn `command`) — quick‑nav to each screen, "Add Server", "Add Zone", "Purge", jump to a zone/server by name. Keyboard‑first.
- **Consistency pass:** uniform card/table/dialog styling, spacing on the 8px grid, consistent status‑pill colors across Servers/Zones/Purge, consistent humanized units (bandwidth, counts, durations, relative timestamps like "2m ago"), consistent button hierarchy.
- **States everywhere:** every screen has proper **skeleton / empty / error+retry** states; no raw spinners where a skeleton fits; no flashing empty during refetch (keep prior data).
- **Overview as the hub:** make sure Overview ties it together — KPIs + a compact PoP health summary + recent activity (recent purges from `/purge/jobs`, recent provisions) — links into the deeper screens. (Use only real endpoints; flag gaps.)
- **Dark/light:** audit both themes on every screen (contrast, borders, chart legibility).
- **Responsive:** every screen works down to mobile (sidebar → sheet, tables → stacked, grids → 1‑col).
- **Accessibility (WCAG 2.1 AA):** focus‑trapped dialogs, keyboard nav across tables/forms/palette, ARIA labels, status conveyed by text+icon not color alone, visible focus rings, contrast ≥ 4.5:1.
- **Auth seam:** admin routes are open locally — confirm there's a single clean place to add the admin login/token later (don't build auth now; just verify the seam exists, per 6.1).
- **README:** update `brisk-dashboard/README.md` with run instructions (dev + prod Docker), the env vars, and a short note on the known gaps (logs API, status‑code/geo/top‑paths analytics, edge‑rule enforcement, rule‑update/inverse‑lookup endpoints) so the Phase‑2 cleanup list is captured.

---

## Acceptance tests (Step 6.5 definition of done — local Docker)
```bash
docker compose up --build -d
# warm cache on the local edge, then:
open http://localhost:5173/purge
# 1) Purge by URL: submit /style.css -> 202 + job appears; within seconds job -> done; next request to /style.css is a MISS
# 2) Purge by prefix: /video/movie.mp4 -> clears all slices; subsequent range request is MISS
# 3) Whole-zone purge: stronger confirm required; clears the zone
# 4) Purge-all: danger-styled + type-to-confirm; clears entire cache (test on the local/test edge, NOT production)
# 5) Jobs table: live status polls while pending -> stops when settled; edges_done/edges_total shown; partial/failed render correctly
# 6) Confirmations: destructive scope is obvious; accidental purge-all is hard to trigger
# 7) ⌘K palette: opens, navigates to every screen + quick actions, keyboard-only works
# 8) Polish: consistent pills/units/timestamps; skeleton/empty/error on every screen; no empty-flash on refetch
# 9) Overview hub: shows KPIs + PoP health + recent activity, links into screens (real data only)
# 10) Dark/light correct on all screens; responsive to mobile; keyboard/ARIA pass
npm run build      # type-check + prod build pass; console clean
```
**Done when:** purges (URL / prefix / whole‑zone / purge‑all) work from the UI with **honest live job status** and strong confirmations on destructive scope, the **⌘K palette** works, and the **whole dashboard is consistent, accessible, responsive, and polished** in the Voltage design — verified locally. **At this point Phase 2 is complete:** the Brisk admin dashboard fully manages the CDN (servers, zones, rules, analytics, purge) against the live control plane.

---

## Pitfalls (do not skip)
1. **Destructive‑scope safety** — purge‑all and whole‑zone need strong, explicit confirms (type‑to‑confirm for purge‑all); never make a full purge a one‑click accident. Test purge‑all on the local/test edge, **not production**.
2. **Stop polling settled jobs** — poll `/purge/jobs` only while pending/partial; return `false` to clear the interval once done (don't poll forever).
3. **Honest status** — reflect `edges_done/edges_total` and partial/failed truthfully; purge is fast (NATS) but show real per‑edge progress, and note an offline edge gets it on reconnect.
4. **Prefix purge for video** — make clear prefix/`*` purge is how all slices of a sliced video are cleared (Step 5 behavior).
5. **Invalidate on mutation** — after a purge, refresh `/purge/jobs`; don't leave stale UI.
6. **Polish without regressions** — the polish sweep must not break Servers/Zones/Analytics; re‑verify those still work after refactors.
7. **Backend frozen** — existing endpoints only; flag gaps (capture in the README), don't invent API or change the control plane.
8. **Real data only on Overview** — no fabricated "recent activity"; use `/purge/jobs` + real sources, flag anything missing.

## After 6.5 — Phase 2 is DONE ✅
The Brisk admin dashboard now manages the whole CDN end‑to‑end: live PoPs + add‑server provisioning, zones + cache rules + assignments, analytics, and instant purge — all on the Go control plane, in Brisk's own Voltage design.

**Phase‑2 cleanup backlog (note for later, do NOT start):** rule‑update/bulk‑reorder endpoint (`PUT /rules/{id}`), `GET /zones/{id}/servers` inverse lookup, network‑aggregate `/stats`, status‑code/geo/top‑paths/latency in the stats schema, a real **logs API**, edge enforcement of custom cache rules, and admin auth for the dashboard.

**Phase 3 (preview, do NOT start):** multi‑PoP — provision several edges, **Bunny DNS GeoDNS** routing + health‑based failover, so traffic goes to the nearest healthy PoP. **Phase 4:** WAF/security hardening, origin shield, Lua/OpenResty edge logic, custom‑domain CNAMEs, then the **customer‑facing portal** (the role‑aware API is already designed for it). Wait for the user's go‑ahead and a Phase 3 plan.
