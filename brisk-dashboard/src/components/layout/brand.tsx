import { Zap } from "lucide-react";

/** Brisk wordmark + lightning glyph (Voltage gradient). */
export function Brand() {
  return (
    <div className="flex items-center gap-2">
      <div
        className="grid size-7 place-items-center rounded-lg text-primary-foreground"
        style={{ background: "linear-gradient(135deg, var(--chart-2), var(--primary))" }}
      >
        <Zap className="size-4" fill="currentColor" />
      </div>
      <span className="font-semibold tracking-tight">Brisk</span>
      <span className="ml-1 rounded bg-muted px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-muted-foreground">
        CDN
      </span>
    </div>
  );
}
