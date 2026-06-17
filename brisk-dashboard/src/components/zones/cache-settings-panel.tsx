import * as React from "react";
import { Loader2, Save, Database, Clock, Filter, Layers, Cookie, FileBox, History, ShieldAlert } from "lucide-react";
import { toast } from "sonner";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Skeleton } from "@/components/ui/skeleton";
import { useZone, useSetCacheSettings } from "@/hooks/use-zones";
import { DEFAULT_CACHE_SETTINGS, type CacheSettings } from "@/lib/types";

const EDGE_MODES = [
  { value: "default", label: "Brisk default (30d assets / 10m pages)" },
  { value: "respect_origin", label: "Respect origin Cache-Control" },
  { value: "override", label: "Override TTL…" },
  { value: "no_cache", label: "Do not cache at the edge" },
];
const BROWSER_MODES = [
  { value: "default", label: "Brisk default" },
  { value: "match_server", label: "Match edge TTL" },
  { value: "override", label: "Override TTL…" },
  { value: "no_cache", label: "Do not cache in browser" },
];

/** Per-zone Cache Settings panel (Bunny-style Smart Cache). Every control defaults to
    current edge behavior; saving bumps config_version so edges re-pull + reload (~15s,
    nginx validated before reload). Reuses the same config_version path as every other
    zone setting, so it composes with Edge Rules (rules override these where they match). */
export function CacheSettingsPanel({ zoneId }: { zoneId: number }) {
  const zoneQ = useZone(zoneId);
  const save = useSetCacheSettings(zoneId);
  const [form, setForm] = React.useState<CacheSettings | null>(null);

  React.useEffect(() => {
    if (zoneQ.data && !form) setForm({ ...DEFAULT_CACHE_SETTINGS, ...(zoneQ.data.cache_settings ?? {}) });
  }, [zoneQ.data, form]);

  if (!form) return <Skeleton className="h-96 w-full" />;

  const set = <K extends keyof CacheSettings>(k: K, v: CacheSettings[K]) => setForm({ ...form, [k]: v });
  const loaded = { ...DEFAULT_CACHE_SETTINGS, ...(zoneQ.data?.cache_settings ?? {}) };
  const dirty = JSON.stringify(form) !== JSON.stringify(loaded);

  const onSave = () => {
    save.mutate(form, {
      onSuccess: (z) =>
        toast.success("Cache settings saved", {
          description: `config_version v${z.config_version} · edges reload in ~15s.`,
        }),
      onError: (e) => toast.error("Couldn't save cache settings", { description: (e as Error).message }),
    });
  };

  return (
    <div className="space-y-5">
      {/* Smart Cache */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Database className="size-4 text-muted-foreground" /> Smart Cache
          </CardTitle>
        </CardHeader>
        <CardContent>
          <Row
            label="Smart Cache"
            desc="Cache pages by file type even when the origin sends no Cache-Control — full-site acceleration. Off keeps today's behavior (assets cache, dynamic pages cache briefly)."
          >
            <Switch checked={form.smart} onCheckedChange={(c) => set("smart", c)} aria-label="Smart Cache" />
          </Row>
        </CardContent>
      </Card>

      {/* Expiration */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Clock className="size-4 text-muted-foreground" /> Expiration
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <Field label="Edge cache expiration" desc="How long the edge keeps a cached response. Override applies to assets + pages; Edge Rules can still override per-path.">
            <Select value={form.edge_mode} onChange={(e) => set("edge_mode", e.target.value)} options={EDGE_MODES} />
            {form.edge_mode === "override" && (
              <Input
                className="mt-2"
                value={form.edge_ttl}
                onChange={(e) => set("edge_ttl", e.target.value)}
                placeholder="e.g. 1h, 7d, 30m"
                aria-label="Edge TTL"
              />
            )}
          </Field>
          <Field label="Browser cache expiration" desc="The Cache-Control max-age sent to the visitor's browser.">
            <Select value={form.browser_mode} onChange={(e) => set("browser_mode", e.target.value)} options={BROWSER_MODES} />
            {form.browser_mode === "override" && (
              <Input
                className="mt-2"
                value={form.browser_ttl}
                onChange={(e) => set("browser_ttl", e.target.value)}
                placeholder="e.g. 1h, 30d"
                aria-label="Browser TTL"
              />
            )}
          </Field>
          <Row
            label="Cache error responses"
            desc="Briefly cache origin 5xx (~5s) so a flood of retries can't hammer a struggling origin."
          >
            <Switch checked={form.cache_errors} onCheckedChange={(c) => set("cache_errors", c)} aria-label="Cache error responses" />
          </Row>
        </CardContent>
      </Card>

      {/* Vary Cache */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Layers className="size-4 text-muted-foreground" /> Vary Cache
          </CardTitle>
          <p className="text-xs text-muted-foreground">
            Each dimension stores a separate cached variant. The zone hostname is always part of the key (tenant isolation).
          </p>
        </CardHeader>
        <CardContent className="space-y-4">
          <Row label="Browser WebP / AVIF support" desc="Serve (and cache) modern image formats per the browser's Accept header.">
            <Switch checked={form.vary_webp} onCheckedChange={(c) => set("vary_webp", c)} aria-label="Vary WebP/AVIF" />
          </Row>
          <Row label="Desktop / Mobile" desc="Cache a separate variant per device class (from the User-Agent).">
            <Switch checked={form.vary_device} onCheckedChange={(c) => set("vary_device", c)} aria-label="Vary device" />
          </Row>
          <Row
            label="Visitor country"
            desc="Vary by GeoIP country. Needs the GeoLite2 DB on the edge — without it every request keys the same (no error)."
          >
            <Switch checked={form.vary_country} onCheckedChange={(c) => set("vary_country", c)} aria-label="Vary country" />
          </Row>
          <Row label="URL query string" desc="Treat each distinct query string as its own cached entry.">
            <Switch checked={form.vary_querystring} onCheckedChange={(c) => set("vary_querystring", c)} aria-label="Vary query string" />
          </Row>
          <Field label="Cookie value" desc="Vary on one cookie's value (e.g. a currency or locale cookie). Leave blank for none.">
            <Input value={form.vary_cookie} onChange={(e) => set("vary_cookie", e.target.value)} placeholder="e.g. currency" aria-label="Vary cookie" />
          </Field>
          <Field label="Request headers" desc="Comma-separated request headers folded into the key (e.g. Accept-Language, X-API-Version).">
            <Input value={form.vary_headers} onChange={(e) => set("vary_headers", e.target.value)} placeholder="Accept-Language, X-API-Version" aria-label="Vary headers" />
          </Field>
        </CardContent>
      </Card>

      {/* Query string handling */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Filter className="size-4 text-muted-foreground" /> Query string handling
          </CardTitle>
          <p className="text-xs text-muted-foreground">
            <ShieldAlert className="mr-1 inline size-3 text-warning" />
            Sort + whitelist normalize the cache key via the Lua edge — they apply on edges with the Lua module
            (the live fleet) and are saved regardless.
          </p>
        </CardHeader>
        <CardContent className="space-y-4">
          <Row label="Sort query parameters" desc="Treat ?a=1&b=2 and ?b=2&a=1 as one cache entry (order-insensitive).">
            <Switch checked={form.query_sort} onCheckedChange={(c) => set("query_sort", c)} aria-label="Query sort" />
          </Row>
          <Field label="Query whitelist" desc="Comma-separated params that count toward the key; all others are ignored. Blank = every param counts.">
            <Input value={form.query_whitelist} onChange={(e) => set("query_whitelist", e.target.value)} placeholder="id, page" aria-label="Query whitelist" />
          </Field>
        </CardContent>
      </Card>

      {/* Cookies + large objects */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Cookie className="size-4 text-muted-foreground" /> Cookies &amp; large objects
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <Row
            label="Strip response cookies"
            desc="Remove Set-Cookie from responses so a cookie-setting origin (e.g. WordPress) is still cacheable on dynamic pages."
          >
            <Switch checked={form.strip_cookies} onCheckedChange={(c) => set("strip_cookies", c)} aria-label="Strip cookies" />
          </Row>
          <Row
            label="Optimize for large objects"
            desc="Slice big files into 1 MB chunks for byte-range / video delivery (recommended on for video zones — already on for HLS segments)."
          >
            <Switch checked={form.large_object} onCheckedChange={(c) => set("large_object", c)} aria-label="Large object slicing" />
          </Row>
        </CardContent>
      </Card>

      {/* Stale cache */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <History className="size-4 text-muted-foreground" /> Stale cache
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <Row label="Serve stale while origin is offline" desc="Keep serving the last cached copy when the origin is down or erroring (5xx/timeout). On by default.">
            <Switch checked={form.stale_offline} onCheckedChange={(c) => set("stale_offline", c)} aria-label="Stale while offline" />
          </Row>
          <Row label="Serve stale while updating" desc="Serve the cached copy instantly and refresh it in the background. On by default.">
            <Switch checked={form.stale_updating} onCheckedChange={(c) => set("stale_updating", c)} aria-label="Stale while updating" />
          </Row>
        </CardContent>
      </Card>

      <div className="flex items-center justify-between gap-3">
        <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <FileBox className="size-3.5" />
          Defaults reproduce current behavior. Edge Rules override these where a rule matches.
        </p>
        <Button onClick={onSave} disabled={!dirty || save.isPending}>
          {save.isPending ? <Loader2 className="animate-spin" /> : <Save />}
          Save cache settings
        </Button>
      </div>
    </div>
  );
}

function Row({ label, desc, children }: { label: string; desc: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 rounded-lg border border-border px-3 py-2.5">
      <div className="min-w-0">
        <div className="text-sm font-medium text-foreground">{label}</div>
        <div className="text-xs text-muted-foreground">{desc}</div>
      </div>
      <div className="shrink-0 pt-0.5">{children}</div>
    </div>
  );
}

function Field({ label, desc, children }: { label: string; desc: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5 rounded-lg border border-border px-3 py-2.5">
      <Label>{label}</Label>
      {children}
      <p className="text-xs text-muted-foreground">{desc}</p>
    </div>
  );
}
