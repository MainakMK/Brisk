import * as React from "react";
import { Link } from "react-router-dom";
import {
  Loader2,
  ShieldCheck,
  ArrowRight,
  CheckCircle2,
  XCircle,
  ChevronDown,
  Search,
} from "lucide-react";
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { TokenReveal } from "@/components/servers/token-reveal";
import { ProvisionLogPanel } from "@/components/servers/provision-log";
import { StatusPill } from "@/components/servers/status-pill";
import { deriveStatus } from "@/components/servers/server-status";
import { useCreateServer, useServer } from "@/hooks/use-servers";
import { useDnsRouting } from "@/hooks/use-health";
import { geoLocateIP, nearestRegion } from "@/lib/geo";
import { cn } from "@/lib/utils";
import type { CreateServerInput, Server } from "@/lib/types";

const IPV4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/;
function isValidIP(ip: string): boolean {
  const m = IPV4.exec(ip.trim());
  if (!m) return false;
  return m.slice(1).every((o) => Number(o) >= 0 && Number(o) <= 255);
}

type Auth = "password" | "key";

// suggestEdgeId builds a Brisk-branded, Bunny-style edge id from the region, e.g.
// "DE-FRA" -> "Brisk-DE-FRA-01". The edge id is the public identity in the
// X-Brisk-Edge response header (a branding surface, like Bunny's "BunnyCDN-CCU1-1124"),
// the DNS record tag, and the heartbeat key — so it stays branded + human-readable.
function suggestEdgeId(region: string): string {
  const r = region
    .trim()
    .toUpperCase()
    .replace(/[^A-Z0-9-]+/g, "-")
    .replace(/^-+|-+$/g, "");
  return r ? `Brisk-${r}-01` : "";
}

const empty = {
  name: "",
  region: "",
  ip: "",
  edge_id: "",
  capacity: "1",
  capacityUnit: "Gbps" as "Gbps" | "Mbps",
  weightByCapacity: false,
  ssh_user: "root",
  ssh_port: 22,
  auth: "password" as Auth,
  ssh_password: "",
  ssh_private_key: "",
};

export function AddServerSheet({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
}) {
  const [form, setForm] = React.useState(empty);
  // Tracks whether the operator edited the edge id by hand; until then it auto-fills
  // (Brisk-branded) from the region so a typed region instantly suggests an id.
  const [edgeIdTouched, setEdgeIdTouched] = React.useState(false);
  // Region UX: a dropdown from the backend RegionMap (no typing codes) + IP auto-detect.
  // regionTouchedRef tracks a manual pick so the IP geo-guess never overrides the operator;
  // `detected` holds the "City, Country" hint from the IP lookup. geoTimer debounces it.
  const [regionTouched, setRegionTouched] = React.useState(false);
  const regionTouchedRef = React.useRef(false);
  const [detected, setDetected] = React.useState<string | null>(null);
  const geoTimer = React.useRef<number | undefined>(undefined);
  const [createdId, setCreatedId] = React.useState<number | null>(null);
  const [token, setToken] = React.useState<string | null>(null);
  const create = useCreateServer();

  // Region catalogue = the control plane's RegionMap (GET /dns/routing), so the dropdown
  // and the geo auto-detect share the SAME source of truth as the edge routing.
  const routing = useDnsRouting();
  const regions = routing.data?.regions ?? [];
  const regionOptions = [
    { value: "", label: regions.length ? "Select a region…" : "Loading regions…" },
    ...[...regions]
      .sort((a, b) => a.label.localeCompare(b.label))
      .map((r) => ({ value: r.region, label: `${r.label} (${r.region})` })),
  ];

  // Poll the new server's status; stop once it leaves "provisioning".
  const createdServer = useServer(createdId ?? 0, createdId != null);
  const provisioning =
    createdServer.data != null && deriveStatus(createdServer.data) === "provisioning";

  const reset = () => {
    setForm(empty);
    setEdgeIdTouched(false);
    setRegionTouched(false);
    regionTouchedRef.current = false;
    setDetected(null);
    window.clearTimeout(geoTimer.current);
    setCreatedId(null);
    setToken(null);
    create.reset();
  };

  // Region change: keep the edge id auto-suggesting (Brisk-<REGION>-01) until the
  // operator types their own. So "DE-FRA" -> "Brisk-DE-FRA-01" without extra clicks.
  const setRegion = (region: string) =>
    setForm((f) => ({
      ...f,
      region,
      edge_id: edgeIdTouched ? f.edge_id : suggestEdgeId(region),
    }));

  // Operator picked a region by hand → lock out the IP geo-guess from overriding it.
  const pickRegion = (region: string) => {
    regionTouchedRef.current = true;
    setRegionTouched(true);
    setRegion(region);
  };

  // IP change: debounce a GeoIP lookup; when it resolves, pre-select the nearest region
  // (unless the operator already picked one) and show a "City, Country" hint. Fully
  // best-effort — a blocked/offline lookup just leaves the dropdown for manual choice.
  const onIpChange = (v: string) => {
    set("ip", v);
    window.clearTimeout(geoTimer.current);
    setDetected(null);
    if (regionTouchedRef.current || !isValidIP(v)) return;
    const ip = v.trim();
    geoTimer.current = window.setTimeout(async () => {
      const geo = await geoLocateIP(ip);
      if (!geo || regionTouchedRef.current) return;
      setDetected(geo.label || null);
      const code = nearestRegion(geo.lat, geo.long, regions);
      if (code) {
        setForm((f) => ({
          ...f,
          region: code,
          edge_id: edgeIdTouched ? f.edge_id : suggestEdgeId(code),
        }));
      }
    }, 600);
  };

  const close = (o: boolean) => {
    if (!o) reset();
    onOpenChange(o);
  };

  const set = <K extends keyof typeof form>(k: K, v: (typeof form)[K]) =>
    setForm((f) => ({ ...f, [k]: v }));

  const detailsValid =
    form.name.trim() !== "" && form.region.trim() !== "" && isValidIP(form.ip);
  const credsValid =
    form.ssh_user.trim() !== "" &&
    form.ssh_port > 0 &&
    (form.auth === "password" ? form.ssh_password !== "" : form.ssh_private_key.trim() !== "");

  const submit = () => {
    const capMbps =
      form.capacityUnit === "Gbps"
        ? Math.round(Number(form.capacity) * 1000)
        : Math.round(Number(form.capacity));
    const payload: CreateServerInput = {
      name: form.name.trim(),
      region: form.region.trim(),
      ip: form.ip.trim(),
      // The operator-set, Brisk-branded edge id (shown in X-Brisk-Edge). Blank => the
      // backend auto-generates a Brisk-<REGION>-<hex> fallback, so it's never raw hex.
      edge_id: form.edge_id.trim() || undefined,
      capacity_mbps: Number.isFinite(capMbps) && capMbps > 0 ? capMbps : undefined,
      weight_by_capacity: form.weightByCapacity,
      ssh_user: form.ssh_user.trim(),
      ssh_port: form.ssh_port,
      ...(form.auth === "password"
        ? { ssh_password: form.ssh_password }
        : { ssh_private_key: form.ssh_private_key }),
    };
    create.mutate(payload, {
      onSuccess: (res) => {
        setToken(res.agent_token);
        setCreatedId(res.server.id);
        // SSH creds are intentionally dropped from state now.
        setForm((f) => ({ ...f, ssh_password: "", ssh_private_key: "" }));
      },
    });
  };

  const provisioned = createdId != null;

  return (
    <Sheet open={open} onOpenChange={close}>
      <SheetContent side="right" className="w-full overflow-y-auto sm:w-[440px]">
        <SheetTitle>{provisioned ? "Provisioning server" : "Add server"}</SheetTitle>

        {!provisioned ? (
          <form
            className="space-y-4"
            onSubmit={(e) => {
              e.preventDefault();
              if (detailsValid && credsValid) submit();
            }}
          >
            <p className="text-sm text-muted-foreground">
              Register a new edge and provision it over SSH. The fleet onboards the box, installs
              the agent, and bootstraps nginx.
            </p>

            {/* Step 1 — details */}
            <fieldset className="space-y-3">
              <legend className="mb-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Details
              </legend>
              <Field label="Name">
                <Input
                  value={form.name}
                  onChange={(e) => set("name", e.target.value)}
                  placeholder="brisk-fra-1"
                  autoFocus
                />
              </Field>
              <div className="grid grid-cols-2 gap-3">
                <Field label="Region">
                  <RegionCombobox
                    value={form.region}
                    onChange={pickRegion}
                    options={regionOptions}
                    placeholder={regions.length ? "Search a city…" : "Loading regions…"}
                  />
                </Field>
                <Field
                  label="IP address"
                  error={form.ip !== "" && !isValidIP(form.ip) ? "Invalid IPv4" : undefined}
                >
                  <Input
                    value={form.ip}
                    onChange={(e) => onIpChange(e.target.value)}
                    placeholder="203.0.113.7"
                    inputMode="numeric"
                  />
                </Field>
              </div>
              {detected && (
                <p className="-mt-1 text-xs text-muted-foreground">
                  📍 Detected from IP: <span className="font-medium text-foreground">{detected}</span>
                  {form.region && !regionTouched ? ` → auto-selected ${form.region}` : ""}
                </p>
              )}
              <Field label="Edge ID">
                <Input
                  value={form.edge_id}
                  onChange={(e) => {
                    setEdgeIdTouched(true);
                    set("edge_id", e.target.value);
                  }}
                  placeholder="Brisk-FRA-01"
                  className="font-mono"
                />
                <p className="text-xs text-muted-foreground">
                  Shown in the <span className="font-mono">X-Brisk-Edge</span> header (Bunny-style
                  branding) &amp; used as the DNS tag. Auto-fills from Region; edit freely.
                  Can&apos;t be changed after the edge is added.
                </p>
              </Field>
              {/* The capacity input is only meaningful when it drives routing weight, so
                  it's disabled until the toggle is on (the toggle leads the field). */}
              <label className="flex items-center justify-between gap-2 text-sm">
                <span>
                  Weight DNS routing by capacity{" "}
                  <span className="font-normal text-muted-foreground">(opt-in)</span>
                </span>
                <Switch
                  checked={form.weightByCapacity}
                  onCheckedChange={(v) => set("weightByCapacity", v)}
                  aria-label="Weight routing by capacity"
                />
              </label>
              <Field label="Capacity (bandwidth)">
                <div
                  className={cn(
                    "flex gap-2",
                    !form.weightByCapacity && "pointer-events-none opacity-50",
                  )}
                >
                  <Input
                    value={form.capacity}
                    onChange={(e) => set("capacity", e.target.value)}
                    inputMode="decimal"
                    placeholder="e.g. 1"
                    disabled={!form.weightByCapacity}
                  />
                  <Segmented
                    options={[
                      { label: "Gbps", value: "Gbps" },
                      { label: "Mbps", value: "Mbps" },
                    ]}
                    value={form.capacityUnit}
                    onChange={(v) => set("capacityUnit", v as "Gbps" | "Mbps")}
                  />
                </div>
                {!form.weightByCapacity && (
                  <p className="text-xs text-muted-foreground">
                    Turn on the toggle above to set this PoP&apos;s bandwidth and weight DNS
                    routing by it (bigger box → bigger share).
                  </p>
                )}
              </Field>
            </fieldset>

            {/* Step 2 — SSH creds */}
            <fieldset className="space-y-3">
              <legend className="mb-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                SSH credentials
              </legend>
              <div className="grid grid-cols-2 gap-3">
                <Field label="SSH user">
                  <Input value={form.ssh_user} onChange={(e) => set("ssh_user", e.target.value)} />
                </Field>
                <Field label="Port">
                  <Input
                    type="number"
                    value={form.ssh_port}
                    onChange={(e) => set("ssh_port", Number(e.target.value))}
                  />
                </Field>
              </div>
              <Segmented
                options={[
                  { label: "Password", value: "password" as Auth },
                  { label: "Private key", value: "key" as Auth },
                ]}
                value={form.auth}
                onChange={(v) => set("auth", v)}
              />
              {form.auth === "password" ? (
                <Field label="Password">
                  <Input
                    type="password"
                    value={form.ssh_password}
                    onChange={(e) => set("ssh_password", e.target.value)}
                    placeholder="••••••••"
                    autoComplete="off"
                  />
                </Field>
              ) : (
                <Field label="Private key (PEM)">
                  <textarea
                    value={form.ssh_private_key}
                    onChange={(e) => set("ssh_private_key", e.target.value)}
                    placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                    rows={4}
                    className="w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-xs shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  />
                </Field>
              )}
              <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
                <ShieldCheck className="size-3.5 text-success" />
                Used once for provisioning — never stored.
              </p>
            </fieldset>

            {create.isError && (
              <div className="rounded-md border border-destructive/40 bg-destructive/5 p-2 text-xs text-destructive">
                {(create.error as Error).message}
              </div>
            )}

            <Button
              type="submit"
              className="w-full"
              disabled={!detailsValid || !credsValid || create.isPending}
            >
              {create.isPending ? <Loader2 className="animate-spin" /> : <ArrowRight />}
              Provision server
            </Button>
          </form>
        ) : (
          <div className="space-y-4">
            {createdServer.data && <ProvisioningHeader server={createdServer.data} />}
            {token && <TokenReveal token={token} />}
            <ProvisionLogPanel serverId={createdId} active={provisioning} />

            {!provisioning && createdServer.data && <Outcome server={createdServer.data} />}

            <div className="flex gap-2">
              <Button asChild variant="outline" className="flex-1">
                <Link to={`/servers/${createdId}`} onClick={() => close(false)}>
                  Open detail
                </Link>
              </Button>
              <Button className="flex-1" onClick={() => close(false)}>
                Done
              </Button>
            </div>
          </div>
        )}
      </SheetContent>
    </Sheet>
  );
}

function ProvisioningHeader({ server }: { server: Server }) {
  return (
    <div className="flex items-center justify-between rounded-lg border border-border bg-secondary/40 px-3 py-2">
      <div className="min-w-0">
        <div className="truncate text-sm font-medium">{server.edge_id || server.name}</div>
        <div className="truncate text-xs text-muted-foreground">
          {server.region} · {server.ip}
        </div>
      </div>
      <StatusPill server={server} />
    </div>
  );
}

function Outcome({ server }: { server: Server }) {
  const st = deriveStatus(server);
  const online = st === "online";
  return (
    <div
      className={cn(
        "flex items-center gap-2 rounded-lg border p-3 text-sm",
        online
          ? "border-success/40 bg-success/5 text-success"
          : "border-destructive/40 bg-destructive/5 text-destructive",
      )}
    >
      {online ? <CheckCircle2 className="size-4" /> : <XCircle className="size-4" />}
      {online ? (
        <span>Online — the edge is serving and appears in the grid.</span>
      ) : (
        <span>
          Provisioning didn&apos;t complete. Review the log, then reprovision from the detail page.
        </span>
      )}
    </div>
  );
}

// RegionCombobox is a searchable region picker: click to open, type to filter by city
// or code, click to select. Solves "I don't know the codes" + "auto-pick didn't detect"
// — the operator can always search. Options with value "" are skipped (placeholder rows).
function RegionCombobox({
  value,
  onChange,
  options,
  placeholder = "Select a region…",
}: {
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string }[];
  placeholder?: string;
}) {
  const [open, setOpen] = React.useState(false);
  const [query, setQuery] = React.useState("");
  const ref = React.useRef<HTMLDivElement>(null);

  React.useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [open]);

  const selected = options.find((o) => o.value === value && o.value !== "");
  const q = query.trim().toLowerCase();
  const list = options.filter(
    (o) => o.value !== "" && (!q || o.label.toLowerCase().includes(q) || o.value.toLowerCase().includes(q)),
  );

  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex h-9 w-full items-center justify-between gap-2 rounded-md border border-input bg-transparent px-3 text-left text-sm shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <span className={cn("truncate", !selected && "text-muted-foreground")}>
          {selected ? selected.label : placeholder}
        </span>
        <ChevronDown className="size-4 shrink-0 text-muted-foreground" />
      </button>
      {open && (
        <div className="absolute z-50 mt-1 w-full overflow-hidden rounded-md border border-border bg-popover shadow-lg">
          <div className="flex items-center gap-2 border-b border-border px-2.5">
            <Search className="size-3.5 shrink-0 text-muted-foreground" />
            <input
              autoFocus
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search city or code…"
              className="h-8 w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground"
            />
          </div>
          <div className="max-h-56 overflow-y-auto py-1">
            {list.length === 0 ? (
              <div className="px-3 py-2 text-xs text-muted-foreground">
                No region matches “{query}”.
              </div>
            ) : (
              list.map((o) => (
                <button
                  key={o.value}
                  type="button"
                  onClick={() => {
                    onChange(o.value);
                    setOpen(false);
                    setQuery("");
                  }}
                  className={cn(
                    "block w-full px-3 py-1.5 text-left text-sm transition-colors hover:bg-accent",
                    o.value === value && "bg-accent/60 font-medium",
                  )}
                >
                  {o.label}
                </button>
              ))
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function Field({
  label,
  error,
  children,
}: {
  label: string;
  error?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1">
      <Label>{label}</Label>
      {children}
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  );
}

function Segmented<T extends string | number>({
  options,
  value,
  onChange,
}: {
  options: { label: string; value: T }[];
  value: T;
  onChange: (v: T) => void;
}) {
  return (
    <div className="inline-flex rounded-md border border-border bg-muted/40 p-0.5">
      {options.map((o) => (
        <button
          key={String(o.value)}
          type="button"
          onClick={() => onChange(o.value)}
          className={cn(
            "rounded px-3 py-1 text-xs font-medium transition-colors",
            value === o.value
              ? "bg-primary text-primary-foreground"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}
