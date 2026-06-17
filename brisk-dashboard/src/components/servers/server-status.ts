import type { Server, ServerStatus } from "@/lib/types";

export type DerivedStatus = "online" | "provisioning" | "offline" | "pending" | "disabled";

const STALE_MS = 60_000; // no heartbeat in 60s => treat as offline even if status=online

/** Derive a display status: a stale last_seen overrides a lingering "online". */
export function deriveStatus(s: Server, now: number = Date.now()): DerivedStatus {
  const raw = (s.status ?? "").toLowerCase() as ServerStatus;
  if (raw === "provisioning") return "provisioning";
  if (raw === "pending") return "pending";
  if (raw === "disabled") return "disabled";
  if (raw === "online") {
    if (!s.last_seen) return "offline";
    const age = now - new Date(s.last_seen).getTime();
    if (Number.isFinite(age) && age > STALE_MS) return "offline";
    return "online";
  }
  return "offline";
}

export const statusMeta: Record<
  DerivedStatus,
  { label: string; badge: "success" | "warning" | "danger" | "muted"; dot: string; pulse?: boolean }
> = {
  online: { label: "Online", badge: "success", dot: "var(--success)" },
  provisioning: { label: "Provisioning", badge: "warning", dot: "var(--warning)", pulse: true },
  offline: { label: "Offline", badge: "danger", dot: "var(--danger)" },
  pending: { label: "Pending", badge: "muted", dot: "var(--muted-foreground)" },
  disabled: { label: "Disabled", badge: "muted", dot: "var(--muted-foreground)" },
};
