import type { ReactNode } from "react";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

/** Hero KPI card: uppercase label + big tabular number + unit + optional footer.
   No fabricated deltas/sparklines — values are real or skeleton. */
export function KpiCard({
  label,
  value,
  unit,
  footer,
  loading,
  className,
}: {
  label: string;
  value: ReactNode;
  unit?: string;
  footer?: ReactNode;
  loading?: boolean;
  className?: string;
}) {
  return (
    <Card className={cn("p-5", className)}>
      <div className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
        {label}
      </div>
      {loading ? (
        <Skeleton className="mt-3 h-8 w-24" />
      ) : (
        <div className="mt-2 flex items-end gap-1.5">
          <span className="tabular text-3xl font-semibold leading-none">{value}</span>
          {unit && <span className="mb-0.5 text-sm text-muted-foreground">{unit}</span>}
        </div>
      )}
      {footer && (
        <div className="mt-3 text-xs text-muted-foreground">
          {loading ? <Skeleton className="h-3 w-20" /> : footer}
        </div>
      )}
    </Card>
  );
}
