import * as React from "react";
import { toast } from "sonner";
import { Loader2, Trash2, Link2, FolderTree, Globe, TriangleAlert, Zap } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";
import { useZones } from "@/hooks/use-zones";
import { useServers } from "@/hooks/use-servers";
import { usePurgeZone, usePurgeAll } from "@/hooks/use-purge";
import type { Zone } from "@/lib/types";

type ScopeType = "url" | "prefix" | "zone";

/** Reduce a purge target to the cache-key PATH. Accepts a bare "/path?x=1" OR a full
   http(s)://host/path URL — the scheme + host are stripped to the path, because the
   edge keys cache by the ZONE's own hostname + path, so the host a user pastes (often
   the origin, e.g. https://cdn.mainakghosh.com/...) is irrelevant. Mirrors the server
   normalizer. Invalid leftovers (e.g. "foo" with no scheme/slash) are left for the
   "must start with /" check to flag. */
function normalizeTarget(raw: string): string {
  const t = raw.trim();
  if (/^https?:\/\//i.test(t)) {
    try {
      const u = new URL(t);
      return (u.pathname || "/") + (u.search || "");
    } catch {
      return t;
    }
  }
  return t;
}

const TYPES: { key: ScopeType; label: string; icon: typeof Link2 }[] = [
  { key: "url", label: "By URL", icon: Link2 },
  { key: "prefix", label: "By prefix", icon: FolderTree },
  { key: "zone", label: "Whole zone", icon: Globe },
];

/** New-purge action panel. `presetZoneId` locks the zone (zone-detail quick purge). */
export function PurgePanel({ presetZoneId }: { presetZoneId?: number }) {
  const zones = useZones();
  const zoneList = zones.data ?? [];
  const [zoneId, setZoneId] = React.useState<number | null>(presetZoneId ?? null);
  const [type, setType] = React.useState<ScopeType>("url");
  const [urls, setUrls] = React.useState("");
  const [prefix, setPrefix] = React.useState("");
  const [confirm, setConfirm] = React.useState<null | "scope" | "all">(null);

  React.useEffect(() => {
    if (presetZoneId != null) setZoneId(presetZoneId);
    else if (zoneId == null && zoneList.length > 0) setZoneId(zoneList[0].id);
  }, [presetZoneId, zoneList, zoneId]);

  const zone = zoneList.find((z) => z.id === zoneId) ?? null;
  const purgeZone = usePurgeZone(zoneId ?? 0);

  const targets: string[] = React.useMemo(() => {
    if (type === "url") return urls.split("\n").map(normalizeTarget).filter(Boolean);
    if (type === "prefix") return normalizeTarget(prefix) ? [normalizeTarget(prefix)] : [];
    return ["/"];
  }, [type, urls, prefix]);

  const invalidTargets = type !== "zone" ? targets.filter((t) => !t.startsWith("/")) : [];
  const canSubmit = zone != null && targets.length > 0 && invalidTargets.length === 0;

  const submit = async () => {
    if (!zone) return;
    try {
      if (type === "zone") {
        await purgeZone.mutateAsync({ type: "zone", target: "/" });
      } else {
        // one job per target line
        for (const target of targets) {
          await purgeZone.mutateAsync({ type, target });
        }
      }
      toast.success(`Purge queued for ${zone.name}`, {
        description: `${type === "zone" ? "Whole zone" : `${targets.length} ${type} target(s)`} · over the instant NATS channel.`,
      });
      setConfirm(null);
      setUrls("");
      setPrefix("");
    } catch (e) {
      toast.error("Purge failed", { description: (e as Error).message });
    }
  };

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>New purge</CardTitle>
          <p className="text-xs text-muted-foreground">
            Invalidate cached content. Purge runs over the instant NATS channel (not the ~15s config pull).
          </p>
        </CardHeader>
        <CardContent className="space-y-4">
          {presetZoneId == null && (
            <div className="space-y-1">
              <Label>Zone</Label>
              <Select
                value={zoneId != null ? String(zoneId) : ""}
                onChange={(e) => setZoneId(Number(e.target.value))}
                options={zoneList.map((z) => ({ value: String(z.id), label: `${z.name} (${z.cdn_hostname})` }))}
                aria-label="Zone to purge"
              />
            </div>
          )}

          {/* type segmented */}
          <div className="inline-flex flex-wrap gap-1 rounded-md border border-border bg-muted/40 p-0.5">
            {TYPES.map((t) => (
              <button
                key={t.key}
                onClick={() => setType(t.key)}
                className={cn(
                  "inline-flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium transition-colors",
                  type === t.key ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground",
                )}
              >
                <t.icon className="size-3.5" />
                {t.label}
              </button>
            ))}
          </div>

          {type === "url" && (
            <div className="space-y-1">
              <Label>Paths (one per line)</Label>
              <textarea
                value={urls}
                onChange={(e) => setUrls(e.target.value)}
                rows={4}
                placeholder={"/style.css\nhttps://your-site.com/index.html?v=2"}
                className="w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-xs shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              />
              <p className="text-xs text-muted-foreground">
                Paste a path (<span className="font-mono">/img/logo.png</span>) or a full
                <span className="font-mono"> http(s):// </span>URL — the scheme and host are dropped to the path.
                Include query params if the cache key has them.
              </p>
            </div>
          )}

          {type === "prefix" && (
            <div className="space-y-1">
              <Label>Prefix</Label>
              <Input
                value={prefix}
                onChange={(e) => setPrefix(e.target.value)}
                placeholder="/video/movie.mp4"
                className="font-mono text-xs"
              />
              <p className="text-xs text-muted-foreground">
                Clears <span className="font-medium text-foreground">everything under</span> this path — including all
                slices of a sliced video.
              </p>
            </div>
          )}

          {type === "zone" && (
            <div className="rounded-lg border border-warning/40 bg-warning/5 p-3 text-sm text-warning">
              Purges <span className="font-medium">all cached content</span> for{" "}
              <span className="font-mono">{zone?.cdn_hostname ?? "this zone"}</span>. Hit ratio dips briefly as objects
              re-fetch from origin.
            </div>
          )}

          {invalidTargets.length > 0 && <p className="text-xs text-destructive">Paths must start with “/”.</p>}

          <Button
            disabled={!canSubmit}
            variant={type === "zone" ? "destructive" : "default"}
            onClick={() => setConfirm("scope")}
          >
            <Trash2 /> Purge {type === "zone" ? "whole zone" : `${targets.length || ""} ${type}`.trim()}
          </Button>
        </CardContent>
      </Card>

      {/* Purge everything — visually separated danger zone */}
      <Card className="border-destructive/40">
        <CardContent className="flex flex-col gap-3 p-4 sm:flex-row sm:items-center">
          <div className="flex-1">
            <div className="flex items-center gap-1.5 text-sm font-medium text-destructive">
              <TriangleAlert className="size-4" /> Purge everything
            </div>
            <p className="text-xs text-muted-foreground">
              Clears the <span className="font-medium">entire cache</span> on selected edges and triggers an origin-load
              surge as everything re-fetches. Reserved for emergencies.
            </p>
          </div>
          <Button variant="destructive" onClick={() => setConfirm("all")}>
            <Zap /> Purge everything
          </Button>
        </CardContent>
      </Card>

      {/* scope confirm (url/prefix review, or whole-zone strong) */}
      <ScopeConfirm
        open={confirm === "scope"}
        onOpenChange={(o) => !o && setConfirm(null)}
        zone={zone}
        type={type}
        targets={targets}
        pending={purgeZone.isPending}
        onConfirm={submit}
      />

      {/* purge-all type-to-confirm + server scope */}
      <PurgeAllConfirm open={confirm === "all"} onOpenChange={(o) => !o && setConfirm(null)} />
    </div>
  );
}

function ScopeConfirm({
  open,
  onOpenChange,
  zone,
  type,
  targets,
  pending,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  zone: Zone | null;
  type: ScopeType;
  targets: string[];
  pending: boolean;
  onConfirm: () => void;
}) {
  const [ack, setAck] = React.useState(false);
  React.useEffect(() => {
    if (!open) setAck(false);
  }, [open]);
  const strong = type === "zone";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className={cn("flex items-center gap-2", strong && "text-destructive")}>
            {strong && <TriangleAlert className="size-4" />}
            {strong ? "Purge the whole zone?" : "Confirm purge"}
          </DialogTitle>
          <DialogDescription>
            {zone?.name} · {zone?.cdn_hostname}
          </DialogDescription>
        </DialogHeader>

        {type !== "zone" ? (
          <div className="max-h-40 space-y-1 overflow-y-auto rounded-md border border-border bg-secondary/30 p-3 font-mono text-xs">
            {targets.map((t, i) => (
              <div key={i} className="truncate">
                {type === "prefix" ? `${t}*` : t}
              </div>
            ))}
          </div>
        ) : (
          <label className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm">
            <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} className="mt-0.5 accent-[var(--destructive)]" />
            <span>
              I understand this clears <span className="font-medium">all cached content</span> for this zone, and hit
              ratio will dip while it re-fetches.
            </span>
          </label>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button variant={strong ? "destructive" : "default"} disabled={pending || (strong && !ack)} onClick={onConfirm}>
            {pending && <Loader2 className="animate-spin" />}
            {strong ? "Purge whole zone" : "Purge"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PurgeAllConfirm({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const servers = useServers();
  const list = servers.data ?? [];
  const purgeAll = usePurgeAll();
  const [typed, setTyped] = React.useState("");
  const [selected, setSelected] = React.useState<Set<number>>(new Set());

  React.useEffect(() => {
    if (!open) {
      setTyped("");
      setSelected(new Set());
    }
  }, [open]);

  const allScope = selected.size === 0;
  const confirmed = typed.trim().toUpperCase() === "PURGE";

  const toggle = (id: number) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  const submit = () => {
    purgeAll.mutate(
      { server_ids: allScope ? undefined : Array.from(selected) },
      {
        onSuccess: () => {
          toast.success("Purge-all queued", {
            description: allScope ? "Every edge will clear its entire cache." : `${selected.size} edge(s) selected.`,
          });
          onOpenChange(false);
        },
        onError: (e) => toast.error("Purge-all failed", { description: (e as Error).message }),
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-destructive">
            <TriangleAlert className="size-4" /> Purge the entire cache?
          </DialogTitle>
          <DialogDescription>
            This clears <span className="font-medium text-foreground">all cached objects</span> on the selected edges.
            Expect an origin-load surge and a temporary hit-ratio drop.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-1">
          <Label>Scope</Label>
          <div className="max-h-40 space-y-1 overflow-y-auto rounded-md border border-border p-2 text-sm">
            <div className="px-1 text-xs text-muted-foreground">
              {allScope ? "All edges (none selected = everything)" : `${selected.size} edge(s) selected`}
            </div>
            {list.map((s) => (
              <label key={s.id} className="flex cursor-pointer items-center gap-2 rounded px-1 py-1 hover:bg-secondary/40">
                <input type="checkbox" checked={selected.has(s.id)} onChange={() => toggle(s.id)} className="accent-[var(--primary)]" />
                <span className="font-medium">{s.edge_id || s.name}</span>
                <span className="text-xs text-muted-foreground">{s.region}</span>
              </label>
            ))}
            {list.length === 0 && <div className="px-1 py-2 text-xs text-muted-foreground">No edges.</div>}
          </div>
        </div>

        <div className="space-y-1">
          <Label>
            Type <span className="font-mono font-semibold text-destructive">PURGE</span> to confirm
          </Label>
          <Input value={typed} onChange={(e) => setTyped(e.target.value)} placeholder="PURGE" autoComplete="off" />
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button variant="destructive" disabled={!confirmed || purgeAll.isPending} onClick={submit}>
            {purgeAll.isPending && <Loader2 className="animate-spin" />}
            Purge everything
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
