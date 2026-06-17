import * as React from "react";
import { AlertTriangle, RefreshCw, Info } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { FilterBar, type Filters } from "@/components/analytics/filter-bar";
import { ByPopTable } from "@/components/analytics/by-pop-table";
import { LogInsights } from "@/components/analytics/log-insights";
import { Kpi, ChartCard, delta, deltaPts, bytesH } from "@/components/analytics/metric-cards";
import { TimeSeries } from "@/components/charts/time-series";
import { useStatsSeries } from "@/hooks/use-stats";
import { useLogAnalytics } from "@/hooks/use-logs";
import { RANGES, rangeSeconds, refreshMs, summarize, type RangeKey } from "@/lib/stats";
import { bps, formatInt, formatReqPerSec, formatRatioPct } from "@/lib/format";
import type { SeriesPoint } from "@/lib/types";

/**
 * Per-zone Analytics tab — the same view as the network Analytics page, locked to ONE
 * zone (zone selector hidden). Range + PoP filterable; KPIs + bandwidth/requests/cache
 * charts from /stats, per-PoP totals, and real per-request insights (offload, status,
 * latency, top paths/countries) from request_logs — all scoped to this zone.
 */
export function ZoneAnalyticsTab({ zoneId }: { zoneId: number }) {
  const [range, setRange] = React.useState<RangeKey>("24h");
  const [serverId, setServerId] = React.useState<number | "all">("all");
  const filters: Filters = { range, serverId, zoneId }; // zoneId is FIXED to this zone

  const setFilters = (next: Partial<Filters>) => {
    if (next.range) setRange(next.range);
    if (next.serverId !== undefined) setServerId(next.serverId);
    // zoneId changes are ignored — the zone is fixed in this tab.
  };

  // Anchor freezes the from/to window; advance it on a range-appropriate timer + manual refresh.
  const [anchor, setAnchor] = React.useState(() => Date.now());
  React.useEffect(() => {
    const id = setInterval(() => setAnchor(Date.now()), refreshMs(range));
    return () => clearInterval(id);
  }, [range]);

  const current = useStatsSeries(filters, anchor);
  const previous = useStatsSeries(filters, anchor - rangeSeconds(range) * 1000);
  const windowSecs = rangeSeconds(range);

  const analyticsFrom = React.useMemo(
    () => new Date(anchor - windowSecs * 1000).toISOString(),
    [anchor, windowSecs],
  );
  const logAnalytics = useLogAnalytics({ zoneId, from: analyticsFrom });
  const rangeLabel = RANGES.find((r) => r.key === range)?.label ?? range;

  const summary = summarize(current.points, windowSecs);
  const prev = summarize(previous.points, windowSecs);
  const hasData = current.points.length > 0;
  const singlePop = serverId !== "all";
  const comparable = previous.points.length > 0 && previous.points.length >= current.points.length * 0.6;
  const d = (cur: number, p: number) => (comparable ? delta(cur, p) : null);
  const dPts = (cur: number, p: number) => (comparable ? deltaPts(cur, p) : null);

  const rows = React.useMemo(
    () => current.points.map((p: SeriesPoint) => ({ ...p, hit_pct: p.hit_ratio * 100 })),
    [current.points],
  );

  const xFmt = (iso: string) => {
    const dt = new Date(iso);
    if (range === "7d" || range === "30d") return `${dt.getMonth() + 1}/${dt.getDate()}`;
    return dt.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
  };
  const labelFmt = (iso: string) =>
    new Date(iso).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false });

  const [cacheView, setCacheView] = React.useState<"ratio" | "volume">("ratio");

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <FilterBar filters={filters} servers={current.servers} zones={[]} onChange={setFilters} hideZone />
        <Button variant="outline" size="sm" onClick={() => setAnchor(Date.now())} disabled={current.isFetching}>
          <RefreshCw className={current.isFetching ? "animate-spin" : ""} />
          Refresh
        </Button>
      </div>

      {current.isError && (
        <Card className="border-destructive/40 bg-destructive/5">
          <CardContent className="flex items-center gap-3 p-4 text-sm">
            <AlertTriangle className="size-5 text-destructive" />
            <div className="flex-1">
              <div className="font-medium text-destructive">Couldn&apos;t load analytics</div>
              <div className="text-muted-foreground">The /stats query failed. Is the control plane up?</div>
            </div>
            <Button variant="outline" size="sm" onClick={() => setAnchor(Date.now())}>
              Retry
            </Button>
          </CardContent>
        </Card>
      )}

      {/* KPI ROW */}
      <section className="grid grid-cols-2 gap-4 lg:grid-cols-5">
        <Kpi label="Total requests" loading={current.isLoading} value={hasData ? formatInt(summary.totalRequests) : "—"} delta={d(summary.totalRequests, prev.totalRequests)} />
        <Kpi
          label="Cache hit ratio"
          loading={current.isLoading}
          value={hasData ? formatRatioPct(summary.hitRatio) : "—"}
          unit={hasData ? "%" : undefined}
          tone={summary.hitRatio >= 0.9 ? "good" : summary.hitRatio >= 0.75 ? "warn" : "bad"}
          delta={dPts(summary.hitRatio, prev.hitRatio)}
        />
        <Kpi label="Egress" loading={current.isLoading} value={hasData ? bytesH(summary.totalBytes) : "—"} delta={d(summary.totalBytes, prev.totalBytes)} />
        <Kpi label="Avg req/s" loading={current.isLoading} value={hasData ? formatReqPerSec(summary.avgReqPerSec) : "—"} delta={d(summary.avgReqPerSec, prev.avgReqPerSec)} />
        <Kpi label="Cache miss" loading={current.isLoading} value={hasData ? formatRatioPct(summary.missRatio) : "—"} unit={hasData ? "%" : undefined} />
      </section>

      {/* CHARTS */}
      <section className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <ChartCard title="Bandwidth" subtitle="Edge egress rate over time" loading={current.isLoading} empty={!hasData}>
          <TimeSeries
            data={rows}
            series={[{ key: "bandwidth_bps", label: "Bandwidth", color: "var(--chart-1)" }]}
            valueFormatter={(v) => bps(v)}
            xFormatter={xFmt}
            labelFormatter={labelFmt}
          />
        </ChartCard>

        <ChartCard title="Requests" subtitle="Requests per bucket over time" loading={current.isLoading} empty={!hasData}>
          <TimeSeries
            data={rows}
            series={[{ key: "requests", label: "Requests", color: "var(--chart-3)" }]}
            valueFormatter={(v) => (v >= 1000 ? (v / 1000).toFixed(1) + "k" : String(Math.round(v)))}
            xFormatter={xFmt}
            labelFormatter={labelFmt}
          />
        </ChartCard>

        <ChartCard
          title="Cache hit vs miss"
          subtitle="The headline CDN metric"
          loading={current.isLoading}
          empty={!hasData}
          className="lg:col-span-2"
          action={
            <div className="inline-flex rounded-md border border-border bg-muted/40 p-0.5">
              {(["ratio", "volume"] as const).map((v) => (
                <button
                  key={v}
                  onClick={() => setCacheView(v)}
                  className={cn(
                    "rounded px-2.5 py-1 text-xs font-medium capitalize transition-colors",
                    cacheView === v ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground",
                  )}
                >
                  {v}
                </button>
              ))}
            </div>
          }
        >
          {cacheView === "ratio" ? (
            <TimeSeries
              data={rows}
              type="line"
              series={[{ key: "hit_pct", label: "Hit ratio", color: "var(--chart-1)" }]}
              valueFormatter={(v) => v.toFixed(0) + "%"}
              xFormatter={xFmt}
              labelFormatter={labelFmt}
            />
          ) : (
            <TimeSeries
              data={rows}
              type="area"
              showLegend
              series={[
                { key: "hits", label: "Hits", color: "var(--chart-1)", stackId: "c" },
                { key: "misses", label: "Misses (origin)", color: "var(--chart-5)", stackId: "c" },
              ]}
              valueFormatter={(v) => (v >= 1000 ? (v / 1000).toFixed(1) + "k" : String(Math.round(v)))}
              xFormatter={xFmt}
              labelFormatter={labelFmt}
            />
          )}
        </ChartCard>
      </section>

      {/* BY-POP TABLE (aggregate view only) */}
      {!singlePop && (
        <Card>
          <CardHeader>
            <CardTitle>By PoP</CardTitle>
            <p className="text-xs text-muted-foreground">Per-edge totals for this zone over the selected range</p>
          </CardHeader>
          <CardContent>
            <ByPopTable servers={current.servers} range={range} zoneId={zoneId} anchor={anchor} />
          </CardContent>
        </Card>
      )}

      {/* REAL per-request insights from request_logs, scoped to this zone. */}
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold">Request insights</h2>
          {singlePop && (
            <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
              <Info className="size-3.5" /> Zone aggregate — not filtered by the selected PoP
            </span>
          )}
        </div>
        <LogInsights data={logAnalytics.data?.analytics} loading={logAnalytics.isLoading} windowLabel={rangeLabel} />
      </div>
    </div>
  );
}
