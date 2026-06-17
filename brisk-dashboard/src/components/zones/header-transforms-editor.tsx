import * as React from "react";
import { ArrowRightLeft, Plus, Trash2, Loader2, Info } from "lucide-react";
import { toast } from "sonner";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { useZoneTransforms, useCreateTransform, useDeleteTransform } from "@/hooks/use-zones";
import type { HeaderTransform, TransformPhase, TransformOp, TransformMatchType } from "@/lib/types";

// Mirror of the control-plane managed-header deny-list (api/transforms.go): the UI
// blocks these so tenants don't clobber Brisk-managed / TLS / framing headers.
function denied(phase: string, header: string): boolean {
  const h = header.trim().toLowerCase();
  if (h.startsWith("x-brisk-")) return true;
  if (["content-length", "transfer-encoding", "connection", ""].includes(h)) return true;
  if (phase === "response" && ["server", "strict-transport-security", "content-encoding", "date"].includes(h)) return true;
  if (phase === "request" && ["host", "x-forwarded-proto", "x-forwarded-for"].includes(h)) return true;
  return false;
}

/** Per-zone request/response header transforms (Phase 4 Step 5 — Lua-enforced). */
export function HeaderTransformsEditor({ zoneId }: { zoneId: number }) {
  const { data, isLoading } = useZoneTransforms(zoneId);
  const create = useCreateTransform(zoneId);
  const del = useDeleteTransform(zoneId);

  const [phase, setPhase] = React.useState<TransformPhase>("response");
  const [op, setOp] = React.useState<TransformOp>("set");
  const [header, setHeader] = React.useState("");
  const [value, setValue] = React.useState("");
  const [matchType, setMatchType] = React.useState<TransformMatchType>("all");
  const [matchValue, setMatchValue] = React.useState("");

  const transforms = data ?? [];

  const add = () => {
    if (!header.trim()) {
      toast.error("Enter a header name");
      return;
    }
    if (denied(phase, header)) {
      toast.error("Managed header", { description: "Brisk manages X-Brisk-*, Server, HSTS, Host and framing headers." });
      return;
    }
    if (op === "set" && !value.trim()) {
      toast.error("Enter a value for set");
      return;
    }
    create.mutate(
      {
        priority: transforms.length,
        phase,
        op,
        header: header.trim(),
        value: op === "set" ? value.trim() : undefined,
        match_type: matchType,
        match_value: matchType === "all" ? undefined : matchValue.trim(),
        enabled: true,
      },
      {
        onSuccess: () => {
          setHeader("");
          setValue("");
          setMatchValue("");
          toast.success("Transform added", { description: "config_version bumped · applies at the edge in ~15s." });
        },
        onError: (e) => toast.error("Add failed", { description: (e as Error).message }),
      },
    );
  };

  return (
    <Card>
      <CardHeader className="flex-row items-center gap-2">
        <ArrowRightLeft className="size-4 text-primary" />
        <CardTitle>Header transforms</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
          <Info className="mt-0.5 size-3.5 shrink-0" />
          Add / remove / override headers on the request sent upstream or the response sent to the
          client — enforced at the edge by Lua. First applicable per header. Managed headers
          (X-Brisk-*, Server, HSTS, Host) can't be overridden. ~15s propagation.
        </p>

        {isLoading ? (
          <Skeleton className="h-16 w-full" />
        ) : transforms.length === 0 ? (
          <p className="text-xs text-muted-foreground">No header transforms.</p>
        ) : (
          <ul className="space-y-1.5">
            {transforms.map((t: HeaderTransform) => (
              <li key={t.id} className="flex items-center gap-2 rounded-md border border-border px-3 py-1.5 text-sm">
                <Badge variant="outline" className="capitalize">{t.phase}</Badge>
                <Badge variant={t.op === "remove" ? "danger" : "secondary"}>{t.op}</Badge>
                <code className="text-xs">{t.header}</code>
                {t.op === "set" && t.value && <span className="text-muted-foreground">= {t.value}</span>}
                {t.match_type !== "all" && (
                  <span className="text-[11px] text-muted-foreground">
                    if {t.match_type} {t.match_value}
                  </span>
                )}
                <Button size="icon" variant="ghost" className="ml-auto" aria-label="Delete transform" onClick={() => del.mutate(t.id)}>
                  <Trash2 className="size-4 text-danger" />
                </Button>
              </li>
            ))}
          </ul>
        )}

        <div className="grid grid-cols-2 gap-2 rounded-md border border-dashed border-border p-3 sm:grid-cols-6">
          <Field label="Phase">
            <Select value={phase} onChange={(e) => setPhase(e.target.value as TransformPhase)}
              options={[{ value: "response", label: "Response" }, { value: "request", label: "Request" }]} />
          </Field>
          <Field label="Op">
            <Select value={op} onChange={(e) => setOp(e.target.value as TransformOp)}
              options={[{ value: "set", label: "Set" }, { value: "remove", label: "Remove" }]} />
          </Field>
          <Field label="Header">
            <Input value={header} onChange={(e) => setHeader(e.target.value)} placeholder="X-Frame-Options" />
          </Field>
          {op === "set" && (
            <Field label="Value">
              <Input value={value} onChange={(e) => setValue(e.target.value)} placeholder="DENY" />
            </Field>
          )}
          <Field label="When">
            <Select value={matchType} onChange={(e) => setMatchType(e.target.value as TransformMatchType)}
              options={[
                { value: "all", label: "Always" },
                { value: "path_prefix", label: "Path prefix" },
                { value: "path_regex", label: "Path regex" },
                { value: "method", label: "Method" },
              ]} />
          </Field>
          {matchType !== "all" && (
            <Field label="Match">
              <Input value={matchValue} onChange={(e) => setMatchValue(e.target.value)} placeholder="/app or GET" />
            </Field>
          )}
          <div className="col-span-2 flex items-end sm:col-span-1">
            <Button size="sm" onClick={add} disabled={create.isPending} className="w-full">
              {create.isPending ? <Loader2 className="animate-spin" /> : <Plus />} Add
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1">
      <Label className="text-xs text-muted-foreground">{label}</Label>
      {children}
    </div>
  );
}
