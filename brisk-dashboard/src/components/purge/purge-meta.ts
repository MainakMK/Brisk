import type { PurgeJob, PurgeStatus, PurgeType } from "@/lib/types";

export const statusVariant: Record<string, "success" | "warning" | "danger" | "muted"> = {
  done: "success",
  pending: "warning",
  partial: "warning",
  failed: "danger",
};

export function purgeStatusVariant(s: PurgeStatus): "success" | "warning" | "danger" | "muted" {
  return statusVariant[s] ?? "muted";
}

export const typeLabels: Record<string, string> = {
  url: "URL",
  prefix: "Prefix",
  zone: "Whole zone",
  all: "Everything",
};

export function purgeTypeLabel(t: PurgeType | string): string {
  return typeLabels[t] ?? t;
}

export function progressPct(j: PurgeJob): number {
  if (j.edges_total <= 0) return 100;
  return Math.round((j.edges_done / j.edges_total) * 100);
}
