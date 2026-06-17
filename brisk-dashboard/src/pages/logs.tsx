import { useMemo, useState } from "react";
import {
  ScrollText,
  Pause,
  Play,
  RotateCw,
  Search,
  X,
  ChevronRight,
} from "lucide-react";
import { PageHeader } from "@/components/layout/page-header";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { useLogs } from "@/hooks/use-logs";
import { useZones } from "@/hooks/use-zones";
import type { RequestLog, LogFilter } from "@/lib/types";
import { cn } from "@/lib/utils";

// Window presets -> minutes back from now. The page passes `from` as RFC3339;
// the control plane defaults `to` to now.
const WINDOWS = [
  { value: "15", label: "Last 15 min" },
  { value: "60", label: "Last hour" },
  { value: "360", label: "Last 6 hours" },
  { value: "1440", label: "Last 24 hours" },
];
const STATUS_OPTS = [
  { value: "", label: "Any status" },
  { value: "2", label: "2xx success" },
  { value: "3", label: "3xx redirect" },
  { value: "4", label: "4xx client err" },
  { value: "5", label: "5xx server err" },
];
const CACHE_OPTS = [
  { value: "", label: "Any cache" },
  { value: "HIT", label: "HIT" },
  { value: "MISS", label: "MISS" },
  { value: "BYPASS", label: "BYPASS" },
  { value: "EXPIRED", label: "EXPIRED" },
  { value: "STALE", label: "STALE" },
];
const LIMIT_OPTS = [
  { value: "100", label: "100 rows" },
  { value: "200", label: "200 rows" },
  { value: "500", label: "500 rows" },
  { value: "1000", label: "1000 rows" },
];

function statusVariant(s: number): "success" | "warning" | "danger" | "muted" {
  if (s >= 500) return "danger";
  if (s >= 400) return "warning";
  if (s >= 300) return "muted";
  return "success";
}
function cacheVariant(c: string): "success" | "warning" | "muted" | "outline" {
  switch (c.toUpperCase()) {
    case "HIT":
      return "success";
    case "BYPASS":
      return "warning";
    case "MISS":
    case "EXPIRED":
    case "STALE":
      return "muted";
    default:
      return "outline";
  }
}

function fmtTime(ts: string): string {
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return ts;
  return (
    d.toLocaleTimeString(undefined, { hour12: false }) +
    "." +
    String(d.getMilliseconds()).padStart(3, "0")
  );
}
function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
function fmtMs(seconds: number): string {
  return `${Math.round(seconds * 1000)} ms`;
}

export default function LogsPage() {
  const [live, setLive] = useState(true);
  const [zoneId, setZoneId] = useState(0);
  const [status, setStatus] = useState("");
  const [cache, setCache] = useState("");
  const [path, setPath] = useState("");
  const [ip, setIp] = useState("");
  const [country, setCountry] = useState("");
  const [windowMin, setWindowMin] = useState("60");
  const [limit, setLimit] = useState("200");
  const [expanded, setExpanded] = useState<number | null>(null);

  const { data: zones } = useZones();

  // Recompute `from` only when the window changes; trimmed to the minute so the
  // query key stays stable across poll ticks within the same minute.
  const from = useMemo(() => {
    const ms = Number(windowMin) * 60_000;
    const d = new Date(Date.now() - ms);
    d.setSeconds(0, 0);
    return d.toISOString();
  }, [windowMin]);

  const filter: LogFilter = {
    zone_id: zoneId || undefined,
    status: status || undefined,
    cache: cache || undefined,
    path: path.trim() || undefined,
    ip: ip.trim() || undefined,
    country: country.trim().toUpperCase() || undefined,
    from,
    limit: Number(limit),
  };

  const { data, isLoading, isError, error, refetch, isFetching } = useLogs(filter, live);
  const logs: RequestLog[] = data?.logs ?? [];

  const zoneName = (id?: number | null) =>
    id ? zones?.find((z) => z.id === id)?.cdn_hostname ?? `zone ${id}` : null;

  const hasActiveFilter = !!status || !!cache || !!path || !!ip || !!country || !!zoneId;
  const clearFilters = () => {
    setZoneId(0);
    setStatus("");
    setCache("");
    setPath("");
    setIp("");
    setCountry("");
  };

  return (
    <div className="space-y-5">
      <PageHeader
        title="Logs"
        description="Live request log across every edge — for chasing a bad response or a cache MISS."
        actions={
          <div className="flex items-center gap-2">
            <Badge variant={live ? "success" : "muted"}>{live ? "live · 5s" : "paused"}</Badge>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setLive((v) => !v)}
              title={live ? "Pause auto-refresh" : "Resume auto-refresh"}
            >
              {live ? <Pause className="size-4" /> : <Play className="size-4" />}
              {live ? "Pause" : "Resume"}
            </Button>
            <Button variant="outline" size="sm" onClick={() => refetch()} disabled={isFetching}>
              <RotateCw className={cn("size-4", isFetching && "animate-spin")} />
              Refresh
            </Button>
          </div>
        }
      />

      {/* ---- filter bar ---- */}
      <Card>
        <CardContent className="flex flex-wrap items-end gap-3 py-4">
          <div className="min-w-[160px] flex-1">
            <label className="mb-1 block text-xs text-muted-foreground">Zone</label>
            <Select
              value={String(zoneId)}
              onChange={(e) => setZoneId(Number(e.target.value))}
              options={[
                { value: "0", label: "All zones" },
                ...(zones ?? []).map((z) => ({ value: String(z.id), label: z.cdn_hostname })),
              ]}
            />
          </div>
          <div className="w-[150px]">
            <label className="mb-1 block text-xs text-muted-foreground">Status</label>
            <Select value={status} onChange={(e) => setStatus(e.target.value)} options={STATUS_OPTS} />
          </div>
          <div className="w-[140px]">
            <label className="mb-1 block text-xs text-muted-foreground">Cache</label>
            <Select value={cache} onChange={(e) => setCache(e.target.value)} options={CACHE_OPTS} />
          </div>
          <div className="min-w-[180px] flex-1">
            <label className="mb-1 block text-xs text-muted-foreground">Path contains</label>
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={path}
                onChange={(e) => setPath(e.target.value)}
                placeholder="/videos/…"
                className="pl-8"
              />
            </div>
          </div>
          <div className="w-[150px]">
            <label className="mb-1 block text-xs text-muted-foreground">Client IP</label>
            <Input value={ip} onChange={(e) => setIp(e.target.value)} placeholder="203.0.113.4" />
          </div>
          <div className="w-[90px]">
            <label className="mb-1 block text-xs text-muted-foreground">Country</label>
            <Input
              value={country}
              onChange={(e) => setCountry(e.target.value)}
              placeholder="US"
              maxLength={2}
            />
          </div>
          <div className="w-[150px]">
            <label className="mb-1 block text-xs text-muted-foreground">Window</label>
            <Select value={windowMin} onChange={(e) => setWindowMin(e.target.value)} options={WINDOWS} />
          </div>
          <div className="w-[130px]">
            <label className="mb-1 block text-xs text-muted-foreground">Rows</label>
            <Select value={limit} onChange={(e) => setLimit(e.target.value)} options={LIMIT_OPTS} />
          </div>
          {hasActiveFilter && (
            <Button variant="ghost" size="sm" onClick={clearFilters}>
              <X className="size-4" /> Clear
            </Button>
          )}
        </CardContent>
      </Card>

      {/* ---- result count ---- */}
      <div className="flex items-center justify-between px-1 text-xs text-muted-foreground">
        <span>
          {isLoading
            ? "Loading…"
            : `${logs.length} request${logs.length === 1 ? "" : "s"}${
                logs.length === Number(limit) ? " (capped — narrow the window or filters)" : ""
              }`}
        </span>
        {data && (
          <span>
            {new Date(data.from).toLocaleString()} →{" "}
            {new Date(data.to).toLocaleTimeString(undefined, { hour12: false })}
          </span>
        )}
      </div>

      {/* ---- table ---- */}
      <Card>
        <CardContent className="p-0">
          {isError ? (
            <div className="grid place-items-center gap-1 py-14 text-center text-sm text-danger">
              <ScrollText className="size-5" />
              <span>Couldn&apos;t load logs: {(error as Error)?.message}</span>
            </div>
          ) : logs.length === 0 && !isLoading ? (
            <div className="grid place-items-center gap-2 py-16 text-center text-sm text-muted-foreground">
              <ScrollText className="size-6" />
              <span className="font-medium text-foreground">No requests in this window</span>
              <span className="max-w-md">
                Logs ship from edges running the Phase-4 agent (structured JSON access log → control
                plane). Widen the window, clear filters, or send traffic through an edge to see lines
                here.
              </span>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-border text-left text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
                    <th className="w-8 px-2 py-2"></th>
                    <th className="px-3 py-2">Time</th>
                    <th className="px-3 py-2">Status</th>
                    <th className="px-3 py-2">Method</th>
                    <th className="px-3 py-2">Zone / Path</th>
                    <th className="px-3 py-2">Cache</th>
                    <th className="px-3 py-2">Edge</th>
                    <th className="px-3 py-2 text-right">Bytes</th>
                    <th className="px-3 py-2 text-right">Time</th>
                    <th className="px-3 py-2">Client</th>
                  </tr>
                </thead>
                <tbody>
                  {logs.map((l, i) => (
                    <FragmentRow
                      key={`${l.ts}-${l.request_id}-${i}`}
                      l={l}
                      open={expanded === i}
                      zone={zoneName(l.zone_id)}
                      onToggle={() => setExpanded(expanded === i ? null : i)}
                    />
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function FragmentRow({
  l,
  open,
  zone,
  onToggle,
}: {
  l: RequestLog;
  open: boolean;
  zone: string | null;
  onToggle: () => void;
}) {
  return (
    <>
      <tr className="cursor-pointer border-b border-border/60 hover:bg-secondary/30" onClick={onToggle}>
        <td className="px-2 py-1.5 text-muted-foreground">
          <ChevronRight className={cn("size-4 transition-transform", open && "rotate-90")} />
        </td>
        <td className="whitespace-nowrap px-3 py-1.5 font-mono text-xs text-muted-foreground">
          {fmtTime(l.ts)}
        </td>
        <td className="px-3 py-1.5">
          <Badge variant={statusVariant(l.status)}>{l.status || "—"}</Badge>
        </td>
        <td className="px-3 py-1.5 font-mono text-xs">{l.method}</td>
        <td className="max-w-[420px] px-3 py-1.5">
          {zone && <span className="mr-1.5 text-xs text-muted-foreground">{zone}</span>}
          <span className="font-mono text-xs">{l.path}</span>
        </td>
        <td className="px-3 py-1.5">
          {l.cache_status && l.cache_status !== "-" ? (
            <Badge variant={cacheVariant(l.cache_status)}>{l.cache_status}</Badge>
          ) : (
            <span className="text-xs text-muted-foreground">—</span>
          )}
        </td>
        <td className="whitespace-nowrap px-3 py-1.5 text-xs text-muted-foreground">{l.edge_id}</td>
        <td className="whitespace-nowrap px-3 py-1.5 text-right font-mono text-xs">{fmtBytes(l.bytes)}</td>
        <td className="whitespace-nowrap px-3 py-1.5 text-right font-mono text-xs text-muted-foreground">
          {fmtMs(l.request_time)}
        </td>
        <td className="whitespace-nowrap px-3 py-1.5 font-mono text-xs text-muted-foreground">
          {l.country && l.country !== "-" ? `${l.country} · ` : ""}
          {l.client_ip}
        </td>
      </tr>
      {open && (
        <tr className="border-b border-border bg-secondary/20">
          <td></td>
          <td colSpan={9} className="px-3 py-3">
            <dl className="grid grid-cols-2 gap-x-8 gap-y-1.5 text-xs md:grid-cols-3">
              <Detail label="Host" value={l.host} mono />
              <Detail label="Request ID" value={l.request_id} mono />
              <Detail label="Upstream time" value={l.upstream_time ? fmtMs(l.upstream_time) : "—"} />
              <Detail label="Referer" value={l.referer || "—"} mono />
              <Detail label="User-Agent" value={l.user_agent || "—"} mono span2 />
            </dl>
          </td>
        </tr>
      )}
    </>
  );
}

function Detail({
  label,
  value,
  mono,
  span2,
}: {
  label: string;
  value: string;
  mono?: boolean;
  span2?: boolean;
}) {
  return (
    <div className={cn(span2 && "md:col-span-2")}>
      <dt className="text-[10px] uppercase tracking-wider text-muted-foreground">{label}</dt>
      <dd className={cn("truncate text-foreground", mono && "font-mono")} title={value}>
        {value}
      </dd>
    </div>
  );
}
