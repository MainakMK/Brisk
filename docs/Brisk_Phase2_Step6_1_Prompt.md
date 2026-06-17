# Brisk CDN — Phase 2 / Step 6.1 Build Prompt (Frontend Skeleton + Design System)

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + the Phase‑2 Step‑1…5 prompts + **`dashboard-reference/`** (from Step 6.0: `brisk-design-spec.md`, `brisk-design-tokens.md`, `design-inspiration.md`, `brisk-mockup-concepts.md`, `feature-comparison.md`). **Phase 2 Steps 1–5 + 6.0 are complete:** `brisk-control` (Go + chi + pgx + TimescaleDB + NATS) exposes the full API (`/overview`, `/servers`, `/servers/{id}/live`, `/servers/{id}/provision-log`, `/zones`, `/zones/{id}/rules`, `/stats`, `/zones/{id}/purge`, `/purge/all`, `/purge/jobs`, `/agent/*`). The design blueprint is locked: **palette "Voltage" (indigo), dark‑first**, calm‑bento Overview, tile‑grid Servers, shadcn `dashboard-01`/`sidebar-07` + Tremor as building blocks.

> **Read `CLAUDE.md`, `docs/Brisk_Phase1_Build_Spec.md`, and the whole `dashboard-reference/` folder first** — the design spec, tokens, and mockups are the **source of truth** for this build. This is **Step 6.1 of Phase 2**. Build the **skeleton + design system only**; the individual screens get filled in across 6.2–6.5. Pass the acceptance tests and stop before 6.2.

## Step 6.1 goal (one line)
Scaffold **`brisk-dashboard`** — React + TypeScript + Vite + Tailwind v4 + shadcn/ui + Tremor in Docker — apply the **Brisk "Voltage" design tokens (dark‑first)**, build the **app shell** (sidebar + top bar + routing + dark/light), wire the **API client + TanStack Query**, and stand up **empty/placeholder pages** for all screens, with **Overview fetching real `/overview` data** to prove the data path end‑to‑end.

## ✅ Test locally in Docker
Docker Desktop is installed. The dashboard runs locally and talks to the already‑running `brisk-control` on the laptop. No VPS/cost needed for this step.

---

## Part 1 — Scaffold the project (current 2026 setup)

Create `brisk-dashboard/`:
```bash
npm create vite@latest brisk-dashboard -- --template react-ts
cd brisk-dashboard && npm install
npm install tailwindcss @tailwindcss/vite        # Tailwind v4 + official Vite plugin
npm install -D @types/node
```
`vite.config.ts` — add the Tailwind plugin + the `@` alias:
```ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "path";
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": path.resolve(__dirname, "./src") } },
  server: { host: true, port: 5173 },   // host:true so it works inside Docker
});
```
Add the `@/*` path alias to **both** `tsconfig.json` and `tsconfig.app.json` (`baseUrl: "."`, `paths: { "@/*": ["./src/*"] }`) — shadcn checks the root tsconfig. Import Tailwind v4 in `src/index.css` (`@import "tailwindcss";`).

Init shadcn/ui and add the base components used by the shell:
```bash
npx shadcn@latest init      # creates components.json + CSS variables; pick the Vite/TS setup
npx shadcn@latest add button card table badge input dropdown-menu dialog sheet sidebar tabs sonner skeleton tooltip avatar
```
Charts: use **Tremor** (chosen in 6.0; open‑source MIT, Recharts‑based) and/or shadcn's chart components — **keep ONE unified token system**: if you use the Tremor npm package, map its theme to the Voltage tokens; Tremor Raw (copy‑paste, shadcn‑aligned) is the cleanest fit with shadcn + Tailwind v4. Don't let two competing theme systems clash.

Data layer:
```bash
npm install @tanstack/react-query        # v5 — server-state/data fetching + caching + refetch
npm install react-router-dom             # v7 — routing (or TanStack Router; React Router is the safe default)
```

## Part 1.5 — The user's HTML design references (DIRECTION, not a pixel‑port)
The user provides **two of their own dashboard HTML references**; place them in `dashboard-reference/inspiration-html/` and open them for design direction:
- **`dashboard-v4.html`** — a clean, compact **light‑theme SaaS** dashboard (Plus Jakarta Sans, indigo accent, a ~216px **grouped sidebar with an active‑item accent bar**, soft‑shadow KPI/stat cards, tidy tables, small badges, tight ~13px density). **Learn from it:** the overall **layout density + spacing rhythm**, the **grouped‑sidebar + active‑accent** nav pattern, the **KPI/stat card** structure, table styling, and the crisp professional feel.
- **`legacy-dashboard.html`** — a Material‑style dashboard with a **full light + dark theme token system** (tiered surface colors) and a **CDN/media‑flavoured information architecture** (Overview, Analytics, Bandwidth, Egress, Storage, Domain/Security/Account settings, Audit Log). **Learn from it:** the **dark‑mode token approach** and the **settings/section organisation** that's genuinely relevant to a CDN dashboard.

**Rules for these references:**
- Treat them as **direction only** — study layout structure, spacing, component patterns, and the light/dark approach, then **rebuild in Brisk's Voltage tokens + shadcn/ui + Tremor**.
- **Do NOT** port their CSS/markup wholesale, copy their fonts/colors/branding (e.g. "WP Sync Pro", "The Archive", Plus Jakarta/DM Sans, their indigo/Material palettes), or reproduce them pixel‑for‑pixel. Brisk keeps its **own Voltage identity** — these only inform good structure.
- **"Don't take all things"** (user's words): borrow the patterns that fit Brisk's 6 screens; ignore sections that aren't in Brisk's scope (e.g. storage/player settings). Where the two references conflict, the **`brisk-design-spec.md` + Voltage tokens win**.

## Part 2 — Apply the Brisk design system (Voltage, dark‑first)

From `dashboard-reference/brisk-design-tokens.md`, write the **Voltage** palette into the Tailwind v4 CSS variables in `src/index.css` (the shadcn theme block), for **light and dark**, with **dark as the default**. Set `--color-primary` (indigo Voltage accent), background/foreground, card, border, muted, ring, `--radius`, etc. Define the typography scale, the **8px spacing scale**, and radii here so shadcn + Tremor both inherit them. This is where Brisk's identity lives — match the spec, don't import competitor colors.

Implement a **theme provider** with a dark/light toggle (default dark) persisted in memory/localStorage‑equivalent within the app's own state (a context + class toggle on `<html>`).

## Part 3 — App shell (from `brisk-design-spec.md`)
Build the persistent layout:
- **Left sidebar** (shadcn `sidebar`): nav items for **Overview · Servers · Zones · Analytics · Logs · Purge · Settings**, with icons (lucide‑react), active‑route highlight, collapsible. Brisk logo/wordmark at top (text is fine for now).
- **Top bar:** account/area on the right, a **global search / ⌘K command palette** placeholder, the **dark‑mode toggle**, and quick‑action buttons (**Add Server**, **Add Zone**) that route to the relevant pages (wired in later sub‑steps).
- **Routing** (React Router v7): a layout route wrapping all pages; each nav item routes to its page; a 404 route.
- Responsive: sidebar collapses to a sheet on small screens.

## Part 4 — API client + TanStack Query
- **API base URL** from an env var: `VITE_API_URL` (e.g. `http://localhost:8080`). `.env.example` documents it.
- A small **typed fetch client** (`src/lib/api.ts`): wraps `fetch`, sets JSON headers, base URL, and an **auth hook** — admin routes are open locally now (per 6.0), but structure the client so a real **admin bearer token** slots in later (a single place to add the `Authorization` header). Centralized error handling → throw typed errors.
- **TanStack Query** provider at the app root (`QueryClientProvider`) with sensible defaults (staleTime, retry, and **`refetchInterval`** for live data like `/overview` and `/servers/{id}/live`). Define typed query hooks (e.g. `useOverview()`).
- **CORS:** `brisk-control` already enables permissive dev CORS (Step 1); confirm the dashboard origin (`http://localhost:5173`) is allowed. If not, note the one‑line control‑plane CORS addition.

## Part 5 — Pages (skeleton + ONE real data path)
Create a page component for every nav item, all routed and showing proper **skeleton/empty states** (no fake data dumped in):
- **Overview** — **wire this one for real**: call `GET /api/v1/overview` via a `useOverview()` query and render the hero KPI cards (total bandwidth, req/s, global cache‑hit %, PoPs online/total) with real values + skeleton while loading + an error state. This proves the whole data path (Docker → API → Query → UI).
- **Servers, Zones, Analytics, Purge** — placeholder pages with the intended layout sketched (cards/table/chart shells + "coming in 6.x" empty state). No live data yet.
- **Logs** — **deferred** (no Logs API yet, per 6.0): reserve the nav slot, show a clear "Logs — coming soon" empty state.
- **Settings** — minimal placeholder (account + API tokens section stub). Reserve the slot; real content later. *(Resolves the 6.0 open question: keep a minimal Settings slot in v1, content TBD.)*

Use shadcn components for shells (Card, Table, Tabs, Skeleton) and a Tremor chart on the Analytics placeholder (with mock data clearly marked) to prove charts render with the Voltage theme.

## Part 6 — Docker
Add the dashboard to the stack so the whole system runs locally with one command:
- **Dev:** a `Dockerfile.dev` (node:22‑alpine) running `vite --host`, port 5173; mount source for HMR. Add a `brisk-dashboard` service to `docker-compose.yml` (alongside `brisk-control`, `timescaledb`, `nats`), with `VITE_API_URL` pointing at the control plane.
- **Prod (define, don't deploy):** a multi‑stage `Dockerfile` (build with node → serve the static `dist/` via a tiny static server / nginx). Document it; we use it at deploy time.

---

## Acceptance tests (Step 6.1 definition of done — local Docker)
```bash
docker compose up --build -d        # brisk-control + timescaledb + nats + brisk-dashboard
# 1) Dashboard builds + loads
open http://localhost:5173          # app shell renders: sidebar + top bar
# 2) Navigation works
#    clicking Overview/Servers/Zones/Analytics/Logs/Purge/Settings routes correctly; active item highlighted; 404 route works
# 3) Brisk theme + dark/light
#    Voltage palette visible; dark mode is the default; toggle switches to light and persists
# 4) Real data path (the key test)
#    Overview shows REAL KPIs from GET /api/v1/overview (online PoPs, bandwidth, req/s, hit ratio); skeleton while loading; error state if control plane is down
# 5) Components render
#    shadcn components (Card/Button/Table/Skeleton) and a Tremor chart render correctly with Voltage tokens
# 6) Responsive
#    sidebar collapses to a sheet on a narrow viewport
# 7) Build passes
npm run build                       # type-check + production build succeed, no errors
```
**Done when:** the dashboard runs in Docker, the app shell + routing work for all nav items, the **Voltage dark‑first theme** is applied via tokens, shadcn + Tremor render cleanly, and the **Overview page shows real `/overview` data** through the API client + TanStack Query — proving the full Docker→API→UI path. No screen is fully built yet beyond Overview's KPIs; that's 6.2+.

---

## Pitfalls (do not skip)
1. **Tailwind v4 setup** — use the `@tailwindcss/vite` plugin + `@import "tailwindcss";` (not the old v3 three‑line/PostCSS dance). shadcn writes CSS variables, not a big `tailwind.config.js`.
2. **`@` alias in BOTH tsconfig files + vite.config** — or shadcn init / imports fail ("No import alias found").
3. **One unified token system** — Voltage tokens drive shadcn AND Tremor; don't let Tremor's default theme fight the Voltage palette.
4. **`server.host: true`** in Vite so HMR works inside the Docker container; expose port 5173.
5. **CORS** — ensure `brisk-control` allows `http://localhost:5173`; otherwise the Overview fetch fails.
6. **No fake data masquerading as real** — Overview uses the real endpoint; other pages show honest empty/skeleton states, not hardcoded numbers.
7. **Auth structured for later** — admin routes open locally now, but the API client must have a single place to add the admin bearer token (don't scatter auth logic).
8. **Build from `dashboard-reference/`** — the spec/tokens/mockups are the source of truth; the user's two HTML files in `inspiration-html/` are **direction only** (patterns/structure, not their CSS/colors/fonts/branding). The look must match the locked **Voltage/dark‑first** decisions, not the references' palettes.
9. **No business logic yet** — this is the shell; resist building Servers/Zones/Analytics/Purge functionality (that's 6.2–6.5).

## Next — Step 6.2 (do NOT start)
**Servers page:** the live PoP tiles (CPU/RAM/disk, bandwidth, hit‑ratio from `/servers` + `/servers/{id}/live`), per‑server detail, and the **Add Server** flow (`POST /servers` + stream `/servers/{id}/provision-log`) — wired to the real control plane. Wait for the user's go‑ahead and a Step 6.2 prompt.
