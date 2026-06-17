import { useMutation, useQuery, useQueryClient, keepPreviousData } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { CustomDomain, AddDomainInput } from "@/lib/types";

/** A zone's custom domains. Polls while any domain is mid-lifecycle so the status
    chip (Waiting for DNS -> Verifying -> Issuing -> Active) tracks the manager. */
export function useZoneDomains(zoneId: number, enabled = true) {
  return useQuery({
    queryKey: ["zone-domains", zoneId],
    queryFn: () => api.get<CustomDomain[]>(`/zones/${zoneId}/domains`),
    enabled: enabled && Number.isFinite(zoneId),
    placeholderData: keepPreviousData,
    refetchInterval: (q) => {
      const data = q.state.data as CustomDomain[] | undefined;
      const settling = (data ?? []).some((d) => d.status !== "active" || d.last_error);
      return settling ? 8_000 : 30_000;
    },
  });
}

/** Admin: every custom domain across tenants (ops visibility). */
export function useAllCustomDomains(enabled = true) {
  return useQuery({
    queryKey: ["all-domains"],
    queryFn: () => api.get<CustomDomain[]>("/domains"),
    enabled,
    placeholderData: keepPreviousData,
    refetchInterval: 15_000,
  });
}

export function useAddDomain(zoneId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: AddDomainInput) =>
      api.post<CustomDomain>(`/zones/${zoneId}/domains`, input),
    onSuccess: () => invalidate(qc, zoneId),
  });
}

/** "Check now" — runs DNS verification (and issuance if it passes) immediately. */
export function useVerifyDomain(zoneId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (domainId: number) => api.post<CustomDomain>(`/domains/${domainId}/verify`, {}),
    onSuccess: () => invalidate(qc, zoneId),
  });
}

export function useDeleteDomain(zoneId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (domainId: number) => api.del<void>(`/domains/${domainId}`),
    onSuccess: () => invalidate(qc, zoneId),
  });
}

function invalidate(qc: ReturnType<typeof useQueryClient>, zoneId: number) {
  qc.invalidateQueries({ queryKey: ["zone-domains", zoneId] });
  qc.invalidateQueries({ queryKey: ["all-domains"] });
}
