import { Link } from "react-router-dom";
import { AlertTriangle, RefreshCw, Server as ServerIcon, Trash2, ArrowRight } from "lucide-react";
import { useOverview, useServers } from "@/hooks/use-overview";
import { usePurgeJobs } from "@/hooks/use-purge";
import { purgeStatusVariant, purgeTypeLabel } from "@/components/purge/purge-meta";
import { KpiCard } from "@/components/kpi-card";
import { RoutingSummary, RoutingEvents } from "@/components/overview/routing-summary";
import { SecurityOverview } from "@/components/overview/security-overview";
import { NetworkMap } from "@/components/overview/network-map";
import { PageHeader } from "@/components/layout/page-header";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Badge } from "@/components/ui/badge";
import { StatusPill } from "@/components/servers/status-pill";
import { formatBps, formatReqPerSec, formatRatioPct, formatInt, timeAgo } from "@/lib/format";
import type { Server } from "@/lib/types";

export default function OverviewPage() {
  const overview = useOverview();
  const servers = useServers();

  const isLoading = overview.isLoading;
  const isError = overview.isError;
  const data = overview.data;

  const totalPops = servers.data?.length ?? null;
  const onlinePops = data?.online_servers ?? null;
  const bw = data ? formatBps(data.bandwidth_bps) : null;
  const hit = data ? formatRatioPct(data.hit_ratio) : null;

  return (
    <div className="space-y-5">
      <PageHeader
        title="Overview"
        description="Network health and live throughput across all PoPs."
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              overview.refetch();
              servers.refetch();
            }}
            disabled={overview.isFetching}
          >
            <RefreshCw className={overview.isFetching ? "animate-spin" : ""} />
            Refresh
          </Button>
        }
      />

      {isError && (
        <Card className="border-destructive/40 bg-destructive/5">
          <CardContent className="flex items-center gap-3 p-4 text-sm">
            <AlertTriangle className="size-5 text-destructive" />
            <div className="flex-1">
              <div className="font-medium text-destructive">Can&apos;t reach the control plane</div>
              <div className="text-muted-foreground">
                {(overview.error as Error)?.message ?? "Request failed"}. Is brisk-control running?
              </div>
            </div>
            <Button variant="outline" size="sm" onClick={() => overview.refetch()}>
              Retry
            </Button>
          </CardContent>
        </Card>
      )}

      <section className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <KpiCard
          label="Total bandwidth"
          loading={isLoading}
          value={bw?.value ?? "—"}
          unit={bw?.unit}
          footer={data ? "Live · " + data.window_seconds + "s window" : undefined}
        />
        <KpiCard
          label="Requests / sec"
          loading={isLoading}
          value={data ? formatReqPerSec(data.req_per_sec) : "—"}
          footer={data ? "Across the network" : undefined}
        />
        <KpiCard
          label="Cache hit ratio"
          loading={isLoading}
          value={hit ?? "—"}
          unit={data ? "%" : undefined}
          footer={
            hit ? (
              <div className="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-muted">
                <div className="h-full rounded-full bg-primary" style={{ width: hit + "%" }} />
              </div>
            ) : undefined
          }
        />
        <KpiCard
          label="PoPs online"
          loading={isLoading}
          value={
            onlinePops === null ? (
              "—"
            ) : (
              <>
                {onlinePops}
                <span className="text-muted-foreground">/{totalPops ?? "?"}</span>
              </>
            )
          }
          footer={totalPops === 0 ? "No servers yet" : "Edge fleet"}
        />
      </section>

      <section className="grid grid-cols-1 gap-4">
        <NetworkMap />
      </section>

      <section className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <div className="lg:col-span-2">
          <RoutingSummary />
        </div>
        <Card>
          <CardHeader>
            <CardTitle>Routing events</CardTitle>
            <p className="text-xs text-muted-foreground">Drains, failovers &amp; recoveries</p>
          </CardHeader>
          <CardContent>
            <RoutingEvents />
          </CardContent>
        </Card>
      </section>

      {/* Admin-only WAF overview (Phase 4 Step 4); renders null for customers. */}
      <section className="grid grid-cols-1 gap-4">
        <SecurityOverview />
      </section>

      <section className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader className="flex-row items-center justify-between">
            <div>
              <CardTitle>Recent activity</CardTitle>
              <p className="text-xs text-muted-foreground">Latest purges across the network</p>
            </div>
            <Button asChild variant="ghost" size="sm" className="text-muted-foreground">
              <Link to="/analytics">
                Analytics <ArrowRight />
              </Link>
            </Button>
          </CardHeader>
          <CardContent>
            <RecentActivity />
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>PoP status</CardTitle>
            <p className="text-xs text-muted-foreground">Live from the edge fleet</p>
          </CardHeader>
          <CardContent className="space-y-1">
            {servers.isLoading && (
              <div className="space-y-2">
                {[0, 1, 2].map((i) => (
                  <Skeleton key={i} className="h-6 w-full" />
                ))}
              </div>
            )}
            {servers.isError && <p className="text-sm text-muted-foreground">Couldn&apos;t load servers.</p>}
            {servers.data && servers.data.length === 0 && (
              <div className="flex flex-col items-center gap-2 py-6 text-center text-sm text-muted-foreground">
                <ServerIcon className="size-5" />
                No PoPs yet — add your first server.
              </div>
            )}
            {servers.data &&
              servers.data.slice(0, 8).map((s: Server) => (
                <div key={s.id} className="flex items-center gap-2 rounded-md px-1 py-1.5 text-sm">
                  <span className="font-medium">{s.edge_id || s.name}</span>
                  <span className="text-xs text-muted-foreground">{s.region}</span>
                  <span className="ml-auto">
                    <StatusPill server={s} />
                  </span>
                </div>
              ))}
            {servers.data && servers.data.length > 0 && (
              <p className="pt-2 text-xs text-muted-foreground tabular">
                {formatInt(servers.data.length)} server(s) total
              </p>
            )}
          </CardContent>
        </Card>
      </section>
    </div>
  );
}

/** Recent purges from /purge/jobs — real activity feed, links to the Purge screen. */
function RecentActivity() {
  const { data, isLoading } = usePurgeJobs();
  const jobs = (data ?? []).slice(0, 6);

  if (isLoading) {
    return (
      <div className="space-y-2">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-9 w-full" />
        ))}
      </div>
    );
  }

  if (jobs.length === 0) {
    return (
      <div className="flex h-[200px] flex-col items-center justify-center gap-2 text-center text-sm text-muted-foreground">
        <Trash2 className="size-5" />
        <span>No purges yet.</span>
        <Button asChild variant="outline" size="sm">
          <Link to="/purge">Open Purge</Link>
        </Button>
      </div>
    );
  }

  return (
    <ul className="divide-y divide-border">
      {jobs.map((j) => (
        <li key={j.id} className="flex items-center gap-2 py-2 text-sm">
          <Trash2 className="size-3.5 text-muted-foreground" />
          <Badge variant="outline">{purgeTypeLabel(j.type)}</Badge>
          <span className="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground">{j.target}</span>
          <span className="text-xs text-muted-foreground tabular">
            {j.edges_done}/{j.edges_total}
          </span>
          <Badge variant={purgeStatusVariant(j.status)} className="capitalize">
            {j.status}
          </Badge>
          <span className="hidden w-16 text-right text-xs text-muted-foreground tabular sm:inline">
            {timeAgo(j.created_at)}
          </span>
        </li>
      ))}
    </ul>
  );
}
