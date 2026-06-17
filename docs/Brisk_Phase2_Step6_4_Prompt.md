# Brisk CDN — Phase 2 / Step 6.4 Build Prompt (Analytics + Logs)

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + the Phase‑2 prompts + `dashboard-reference/` + the 6.1/6.2/6.3 prompts. **Steps 1–5 + 6.0–6.3 are complete:** `brisk-control` exposes the full API; `brisk-dashboard` (React + TS + Vite + Tailwind v4 + shadcn primitives + Recharts, **Voltage palette, dark default**) has the app shell, Overview (real `/overview`), Servers (live tiles + Add Server), and Zones (+ cache rules + assignments). Analytics, Logs, Purge are still placeholders.

> **Read `CLAUDE.md`, `dashboard-reference/brisk-design-spec.md` + `brisk-design-tokens.md`, and the 6.1–6.3 prompts first.** This is **Step 6.4 of Phase 2**. Build the **Analytics screen** + the **Logs screen (honest placeholder)** only — don't touch Purge (6.5). Pass the acceptance tests, stop before 6.5.

## Step 6.4 goal (one line)
Build the **Analytics page**: Voltage‑themed time‑series charts (bandwidth, requests/s, cache hit/miss ratio, plus CPU/RAM where useful) from `GET /api/v1/stats?...&resolution=1m`, with **filters by PoP, zone, and time range**, KPI summary cards, and proper loading/empty states — and a **Logs screen** that stays honest (no real logs API yet → a clear "coming soon" with what's planned).

## ✅ Test locally in Docker
Dashboard + `brisk-control` + TimescaleDB + NATS run locally. Stats flow into the `stats` hypertable + `stats_1m` continuous aggregate (Step 4). Generate traffic on a local edge to populate charts; the production edge (BLR1‑01) only contributes when it's heartbeating (tunnel up).

---

## API this screen uses (already built — backend frozen)
```
GET /api/v1/stats?server_id=&zone_id=&from=&to=&resolution=raw|1m
    -> time-series array of buckets: { bucket/time, requests, hits, misses, bytes_sent, bandwidth_bps, cpu_pct, ram_pct, disk_pct }
GET /api/v1/overview                      # network totals (for the top KPI row)
GET /api/v1/servers                       # to populate the PoP filter
GET /api/v1/zones                         # to populate the zone filter
```
`resolution=1m` reads the `stats_1m` continuous aggregate (fast, for ranges); `raw` reads recent raw rows. **Hit ratio is computed from summed hits/misses** (`hits / (hits + misses)`), not averaged — compute it client‑side from the returned counts (the API returns counts). If a metric the UI wants isn't returned, **flag the gap** (see "Known data gaps" below) — don't invent API.

## Part 1 — Filters (the control bar)
A filter bar at the top of Analytics (per dashboard best practice — add filters when users need several views of the same data):
- **Time range picker:** presets (Last 1h, 6h, 24h, 7d, 30d) + a custom range. Drives `from`/`to`. Default **Last 24h**.
- **Resolution:** auto‑pick from range (short ranges → `raw`/1m; long ranges → `1m`), or a manual toggle. Keep it simple: default `1m`.
- **PoP filter:** dropdown from `/servers` (All PoPs default) → sets `server_id`.
- **Zone filter:** dropdown from `/zones` (All zones default) → sets `zone_id`.
- Persist the selected filters in the URL query string so views are shareable/refresh‑safe.

## Part 2 — KPI summary row
A top row of **5–7 KPI cards** (per the F‑pattern / "limit KPIs" principle) summarizing the selected range:
- **Total requests**, **Cache hit ratio %** (computed from summed hits/misses), **Total bandwidth/egress** (humanized), **Avg req/s**, **Cache MISS %** or **origin egress** (the inverse — to correlate). Show a delta vs the previous equivalent period if easy. Color the hit‑ratio (e.g. green ≥ target). These are the numbers operators scan first.

## Part 3 — Time‑series charts (Recharts, Voltage‑themed)
Stacked below the KPIs, the core charts (use Recharts themed with the Voltage CSS variables, as established in 6.1 — no clashing theme):
- **Bandwidth over time** (area) — bytes/s; the cost/load signal.
- **Requests over time** (line/area) — req/s; surface spikes.
- **Cache hit vs miss over time** — either a stacked area (hits vs misses) or a hit‑ratio % line; this is the headline CDN metric. Correlating a bandwidth spike with a rising miss rate tells you whether traffic is origin‑driven or edge‑served — make that visually legible (e.g. bandwidth + miss% viewable together).
- **System (optional, when a single PoP is selected):** CPU/RAM/disk over time.
- Each chart: hover tooltips with exact values + timestamp, a legend, responsive sizing, **skeleton while loading**, and an **empty state** ("no data for this range/filter") that's honest rather than a flat zero line pretending to be data.

> Use **line/area for trends over time** (never pie — pie can't show change over time). Keep it clean: no chart junk, generous whitespace, one accent (Voltage) + muted grid.

## Part 4 — Per‑PoP / per‑zone breakdown (use the filters, not 50 charts)
Rather than cramming everything on screen, lean on the filters: selecting a PoP or zone re‑queries `/stats` with that `server_id`/`zone_id` and the same charts update. Optionally add a small **"by PoP" table** (each PoP's requests, bandwidth, hit‑ratio for the range) as a quick comparison — server‑side data, sortable. Keep the default (All PoPs/All zones, 24h) as the primary view since most users want the aggregate first.

## Part 5 — Logs screen (HONEST placeholder)
There is **no logs API yet** (flagged since 6.0). Do **not** fake it. Build the Logs route as a clear, well‑designed **"coming soon"** state that:
- Explains what Logs will show (real‑time request log: time, method, path, status, cache HIT/MISS, bytes, edge, zone) and that it's on the roadmap.
- Optionally notes that the raw data exists in the edge access logs today but isn't yet exposed via an API (true per Phase‑1/Step‑4).
- Looks intentional and polished (Voltage styled), not broken. Keep the reserved nav slot.
> Do not invent a logs endpoint or stream fabricated log lines. When a real logs API is built (future step/phase), this screen gets wired up.

## Part 6 — States & polish
- Skeletons for KPIs + charts; honest **empty** states per filter; **error/retry** if `/stats` fails.
- **Live‑ish refresh:** a modest `refetchInterval` (e.g. 30–60s) on the current range so Analytics stays current without hammering the DB; **don't** poll a 30‑day range every few seconds. (For "Last 1h" you can refresh faster.)
- URL‑synced filters; responsive (charts reflow / stack on mobile); accessibility (chart aria‑labels, keyboard‑reachable filters, contrast); Voltage tokens, dark/light.

---

## Known data gaps (be honest in the UI; flag, don't fake)
Per earlier reports, the `stats` schema is summary‑level. The UI must **not** invent these:
- **No status‑code breakdown** (2xx/4xx/5xx), **no geo/country**, **no top‑paths/referrers**, **no TTFB/latency percentiles** yet. These are common CDN‑analytics dimensions but require schema/agent work later. If you'd show them, instead render a small "not yet available" note or omit the panel. Document the gaps in a short comment/README so 6.x/Phase‑3 can add them.

## Acceptance tests (Step 6.4 definition of done — local Docker)
```bash
docker compose up --build -d
# generate traffic on a local edge so stats_1m has data across a range
open http://localhost:5173/analytics
# 1) KPI row shows real totals for the default Last-24h range (requests, hit ratio %, bandwidth, avg req/s)
# 2) Charts render from /stats?resolution=1m: bandwidth, requests, hit-vs-miss over time (Voltage-themed, tooltips work)
# 3) Time-range presets (1h/6h/24h/7d/30d) re-query and update all charts + KPIs; filters persist in the URL
# 4) PoP filter + Zone filter re-query /stats with server_id/zone_id; charts update accordingly
# 5) Empty state: pick a range with no data -> honest "no data" (not a fake zero line)
# 6) Refresh: current range updates on a sane interval (e.g. 30-60s), not aggressively
# 7) Logs route -> polished "coming soon" (no fabricated logs, no invented endpoint)
# 8) Skeleton/error states render; responsive + dark/light correct
npm run build      # type-check + prod build pass
```
**Done when:** Analytics shows real KPI summaries + Voltage‑themed time‑series charts from `/stats`, with working **PoP/zone/time‑range filters** (URL‑synced) and honest empty/loading states; the **Logs** screen is a clear, polished placeholder with no fabricated data; and the known data gaps (status codes, geo, top paths, latency) are flagged rather than faked — all verified locally.

---

## Pitfalls (do not skip)
1. **Compute hit ratio from summed counts** (`hits/(hits+misses)`), not by averaging ratios — matches the Step‑4 backend.
2. **No faked data** — empty ranges show honest empty states; Logs is a real "coming soon", not invented log lines; missing dimensions (status/geo/paths/latency) are flagged, not fabricated.
3. **Sane refresh** — short ranges can refresh ~30–60s; never poll a 30‑day query every few seconds.
4. **Charts trends = line/area, never pie**; clean styling, Voltage theme, no chart junk.
5. **URL‑sync filters** so views are shareable/refresh‑safe.
6. **Read the right resolution** — `1m` (continuous aggregate) for ranges, `raw` only for short recent windows; don't scan raw over 30 days.
7. **Backend frozen** — use existing endpoints; flag gaps, don't invent API or change the control plane.
8. **Scope** — Analytics + Logs placeholder only. No Purge here (6.5).

## Next — Step 6.5 (do NOT start) — finishes Phase 2
**Purge UI + polish:** purge by URL/prefix/zone + purge‑all (`/zones/{id}/purge`, `/purge/all`) with job status (`/purge/jobs`), plus final cross‑app polish (consistent empty/error/loading, the Overview page tying everything together, command‑palette ⌘K wiring, responsive/accessibility sweep). After 6.5, **Phase 2 is complete** — the admin dashboard fully manages the CDN. Wait for the user's go‑ahead and a Step 6.5 prompt.
