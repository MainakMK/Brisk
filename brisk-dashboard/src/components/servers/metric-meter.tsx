import { cn } from "@/lib/utils";

/** Compact labelled bar for CPU/RAM/disk. Tints amber>75%, red>90%. */
export function MetricMeter({
  label,
  value,
  size = "sm",
  sub,
}: {
  label: string;
  value: number | null;
  size?: "sm" | "lg";
  /** Optional caption under the bar, e.g. "6.3 GB free of 8 GB". */
  sub?: string;
}) {
  const pct = value == null || !isFinite(value) ? null : Math.max(0, Math.min(100, value));
  const color =
    pct == null
      ? "var(--muted-foreground)"
      : pct > 90
        ? "var(--danger)"
        : pct > 75
          ? "var(--warning)"
          : "var(--primary)";
  return (
    <div>
      <div className="mb-1 flex items-center justify-between text-xs">
        <span className="text-muted-foreground">{label}</span>
        <span className="tabular text-foreground">{pct == null ? "—" : Math.round(pct) + "%"}</span>
      </div>
      <div
        className={cn("w-full overflow-hidden rounded-full bg-muted", size === "lg" ? "h-2" : "h-1.5")}
        role="meter"
        aria-label={label}
        aria-valuenow={pct ?? undefined}
        aria-valuemin={0}
        aria-valuemax={100}
      >
        <div
          className="h-full rounded-full transition-all duration-500"
          style={{ width: (pct ?? 0) + "%", background: color }}
        />
      </div>
      {sub && <div className="mt-1 text-[11px] text-muted-foreground">{sub}</div>}
    </div>
  );
}
