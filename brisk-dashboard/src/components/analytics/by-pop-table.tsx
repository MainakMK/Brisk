import { useQueries } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "@/lib/api";
import { rangeWindow, resolutionFor, summarize, rangeSeconds, type RangeKey } from "@/lib/stats";
import { bps, formatInt, formatRatioPct } from "@/lib/format";
import { Skeleton } from "@/components/ui/skeleton";
import type { Server, Stats } from "@/lib/types";

/** Per-PoP requests / bandwidth / hit-ratio for the range. Reuses the exact
   query keys from useStatsSeries, so when "All PoPs" is selected these are
   cache hits (no extra network). */
export function ByPopTable({
  servers,
  range,
  zoneId,
  anchor,
}: {
  servers: Server[];
  range: RangeKey;
  zoneId: number | "all";
  anchor: number;
}) {
  const { from, to } = rangeWindow(range, anchor);
  const resolution = resolutionFor(range);
  const zoneParam = zoneId !== "all" ? `&zone_id=${zoneId}` : "";

  const queries = useQueries({
    queries: servers.map((s) => ({
      queryKey: ["stats", s.id, zoneId, from, to, resolution] as const,
      queryFn: () =>
        api.get<Stats>(
          `/stats?server_id=${s.id}&resolution=${resolution}${zoneParam}&from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
        ),
      staleTime: 20_000,
    })),
  });

  const rows = servers
    .map((s, i) => {
      const points = queries[i]?.data?.points ?? [];
      const sum = summarize(points, rangeSeconds(range));
      return { server: s, sum, has: points.length > 0 };
    })
    .sort((a, b) => b.sum.totalRequests - a.sum.totalRequests);

  const loading = queries.some((q) => q.isLoading);

  if (loading) return <Skeleton className="h-32 w-full" />;

  const withData = rows.filter((r) => r.has);
  if (withData.length === 0) {
    return <p className="py-6 text-center text-sm text-muted-foreground">No per-PoP data in this range.</p>;
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-border text-left text-xs uppercase tracking-wider text-muted-foreground">
            <th className="py-2 font-medium">PoP</th>
            <th className="py-2 text-right font-medium">Requests</th>
            <th className="py-2 text-right font-medium">Egress</th>
            <th className="py-2 text-right font-medium">Hit ratio</th>
          </tr>
        </thead>
        <tbody>
          {withData.map(({ server, sum }) => (
            <tr key={server.id} className="border-b border-border/60 last:border-0">
              <td className="py-2">
                <Link to={`/servers/${server.id}`} className="font-medium hover:text-primary">
                  {server.edge_id || server.name}
                </Link>
                <span className="ml-2 text-xs text-muted-foreground">{server.region}</span>
              </td>
              <td className="py-2 text-right tabular">{formatInt(sum.totalRequests)}</td>
              <td className="py-2 text-right tabular">{bps((sum.totalBytes * 8) / rangeSeconds(range))}</td>
              <td className="py-2 text-right tabular">{formatRatioPct(sum.hitRatio)}%</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
