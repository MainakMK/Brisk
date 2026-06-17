import { useQueries } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { useServers } from "@/hooks/use-servers";
import { rangeWindow, resolutionFor, mergeSeries, downsample, type RangeKey } from "@/lib/stats";
import type { Stats, SeriesPoint, Server } from "@/lib/types";

export interface AnalyticsFilters {
  range: RangeKey;
  serverId: number | "all";
  zoneId: number | "all";
}

export interface StatsResult {
  points: SeriesPoint[];
  isLoading: boolean;
  isError: boolean;
  isFetching: boolean;
  servers: Server[];
  /** number of PoPs that returned at least one bucket in this window */
  reportingServers: number;
}

/** Fetch a time-series for the selected filters.

   `/stats` is per-server (no network aggregate endpoint), so "All PoPs" fans
   out one query per server and merges client-side. `anchor` (ms) freezes the
   from/to window so query keys are stable between renders; advance it to slide
   the window / refresh. */
export function useStatsSeries(filters: AnalyticsFilters, anchor: number): StatsResult {
  const servers = useServers();
  const { range, serverId, zoneId } = filters;
  const { from, to } = rangeWindow(range, anchor);
  const resolution = resolutionFor(range);

  const targetIds =
    serverId === "all" ? (servers.data ?? []).map((s) => s.id) : [serverId];

  const zoneParam = zoneId !== "all" ? `&zone_id=${zoneId}` : "";

  const queries = useQueries({
    queries: targetIds.map((id) => ({
      queryKey: ["stats", id, zoneId, from, to, resolution] as const,
      queryFn: () =>
        api.get<Stats>(
          `/stats?server_id=${id}&resolution=${resolution}${zoneParam}&from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
        ),
      staleTime: 20_000,
    })),
  });

  const merged = mergeSeries(queries.map((q) => q.data?.points ?? []));
  const points = downsample(merged);
  const reportingServers = queries.filter((q) => (q.data?.points?.length ?? 0) > 0).length;

  return {
    points,
    isLoading: servers.isLoading || (targetIds.length > 0 && queries.some((q) => q.isLoading)),
    isError: servers.isError || queries.some((q) => q.isError),
    isFetching: queries.some((q) => q.isFetching),
    servers: servers.data ?? [],
    reportingServers,
  };
}
