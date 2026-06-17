# Brisk Dashboard — Design + Information-Architecture spec (MAIN deliverable)

Brisk's **own** admin dashboard, grounded in the patterns from `bunny-notes.md`,
`cloudflare-notes.md`, `design-inspiration.md`, and mapped to the **actual Go API** shipped in
Phase 2 Steps 1–5. This is the source of truth for Step 6.1+. No app code here.

**Stack (per Step 6.1 plan):** React + TypeScript + Vite + Tailwind v4 + shadcn/ui + Tremor,
in Docker. Tokens from `brisk-design-tokens.md`.

**Brand idea:** *Brisk = fast, sharp, professional, owned.* The differentiator vs Bunny/
Cloudflare is that **we run the metal** — so the dashboard is proud of its **Servers/PoPs** and
its **instant, job-tracked purge**.

---

## 0. API surface of record (from `brisk-control/internal/api/router.go`)
Admin (open locally now; admin-auth slots in later):
```
GET    /api/v1/overview
GET    /api/v1/stats?server_id=&zone_id=&from=&to=&resolution=1m|raw
GET    /api/v1/servers
POST   /api/v1/servers
GET    /api/v1/servers/{id}
DELETE /api/v1/servers/{id}
POST   /api/v1/servers/{id}/reprovision
POST   /api/v1/servers/{id}/token/rotate
GET    /api/v1/servers/{id}/provision-log
GET    /api/v1/servers/{id}/live
GET    /api/v1/servers/{id}/zones
POST   /api/v1/servers/{id}/zones
DELETE /api/v1/servers/{id}/zones/{zoneId}
GET    /api/v1/zones
POST   /api/v1/zones
GET    /api/v1/zones/{id}
PUT    /api/v1/zones/{id}
DELETE /api/v1/zones/{id}
GET    /api/v1/zones/{id}/rules
POST   /api/v1/zones/{id}/rules
DELETE /api/v1/zones/{id}/rules/{rid}
POST   /api/v1/zones/{id}/purge        # {type:url|prefix|zone, target}
POST   /api/v1/purge/all               # {server_ids?:[]}
GET    /api/v1/purge/jobs?zone_id=&limit=
GET    /health
```
Agent-only (bearer auth; not used by the dashboard): `/agent/heartbeat`, `/agent/config`,
`/agent/stats`, `/agent/purge/ack`.

Key JSON shapes (for component contracts):
- **Overview** `{online_servers, req_per_sec, bandwidth_bps, hit_ratio, window_seconds}`
- **ServerLive** `{server_id, cpu_pct, ram_pct, disk_pct, req_per_sec, bandwidth_bps, hit_ratio, last_sample}`
- **Server** `{id,name,region,ip,hostname,edge_id,capacity_mbps,status,last_seen,provisioned_at,created_at}`
- **Zone** `{id,account_id,name,cdn_hostname,custom_domain,origin_url,tls_mode,video,profile,playlist_ttl,segment_ttl,cors_origin,brotli_level,status,config_version,created_at,updated_at,rules[]}`
- **CacheRule** `{id,priority,match_type,match_value,action,action_value}`
- **Stats** `{server_id,resolution,from,to,points:[{time,requests,hits,misses,bytes_sent,bandwidth_bps,cpu_pct,ram_pct,disk_pct,hit_ratio}]}`
- **PurgeJob** `{id,account_id,zone_id,type,target,status,edges_total,edges_done,created_at,completed_at}`

---

## 1. Navigation shell (applies to every screen)
**Layout:** left **sidebar** (collapsible to icons — shadcn `sidebar-07`) + persistent **top bar**
+ main content `SidebarInset`.

- **Sidebar (top → bottom):**
  - Brand mark "Brisk" + (future) account/workspace switcher.
  - **Primary sections (6):** Overview · Servers · Zones · Analytics · Logs · Purge.
  - Reserved-but-disabled (labeled "soon"): Security, Settings.
  - User menu pinned to the bottom (avatar + name + theme/sign-out).
  - Active item = filled pill + accent left-border (subtle, not loud).
- **Top bar:** breadcrumb / page title (left); **global search ⌘K** (center, shadcn `command` —
  jump to any zone/server/screen, client-side, no new API); right cluster = **dark/light toggle**
  (saved pref), **quick-create** split button ("Add Server" / "Add Zone"), notifications (later),
  user avatar.
- **Behavior:** responsive (sidebar → drawer on mobile); keyboard nav; toasts via `sonner`
  (purge issued, zone saved, server online).

---

## 2. The six screens

### 2.1 Overview  →  `GET /api/v1/overview` (+ `GET /stats` for trend, `GET /servers` for tiles)
**Purpose:** one glance = is the network healthy and how much is it doing.
**Layout (F-pattern):**
- **Hero KPI row (4 cards, top, most critical top-left):**
  1. **Total bandwidth** (bandwidth_bps → human Gbps/MB/s) — top-left, biggest.
  2. **Requests / sec** (req_per_sec).
  3. **Global cache-hit %** (hit_ratio).
  4. **PoPs online / total** (online_servers / count of `/servers`).
  Each card: label + big tabular number + delta vs previous window + `SparkAreaChart`.
- **Main time-series (spans 2 cols):** requests + bandwidth over last hour (`/stats?resolution=1m`,
  server_id omitted = network-level; sum across PoPs). Tremor `AreaChart`, single accent + gradient.
- **Right column (1 col):** **PoP status list** (Tremor `Tracker` or a compact list from
  `/servers` + per-row status badge) and **Recent events** (server came online, purge issued,
  zone created — sourced from purge jobs + server status changes; v1 can derive from
  `/purge/jobs` + server last_seen).
- **Optional world/PoP map** (future; we have region strings on servers, not lat/long yet — flag).
**States:** skeleton KPI cards + chart shimmer; empty ("No PoPs yet — add your first server");
error banner with retry. **Refresh:** poll overview + live every ~10s.

### 2.2 Servers (PoPs)  →  `GET /servers`, `GET /servers/{id}/live`, detail + Add + provision-log
**Purpose:** Brisk's signature screen — the fleet of edges we operate.
**Layout:**
- **List as a grid of PoP tiles** (shadcn `card`): name + edge_id, region, **status badge**
  (online/offline/provisioning/pending), live **CPU / RAM / Disk** as Tremor `ProgressBar`s or
  `ProgressCircle`s (from `/servers/{id}/live`), req/s + bandwidth, last_seen. Toggle to a dense
  `data-table` view for many servers (server-side paginate/filter when the fleet grows).
- **Primary action: Add Server** (top-right) → opens a **`sheet` (drawer) wizard**:
  1. name, region, IP, edge_id, SSH user/port, capacity.
  2. `POST /servers` → returns the new server (status `pending`).
  3. **Live provision log** streams from `GET /servers/{id}/provision-log` (the SSH bootstrap
     output) in a console panel; status flips to `online` on first heartbeat. Show a one-time
     **agent token** if surfaced (copy-once, never re-shown).
- **Per-server detail** (click tile → `/servers/{id}` route): header (name/edge_id/region/IP/
  status/last_seen) + live gauges + a `/stats?server_id={id}&resolution=1m` time-series + the
  zones it serves (`/servers/{id}/zones`) + actions: **Reprovision** (`POST …/reprovision`),
  **Rotate token** (`POST …/token/rotate`), **Assign/unassign zones**
  (`POST/DELETE …/zones`), **Delete** (`DELETE /servers/{id}`, confirm dialog).
**States:** provisioning = animated badge + live log; offline = red + "last seen X ago";
empty = add-first-server CTA.

### 2.3 Zones  →  `/zones` CRUD + per-zone `rules`
**Purpose:** the sites/origins Brisk accelerates (Bunny-style "pull zones").
**Layout (shallow list + deep tabs, the Bunny pattern):**
- **List:** `data-table` of zones — name, cdn_hostname (+ custom_domain), origin_url, status,
  video badge, config_version, updated_at; row actions (edit, purge, delete). **Add Zone** button.
- **Add/Edit Zone** (`dialog` or `sheet`): name, cdn_hostname, custom_domain, origin_url,
  tls_mode (selfsigned/mkcert/letsencrypt), **video** toggle → reveals profile (vod/live),
  playlist_ttl, segment_ttl, cors_origin; brotli_level. `POST /zones` / `PUT /zones/{id}`.
- **Per-zone detail** (`/zones/{id}`): **tabbed** —
  - **Settings** (the fields above).
  - **Cache Rules** — ordered `data-table` from `/zones/{id}/rules`; add rule = matcher
    (match_type path_prefix/extension/regex + match_value) → action (override_cache_ttl/
    bypass_cache/force_download/redirect + action_value) → priority. `POST`/`DELETE` rules.
  - **Servers** — which PoPs serve this zone (assign/unassign).
  - **Analytics** — `/stats?zone_id={id}` scoped charts (reuse Analytics components).
  - **Purge** — quick purge scoped to this zone (deep-links to Purge screen with zone preset).
**States:** empty = "Add your first zone"; validation inline; "config_version bumped" toast on save.

### 2.4 Analytics  →  `GET /stats?...&resolution=1m`
**Purpose:** trends and drill-down across the network, PoPs, and zones.
**Layout:**
- **Filter bar (top):** **Date-range picker** (Tremor) → sets from/to; **resolution** (1m/raw);
  **PoP** select (`/servers`); **Zone** select (`/zones`). All map to `/stats` query params.
- **KPI row:** totals for the selected range (requests, bandwidth, hit ratio, avg req/s) derived
  from the points sum.
- **Charts (bento, priority-sized):**
  - **Requests over time** (Tremor `AreaChart`, stacked hits vs misses to show cached vs origin).
  - **Bandwidth over time** (`AreaChart`/`LineChart`).
  - **Cache HIT vs MISS** (`DonutChart`) for the range.
  - **Top PoPs / Top zones** (`BarList`) — derived from per-server/zone sums (top paths/referrers
    = future, needs logs).
  - **System** (cpu/ram/disk avg) mini-charts when a single PoP is selected.
- **Honesty:** if/when sampling is added, disclose it (Cloudflare pattern). No chart junk.
**States:** skeletons per card; "no data in range" empty; error retry.

### 2.5 Logs  →  (live request tail)  ⚠️ DATA GAP
**Purpose:** real-time request view for debugging (HIT/MISS, status, path, PoP).
**Layout:** filter bar (PoP, zone, status, free-text) + **virtualized `data-table`** streaming
newest-first; columns: time, PoP (edge_id), zone (host), method, path, status (badge), bytes,
**cache result** (HIT/MISS/BYPASS badge), response time; (country later). Pause/resume tail;
row click → detail drawer.
**API status:** **no logs endpoint exists yet.** Flag for 6.1+: add a control-plane endpoint to
tail the agent access stream (e.g. `GET /api/v1/logs?...` or a WS/SSE stream). v1 may ship the
screen with a clear "live logs require the logs endpoint (coming in 6.x)" empty state, or wire it
once the endpoint lands. **Do not invent the API in 6.0.**

### 2.6 Purge  →  `POST /zones/{id}/purge`, `POST /purge/all`, `GET /purge/jobs`
**Purpose:** Brisk's instant, job-tracked invalidation (a selling point).
**Layout:**
- **Purge panel (top):** mode picker (segmented control / `dialog`):
  - **URL** → zone select + path input → `{type:url, target}`.
  - **Prefix** → zone select + prefix input → `{type:prefix, target}`.
  - **Zone** → zone select → `{type:zone, target:"/"}`.
  - **Everything** → optional server multiselect → `POST /purge/all {server_ids?}`.
  - (Cache-tag / hostname = future, shown disabled.)
  Submit → 202 → `sonner` toast "Purge queued (job #N)".
- **Job history (`data-table` from `/purge/jobs`):** id, zone, type, target, **status**
  (pending/partial/done/failed badge), **edges_done/edges_total** progress bar, created_at,
  completed_at. Auto-refresh until done. This status trail is a Brisk advantage over competitors.
**States:** confirm dialog for Everything/zone; empty = "No purges yet"; per-row live progress.

---

## 3. Design principles baked in (from the prompt's 5b)
- **F-pattern + KPI cap:** most critical metric top-left; **3–5 primary KPIs** across the top;
  secondary content down/left. Limit to 5–7 primary numbers per screen.
- **Progressive disclosure:** summary first (Overview/KPIs), drill into Servers/Zones/Analytics/Logs.
- **Grid + spacing:** 12-col grid, **~16px gutters**, **8px spacing rhythm**; **tile size reflects
  data priority** (main time-series spans 2 cols; KPI cards 1 col each).
- **No chart junk:** muted gridlines, single accent + gradient fill, generous whitespace.
- **Large tables:** server-side pagination/filtering + **virtualization** (Logs especially).
- **Chart choice:** KPI card = single number + spark; table = precise/compare; line/area = trends;
  donut = composition (HIT/MISS); BarList = top-N; geo/heat = distribution (future).
- **Dark + light** with saved preference; **skeleton loaders**; explicit **empty/error** states;
  full **keyboard nav** + ⌘K.
- **WCAG 2.1 AA:** ARIA labels on charts/controls; DOM order = visual order; contrast ≥ 4.5:1;
  visible focus rings; status conveyed by **color + label/icon**, never color alone.

---

## 4. Role-aware: admin now, customer portal later
- **v1 = single admin** sees everything (all servers, all zones, all stats). Brisk owns the fleet,
  so **Servers** is admin-only forever (customers never see PoP internals).
- **Future customer portal** reuses the **same screens, filtered by `account_id`:** a customer
  sees only their zones, their zones' analytics/logs, and purge for their zones. They do NOT see
  Servers, other accounts' data, or fleet-wide Overview (they get an account-scoped overview).
- **Build for this now (without building the portal):** keep all data fetching **account-scopable**
  (every zone/stats/purge query can take an account filter later); keep components prop-driven so
  the same `<ZonesTable>`/`<Analytics>` render admin-wide or account-scoped; gate **Servers** +
  fleet Overview behind an `isAdmin` flag. The DB already has `account_id` on zones (default 1).
- **Auth note:** admin routes are currently open locally; real admin auth slots in later (same
  middleware pattern as agent bearer auth). The dashboard's API client should centralize auth so
  adding it is one change.

---

## 5. Decisions (locked 2026-06-08, before 6.1)
1. **Palette = Option C "Voltage" (indigo/violet)** — `brisk-design-tokens.md` (the chosen default,
   light + dark). Mockups drawn in azure are re-skinned to indigo in 6.1.
2. **Overview layout = "Calm bento"** — spacious, few elements, generous whitespace (not Dense NOC).
3. **Servers default view = Tile grid** — PoP cards with CPU/RAM/disk gauges; a Table toggle is
   secondary (for large fleets later).
4. **Logs = DEFERRED in v1** — there is no logs/tail endpoint yet; do NOT build the Logs screen in
   the first cut. Keep "Logs" in the sidebar disabled/"soon" until a logs endpoint lands in a later
   6.x. (The nav still reserves the slot.)
5. **Dark + light both**, dark-first default (saved preference).

### Still-open (not blocking 6.1; decide later)
- **Map on Overview?** Deferred until servers carry lat/long (today: region strings only).
- **Settings/Tokens screen in v1?** Minimal token management vs defer to customer-portal era.
