# PoP World Map — Design Spec

**Date:** 2026-06-15
**Status:** Approved (brainstorm)
**Scope:** Dashboard-only, front-end. No control-plane / agent / DB / migration changes.

## Goal

Show Brisk's points of presence (edges) on a vector world map, Bunny-style, in two places:

1. **Per-zone Assignments map** — interactive: stage PoP add/remove by clicking dots,
   apply with a Save button. Replaces nothing; mounts above the existing assignment list.
2. **Global Overview footprint map** — read-only network footprint with hover tooltips
   and a headline count.

Both reuse data the dashboard already has; the only write path reuses the existing,
guarded assign/unassign mutations.

## Why it's low-risk

- **All geo data already exists.** `useDnsRouting().regions` returns every region's
  `{lat, long, cc, label}` (the same map that drives DNS geo-routing); `resolveLocation()`
  already resolves an edge's region → that location. Online/assigned/health state already
  comes from `useServers`, `useHealthStatus`, and the per-zone assignment queries.
- **Only write path is the existing mutations.** Save calls `useAssignZone` /
  `useUnassignZone` (unchanged), behind the live-zone guard already in the code.
- **Self-contained basemap.** The world topology is vendored into the dashboard bundle —
  no runtime CDN fetch, no API key, no tile server.
- **Graceful failure.** If the map fails to render, the assignment list below still works.

## Tech

- **Rendering:** `d3-geo` (`geoNaturalEarth1` projection + `geoPath`) to compute SVG path
  `d` strings and to project PoP `[long, lat]` → `[x, y]`. Rendered as plain **JSX**
  `<path>` / `<circle>` / `<text>` — NO `d3-selection`, no imperative DOM, no React wrapper
  library. Keeps it idiomatic React and minimal-dep.
- **Topology:** `topojson-client` (`feature()`) over a vendored `world-110m.json`
  (Natural Earth countries, ~100 KB) imported as a static asset.
- **New deps:** `d3-geo`, `topojson-client` (+ `@types/d3-geo`, `@types/topojson-client`).
- Colors via existing theme tokens; dot status colors hard-coded hex that read in both
  light/dark (the dashboard is dark-first). Country flags via the existing `Flag` component.

### Visual style (LOCKED 2026-06-15) — "polished solid fill"
Bunny-style **solid-filled** continents, cleaned up: a muted two-tone treatment (land fill
slightly lighter than the ocean/card surface) with **hairline country borders**, NOT a dotted
or 3D-globe style. Goal: looks more premium than Bunny but unmistakably the same flat,
fast family. PoP nodes sit on top: a solid status-colored dot + a subtle expanding **pulse
ring** on serving edges (no blur/glow). Same solid map for BOTH the per-zone and global maps
(one consistent look). Compared against Bunny live (flat slate fill, plain) — the upgrade is
the cleaner two-tone land, hairline borders, live pulse nodes, and flag+city tooltip.

## Components

### `components/maps/pop-map.tsx` — the primitive
Reusable, presentational. Props:

```
PopMapPoint = {
  edgeId: string
  lat: number; long: number
  cc?: string; label?: string
  status: "assigned" | "available" | "unhealthy" | "offline" | "pending-add" | "pending-remove"
}
PopMapProps = {
  points: PopMapPoint[]
  onPointClick?: (edgeId: string) => void   // omit => read-only map
  height?: number
}
```

Behavior:
- Renders the vendored basemap (muted landmass fill, hairline borders) + one dot per point.
- Dot color by status: assigned=green, available=grey hollow, unhealthy=amber,
  offline=red ring, pending-add=green hollow, pending-remove=grey-dashed. Subtle pulse ring
  on `assigned` (live feel).
- Hover → tooltip card: `<Flag cc /> {label || region} · {edgeId} · {status}`.
- If `onPointClick` given, dots are buttons (keyboard-focusable, `aria-label`); else
  decorative (`role="img"` on the svg with a summary `<title>`/`<desc>`).
- Self-contained projection memoized from the basemap feature collection via `fitExtent`.

### `components/zones/assignments-map.tsx` — per-zone interactive
Wraps `PopMap` for one zone. Owns the **staged-changes** state:
- Seeds the desired set from the zone's current assignments (`useZoneServers(zoneId)` or the
  server→zones union already used by the Assignments tab).
- Click a dot → toggle its membership in a local `desiredSet` (Set of edgeIds), deriving each
  point's status: currently-assigned & still desired = `assigned`; assigned & toggled-off =
  `pending-remove`; available & toggled-on = `pending-add`; available & untouched = `available`.
- When `desiredSet` differs from the current set, render a sticky footer bar:
  `"{n} change(s) pending"` + **Save** + **Reset**.
- **Save** computes the diff vs. the current assignment set and fires
  `useAssignZone`/`useUnassignZone` per delta (sequentially), toasts success/fail, invalidates
  the assignment queries, clears staging. The existing live-zone confirm guard still applies.
- **Reset** clears `desiredSet` back to current.
- The existing assignment **list stays below the map** unchanged (fallback + per-row actions).

### `components/overview/network-map.tsx` — global read-only
Wraps `PopMap` read-only (no `onPointClick`). Points = all servers across the network with
status from health/rotation. Above it: a one-line stat — `"{P} PoPs · {R} regions · {C}
countries"`. A card on the Overview page.

## Data flow

```
useDnsRouting() ─┐
useServers() ────┼─► map each edge → PopMapPoint { lat,long,cc,label,status }
useHealthStatus()┘            (status from assignment membership + health/rotation)
                                  │
        ┌─────────────────────────┴──────────────────────────┐
   AssignmentsMap (per-zone)                          NetworkMap (global)
   desiredSet staging + Save                          read-only + tooltip
        │
   Save → useAssignZone / useUnassignZone (existing, guarded) → query invalidate
```

## Touched files
- New: `components/maps/pop-map.tsx`, `components/maps/world-110m.json`,
  `components/zones/assignments-map.tsx`, `components/overview/network-map.tsx`.
- Edit: `pages/zone-detail.tsx` (mount `AssignmentsMap` atop the Assignments tab),
  the Overview page (add `NetworkMap` card), `package.json` (deps).
- Possibly a tiny `lib/geo.ts` helper to map a server+region+health → `PopMapPoint.status`
  (single source of truth for status derivation, shared by both wrappers).

## Testing / acceptance
- `tsc -b` clean; dashboard builds.
- Per-zone: staging a change shows the footer; Save assigns/unassigns the right edges (verify
  via the list + `/servers/{id}/zones`); Reset clears; live-zone guard still fires.
- Global: all PoPs plotted at correct locations; tooltip shows flag + city + status; no actions.
- Map-load failure leaves the list usable (graceful).
- Dark + light mode both legible.

## Out of scope (YAGNI)
- Zoom/pan, clustering, animated routing lines, latency heatmaps, country choropleth.
  (Can come later; not needed for v1.)
- Any backend/agent change.
