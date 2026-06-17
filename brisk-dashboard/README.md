# brisk-dashboard

Brisk CDN admin dashboard — **Step 6.1 skeleton** (React + TypeScript + Vite + Tailwind v4 +
shadcn-style primitives + Recharts). Voltage design tokens, **dark-first**.

## What's wired
- App shell: collapsible sidebar (Overview · Servers · Zones · Analytics · Logs · Purge · Settings),
  top bar (⌘K palette, dark/light toggle, Add Server/Zone), React Router v7 layout + 404, responsive
  mobile sheet.
- **Overview** fetches **real** `GET /api/v1/overview` via TanStack Query (`useOverview`) and renders
  the hero KPIs (bandwidth, req/s, hit ratio, PoPs online/total) with skeleton + error states. The
  PoP-status rail uses real `GET /api/v1/servers`.
- Other screens are honest placeholders ("coming in 6.x"); Logs is deferred (no endpoint yet);
  Settings is a minimal stub. Analytics shows a **clearly-marked mock** chart to prove charts render
  with the Voltage theme.

## Design system
Voltage tokens (Option C, indigo) live in `src/index.css` as Tailwind v4 CSS variables for light +
dark — one unified token system drives every primitive and chart. See
`../dashboard-reference/brisk-design-tokens.md`.

## Run
### Local
```bash
cp .env.example .env       # VITE_API_URL=http://localhost:8080
npm install
npm run dev                # http://localhost:5173
npm run build              # type-check + production build
```

### Docker (with the control plane)
From `brisk-control/`:
```bash
docker compose up --build -d   # timescaledb + nats + brisk-control + brisk-dashboard
# dashboard: http://localhost:5173   control plane: http://localhost:8080
```

## Charts note
Tailwind v4 + shadcn made Tremor's npm theme clash with the token system, so charts use **Recharts
directly, themed with the Voltage CSS variables** (the "Tremor Raw" approach the 6.1 prompt
recommends). One accent (`--chart-1`) + gradient fill, muted grid/axes.

## Analytics (6.4)
Charts read `GET /stats?server_id=&zone_id=&from=&to=&resolution=1m`. Notes:
- **`/stats` is per-server** (no network-aggregate endpoint), so **"All PoPs" fans out one query
  per server and merges client-side** (`src/lib/stats.ts` → `mergeSeries`): counts/bytes summed,
  cpu/ram/disk averaged, **hit ratio recomputed from summed hits/misses** (not averaged). Long
  ranges are `downsample`d to keep charts readable. Filters (range/PoP/zone) are URL-synced; the
  refresh cadence scales with range (`refreshMs`).
- **Known data gaps (flagged in the UI, not faked):** no status-code (2xx/4xx/5xx) breakdown, no
  geo/country, no top paths/referrers, no latency percentiles — the `stats` schema is summary-level;
  these need agent + schema work in a later phase.
- **Local test data:** no edge ships to the local control plane, so `brisk-control/_seed_stats.sql`
  seeds ~24h of synthetic stats (server 3 + zones) for chart testing. Re-run it after a DB reset:
  `docker compose exec -T timescaledb psql -U brisk -d brisk < _seed_stats.sql`.

## Logs (6.4)
Honest "coming soon" placeholder — there is no logs API yet. No fabricated log lines; the screen
explains the planned columns and gets wired up when a control-plane tail/stream endpoint ships.

## Purge (6.5)
URL / prefix / whole-zone / purge-all over the instant **NATS JetStream** channel
(`POST /zones/{id}/purge`, `POST /purge/all`), with a live job table (`GET /purge/jobs`) that polls
only while jobs are pending/partial and shows `edges_done/edges_total`. Strong confirms: whole-zone
needs an explicit ack, **purge-all is danger-styled + type-to-confirm** with an optional per-edge
scope. A zone with no live edges settles to `done` instantly; a job stays `partial`/`pending` until
every edge acks (an offline edge gets it on reconnect via JetStream durability).

## ⌘K command palette
Top-bar search → cmdk palette: jump to any screen, run quick actions (Add server/zone, Purge), or
jump to a zone/server by name. Keyboard-first.

## Auth seam
Admin routes are open locally. The single place to add the admin bearer token later is
`authHeader()` in `src/lib/api.ts` — every request flows through it; no auth logic is scattered.

## Phase-2 cleanup backlog (control-plane work for later)
These were flagged honestly in the UI rather than faked — they need backend changes:
- **Rule update / bulk-reorder endpoint** (`PUT /zones/{id}/rules/{rid}`) — today reorder is
  delete+recreate (no rule-UPDATE endpoint).
- **`GET /zones/{id}/servers` inverse lookup** — "served by" is currently derived by unioning each
  server's `/zones`.
- **Network-aggregate `/stats`** — `/stats` is per-server, so "All PoPs" merges client-side.
- **Stats schema**: status codes (2xx/4xx/5xx), geo/country, top paths/referrers, latency
  percentiles — not collected yet; Analytics omits these with a note.
- **Logs API** — no endpoint; Logs is an honest "coming soon".
- **Edge enforcement of custom cache rules** — rules are stored + versioned; the edge templates
  don't render them yet.
- **Admin auth** for the dashboard (see Auth seam above).

After Step 6.5, **Phase 2 is complete**: the dashboard manages servers, zones, rules, analytics,
and purge end-to-end against the Go control plane.
