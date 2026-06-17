import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { AuditEntry } from "@/lib/types";

/** The deploy/pause/rollback/upload audit trail, newest first. */
export function useAudit(limit = 20) {
  return useQuery({
    queryKey: ["audit", limit],
    queryFn: () => api.get<AuditEntry[]>(`/audit?limit=${limit}`),
    refetchInterval: 15_000,
  });
}
