import { useSearchParams } from "react-router-dom";
import { ShieldCheck, Info, Globe2 } from "lucide-react";
import { PageHeader } from "@/components/layout/page-header";
import { Card, CardContent } from "@/components/ui/card";
import { Select } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { SecurityTab } from "@/components/zones/security-tab";
import { SecurityOverview } from "@/components/overview/security-overview";
import { useZones } from "@/hooks/use-zones";

/** Top-level Security page (Phase 4): per-zone WAF (detect/block + managed OWASP
    CRS + WordPress preset + custom rules + rate limits + security events), wired to
    the live WAF APIs. Admins also see the cross-tenant overview. Tenant-scoped — the
    zone list and every WAF call go through the RBAC'd API, so a customer only sees
    their own zones. Off by default; nothing here auto-enables. */
export default function SecurityPage() {
  const [params, setParams] = useSearchParams();
  const zonesQ = useZones();
  const zones = zonesQ.data ?? [];

  const paramZone = Number(params.get("zone"));
  const selected =
    zones.find((z) => z.id === paramZone)?.id ?? zones[0]?.id ?? 0;

  const setZone = (id: number) =>
    setParams(
      (prev) => {
        const p = new URLSearchParams(prev);
        p.set("zone", String(id));
        return p;
      },
      { replace: true },
    );

  return (
    <div className="space-y-5">
      <PageHeader
        title="Security"
        description="Per-zone WAF, managed OWASP CRS, custom rules, rate limits, and the firewall log."
      />

      <SecurityOverview />

      {zonesQ.isLoading ? (
        <Skeleton className="h-64 w-full" />
      ) : zones.length === 0 ? (
        <Card>
          <CardContent className="grid place-items-center gap-2 py-16 text-center text-sm text-muted-foreground">
            <ShieldCheck className="size-6" />
            <span className="font-medium text-foreground">No zones yet</span>
            <span className="max-w-md">
              Create a zone first — then enable the WAF (start in <strong>Detect</strong>, review the
              firewall log, then switch to <strong>Block</strong>) per zone here.
            </span>
          </CardContent>
        </Card>
      ) : (
        <>
          <div className="flex flex-wrap items-end gap-3">
            <div className="w-full max-w-xs">
              <label className="mb-1 block text-xs text-muted-foreground">Zone</label>
              <Select
                value={String(selected)}
                onChange={(e) => setZone(Number(e.target.value))}
                options={zones.map((z) => ({ value: String(z.id), label: z.cdn_hostname }))}
              />
            </div>
            <p className="flex items-center gap-1.5 pb-2 text-xs text-muted-foreground">
              <Info className="size-3.5" />
              WAF is <strong>off by default</strong>; rate-limit counters are per-edge &amp; approximate.
            </p>
          </div>

          {selected > 0 && <SecurityTab zoneId={selected} />}

          <div className="flex items-start gap-2 rounded-lg border border-border bg-secondary/30 p-3 text-xs text-muted-foreground">
            <Globe2 className="mt-0.5 size-3.5 shrink-0" />
            <span>
              <strong>Country rules</strong> match the client's GeoIP country — available once the
              GeoLite2 database is installed on the edges (the GeoIP module is built but the DB isn't
              shipped yet, so country stays empty until then).
            </span>
          </div>
        </>
      )}
    </div>
  );
}
