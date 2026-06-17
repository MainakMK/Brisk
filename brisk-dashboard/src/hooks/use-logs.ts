import { useQuery, keepPreviousData } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { LogsResponse, LogFilter, LogAnalyticsResponse } from "@/lib/types";

/** Build the /logs query string from a LogFilter (omitting empty values). */
function logsQuery(f: LogFilter): string {
  const p = new URLSearchParams();
  if (f.zone_id) p.set("zone_id", String(f.zone_id));
  if (f.status) p.set("status", f.status);
  if (f.cache) p.set("cache", f.cache);
  if (f.path) p.set("path", f.path);
  if (f.ip) p.set("ip", f.ip);
  if (f.country) p.set("country", f.country);
  if (f.from) p.set("from", f.from);
  if (f.limit) p.set("limit", String(f.limit));
  const s = p.toString();
  return s ? `?${s}` : "";
}

/** Admin cross-tenant request logs (GET /logs). Polls so new lines appear live;
    pass live=false to pause the poll (e.g. while inspecting a frozen view). */
export function useLogs(filter: LogFilter, live = true) {
  return useQuery({
    queryKey: ["logs", filter],
    queryFn: () => api.get<LogsResponse>(`/logs${logsQuery(filter)}`),
    refetchInterval: live ? 5_000 : false,
    placeholderData: keepPreviousData,
  });
}

/** Real per-request aggregates over request_logs (Parts 3+4): origin offload,
    status breakdown, latency percentiles, top paths/countries. Admin scope by
    default; pass a zoneId to scope to one tenant zone. `from` is RFC3339 (the
    window start); the control plane defaults `to` to now. */
export function useLogAnalytics(opts: { zoneId?: number; from?: string } = {}, enabled = true) {
  const p = new URLSearchParams();
  if (opts.from) p.set("from", opts.from);
  const qs = p.toString() ? `?${p.toString()}` : "";
  const path =
    opts.zoneId && opts.zoneId > 0
      ? `/zones/${opts.zoneId}/logs/analytics${qs}`
      : `/logs/analytics${qs}`;
  return useQuery({
    queryKey: ["log-analytics", opts.zoneId ?? 0, opts.from ?? ""],
    queryFn: () => api.get<LogAnalyticsResponse>(path),
    enabled,
    refetchInterval: 30_000,
    placeholderData: keepPreviousData,
  });
}
