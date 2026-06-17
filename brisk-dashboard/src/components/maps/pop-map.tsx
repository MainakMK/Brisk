import * as React from "react";
import { geoNaturalEarth1, geoPath } from "d3-geo";
import { feature } from "topojson-client";
import worldTopo from "@/components/maps/world-110m.json";
import { Flag } from "@/components/ui/flag";
import type { PopPoint, PopStatus } from "@/lib/geo";

// viewBox width in projection units. The SVG scales to the container's full width
// (width:100% + height:auto), so this is just the internal coordinate space — the
// rendered size follows the container. We fit the world to this width, then crop the
// empty polar ocean (above ~80°N / below ~56°S) so the map reads as a clean wide band
// that FILLS its panel (Bunny-style), instead of a small letterboxed rectangle.
const VB_W = 980;
const LAT_TOP = 80;
const LAT_BOTTOM = -56;

const DOT: Record<PopStatus, { fill: string; stroke: string; dashed?: boolean; pulse?: boolean }> = {
  assigned:         { fill: "var(--success)",  stroke: "var(--success)", pulse: true },
  "pending-add":    { fill: "transparent",     stroke: "var(--success)" },
  available:        { fill: "transparent",     stroke: "var(--muted-foreground)" },
  "pending-remove": { fill: "transparent",     stroke: "var(--muted-foreground)", dashed: true },
  unhealthy:        { fill: "var(--warning)",  stroke: "var(--warning)" },
  offline:          { fill: "transparent",     stroke: "var(--danger)" },
};

// Human-readable status word shown in the tooltip + legend. Callers can override per
// status via `statusLabels` — e.g. the global network map relabels "assigned" → "Online"
// (KPI vocabulary), while the per-zone map keeps the zone-assignment wording.
const STATUS_LABEL: Record<PopStatus, string> = {
  assigned: "Serving",
  available: "Available",
  unhealthy: "Unhealthy",
  offline: "Offline",
  "pending-add": "Pending add",
  "pending-remove": "Pending remove",
};

export function PopMap({
  points,
  onPointClick,
  statusLabels,
  legendItems,
}: {
  points: PopPoint[];
  onPointClick?: (edgeId: string) => void;
  statusLabels?: Partial<Record<PopStatus, string>>;
  legendItems?: PopStatus[];
}) {
  const [hover, setHover] = React.useState<{ p: PopPoint; x: number; y: number } | null>(null);
  const labelFor = (s: PopStatus) => statusLabels?.[s] ?? STATUS_LABEL[s];

  // Project once. fitWidth makes the world span [0, VB_W] exactly (no horizontal
  // letterbox), then we derive the viewBox top/height from the cropped latitude band.
  // Because the SVG fills 100% width with that viewBox, projected coords map linearly
  // to the wrapper box — so the tooltip can be placed by simple percentage and lands
  // right on the node at any container width.
  const { land, project, viewBox, vbY, vbH } = React.useMemo(() => {
    // @ts-expect-error vendored topojson object typing
    const fc = feature(worldTopo, worldTopo.objects.countries) as GeoJSON.FeatureCollection;
    const proj = geoNaturalEarth1().fitWidth(VB_W, fc as never);
    const path = geoPath(proj);
    const yTop = proj([0, LAT_TOP])?.[1] ?? 0;
    const yBottom = proj([0, LAT_BOTTOM])?.[1] ?? VB_W / 2;
    const land = (fc.features as never[]).map((f) => path(f as never) ?? "");
    return {
      land,
      project: (lo: number, la: number) => proj([lo, la]),
      viewBox: `0 ${yTop} ${VB_W} ${yBottom - yTop}`,
      vbY: yTop,
      vbH: yBottom - yTop,
    };
  }, []);

  const interactive = !!onPointClick;

  return (
    // Outer column caps the display size (maxWidth) so the map never balloons on wide
    // monitors, and keeps the legend aligned with the map's left edge.
    <div style={{ width: "100%", maxWidth: 1040, margin: "0 auto" }}>
      {/* Map box: aspectRatio == viewBox ratio ⇒ no letterbox, so the tooltip percentages
          stay exact at any size. The tooltip is positioned relative to THIS box. */}
      <div style={{ position: "relative", width: "100%", aspectRatio: `${VB_W} / ${vbH}` }}>
        <svg
          viewBox={viewBox}
          preserveAspectRatio="xMidYMid meet"
          role="img"
          aria-label={`World map with ${points.length} Brisk points of presence`}
          style={{ display: "block", width: "100%", height: "100%" }}
        >
          <title>Brisk points of presence</title>
          <g>
            {land.map((d, i) => (
              <path key={i} d={d} style={{ fill: "var(--muted)", stroke: "var(--border)" }} strokeWidth={0.4} />
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
                aria-label={interactive ? `${pt.label ?? pt.edgeId} — ${labelFor(pt.status)}` : undefined}
                onMouseEnter={() => setHover({ p: pt, x, y })}
                onMouseLeave={() => setHover(null)}
                onClick={interactive ? () => onPointClick!(pt.edgeId) : undefined}
                onKeyDown={interactive ? (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onPointClick!(pt.edgeId); } } : undefined}>
                {s.pulse && (
                  <circle r={5.5} style={{ fill: "none", stroke: s.stroke }} strokeWidth={1.4} opacity={0.55}>
                    <animate attributeName="r" values="5.5;14;5.5" dur="2.6s" repeatCount="indefinite" />
                    <animate attributeName="opacity" values="0.55;0;0.55" dur="2.6s" repeatCount="indefinite" />
                  </circle>
                )}
                <circle r={5.5} style={{ fill: s.fill, stroke: s.stroke }} strokeWidth={1.6}
                  strokeDasharray={s.dashed ? "2 2" : undefined} />
              </g>
            );
          })}
        </svg>
        {hover && (
          <div style={{ position: "absolute",
            left: `${(hover.x / VB_W) * 100}%`,
            top: `calc(${((hover.y - vbY) / vbH) * 100}% + 12px)`,
            transform: "translateX(-50%)", pointerEvents: "none",
            background: "var(--popover)", color: "var(--popover-foreground)",
            border: "0.5px solid var(--border)", borderRadius: 8, padding: "6px 9px", fontSize: 12,
            whiteSpace: "nowrap", zIndex: 10,
            boxShadow: "0 4px 16px rgba(0,0,0,0.16)" }}>
            <div style={{ display: "flex", alignItems: "center", gap: 6, marginBottom: 2 }}>
              {hover.p.cc && <Flag cc={hover.p.cc} />}
              <span style={{ fontWeight: 500 }}>{hover.p.label ?? hover.p.edgeId}</span>
            </div>
            <div style={{ opacity: 0.7 }}>{hover.p.edgeId} · {labelFor(hover.p.status)}</div>
          </div>
        )}
      </div>
      {legendItems && legendItems.length > 0 && (
        <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 16,
          padding: "10px 2px 0", fontSize: 11.5, color: "var(--muted-foreground)" }}>
          {legendItems.map((s) => (
            <span key={s} style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
              <span aria-hidden="true" style={{ width: 9, height: 9, borderRadius: 999, boxSizing: "border-box",
                background: DOT[s].fill === "transparent" ? "transparent" : DOT[s].fill,
                border: `1.5px ${DOT[s].dashed ? "dashed" : "solid"} ${DOT[s].stroke}` }} />
              {labelFor(s)}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}
