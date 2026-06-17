import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { RolloutDetail, StartRolloutInput } from "@/lib/types";

/** The single active rollout (scheduled/running/paused) + its targets, or null. Polls fast
 *  while a rollout is live so the progress panel updates in near-real-time. */
export function useActiveRollout() {
  return useQuery({
    queryKey: ["rollout-active"],
    queryFn: () => api.get<RolloutDetail | null>("/rollouts/active"),
    refetchInterval: 2_000,
    refetchIntervalInBackground: true,
  });
}

function invalidate(qc: ReturnType<typeof useQueryClient>) {
  qc.invalidateQueries({ queryKey: ["rollout-active"] });
  qc.invalidateQueries({ queryKey: ["servers"] });
}

/** Start a rollout (Deploy). 409 if one is already in progress. */
export function useStartRollout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: StartRolloutInput) => api.post<{ id: number }>("/rollouts", input),
    onSuccess: () => invalidate(qc),
  });
}

export function usePauseRollout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.post<{ id: number; status: string }>(`/rollouts/${id}/pause`),
    onSuccess: () => invalidate(qc),
  });
}

export function useResumeRollout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.post<{ id: number; status: string }>(`/rollouts/${id}/resume`),
    onSuccess: () => invalidate(qc),
  });
}

export function useCancelRollout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.post<{ id: number; status: string }>(`/rollouts/${id}/cancel`),
    onSuccess: () => invalidate(qc),
  });
}

/** Roll the affected PoPs back. version optional — defaults server-side to the from-version. */
export function useRollback() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, version }: { id: number; version?: string }) =>
      api.post<{ id: number; version: string }>(`/rollouts/${id}/rollback`, { version: version ?? "" }),
    onSuccess: () => invalidate(qc),
  });
}
