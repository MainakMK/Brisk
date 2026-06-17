import * as React from "react";
import { Shield, Loader2, Save, Info } from "lucide-react";
import { toast } from "sonner";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Select } from "@/components/ui/select";
import { Badge } from "@/components/ui/badge";
import { useZone, useSetZoneShield } from "@/hooks/use-zones";
import { useServers } from "@/hooks/use-servers";

/** Per-zone Origin Shield control (Phase 4 Step 3): toggle + shield-PoP selector.
    Honest about when shield helps (cacheable/static/video) vs not (dynamic). */
export function OriginShieldCard({ zoneId }: { zoneId: number }) {
  const zoneQ = useZone(zoneId);
  const serversQ = useServers();
  const setShield = useSetZoneShield(zoneId);

  const shields = (serversQ.data ?? []).filter((s) => s.role === "shield");
  const zone = zoneQ.data;

  const [enabled, setEnabled] = React.useState(false);
  const [shieldId, setShieldId] = React.useState<string>(""); // "" = network default
  const [seeded, setSeeded] = React.useState(false);

  React.useEffect(() => {
    if (zone && !seeded) {
      setEnabled(!!zone.origin_shield_enabled);
      setShieldId(zone.shield_server_id != null ? String(zone.shield_server_id) : "");
      setSeeded(true);
    }
  }, [zone, seeded]);

  const save = () => {
    setShield.mutate(
      { enabled, shield_server_id: shieldId ? Number(shieldId) : null },
      {
        onSuccess: (z) =>
          toast.success("Origin shield updated", {
            description: `config_version v${z.config_version} · edges re-pull in ~15s.`,
          }),
        onError: (e) => toast.error("Couldn't update shield", { description: (e as Error).message }),
      },
    );
  };

  const options = [
    { value: "", label: "Network default shield" },
    ...shields.map((s) => ({
      value: String(s.id),
      label: `${s.edge_id || s.name} · ${s.region}${s.serve_public ? " (hybrid)" : ""}`,
    })),
  ];

  return (
    <Card>
      <CardHeader className="flex-row items-center gap-2">
        <Shield className="size-4 text-primary" />
        <CardTitle>Origin Shield</CardTitle>
        {zone?.origin_shield_enabled && <Badge variant="success">on</Badge>}
      </CardHeader>
      <CardContent className="space-y-4">
        <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
          <Info className="mt-0.5 size-3.5 shrink-0" />
          Edge misses pull through a shield PoP, so your origin sees ~one request per object
          (collapsed across all edges). Best for cacheable/static/video; little benefit for
          dynamic content (an extra hop, no consolidation).
        </p>

        <div className="flex items-center justify-between rounded-md border border-border px-3 py-2">
          <div>
            <div className="text-sm font-medium">Route edge misses through a shield</div>
            <div className="text-xs text-muted-foreground">Falls back to direct origin if the shield is down.</div>
          </div>
          <Switch checked={enabled} onCheckedChange={setEnabled} aria-label="Origin shield" />
        </div>

        {enabled && (
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-muted-foreground">Shield PoP</label>
            {shields.length === 0 ? (
              <p className="text-xs text-warning">
                No shield PoP exists yet. Mark a server as a <strong>Shield</strong> on the Servers page
                (ideally the one nearest this zone's origin), then select it here.
              </p>
            ) : (
              <Select value={shieldId} onChange={(e) => setShieldId(e.target.value)} options={options} />
            )}
          </div>
        )}

        <div className="flex justify-end">
          <Button onClick={save} disabled={setShield.isPending || !seeded}>
            {setShield.isPending ? <Loader2 className="animate-spin" /> : <Save />}
            Save shield setting
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
