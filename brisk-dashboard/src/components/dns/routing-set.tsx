import { Route, MapPin, ShieldCheck } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { RotationBadge } from "@/components/servers/rotation-badge";
import { useDnsRouting, useHealthStatus, indexByEdge } from "@/hooks/use-health";

/** The live cdn.<zone> smart-routed set: per-edge record state (enabled/disabled),
   smart-routing type + location, weight, health, TTL — the whole routing picture. */
export function RoutingSet() {
  const routing = useDnsRouting();
  const health = useHealthStatus();
  const byEdge = indexByEdge(health.data?.edges);

  const mode = routing.data?.mode ?? "—";
  const ttl = routing.data?.ttl;
  const servers = routing.data?.servers ?? [];
  const monitor = routing.data?.bunny_monitor;

  return (
    <Card className="overflow-hidden">
      <CardHeader className="flex-row flex-wrap items-center gap-2">
        <Route className="size-4 text-muted-foreground" />
        <CardTitle>{routing.data?.record ?? "cdn"} routing set</CardTitle>
        <Badge variant="outline" className="capitalize">
          {mode}
        </Badge>
        {ttl != null && <Badge variant="muted">TTL {ttl}s</Badge>}
        {!routing.data?.dns_enabled && <Badge variant="warning">DNS off</Badge>}
        {/* Bunny native DNS monitor (Layer-2 failover backstop): on => show the method
            (ping/http), off => a muted hint. So the DNS page tells you whether Bunny's own
            infra is independently watching the edges, and how. */}
        {monitor?.enabled ? (
          <Badge variant="success" className="gap-1">
            <ShieldCheck className="size-3" /> Bunny monitor · {monitor.method}
          </Badge>
        ) : (
          <Badge variant="muted" className="gap-1">
            <ShieldCheck className="size-3" /> Bunny monitor off
          </Badge>
        )}
        <p className="w-full text-xs text-muted-foreground">
          Each edge's A record, its smart-routing type + location, weight, and live rotation/health state.
          {monitor?.enabled
            ? ` Bunny independently pings each edge (${monitor.method}, ~30s) as a control-plane-down failover backstop.`
            : ""}
        </p>
      </CardHeader>
      <CardContent className="p-0">
        {routing.isLoading ? (
          <div className="space-y-2 p-4">
            {[0, 1].map((i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        ) : servers.length === 0 ? (
          <p className="px-4 py-10 text-center text-sm text-muted-foreground">
            No Brisk-managed edges in the routing set yet.
          </p>
        ) : (
          <ul className="divide-y divide-border">
            <li className="hidden grid-cols-[1.2fr_1.4fr_auto_auto_auto] gap-3 px-4 py-2 text-xs font-medium uppercase tracking-wider text-muted-foreground md:grid">
              <span>Edge</span>
              <span>Location</span>
              <span>Weight</span>
              <span>Record</span>
              <span className="text-right">Rotation</span>
            </li>
            {servers.map((s) => {
              const edge = byEdge[s.edge_id];
              const enabled = edge?.in_rotation ?? s.online;
              return (
                <li
                  key={s.edge_id}
                  className="grid grid-cols-1 items-center gap-2 px-4 py-3 text-sm md:grid-cols-[1.2fr_1.4fr_auto_auto_auto] md:gap-3"
                >
                  <span className="truncate font-mono text-xs">{s.edge_id}</span>
                  <span className="flex items-center gap-1.5 truncate text-xs text-muted-foreground">
                    {s.mapped ? (
                      <>
                        <MapPin className="size-3" />
                        {s.mode === "latency"
                          ? `latency · ${s.latency_zone}`
                          : `${s.label ?? s.region} · ${s.lat?.toFixed(1)},${s.long?.toFixed(1)}`}
                      </>
                    ) : (
                      <span className="text-warning">{s.region} (unmapped)</span>
                    )}
                  </span>
                  <span className="tabular text-xs">{s.weight}</span>
                  <span>
                    {enabled ? <Badge variant="success">enabled</Badge> : <Badge variant="muted">disabled</Badge>}
                  </span>
                  <span className="flex justify-start md:justify-end">
                    <RotationBadge reason={edge?.rotation_reason} />
                  </span>
                </li>
              );
            })}
          </ul>
        )}
        {routing.data?.mode === "latency" && routing.data?.latency_note && (
          <p className="border-t border-border px-4 py-2 text-xs text-muted-foreground">{routing.data.latency_note}</p>
        )}
      </CardContent>
    </Card>
  );
}
