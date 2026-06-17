import { Pause, Play, X, Undo2, Loader2, CheckCircle2, Clock, AlertTriangle, MinusCircle } from "lucide-react";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  useActiveRollout,
  usePauseRollout,
  useResumeRollout,
  useCancelRollout,
  useRollback,
} from "@/hooks/use-rollouts";
import type { RolloutTarget, RolloutTargetState, RolloutStatus } from "@/lib/types";

/** Live per-PoP progress for the in-flight rollout. Renders nothing when no rollout is active.
 *  Polls every 2s via useActiveRollout so each PoP's state advances in near-real-time. */
export function RolloutProgress() {
  const { data } = useActiveRollout();
  const pause = usePauseRollout();
  const resume = useResumeRollout();
  const cancel = useCancelRollout();
  const rollback = useRollback();

  if (!data) return null;
  const { rollout, targets } = data;
  const id = rollout.id;

  const done = targets.filter((t) => t.state === "done").length;
  const failed = targets.some((t) => t.state === "failed");
  const total = targets.length;

  // A split fleet = some targets done, some not yet — surfaced so the operator knows the
  // network is mid-version (matches the "X of N PoPs" warning on the version card).
  const split = done > 0 && done < total;

  const running = rollout.status === "running";
  const paused = rollout.status === "paused";
  const scheduled = rollout.status === "scheduled";
  const live = running || paused || scheduled;

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-3 space-y-0">
        <div className="space-y-1">
          <CardTitle className="flex items-center gap-2 text-base">
            Rolling out {rollout.release_version}
            <StatusBadge status={rollout.status} />
            {split && (
              <Badge variant="warning" className="text-[10px] uppercase">
                split fleet
              </Badge>
            )}
          </CardTitle>
          <CardDescription>
            {done}/{total} PoP{total !== 1 ? "s" : ""} done · {rollout.soak_seconds}s soak each, one at a time
            {rollout.error_reason ? ` · ${rollout.error_reason}` : ""}
            {scheduled && rollout.scheduled_at ? ` · starts ${new Date(rollout.scheduled_at).toLocaleString()}` : ""}
          </CardDescription>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {running && (
            <Button variant="outline" size="sm" onClick={() => pause.mutate(id)} disabled={pause.isPending}>
              <Pause /> Pause
            </Button>
          )}
          {paused && (
            <Button variant="outline" size="sm" onClick={() => resume.mutate(id)} disabled={resume.isPending}>
              <Play /> Resume
            </Button>
          )}
          {live && (
            <Button variant="outline" size="sm" onClick={() => cancel.mutate(id)} disabled={cancel.isPending}>
              <X /> Cancel
            </Button>
          )}
          {(live || failed || split) && (
            <Button
              variant="destructive"
              size="sm"
              onClick={() => rollback.mutate({ id })}
              disabled={rollback.isPending}
              title="Roll the affected PoPs back to their previous version"
            >
              {rollback.isPending ? <Loader2 className="animate-spin" /> : <Undo2 />} Roll back
            </Button>
          )}
        </div>
      </CardHeader>
      <CardContent className="space-y-1.5">
        {targets.map((t) => (
          <TargetRow key={t.edge_id} t={t} />
        ))}
      </CardContent>
    </Card>
  );
}

function TargetRow({ t }: { t: RolloutTarget }) {
  const soakLeft =
    t.state === "soaking" && t.soak_until ? Math.max(0, Math.round((new Date(t.soak_until).getTime() - Date.now()) / 1000)) : null;
  return (
    <div className="flex items-center gap-3 rounded-md px-2 py-1.5 text-sm">
      <StateIcon state={t.state} />
      <span className="font-medium">{t.edge_id}</span>
      <span className="text-xs text-muted-foreground tabular">
        {t.from_version || "—"} → {t.to_version}
      </span>
      <span className="ml-auto text-xs">
        {t.state === "soaking" && soakLeft != null ? (
          <span className="text-warning">soaking · {soakLeft}s left</span>
        ) : t.state === "failed" ? (
          <span className="text-danger">{t.error_reason || "failed"}</span>
        ) : (
          <StateLabel state={t.state} />
        )}
      </span>
    </div>
  );
}

function StateIcon({ state }: { state: RolloutTargetState }) {
  switch (state) {
    case "done":
      return <CheckCircle2 className="size-4 text-success" />;
    case "in_progress":
      return <Loader2 className="size-4 animate-spin text-primary" />;
    case "soaking":
      return <Clock className="size-4 text-warning" />;
    case "failed":
      return <AlertTriangle className="size-4 text-danger" />;
    case "skipped":
      return <MinusCircle className="size-4 text-muted-foreground" />;
    default:
      return <Clock className="size-4 text-muted-foreground" />;
  }
}

function StateLabel({ state }: { state: RolloutTargetState }) {
  const map: Record<string, string> = {
    queued: "queued",
    in_progress: "updating…",
    done: "done",
    skipped: "skipped (offline)",
  };
  return <span className="text-muted-foreground">{map[state] ?? state}</span>;
}

function StatusBadge({ status }: { status: RolloutStatus }) {
  const variant =
    status === "done"
      ? "success"
      : status === "failed"
        ? "danger"
        : status === "paused" || status === "scheduled"
          ? "warning"
          : status === "cancelled"
            ? "secondary"
            : "default";
  return (
    <Badge variant={variant as "success" | "danger" | "warning" | "secondary" | "default"} className="text-[10px] uppercase">
      {status}
    </Badge>
  );
}
