import { Info } from "lucide-react";
import { PageHeader } from "@/components/layout/page-header";
import { PurgePanel } from "@/components/purge/purge-panel";
import { PurgeJobsTable } from "@/components/purge/jobs-table";

export default function PurgePage() {
  return (
    <div className="space-y-5">
      <PageHeader title="Purge" description="Instant, job-tracked cache invalidation." />

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-[minmax(0,420px)_1fr]">
        <div>
          <PurgePanel />
        </div>

        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-medium">Purge jobs</h2>
          </div>
          <PurgeJobsTable />

          <div className="flex items-start gap-2 rounded-lg border border-border bg-secondary/30 p-3 text-xs text-muted-foreground">
            <Info className="mt-0.5 size-3.5 shrink-0" />
            <span>
              Purge fans out over NATS JetStream to every edge serving the zone — fast, but not always perfectly
              simultaneous across many edges. An offline edge gets the purge on reconnect (durable), so a job can sit{" "}
              <span className="font-medium text-foreground">partial</span> until every edge acks. Progress is shown as{" "}
              <span className="font-medium text-foreground">edges&nbsp;done / total</span>.
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}
