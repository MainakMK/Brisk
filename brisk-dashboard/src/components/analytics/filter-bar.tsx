import { Select } from "@/components/ui/select";
import { RANGES, type RangeKey } from "@/lib/stats";
import { cn } from "@/lib/utils";
import type { Server, Zone } from "@/lib/types";

export interface Filters {
  range: RangeKey;
  serverId: number | "all";
  zoneId: number | "all";
}

export function FilterBar({
  filters,
  servers,
  zones,
  onChange,
  hideZone = false,
}: {
  filters: Filters;
  servers: Server[];
  zones: Zone[];
  onChange: (next: Partial<Filters>) => void;
  /** Hide the zone selector — used by the per-zone Analytics tab where the zone is fixed. */
  hideZone?: boolean;
}) {
  const serverOptions = [
    { value: "all", label: "All PoPs" },
    ...servers.map((s) => ({ value: String(s.id), label: s.edge_id || s.name })),
  ];
  const zoneOptions = [
    { value: "all", label: "All zones" },
    ...zones.map((z) => ({ value: String(z.id), label: z.name })),
  ];

  return (
    <div className="flex flex-wrap items-center gap-3">
      {/* range presets */}
      <div className="inline-flex rounded-md border border-border bg-muted/40 p-0.5">
        {RANGES.map((r) => (
          <button
            key={r.key}
            onClick={() => onChange({ range: r.key })}
            className={cn(
              "rounded px-2.5 py-1 text-xs font-medium transition-colors",
              filters.range === r.key
                ? "bg-primary text-primary-foreground"
                : "text-muted-foreground hover:text-foreground",
            )}
            aria-pressed={filters.range === r.key}
          >
            {r.label}
          </button>
        ))}
      </div>

      <div className="flex items-center gap-2">
        <span className="text-xs text-muted-foreground">PoP</span>
        <Select
          className="h-8 w-36"
          value={String(filters.serverId)}
          onChange={(e) => onChange({ serverId: e.target.value === "all" ? "all" : Number(e.target.value) })}
          options={serverOptions}
          aria-label="Filter by PoP"
        />
      </div>

      {!hideZone && (
        <div className="flex items-center gap-2">
          <span className="text-xs text-muted-foreground">Zone</span>
          <Select
            className="h-8 w-40"
            value={String(filters.zoneId)}
            onChange={(e) => onChange({ zoneId: e.target.value === "all" ? "all" : Number(e.target.value) })}
            options={zoneOptions}
            aria-label="Filter by zone"
          />
        </div>
      )}
    </div>
  );
}
