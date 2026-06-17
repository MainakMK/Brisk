import * as React from "react";
import { useParams, useNavigate, useSearchParams, Link } from "react-router-dom";
import { ArrowLeft, AlertTriangle, Loader2, ShieldAlert, Save, Server as ServerIcon, Copy, Rocket } from "lucide-react";
import { toast } from "sonner";
import { PageHeader } from "@/components/layout/page-header";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { ZoneForm, zoneToForm, validateZone, type ZoneFormValue } from "@/components/zones/zone-form";
import { RulesEditor } from "@/components/zones/rules-editor";
import { AssignmentsMap } from "@/components/zones/assignments-map";
import { ZoneAnalyticsTab } from "@/components/zones/zone-analytics-tab";
import { ZoneUsageStrip } from "@/components/zones/zone-usage-strip";
import { PurgePanel } from "@/components/purge/purge-panel";
import { PurgeJobsTable } from "@/components/purge/jobs-table";
import { ZoneActions } from "@/components/zones/zone-actions";
import { CustomDomainsTab } from "@/components/zones/custom-domains-tab";
import { OriginShieldCard } from "@/components/zones/origin-shield-card";
import { SecurityTab } from "@/components/zones/security-tab";
import { CacheSettingsPanel } from "@/components/zones/cache-settings-panel";
import { HeaderTransformsEditor } from "@/components/zones/header-transforms-editor";
import { zoneStatusVariant } from "@/components/zones/zone-meta";
import { useZone, useUpdateZone, useZoneAssignments } from "@/hooks/use-zones";
import { timeAgo } from "@/lib/format";
import type { Server, UpdateZoneInput, Zone } from "@/lib/types";

const TABS = ["overview", "settings", "cache", "security", "domains", "rules", "assignments", "purge", "analytics"] as const;
type TabKey = (typeof TABS)[number];

export default function ZoneDetailPage() {
  const { id } = useParams();
  const zoneId = Number(id);
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const tab = (TABS.includes(params.get("tab") as TabKey) ? params.get("tab") : "overview") as TabKey;

  const zoneQ = useZone(zoneId);
  const assignments = useZoneAssignments();
  const servingEdges = assignments.map.get(zoneId) ?? [];

  const setTab = (t: string) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set("tab", t);
        return next;
      },
      { replace: true },
    );

  if (zoneQ.isLoading) {
    return (
      <div className="space-y-5">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-9 w-80" />
        <Skeleton className="h-64 w-full" />
      </div>
    );
  }

  if (zoneQ.isError || !zoneQ.data) {
    return (
      <Card className="border-destructive/40 bg-destructive/5">
        <CardContent className="flex items-center gap-3 p-4 text-sm">
          <AlertTriangle className="size-5 text-destructive" />
          <div className="flex-1">
            <div className="font-medium text-destructive">Zone not found</div>
            <div className="text-muted-foreground">{(zoneQ.error as Error)?.message ?? "Unknown zone"}</div>
          </div>
          <Button asChild variant="outline" size="sm">
            <Link to="/zones">Back to Zones</Link>
          </Button>
        </CardContent>
      </Card>
    );
  }

  const zone = zoneQ.data;

  return (
    <div className="space-y-5">
      <Button asChild variant="ghost" size="sm" className="-ml-2 text-muted-foreground">
        <Link to="/zones">
          <ArrowLeft /> Zones
        </Link>
      </Button>

      <PageHeader
        title={zone.name}
        description={zone.cdn_hostname}
        actions={
          <div className="flex items-center gap-2">
            <Button size="sm" onClick={() => setTab("purge")}>
              <Rocket className="size-4" /> Purge Cache
            </Button>
            <ZoneActions zone={zone} servingEdges={servingEdges} size="sm" onDeleted={() => navigate("/zones")} />
          </div>
        }
      />

      <div className="flex flex-wrap items-center gap-2 text-sm">
        <Badge variant={zoneStatusVariant(zone.status)}>{zone.status}</Badge>
        {zone.video && <Badge variant="secondary">video · {zone.profile}</Badge>}
        {servingEdges.length > 0 && (
          <Badge variant="warning" className="gap-1">
            <ShieldAlert className="size-3" /> live · {servingEdges.length} edge{servingEdges.length > 1 ? "s" : ""}
          </Badge>
        )}
        <span className="text-muted-foreground tabular">v{zone.config_version}</span>
        <span className="text-muted-foreground">updated {timeAgo(zone.updated_at)}</span>
      </div>

      {/* CDN hostname — what clients/tenants point at (CNAME target in Step 2). */}
      <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-card px-3 py-2">
        <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">CDN hostname</span>
        <code className="font-mono text-sm text-foreground">{zone.cdn_hostname}</code>
        <Button
          size="sm"
          variant="ghost"
          className="h-6 px-2"
          onClick={() => {
            navigator.clipboard?.writeText(zone.cdn_hostname);
            toast.success("CDN hostname copied");
          }}
          aria-label="Copy CDN hostname"
        >
          <Copy className="size-3.5" />
        </Button>
        {zone.host_header && (
          <span className="text-xs text-muted-foreground">→ upstream Host: <code className="font-mono">{zone.host_header}</code></span>
        )}
        <span className="ml-auto text-xs text-muted-foreground">account #{zone.account_id}</span>
        <p className="w-full text-xs text-muted-foreground">
          Point your domain at this hostname via a <strong>CNAME</strong>, or add a custom domain in the{" "}
          <button type="button" className="underline hover:text-foreground" onClick={() => setTab("domains")}>
            Domains
          </button>{" "}
          tab (Brisk then issues TLS for it).
        </p>
      </div>

      {/* Usage this month (Bunny-style) — always-visible glance; full detail in Analytics. */}
      <ZoneUsageStrip zoneId={zoneId} />

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="settings">Settings</TabsTrigger>
          <TabsTrigger value="cache">Cache</TabsTrigger>
          <TabsTrigger value="security">Security</TabsTrigger>
          <TabsTrigger value="domains">Domains</TabsTrigger>
          <TabsTrigger value="rules">Edge Rules</TabsTrigger>
          <TabsTrigger value="assignments">Assignments</TabsTrigger>
          <TabsTrigger value="purge">Purge</TabsTrigger>
          <TabsTrigger value="analytics">Analytics</TabsTrigger>
        </TabsList>

        <TabsContent value="overview">
          <OverviewTab zone={zone} servingEdges={servingEdges} onManage={() => setTab("assignments")} />
        </TabsContent>
        <TabsContent value="settings">
          <SettingsTab zoneId={zoneId} protectedZone={servingEdges.length > 0} />
        </TabsContent>
        <TabsContent value="cache">
          <CacheSettingsPanel zoneId={zoneId} />
        </TabsContent>
        <TabsContent value="security">
          <SecurityTab zoneId={zoneId} />
        </TabsContent>
        <TabsContent value="domains">
          <CustomDomainsTab zoneId={zoneId} />
        </TabsContent>
        <TabsContent value="rules">
          <div className="space-y-6">
            <RulesEditor zoneId={zoneId} />
            <HeaderTransformsEditor zoneId={zoneId} />
          </div>
        </TabsContent>
        <TabsContent value="assignments">
          <AssignmentsMap zoneId={zoneId} isLiveZone={servingEdges.length > 0} />
        </TabsContent>
        <TabsContent value="purge">
          <div className="grid grid-cols-1 gap-5 lg:grid-cols-[minmax(0,420px)_1fr]">
            <PurgePanel presetZoneId={zone.id} />
            <div className="space-y-3">
              <h2 className="text-sm font-medium">Recent purges for this zone</h2>
              <PurgeJobsTable zoneId={zone.id} />
            </div>
          </div>
        </TabsContent>
        <TabsContent value="analytics">
          <ZoneAnalyticsTab zoneId={zoneId} />
        </TabsContent>
      </Tabs>
    </div>
  );
}

function OverviewTab({
  zone,
  servingEdges,
  onManage,
}: {
  zone: Zone;
  servingEdges: Server[];
  onManage: () => void;
}) {
  const rows: [string, string][] = [
    ["Origin", zone.origin_url],
    ["TLS mode", zone.tls_mode],
    ["Custom domain", zone.custom_domain || "—"],
    ["Video", zone.video ? `${zone.profile} · playlist ${zone.playlist_ttl} · segment ${zone.segment_ttl}` : "off"],
    ["CORS origin", zone.cors_origin],
    ["Brotli level", String(zone.brotli_level)],
    ["Config version", `v${zone.config_version}`],
    ["Updated", timeAgo(zone.updated_at)],
  ];
  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
      <Card className="lg:col-span-2">
        <CardHeader>
          <CardTitle>Settings at a glance</CardTitle>
        </CardHeader>
        <CardContent>
          <dl className="grid grid-cols-1 gap-x-6 gap-y-3 sm:grid-cols-2">
            {rows.map(([k, v]) => (
              <div key={k} className="flex flex-col gap-0.5">
                <dt className="text-xs uppercase tracking-wider text-muted-foreground">{k}</dt>
                <dd className="truncate font-mono text-sm">{v}</dd>
              </div>
            ))}
          </dl>
        </CardContent>
      </Card>
      <Card>
        <CardHeader className="flex-row items-center justify-between">
          <CardTitle>Served by</CardTitle>
          <Button variant="outline" size="sm" onClick={onManage}>
            Manage
          </Button>
        </CardHeader>
        <CardContent className="space-y-2">
          {servingEdges.length === 0 ? (
            <p className="text-sm text-muted-foreground">Not assigned to any edge yet.</p>
          ) : (
            servingEdges.map((s) => (
              <Link
                key={s.id}
                to={`/servers/${s.id}`}
                className="flex items-center gap-2 rounded-md px-1 py-1.5 text-sm hover:text-primary"
              >
                <ServerIcon className="size-4 text-muted-foreground" />
                <span className="font-medium">{s.edge_id || s.name}</span>
                <span className="text-xs text-muted-foreground">{s.region}</span>
              </Link>
            ))
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function SettingsTab({ zoneId, protectedZone }: { zoneId: number; protectedZone: boolean }) {
  const zoneQ = useZone(zoneId);
  const update = useUpdateZone(zoneId);
  const [form, setForm] = React.useState<ZoneFormValue | null>(null);
  const [errors, setErrors] = React.useState<Record<string, string>>({});
  const [confirmOpen, setConfirmOpen] = React.useState(false);

  // Seed the form from the loaded zone once.
  React.useEffect(() => {
    if (zoneQ.data && !form) setForm(zoneToForm(zoneQ.data));
  }, [zoneQ.data, form]);

  if (!form) return <Skeleton className="h-64 w-full" />;

  const toUpdateInput = (v: ZoneFormValue): UpdateZoneInput => ({
    name: v.name.trim(),
    custom_domain: v.custom_domain.trim() || null,
    origin_url: v.origin_url.trim(),
    // Omit when empty: the API validates host_header as an optional hostname, and the
    // validator's omitempty only skips an ABSENT field — sending "" fails the hostname
    // rule (400). undefined drops it from the JSON so an empty host header is allowed.
    host_header: v.host_header.trim() || undefined,
    tls_mode: v.tls_mode as UpdateZoneInput["tls_mode"],
    video: v.video,
    profile: v.profile as UpdateZoneInput["profile"],
    playlist_ttl: v.playlist_ttl.trim(),
    segment_ttl: v.segment_ttl.trim(),
    cors_origin: v.cors_origin.trim() || "*",
    brotli_level: v.brotli_level,
    status: v.status,
    origin_ssl_verify: v.origin_ssl_verify,
    origin_follow_redirects: v.origin_follow_redirects,
    forward_host_header: v.forward_host_header,
  });

  const save = () => {
    update.mutate(toUpdateInput(form), {
      onSuccess: (z) => {
        toast.success("Zone saved", { description: `config_version v${z.config_version} · propagates in ~15s.` });
        setConfirmOpen(false);
      },
      onError: (e) => toast.error("Couldn't save zone", { description: (e as Error).message }),
    });
  };

  const onSubmit = () => {
    const errs = validateZone(form, "edit");
    setErrors(errs);
    if (Object.keys(errs).length > 0) return;
    if (protectedZone) setConfirmOpen(true);
    else save();
  };

  return (
    <div className="space-y-5">
    <Card>
      <CardContent className="space-y-5 p-5">
        <ZoneForm value={form} onChange={setForm} errors={errors} mode="edit" />
        <div className="flex justify-end">
          <Button onClick={onSubmit} disabled={update.isPending}>
            {update.isPending ? <Loader2 className="animate-spin" /> : <Save />}
            Save changes
          </Button>
        </div>
      </CardContent>

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <ShieldAlert className="size-4 text-warning" /> Save changes to a live zone?
            </DialogTitle>
            <DialogDescription>
              This zone is served by a live edge. Saving bumps config_version; the edge re-pulls and reloads nginx
              within ~15s. nginx is validated before reload, so a bad config won&apos;t drop the site.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmOpen(false)}>
              Cancel
            </Button>
            <Button onClick={save} disabled={update.isPending}>
              {update.isPending && <Loader2 className="animate-spin" />}
              Save changes
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
    <OriginShieldCard zoneId={zoneId} />
    </div>
  );
}
