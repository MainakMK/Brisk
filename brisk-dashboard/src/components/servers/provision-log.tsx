import * as React from "react";
import { Loader2, Terminal } from "lucide-react";
import { useProvisionLog } from "@/hooks/use-servers";
import { cn } from "@/lib/utils";

/** Terminal-style provisioning log. Polls every 2s while `active` (provisioning),
   stops once the server is online/failed. Auto-scrolls to the newest line. */
export function ProvisionLogPanel({
  serverId,
  active,
  className,
}: {
  serverId: number | null;
  active: boolean;
  className?: string;
}) {
  const { data, isLoading, isError } = useProvisionLog(serverId, active);
  const boxRef = React.useRef<HTMLDivElement>(null);
  const lines = data ?? [];

  // Keep the newest line in view by scrolling the log box's OWN scroll container —
  // NOT scrollIntoView, which would scroll the whole page down to the log (and fight
  // the route-level scroll-to-top, landing the page in the middle on open).
  React.useEffect(() => {
    const box = boxRef.current;
    if (box) box.scrollTop = box.scrollHeight;
  }, [lines.length]);

  return (
    <div className={cn("overflow-hidden rounded-lg border border-border bg-[#0a0812]", className)}>
      <div className="flex items-center gap-2 border-b border-border px-3 py-2 text-xs text-muted-foreground">
        <Terminal className="size-3.5" />
        <span>Provisioning log</span>
        {active && (
          <span className="ml-auto flex items-center gap-1 text-warning">
            <Loader2 className="size-3 animate-spin" />
            streaming
          </span>
        )}
      </div>
      <div ref={boxRef} className="max-h-80 overflow-y-auto p-3 font-mono text-[11px] leading-relaxed">
        {isLoading && lines.length === 0 && (
          <div className="text-muted-foreground">connecting…</div>
        )}
        {isError && lines.length === 0 && (
          <div className="text-danger">couldn&apos;t load provisioning log</div>
        )}
        {!isLoading && lines.length === 0 && !isError && (
          <div className="text-muted-foreground">no provisioning output yet</div>
        )}
        {lines.map((l) => (
          <div key={l.id} className="flex gap-2">
            <span className="shrink-0 text-slate-600">
              {new Date(l.ts).toLocaleTimeString(undefined, { hour12: false })}
            </span>
            <span
              className={cn(
                "whitespace-pre-wrap break-all",
                l.level === "error" ? "text-danger" : "text-slate-300",
              )}
            >
              {l.message}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}
