import * as React from "react";
import { Link } from "react-router-dom";
import { Route, Wrench, HeartCrack, CircleCheck, PowerOff, Network } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { useServers } from "@/hooks/use-servers";
import { useHealthStatus, useDnsAudit, indexByEdge } from "@/hooks/use-health";
import { timeAgo } from "@/lib/format";
import type { DnsAuditEntry } from "@/lib/types";

/** Network routing-health summary: in-rotation/total, draining, unhealthy, offline,
   plus a per-region rollup. Real state only. */
export function RoutingSummary() {
  const servers = useServers();
  const health = useHealthStatus();
  const byEdge = indexByEdge(health.data?.edges);

  const rows = servers.data ?? [];
  const counts = React.useMemo(() => {
    let inRotation = 0,
      draining = 0,
      unhealthy = 0,
      offline = 0;
    for (const s of rows) {
      const reason = byEdge[s.edge_id]?.rotation_reason;
      if (reason === "in_rotation") inRotation++;
      else if (reason === "drained") draining++;
      else if (reason === "unhealthy") unhealthy++;
      else offline++;
    }
    return { inRotation, draining, unhealthy, offline, total: rows.length };
  }, [rows, byEdge]);

  const regions = React.useMemo(() => {
    const map = new Map<string, { region: string; total: number; inRotation: number; draining: number; down: number }>();
    for (const s of rows) {
      const g = map.get(s.region) ?? { region: s.region, total: 0, inRotation: 0, draining: 0, down: 0 };
      g.total++;
      const reason = byEdge[s.edge_id]?.rotation_reason;
      if (reason === "in_rotation") g.inRotation++;
      else if (reason === "drained") g.draining++;
      else g.down++;
      map.set(s.region, g);
    }
    return [...map.values()].sort((a, b) => a.region.localeCompare(b.region));
  }, [rows, byEdge]);

  const loading = servers.isLoading;

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between">
        <div className="flex items-center gap-2">
          <Route className="size-4 text-muted-foreground" />
          <CardTitle>Routing health</CardTitle>
        </div>
        <Link to="/dns" className="text-xs text-muted-foreground hover:text-foreground">
          DNS &amp; routing →
        </Link>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
          <Stat icon={CircleCheck} label="In rotation" value={loading ? null : `${counts.inRotation}/${counts.total}`} tone="success" />
          <Stat icon={Wrench} label="Draining" value={loading ? null : counts.draining} tone={counts.draining ? "warning" : "muted"} />
          <Stat icon={HeartCrack} label="Unhealthy" value={loading ? null : counts.unhealthy} tone={counts.unhealthy ? "danger" : "muted"} />
          <Stat icon={PowerOff} label="Offline" value={loading ? null : counts.offline} tone="muted" />
        </div>

        <div className="space-y-1">
          <div className="flex items-center gap-1.5 text-xs font-medium uppercase tracking-wider text-muted-foreground">
            <Network className="size-3.5" /> Regions
          </div>
          {loading ? (
            <div className="space-y-2 pt-1">
              {[0, 1].map((i) => (
                <Skeleton key={i} className="h-5 w-full" />
              ))}
            </div>
          ) : regions.length === 0 ? (
            <p className="py-2 text-sm text-muted-foreground">No PoPs yet.</p>
          ) : (
            <ul className="divide-y divide-border">
              {regions.map((r) => {
                const tone = r.draining > 0 ? "warning" : r.down > 0 && r.inRotation === 0 ? "danger" : "success";
                const label = r.draining > 0 ? "Draining" : r.down > 0 && r.inRotation === 0 ? "Down" : "Healthy";
                return (
                  <li key={r.region} className="flex items-center gap-2 py-1.5 text-sm">
                    <span className="font-medium">{r.region}</span>
                    <span className="text-xs text-muted-foreground tabular">
                      {r.inRotation}/{r.total} in rotation
                    </span>
                    <Badge variant={tone} className="ml-auto">
                      {label}
                    </Badge>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      </CardContent>
    </Card>
  );
}

function Stat({
  icon: Icon,
  label,
  value,
  tone,
}: {
  icon: typeof Route;
  label: string;
  value: React.ReactNode;
  tone: "success" | "warning" | "danger" | "muted";
}) {
  const color =
    tone === "success" ? "text-success" : tone === "warning" ? "text-warning" : tone === "danger" ? "text-danger" : "text-muted-foreground";
  return (
    <div className="rounded-lg border border-border p-3">
      <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <Icon className={`size-3.5 ${color}`} />
        {label}
      </div>
      <div className="tabular mt-1 text-xl font-semibold">{value ?? <Skeleton className="h-6 w-10" />}</div>
    </div>
  );
}

/** Map an audit entry to a friendly routing-event label + tone. */
function eventMeta(a: DnsAuditEntry): { label: string; tone: "success" | "warning" | "danger" | "muted" } {
  if (a.action === "drain") return { label: "Drained", tone: "warning" };
  if (a.action === "undrain") return { label: "Resumed", tone: "success" };
  if (a.reason === "health_unhealthy" || a.action === "disable") return { label: "Failed over", tone: "danger" };
  if (a.reason === "health_recovered" || a.action === "enable") return { label: "Recovered", tone: "success" };
  if (a.action === "add") return { label: "Registered", tone: "success" };
  if (a.action === "remove") return { label: "Removed", tone: "muted" };
  return { label: a.action, tone: "muted" };
}

/** Recent routing events from the DNS audit trail. */
export function RoutingEvents({ limit = 7 }: { limit?: number }) {
  const audit = useDnsAudit();
  const events = (audit.data ?? []).slice(0, limit);

  if (audit.isLoading) {
    return (
      <div className="space-y-2">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-8 w-full" />
        ))}
      </div>
    );
  }
  if (events.length === 0) {
    return (
      <div className="flex h-[160px] flex-col items-center justify-center gap-1 text-center text-sm text-muted-foreground">
        <Route className="size-5" />
        No routing events yet.
      </div>
    );
  }
  return (
    <ul className="divide-y divide-border">
      {events.map((a) => {
        const m = eventMeta(a);
        return (
          <li key={a.id} className="flex items-center gap-2 py-2 text-sm">
            <Badge variant={m.tone}>{m.label}</Badge>
            <span className="font-mono text-xs text-muted-foreground">{a.edge_id}</span>
            {a.reason && a.reason !== a.action && (
              <span className="hidden truncate text-xs text-muted-foreground sm:inline">· {a.reason}</span>
            )}
            <span className="ml-auto w-16 text-right text-xs text-muted-foreground tabular">{timeAgo(a.created_at)}</span>
          </li>
        );
      })}
    </ul>
  );
}
