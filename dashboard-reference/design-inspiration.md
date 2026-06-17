# Visual design inspiration + reusable libraries (the "professional look" source)

This is where Brisk's polish legally comes from: **inspiration galleries** (study patterns,
describe in our own words) and **licensed, reusable component libraries/templates** (build on
directly). Competitor UIs are NOT the visual source — we copy *patterns and principles*, skinned
in Brisk's own identity (see `brisk-design-tokens.md`).

---

## A. Inspiration galleries — recurring high-quality patterns
Sources to browse for "analytics / admin / SaaS / CDN dashboard": **Dribbble**, **Mobbin**
(real shipped product flows), **SaaSFrame**, **Land-book**, **Godly**. Patterns worth adopting,
in our words:

1. **Hero KPI row.** 3–5 stat cards across the top: small uppercase label, one big tabular
   number, a colored delta (▲/▼ vs previous period), and a tiny **sparkline**. This is the single
   most repeated "pro dashboard" element. Keep cards equal-width, generous padding (~20–24px).
2. **Bento grid.** Mixed-size tiles on one grid where **tile size = data priority** (the main
   time-series spans 2 columns; secondary stats are 1 column). Looks intentional, not a wall of
   equal boxes.
3. **Sidebar styles.** Slim left rail with icon + label, grouped sections, a collapsible
   icon-only mode, a workspace/account switcher pinned top, user menu pinned bottom. Active item
   gets a subtle filled pill + accent left-border, not a loud highlight.
4. **Chart treatment (the "expensive" look).** Muted/low-contrast gridlines, **single accent
   color** per series (gradient area fill at ~10–15% opacity under a 2px line), no 3D, no heavy
   borders, rounded tooltips with a small color dot + tabular numbers, axis labels in a muted gray.
   Restraint reads as premium.
5. **Dark-mode palettes.** Near-black-but-not-black background (`#0B0E14`-ish), slightly lighter
   elevated surfaces for cards, one saturated accent, text in layered grays (primary/secondary/
   muted). Borders are very low-contrast (white at ~6–10% alpha). Avoid pure `#000`/`#fff`.
6. **Density + whitespace.** Pro dashboards are *dense with data but calm*: tight numbers,
   generous gutters (16px), consistent 8px rhythm, one or two accents max, lots of neutral space.
7. **Status as color + shape.** Online/healthy = green dot; degraded = amber; offline = red;
   always pair color with a label/icon (accessibility + scannability).
8. **Microcopy + empty states.** Friendly, instructive empty states ("No servers yet — add your
   first PoP") with a single primary CTA. Skeleton shimmer while loading, never spinners-only.

---

## B. Reusable, licensed component libraries (build on these directly)
These give Cloudflare-grade polish and are **licensed for us to use** — Brisk's stack is
React + TS + Vite + Tailwind v4 + shadcn/ui + Tremor (per Step 6.1 plan).

### shadcn/ui (MIT, copy-in components — the app shell + primitives)
- **`dashboard-01` block** = the reference layout: app sidebar + site header + a section of
  **stat cards** + an **interactive area chart** + a **data table**. This is almost exactly
  Brisk's Overview/Analytics skeleton. Components it ships: `app-sidebar`, `site-header`,
  `section-cards`, `chart-area-interactive`, `data-table` (TanStack Table).
- **`sidebar-07`** = sidebar that **collapses to icons** (with `SidebarProvider`,
  `SidebarInset`, `SidebarTrigger`) — use for Brisk's collapsible left nav + team/account switcher.
- **Charts** = shadcn chart wrappers over **Recharts** (`ChartContainer`, `ChartTooltip`,
  `ChartLegend`) themeable via CSS variables — area/bar/line/pie/radar/radial.
- **Primitives Brisk will use directly:** `card`, `table`, `tabs`, `dialog`/`sheet` (modals,
  side panels — purge modal, add-server drawer), `dropdown-menu`, `command` (⌘K global search),
  `sonner` (toasts for purge/save), `skeleton` (loading), `badge` (status), `button`, `input`,
  `select`, `switch`, `tooltip`, `breadcrumb`, `avatar`, `alert`.
- **Principle captured:** shadcn is unstyled-by-tokens; we **skin it with Brisk CSS variables**
  (`--primary`, `--background`, `--radius`, …) so the same blocks become unmistakably Brisk.

### Tremor (open-source, Tailwind + Recharts + Radix — the analytics charts/KPIs)
- **35+ dashboard components**: `AreaChart`, `BarChart`, `LineChart`, `DonutChart`,
  `SparkAreaChart`/spark charts, `BarList` (top-N), `Tracker` (uptime squares), `ProgressBar`/
  `ProgressCircle`, `Callout`, KPI "Card" with delta. **300+ blocks/templates** (dashboard,
  report pages) and a **Date-Range Picker** + advanced filter inputs.
- **Why Tremor for Brisk:** it's purpose-built for *data dashboards* — beautiful defaults, simple
  `valueFormatter`/`colors` props, accessible, copy-paste OR npm. Tremor handles Analytics
  charts; shadcn handles app shell + tables + dialogs. They share Tailwind + Recharts so they
  compose. (Note: Tremor is joining Vercel; the OSS components remain usable.)
- **Patterns captured:** KPI card = label + big number + delta badge; `BarList` for top zones/
  paths; `Tracker` for per-PoP uptime; `SparkAreaChart` inside stat cards; `DonutChart` for
  cache HIT/MISS or traffic by status; consistent muted palette with one accent.

### Tailwind Plus (formerly Tailwind UI) — application-shell patterns (reference)
- Application UI patterns to mirror in structure (not copy code unless licensed): **sidebar
  layout** with constant top bar, **page headings** with action buttons + breadcrumbs, **stat
  card rows**, **dense tables** with sticky headers + row actions, **slide-over panels**, **command
  palette**. Use as a checklist of "what a complete app shell includes."

### Permissively-licensed admin templates (reference for completeness)
- **shadcn-admin** (community, MIT-style) and **TailAdmin** — full admin scaffolds (sidebar +
  topbar + charts + tables + auth pages + settings). Use as a **structural reference** for which
  screens/states a complete admin has (settings, profile, 404/error, auth), and for proven layout
  proportions. Brisk builds its own from shadcn+Tremor rather than adopting a template wholesale.

---

## C. Which reusable blocks map to which Brisk screen
| Brisk screen | Primary reusable blocks | Notes |
|---|---|---|
| **App shell / nav** | shadcn `sidebar-07` + `site-header` + `command` (⌘K) + `sonner` | collapsible sidebar, top bar, global search, toasts |
| **Overview** | shadcn `section-cards` + Tremor KPI cards + `SparkAreaChart` + `AreaChart` + `BarList` + Tremor `Tracker` | hero KPIs, mini trends, recent events, PoP uptime |
| **Servers (PoPs)** | shadcn `card` grid + `badge` (status) + Tremor `ProgressBar`/`ProgressCircle` (cpu/ram/disk) + `data-table`; `sheet` for Add-Server drawer + provision-log stream | live PoP tiles + detail |
| **Zones** | shadcn `data-table` + `dialog`/`sheet` (create/edit) + `tabs` (per-zone settings) + cache-rules `data-table` | shallow list, deep tabs |
| **Analytics** | Tremor `AreaChart`/`BarChart`/`DonutChart` + Date-Range Picker + `BarList` + shadcn `select` filters | time-series + breakdowns |
| **Logs** | shadcn `data-table` (virtualized) + `badge` (status/cache) + filter inputs | live tail |
| **Purge** | shadcn `dialog` (mode picker) + `data-table` (job history) + `badge` (job status) + `sonner` | modes + status trail |

---

## D. The visual-quality bar (how we hit "Cloudflare-grade" legitimately)
1. Build the shell from shadcn `dashboard-01`/`sidebar-07`; build analytics from Tremor.
2. Skin everything with Brisk tokens (one accent, layered neutrals, 8px rhythm, `--radius`).
3. Apply the gallery patterns: hero KPI row, bento priority sizing, muted-grid charts with single
   accent + gradient fill, near-black dark mode, status = color+label, friendly empty/skeleton states.
4. Result: a dashboard that looks as polished as the competitors but is **Brisk's own design**,
   assembled from licensed parts — zero pixels copied from Bunny/Cloudflare.
