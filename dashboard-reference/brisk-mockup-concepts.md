# Brisk mockup concepts (original, throwaway direction)

Low-/mid-fidelity **concept directions** for the **Overview** and **Servers** screens, in Brisk's
own identity (Option A "Brisk Blue", dark-first), informed by `design-inspiration.md` +
`brisk-design-spec.md`. **These are concept art for direction, not the real app** — the real build
is 6.1+ (React + shadcn + Tremor). Static HTML mockups (Tailwind via CDN, **mock data only**) live
in `mockups/overview.html` and `mockups/servers.html`.

> Goal: give you something concrete to react to and refine before we pick a direction and build.

---

## Shared shell concept (both screens)
- **Left sidebar**, dark, collapsible-to-icons feel: Brisk wordmark + lightning glyph on top;
  nav = Overview · Servers · Zones · Analytics · Logs · Purge; Security/Settings greyed "soon";
  user chip pinned bottom. Active item = soft azure pill + left accent bar.
- **Top bar:** page title + breadcrumb (left); ⌘K search field (center); right = dark/light toggle,
  "＋ Add" split button, avatar.
- **Surface system:** near-black navy bg `#0b1220`, elevated cards `#111a2b`, hairline borders at
  white/8%, one azure accent `#38bdf8`, layered gray text. Numbers in mono, tabular.

---

## Concept 1 — Overview
**Intent:** "is the network healthy + how hard is it working," in one screen.
- **Hero KPI row (4 cards):** Total Bandwidth (biggest, top-left) · Requests/sec · Cache-Hit % ·
  PoPs Online. Each = uppercase label + big mono number + green/red delta vs prev window + inline
  sparkline (azure gradient).
- **Main panel (2-col span):** "Traffic — last hour" area chart, requests with hits-vs-misses
  band, muted grid, single azure series + gradient fill, range chips (1h/24h/7d).
- **Right rail (1 col):** **PoP status** list (green/amber/red dot + name + region + req/s) and
  **Recent events** feed (purge issued, server online, zone created) with relative timestamps.
- **Bottom strip:** small "Cache HIT vs MISS" donut + "Top zones" bar list.
- **Direction variants to react to:**
  - **1A "Calm bento"** (recommended): spacious, few elements, big whitespace — premium/calm.
  - **1B "Dense NOC"**: tighter grid, more tiles, a ticker — "ops wall" feel for power users.

## Concept 2 — Servers (PoPs) — Brisk's signature screen
**Intent:** show off that **we run the metal** (Bunny/Cloudflare can't).
- **Header:** "Servers" + counts (online/total) + "＋ Add Server".
- **PoP tile grid (cards):** each tile = name + edge_id, region chip, status badge (online/
  provisioning/offline), three compact gauges **CPU / RAM / Disk** (ring or bar), req/s +
  bandwidth, "last seen" — live feel.
- **Add-Server drawer (slide-over):** short form (name/region/IP/edge_id/SSH) → then a **live
  provision-log console** streaming the bootstrap output; status flips to online on first heartbeat.
- **Per-server detail (not mocked, described):** big gauges + per-PoP traffic chart + zones served
  + actions (reprovision / rotate token / assign zones / delete).
- **Direction variants:**
  - **2A "Tile grid"** (recommended, mocked): scannable cards, great for ≤ ~24 PoPs.
  - **2B "Dense table"**: a virtualized table (status, cpu/ram/disk bars, traffic) for large fleets.

---

## How to view
Open the static mockups (rendered with mock data, no backend):
```
dashboard-reference/mockups/overview.html
dashboard-reference/mockups/servers.html
```
They're self-contained (Tailwind CDN + inline SVG sparklines). Throwaway — for direction only.

## What I'd like your reaction on
1. **Palette** — does "Brisk Blue" feel right, or try Signal (teal) / Voltage (indigo)? (tokens doc)
2. **Overview density** — Calm bento (1A) vs Dense NOC (1B)?
3. **Servers** — Tile grid (2A) vs Dense table (2B) as the default view?
4. **Dark-first** ok (with light available), or light-first?
