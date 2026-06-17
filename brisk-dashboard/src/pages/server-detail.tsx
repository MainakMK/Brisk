import * as React from "react";
import { useParams, useNavigate, Link } from "react-router-dom";
import { ArrowLeft, AlertTriangle, MapPin, Network, Clock, Gauge } from "lucide-react";
import { PageHeader } from "@/components/layout/page-header";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Badge } from "@/components/ui/badge";
import { StatusPill } from "@/components/servers/status-pill";
import { RotationBadge } from "@/components/servers/rotation-badge";
import { ServerOps } from "@/components/servers/server-ops";
import { MetricMeter } from "@/components/servers/metric-meter";
import { ServerActions } from "@/components/servers/server-actions";
import { ProvisionLogPanel } from "@/components/servers/provision-log";
import { deriveStatus } from "@/components/servers/server-status";
import { AreaChart, type AreaPoint } from "@/components/charts/area-chart";
import { useServer, useServerLive, useServerStats } from "@/hooks/use-servers";
import { useHealthStatus, indexByEdge, useDnsRouting } from "@/hooks/use-health";
import {
  bps,
  formatReqPerSec,
  formatRatioPct,
  formatCapacity,
  timeAgo,
  resourceFree,
} from "@/lib/format";
import { resolveLocation } from "@/lib/geo";
import { Flag } from "@/components/ui/flag";
import type { SeriesPoint } from "@/lib/types";

type Metric = "bandwidth" | "requests" | "cpu";

const METRICS: { key: Metric; label: string; pick: (p: SeriesPoint) => number; fmt: (v: number) => string }[] = [
  { key: "bandwidth", label: "Bandwidth", pick: (p) => p.bandwidth_bps ?? 0, fmt: (v) => bps(v) },
  { key: "requests", label: "Requests", pick: (p) => p.requests, fmt: (v) => formatReqPerSec(v) },
  { key: "cpu", label: "CPU %", pick: (p) => p.cpu_pct ?? 0, fmt: (v) => Math.round(v) + "%" },
];

export default function ServerDetailPage() {
  const { id } = useParams();
  const serverId = Number(id);
  const navigate = useNavigate();
  const [metric, setMetric] = React.useState<Metric>("bandwidth");

  const server = useServer(serverId);
  const st = server.data ? deriveStatus(server.data) : null;
  const isLive = st === "online";
  const provisioning = st === "provisioning";
  const live = useServerLive(serverId, isLive);
  const stats = useServerStats(serverId, isLive);
  const health = useHealthStatus();
  const routing = useDnsRouting(); // RegionMap (single source of truth for location)

  if (server.isLoading) {
    return (
      <div className="space-y-5">
        <Skeleton className="h-8 w-48" />
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
          <Skeleton className="h-48 lg:col-span-2" />
          <Skeleton className="h-48" />
        </div>
      </div>
    );
  }

  if (server.isError || !server.data) {
    return (
      <Card className="border-destructive/40 bg-destructive/5">
        <CardContent className="flex items-center gap-3 p-4 text-sm">
          <AlertTriangle className="size-5 text-destructive" />
          <div className="flex-1">
            <div className="font-medium text-destructive">Server not found</div>
            <div className="text-muted-foreground">{(server.error as Error)?.message ?? "Unknown server"}</div>
          </div>
          <Button asChild variant="outline" size="sm">
            <Link to="/servers">Back to Servers</Link>
          </Button>
        </CardContent>
      </Card>
    );
  }

  const s = server.data;
  const loc = resolveLocation(s.region, routing.data?.regions);
  const activeMetric = METRICS.find((m) => m.key === metric)!;
  const points: AreaPoint[] = (stats.data?.points ?? []).map((p) => ({
    label: new Date(p.time).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false }),
    value: activeMetric.pick(p),
  }));

  return (
    <div className="space-y-5">
      <Button asChild variant="ghost" size="sm" className="-ml-2 text-muted-foreground">
        <Link to="/servers">
          <ArrowLeft /> Servers
        </Link>
      </Button>

      <PageHeader
        title={s.edge_id || s.name}
        description={s.name}
        actions={<ServerActions server={s} showView={false} size="sm" onDeleted={() => navigate("/servers")} />}
      />

      {/* meta row */}
      <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm">
        <StatusPill server={s} />
        <RotationBadge reason={indexByEdge(health.data?.edges)[s.edge_id]?.rotation_reason} />
        <Meta
          icon={MapPin}
          text={
            loc ? (
              <span className="inline-flex items-center gap-1.5">
                <Flag cc={loc.cc} /> {loc.label}
              </span>
            ) : (
              s.region
            )
          }
        />
        <Meta icon={Network} text={s.ip} />
        <Meta icon={Gauge} text={formatCapacity(s.capacity_mbps)} />
        <Meta icon={Clock} text={`seen ${timeAgo(s.last_seen)}`} />
        {s.provisioned_at && <Badge variant="muted">provisioned {timeAgo(s.provisioned_at)}</Badge>}
      </div>

      {/* live metrics + trend */}
      <section className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader className="flex-row items-center justify-between">
            <div>
              <CardTitle>Trend · last hour</CardTitle>
              <p className="text-xs text-muted-foreground">From /stats (1m resolution)</p>
            </div>
            <div className="inline-flex rounded-md border border-border bg-muted/40 p-0.5">
              {METRICS.map((m) => (
                <button
                  key={m.key}
                  onClick={() => setMetric(m.key)}
                  className={
                    "rounded px-2.5 py-1 text-xs font-medium transition-colors " +
                    (metric === m.key
                      ? "bg-primary text-primary-foreground"
                      : "text-muted-foreground hover:text-foreground")
                  }
                >
                  {m.label}
                </button>
              ))}
            </div>
          </CardHeader>
          <CardContent>
            {!isLive ? (
              <EmptyChart text="Live trend is available when the edge is online." />
            ) : stats.isLoading ? (
              <Skeleton className="h-[260px] w-full" />
            ) : points.length === 0 ? (
              <EmptyChart text="No samples in the last hour yet." />
            ) : (
              <AreaChart data={points} valueFormatter={activeMetric.fmt} />
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Live metrics</CardTitle>
            <p className="text-xs text-muted-foreground">~5s refresh</p>
          </CardHeader>
          <CardContent className="space-y-4">
            {!isLive ? (
              <p className="text-sm text-muted-foreground">
                {provisioning
                  ? "Provisioning — metrics start after the first heartbeat."
                  : "Edge offline — no live metrics."}
              </p>
            ) : (
              <>
                <MetricMeter label="CPU" value={live.data?.cpu_pct ?? null} size="lg" />
                <MetricMeter
                  label="RAM"
                  value={live.data?.ram_pct ?? null}
                  size="lg"
                  sub={resourceFree(live.data?.ram_total_bytes, live.data?.ram_pct)}
                />
                <MetricMeter
                  label="Disk"
                  value={live.data?.disk_pct ?? null}
                  size="lg"
                  sub={resourceFree(live.data?.disk_total_bytes, live.data?.disk_pct)}
                />
                <div className="grid grid-cols-3 gap-2 border-t border-border pt-3 text-center text-xs">
                  <Stat label="Bandwidth" value={live.data ? bps(live.data.bandwidth_bps) : "—"} />
                  <Stat label="Req/s" value={live.data ? formatReqPerSec(live.data.req_per_sec) : "—"} />
                  <Stat label="Hit" value={live.data ? formatRatioPct(live.data.hit_ratio) + "%" : "—"} />
                </div>
              </>
            )}
          </CardContent>
        </Card>
      </section>

      {/* rotation / health / routing + per-PoP config */}
      <ServerOps server={s} />

      {/* provisioning log (live while provisioning, history otherwise) */}
      <ProvisionLogPanel serverId={serverId} active={provisioning} />
    </div>
  );
}

function Meta({ icon: Icon, text }: { icon: typeof MapPin; text: React.ReactNode }) {
  return (
    <span className="flex items-center gap-1.5 text-muted-foreground">
      <Icon className="size-3.5" />
      <span className="tabular text-foreground">{text}</span>
    </span>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-muted-foreground">{label}</div>
      <div className="tabular mt-0.5 font-medium">{value}</div>
    </div>
  );
}

function EmptyChart({ text }: { text: string }) {
  return (
    <div className="flex h-[260px] items-center justify-center rounded-lg border border-dashed border-border text-sm text-muted-foreground">
      {text}
    </div>
  );
}
