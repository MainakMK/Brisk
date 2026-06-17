import { ShieldCheck, Gauge, ListOrdered, Globe2, Info } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { formatInt, formatRatioPct } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { LogAnalytics } from "@/lib/types";

// Real per-request insights derived from request_logs (Phase 4 Step 6, Parts 3+4).
// Everything here is computed from logged requests — no estimates.

function bytesH(n: number): string {
  if (!isFinite(n) || n <= 0) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB", "PB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return (v >= 100 ? v.toFixed(0) : v >= 10 ? v.toFixed(1) : v.toFixed(2)) + " " + u[i];
}
function ms(seconds: number): string {
  if (!isFinite(seconds) || seconds <= 0) return "0 ms";
  const m = seconds * 1000;
  return m >= 100 ? `${Math.round(m)} ms` : `${m.toFixed(1)} ms`;
}

const STATUS_COLOR: Record<string, string> = {
  "2xx": "bg-success",
  "3xx": "bg-muted-foreground",
  "4xx": "bg-warning",
  "5xx": "bg-danger",
};

export function LogInsights({
  data,
  loading,
  windowLabel,
}: {
  data?: LogAnalytics;
  loading?: boolean;
  windowLabel: string;
}) {
  if (loading) {
    return (
      <section className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {[0, 1, 2, 3].map((i) => (
          <Card key={i}>
            <CardContent className="p-5">
              <Skeleton className="h-[160px] w-full" />
            </CardContent>
          </Card>
        ))}
      </section>
    );
  }

  const empty = !data || data.total === 0;
  if (empty) {
    return (
      <Card>
        <CardContent className="flex items-start gap-2 p-4 text-sm text-muted-foreground">
          <Info className="mt-0.5 size-4 shrink-0" />
          <span>
            No request-log data in this window yet. These insights (origin offload, status
            breakdown, latency percentiles, top paths &amp; countries) are computed from the edge
            request logs shipped by the Phase-4 agent — they populate once edges on that agent serve
            traffic. Country needs the GeoIP module (Part 5).
          </span>
        </CardContent>
      </Card>
    );
  }

  const c = data.cache;
  const cacheTotal = Math.max(1, c.hit + c.miss + c.bypass + c.other);
  const statusTotal = Math.max(1, data.status_classes.reduce((s, x) => s + x.count, 0));
  const maxPath = Math.max(1, ...data.top_paths.map((p) => p.count));
  const maxCountry = Math.max(1, ...data.top_countries.map((p) => p.count));

  return (
    <section className="space-y-4">
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {/* ---- Origin offload (REAL) ---- */}
        <Card className="border-primary/30">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <ShieldCheck className="size-4 text-primary" /> Origin offload
            </CardTitle>
            <p className="text-xs text-muted-foreground">
              Share of requests served from cache (origin never touched) · {windowLabel}
            </p>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex items-end gap-6">
              <div>
                <div className="text-3xl font-semibold tabular text-primary">
                  {formatRatioPct(c.offload_req)}
                  <span className="text-base text-muted-foreground">%</span>
                </div>
                <div className="text-xs text-muted-foreground">by request</div>
              </div>
              <div>
                <div className="text-3xl font-semibold tabular">
                  {formatRatioPct(c.offload_bytes)}
                  <span className="text-base text-muted-foreground">%</span>
                </div>
                <div className="text-xs text-muted-foreground">by bytes ({bytesH(c.hit_bytes)} saved)</div>
              </div>
            </div>
            {/* hit / miss / bypass / other bar */}
            <div>
              <div className="flex h-3 w-full overflow-hidden rounded-full bg-muted">
                <span className="bg-success" style={{ width: `${(c.hit / cacheTotal) * 100}%` }} />
                <span className="bg-chart-5" style={{ width: `${(c.miss / cacheTotal) * 100}%` }} />
                <span className="bg-warning" style={{ width: `${(c.bypass / cacheTotal) * 100}%` }} />
                <span className="bg-muted-foreground/40" style={{ width: `${(c.other / cacheTotal) * 100}%` }} />
              </div>
              <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs">
                <Legend dot="bg-success" label="HIT" value={formatInt(c.hit)} />
                <Legend dot="bg-chart-5" label="MISS → origin" value={formatInt(c.miss)} />
                <Legend dot="bg-warning" label="BYPASS" value={formatInt(c.bypass)} />
                <Legend dot="bg-muted-foreground/40" label="Other" value={formatInt(c.other)} />
              </div>
            </div>
          </CardContent>
        </Card>

        {/* ---- Latency percentiles (REAL) ---- */}
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Gauge className="size-4 text-muted-foreground" /> Response time
            </CardTitle>
            <p className="text-xs text-muted-foreground">How long the edge took to serve a request · {windowLabel}</p>
          </CardHeader>
          <CardContent>
            <div className="grid grid-cols-4 gap-3 text-center">
              <Pct
                label="Typical"
                sub="P50 · median"
                value={ms(data.latency.p50)}
                title="Half of all requests were faster than this (the median) — the typical visitor's experience."
              />
              <Pct
                label="Slow"
                sub="P95"
                value={ms(data.latency.p95)}
                tone="warn"
                title="95% of requests were faster than this — only the slowest 5% were worse."
              />
              <Pct
                label="Slowest"
                sub="P99"
                value={ms(data.latency.p99)}
                tone="bad"
                title="99% of requests were faster than this — only the slowest 1% (worst tail) were worse."
              />
              <Pct
                label="Average"
                sub="mean"
                value={ms(data.latency.avg)}
                title="Plain average of every request. Easily skewed by outliers — the percentiles are the honest picture."
              />
            </div>
            {/* status breakdown bar */}
            <div className="mt-5">
              <div className="mb-2 text-xs font-medium text-muted-foreground">Status codes</div>
              <div className="flex h-3 w-full overflow-hidden rounded-full bg-muted">
                {data.status_classes.map((s) => (
                  <span
                    key={s.label}
                    className={STATUS_COLOR[s.label] ?? "bg-muted"}
                    style={{ width: `${(s.count / statusTotal) * 100}%` }}
                    title={`${s.label}: ${s.count}`}
                  />
                ))}
              </div>
              <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs">
                {data.status_classes.map((s) => (
                  <Legend
                    key={s.label}
                    dot={STATUS_COLOR[s.label] ?? "bg-muted"}
                    label={s.label}
                    value={formatInt(s.count)}
                  />
                ))}
              </div>
            </div>
          </CardContent>
        </Card>

        {/* ---- Top paths (REAL) ---- */}
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <ListOrdered className="size-4 text-muted-foreground" /> Top paths
            </CardTitle>
            <p className="text-xs text-muted-foreground">Most-requested URLs · {windowLabel}</p>
          </CardHeader>
          <CardContent>
            {data.top_paths.length === 0 ? (
              <p className="py-6 text-center text-sm text-muted-foreground">No paths in window.</p>
            ) : (
              <ul className="space-y-1.5">
                {data.top_paths.slice(0, 10).map((p) => (
                  <li key={p.path} className="relative">
                    <div
                      className="absolute inset-y-0 left-0 rounded bg-primary/10"
                      style={{ width: `${(p.count / maxPath) * 100}%` }}
                    />
                    <div className="relative flex items-center justify-between gap-3 px-2 py-1 text-xs">
                      <span className="truncate font-mono" title={p.path}>
                        {p.path}
                      </span>
                      <span className="shrink-0 tabular text-muted-foreground">
                        {formatInt(p.count)} · {bytesH(p.bytes)}
                      </span>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>

        {/* ---- Top countries (REAL, needs GeoIP) ---- */}
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Globe2 className="size-4 text-muted-foreground" /> Top countries
            </CardTitle>
            <p className="text-xs text-muted-foreground">By request count · {windowLabel}</p>
          </CardHeader>
          <CardContent>
            {data.top_countries.length === 0 ? (
              <p className="py-6 text-center text-sm text-muted-foreground">
                No geo data — the GeoIP module (Part 5) resolves client country.
              </p>
            ) : (
              <ul className="space-y-1.5">
                {data.top_countries.slice(0, 10).map((c2) => (
                  <li key={c2.label} className="relative">
                    <div
                      className="absolute inset-y-0 left-0 rounded bg-chart-3/15"
                      style={{ width: `${(c2.count / maxCountry) * 100}%` }}
                    />
                    <div className="relative flex items-center justify-between gap-3 px-2 py-1 text-xs">
                      <span className="font-mono uppercase">{c2.label}</span>
                      <span className="shrink-0 tabular text-muted-foreground">{formatInt(c2.count)}</span>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>
    </section>
  );
}

function Legend({ dot, label, value }: { dot: string; label: string; value: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 text-muted-foreground">
      <span className={cn("size-2 rounded-full", dot)} />
      {label} <span className="tabular text-foreground">{value}</span>
    </span>
  );
}

function Pct({
  label,
  sub,
  value,
  title,
  tone,
}: {
  label: string;
  sub?: string;
  value: string;
  title?: string;
  tone?: "warn" | "bad";
}) {
  const color = tone === "warn" ? "text-warning" : tone === "bad" ? "text-danger" : "text-foreground";
  return (
    <div className="rounded-lg border border-border bg-secondary/30 px-1 py-2" title={title}>
      <div className="text-[11px] font-medium leading-tight text-foreground">{label}</div>
      {sub && <div className="text-[9px] uppercase tracking-wider text-muted-foreground">{sub}</div>}
      <div className={cn("mt-1 text-sm font-semibold tabular", color)}>{value}</div>
    </div>
  );
}
