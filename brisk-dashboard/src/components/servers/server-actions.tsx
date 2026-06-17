import * as React from "react";
import { useNavigate } from "react-router-dom";
import { MoreVertical, Eye, RefreshCw, KeyRound, Trash2, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
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
import { TokenReveal } from "@/components/servers/token-reveal";
import { useReprovision, useRotateToken, useDeleteServer } from "@/hooks/use-servers";
import type { Server } from "@/lib/types";

type ActiveDialog = null | "reprovision" | "rotate" | "delete";

/** Kebab menu shared by tiles + detail. Owns the confirm/token dialogs. */
export function ServerActions({
  server,
  showView = true,
  onDeleted,
  size = "icon",
}: {
  server: Server;
  showView?: boolean;
  onDeleted?: () => void;
  size?: "icon" | "sm";
}) {
  const navigate = useNavigate();
  const [dialog, setDialog] = React.useState<ActiveDialog>(null);
  const [rotatedToken, setRotatedToken] = React.useState<string | null>(null);

  const reprovision = useReprovision(server.id);
  const rotate = useRotateToken(server.id);
  const del = useDeleteServer();

  const doReprovision = () => {
    reprovision.mutate(undefined, {
      onSuccess: () => {
        toast.success(`Reprovisioning ${server.edge_id || server.name}`, {
          description: "Streaming the provisioning log on the detail page.",
        });
        setDialog(null);
        navigate(`/servers/${server.id}`);
      },
      onError: (e) => toast.error("Reprovision failed", { description: (e as Error).message }),
    });
  };

  const doRotate = () => {
    rotate.mutate(undefined, {
      onSuccess: (res) => setRotatedToken(res.agent_token),
      onError: (e) => toast.error("Token rotation failed", { description: (e as Error).message }),
    });
  };

  const doDelete = () => {
    del.mutate(server.id, {
      onSuccess: () => {
        toast.success(`Deleted ${server.edge_id || server.name}`);
        setDialog(null);
        onDeleted?.();
      },
      onError: (e) => toast.error("Delete failed", { description: (e as Error).message }),
    });
  };

  const closeRotate = () => {
    setDialog(null);
    setRotatedToken(null); // drop the token from state when the dialog closes
  };

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size={size} aria-label="Server actions">
            <MoreVertical />
            {size === "sm" && <span>Actions</span>}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          {showView && (
            <DropdownMenuItem onSelect={() => navigate(`/servers/${server.id}`)}>
              <Eye /> View detail
            </DropdownMenuItem>
          )}
          <DropdownMenuItem onSelect={() => setDialog("reprovision")}>
            <RefreshCw /> Reprovision
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => setDialog("rotate")}>
            <KeyRound /> Rotate token
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onSelect={() => setDialog("delete")}
            className="text-destructive focus:bg-destructive/10"
          >
            <Trash2 /> Delete
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      {/* Reprovision confirm */}
      <Dialog open={dialog === "reprovision"} onOpenChange={(o) => !o && setDialog(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Reprovision {server.edge_id || server.name}?</DialogTitle>
            <DialogDescription>
              Re-runs the SSH bootstrap over the installed control-plane key and issues a fresh
              agent token. The edge keeps serving from its last config during the process.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDialog(null)}>
              Cancel
            </Button>
            <Button onClick={doReprovision} disabled={reprovision.isPending}>
              {reprovision.isPending && <Loader2 className="animate-spin" />}
              Reprovision
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Rotate token (show once) */}
      <Dialog open={dialog === "rotate"} onOpenChange={(o) => !o && closeRotate()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Rotate agent token</DialogTitle>
            <DialogDescription>
              {rotatedToken
                ? "The old token is now revoked. The new token is pushed to the agent."
                : `Revoke ${server.edge_id || server.name}'s current token and mint a new one. The old token stops working immediately.`}
            </DialogDescription>
          </DialogHeader>
          {rotatedToken ? (
            <TokenReveal token={rotatedToken} />
          ) : (
            <DialogFooter>
              <Button variant="outline" onClick={closeRotate}>
                Cancel
              </Button>
              <Button onClick={doRotate} disabled={rotate.isPending}>
                {rotate.isPending && <Loader2 className="animate-spin" />}
                Rotate token
              </Button>
            </DialogFooter>
          )}
          {rotatedToken && (
            <DialogFooter>
              <Button onClick={closeRotate}>Done</Button>
            </DialogFooter>
          )}
        </DialogContent>
      </Dialog>

      {/* Delete confirm */}
      <Dialog open={dialog === "delete"} onOpenChange={(o) => !o && setDialog(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete {server.edge_id || server.name}?</DialogTitle>
            <DialogDescription>
              Removes the server from the control plane. This does not wipe the box; it stops
              tracking the edge and revokes its tokens. This can&apos;t be undone here.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDialog(null)}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={doDelete} disabled={del.isPending}>
              {del.isPending && <Loader2 className="animate-spin" />}
              Delete server
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
