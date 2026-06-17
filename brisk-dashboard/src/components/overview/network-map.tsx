import * as React from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { PopMap } from "@/components/maps/pop-map";
import { useServers } from "@/hooks/use-servers";
import { useDnsRouting, useHealthStatus, indexByEdge } from "@/hooks/use-health";
import { pointsFromServers } from "@/lib/geo";

export function NetworkMap() {
  const servers = useServers();
  const routing = useDnsRouting();
  const health = useHealthStatus();
  const byEdge = React.useMemo(() => indexByEdge(health.data?.edges), [health.data?.edges]);

  const edgeState = React.useMemo(() => {
    const m: Record<string, { online: boolean; inRotation?: boolean; drained?: boolean }> = {};
    for (const s of servers.data ?? []) {
      const e = byEdge[s.edge_id];
      m[s.edge_id] = { online: (s.status ?? "").toLowerCase() === "online", inRotation: e?.in_rotation, drained: s.drained };
    }
    return m;
  }, [servers.data, byEdge]);

  const points = pointsFromServers({ servers: servers.data ?? [], regions: routing.data?.regions, edgeState });
  const regionsCount = new Set(points.map((p) => p.label)).size;
  const countriesCount = new Set(points.map((p) => p.cc).filter(Boolean)).size;

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between">
        <CardTitle>Network footprint</CardTitle>
        <span className="text-xs text-muted-foreground">
          {points.length} PoP{points.length !== 1 ? "s" : ""} · {regionsCount} region{regionsCount !== 1 ? "s" : ""} · {countriesCount} countr{countriesCount !== 1 ? "ies" : "y"}
        </span>
      </CardHeader>
      <CardContent>
        <PopMap
          points={points}
          statusLabels={{ assigned: "Online" }}
          legendItems={["assigned", "unhealthy", "offline"]}
        />
      </CardContent>
    </Card>
  );
}
