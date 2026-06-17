import * as React from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Globe, Plus, AlertTriangle, RefreshCw, Search, Video, ShieldAlert } from "lucide-react";
import { PageHeader } from "@/components/layout/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { AddZoneSheet } from "@/components/zones/add-zone-sheet";
import { ZoneActions } from "@/components/zones/zone-actions";
import { zoneStatusVariant, originLabel } from "@/components/zones/zone-meta";
import { useZones, useZoneAssignments } from "@/hooks/use-zones";
import { useAllCustomDomains } from "@/hooks/use-domains";
import type { CustomDomain, Server, Zone } from "@/lib/types";

export default function ZonesPage() {
  const { data, isLoading, isError, error, refetch, isFetching } = useZones();
  const assignments = useZoneAssignments();
  const allDomains = useAllCustomDomains();
  // Map each zone -> its custom domains (BYO domains live in a separate table, surfaced
  // in the CDN-hostname column so an added custom domain shows up here like Bunny).
  const domainsByZone = React.useMemo(() => {
    const m = new Map<number, CustomDomain[]>();
    for (const d of allDomains.data ?? []) {
      const arr = m.get(d.zone_id) ?? [];
      arr.push(d);
      m.set(d.zone_id, arr);
    }
    return m;
  }, [allDomains.data]);
  const [params, setParams] = useSearchParams();
  const [q, setQ] = React.useState("");
  const addOpen = params.get("add") === "1";

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

  const zones = data ?? [];
  const filtered = React.useMemo(() => {
    const needle = q.trim().toLowerCase();
    if (!needle) return zones;
    return zones.filter(
      (z) =>
        z.name.toLowerCase().includes(needle) ||
        z.cdn_hostname.toLowerCase().includes(needle) ||
        z.origin_url.toLowerCase().includes(needle) ||
        (domainsByZone.get(z.id) ?? []).some((d) => d.domain.toLowerCase().includes(needle)),
    );
  }, [zones, q, domainsByZone]);

  return (
    <div className="space-y-5">
      <PageHeader
        title="Zones"
        description="The sites and origins Brisk accelerates (pull zones)."
        actions={
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={isFetching ? "animate-spin" : ""} />
              Refresh
            </Button>
            <Button size="sm" onClick={() => setAddOpen(true)}>
              <Plus /> Add zone
            </Button>
          </div>
        }
      />

      {!isLoading && !isError && zones.length > 0 && (
        <div className="relative max-w-sm">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search name or hostname…" className="pl-8" />
        </div>
      )}

      {isError && (
        <Card className="border-destructive/40 bg-destructive/5">
          <CardContent className="flex items-center gap-3 p-4 text-sm">
            <AlertTriangle className="size-5 text-destructive" />
            <div className="flex-1">
              <div className="font-medium text-destructive">Couldn&apos;t load zones</div>
              <div className="text-muted-foreground">{(error as Error)?.message}</div>
            </div>
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              Retry
            </Button>
          </CardContent>
        </Card>
      )}

      {isLoading && (
        <Card>
          <CardContent className="space-y-3 p-4">
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-12 w-full" />
            ))}
          </CardContent>
        </Card>
      )}

      {!isLoading && !isError && zones.length === 0 && (
        <Card className="flex flex-col items-center justify-center gap-3 border-dashed py-16 text-center">
          <div className="grid size-12 place-items-center rounded-full bg-muted text-muted-foreground">
            <Globe className="size-6" />
          </div>
          <div className="space-y-1">
            <h3 className="text-sm font-medium">No zones yet</h3>
            <p className="mx-auto max-w-sm text-sm text-muted-foreground">
              Create your first zone — point a CDN hostname at your origin and Brisk caches it at the edge.
            </p>
          </div>
          <Button onClick={() => setAddOpen(true)}>
            <Plus /> Create your first zone
          </Button>
        </Card>
      )}

      {!isLoading && filtered.length > 0 && (
        <Card className="overflow-hidden">
          {/* header (desktop) */}
          <div className="hidden grid-cols-[minmax(0,1.5fr)_minmax(0,1.9fr)_minmax(0,1.3fr)_minmax(116px,auto)_84px_56px] items-center gap-5 border-b border-border px-5 py-3 text-xs font-medium uppercase tracking-wider text-muted-foreground md:grid">
            <div>Name</div>
            <div>CDN hostname</div>
            <div>Origin</div>
            <div>Status</div>
            <div className="text-center">Config rev</div>
            <div className="text-right">Actions</div>
          </div>
          <ul className="divide-y divide-border">
            {filtered.map((z) => (
              <ZoneRow key={z.id} zone={z} servingEdges={assignments.map.get(z.id) ?? []} customDomains={domainsByZone.get(z.id) ?? []} onDeleted={() => refetch()} />
            ))}
          </ul>
        </Card>
      )}

      {!isLoading && zones.length > 0 && filtered.length === 0 && (
        <p className="px-1 text-sm text-muted-foreground">No zones match "{q}".</p>
      )}

      <AddZoneSheet open={addOpen} onOpenChange={setAddOpen} />
    </div>
  );
}

function ZoneRow({
  zone,
  servingEdges,
  customDomains,
  onDeleted,
}: {
  zone: Zone;
  servingEdges: Server[];
  customDomains: CustomDomain[];
  onDeleted: () => void;
}) {
  const live = servingEdges.length > 0;
  const activeDomains = customDomains.filter((d) => d.status === "active");
  const pendingDomains = customDomains.filter((d) => d.status !== "active");
  return (
    <li className="grid grid-cols-1 items-center gap-2 px-5 py-3.5 transition-colors hover:bg-secondary/30 md:grid-cols-[minmax(0,1.5fr)_minmax(0,1.9fr)_minmax(0,1.3fr)_minmax(116px,auto)_84px_56px] md:gap-5">
      <div className="min-w-0">
        <Link to={`/zones/${zone.id}`} className="flex items-center gap-1.5 font-medium hover:text-primary">
          <span className="shrink-0 font-mono text-xs text-muted-foreground" title="Zone ID">#{zone.id}</span>
          <span className="truncate">{zone.name}</span>
          {live && <ShieldAlert className="size-3.5 shrink-0 text-warning" aria-label="Live zone" />}
        </Link>
      </div>
      <div className="min-w-0 space-y-0.5 text-sm">
        {/* Once a BYO custom domain is ACTIVE it IS the public hostname — show it instead
            of the platform …cdn.a2zjav.com hostname (the CNAME target still lives on the
            zone detail page). No active custom domain => show the platform hostname. */}
        {activeDomains.length > 0 ? (
          activeDomains.map((d) => (
            <a
              key={d.id}
              href={`https://${d.domain}`}
              target="_blank"
              rel="noreferrer"
              className="block truncate font-mono text-xs text-foreground hover:text-primary hover:underline"
            >
              {d.domain}
            </a>
          ))
        ) : (
          <a
            href={`https://${zone.cdn_hostname}`}
            target="_blank"
            rel="noreferrer"
            className="block truncate font-mono text-xs text-foreground hover:text-primary hover:underline"
          >
            {zone.cdn_hostname}
          </a>
        )}
        {/* Domains still verifying (not yet serving) shown muted with their status. */}
        {pendingDomains.map((d) => (
          <div key={d.id} className="truncate text-xs text-muted-foreground" title={d.last_error || d.status}>
            {d.domain} <span className="opacity-70">· {d.status}</span>
          </div>
        ))}
      </div>
      <div className="min-w-0">
        <a
          href={zone.origin_url}
          target="_blank"
          rel="noreferrer"
          className="block truncate font-mono text-xs text-muted-foreground hover:text-primary hover:underline"
        >
          {originLabel(zone)}
        </a>
      </div>
      <div className="flex items-center gap-1.5">
        <Badge variant={zoneStatusVariant(zone.status)}>{zone.status}</Badge>
        {zone.video && (
          <Badge variant="secondary" className="gap-1">
            <Video className="size-3" /> {zone.profile}
          </Badge>
        )}
      </div>
      <div
        className="text-center text-xs text-muted-foreground tabular md:px-2"
        title="Config revision — increments each time this zone's settings change; edges re-pull when it bumps."
      >
        {zone.config_version}
      </div>
      <div className="flex justify-end">
        <ZoneActions zone={zone} servingEdges={servingEdges} onDeleted={onDeleted} />
      </div>
    </li>
  );
}
