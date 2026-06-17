import type { CacheRule, RuleAction, RuleMatchType, Server, Zone } from "@/lib/types";

/** A zone is "protected" if it's served by at least one edge (live traffic).
   Editing/deleting/unassigning it changes what a real edge serves. */
export function isProtected(zoneId: number, assignments: Map<number, Server[]>): boolean {
  return (assignments.get(zoneId)?.length ?? 0) > 0;
}

export const matchTypeLabels: Record<RuleMatchType, string> = {
  path_prefix: "Path prefix",
  extension: "Extension",
  regex: "Regex",
};

export const actionLabels: Record<RuleAction, string> = {
  override_cache_ttl: "Override cache TTL",
  bypass_cache: "Bypass cache",
  force_download: "Force download",
  redirect: "Redirect",
};

/** Which actions need an action_value, and how to label that field. */
export const actionValueField: Record<
  RuleAction,
  { needed: boolean; label?: string; placeholder?: string; hint?: string }
> = {
  override_cache_ttl: {
    needed: true,
    label: "Cache TTL",
    placeholder: "30d",
    hint: "Duration (e.g. 2s, 30d). Origin Cache-Control still applies.",
  },
  bypass_cache: { needed: false },
  force_download: { needed: false },
  redirect: { needed: true, label: "Redirect target", placeholder: "https://example.com/$1", hint: "Destination URL." },
};

/** Human one-liner for a rule, e.g. "Extension m3u8 → Override cache TTL (2s)". */
export function describeRule(r: CacheRule): string {
  const mt = matchTypeLabels[r.match_type as RuleMatchType] ?? r.match_type;
  const act = actionLabels[r.action as RuleAction] ?? r.action;
  const val = r.action_value ? ` (${r.action_value})` : "";
  return `${mt} ${r.match_value} → ${act}${val}`;
}

/** Sort rules by evaluation order (priority ASC, then id). */
export function sortRules(rules: CacheRule[]): CacheRule[] {
  return [...rules].sort((a, b) => a.priority - b.priority || a.id - b.id);
}

export function zoneStatusVariant(status: string): "success" | "muted" {
  return status === "active" ? "success" : "muted";
}

export function originLabel(z: Zone): string {
  return z.origin_url.replace(/^https?:\/\//, "");
}
