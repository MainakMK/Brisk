import * as React from "react";
import { Wrench, Play, Loader2, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { useDrainServer, useUndrainServer } from "@/hooks/use-servers";
import { ApiError } from "@/lib/api";
import type { Server } from "@/lib/types";

/** Drain / Resume control. Drain confirms; if it would empty the rotation pool
   the backend returns 409 and the dialog escalates to a strong "force" confirm. */
export function DrainToggle({
  server,
  size = "sm",
  variant = "outline",
}: {
  server: Server;
  size?: "sm" | "icon" | "default";
  variant?: "outline" | "secondary" | "ghost";
}) {
  const drain = useDrainServer();
  const undrain = useUndrainServer();
  const [open, setOpen] = React.useState(false);
  const [reason, setReason] = React.useState("");
  const [wouldEmpty, setWouldEmpty] = React.useState(false);

  const label = server.edge_id || server.name;

  const reset = () => {
    setReason("");
    setWouldEmpty(false);
  };

  const doDrain = (force: boolean) => {
    drain.mutate(
      { id: server.id, reason: reason.trim() || undefined, force },
      {
        onSuccess: () => {
          toast.success(`Draining ${label}`, {
            description: "New traffic now routes to other PoPs. The box keeps serving in-flight requests.",
          });
          setOpen(false);
          reset();
        },
        onError: (e) => {
          if (e instanceof ApiError && e.status === 409) {
            const body = e.body as { would_empty?: boolean } | undefined;
            if (body?.would_empty) {
              setWouldEmpty(true); // escalate to force-confirm
              return;
            }
          }
          toast.error("Drain failed", { description: (e as Error).message });
        },
      },
    );
  };

  const doResume = () => {
    undrain.mutate(server.id, {
      onSuccess: () =>
        toast.success(`Resuming ${label}`, {
          description: "Returned to health-governed rotation (re-enters if healthy).",
        }),
      onError: (e) => toast.error("Resume failed", { description: (e as Error).message }),
    });
  };

  if (server.drained) {
    return (
      <Button variant={variant} size={size} onClick={doResume} disabled={undrain.isPending}>
        {undrain.isPending ? <Loader2 className="animate-spin" /> : <Play />}
        Resume
      </Button>
    );
  }

  return (
    <>
      <Button
        variant={variant}
        size={size}
        onClick={() => {
          reset();
          setOpen(true);
        }}
      >
        <Wrench />
        Drain
      </Button>

      <Dialog
        open={open}
        onOpenChange={(o) => {
          setOpen(o);
          if (!o) reset();
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle className={wouldEmpty ? "flex items-center gap-2 text-destructive" : undefined}>
              {wouldEmpty && <TriangleAlert className="size-4" />}
              {wouldEmpty ? `This empties the rotation pool` : `Drain ${label}?`}
            </DialogTitle>
            <DialogDescription>
              {wouldEmpty ? (
                <>
                  {label} is the <strong>last PoP in rotation</strong>. Draining it leaves{" "}
                  <strong>no edge serving new traffic</strong> for this zone. Only continue if you intend a
                  full maintenance window.
                </>
              ) : (
                <>
                  New traffic will route to other PoPs over the TTL (~15–60s); existing connections finish and the
                  box stays up (it is <strong>not</strong> deleted). Drained PoPs show as “Draining (maintenance)”.
                </>
              )}
            </DialogDescription>
          </DialogHeader>

          {!wouldEmpty && (
            <div className="space-y-1.5">
              <Label htmlFor="drain-reason">Reason (optional)</Label>
              <Input
                id="drain-reason"
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                placeholder="e.g. kernel upgrade"
              />
            </div>
          )}

          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                setOpen(false);
                reset();
              }}
            >
              Cancel
            </Button>
            <Button
              variant={wouldEmpty ? "destructive" : "default"}
              onClick={() => doDrain(wouldEmpty)}
              disabled={drain.isPending}
            >
              {drain.isPending && <Loader2 className="animate-spin" />}
              {wouldEmpty ? "Drain anyway" : "Drain PoP"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
