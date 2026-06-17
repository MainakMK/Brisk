import * as React from "react";
import { toast } from "sonner";
import { ListOrdered, Plus, ArrowUp, ArrowDown, Trash2, Loader2, Info } from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { AddRuleDialog } from "@/components/zones/rule-form";
import { matchTypeLabels, actionLabels, sortRules } from "@/components/zones/zone-meta";
import { useZoneRules, useCreateRule, useDeleteRule, useReorderRules } from "@/hooks/use-zones";
import type { CacheRule, CreateRuleInput, RuleAction, RuleMatchType } from "@/lib/types";

export function RulesEditor({ zoneId }: { zoneId: number }) {
  const { data, isLoading, isError, refetch } = useZoneRules(zoneId);
  const createRule = useCreateRule(zoneId);
  const deleteRule = useDeleteRule(zoneId);
  const [addOpen, setAddOpen] = React.useState(false);
  const [reordering, setReordering] = React.useState(false);
  const [deletingId, setDeletingId] = React.useState<number | null>(null);

  const rules = React.useMemo(() => sortRules(data ?? []), [data]);

  const onCreate = (input: Omit<CreateRuleInput, "priority">) => {
    createRule.mutate(
      { ...input, priority: rules.length }, // append to the end
      {
        onSuccess: () => {
          toast.success("Rule added", { description: "config_version bumped · propagates in ~15s." });
          setAddOpen(false);
        },
        onError: (e) => toast.error("Couldn't add rule", { description: (e as Error).message }),
      },
    );
  };

  const onDelete = (rule: CacheRule) => {
    setDeletingId(rule.id);
    deleteRule.mutate(rule.id, {
      onSuccess: () => toast.success("Rule deleted", { description: "config_version bumped." }),
      onError: (e) => toast.error("Couldn't delete rule", { description: (e as Error).message }),
      onSettled: () => setDeletingId(null),
    });
  };

  // Atomic reorder (Phase 4 Step 6): one POST /rules/reorder — no delete+recreate,
  // so rule IDs no longer churn on every move.
  const reorder = useReorderRules(zoneId);
  const move = (index: number, dir: -1 | 1) => {
    const target = index + dir;
    if (target < 0 || target >= rules.length) return;
    const next = [...rules];
    [next[index], next[target]] = [next[target], next[index]];
    setReordering(true);
    reorder.mutate(
      next.map((r) => r.id),
      {
        onSuccess: () => toast.success("Rules reordered", { description: "config_version bumped · propagates in ~15s." }),
        onError: (e) => toast.error("Reorder failed", { description: (e as Error).message }),
        onSettled: () => {
          setReordering(false);
          refetch();
        },
      },
    );
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          Priority-ordered condition → action rules. Evaluated top-down (rule 1 first).
        </p>
        <Button size="sm" onClick={() => setAddOpen(true)} disabled={reordering}>
          <Plus /> Add rule
        </Button>
      </div>

      <div className="flex items-start gap-2 rounded-lg border border-border bg-secondary/30 p-3 text-xs text-muted-foreground">
        <Info className="mt-0.5 size-3.5 shrink-0" />
        <span>
          Rules are <span className="font-medium text-foreground">enforced at the edge</span> by the Lua layer
          (~15s after a change). Evaluated in priority order, <span className="font-medium text-foreground">first match wins</span>;
          when none match, the Phase-1 video/static/HTML caching defaults apply. A rule changes{" "}
          <span className="font-medium text-foreground">how long</span> or{" "}
          <span className="font-medium text-foreground">whether</span> something caches, forces a download, or redirects.
        </span>
      </div>

      {isError && (
        <Card className="border-destructive/40 bg-destructive/5">
          <CardContent className="flex items-center justify-between p-4 text-sm">
            <span className="text-destructive">Couldn&apos;t load rules.</span>
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              Retry
            </Button>
          </CardContent>
        </Card>
      )}

      {isLoading && (
        <div className="space-y-2">
          {[0, 1].map((i) => (
            <Skeleton key={i} className="h-14 w-full" />
          ))}
        </div>
      )}

      {!isLoading && rules.length === 0 && (
        <Card className="flex flex-col items-center justify-center gap-2 border-dashed py-12 text-center">
          <div className="grid size-10 place-items-center rounded-full bg-muted text-muted-foreground">
            <ListOrdered className="size-5" />
          </div>
          <h3 className="text-sm font-medium">No cache rules yet</h3>
          <p className="mx-auto max-w-md text-sm text-muted-foreground">
            Edge rules tune caching per request. Example: cache{" "}
            <span className="font-mono text-foreground">/assets/*</span> for 30 days, or bypass cache for{" "}
            <span className="font-mono text-foreground">/api/</span>.
          </p>
          <Button size="sm" className="mt-1" onClick={() => setAddOpen(true)}>
            <Plus /> Add your first rule
          </Button>
        </Card>
      )}

      {rules.length > 0 && (
        <ul className="space-y-2">
          {rules.map((r, i) => (
            <li key={r.id}>
              <Card className="flex items-center gap-3 p-3">
                <div className="flex flex-col items-center gap-0.5">
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-6"
                    aria-label="Move up"
                    disabled={i === 0 || reordering}
                    onClick={() => move(i, -1)}
                  >
                    <ArrowUp className="size-3.5" />
                  </Button>
                  <span className="text-[10px] tabular text-muted-foreground" title="evaluation order">
                    {i + 1}
                  </span>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-6"
                    aria-label="Move down"
                    disabled={i === rules.length - 1 || reordering}
                    onClick={() => move(i, 1)}
                  >
                    <ArrowDown className="size-3.5" />
                  </Button>
                </div>

                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-1.5 text-sm">
                    <Badge variant="outline">{matchTypeLabels[r.match_type as RuleMatchType] ?? r.match_type}</Badge>
                    <span className="font-mono text-xs text-foreground">{r.match_value}</span>
                    <span className="text-muted-foreground">→</span>
                    <Badge>{actionLabels[r.action as RuleAction] ?? r.action}</Badge>
                    {r.action_value && <span className="font-mono text-xs text-muted-foreground">{r.action_value}</span>}
                  </div>
                </div>

                <Button
                  variant="ghost"
                  size="icon"
                  aria-label="Delete rule"
                  className="text-muted-foreground hover:text-destructive"
                  disabled={reordering}
                  onClick={() => onDelete(r)}
                >
                  {deletingId === r.id ? <Loader2 className="animate-spin" /> : <Trash2 />}
                </Button>
              </Card>
            </li>
          ))}
        </ul>
      )}

      {reordering && (
        <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <Loader2 className="size-3.5 animate-spin" /> Rewriting rule order…
        </p>
      )}

      <AddRuleDialog open={addOpen} onOpenChange={setAddOpen} onCreate={onCreate} pending={createRule.isPending} />
    </div>
  );
}
