import { useQuery, keepPreviousData } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type {
  HealthStatusResponse,
  HealthConfigResponse,
  DnsRouting,
  DnsAuditEntry,
  EdgeHealth,
} from "@/lib/types";

/** Per-edge health + rotation state. Polls ~7s (live badges) and keeps the prior
   data during refetch so badges don't flicker. */
export function useHealthStatus() {
  return useQuery({
    queryKey: ["health-status"],
    queryFn: () => api.get<HealthStatusResponse>("/health/status"),
    refetchInterval: 7_000,
    refetchIntervalInBackground: true,
    placeholderData: keepPreviousData,
  });
}

/** Effective per-PoP health config (network defaults + overrides). */
export function useHealthConfig() {
  return useQuery({
    queryKey: ["health-config"],
    queryFn: () => api.get<HealthConfigResponse>("/health/config"),
    refetchInterval: 30_000,
    placeholderData: keepPreviousData,
  });
}

/** Smart-routing config: network mode, region map, effective per-server routing. */
export function useDnsRouting() {
  return useQuery({
    queryKey: ["dns-routing"],
    queryFn: () => api.get<DnsRouting>("/dns/routing"),
    refetchInterval: 15_000,
    placeholderData: keepPreviousData,
  });
}

/** Recent DNS / routing audit events (drain, recovered, failed-over, ...). */
export function useDnsAudit() {
  return useQuery({
    queryKey: ["dns-audit"],
    queryFn: () => api.get<DnsAuditEntry[]>("/dns/audit"),
    refetchInterval: 10_000,
    placeholderData: keepPreviousData,
  });
}

/** Index the health rows by edge_id for quick lookup from a server row. */
export function indexByEdge(edges: EdgeHealth[] | undefined): Record<string, EdgeHealth> {
  const out: Record<string, EdgeHealth> = {};
  for (const e of edges ?? []) out[e.edge_id] = e;
  return out;
}
