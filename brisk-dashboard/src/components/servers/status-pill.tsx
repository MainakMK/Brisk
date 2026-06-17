import type { Server } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { deriveStatus, statusMeta } from "@/components/servers/server-status";
import { cn } from "@/lib/utils";

/** Status as color + label + ARIA (never color alone). */
export function StatusPill({ server }: { server: Server }) {
  const st = deriveStatus(server);
  const meta = statusMeta[st];
  return (
    <Badge variant={meta.badge} aria-label={`Status: ${meta.label}`}>
      <span
        className={cn("inline-block size-1.5 rounded-full", meta.pulse && "animate-pulse")}
        style={{ background: meta.dot }}
        aria-hidden
      />
      {meta.label}
    </Badge>
  );
}
