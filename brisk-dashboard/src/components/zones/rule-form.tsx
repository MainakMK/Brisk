import * as React from "react";
import { Loader2 } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { actionValueField } from "@/components/zones/zone-meta";
import { validateMatchValue, validateTTL, validateRequired } from "@/lib/validators";
import type { CreateRuleInput, RuleAction, RuleMatchType } from "@/lib/types";

const MATCH_OPTIONS = [
  { value: "path_prefix", label: "Path prefix" },
  { value: "extension", label: "Extension" },
  { value: "regex", label: "Regex" },
];
const ACTION_OPTIONS = [
  { value: "override_cache_ttl", label: "Override cache TTL" },
  { value: "bypass_cache", label: "Bypass cache" },
  { value: "force_download", label: "Force download" },
  { value: "redirect", label: "Redirect" },
];

const MATCH_PLACEHOLDER: Record<string, string> = {
  path_prefix: "/assets/",
  extension: "m3u8",
  regex: "\\.(jpg|png|webp)$",
};

export function AddRuleDialog({
  open,
  onOpenChange,
  onCreate,
  pending,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  onCreate: (input: Omit<CreateRuleInput, "priority">) => void;
  pending: boolean;
}) {
  const [matchType, setMatchType] = React.useState<RuleMatchType>("path_prefix");
  const [matchValue, setMatchValue] = React.useState("");
  const [action, setAction] = React.useState<RuleAction>("override_cache_ttl");
  const [actionValue, setActionValue] = React.useState("");
  const [errors, setErrors] = React.useState<Record<string, string>>({});

  const valueField = actionValueField[action];

  React.useEffect(() => {
    if (!open) {
      setMatchType("path_prefix");
      setMatchValue("");
      setAction("override_cache_ttl");
      setActionValue("");
      setErrors({});
    }
  }, [open]);

  const submit = () => {
    const errs: Record<string, string> = {};
    const mv = validateMatchValue(matchType, matchValue);
    if (mv) errs.match_value = mv;
    if (valueField.needed) {
      const av = action === "override_cache_ttl" ? validateTTL(actionValue) : validateRequired(actionValue);
      if (av) errs.action_value = av;
    }
    setErrors(errs);
    if (Object.keys(errs).length > 0) return;
    onCreate({
      match_type: matchType,
      match_value: matchValue.trim(),
      action,
      action_value: valueField.needed ? actionValue.trim() : null,
    });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add cache rule</DialogTitle>
          <DialogDescription>
            Match requests, then apply an action. Rules evaluate top-down by priority.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-3">
            <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Match</div>
            <div className="grid grid-cols-[140px_1fr] gap-3">
              <div className="space-y-1">
                <Label>Type</Label>
                <Select
                  value={matchType}
                  onChange={(e) => setMatchType(e.target.value as RuleMatchType)}
                  options={MATCH_OPTIONS}
                />
              </div>
              <div className="space-y-1">
                <Label>Value</Label>
                <Input
                  value={matchValue}
                  onChange={(e) => setMatchValue(e.target.value)}
                  placeholder={MATCH_PLACEHOLDER[matchType]}
                  className="font-mono text-xs"
                />
                {errors.match_value && <p className="text-xs text-destructive">{errors.match_value}</p>}
              </div>
            </div>
          </div>

          <div className="space-y-3">
            <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Action</div>
            <div className="space-y-1">
              <Label>Then</Label>
              <Select value={action} onChange={(e) => setAction(e.target.value as RuleAction)} options={ACTION_OPTIONS} />
            </div>
            {valueField.needed && (
              <div className="space-y-1">
                <Label>{valueField.label}</Label>
                <Input
                  value={actionValue}
                  onChange={(e) => setActionValue(e.target.value)}
                  placeholder={valueField.placeholder}
                  className="font-mono text-xs"
                />
                {errors.action_value ? (
                  <p className="text-xs text-destructive">{errors.action_value}</p>
                ) : valueField.hint ? (
                  <p className="text-xs text-muted-foreground">{valueField.hint}</p>
                ) : null}
              </div>
            )}
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={pending}>
            {pending && <Loader2 className="animate-spin" />}
            Add rule
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
