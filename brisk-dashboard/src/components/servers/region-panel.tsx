import * as React from "react";
import { Loader2, Wrench, Play, TriangleAlert, Globe2, ChevronDown, ChevronUp } from "lucide-react";
import { toast } from "sonner";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { useDrainRegion, useUndrainRegion } from "@/hooks/use-servers";
import { useHealthStatus, indexByEdge } from "@/hooks/use-health";
import { ApiError } from "@/lib/api";
import type { Server } from "@/lib/types";

interface RegionGroup {
  region: string;
  total: number;
  inRotation: number;
  drained: number;
}

/** PoPs grouped by region with a per-region drain/resume action (bulk maintenance). */
export function RegionPanel({ servers }: { servers: Server[] }) {
  const health = useHealthStatus();
  const byEdge = indexByEdge(health.data?.edges);

  const groups = React.useMemo<RegionGroup[]>(() => {
    const map = new Map<string, RegionGroup>();
    for (const s of servers) {
      const g = map.get(s.region) ?? { region: s.region, total: 0, inRotation: 0, drained: 0 };
      g.total++;
      if (byEdge[s.edge_id]?.in_rotation) g.inRotation++;
      if (s.drained) g.drained++;
      map.set(s.region, g);
    }
    return [...map.values()].sort((a, b) => a.region.localeCompare(b.region));
  }, [servers, byEdge]);

  // Collapsible: ALWAYS collapsed by default so the panel never pushes the server cards
  // down the page — the user expands it on demand for bulk region drain/resume.
  const [open, setOpen] = React.useState(false);

  if (groups.length === 0) return null;

  return (
    <Card>
      <CardHeader
        className="flex-row items-center gap-2 cursor-pointer select-none"
        role="button"
        tabIndex={0}
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            setOpen((o) => !o);
          }
        }}
      >
        <Globe2 className="size-4 text-muted-foreground" />
        <CardTitle>Regions</CardTitle>
        <p className="hidden text-xs text-muted-foreground sm:block">
          Bulk maintenance — drain or resume a whole region
        </p>
        <span className="ml-auto flex items-center gap-2 text-xs text-muted-foreground">
          {groups.length} region{groups.length !== 1 ? "s" : ""}
          {open ? <ChevronUp className="size-4" /> : <ChevronDown className="size-4" />}
        </span>
      </CardHeader>
      {open && (
        <CardContent className="divide-y divide-border pt-0">
          {groups.map((g) => (
            <RegionRow key={g.region} group={g} />
          ))}
        </CardContent>
      )}
    </Card>
  );
}

function RegionRow({ group }: { group: RegionGroup }) {
  const drain = useDrainRegion();
  const undrain = useUndrainRegion();
  const [open, setOpen] = React.useState(false);
  const [wouldEmpty, setWouldEmpty] = React.useState(false);

  const fullyDrained = group.drained === group.total;
  const partiallyDrained = group.drained > 0 && !fullyDrained;

  const doDrain = (force: boolean) => {
    drain.mutate(
      { region: group.region, force },
      {
        onSuccess: (res) => {
          toast.success(`Drained ${group.region}`, { description: `${res.drained} PoP(s) pulled from rotation.` });
          setOpen(false);
          setWouldEmpty(false);
        },
        onError: (e) => {
          if (e instanceof ApiError && e.status === 409 && (e.body as { would_empty?: boolean })?.would_empty) {
            setWouldEmpty(true);
            return;
          }
          toast.error("Region drain failed", { description: (e as Error).message });
        },
      },
    );
  };

  const doResume = () => {
    undrain.mutate(group.region, {
      onSuccess: (res) => toast.success(`Resumed ${group.region}`, { description: `${res.resumed} PoP(s) returned.` }),
      onError: (e) => toast.error("Region resume failed", { description: (e as Error).message }),
    });
  };

  return (
    <div className="flex items-center gap-3 py-2.5">
      <div className="min-w-0">
        <div className="font-medium">{group.region}</div>
        <div className="text-xs text-muted-foreground tabular">
          {group.inRotation}/{group.total} in rotation
          {group.drained > 0 && <span className="text-warning"> · {group.drained} draining</span>}
        </div>
      </div>
      <div className="ml-auto flex items-center gap-2">
        {fullyDrained ? (
          <Badge variant="warning">Draining</Badge>
        ) : partiallyDrained ? (
          <Badge variant="warning">Partial</Badge>
        ) : (
          <Badge variant="success">Active</Badge>
        )}
        {group.drained > 0 && (
          <Button variant="outline" size="sm" onClick={doResume} disabled={undrain.isPending}>
            {undrain.isPending ? <Loader2 className="animate-spin" /> : <Play />}
            Resume
          </Button>
        )}
        {group.drained < group.total && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              setWouldEmpty(false);
              setOpen(true);
            }}
          >
            <Wrench />
            Drain
          </Button>
        )}
      </div>

      <Dialog
        open={open}
        onOpenChange={(o) => {
          setOpen(o);
          if (!o) setWouldEmpty(false);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle className={wouldEmpty ? "flex items-center gap-2 text-destructive" : undefined}>
              {wouldEmpty && <TriangleAlert className="size-4" />}
              {wouldEmpty ? "This empties the rotation pool" : `Drain region ${group.region}?`}
            </DialogTitle>
            <DialogDescription>
              {wouldEmpty ? (
                <>
                  Draining {group.region} leaves <strong>no edge in rotation</strong> for this zone. Only continue
                  for a deliberate full maintenance window.
                </>
              ) : (
                <>
                  All {group.total} PoP(s) in {group.region} stop receiving new traffic; geo routing sends users to
                  the next-closest in-rotation region over the TTL. Boxes stay up and finish in-flight requests.
                </>
              )}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button
              variant={wouldEmpty ? "destructive" : "default"}
              onClick={() => doDrain(wouldEmpty)}
              disabled={drain.isPending}
            >
              {drain.isPending && <Loader2 className="animate-spin" />}
              {wouldEmpty ? "Drain anyway" : `Drain ${group.region}`}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
