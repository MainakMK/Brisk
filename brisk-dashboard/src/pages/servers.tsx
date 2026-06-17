import * as React from "react";
import { useSearchParams } from "react-router-dom";
import { Server as ServerIcon, Plus, AlertTriangle, RefreshCw, Search } from "lucide-react";
import { PageHeader } from "@/components/layout/page-header";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { ServerTile } from "@/components/servers/server-tile";
import { RegionPanel } from "@/components/servers/region-panel";
import { AddServerSheet } from "@/components/servers/add-server-sheet";
import { AgentVersionCard } from "@/components/servers/agent-version-card";
import { RolloutProgress } from "@/components/servers/rollout-progress";
import { AuditFeed } from "@/components/servers/audit-feed";
import { deriveStatus } from "@/components/servers/server-status";
import { useServers } from "@/hooks/use-servers";
import { useDnsRouting } from "@/hooks/use-health";
import { resolveLocation } from "@/lib/geo";

export default function ServersPage() {
  const { data, isLoading, isError, error, refetch, isFetching } = useServers();
  const routing = useDnsRouting();
  const [params, setParams] = useSearchParams();
  const addOpen = params.get("add") === "1";
  const [query, setQuery] = React.useState("");

  const setAddOpen = (open: boolean) => {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (open) next.set("add", "1");
        else next.delete("add");
        return next;
      },
      { replace: true },
    );
  };

  const servers = data ?? [];
  const counts = React.useMemo(() => {
    let online = 0;
    let offline = 0;
    let other = 0;
    for (const s of servers) {
      const st = deriveStatus(s);
      if (st === "online") online++;
      else if (st === "offline") offline++;
      else other++;
    }
    return { online, offline, other, total: servers.length };
  }, [servers]);

  // Client-side search across name / edge id / region / IP / hostname, plus the
  // resolved city + country (so "bengaluru" or "germany" matches too).
  const regions = routing.data?.regions;
  const filtered = React.useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return servers;
    return servers.filter((s) => {
      const loc = resolveLocation(s.region, regions);
      return [s.name, s.edge_id, s.region, s.ip, s.hostname, loc?.label, loc?.cc].some((v) =>
        (v ?? "").toLowerCase().includes(q),
      );
    });
  }, [servers, query, regions]);

  return (
    <div className="space-y-5">
      <PageHeader
        title="Servers"
        description="The edge fleet (PoPs) Brisk runs — live metrics straight off the metal."
        actions={
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={isFetching ? "animate-spin" : ""} />
              Refresh
            </Button>
            <Button size="sm" onClick={() => setAddOpen(true)}>
              <Plus /> Add server
            </Button>
          </div>
        }
      />

      {!isLoading && !isError && servers.length > 0 && (
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-muted-foreground tabular">
            <span>
              <span className="font-medium text-foreground">{counts.online}</span> online
            </span>
            {counts.offline > 0 && (
              <span>
                <span className="font-medium text-danger">{counts.offline}</span> offline
              </span>
            )}
            {counts.other > 0 && (
              <span>
                <span className="font-medium text-warning">{counts.other}</span> provisioning/pending
              </span>
            )}
            <span>{counts.total} total</span>
          </div>
          <div className="relative w-full sm:w-72">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search name, region, IP…"
              aria-label="Search servers"
              className="pl-8"
            />
          </div>
        </div>
      )}

      {isError && (
        <Card className="border-destructive/40 bg-destructive/5">
          <CardContent className="flex items-center gap-3 p-4 text-sm">
            <AlertTriangle className="size-5 text-destructive" />
            <div className="flex-1">
              <div className="font-medium text-destructive">Couldn&apos;t load servers</div>
              <div className="text-muted-foreground">{(error as Error)?.message}</div>
            </div>
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              Retry
            </Button>
          </CardContent>
        </Card>
      )}

      {isLoading && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {[0, 1, 2].map((i) => (
            <Card key={i} className="space-y-4 p-4">
              <Skeleton className="h-5 w-32" />
              <Skeleton className="h-1.5 w-full" />
              <Skeleton className="h-1.5 w-full" />
              <Skeleton className="h-1.5 w-full" />
              <Skeleton className="h-8 w-full" />
            </Card>
          ))}
        </div>
      )}

      {!isLoading && !isError && servers.length === 0 && (
        <Card className="flex flex-col items-center justify-center gap-3 border-dashed py-16 text-center">
          <div className="grid size-12 place-items-center rounded-full bg-muted text-muted-foreground">
            <ServerIcon className="size-6" />
          </div>
          <div className="space-y-1">
            <h3 className="text-sm font-medium">No servers yet</h3>
            <p className="mx-auto max-w-sm text-sm text-muted-foreground">
              Add your first PoP — Brisk onboards it over SSH and streams the provisioning log live.
            </p>
          </div>
          <Button onClick={() => setAddOpen(true)}>
            <Plus /> Add your first server
          </Button>
        </Card>
      )}

      {!isLoading && !isError && servers.length > 0 && (
        <div className="space-y-4">
          <AgentVersionCard />
          <RolloutProgress />
          <AuditFeed />
        </div>
      )}

      {!isLoading && servers.length > 1 && <RegionPanel servers={servers} />}

      {!isLoading && servers.length > 0 && filtered.length === 0 && (
        <p className="py-8 text-center text-sm text-muted-foreground">
          No servers match “{query}”.
        </p>
      )}

      {!isLoading && filtered.length > 0 && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {filtered.map((s) => (
            <ServerTile key={s.id} server={s} />
          ))}
        </div>
      )}

      <AddServerSheet open={addOpen} onOpenChange={setAddOpen} />
    </div>
  );
}
