import * as React from "react";
import { Loader2, Rocket } from "lucide-react";
import { toast } from "sonner";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useStartRollout } from "@/hooks/use-rollouts";
import { ApiError } from "@/lib/api";
import type { Server } from "@/lib/types";

/** Deploy dialog: pick which PoPs update (grouped by region) + the soak window, then start the
 *  rollout. Edges not yet on the target version are pre-selected; already-current ones are shown
 *  but unchecked (no point redeploying them). */
export function DeployDialog({
  open,
  onOpenChange,
  version,
  servers,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  version: string;
  servers: Server[];
}) {
  const start = useStartRollout();
  const [selected, setSelected] = React.useState<Record<string, boolean>>({});
  const [soak, setSoak] = React.useState("90");

  // Seed selection whenever the dialog opens: pre-check edges that are behind the target.
  React.useEffect(() => {
    if (!open) return;
    const next: Record<string, boolean> = {};
    for (const s of servers) next[s.edge_id] = s.agent_version !== version;
    setSelected(next);
  }, [open, servers, version]);

  // Group edges by region for the picker.
  const groups = React.useMemo(() => {
    const m = new Map<string, Server[]>();
    for (const s of servers) {
      const g = m.get(s.region) ?? [];
      g.push(s);
      m.set(s.region, g);
    }
    return [...m.entries()].sort((a, b) => a[0].localeCompare(b[0]));
  }, [servers]);

  const chosen = servers.filter((s) => selected[s.edge_id]).map((s) => s.edge_id);

  const submit = () => {
    const soakSeconds = Number(soak) || 90;
    if (chosen.length === 0) {
      toast.error("Pick at least one PoP");
      return;
    }
    start.mutate(
      { version, pops: chosen, soak_seconds: soakSeconds },
      {
        onSuccess: () => {
          toast.success(`Deploying ${version}`, { description: `${chosen.length} PoP(s), ${soakSeconds}s soak each.` });
          onOpenChange(false);
        },
        onError: (e) => {
          const msg = e instanceof ApiError ? (e.body as { error?: string })?.error ?? e.message : (e as Error).message;
          toast.error("Couldn't start rollout", { description: msg });
        },
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Deploy {version} to selected PoPs</DialogTitle>
          <DialogDescription>
            One PoP updates at a time, health-gated; you can pause or roll back from the live panel.
          </DialogDescription>
        </DialogHeader>

        <div className="max-h-72 space-y-3 overflow-y-auto pr-1">
          {groups.map(([region, edges]) => (
            <div key={region}>
              <div className="mb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">{region}</div>
              <div className="space-y-1">
                {edges.map((s) => {
                  const current = s.agent_version === version;
                  return (
                    <label
                      key={s.edge_id}
                      className="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted/40"
                    >
                      <input
                        type="checkbox"
                        className="size-4 accent-[var(--primary)]"
                        checked={!!selected[s.edge_id]}
                        onChange={(e) => setSelected((m) => ({ ...m, [s.edge_id]: e.target.checked }))}
                      />
                      <span className="font-medium">{s.edge_id}</span>
                      <span className="ml-auto text-xs text-muted-foreground tabular">
                        {current ? <span className="text-success">on {version}</span> : s.agent_version || "—"}
                      </span>
                    </label>
                  );
                })}
              </div>
            </div>
          ))}
          {servers.length === 0 && <p className="text-sm text-muted-foreground">No online edges to deploy to.</p>}
        </div>

        <div className="flex items-end gap-3">
          <div className="space-y-1">
            <Label className="text-xs text-muted-foreground">Soak per PoP (seconds)</Label>
            <Input value={soak} onChange={(e) => setSoak(e.target.value)} inputMode="numeric" className="w-32" />
          </div>
          <p className="pb-2 text-xs text-muted-foreground">
            Each edge is watched this long after it updates before the next one starts.
          </p>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={start.isPending || chosen.length === 0}>
            {start.isPending ? <Loader2 className="animate-spin" /> : <Rocket />}
            Deploy to selected ({chosen.length})
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
