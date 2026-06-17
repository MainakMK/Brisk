import * as React from "react";
import { useSearchParams } from "react-router-dom";
import { AlertTriangle, RefreshCw, Info } from "lucide-react";
import { PageHeader } from "@/components/layout/page-header";
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
import { useZones } from "@/hooks/use-zones";
import { RANGES, rangeSeconds, refreshMs, summarize, type RangeKey } from "@/lib/stats";
import { bps, formatInt, formatReqPerSec, formatRatioPct } from "@/lib/format";
import type { SeriesPoint } from "@/lib/types";

const VALID_RANGES = RANGES.map((r) => r.key);

export default function AnalyticsPage() {
  const [params, setParams] = useSearchParams();
  const filters: Filters = {
    range: (VALID_RANGES.includes(params.get("range") as RangeKey) ? params.get("range") : "24h") as RangeKey,
    serverId: params.get("pop") && params.get("pop") !== "all" ? Number(params.get("pop")) : "all",
    zoneId: params.get("zone") && params.get("zone") !== "all" ? Number(params.get("zone")) : "all",
  };

  const setFilters = (next: Partial<Filters>) => {
    setParams(
      (prev) => {
        const p = new URLSearchParams(prev);
        if (next.range) p.set("range", next.range);
        if (next.serverId !== undefined) p.set("pop", String(next.serverId));
        if (next.zoneId !== undefined) p.set("zone", String(next.zoneId));
        return p;
      },
      { replace: true },
    );
  };

  // Anchor freezes the from/to window; advance it on a range-appropriate timer
  // (and on manual refresh) so the window slides without per-render churn.
  const [anchor, setAnchor] = React.useState(() => Date.now());
  React.useEffect(() => {
    const id = setInterval(() => setAnchor(Date.now()), refreshMs(filters.range));
    return () => clearInterval(id);
  }, [filters.range]);

  const current = useStatsSeries(filters, anchor);
  const previous = useStatsSeries(filters, anchor - rangeSeconds(filters.range) * 1000);
  const zones = useZones();

  const windowSecs = rangeSeconds(filters.range);

  // Real per-request insights from request_logs (Parts 3+4). Scoped by zone when
  // one is selected; network-wide otherwise. Not PoP-filterable (logs aggregate
  // across edges), so we note that when a single PoP is chosen.
  const analyticsFrom = React.useMemo(
    () => new Date(anchor - windowSecs * 1000).toISOString(),
    [anchor, windowSecs],
  );
  const logAnalytics = useLogAnalytics({
    zoneId: filters.zoneId !== "all" ? (filters.zoneId as number) : undefined,
    from: analyticsFrom,
  });
  const rangeLabel = RANGES.find((r) => r.key === filters.range)?.label ?? filters.range;

  const summary = summarize(current.points, windowSecs);
  const prev = summarize(previous.points, windowSecs);
  const hasData = current.points.length > 0;
  const singlePop = filters.serverId !== "all";
  // Only show "vs prev" when the previous period has comparable coverage —
  // otherwise a near-empty prior window yields a meaningless delta.
  const comparable = previous.points.length > 0 && previous.points.length >= current.points.length * 0.6;
  const d = (cur: number, p: number) => (comparable ? delta(cur, p) : null);
  const dPts = (cur: number, p: number) => (comparable ? deltaPts(cur, p) : null);

  const rows = React.useMemo(
    () =>
      current.points.map((p: SeriesPoint) => ({
        ...p,
        hit_pct: p.hit_ratio * 100,
      })),
    [current.points],
  );

  const xFmt = (iso: string) => {
    const d = new Date(iso);
    if (filters.range === "7d" || filters.range === "30d") return `${d.getMonth() + 1}/${d.getDate()}`;
    return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
  };
  const labelFmt = (iso: string) =>
    new Date(iso).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false });

  const [cacheView, setCacheView] = React.useState<"ratio" | "volume">("ratio");

  return (
    <div className="space-y-5">
      <PageHeader
        title="Analytics"
        description="Trends across the network, PoPs, and zones."
        actions={
          <Button variant="outline" size="sm" onClick={() => setAnchor(Date.now())} disabled={current.isFetching}>
            <RefreshCw className={current.isFetching ? "animate-spin" : ""} />
            Refresh
          </Button>
        }
      />

      <FilterBar filters={filters} servers={current.servers} zones={zones.data ?? []} onChange={setFilters} />

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

        {singlePop && filters.zoneId === "all" && (
          <ChartCard title="System" subtitle="CPU / RAM / disk for the selected PoP" loading={current.isLoading} empty={!hasData} className="lg:col-span-2">
            <TimeSeries
              data={rows}
              type="line"
              showLegend
              series={[
                { key: "cpu_pct", label: "CPU", color: "var(--chart-1)" },
                { key: "ram_pct", label: "RAM", color: "var(--chart-3)" },
                { key: "disk_pct", label: "Disk", color: "var(--chart-4)" },
              ]}
              valueFormatter={(v) => v.toFixed(0) + "%"}
              xFormatter={xFmt}
              labelFormatter={labelFmt}
            />
          </ChartCard>
        )}
      </section>

      {/* BY-POP TABLE (aggregate view only) */}
      {!singlePop && (
        <Card>
          <CardHeader>
            <CardTitle>By PoP</CardTitle>
            <p className="text-xs text-muted-foreground">Per-edge totals for the selected range</p>
          </CardHeader>
          <CardContent>
            <ByPopTable servers={current.servers} range={filters.range} zoneId={filters.zoneId} anchor={anchor} />
          </CardContent>
        </Card>
      )}

      {/* REAL per-request insights from request_logs (Phase 4 Step 6, Parts 3+4):
          origin offload, status breakdown, latency percentiles, top paths/countries. */}
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold">Request insights</h2>
          {singlePop && (
            <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
              <Info className="size-3.5" /> Network/zone aggregate — not filtered by the selected PoP
            </span>
          )}
        </div>
        <LogInsights
          data={logAnalytics.data?.analytics}
          loading={logAnalytics.isLoading}
          windowLabel={rangeLabel}
        />
      </div>
    </div>
  );
}
