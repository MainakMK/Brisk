import {
  useQuery,
  useMutation,
  useQueryClient,
  keepPreviousData,
} from "@tanstack/react-query";
import { api } from "@/lib/api";
import type {
  WAFConfig,
  WAFSettings,
  WAFCustomRule,
  WAFRateLimit,
  SetZoneWAFInput,
  CreateWAFRuleInput,
  CreateWAFRateLimitInput,
  SecurityEventsResponse,
  SecurityEventSummary,
} from "@/lib/types";

/** GET /zones/{id}/waf — settings + ordered custom rules + rate limits. */
export function useZoneWAF(id: number, enabled = true) {
  return useQuery({
    queryKey: ["zone-waf", id],
    queryFn: () => api.get<WAFConfig>(`/zones/${id}/waf`),
    enabled: enabled && Number.isFinite(id),
    placeholderData: keepPreviousData,
  });
}

/** Per-zone firewall log (newest first). Polls so new blocks appear live while
    tuning. action filters to a single action (block/detect/log/challenge). */
export function useZoneSecurityEvents(
  id: number,
  opts: { action?: string; enabled?: boolean } = {},
) {
  const action = opts.action ?? "";
  return useQuery({
    queryKey: ["zone-sec-events", id, action],
    queryFn: () =>
      api.get<SecurityEventsResponse>(
        `/zones/${id}/security-events${action ? `?action=${encodeURIComponent(action)}` : ""}`,
      ),
    enabled: (opts.enabled ?? true) && Number.isFinite(id),
    refetchInterval: 15_000,
    placeholderData: keepPreviousData,
  });
}

/** Admin cross-tenant overview (top attacked zones, top blocked IPs). */
export function useSecurityEventSummary(enabled = true) {
  return useQuery({
    queryKey: ["sec-summary"],
    queryFn: () => api.get<SecurityEventSummary>(`/security-events/summary`),
    enabled,
    refetchInterval: 30_000,
    placeholderData: keepPreviousData,
  });
}

// --- mutations (each bumps the zone config_version server-side -> edges re-pull) ---

export function useSetZoneWAF(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: SetZoneWAFInput) => api.put<WAFSettings>(`/zones/${id}/waf`, input),
    onSuccess: () => invalidateWAF(qc, id),
  });
}

export function useCreateWAFRule(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateWAFRuleInput) => api.post<WAFCustomRule>(`/zones/${id}/waf/rules`, input),
    onSuccess: () => invalidateWAF(qc, id),
  });
}

export function useDeleteWAFRule(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (ruleId: number) => api.del<void>(`/zones/${id}/waf/rules/${ruleId}`),
    onSuccess: () => invalidateWAF(qc, id),
  });
}

export function useCreateWAFRateLimit(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateWAFRateLimitInput) =>
      api.post<WAFRateLimit>(`/zones/${id}/waf/ratelimits`, input),
    onSuccess: () => invalidateWAF(qc, id),
  });
}

export function useDeleteWAFRateLimit(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (rlId: number) => api.del<void>(`/zones/${id}/waf/ratelimits/${rlId}`),
    onSuccess: () => invalidateWAF(qc, id),
  });
}

function invalidateWAF(qc: ReturnType<typeof useQueryClient>, id: number) {
  qc.invalidateQueries({ queryKey: ["zone-waf", id] });
  qc.invalidateQueries({ queryKey: ["zone", id] });
  qc.invalidateQueries({ queryKey: ["zones"] });
}
