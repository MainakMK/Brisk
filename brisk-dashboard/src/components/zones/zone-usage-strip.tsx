import * as React from "react";
import { Activity } from "lucide-react";
import { Skeleton } from "@/components/ui/skeleton";
import { useStatsSeries } from "@/hooks/use-stats";
import { rangeSeconds, summarize } from "@/lib/stats";
import { bytesH } from "@/components/analytics/metric-cards";
import { formatInt, formatRatioPct } from "@/lib/format";
import type { SeriesPoint } from "@/lib/types";

/**
 * Compact "Usage this month" strip for a zone — Bunny's always-visible sidebar widget,
 * adapted to Brisk's tabbed zone layout (shown under the CDN-hostname panel, on every
 * tab). Traffic (egress) + Requests + Cache-HIT over the last 30 days, with a tiny
 * bandwidth sparkline. Pure read from the existing /stats, scoped to this zone. For the
 * full breakdown, the zone's Analytics tab.
 */
export function ZoneUsageStrip({ zoneId }: { zoneId: number }) {
  // Anchor the 30d window once on mount + refresh every 5m (the 30d cadence). Stats are
  // a continuous aggregate, so a fixed anchor keeps the window from churning per render.
  const [anchor, setAnchor] = React.useState(() => Date.now());
  React.useEffect(() => {
    const id = setInterval(() => setAnchor(Date.now()), 300_000);
    return () => clearInterval(id);
  }, []);

  const q = useStatsSeries({ range: "30d", serverId: "all", zoneId }, anchor);
  const summary = summarize(q.points, rangeSeconds("30d"));
  const hasData = q.points.length > 0;

  return (
    <div className="flex flex-wrap items-center gap-x-8 gap-y-3 rounded-lg border border-border bg-card px-4 py-3">
      <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
        <Activity className="size-3.5" /> Usage this month
      </div>

      <Stat label="Traffic" loading={q.isLoading} value={hasData ? bytesH(summary.totalBytes) : "—"} />
      <Stat label="Requests" loading={q.isLoading} value={hasData ? formatInt(summary.totalRequests) : "—"} />
      <Stat
        label="Cache HIT"
        loading={q.isLoading}
        value={hasData ? formatRatioPct(summary.hitRatio) + "%" : "—"}
        tone={summary.hitRatio >= 0.9 ? "good" : summary.hitRatio >= 0.75 ? "warn" : undefined}
      />

      <div className="ml-auto">
        {q.isLoading ? (
          <Skeleton className="h-8 w-28" />
        ) : (
          <Sparkline points={q.points} />
        )}
      </div>
    </div>
  );
}

function Stat({
  label,
  value,
  loading,
  tone,
}: {
  label: string;
  value: React.ReactNode;
  loading?: boolean;
  tone?: "good" | "warn";
}) {
  const toneColor = tone === "good" ? "text-success" : tone === "warn" ? "text-warning" : "";
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-[10px] uppercase tracking-wider text-muted-foreground">{label}</span>
      {loading ? (
        <Skeleton className="h-5 w-16" />
      ) : (
        <span className={`tabular text-base font-semibold leading-none ${toneColor}`}>{value}</span>
      )}
    </div>
  );
}

/** Minimal inline bandwidth sparkline (no chart lib) — last 30d egress rate shape. */
function Sparkline({ points }: { points: SeriesPoint[] }) {
  const vals = points.map((p) => p.bandwidth_bps ?? 0);
  if (vals.length < 2 || vals.every((v) => v === 0)) {
    return <span className="text-[10px] text-muted-foreground">no traffic yet</span>;
  }
  const w = 112;
  const h = 32;
  const max = Math.max(...vals);
  const min = Math.min(...vals);
  const span = max - min || 1;
  const step = w / (vals.length - 1);
  const d = vals
    .map((v, i) => {
      const x = i * step;
      const y = h - ((v - min) / span) * (h - 4) - 2; // 2px padding top/bottom
      return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
  return (
    <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} className="overflow-visible" aria-hidden>
      <path d={d} fill="none" stroke="var(--chart-1)" strokeWidth={1.5} strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  );
}
