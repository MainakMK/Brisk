import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { DnsStatus, DnsRecord, DnsLock, AddDnsRecordInput, TlsStatus } from "@/lib/types";

export function useDnsStatus() {
  return useQuery({
    queryKey: ["dns-status"],
    queryFn: () => api.get<DnsStatus>("/dns/status"),
    refetchInterval: 30_000,
  });
}

/** Managed-TLS status: every control-plane-issued cert (both wildcards), metadata
   only. Surfaces the apex `*.a2zjav.com` + the tenant `*.cdn.a2zjav.com` certs. */
export function useTlsStatus() {
  return useQuery({
    queryKey: ["tls-status"],
    queryFn: () => api.get<TlsStatus>("/tls/status"),
    refetchInterval: 60_000,
  });
}

export function useDnsRecords(enabled = true) {
  return useQuery({
    queryKey: ["dns-records"],
    queryFn: () => api.get<DnsRecord[]>("/dns/records"),
    enabled,
    refetchInterval: 30_000,
  });
}

/** Lock state. Polls every 1s while a pending unlock is counting down so the
   countdown stays live; stops once locked or unlocked. */
export function useDnsLock() {
  return useQuery({
    queryKey: ["dns-lock"],
    queryFn: () => api.get<DnsLock>("/dns/lock"),
    refetchInterval: (q) => ((q.state.data as DnsLock | undefined)?.state === "pending" ? 1_000 : false),
    refetchIntervalInBackground: true,
  });
}

export function useAddDnsRecord() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: AddDnsRecordInput) => api.post<{ created: boolean; record: DnsRecord }>("/dns/records", input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["dns-records"] });
      qc.invalidateQueries({ queryKey: ["dns-status"] });
    },
  });
}

export function useDeleteDnsRecord() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.del<void>(`/dns/records/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["dns-records"] });
      qc.invalidateQueries({ queryKey: ["dns-status"] });
    },
  });
}

// --- lock controls ---

export function useRequestUnlock() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<DnsLock>("/dns/lock/request-unlock"),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["dns-lock"] }),
  });
}

export function useCancelUnlock() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<DnsLock>("/dns/lock/cancel"),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["dns-lock"] }),
  });
}

export function useRelock() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<DnsLock>("/dns/lock/relock"),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["dns-lock"] }),
  });
}

export function useSetUnlockDelay() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (seconds: number) => api.post<DnsLock>("/dns/lock/delay", { seconds }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["dns-lock"] }),
  });
}
