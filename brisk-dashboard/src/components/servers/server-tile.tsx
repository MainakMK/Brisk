import { Link } from "react-router-dom";
import { Activity, Gauge, Wifi, Shield } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusPill } from "@/components/servers/status-pill";
import { RotationBadge } from "@/components/servers/rotation-badge";
import { DrainToggle } from "@/components/servers/drain-toggle";
import { MetricMeter } from "@/components/servers/metric-meter";
import { ServerActions } from "@/components/servers/server-actions";
import { deriveStatus } from "@/components/servers/server-status";
import { useServerLive } from "@/hooks/use-servers";
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
import type { Server } from "@/lib/types";
import { cn } from "@/lib/utils";

/** One PoP tile: status + live CPU/RAM/disk + bandwidth/req-s/hit-ratio.
   /live polls ~5s; keeps stale numbers visible during refetch. */
export function ServerTile({ server }: { server: Server }) {
  const st = deriveStatus(server);
  const provisioning = st === "provisioning";
  const offline = st === "offline";
  const pending = st === "pending";
  // Don't poll /live for a box that isn't up.
  const live = useServerLive(server.id, !provisioning && !offline);
  // Rotation/health (shared query — TanStack dedupes by key across tiles).
  const health = useHealthStatus();
  const edge = indexByEdge(health.data?.edges)[server.edge_id];
  const showRotation = !provisioning && !pending;
  // Location comes from the control plane's RegionMap (single source of truth — same
  // map that drives DNS geo-routing). TanStack dedupes this query across all tiles.
  const routing = useDnsRouting();
  const loc = resolveLocation(server.region, routing.data?.regions);

  return (
    // Whole-card stretched-link pattern (accessible): the title <Link> owns an ::after
    // overlay covering the card, so a click anywhere navigates to the detail page; the
    // action menu / drain button sit at z-10 so they stay independently clickable.
    <Card
      className={cn(
        "group relative flex flex-col gap-4 p-4 transition-colors",
        "focus-within:ring-1 focus-within:ring-ring hover:border-primary/40 hover:bg-accent/20",
        offline && "opacity-80",
        provisioning && "border-warning/40",
      )}
    >
      <div className="flex items-start gap-2">
        <div className="min-w-0">
          <Link
            to={`/servers/${server.id}`}
            aria-label={`Open ${server.edge_id || server.name} details`}
            className="block truncate font-medium hover:text-primary focus:outline-none focus-visible:underline after:absolute after:inset-0 after:rounded-[inherit]"
          >
            {server.edge_id || server.name}
          </Link>
          <div className="truncate text-xs text-muted-foreground">
            {loc ? (
              <span className="inline-flex items-center gap-1.5">
                <Flag cc={loc.cc} /> {loc.label}
              </span>
            ) : (
              server.region
            )}
          </div>
          <div className="truncate text-xs text-muted-foreground">
            <span className="font-mono">{server.ip}</span> · {formatCapacity(server.capacity_mbps)}
          </div>
        </div>
        <div className="relative z-10 ml-auto flex items-center gap-1">
          {server.role === "shield" && (
            <Badge variant="secondary" className="gap-1">
              <Shield className="size-3" /> Shield
            </Badge>
          )}
          <StatusPill server={server} />
          <ServerActions server={server} />
        </div>
      </div>

      {showRotation && (
        <div className="flex items-center justify-between gap-2">
          <RotationBadge reason={edge?.rotation_reason} />
          <span className="relative z-10">
            <DrainToggle server={server} size="sm" variant="ghost" />
          </span>
        </div>
      )}

      {provisioning ? (
        <div className="rounded-lg border border-dashed border-warning/40 bg-warning/5 px-3 py-4 text-center text-xs text-warning">
          Provisioning over SSH — view the live log →
        </div>
      ) : offline ? (
        <div className="rounded-lg border border-dashed border-border px-3 py-4 text-center text-xs text-muted-foreground">
          No heartbeat · last seen {timeAgo(server.last_seen)}
        </div>
      ) : (
        <>
          <div className="space-y-2">
            {live.isLoading && !live.data ? (
              <>
                <Skeleton className="h-6 w-full" />
                <Skeleton className="h-6 w-full" />
                <Skeleton className="h-6 w-full" />
              </>
            ) : (
              <>
                <MetricMeter label="CPU" value={live.data?.cpu_pct ?? null} />
                <MetricMeter
                  label="RAM"
                  value={live.data?.ram_pct ?? null}
                  sub={resourceFree(live.data?.ram_total_bytes, live.data?.ram_pct)}
                />
                <MetricMeter
                  label="Disk"
                  value={live.data?.disk_pct ?? null}
                  sub={resourceFree(live.data?.disk_total_bytes, live.data?.disk_pct)}
                />
              </>
            )}
          </div>

          <div className="grid grid-cols-3 gap-2 border-t border-border pt-3 text-xs">
            <Metric icon={Wifi} label="Bandwidth" value={live.data ? bps(live.data.bandwidth_bps) : "—"} />
            <Metric icon={Activity} label="Req/s" value={live.data ? formatReqPerSec(live.data.req_per_sec) : "—"} />
            <Metric icon={Gauge} label="Hit" value={live.data ? formatRatioPct(live.data.hit_ratio) + "%" : "—"} />
          </div>

          <div className="flex items-center justify-between text-[11px] text-muted-foreground">
            <span>seen {timeAgo(server.last_seen)}</span>
            {live.isError && <span className="text-danger">live metrics unavailable</span>}
          </div>
        </>
      )}
    </Card>
  );
}

function Metric({
  icon: Icon,
  label,
  value,
}: {
  icon: typeof Wifi;
  label: string;
  value: string;
}) {
  return (
    <div className="min-w-0">
      <div className="flex items-center gap-1 text-muted-foreground">
        <Icon className="size-3" />
        <span className="truncate">{label}</span>
      </div>
      <div className="tabular mt-0.5 truncate font-medium text-foreground">{value}</div>
    </div>
  );
}
