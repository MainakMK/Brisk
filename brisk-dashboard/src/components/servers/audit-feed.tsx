import { History, Rocket, Pause, Play, X, Undo2, UploadCloud } from "lucide-react";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { useAudit } from "@/hooks/use-audit";
import type { AuditEntry } from "@/lib/types";

/** Compact, append-only feed of deploy/pause/rollback/upload actions. Renders nothing until
 *  there's at least one entry (so it stays invisible on a fleet that has never deployed). */
export function AuditFeed() {
  const { data } = useAudit(20);
  const entries = data ?? [];
  if (entries.length === 0) return null;

  return (
    <Card>
      <CardHeader className="space-y-0 pb-2">
        <CardTitle className="flex items-center gap-2 text-sm text-muted-foreground">
          <History className="size-4" /> Deploy history
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-1">
        {entries.map((e) => (
          <Row key={e.id} e={e} />
        ))}
      </CardContent>
    </Card>
  );
}

function Row({ e }: { e: AuditEntry }) {
  return (
    <div className="flex items-center gap-3 text-sm">
      <ActionIcon action={e.action} />
      <span className="font-medium">{label(e.action)}</span>
      {e.subject && <span className="text-muted-foreground tabular">{e.subject}</span>}
      {e.details && <span className="truncate text-xs text-muted-foreground">{e.details}</span>}
      <span className="ml-auto whitespace-nowrap text-xs text-muted-foreground" title={new Date(e.at).toLocaleString()}>
        {timeAgo(e.at)}
      </span>
    </div>
  );
}

function label(action: string): string {
  switch (action) {
    case "rollout.start":
      return "Deploy started";
    case "rollout.paused":
      return "Rollout paused";
    case "rollout.running":
      return "Rollout resumed";
    case "rollout.cancelled":
      return "Rollout cancelled";
    case "rollout.rollback":
      return "Rolled back to";
    case "release.upload":
      return "Release uploaded";
    default:
      return action;
  }
}

function ActionIcon({ action }: { action: string }) {
  const cls = "size-4";
  switch (action) {
    case "rollout.start":
      return <Rocket className={`${cls} text-primary`} />;
    case "rollout.paused":
      return <Pause className={`${cls} text-warning`} />;
    case "rollout.running":
      return <Play className={`${cls} text-success`} />;
    case "rollout.cancelled":
      return <X className={`${cls} text-danger`} />;
    case "rollout.rollback":
      return <Undo2 className={`${cls} text-danger`} />;
    case "release.upload":
      return <UploadCloud className={`${cls} text-muted-foreground`} />;
    default:
      return <History className={`${cls} text-muted-foreground`} />;
  }
}

function timeAgo(iso: string): string {
  const then = new Date(iso).getTime();
  const s = Math.max(0, Math.round((Date.now() - then) / 1000));
  if (s < 60) return `${s}s ago`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.round(h / 24)}d ago`;
}
