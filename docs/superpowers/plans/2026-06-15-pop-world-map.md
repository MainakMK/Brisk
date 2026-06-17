# PoP World Map Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a polished, solid-fill world map of Brisk's points of presence to the dashboard — interactive (stage + Save) on the per-zone Assignments tab, read-only on the Overview page.

**Architecture:** Pure front-end, additive. A presentational `<PopMap>` primitive renders a vendored TopoJSON world via `d3-geo` (projection + path) as plain JSX `<path>`/`<circle>` (no d3-selection). Two wrappers feed it points: `AssignmentsMap` (per-zone, stages add/remove in local state, applies the diff via the existing `useAssignZone`/`useUnassignZone` mutations behind a Save button) and `NetworkMap` (global, read-only). All geo + status data already exists client-side.

**Tech Stack:** React + TypeScript + Vite + Tailwind, `d3-geo`, `topojson-client`, vendored `world-110m.json` (Natural Earth).

**Environment notes (this repo):**
- **Not a git repo** → no `git commit` steps. Each "Checkpoint" = run `tsc -b` clean before moving on.
- **No front-end unit-test runner** (no vitest) → verification is `tsc -b` + dashboard build + a live visual check in the running dashboard (`http://localhost:5173`). Keep logic in small pure functions so it's obviously correct and future-testable.
- Build/typecheck command (run from repo root):
  `docker exec brisk-control-brisk-dashboard-1 npx tsc -b`
- After source changes, the Vite dev server hot-reloads; if a brand-new file isn't picked up, `docker restart brisk-control-brisk-dashboard-1` (known bind-mount cache quirk).

---

## File structure

| File | Responsibility |
|---|---|
| `brisk-dashboard/src/components/maps/world-110m.json` (new) | Vendored Natural Earth countries TopoJSON (~100 KB). Static import. |
| `brisk-dashboard/src/lib/geo.ts` (modify) | Add `PopStatus`, `popBaseStatus()`, `pointsFromServers()` — pure status/point derivation shared by both wrappers. |
| `brisk-dashboard/src/components/maps/pop-map.tsx` (new) | Presentational `<PopMap>`: renders basemap + status dots + hover tooltip; optional `onPointClick`. |
| `brisk-dashboard/src/components/zones/assignments-map.tsx` (new) | Per-zone interactive wrapper: staging set, pending diff, sticky Save/Reset footer, applies via existing mutations. |
| `brisk-dashboard/src/components/overview/network-map.tsx` (new) | Global read-only wrapper + headline count card. |
| `brisk-dashboard/src/pages/zone-detail.tsx` (modify) | Mount `<AssignmentsMap>` at the top of the Assignments tab, above the existing list. |
| `brisk-dashboard/src/pages/overview.tsx` (modify) | Add the `<NetworkMap>` card. |
| `brisk-dashboard/package.json` (modify) | Add deps `d3-geo`, `topojson-client`, devDeps `@types/d3-geo`, `@types/topojson-client`. |

---

## Task 1: Dependencies + vendored basemap

**Files:**
- Modify: `brisk-dashboard/package.json`
- Create: `brisk-dashboard/src/components/maps/world-110m.json`

- [ ] **Step 1: Install deps in the dashboard container**

Run (repo root):
```
docker exec brisk-control-brisk-dashboard-1 npm i d3-geo topojson-client
docker exec brisk-control-brisk-dashboard-1 npm i -D @types/d3-geo @types/topojson-client
```
Expected: `package.json` gains `d3-geo`, `topojson-client` (deps) and the two `@types/*` (devDeps).

- [ ] **Step 2: Vendor the world topology**

Run (repo root, PowerShell):
```
Invoke-WebRequest -UseBasicParsing -Uri "https://cdn.jsdelivr.net/npm/world-atlas@2/countries-110m.json" -OutFile "D:\Webapps\Brisk\brisk-dashboard\src\components\maps\world-110m.json"
```
Expected: a ~100 KB JSON file with `{"type":"Topology","objects":{"countries":...}}`.

- [ ] **Step 3: Confirm TS JSON import is allowed**

Check `brisk-dashboard/tsconfig*.json` for `"resolveJsonModule": true`. If absent, add it to the app tsconfig `compilerOptions`. (Vite already resolves JSON at runtime; this is for `tsc`.)

- [ ] **Step 4: Checkpoint**

Run: `docker exec brisk-control-brisk-dashboard-1 npx tsc -b`
Expected: exit 0 (no new errors).

---

## Task 2: Geo helpers (pure status + point derivation)

**Files:**
- Modify: `brisk-dashboard/src/lib/geo.ts`

Context: `geo.ts` already exports `resolveLocation(region, regions)` → `{ cc, label, lat, long } | null`. Add status derivation + a point builder so both map wrappers share one source of truth. Reuse existing types: `Server` (`lib/types.ts`), the region type used by `resolveLocation`, and the health/rotation state from `useHealthStatus`/`indexByEdge` (`hooks/use-health.ts`).

- [ ] **Step 1: Add the status type + helpers to `geo.ts`**

```ts
import type { Server } from "@/lib/types";
// reuse whatever region type resolveLocation already accepts (e.g. DnsRegion from lib/types)

export type PopStatus =
  | "assigned"        // serving this zone, online & healthy
  | "available"       // online, not serving this zone
  | "unhealthy"       // assigned/online but health-failed or draining
  | "offline"         // no fresh heartbeat
  | "pending-add"     // staged to add (per-zone map only)
  | "pending-remove"; // staged to remove (per-zone map only)

export interface PopPoint {
  edgeId: string;
  lat: number;
  long: number;
  cc?: string;
  label?: string;
  status: PopStatus;
}

/** Base status from a server's online/health/drain state, independent of any zone. */
export function popBaseStatus(opts: {
  online: boolean;
  inRotation?: boolean; // from health edge.in_rotation
  drained?: boolean;
}): "ok" | "offline" | "unhealthy" {
  if (!opts.online) return "offline";
  if (opts.drained || opts.inRotation === false) return "unhealthy";
  return "ok";
}

/**
 * Build map points for a set of servers. `assignedEdgeIds` (present => per-zone map)
 * marks which edges serve the current zone; `pendingAdd`/`pendingRemove` override status
 * for staged edges. Without `assignedEdgeIds` (global map) every serving-capable edge
 * gets the "assigned" tone.
 */
export function pointsFromServers(args: {
  servers: Server[];
  regions: unknown[] | undefined; // pass routing.data?.regions; resolveLocation accepts it
  edgeState: Record<string, { online: boolean; inRotation?: boolean; drained?: boolean }>;
  assignedEdgeIds?: Set<string>;
  pendingAdd?: Set<string>;
  pendingRemove?: Set<string>;
}): PopPoint[] {
  const out: PopPoint[] = [];
  for (const s of args.servers) {
    const loc = resolveLocation(s.region, args.regions as never); // existing fn — DO NOT redefine
    if (!loc) continue; // unmapped region -> skip on the map (still in the list)
    const st = args.edgeState[s.edge_id] ?? { online: false };
    let status: PopStatus;
    if (args.pendingAdd?.has(s.edge_id)) status = "pending-add";
    else if (args.pendingRemove?.has(s.edge_id)) status = "pending-remove";
    else if (args.assignedEdgeIds) {
      const base = popBaseStatus(st);
      status = args.assignedEdgeIds.has(s.edge_id) ? (base === "ok" ? "assigned" : base) : "available";
    } else {
      const base = popBaseStatus(st);
      status = base === "ok" ? "assigned" : base;
    }
    out.push({ edgeId: s.edge_id, lat: loc.lat, long: loc.long, cc: loc.cc, label: loc.label, status });
  }
  return out;
}
```

Note: adjust the region type + `resolveLocation` call to match what already exists. Do not redefine `resolveLocation`.

- [ ] **Step 2: Checkpoint** — `docker exec brisk-control-brisk-dashboard-1 npx tsc -b` → exit 0.

---

## Task 3: `<PopMap>` primitive

**Files:**
- Create: `brisk-dashboard/src/components/maps/pop-map.tsx`

- [ ] **Step 1: Write the component**

```tsx
import * as React from "react";
import { geoNaturalEarth1, geoPath } from "d3-geo";
import { feature } from "topojson-client";
import worldTopo from "@/components/maps/world-110m.json";
import { Flag } from "@/components/ui/flag";
import type { PopPoint, PopStatus } from "@/lib/geo";

const W = 720;
const H = 360;

const DOT: Record<PopStatus, { fill: string; stroke: string; dashed?: boolean; pulse?: boolean }> = {
  assigned:         { fill: "var(--success)",  stroke: "var(--success)", pulse: true },
  "pending-add":    { fill: "transparent",     stroke: "var(--success)" },
  available:        { fill: "transparent",     stroke: "var(--muted-foreground)" },
  "pending-remove": { fill: "transparent",     stroke: "var(--muted-foreground)", dashed: true },
  unhealthy:        { fill: "var(--warning)",  stroke: "var(--warning)" },
  offline:          { fill: "transparent",     stroke: "var(--danger)" },
};

export function PopMap({
  points,
  onPointClick,
  height = 320,
}: {
  points: PopPoint[];
  onPointClick?: (edgeId: string) => void;
  height?: number;
}) {
  const [hover, setHover] = React.useState<{ p: PopPoint; x: number; y: number } | null>(null);

  const { land, project } = React.useMemo(() => {
    // @ts-expect-error vendored topojson object typing
    const fc = feature(worldTopo, worldTopo.objects.countries) as GeoJSON.FeatureCollection;
    const proj = geoNaturalEarth1().fitExtent([[14, 14], [W - 14, H - 14]], fc as never);
    const path = geoPath(proj);
    const land = (fc.features as never[]).map((f) => path(f as never) ?? "");
    return { land, project: (lo: number, la: number) => proj([lo, la]) };
  }, []);

  const interactive = !!onPointClick;

  return (
    <div style={{ position: "relative", width: "100%" }}>
      <svg viewBox={`0 0 ${W} ${H}`} width="100%" height={height} role="img"
        aria-label={`World map with ${points.length} Brisk points of presence`} style={{ display: "block" }}>
        <title>Brisk points of presence</title>
        <g>
          {land.map((d, i) => (
            <path key={i} d={d} style={{ fill: "var(--muted)", stroke: "var(--border)" }} strokeWidth={0.5} />
          ))}
        </g>
        {points.map((pt) => {
          const xy = project(pt.long, pt.lat);
          if (!xy) return null;
          const [x, y] = xy;
          const s = DOT[pt.status];
          return (
            <g key={pt.edgeId} transform={`translate(${x},${y})`}
              style={{ cursor: interactive ? "pointer" : "default" }}
              tabIndex={interactive ? 0 : -1} role={interactive ? "button" : undefined}
              aria-label={interactive ? `${pt.label ?? pt.edgeId} — ${pt.status}` : undefined}
              onMouseEnter={() => setHover({ p: pt, x, y })}
              onMouseLeave={() => setHover(null)}
              onClick={interactive ? () => onPointClick!(pt.edgeId) : undefined}
              onKeyDown={interactive ? (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onPointClick!(pt.edgeId); } } : undefined}>
              {s.pulse && (
                <circle r={5} fill="none" stroke={s.stroke} strokeWidth={1.4} opacity={0.55}>
                  <animate attributeName="r" values="5;13;5" dur="2.6s" repeatCount="indefinite" />
                  <animate attributeName="opacity" values="0.55;0;0.55" dur="2.6s" repeatCount="indefinite" />
                </circle>
              )}
              <circle r={5} style={{ fill: s.fill, stroke: s.stroke }} strokeWidth={1.6}
                strokeDasharray={s.dashed ? "2 2" : undefined} />
            </g>
          );
        })}
      </svg>
      {hover && (
        <div style={{ position: "absolute", left: `${(hover.x / W) * 100}%`,
          top: `${(hover.y / H) * height + 14}px`, transform: "translateX(-50%)", pointerEvents: "none",
          background: "var(--popover, var(--card))", color: "var(--popover-foreground, var(--foreground))",
          border: "0.5px solid var(--border)", borderRadius: 8, padding: "6px 9px", fontSize: 12,
          whiteSpace: "nowrap", zIndex: 10 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 6, marginBottom: 2 }}>
            {hover.p.cc && <Flag cc={hover.p.cc} />}
            <span style={{ fontWeight: 500 }}>{hover.p.label ?? hover.p.edgeId}</span>
          </div>
          <div style={{ opacity: 0.7 }}>{hover.p.edgeId} · {hover.p.status}</div>
        </div>
      )}
    </div>
  );
}
```

Implementer notes:
- Confirm theme CSS var names against `src/index.css` / Voltage tokens (`--muted`, `--border`, `--success`, `--warning`, `--danger`, `--muted-foreground`, `--card`, `--foreground`). If a name differs, use the real one — never hardcode hex.
- Confirm `Flag` import path + prop (`cc`) against `server-tile.tsx`.
- If `tsc` dislikes the topojson `feature()` typing, the `@ts-expect-error` covers it.

- [ ] **Step 2: Checkpoint** — `tsc -b` → exit 0.

---

## Task 4: `<AssignmentsMap>` — per-zone interactive (stage + Save)

**Files:**
- Create: `brisk-dashboard/src/components/zones/assignments-map.tsx`

Context: reuse existing hooks — `useServers()`, `useDnsRouting()` (regions), `useHealthStatus()` + `indexByEdge()`, `useZoneServers(zoneId)` (current assignment set), and mutations `useAssignZone()` / `useUnassignZone()`.

- [ ] **Step 1: Write the component**

```tsx
import * as React from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { PopMap } from "@/components/maps/pop-map";
import { useServers } from "@/hooks/use-servers";
import { useDnsRouting, useHealthStatus, indexByEdge } from "@/hooks/use-health";
import { useZoneServers, useAssignZone, useUnassignZone } from "@/hooks/use-zones";
import { pointsFromServers } from "@/lib/geo";
import type { Server } from "@/lib/types";

export function AssignmentsMap({ zoneId, isLiveZone }: { zoneId: number; isLiveZone: boolean }) {
  const servers = useServers();
  const routing = useDnsRouting();
  const health = useHealthStatus();
  const zoneServers = useZoneServers(zoneId);
  const assign = useAssignZone();
  const unassign = useUnassignZone();

  const byEdge = indexByEdge(health.data?.edges);
  const current = React.useMemo(
    () => new Set((zoneServers.data ?? []).map((s: Server) => s.edge_id)), [zoneServers.data]);
  const [desired, setDesired] = React.useState<Set<string>>(current);
  React.useEffect(() => setDesired(current), [current]);

  const idToServerId = React.useMemo(() => {
    const m: Record<string, number> = {};
    for (const s of servers.data ?? []) m[s.edge_id] = s.id;
    return m;
  }, [servers.data]);

  const edgeState = React.useMemo(() => {
    const m: Record<string, { online: boolean; inRotation?: boolean; drained?: boolean }> = {};
    for (const s of servers.data ?? []) {
      const e = byEdge[s.edge_id];
      m[s.edge_id] = { online: (s.status ?? "").toLowerCase() === "online", inRotation: e?.in_rotation, drained: s.drained };
    }
    return m;
  }, [servers.data, byEdge]);

  const pendingAdd = React.useMemo(() => new Set([...desired].filter((id) => !current.has(id))), [desired, current]);
  const pendingRemove = React.useMemo(() => new Set([...current].filter((id) => !desired.has(id))), [desired, current]);
  const dirty = pendingAdd.size + pendingRemove.size;

  const points = pointsFromServers({
    servers: servers.data ?? [], regions: routing.data?.regions, edgeState,
    assignedEdgeIds: desired, pendingAdd, pendingRemove,
  });

  const toggle = (edgeId: string) =>
    setDesired((prev) => { const next = new Set(prev); next.has(edgeId) ? next.delete(edgeId) : next.add(edgeId); return next; });

  const save = async () => {
    if (isLiveZone && pendingRemove.size > 0) {
      if (!window.confirm(`Remove ${pendingRemove.size} PoP(s) from this LIVE zone? Traffic re-routes within ~15s.`)) return;
    }
    try {
      for (const edgeId of pendingAdd) { const sid = idToServerId[edgeId]; if (sid != null) await assign.mutateAsync({ serverId: sid, zoneId }); }
      for (const edgeId of pendingRemove) { const sid = idToServerId[edgeId]; if (sid != null) await unassign.mutateAsync({ serverId: sid, zoneId }); }
      toast.success("Assignments saved", { description: "Edges re-pull within ~15s." });
      zoneServers.refetch?.();
    } catch (e) { toast.error("Save failed", { description: (e as Error).message }); }
  };

  const saving = assign.isPending || unassign.isPending;

  return (
    <div className="rounded-lg border border-border bg-card">
      <div className="border-b border-border px-4 py-2 text-sm text-muted-foreground">
        Click a PoP to stage it, then Save. Grey = available, green = serving.
      </div>
      <PopMap points={points} onPointClick={toggle} />
      {dirty > 0 && (
        <div className="flex items-center justify-between gap-3 border-t border-border px-4 py-2">
          <span className="text-sm">
            {dirty} change{dirty > 1 ? "s" : ""} pending
            {pendingAdd.size ? ` · +${pendingAdd.size}` : ""}{pendingRemove.size ? ` · −${pendingRemove.size}` : ""}
          </span>
          <span className="flex gap-2">
            <Button variant="ghost" size="sm" onClick={() => setDesired(current)} disabled={saving}>Reset</Button>
            <Button size="sm" onClick={save} disabled={saving}>{saving ? "Saving…" : "Save"}</Button>
          </span>
        </div>
      )}
    </div>
  );
}
```

Notes: verify the `useAssignZone`/`useUnassignZone` mutation arg shape (`{ serverId, zoneId }`) against `hooks/use-zones.ts`; verify `useZoneServers` returns `Server[]` with `refetch`; if `Server.drained` is absent, drop it from `edgeState`.

- [ ] **Step 2: Checkpoint** — `tsc -b` → exit 0.

---

## Task 5: `<NetworkMap>` — global read-only

**Files:**
- Create: `brisk-dashboard/src/components/overview/network-map.tsx`

- [ ] **Step 1: Write the component**

```tsx
import * as React from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { PopMap } from "@/components/maps/pop-map";
import { useServers } from "@/hooks/use-servers";
import { useDnsRouting, useHealthStatus, indexByEdge } from "@/hooks/use-health";
import { pointsFromServers } from "@/lib/geo";

export function NetworkMap() {
  const servers = useServers();
  const routing = useDnsRouting();
  const health = useHealthStatus();
  const byEdge = indexByEdge(health.data?.edges);

  const edgeState = React.useMemo(() => {
    const m: Record<string, { online: boolean; inRotation?: boolean; drained?: boolean }> = {};
    for (const s of servers.data ?? []) {
      const e = byEdge[s.edge_id];
      m[s.edge_id] = { online: (s.status ?? "").toLowerCase() === "online", inRotation: e?.in_rotation, drained: s.drained };
    }
    return m;
  }, [servers.data, byEdge]);

  const points = pointsFromServers({ servers: servers.data ?? [], regions: routing.data?.regions, edgeState });
  const regionsCount = new Set(points.map((p) => p.label)).size;
  const countriesCount = new Set(points.map((p) => p.cc).filter(Boolean)).size;

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between">
        <CardTitle>Network footprint</CardTitle>
        <span className="text-xs text-muted-foreground">
          {points.length} PoP{points.length !== 1 ? "s" : ""} · {regionsCount} region{regionsCount !== 1 ? "s" : ""} · {countriesCount} countr{countriesCount !== 1 ? "ies" : "y"}
        </span>
      </CardHeader>
      <CardContent><PopMap points={points} /></CardContent>
    </Card>
  );
}
```

- [ ] **Step 2: Checkpoint** — `tsc -b` → exit 0.

---

## Task 6: Wire into the pages

**Files:**
- Modify: `brisk-dashboard/src/pages/zone-detail.tsx`
- Modify: `brisk-dashboard/src/pages/overview.tsx`

- [ ] **Step 1: Mount `<AssignmentsMap>` in the Assignments tab** of `zone-detail.tsx`, above the existing list. The page already computes `servingEdges`; pass `isLiveZone={servingEdges.length > 0}`.

```tsx
import { AssignmentsMap } from "@/components/zones/assignments-map";
// inside <TabsContent value="assignments">:
<div className="space-y-4">
  <AssignmentsMap zoneId={zoneId} isLiveZone={servingEdges.length > 0} />
  {/* existing assignments list component stays here, unchanged */}
</div>
```

- [ ] **Step 2: Add `<NetworkMap>` to `overview.tsx`** as a full-width card near the top (after the headline KPIs).

```tsx
import { NetworkMap } from "@/components/overview/network-map";
// within the layout:
<NetworkMap />
```

- [ ] **Step 3: Checkpoint** — `tsc -b` → exit 0.

---

## Task 7: Build + visual acceptance

- [ ] **Step 1: Typecheck + restart dev server**

```
docker exec brisk-control-brisk-dashboard-1 npx tsc -b
docker restart brisk-control-brisk-dashboard-1
```
Expected: tsc exit 0; container restarts.

- [ ] **Step 2: Visual acceptance (`http://localhost:5173`, logged in)**
- **Overview** → "Network footprint" card: solid world map, 3 green pulsing nodes (NYC/FRA/BLR) at correct spots; hover = flag + city + status; header "3 PoPs · 3 regions · 3 countries"; no actions.
- **A zone → Assignments tab** → map above the list. Click a green node → hollow (pending-remove) + footer "1 change pending · −1 · [Reset] [Save]". Click again → reverts. Reset works.
- **Save** on a TEST zone: applies the right assign/unassign (confirm via the list + persistence); live-zone confirm fires on removal. Revert the test change.
- Map-load failure leaves the list usable.
- Dark + light both legible.

- [ ] **Step 3: Final checkpoint** — `tsc -b` → exit 0. Feature complete.

---

## Self-review notes (author)
- Spec coverage: PopMap (T3), per-zone interactive + Save (T4), global read-only + count (T5), wiring (T6), flag+city+status tooltip (T3), polished solid-fill style (T3 `--muted` land + `--border` borders), no backend change (all FE). ✔
- `PopStatus` names match between `geo.ts` and `pop-map.tsx` `DOT`. ✔
- No git/test-runner assumptions: checkpoints = `tsc -b`; acceptance = live visual. ✔
- Open verification points flagged inline (CSS var names, `Flag` prop, mutation arg shapes, `useZoneServers` shape) — the executing subagent confirms each against existing code before writing.
