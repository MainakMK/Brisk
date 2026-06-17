import {
  useQuery,
  useQueries,
  useMutation,
  useQueryClient,
  keepPreviousData,
} from "@tanstack/react-query";
import { api } from "@/lib/api";
import { useServers } from "@/hooks/use-servers";
import type {
  Zone,
  CacheRule,
  CreateZoneInput,
  UpdateZoneInput,
  CreateRuleInput,
  Server,
  SetZoneShieldInput,
  HeaderTransform,
  CreateHeaderTransformInput,
  CacheSettings,
  SetZoneHotlinkInput,
  SetZoneErrorPageInput,
  SetZoneBlockedIPsInput,
  SetZoneAccessFlagsInput,
} from "@/lib/types";

/** Zone list. Zones change rarely — moderate poll keeps config_version fresh. */
export function useZones() {
  return useQuery({
    queryKey: ["zones"],
    queryFn: () => api.get<Zone[]>("/zones"),
    refetchInterval: 30_000,
    placeholderData: keepPreviousData,
  });
}

/** One zone (includes its rules). */
export function useZone(id: number, enabled = true) {
  return useQuery({
    queryKey: ["zone", id],
    queryFn: () => api.get<Zone>(`/zones/${id}`),
    enabled: enabled && Number.isFinite(id),
    placeholderData: keepPreviousData,
  });
}

export function useZoneRules(id: number, enabled = true) {
  return useQuery({
    queryKey: ["zone-rules", id],
    queryFn: () => api.get<CacheRule[]>(`/zones/${id}/rules`),
    enabled: enabled && Number.isFinite(id),
    placeholderData: keepPreviousData,
  });
}

export function useServerZones(serverId: number, enabled = true) {
  return useQuery({
    queryKey: ["server-zones", serverId],
    queryFn: () => api.get<Zone[]>(`/servers/${serverId}/zones`),
    enabled: enabled && Number.isFinite(serverId),
  });
}

/** Inverse lookup: which servers serve each zone (no zone->servers endpoint,
   so we union every server's /zones). Returns a map zoneId -> Server[]. */
export function useZoneAssignments() {
  const servers = useServers();
  const ids = (servers.data ?? []).map((s) => s.id);
  const zoneQueries = useQueries({
    queries: ids.map((sid) => ({
      queryKey: ["server-zones", sid],
      queryFn: () => api.get<Zone[]>(`/servers/${sid}/zones`),
    })),
  });

  const map = new Map<number, Server[]>();
  (servers.data ?? []).forEach((srv, i) => {
    const zones = zoneQueries[i]?.data ?? [];
    for (const z of zones) {
      const arr = map.get(z.id) ?? [];
      arr.push(srv);
      map.set(z.id, arr);
    }
  });

  return {
    map,
    servers: servers.data ?? [],
    isLoading: servers.isLoading || zoneQueries.some((q) => q.isLoading),
    isError: servers.isError || zoneQueries.some((q) => q.isError),
  };
}

// --- zone mutations ---

export function useCreateZone() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateZoneInput) => api.post<Zone>("/zones", input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["zones"] }),
  });
}

export function useUpdateZone(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: UpdateZoneInput) => api.put<Zone>(`/zones/${id}`, input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["zones"] });
      qc.invalidateQueries({ queryKey: ["zone", id] });
    },
  });
}

/** Delete a zone. For a zone serving on live edges the server REQUIRES `confirm`
    to equal the cdn_hostname (412 otherwise) — the type-the-hostname guard that
    tears the zone down across all PoPs (whole-zone purge + vhost removal). */
export function useDeleteZone() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, confirm }: { id: number; confirm?: string }) =>
      api.del<void>(`/zones/${id}${confirm ? `?confirm=${encodeURIComponent(confirm)}` : ""}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["zones"] }),
  });
}

/** Toggle per-zone origin shield (Phase 4 Step 3). Bumps config_version server-side
    so assigned edges re-pull and switch the zone's upstream (shield <-> origin). */
export function useSetZoneShield(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: SetZoneShieldInput) => api.post<Zone>(`/zones/${id}/shield`, input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["zones"] });
      qc.invalidateQueries({ queryKey: ["zone", id] });
    },
  });
}

/** Replace a zone's Cache Settings (Bunny-style controls). Bumps config_version
    server-side so assigned edges re-pull + reload with the new cache directives. */
export function useSetCacheSettings(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CacheSettings) => api.put<Zone>(`/zones/${id}/cache-settings`, input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["zones"] });
      qc.invalidateQueries({ queryKey: ["zone", id] });
    },
  });
}

/** Set a zone's Hotlink Protection (Referer allowlist). Bumps config_version
    server-side so assigned edges re-pull + reload with the new valid_referers. */
export function useSetZoneHotlink(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: SetZoneHotlinkInput) => api.put<Zone>(`/zones/${id}/hotlink`, input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["zones"] });
      qc.invalidateQueries({ queryKey: ["zone", id] });
    },
  });
}

/** Set a zone's custom 502/504 error page (Bunny-style). Empty html clears it. Bumps
    config_version server-side so assigned edges re-pull + reload with the new page. */
export function useSetZoneErrorPage(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: SetZoneErrorPageInput) => api.put<Zone>(`/zones/${id}/error-page`, input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["zones"] });
      qc.invalidateQueries({ queryKey: ["zone", id] });
    },
  });
}

/** Set a zone's Blocked-IP denylist (Bunny-style). Empty clears it. Bumps config_version
    server-side so assigned edges re-pull + reload with the new deny list. */
export function useSetZoneBlockedIPs(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: SetZoneBlockedIPsInput) => api.put<Zone>(`/zones/${id}/blocked-ips`, input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["zones"] });
      qc.invalidateQueries({ queryKey: ["zone", id] });
    },
  });
}

/** Set a zone's access toggles (block root path / block POST). Bumps config_version
    server-side so assigned edges re-pull + reload with the new gated rules. */
export function useSetZoneAccessFlags(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: SetZoneAccessFlagsInput) => api.put<Zone>(`/zones/${id}/access-flags`, input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["zones"] });
      qc.invalidateQueries({ queryKey: ["zone", id] });
    },
  });
}

// --- rule mutations (each bumps the zone config_version server-side) ---

export function useCreateRule(zoneId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateRuleInput) => api.post<CacheRule>(`/zones/${zoneId}/rules`, input),
    onSuccess: () => invalidateZone(qc, zoneId),
  });
}

export function useUpdateRule(zoneId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ ruleId, input }: { ruleId: number; input: CreateRuleInput }) =>
      api.put<CacheRule>(`/zones/${zoneId}/rules/${ruleId}`, input),
    onSuccess: () => invalidateZone(qc, zoneId),
  });
}

/** Atomic reorder (Phase 4 Step 6) — ruleIds in the new order; no delete+recreate. */
export function useReorderRules(zoneId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (ruleIds: number[]) =>
      api.post<CacheRule[]>(`/zones/${zoneId}/rules/reorder`, { rule_ids: ruleIds }),
    onSuccess: () => invalidateZone(qc, zoneId),
  });
}

export function useDeleteRule(zoneId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (ruleId: number) => api.del<void>(`/zones/${zoneId}/rules/${ruleId}`),
    onSuccess: () => invalidateZone(qc, zoneId),
  });
}

/** Inverse lookup (Phase 4 Step 6): which servers serve this zone (server-side). */
export function useZoneServers(zoneId: number, enabled = true) {
  return useQuery({
    queryKey: ["zone-servers", zoneId],
    queryFn: () => api.get<Server[]>(`/zones/${zoneId}/servers`),
    enabled: enabled && Number.isFinite(zoneId),
    placeholderData: keepPreviousData,
  });
}

// --- header transforms (Phase 4 Step 5; each change bumps config_version) ---

export function useZoneTransforms(id: number, enabled = true) {
  return useQuery({
    queryKey: ["zone-transforms", id],
    queryFn: () => api.get<HeaderTransform[]>(`/zones/${id}/header-transforms`),
    enabled: enabled && Number.isFinite(id),
    placeholderData: keepPreviousData,
  });
}

export function useCreateTransform(zoneId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateHeaderTransformInput) =>
      api.post<HeaderTransform>(`/zones/${zoneId}/header-transforms`, input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["zone-transforms", zoneId] });
      qc.invalidateQueries({ queryKey: ["zone", zoneId] });
    },
  });
}

export function useDeleteTransform(zoneId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.del<void>(`/zones/${zoneId}/header-transforms/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["zone-transforms", zoneId] });
      qc.invalidateQueries({ queryKey: ["zone", zoneId] });
    },
  });
}

// --- assignment mutations ---

export function useAssignZone() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ serverId, zoneId }: { serverId: number; zoneId: number }) =>
      api.post<void>(`/servers/${serverId}/zones`, { zone_id: zoneId }),
    onSuccess: (_d, { serverId }) => {
      qc.invalidateQueries({ queryKey: ["server-zones", serverId] });
    },
  });
}

export function useUnassignZone() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ serverId, zoneId }: { serverId: number; zoneId: number }) =>
      api.del<void>(`/servers/${serverId}/zones/${zoneId}`),
    onSuccess: (_d, { serverId }) => {
      qc.invalidateQueries({ queryKey: ["server-zones", serverId] });
    },
  });
}

function invalidateZone(qc: ReturnType<typeof useQueryClient>, zoneId: number) {
  qc.invalidateQueries({ queryKey: ["zones"] });
  qc.invalidateQueries({ queryKey: ["zone", zoneId] });
  qc.invalidateQueries({ queryKey: ["zone-rules", zoneId] });
}
