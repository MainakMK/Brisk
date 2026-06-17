import * as React from "react";
import { TrendingUp, TrendingDown } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

// Shared analytics presentation primitives, used by the top-level Analytics page AND the
// per-zone Analytics tab so both render identically.

/** Human-readable bytes (B → PB). */
export function bytesH(n: number): string {
  if (!isFinite(n) || n <= 0) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB", "PB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return (v >= 100 ? v.toFixed(0) : v >= 10 ? v.toFixed(1) : v.toFixed(2)) + " " + u[i];
}

/** Relative %-change vs the previous period (null when prev has no usable baseline). */
export function delta(cur: number, prev: number): { pct: number } | null {
  if (!isFinite(prev) || prev <= 0) return null;
  return { pct: ((cur - prev) / prev) * 100 };
}

/** Percentage-POINT change for ratio metrics (hit ratio etc.). */
export function deltaPts(cur: number, prev: number): { pct: number } | null {
  if (!isFinite(prev) || prev <= 0) return null;
  return { pct: (cur - prev) * 100 };
}

export function Kpi({
  label,
  value,
  unit,
  loading,
  delta,
  tone,
}: {
  label: string;
  value: React.ReactNode;
  unit?: string;
  loading?: boolean;
  delta?: { pct: number } | null;
  tone?: "good" | "warn" | "bad";
}) {
  const toneColor = tone === "good" ? "text-success" : tone === "warn" ? "text-warning" : tone === "bad" ? "text-danger" : "";
  return (
    <Card className="p-4">
      <div className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">{label}</div>
      {loading ? (
        <Skeleton className="mt-3 h-7 w-20" />
      ) : (
        <div className="mt-1.5 flex items-end gap-1">
          <span className={cn("tabular text-2xl font-semibold leading-none", toneColor)}>{value}</span>
          {unit && <span className="mb-0.5 text-sm text-muted-foreground">{unit}</span>}
        </div>
      )}
      {!loading && delta && isFinite(delta.pct) && (
        <div className={cn("mt-2 flex items-center gap-1 text-xs tabular", delta.pct >= 0 ? "text-success" : "text-danger")}>
          {delta.pct >= 0 ? <TrendingUp className="size-3" /> : <TrendingDown className="size-3" />}
          {Math.abs(delta.pct).toFixed(1)}% <span className="text-muted-foreground">vs prev</span>
        </div>
      )}
    </Card>
  );
}

export function ChartCard({
  title,
  subtitle,
  children,
  loading,
  empty,
  action,
  className,
}: {
  title: string;
  subtitle?: string;
  children: React.ReactNode;
  loading?: boolean;
  empty?: boolean;
  action?: React.ReactNode;
  className?: string;
}) {
  return (
    <Card className={className}>
      <CardHeader className="flex-row items-start justify-between">
        <div>
          <CardTitle>{title}</CardTitle>
          {subtitle && <p className="text-xs text-muted-foreground">{subtitle}</p>}
        </div>
        {action}
      </CardHeader>
      <CardContent>
        {loading ? (
          <Skeleton className="h-[240px] w-full" />
        ) : empty ? (
          <div className="flex h-[240px] flex-col items-center justify-center gap-1 rounded-lg border border-dashed border-border text-center text-sm text-muted-foreground">
            <span>No data for this range / filter.</span>
            <span className="text-xs">Try a wider range or a different PoP/zone.</span>
          </div>
        ) : (
          children
        )}
      </CardContent>
    </Card>
  );
}
