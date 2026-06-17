import * as React from "react";
import { Rocket, CheckCircle2, AlertTriangle } from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { useServers } from "@/hooks/use-servers";
import { useLatestRelease } from "@/hooks/use-releases";
import { useActiveRollout } from "@/hooks/use-rollouts";
import { DeployDialog } from "@/components/servers/deploy-dialog";

/** Version-aware Deploy panel for the Servers page: shows running vs available agent version,
 *  and a Deploy button that lights up only when a newer signed release exists. Greyed while a
 *  rollout is already in progress (the live progress panel shows below it). */
export function AgentVersionCard() {
  const servers = useServers().data ?? [];
  const { latest } = useLatestRelease();
  const active = useActiveRollout().data ?? null;
  const [open, setOpen] = React.useState(false);

  // Online edges are the deploy targets (and what "up to date" is measured against).
  const online = servers.filter((s) => s.status === "online");
  const running = online.map((s) => s.agent_version || "—");
  const distinct = Array.from(new Set(running));
  const onLatest = latest ? online.filter((s) => s.agent_version === latest.version).length : 0;
  const total = online.length;

  const rolloutLive =
    active != null &&
    (active.rollout.status === "running" || active.rollout.status === "paused" || active.rollout.status === "scheduled");
  const noRelease = !latest;
  const allUpToDate = !!latest && total > 0 && onLatest === total;
  const split = !!latest && onLatest > 0 && onLatest < total;
  const newAvailable = !!latest && onLatest < total; // someone is behind

  let title: React.ReactNode;
  let sub: React.ReactNode;
  let icon = <Rocket className="size-4 text-muted-foreground" />;

  if (noRelease) {
    title = <span className="tabular font-semibold">No agent release yet</span>;
    sub = (
      <span className="text-muted-foreground">
        Push one via CI (git tag) or <code className="text-xs">briskctl release push</code>.
      </span>
    );
  } else if (allUpToDate) {
    icon = <CheckCircle2 className="size-4 text-success" />;
    title = <span className="tabular font-semibold">brisk-agent {latest!.version}</span>;
    sub = <span className="text-success">✓ all {total} PoP{total !== 1 ? "s" : ""} up to date</span>;
  } else if (split) {
    icon = <AlertTriangle className="size-4 text-warning" />;
    title = (
      <span className="tabular font-semibold">
        {onLatest} of {total} PoPs on {latest!.version}
      </span>
    );
    sub = (
      <span className="text-warning">
        {total - onLatest} still on {distinct.filter((v) => v !== latest!.version).join(", ") || "older"} — finish the rollout
      </span>
    );
  } else {
    // everyone behind → a new version is available
    title = (
      <span className="tabular font-semibold">
        <span className="text-muted-foreground">{distinct.filter((v) => v !== latest!.version).join(", ") || "current"}</span>
        <span className="mx-1">→</span>
        <span className="text-primary">{latest!.version}</span>
      </span>
    );
    sub = <span className="font-medium text-primary">available</span>;
  }

  const canDeploy = !!latest && newAvailable && !rolloutLive && total > 0;

  return (
    <>
      <Card>
        <CardContent className="flex flex-wrap items-center gap-x-4 gap-y-2 py-4">
          <div className="flex items-center gap-2">
            {icon}
            <div className="text-sm">
              <div>{title}</div>
              <div className="text-xs">{sub}</div>
            </div>
          </div>
          {!noRelease && newAvailable && !allUpToDate && (
            <span className="rounded-full border border-primary/40 bg-primary/10 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider text-primary">
              new
            </span>
          )}
          <div className="ml-auto">
            <Button
              onClick={() => setOpen(true)}
              disabled={!canDeploy}
              title={rolloutLive ? "A rollout is already in progress" : undefined}
            >
              <Rocket />
              {allUpToDate ? "Deploy" : split ? "Finish rollout" : latest ? `Deploy ${latest.version}` : "Deploy"}
            </Button>
          </div>
        </CardContent>
      </Card>

      {latest && <DeployDialog open={open} onOpenChange={setOpen} version={latest.version} servers={online} />}
    </>
  );
}
