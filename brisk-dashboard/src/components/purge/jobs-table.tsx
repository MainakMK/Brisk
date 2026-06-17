import { Trash2, Loader2 } from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { purgeStatusVariant, purgeTypeLabel, progressPct } from "@/components/purge/purge-meta";
import { usePurgeJobs } from "@/hooks/use-purge";
import { timeAgo } from "@/lib/format";
import type { PurgeJob } from "@/lib/types";

/** Purge history + live status. Polling is owned by usePurgeJobs (stops when
   nothing is pending). `zoneId` scopes to one zone (used on zone detail). `limit`
   caps how many of the MOST RECENT jobs are shown (default 6) — the DB keeps the
   full audit, this just trims the on-screen list. */
export function PurgeJobsTable({ zoneId, limit = 6 }: { zoneId?: number; limit?: number }) {
  const { data, isLoading, isError, isFetching, refetch } = usePurgeJobs(zoneId);
  const allJobs = data ?? [];
  const live = allJobs.some((j) => j.status === "pending" || j.status === "partial");
  // Newest-first, then keep only the most recent `limit` (the API already returns
  // DESC, but sort defensively so the cap is always the latest purges).
  const jobs = [...allJobs]
    .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
    .slice(0, limit);

  if (isLoading) {
    return (
      <div className="space-y-2">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-12 w-full" />
        ))}
      </div>
    );
  }

  if (isError) {
    return (
      <Card className="border-destructive/40 bg-destructive/5">
        <CardContent className="flex items-center justify-between p-4 text-sm">
          <span className="text-destructive">Couldn&apos;t load purge jobs.</span>
          <Button variant="outline" size="sm" onClick={() => refetch()}>
            Retry
          </Button>
        </CardContent>
      </Card>
    );
  }

  if (jobs.length === 0) {
    return (
      <Card className="flex flex-col items-center gap-2 border-dashed py-12 text-center">
        <div className="grid size-10 place-items-center rounded-full bg-muted text-muted-foreground">
          <Trash2 className="size-5" />
        </div>
        <h3 className="text-sm font-medium">No purges yet</h3>
        <p className="max-w-sm text-sm text-muted-foreground">
          Purges you submit appear here with live per-edge progress.
        </p>
      </Card>
    );
  }

  return (
    <div className="space-y-2">
      {live && (
        <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <Loader2 className={isFetching ? "size-3 animate-spin" : "size-3"} />
          Live — refreshing while purges are in flight
        </div>
      )}
      <Card className="overflow-hidden">
        <div className="hidden grid-cols-[auto_1fr_auto_auto_auto] items-center gap-3 border-b border-border px-4 py-2.5 text-xs font-medium uppercase tracking-wider text-muted-foreground md:grid">
          <div>Type</div>
          <div>Target</div>
          <div>Status</div>
          <div className="text-center">Edges</div>
          <div className="text-right">When</div>
        </div>
        <ul className="divide-y divide-border">
          {jobs.map((j) => (
            <JobRow key={j.id} job={j} />
          ))}
        </ul>
      </Card>
      {allJobs.length > jobs.length && (
        <p className="text-right text-[11px] text-muted-foreground">
          Showing the {jobs.length} most recent of {allJobs.length} purges.
        </p>
      )}
    </div>
  );
}

function JobRow({ job }: { job: PurgeJob }) {
  const pct = progressPct(job);
  return (
    <li className="grid grid-cols-1 items-center gap-2 px-4 py-3 md:grid-cols-[auto_1fr_auto_auto_auto] md:gap-3">
      <div>
        <Badge variant="outline">{purgeTypeLabel(job.type)}</Badge>
      </div>
      <div className="min-w-0 truncate font-mono text-xs text-foreground" title={job.target}>
        {job.target}
      </div>
      <div>
        <Badge variant={purgeStatusVariant(job.status)} className="capitalize">
          {job.status}
        </Badge>
      </div>
      <div className="md:w-28">
        <div className="flex items-center justify-between text-[11px] text-muted-foreground tabular">
          <span>
            {job.edges_done}/{job.edges_total}
          </span>
          <span>{pct}%</span>
        </div>
        <div className="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-muted">
          <div
            className="h-full rounded-full transition-all"
            style={{
              width: `${pct}%`,
              background: job.status === "failed" ? "var(--danger)" : job.status === "done" ? "var(--success)" : "var(--warning)",
            }}
          />
        </div>
      </div>
      <div className="text-right text-xs text-muted-foreground tabular">{timeAgo(job.created_at)}</div>
    </li>
  );
}
