import * as React from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { PopMap } from "@/components/maps/pop-map";
import { useServers } from "@/hooks/use-servers";
import { useDnsRouting, useHealthStatus, indexByEdge } from "@/hooks/use-health";
import { useZoneServers, useAssignZone, useUnassignZone } from "@/hooks/use-zones";
import { pointsFromServers } from "@/lib/geo";
import type { Server } from "@/lib/types";

export function AssignmentsMap({ zoneId, isLiveZone }: { zoneId: number; isLiveZone: boolean }) {
  const servers = useServers();
  const routing = useDnsRouting();
  const health = useHealthStatus();
  const zoneServers = useZoneServers(zoneId);
  const assign = useAssignZone();
  const unassign = useUnassignZone();

  const byEdge = React.useMemo(() => indexByEdge(health.data?.edges), [health.data?.edges]);
  const current = React.useMemo(
    () => new Set((zoneServers.data ?? []).map((s: Server) => s.edge_id)), [zoneServers.data]);
  const [desired, setDesired] = React.useState<Set<string>>(current);
  React.useEffect(() => setDesired(current), [current]);

  const idToServerId = React.useMemo(() => {
    const m: Record<string, number> = {};
    for (const s of servers.data ?? []) m[s.edge_id] = s.id;
    return m;
  }, [servers.data]);

  const edgeState = React.useMemo(() => {
    const m: Record<string, { online: boolean; inRotation?: boolean; drained?: boolean }> = {};
    for (const s of servers.data ?? []) {
      const e = byEdge[s.edge_id];
      m[s.edge_id] = { online: (s.status ?? "").toLowerCase() === "online", inRotation: e?.in_rotation, drained: s.drained };
    }
    return m;
  }, [servers.data, byEdge]);

  const pendingAdd = React.useMemo(() => new Set([...desired].filter((id) => !current.has(id))), [desired, current]);
  const pendingRemove = React.useMemo(() => new Set([...current].filter((id) => !desired.has(id))), [desired, current]);
  const dirty = pendingAdd.size + pendingRemove.size;

  const points = pointsFromServers({
    servers: servers.data ?? [], regions: routing.data?.regions, edgeState,
    assignedEdgeIds: desired, pendingAdd, pendingRemove,
  });

  const toggle = (edgeId: string) =>
    setDesired((prev) => {
      const next = new Set(prev);
      if (next.has(edgeId)) next.delete(edgeId); else next.add(edgeId);
      return next;
    });

  const save = async () => {
    if (isLiveZone && pendingRemove.size > 0) {
      if (!window.confirm(`Remove ${pendingRemove.size} PoP(s) from this LIVE zone? Traffic re-routes within ~15s.`)) return;
    }
    try {
      for (const edgeId of pendingAdd) { const sid = idToServerId[edgeId]; if (sid != null) await assign.mutateAsync({ serverId: sid, zoneId }); }
      for (const edgeId of pendingRemove) { const sid = idToServerId[edgeId]; if (sid != null) await unassign.mutateAsync({ serverId: sid, zoneId }); }
      toast.success("Assignments saved", { description: "Edges re-pull within ~15s." });
      zoneServers.refetch();
    } catch (e) { toast.error("Save failed", { description: (e as Error).message }); }
  };

  const saving = assign.isPending || unassign.isPending;

  return (
    <div className="rounded-lg border border-border bg-card">
      <div className="border-b border-border px-4 py-2 text-sm text-muted-foreground">
        Click a PoP to stage it, then Save. Grey = available, green = serving.
      </div>
      <PopMap points={points} onPointClick={toggle}
        legendItems={["assigned", "available", "unhealthy", "offline"]} />
      {dirty > 0 && (
        <div className="flex items-center justify-between gap-3 border-t border-border px-4 py-2">
          <span className="text-sm">
            {dirty} change{dirty > 1 ? "s" : ""} pending
            {pendingAdd.size ? ` · +${pendingAdd.size}` : ""}{pendingRemove.size ? ` · −${pendingRemove.size}` : ""}
          </span>
          <span className="flex gap-2">
            <Button variant="ghost" size="sm" onClick={() => setDesired(current)} disabled={saving}>Reset</Button>
            <Button size="sm" onClick={save} disabled={saving}>{saving ? "Saving…" : "Save"}</Button>
          </span>
        </div>
      )}
    </div>
  );
}
