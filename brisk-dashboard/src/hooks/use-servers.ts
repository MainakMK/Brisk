import {
  useQuery,
  useMutation,
  useQueryClient,
  keepPreviousData,
} from "@tanstack/react-query";
import { api } from "@/lib/api";
import type {
  Server,
  ServerLive,
  Stats,
  ProvisionLog,
  CreateServerInput,
  CreateServerResponse,
  TokenResponse,
  DrainInput,
  SetRoutingInput,
  SetHealthInput,
  ServerRotation,
  SetServerRoleInput,
} from "@/lib/types";

/** Server list — slower interval; the fleet roster changes rarely. */
export function useServers() {
  return useQuery({
    queryKey: ["servers"],
    queryFn: () => api.get<Server[]>("/servers"),
    refetchInterval: 15_000,
    refetchIntervalInBackground: true,
    placeholderData: keepPreviousData, // keep stale rows during refetch
  });
}

export function useServer(id: number, enabled = true) {
  return useQuery({
    queryKey: ["server", id],
    queryFn: () => api.get<Server>(`/servers/${id}`),
    enabled,
    refetchInterval: 15_000,
    refetchIntervalInBackground: true,
    placeholderData: keepPreviousData,
  });
}

/** Live metrics — fast ~5s tiles; keep updating when tab is backgrounded. */
export function useServerLive(id: number, enabled = true) {
  return useQuery({
    queryKey: ["server-live", id],
    queryFn: () => api.get<ServerLive>(`/servers/${id}/live`),
    enabled,
    refetchInterval: 5_000,
    refetchIntervalInBackground: true,
    placeholderData: keepPreviousData, // don't flash empty between samples
  });
}

/** Hourly trend for the detail chart. */
export function useServerStats(id: number, enabled = true) {
  return useQuery({
    queryKey: ["server-stats", id],
    queryFn: () => api.get<Stats>(`/stats?server_id=${id}&resolution=1m`),
    enabled,
    refetchInterval: 30_000,
    refetchIntervalInBackground: true,
    placeholderData: keepPreviousData,
  });
}

/** Provision log — polls every 2s while `active`, stops once provisioning ends. */
export function useProvisionLog(id: number | null, active: boolean) {
  return useQuery({
    queryKey: ["provision-log", id],
    queryFn: () => api.get<ProvisionLog[]>(`/servers/${id}/provision-log`),
    enabled: id != null,
    refetchInterval: active ? 2_000 : false,
    refetchIntervalInBackground: true,
  });
}

// --- mutations (invalidate the servers list on success) ---

export function useCreateServer() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateServerInput) =>
      api.post<CreateServerResponse>("/servers", input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
    },
  });
}

export function useReprovision(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<TokenResponse>(`/servers/${id}/reprovision`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      qc.invalidateQueries({ queryKey: ["server", id] });
    },
  });
}

export function useRotateToken(id: number) {
  return useMutation({
    mutationFn: () => api.post<TokenResponse>(`/servers/${id}/token/rotate`),
  });
}

export function useDeleteServer() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.del<void>(`/servers/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
    },
  });
}

// --- drain / rotation / per-PoP config (Phase 3 Step 5) ---

/** Invalidate everything that reflects rotation after a drain/health/routing change. */
function invalidateRotation(qc: ReturnType<typeof useQueryClient>) {
  qc.invalidateQueries({ queryKey: ["servers"] });
  qc.invalidateQueries({ queryKey: ["health-status"] });
  qc.invalidateQueries({ queryKey: ["health-config"] });
  qc.invalidateQueries({ queryKey: ["dns-routing"] });
  qc.invalidateQueries({ queryKey: ["dns-records"] });
  qc.invalidateQueries({ queryKey: ["dns-audit"] });
}

/** Effective rotation state + reason for one server. */
export function useServerRotation(id: number, enabled = true) {
  return useQuery({
    queryKey: ["server-rotation", id],
    queryFn: () => api.get<ServerRotation>(`/servers/${id}/rotation`),
    enabled,
    refetchInterval: 7_000,
    refetchIntervalInBackground: true,
    placeholderData: keepPreviousData,
  });
}

export function useDrainServer() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: number } & DrainInput) =>
      api.post<{ server: Server; in_rotation_after: number }>(`/servers/${id}/drain`, body),
    onSuccess: (_d, v) => {
      invalidateRotation(qc);
      qc.invalidateQueries({ queryKey: ["server", v.id] });
      qc.invalidateQueries({ queryKey: ["server-rotation", v.id] });
    },
  });
}

export function useUndrainServer() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.post<{ server: Server }>(`/servers/${id}/undrain`),
    onSuccess: (_d, id) => {
      invalidateRotation(qc);
      qc.invalidateQueries({ queryKey: ["server", id] });
      qc.invalidateQueries({ queryKey: ["server-rotation", id] });
    },
  });
}

export function useDrainRegion() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ region, ...body }: { region: string } & DrainInput) =>
      api.post<{ region: string; drained: number; servers: Server[] }>(
        `/regions/${encodeURIComponent(region)}/drain`,
        body,
      ),
    onSuccess: () => invalidateRotation(qc),
  });
}

export function useUndrainRegion() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (region: string) =>
      api.post<{ region: string; resumed: number; servers: Server[] }>(
        `/regions/${encodeURIComponent(region)}/undrain`,
      ),
    onSuccess: () => invalidateRotation(qc),
  });
}

export function useSetServerRouting(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: SetRoutingInput) => api.post<Server>(`/servers/${id}/routing`, input),
    onSuccess: () => {
      invalidateRotation(qc);
      qc.invalidateQueries({ queryKey: ["server", id] });
    },
  });
}

export function useSetServerHealth(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: SetHealthInput) => api.post<Server>(`/servers/${id}/health`, input),
    onSuccess: () => {
      invalidateRotation(qc);
      qc.invalidateQueries({ queryKey: ["server", id] });
    },
  });
}

/** Set a server's role (edge|shield). A shield is pulled from the geo DNS set, so
    this converges DNS too (invalidateRotation). */
export function useSetServerRole(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: SetServerRoleInput) => api.post<Server>(`/servers/${id}/role`, input),
    onSuccess: () => {
      invalidateRotation(qc);
      qc.invalidateQueries({ queryKey: ["server", id] });
    },
  });
}
