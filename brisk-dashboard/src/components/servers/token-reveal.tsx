import * as React from "react";
import { Check, Copy, KeyRound, TriangleAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/** Copy-once agent-token panel. The token is shown exactly once and never
   persisted in app state beyond this component's props. */
export function TokenReveal({ token, className }: { token: string; className?: string }) {
  const [copied, setCopied] = React.useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(token);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      /* clipboard blocked — user can select manually */
    }
  };

  return (
    <div className={cn("rounded-lg border border-warning/40 bg-warning/5 p-3", className)}>
      <div className="mb-2 flex items-center gap-2 text-sm font-medium text-warning">
        <TriangleAlert className="size-4" />
        Copy this agent token now — it won&apos;t be shown again.
      </div>
      <div className="flex items-center gap-2">
        <div className="flex min-w-0 flex-1 items-center gap-2 rounded-md border border-border bg-background px-2.5 py-2">
          <KeyRound className="size-4 shrink-0 text-muted-foreground" />
          <code className="truncate font-mono text-xs">{token}</code>
        </div>
        <Button variant="outline" size="sm" onClick={copy} className="shrink-0">
          {copied ? <Check className="text-success" /> : <Copy />}
          {copied ? "Copied" : "Copy"}
        </Button>
      </div>
    </div>
  );
}
