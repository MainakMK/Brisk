import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { PurgeJob, PurgeInput, PurgeAllInput } from "@/lib/types";

const PENDING = new Set(["pending", "partial"]);

export function isSettled(job: PurgeJob): boolean {
  return !PENDING.has(job.status);
}

/** Purge job history + live status. Polls every 2.5s ONLY while a job is still
   pending/partial; stops once everything is settled (returns false from the
   interval fn — same pattern as the provisioning-log poll in 6.2). */
export function usePurgeJobs(zoneId?: number) {
  const qs = zoneId != null ? `?zone_id=${zoneId}` : "";
  return useQuery({
    queryKey: ["purge-jobs", zoneId ?? "all"],
    queryFn: () => api.get<PurgeJob[]>(`/purge/jobs${qs}`),
    refetchInterval: (q) => {
      const jobs = q.state.data as PurgeJob[] | undefined;
      const anyPending = (jobs ?? []).some((j) => PENDING.has(j.status));
      return anyPending ? 2_500 : false;
    },
    refetchIntervalInBackground: true,
  });
}

export function usePurgeZone(zoneId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: PurgeInput) => api.post<PurgeJob>(`/zones/${zoneId}/purge`, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["purge-jobs"] }),
  });
}

export function usePurgeAll() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: PurgeAllInput) => api.post<PurgeJob>("/purge/all", input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["purge-jobs"] }),
  });
}
