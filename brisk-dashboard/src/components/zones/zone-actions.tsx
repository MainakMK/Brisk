import * as React from "react";
import { useNavigate } from "react-router-dom";
import { MoreVertical, Pencil, ListOrdered, Server as ServerIcon, Trash2, Loader2, ShieldAlert } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { useDeleteZone } from "@/hooks/use-zones";
import type { Server, Zone } from "@/lib/types";

/** Zone kebab: edit / rules / assignments (deep-link to detail tabs) + delete.
   `servingEdges` drives the live-site protection confirm. */
export function ZoneActions({
  zone,
  servingEdges,
  onDeleted,
  size = "icon",
}: {
  zone: Zone;
  servingEdges: Server[];
  onDeleted?: () => void;
  size?: "icon" | "sm";
}) {
  const navigate = useNavigate();
  const [confirmOpen, setConfirmOpen] = React.useState(false);
  const [typed, setTyped] = React.useState("");
  const del = useDeleteZone();
  const protectedZone = servingEdges.length > 0;
  // Live zone => require typing the exact hostname (matches the server-side guard).
  const confirmed = !protectedZone || typed.trim() === zone.cdn_hostname;

  const go = (tab: string) => navigate(`/zones/${zone.id}?tab=${tab}`);

  const setOpen = (o: boolean) => {
    setConfirmOpen(o);
    if (!o) setTyped(""); // reset the type-to-confirm box when the dialog closes
  };

  const doDelete = () => {
    if (!confirmed) return;
    // Always send the hostname as confirm for protected zones; harmless otherwise.
    del.mutate(
      { id: zone.id, confirm: protectedZone ? zone.cdn_hostname : undefined },
      {
        onSuccess: () => {
          toast.success(`Zone ${zone.name} deleted`, {
            description: protectedZone
              ? "Cache purged across all PoPs; the vhost drops on the next config pull (~20-30s)."
              : undefined,
          });
          setOpen(false);
          onDeleted?.();
        },
        onError: (e) => toast.error("Delete failed", { description: (e as Error).message }),
      },
    );
  };

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size={size} aria-label="Zone actions">
            <MoreVertical />
            {size === "sm" && <span>Actions</span>}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onSelect={() => go("settings")}>
            <Pencil /> Edit
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => go("rules")}>
            <ListOrdered /> Manage rules
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => go("assignments")}>
            <ServerIcon /> Assign to servers
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onSelect={() => setConfirmOpen(true)}
            className="text-destructive focus:bg-destructive/10"
          >
            <Trash2 /> Delete
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <Dialog open={confirmOpen} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              {protectedZone && <ShieldAlert className="size-4 text-warning" />}
              Delete {zone.name}?
            </DialogTitle>
            <DialogDescription>
              Removes the zone <span className="font-mono">{zone.cdn_hostname}</span> from the control plane,
              purges its cache from every PoP, and stops the edges serving it (~20-30s).
            </DialogDescription>
          </DialogHeader>

          {protectedZone && (
            <div className="space-y-3">
              <div className="rounded-lg border border-warning/40 bg-warning/5 p-3 text-sm text-warning">
                <div className="flex items-center gap-1.5 font-medium">
                  <ShieldAlert className="size-4" /> This is a live zone
                </div>
                <p className="mt-1 text-warning/90">
                  It&apos;s served by {servingEdges.map((s) => s.edge_id || s.name).join(", ")}. Deleting it
                  tears the zone down on those edges (cache purged + vhost removed). Unassign it first if you
                  only mean to stop serving it from one PoP.
                </p>
              </div>
              <div className="space-y-1.5">
                <label className="text-sm text-muted-foreground">
                  Type <span className="font-mono text-foreground">{zone.cdn_hostname}</span> to confirm:
                </label>
                <Input
                  value={typed}
                  onChange={(e) => setTyped(e.target.value)}
                  placeholder={zone.cdn_hostname}
                  autoComplete="off"
                  spellCheck={false}
                  aria-label="Type the hostname to confirm deletion"
                />
              </div>
            </div>
          )}

          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={doDelete} disabled={del.isPending || !confirmed}>
              {del.isPending && <Loader2 className="animate-spin" />}
              Delete zone
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
