import * as React from "react";
import { Loader2, HeartPulse, Compass, SlidersHorizontal, MapPin, Shield } from "lucide-react";
import { toast } from "sonner";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import { RotationBadge } from "@/components/servers/rotation-badge";
import { DrainToggle } from "@/components/servers/drain-toggle";
import { TechStackCard } from "@/components/servers/tech-stack-card";
import { useHealthStatus, useDnsRouting, useHealthConfig, indexByEdge } from "@/hooks/use-health";
import { useServerRotation, useSetServerHealth, useSetServerRouting, useSetServerRole } from "@/hooks/use-servers";
import { timeAgo } from "@/lib/format";
import type { Server } from "@/lib/types";
import { cn } from "@/lib/utils";

/** Rotation/health + routing detail + editable per-PoP config for one server. */
export function ServerOps({ server }: { server: Server }) {
  const health = useHealthStatus();
  const routing = useDnsRouting();
  const hcfg = useHealthConfig();
  const rotation = useServerRotation(server.id);

  const edge = indexByEdge(health.data?.edges)[server.edge_id];
  const route = routing.data?.servers.find((s) => s.edge_id === server.edge_id);
  const reason = rotation.data?.reason ?? edge?.rotation_reason;
  const defaults = hcfg.data?.defaults;

  return (
    <section className="grid grid-cols-1 gap-4 lg:grid-cols-3">
      {/* Rotation & health */}
      <Card>
        <CardHeader className="flex-row items-center justify-between">
          <div className="flex items-center gap-2">
            <HeartPulse className="size-4 text-muted-foreground" />
            <CardTitle>Rotation &amp; health</CardTitle>
          </div>
          <DrainToggle server={server} size="sm" variant="outline" />
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <div className="flex items-center gap-2">
            <RotationBadge reason={reason} />
            {server.drained && server.drain_reason && (
              <span className="text-xs text-muted-foreground">“{server.drain_reason}”</span>
            )}
          </div>
          <Row label="Health probe">
            <Badge variant={edge?.state === "healthy" ? "success" : edge?.state === "unhealthy" ? "danger" : "muted"}>
              {edge?.state ?? "unknown"}
            </Badge>
            {!health.data?.checker_enabled && <span className="text-xs text-muted-foreground">checker off</span>}
          </Row>
          <Row label="Last probe">
            {edge?.last_probe ? `${timeAgo(edge.last_probe)} · ${edge.last_latency_ms}ms` : "—"}
          </Row>
          <Row label="Consecutive">
            <span className="tabular">
              {edge ? `${edge.consecutive_successes} ok / ${edge.consecutive_fails} fail` : "—"}
            </span>
          </Row>
          {edge?.last_error && (
            <Row label="Last error">
              <span className="truncate text-xs text-danger" title={edge.last_error}>
                {edge.last_error}
              </span>
            </Row>
          )}
        </CardContent>
      </Card>

      {/* Routing */}
      <Card>
        <CardHeader className="flex-row items-center gap-2">
          <Compass className="size-4 text-muted-foreground" />
          <CardTitle>Routing</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <Row label="Mode">
            <Badge variant="outline" className="capitalize">
              {route?.mode ?? routing.data?.mode ?? "—"}
            </Badge>
            {route?.overridden && <span className="text-xs text-warning">per-PoP override</span>}
          </Row>
          <Row label="Location">
            {route?.mapped ? (
              <span className="flex items-center gap-1">
                <MapPin className="size-3 text-muted-foreground" />
                {route.mode === "latency"
                  ? `zone ${route.latency_zone}`
                  : `${route.label ?? server.region} (${route.lat?.toFixed(2)}, ${route.long?.toFixed(2)})`}
              </span>
            ) : (
              <span className="text-warning">region “{server.region}” not in map</span>
            )}
          </Row>
          <Row label="Weight">
            <span className="tabular">{route?.weight ?? server.routing_weight}</span>
          </Row>
          <Row label="DNS record">
            {edge?.in_rotation ? (
              <Badge variant="success">enabled</Badge>
            ) : (
              <Badge variant="muted">disabled</Badge>
            )}
            <span className="text-xs text-muted-foreground">TTL {routing.data?.ttl ?? "—"}s</span>
          </Row>
        </CardContent>
      </Card>

      {/* Tech / runtime stack the agent reports for this PoP */}
      <TechStackCard server={server} />

      {/* Per-PoP config (editable) */}
      <Card className="lg:col-span-3">
        <CardHeader className="flex-row items-center gap-2">
          <SlidersHorizontal className="size-4 text-muted-foreground" />
          <CardTitle>Per-PoP configuration</CardTitle>
        </CardHeader>
        <CardContent className="grid grid-cols-1 gap-6 md:grid-cols-2">
          <HealthConfigEditor
            server={server}
            defaults={
              defaults
                ? {
                    interval: defaults.interval_sec,
                    fail: defaults.fail_threshold,
                    rise: defaults.rise_threshold,
                  }
                : undefined
            }
          />
          <RoutingConfigEditor server={server} networkMode={routing.data?.mode} />
          <RoleEditor server={server} />
        </CardContent>
      </Card>
    </section>
  );
}

function RoleEditor({ server }: { server: Server }) {
  const save = useSetServerRole(server.id);
  const [role, setRole] = React.useState(server.role ?? "edge");
  const [servePublic, setServePublic] = React.useState(server.serve_public ?? true);
  React.useEffect(() => {
    setRole(server.role ?? "edge");
    setServePublic(server.serve_public ?? true);
  }, [server.role, server.serve_public]);

  const dirty = role !== server.role || (role === "shield" && servePublic !== (server.serve_public ?? true));

  const submit = () => {
    save.mutate(
      { role, serve_public: role === "shield" ? servePublic : undefined },
      {
        onSuccess: () =>
          toast.success("Role updated", {
            description:
              role === "shield"
                ? servePublic
                  ? "Hybrid shield — stays in the geo DNS set AND fronts origins; select it per-zone under Origin Shield."
                  : "Pure shield — removed from the geo DNS set; select it per-zone under Origin Shield."
                : "Marked as an edge — back in the geo DNS rotation.",
          }),
        onError: (e) => toast.error("Save failed", { description: (e as Error).message }),
      },
    );
  };

  return (
    <div className="space-y-3 md:col-span-2">
      <div className="flex items-center gap-2">
        <Shield className="size-4 text-muted-foreground" />
        <h4 className="text-sm font-medium">Origin-shield role</h4>
      </div>
      <p className="text-xs text-muted-foreground">
        A <strong>shield</strong> is a mid-tier cache in front of origins. Edges fetch a zone's misses
        through it over a warm, pooled (keepalive) connection. Pick the PoP nearest a zone's origin,
        then enable Origin Shield on that zone.
      </p>
      <div className="flex flex-wrap items-end gap-3">
        <Field label="Role">
          <Select
            value={role}
            onChange={(e) => setRole(e.target.value)}
            options={[
              { value: "edge", label: "edge (user-facing, in DNS)" },
              { value: "shield", label: "shield (mid-tier cache)" },
            ]}
          />
        </Field>
        {role === "shield" && (
          <label className="flex items-center gap-2 pb-2 text-xs text-muted-foreground">
            <Switch checked={servePublic} onCheckedChange={setServePublic} aria-label="Also serve as a public PoP" />
            Also serve as a public PoP (hybrid)
          </label>
        )}
        <Button size="sm" onClick={submit} disabled={save.isPending || !dirty}>
          {save.isPending && <Loader2 className="animate-spin" />}
          Save role
        </Button>
      </div>
      {role === "shield" && (
        <p className="text-[11px] text-muted-foreground">
          {servePublic
            ? "Hybrid: keeps its public DNS record (still serves nearby users) AND acts as the shield — best when you can't spare a PoP."
            : "Pure shield: removed from the geo DNS set (no user traffic), dedicated to fronting origins."}
        </p>
      )}
    </div>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="text-muted-foreground">{label}</span>
      <span className="flex items-center gap-2 text-right font-medium">{children}</span>
    </div>
  );
}

function HealthConfigEditor({
  server,
  defaults,
}: {
  server: Server;
  defaults?: { interval: number; fail: number; rise: number };
}) {
  const save = useSetServerHealth(server.id);
  const [enabled, setEnabled] = React.useState(server.health_enabled);
  const [interval, setIntervalVal] = React.useState(String(server.health_interval_seconds || ""));
  const [fail, setFail] = React.useState(String(server.health_fail_threshold || ""));
  const [rise, setRise] = React.useState(String(server.health_rise_threshold || ""));

  // Re-seed when the server row refreshes (e.g. after save / poll).
  React.useEffect(() => {
    setEnabled(server.health_enabled);
    setIntervalVal(String(server.health_interval_seconds || ""));
    setFail(String(server.health_fail_threshold || ""));
    setRise(String(server.health_rise_threshold || ""));
  }, [server.health_enabled, server.health_interval_seconds, server.health_fail_threshold, server.health_rise_threshold]);

  const submit = () => {
    save.mutate(
      {
        enabled,
        interval_seconds: Number(interval) || 0,
        fail_threshold: Number(fail) || 0,
        rise_threshold: Number(rise) || 0,
      },
      {
        onSuccess: () => toast.success("Health config saved", { description: "Takes effect on the next check cycle." }),
        onError: (e) => toast.error("Save failed", { description: (e as Error).message }),
      },
    );
  };

  const ph = (v?: number) => (v ? `inherit (${v})` : "inherit");

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h4 className="text-sm font-medium">Health checks</h4>
        <label className="flex items-center gap-2 text-xs text-muted-foreground">
          enabled
          <Switch checked={enabled} onCheckedChange={setEnabled} aria-label="Health checks enabled" />
        </label>
      </div>
      <p className="text-xs text-muted-foreground">Blank / 0 = inherit the network default.</p>
      <div className="grid grid-cols-3 gap-2">
        <Field label="Interval (s)">
          <Input value={interval} onChange={(e) => setIntervalVal(e.target.value)} inputMode="numeric" placeholder={ph(defaults?.interval)} />
        </Field>
        <Field label="Fail thr.">
          <Input value={fail} onChange={(e) => setFail(e.target.value)} inputMode="numeric" placeholder={ph(defaults?.fail)} />
        </Field>
        <Field label="Rise thr.">
          <Input value={rise} onChange={(e) => setRise(e.target.value)} inputMode="numeric" placeholder={ph(defaults?.rise)} />
        </Field>
      </div>
      <Button size="sm" onClick={submit} disabled={save.isPending}>
        {save.isPending && <Loader2 className="animate-spin" />}
        Save health config
      </Button>
    </div>
  );
}

function RoutingConfigEditor({ server, networkMode }: { server: Server; networkMode?: string }) {
  const save = useSetServerRouting(server.id);
  const [weight, setWeight] = React.useState(String(server.routing_weight ?? 100));
  const [override, setOverride] = React.useState(server.routing_override ?? "");
  const initCap = capacityToInput(server.capacity_mbps);
  const [capValue, setCapValue] = React.useState(initCap.value);
  const [capUnit, setCapUnit] = React.useState(initCap.unit);
  const [byCapacity, setByCapacity] = React.useState(!!server.weight_by_capacity);

  React.useEffect(() => {
    setWeight(String(server.routing_weight ?? 100));
    setOverride(server.routing_override ?? "");
    const c = capacityToInput(server.capacity_mbps);
    setCapValue(c.value);
    setCapUnit(c.unit);
    setByCapacity(!!server.weight_by_capacity);
  }, [server.routing_weight, server.routing_override, server.capacity_mbps, server.weight_by_capacity]);

  const submit = () => {
    const w = Number(weight);
    if (!Number.isFinite(w) || w < 0 || w > 100) {
      toast.error("Weight must be 0–100");
      return;
    }
    const mbps = capUnit === "Gbps" ? Math.round(Number(capValue) * 1000) : Math.round(Number(capValue));
    if (capValue !== "" && (!Number.isFinite(mbps) || mbps < 0)) {
      toast.error("Capacity must be a positive number");
      return;
    }
    save.mutate(
      {
        weight: w,
        override,
        capacity_mbps: capValue === "" ? undefined : mbps,
        weight_by_capacity: byCapacity,
      },
      {
        onSuccess: () => toast.success("Routing config saved", { description: "Applied to DNS on the next reconcile." }),
        onError: (e) => toast.error("Save failed", { description: (e as Error).message }),
      },
    );
  };

  return (
    <div className="space-y-3">
      <h4 className="text-sm font-medium">Smart routing</h4>
      <p className="text-xs text-muted-foreground">
        Weight splits load within a location group. Override pins this PoP's mode.
      </p>
      <div className="grid grid-cols-2 gap-2">
        <Field label="Weight (0–100)">
          <Input
            value={weight}
            onChange={(e) => setWeight(e.target.value)}
            inputMode="numeric"
            disabled={byCapacity}
            title={byCapacity ? "Weight is derived from capacity while 'Weight by capacity' is on" : undefined}
          />
        </Field>
        <Field label="Mode override">
          <Select
            value={override}
            onChange={(e) => setOverride(e.target.value)}
            options={[
              { value: "", label: `inherit (${networkMode ?? "network"})` },
              { value: "geographic", label: "geographic" },
              { value: "latency", label: "latency" },
            ]}
          />
        </Field>
      </div>

      {/* Capacity-weighted routing (opt-in) */}
      <div className="space-y-2 rounded-md border border-border p-3">
        <label className="flex items-center justify-between gap-2 text-xs">
          <span className="font-medium text-foreground">Weight by capacity</span>
          <Switch checked={byCapacity} onCheckedChange={setByCapacity} aria-label="Weight routing by capacity" />
        </label>
        <p className="text-[11px] text-muted-foreground">
          On: this PoP's DNS share scales with its bandwidth (bigger box → proportionally more traffic,
          normalized to your largest PoP). Off: uses the manual Weight above.
        </p>
        <Field label="Capacity (bandwidth)">
          <div className="flex gap-2">
            <Input
              value={capValue}
              onChange={(e) => setCapValue(e.target.value)}
              inputMode="decimal"
              placeholder="e.g. 1"
            />
            <Select
              value={capUnit}
              onChange={(e) => setCapUnit(e.target.value)}
              options={[
                { value: "Gbps", label: "Gbps" },
                { value: "Mbps", label: "Mbps" },
              ]}
            />
          </div>
        </Field>
      </div>

      <Button size="sm" onClick={submit} disabled={save.isPending}>
        {save.isPending && <Loader2 className="animate-spin" />}
        Save routing config
      </Button>
    </div>
  );
}

// capacityToInput splits a stored Mbps value into a number + unit for the editor: clean
// Gbps when it's a round multiple of 1000, else Mbps. Empty when unset.
function capacityToInput(mbps?: number): { value: string; unit: string } {
  if (!mbps || mbps <= 0) return { value: "", unit: "Gbps" };
  if (mbps >= 1000 && mbps % 1000 === 0) return { value: String(mbps / 1000), unit: "Gbps" };
  return { value: String(mbps), unit: "Mbps" };
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className={cn("space-y-1")}>
      <Label className="text-xs text-muted-foreground">{label}</Label>
      {children}
    </div>
  );
}
