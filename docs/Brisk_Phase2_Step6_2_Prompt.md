# Brisk CDN — Phase 2 / Step 6.2 Build Prompt (Servers Page + Add Server)

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + the Phase‑2 Step‑1…5 prompts + `dashboard-reference/` + `docs/Brisk_Phase2_Step6_1_Prompt.md`. **Steps 1–5 + 6.0 + 6.1 are complete:** `brisk-control` exposes the full API; `brisk-dashboard` (React + TS + Vite + Tailwind v4 + shadcn‑style primitives + Recharts, **Voltage palette, dark default**) runs in Docker with the app shell, routing, theme toggle, the API client + TanStack Query, and **Overview wired to real `/overview`**. The other pages are placeholders.

> **Read `CLAUDE.md`, `dashboard-reference/brisk-design-spec.md` + `brisk-design-tokens.md`, and `docs/Brisk_Phase2_Step6_1_Prompt.md` first.** This is **Step 6.2 of Phase 2**. Build the **Servers screen + Add Server flow** only — don't touch Zones/Analytics/Logs/Purge (those are 6.3–6.5). Pass the acceptance tests and stop before 6.3.

## Step 6.2 goal (one line)
Build the **Servers (PoPs) page**: a **live tile grid** of every edge (status + CPU/RAM/disk + bandwidth + hit‑ratio, auto‑refreshing), a **per‑server detail view**, and the **Add Server** flow that registers + SSH‑provisions a new edge and **streams the provisioning log live** until it comes online — all wired to the real control plane.

## ✅ Test locally in Docker
The dashboard + `brisk-control` + TimescaleDB + NATS run locally. The real VPS edge (`BLR1‑01`) is registered and online, so the live tiles show real data. Adding a *new* server can be tested against a throwaway VPS or by re‑provisioning; the UI/flow is what's verified here.

---

## API this screen uses (already built — don't change the backend)
```
GET  /api/v1/servers                  # list: id, name, region, ip, edge_id, status, capacity_mbps, last_seen
GET  /api/v1/servers/{id}/live        # latest sample: cpu_pct, ram_pct, disk_pct, req/s, bandwidth_bps, hit_ratio
GET  /api/v1/servers/{id}             # one server (detail)
POST /api/v1/servers                  # create + provision (name, region, ip, ssh_user, ssh_password|ssh_private_key, capacity_mbps) -> returns agent token ONCE
GET  /api/v1/servers/{id}/provision-log   # provisioning output (poll or stream)
POST /api/v1/servers/{id}/token/rotate
POST /api/v1/servers/{id}/reprovision
DELETE /api/v1/servers/{id}
```
If any field the UI needs isn't returned, **flag it** rather than inventing it — but the above were all built in Steps 1–4.

## Part 1 — Servers list (live tile grid)
Per `brisk-design-spec.md` (**tile‑grid Servers**, Voltage): a responsive grid of **server cards**, one per PoP. Each tile shows:
- **Name + region + edge_id**, and a **status pill** (online = green, provisioning = amber pulse, offline = red, pending/disabled = muted). Derive offline if `last_seen` is stale (e.g. > 60s) even if status says online.
- **Live metrics** from `GET /servers/{id}/live`: CPU / RAM / disk as compact bars or radials, **bandwidth** (humanized, e.g. Mbps/Gbps), **req/s**, **cache hit‑ratio %**. Small sparkline optional (from `/stats?resolution=1m`).
- A **capacity** hint (1/10 Gbps) and a kebab menu (View detail · Reprovision · Rotate token · Delete).

**Live updates with TanStack Query polling:** use `refetchInterval` on the live queries — set it to a number of ms and the query refetches on that timer while it has an active observer (e.g. `refetchInterval: 5000` for ~5s tiles). Set **`refetchIntervalInBackground: true`** for an ops dashboard so tiles keep updating even when the tab isn't focused (polling pauses on blur by default). Keep the list query (`/servers`) on a slower interval (e.g. 15–30s); the per‑tile `/live` queries on ~5s. Show **skeletons** on first load, keep showing stale numbers while refetching (don't flash empty), and an **error/offline** badge if a tile's fetch fails.

> Polling, not WebSockets, is the right call here: `refetchInterval` makes the query refetch on a timer and is the standard way to make a dashboard feel real‑time without the complexity of managing socket lifecycles — perfect for periodic metrics. (We already chose polling for stats; SSE/WS is a later optimization if needed.)

## Part 2 — Per‑server detail view
Clicking a tile opens a detail page/route (`/servers/:id`):
- Header: name, edge_id, region, IP, status, capacity, `last_seen`, `provisioned_at`.
- **Live metrics** (bigger versions of the tile metrics, ~5s refresh).
- **Recent trend**: a Recharts area/line of CPU/bandwidth/hit‑ratio over the last hour from `GET /stats?server_id=&resolution=1m` (themed with the Voltage CSS variables, as in 6.1).
- **Actions:** Reprovision, Rotate token (show the new token **once** in a copy‑once dialog — never persisted/displayed again), Delete (confirm dialog).
- **Provisioning log** panel (see Part 3) if the server is/was provisioning.

## Part 3 — Add Server flow (the headline)
A primary **"Add Server"** button (top bar quick‑action + on the Servers page) opens a **multi‑step dialog/sheet**:
1. **Details:** name, region, IP, capacity (1/10 Gbps).
2. **SSH credentials:** `ssh_user` (default `root`), port (default 22), and either **password** or **private key** (radio toggle). Note in the UI that creds are **used once for provisioning and not stored** (matches Step 2's design). Mask the password field.
3. **Submit** → `POST /api/v1/servers`. The response returns the **agent token exactly once** → show it in a **"copy this token now"** panel with a warning it won't be shown again.
4. **Live provisioning view:** immediately stream `GET /api/v1/servers/{id}/provision-log` and render it like a terminal/timeline (TOFU host key → CP key installed → agent SFTP'd → bootstrap → nginx serving), updating until the server's status flips to **online** (poll the server row). Use TanStack Query polling that **stops when complete** — return `false` from the `refetchInterval` function once status is `online`/`failed` to clear the timer (resumes automatically if it changes). Show success (server now appears in the grid, online) or a clear failure state with the log + a **Retry/Reprovision** action.

> Use **mutations** (`useMutation`) for create/reprovision/rotate/delete, and on success **invalidate** the `servers` query so the grid updates. Validate inputs client‑side (IP format, required fields) before submitting.

## Part 4 — States & polish (per design principles)
- **Empty state** (no servers yet): a friendly panel with a big **Add Server** CTA.
- **Skeleton loaders** for the grid + detail; **error** states with retry; **offline** tiles clearly marked.
- **Accessibility:** tiles are keyboard‑focusable, status pills have text/ARIA (not color‑only), dialog traps focus, contrast ≥ 4.5:1.
- **Responsive:** grid reflows to 1‑col on mobile; the Add‑Server sheet works on small screens.
- All styling via the **Voltage tokens** + shadcn primitives; charts themed with the Voltage CSS variables (Recharts, as in 6.1).

---

## Acceptance tests (Step 6.2 definition of done — local Docker)
```bash
docker compose up --build -d
open http://localhost:5173/servers
# 1) Live grid: BLR1-01 shows as a tile with real status + CPU/RAM/disk/bandwidth/hit-ratio
# 2) Auto-refresh: generate traffic on the edge -> tile metrics update within ~5s WITHOUT a manual reload
#    (and keep updating when the tab is backgrounded)
# 3) Stale numbers persist during refetch (no empty flicker); offline edge shows an offline pill
# 4) Detail view: click the tile -> /servers/:id shows live metrics + an hourly trend chart (Voltage-themed)
# 5) Add Server: open the dialog -> enter details + SSH creds -> submit
#    -> token shown ONCE with a "won't be shown again" warning
#    -> provision-log streams live (TOFU -> CP key -> SFTP -> bootstrap -> serving)
#    -> on completion the new server appears in the grid as online; polling stops
#    (test against a throwaway VPS, or reprovision BLR1-01 to exercise the flow)
# 6) Actions: Rotate token (shows new token once; old invalidated), Reprovision, Delete (confirm) all work and the grid updates
# 7) Empty/skeleton/error/offline states all render correctly
# 8) Responsive + keyboard nav + dark/light both look right
npm run build      # type-check + prod build pass
```
**Done when:** the Servers page shows a **live, auto‑refreshing tile grid** of real PoP metrics, the **detail view** shows live metrics + an hourly trend, and **Add Server** registers + SSH‑provisions a new edge with a **live‑streaming provisioning log** that ends with the server online — all in the Voltage design, with proper empty/skeleton/error/offline states, verified locally in Docker.

---

## Pitfalls (do not skip)
1. **Poll, don't flash** — use `refetchInterval` (~5s tiles, ~15–30s list) with `refetchIntervalInBackground: true`; keep showing stale data while refetching so tiles don't blink empty.
2. **Stop provisioning polls when done** — return `false` from the `refetchInterval` function once status is `online`/`failed`, or you'll poll the log forever.
3. **Token shown once** — display the agent token only in the create/rotate response; never store it in app state beyond the dialog, never refetch/display it again.
4. **Don't store SSH creds** — they go straight into the create request; don't keep them in state after submit; mask the password field.
5. **Derive offline from `last_seen`** — don't trust a stale `online` status; mark a tile offline if no heartbeat recently.
6. **Invalidate queries on mutation** — after create/delete/reprovision, invalidate `servers` so the grid reflects reality.
7. **Charts themed with Voltage CSS variables** (Recharts) — match 6.1; don't reintroduce a clashing chart theme.
8. **Backend is frozen for this step** — use the existing endpoints; if data is missing, flag the gap, don't invent API or change the control plane.
9. **Scope** — Servers only. Resist building Zones/Analytics/Logs/Purge here.

## Next — Step 6.3 (do NOT start)
**Zones page:** list/create/edit zones (`/api/v1/zones`), origin + TLS + video settings, and **cache/edge rules** (`/api/v1/zones/{id}/rules`), with changes propagating to the edge via Step‑3 pull‑config. Wait for the user's go‑ahead and a Step 6.3 prompt.
