import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { Overview } from "@/lib/types";

/** Live network overview. Polls every 10s (spec: refresh overview ~10s). */
export function useOverview() {
  return useQuery({
    queryKey: ["overview"],
    queryFn: () => api.get<Overview>("/overview"),
    refetchInterval: 10_000,
    refetchIntervalInBackground: true,
  });
}

// useServers now lives with the other server hooks; re-exported for the
// Overview page's PoP-status rail.
export { useServers } from "@/hooks/use-servers";
