import * as React from "react";
import { toast } from "sonner";
import { Server as ServerIcon, Loader2, ShieldAlert } from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { StatusPill } from "@/components/servers/status-pill";
import { useServers } from "@/hooks/use-servers";
import { useServerZones, useAssignZone, useUnassignZone } from "@/hooks/use-zones";
import type { Server, Zone } from "@/lib/types";

/** Assign/unassign this zone to each edge. Unassigning from the only edge
   serving it triggers a live-site confirm. */
export function ZoneAssignments({ zone }: { zone: Zone }) {
  const servers = useServers();
  const list = servers.data ?? [];

  if (servers.isLoading) {
    return (
      <div className="space-y-2">
        {[0, 1].map((i) => (
          <Skeleton key={i} className="h-14 w-full" />
        ))}
      </div>
    );
  }
  if (servers.isError) {
    return <p className="text-sm text-muted-foreground">Couldn&apos;t load servers.</p>;
  }
  if (list.length === 0) {
    return (
      <Card className="flex flex-col items-center gap-2 border-dashed py-12 text-center">
        <div className="grid size-10 place-items-center rounded-full bg-muted text-muted-foreground">
          <ServerIcon className="size-5" />
        </div>
        <h3 className="text-sm font-medium">No servers yet</h3>
        <p className="max-w-sm text-sm text-muted-foreground">Add an edge on the Servers page, then assign this zone to it.</p>
      </Card>
    );
  }

  return (
    <div className="space-y-3">
      <p className="text-sm text-muted-foreground">
        Assigning a zone to an edge makes that edge pull + serve it (within ~15s).
      </p>
      <ul className="space-y-2">
        {list.map((srv) => (
          <AssignmentRow key={srv.id} server={srv} zone={zone} />
        ))}
      </ul>
    </div>
  );
}

function AssignmentRow({ server, zone }: { server: Server; zone: Zone }) {
  const serverZones = useServerZones(server.id);
  const assign = useAssignZone();
  const unassign = useUnassignZone();
  const [confirmOpen, setConfirmOpen] = React.useState(false);

  const assigned = (serverZones.data ?? []).some((z) => z.id === zone.id);
  const pending = assign.isPending || unassign.isPending;
  const onlyZoneOnEdge = assigned && (serverZones.data?.length ?? 0) === 1;

  const doAssign = () =>
    assign.mutate(
      { serverId: server.id, zoneId: zone.id },
      {
        onSuccess: () => toast.success(`Assigned to ${server.edge_id || server.name}`, { description: "Edge pulls it within ~15s." }),
        onError: (e) => toast.error("Assign failed", { description: (e as Error).message }),
      },
    );

  const doUnassign = () => {
    unassign.mutate(
      { serverId: server.id, zoneId: zone.id },
      {
        onSuccess: () => {
          toast.success(`Unassigned from ${server.edge_id || server.name}`);
          setConfirmOpen(false);
        },
        onError: (e) => toast.error("Unassign failed", { description: (e as Error).message }),
      },
    );
  };

  return (
    <li>
      <Card>
        <CardContent className="flex items-center gap-3 p-3">
          <ServerIcon className="size-4 text-muted-foreground" />
          <div className="min-w-0 flex-1">
            <div className="truncate text-sm font-medium">{server.edge_id || server.name}</div>
            <div className="truncate text-xs text-muted-foreground">{server.region}</div>
          </div>
          <StatusPill server={server} />
          {assigned ? (
            <>
              <Badge variant="success">assigned</Badge>
              <Button variant="outline" size="sm" disabled={pending} onClick={() => setConfirmOpen(true)}>
                {unassign.isPending && <Loader2 className="animate-spin" />}
                Unassign
              </Button>
            </>
          ) : (
            <Button size="sm" disabled={pending} onClick={doAssign}>
              {assign.isPending && <Loader2 className="animate-spin" />}
              Assign
            </Button>
          )}
        </CardContent>
      </Card>

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <ShieldAlert className="size-4 text-warning" />
              Unassign {zone.name} from {server.edge_id || server.name}?
            </DialogTitle>
            <DialogDescription>
              This edge will stop serving <span className="font-mono">{zone.cdn_hostname}</span> on its next config
              pull (~15s). Make sure the live site is served elsewhere before removing its last edge.
            </DialogDescription>
          </DialogHeader>
          {onlyZoneOnEdge && (
            <div className="rounded-lg border border-warning/40 bg-warning/5 p-3 text-xs text-warning">
              This is the only zone on this edge — the control plane rejects empty-zone configs, so the edge keeps its
              last good config, but it will no longer serve this zone.
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmOpen(false)}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={doUnassign} disabled={unassign.isPending}>
              {unassign.isPending && <Loader2 className="animate-spin" />}
              Unassign
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </li>
  );
}
