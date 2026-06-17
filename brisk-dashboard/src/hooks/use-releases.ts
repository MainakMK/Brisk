import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { AgentRelease } from "@/lib/types";

/** Signed agent releases, newest first. */
export function useReleases() {
  return useQuery({
    queryKey: ["releases"],
    queryFn: () => api.get<AgentRelease[]>("/releases"),
    refetchInterval: 30_000,
    refetchIntervalInBackground: true,
  });
}

/** Convenience: the list plus the newest release (or null). The API returns newest-first. */
export function useLatestRelease() {
  const q = useReleases();
  const latest = q.data && q.data.length > 0 ? q.data[0] : null;
  return { ...q, latest };
}
