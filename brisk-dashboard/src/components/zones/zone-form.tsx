import * as React from "react";
import { Clock, Film } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import {
  validateRequired,
  validateHostname,
  validateOptionalHostname,
  validateURL,
  validateTTL,
} from "@/lib/validators";
import { CDN_BASE_DOMAIN, suggestHostname } from "@/lib/cdn";
import type { CreateZoneInput, Zone } from "@/lib/types";

export interface ZoneFormValue {
  name: string;
  cdn_hostname: string;
  custom_domain: string;
  origin_url: string;
  host_header: string;
  tls_mode: string;
  video: boolean;
  profile: string;
  playlist_ttl: string;
  segment_ttl: string;
  cors_origin: string;
  brotli_level: number;
  status: string;
  origin_ssl_verify: boolean;
  origin_follow_redirects: boolean;
  forward_host_header: boolean;
}

export const emptyZone: ZoneFormValue = {
  name: "",
  cdn_hostname: "",
  custom_domain: "",
  origin_url: "",
  host_header: "",
  tls_mode: "managed",
  video: false,
  profile: "vod",
  playlist_ttl: "2s",
  segment_ttl: "12h",
  cors_origin: "*",
  brotli_level: 5,
  status: "active",
  origin_ssl_verify: false,
  origin_follow_redirects: false,
  forward_host_header: false,
};

export function zoneToForm(z: Zone): ZoneFormValue {
  return {
    name: z.name,
    cdn_hostname: z.cdn_hostname,
    custom_domain: z.custom_domain ?? "",
    origin_url: z.origin_url,
    host_header: z.host_header ?? "",
    tls_mode: z.tls_mode,
    video: z.video,
    profile: z.profile,
    playlist_ttl: z.playlist_ttl,
    segment_ttl: z.segment_ttl,
    cors_origin: z.cors_origin,
    brotli_level: z.brotli_level,
    status: z.status,
    origin_ssl_verify: z.origin_ssl_verify ?? false,
    origin_follow_redirects: z.origin_follow_redirects ?? false,
    forward_host_header: z.forward_host_header ?? false,
  };
}

/** Validate the form; returns a field->error map (empty = valid). */
export function validateZone(v: ZoneFormValue, mode: "create" | "edit"): Record<string, string> {
  const errs: Record<string, string> = {};
  const name = validateRequired(v.name);
  if (name) errs.name = name;
  if (mode === "create") {
    const host = validateHostname(v.cdn_hostname);
    if (host) errs.cdn_hostname = host;
  }
  const custom = validateOptionalHostname(v.custom_domain);
  if (custom) errs.custom_domain = custom;
  const origin = validateURL(v.origin_url);
  if (origin) errs.origin_url = origin;
  const hh = validateOptionalHostname(v.host_header);
  if (hh) errs.host_header = hh;
  if (v.video) {
    const pt = validateTTL(v.playlist_ttl);
    if (pt) errs.playlist_ttl = pt;
    const st = validateTTL(v.segment_ttl);
    if (st) errs.segment_ttl = st;
  }
  return errs;
}

/** Build the create payload from form state. */
export function toCreateInput(v: ZoneFormValue): CreateZoneInput {
  return {
    name: v.name.trim(),
    cdn_hostname: v.cdn_hostname.trim(),
    custom_domain: v.custom_domain.trim() || null,
    origin_url: v.origin_url.trim(),
    host_header: v.host_header.trim() || undefined,
    tls_mode: v.tls_mode as CreateZoneInput["tls_mode"],
    video: v.video,
    profile: v.profile as CreateZoneInput["profile"],
    playlist_ttl: v.playlist_ttl.trim(),
    segment_ttl: v.segment_ttl.trim(),
    cors_origin: v.cors_origin.trim() || "*",
    brotli_level: v.brotli_level,
  };
}

const PROFILE_OPTIONS = [
  { value: "vod", label: "VOD (on-demand)" },
  { value: "live", label: "Live" },
];

// Origin connection options (Bunny-style; migration 00025). Boolean toggles rendered in
// the edit (Settings) form only. `key` indexes the boolean fields on ZoneFormValue.
const ORIGIN_OPTIONS: {
  key: "origin_ssl_verify" | "forward_host_header" | "origin_follow_redirects";
  label: string;
  desc: string;
}[] = [
  {
    key: "origin_ssl_verify",
    label: "Verify origin SSL certificate",
    desc: "Validate the origin's TLS certificate against the system CA bundle when connecting over HTTPS. Off = trust the origin's cert (today's behavior).",
  },
  {
    key: "forward_host_header",
    label: "Forward host header",
    desc: "Send the visitor's Host header to the origin instead of the upstream Host above. Leave off in most cases.",
  },
  {
    key: "origin_follow_redirects",
    label: "Follow redirects",
    desc: "If the origin returns a 301/302, follow it once at the edge instead of returning the redirect to the visitor. Applies to direct-origin (non-shielded) zones.",
  },
];

export function ZoneForm({
  value,
  onChange,
  errors,
  mode,
}: {
  value: ZoneFormValue;
  onChange: (v: ZoneFormValue) => void;
  errors: Record<string, string>;
  mode: "create" | "edit";
}) {
  const set = <K extends keyof ZoneFormValue>(k: K, v: ZoneFormValue[K]) =>
    onChange({ ...value, [k]: v });

  // In create mode the CDN hostname auto-fills from the name (<slug>.<base>) until
  // the user hand-edits it. We detect "still auto" by comparing against the suggestion
  // for the *current* name, so a manual edit stops the sync.
  const onNameChange = (name: string) => {
    if (mode === "create" && (value.cdn_hostname === "" || value.cdn_hostname === suggestHostname(value.name))) {
      onChange({ ...value, name, cdn_hostname: suggestHostname(name) });
    } else {
      set("name", name);
    }
  };

  return (
    <div className="space-y-5">
      <Section title="Basics">
        <Field label="Name" error={errors.name} hint={mode === "create" ? "Used to suggest the CDN hostname below." : undefined}>
          <Input value={value.name} onChange={(e) => onNameChange(e.target.value)} placeholder="acme-static" />
        </Field>
        <Field
          label="CDN hostname"
          error={errors.cdn_hostname}
          hint={
            mode === "edit"
              ? "Hostname can't be changed after creation."
              : `Each zone gets its own hostname under ${CDN_BASE_DOMAIN}. This is the CNAME target — point your domain here, or add a custom domain after creating.`
          }
        >
          <Input
            value={value.cdn_hostname}
            onChange={(e) => set("cdn_hostname", e.target.value)}
            placeholder={`acme-static.${CDN_BASE_DOMAIN}`}
            disabled={mode === "edit"}
          />
        </Field>
        {/* Custom domains are added AFTER creating a zone, with DNS verification +
           per-domain cert issuance (the Custom Domains tab / Step 4.8). The old
           unverified Add-zone field is removed so a new zone can't be born pointing
           at an external hostname with no cert. */}
      </Section>

      <Section title="Origin & TLS">
        <Field label="Origin URL" error={errors.origin_url}>
          <Input value={value.origin_url} onChange={(e) => set("origin_url", e.target.value)} placeholder="https://origin.acme.com" />
        </Field>
        <Field
          label="Upstream Host header (optional)"
          error={errors.host_header}
          hint="Host sent to the origin. Leave blank to use the origin's own host. Set it when the origin serves by a different name (e.g. behind a proxy)."
        >
          <Input value={value.host_header} onChange={(e) => set("host_header", e.target.value)} placeholder="origin.acme.com" />
        </Field>
        <Field label="SSL certificate" hint="Free SSL via Let's Encrypt — issued and renewed automatically.">
          <div className="flex items-center justify-between rounded-lg border border-border px-3 py-2">
            <div className="text-sm font-medium text-foreground">Automatic SSL</div>
            <span className="rounded-full bg-success/10 px-2 py-0.5 text-xs font-medium text-success">managed</span>
          </div>
        </Field>
        {mode === "edit" && (
          <div className="space-y-2">
            {ORIGIN_OPTIONS.map((o) => (
              <div key={o.key} className="flex items-center justify-between gap-3 rounded-lg border border-border px-3 py-2">
                <div className="min-w-0">
                  <div className="text-sm font-medium text-foreground">{o.label}</div>
                  <div className="text-xs text-muted-foreground">{o.desc}</div>
                </div>
                <Switch checked={value[o.key]} onCheckedChange={(c) => set(o.key, c)} aria-label={o.label} />
              </div>
            ))}
          </div>
        )}
      </Section>

      <Section title="Delivery">
        <div className="flex items-center justify-between gap-3 rounded-lg border border-border px-3 py-2">
          <div>
            <div className="flex items-center gap-1.5 text-sm font-medium text-foreground">
              <Film className="size-4 text-muted-foreground" />
              Video mode
            </div>
            <div className="text-xs text-muted-foreground">
              Turn on for any video zone. <span className="font-medium text-foreground">Required for live streaming</span>{" "}
              — always re-fetches the <code>.m3u8</code> playlist so live streams never freeze. Also right for on-demand HLS
              and big <code>.mp4</code> files (slice + cache for fast seeking).
            </div>
          </div>
          <Switch checked={value.video} onCheckedChange={(c) => set("video", c)} aria-label="Video delivery" />
        </div>
        {value.video && (
          <div className="space-y-3 rounded-lg border border-dashed border-border p-3">
            <Field label="Profile">
              <Select value={value.profile} onChange={(e) => set("profile", e.target.value)} options={PROFILE_OPTIONS} />
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="Playlist TTL" error={errors.playlist_ttl} hint="Keep short — .m3u8 must stay fresh.">
                <Input value={value.playlist_ttl} onChange={(e) => set("playlist_ttl", e.target.value)} placeholder="2s" />
              </Field>
              <Field label="Segment TTL" error={errors.segment_ttl} hint="Segments are immutable — cache long.">
                <Input value={value.segment_ttl} onChange={(e) => set("segment_ttl", e.target.value)} placeholder="12h" />
              </Field>
            </div>
          </div>
        )}
        <div className="grid grid-cols-2 gap-3">
          <Field label="CORS origin">
            <Input value={value.cors_origin} onChange={(e) => set("cors_origin", e.target.value)} placeholder="*" />
          </Field>
          <Field label="Brotli level" hint="1–11; dynamic content ~4–6.">
            <Input
              type="number"
              min={1}
              max={11}
              value={value.brotli_level}
              onChange={(e) => set("brotli_level", Math.max(1, Math.min(11, Number(e.target.value) || 1)))}
            />
          </Field>
        </div>
      </Section>

      {mode === "edit" && (
        <Section title="Status">
          <div className="flex items-center justify-between rounded-lg border border-border px-3 py-2">
            <div>
              <div className="text-sm font-medium text-foreground">Active</div>
              <div className="text-xs text-muted-foreground">Disabled zones stop being served.</div>
            </div>
            <Switch
              checked={value.status === "active"}
              onCheckedChange={(c) => set("status", c ? "active" : "disabled")}
              aria-label="Zone active"
            />
          </div>
        </Section>
      )}

      <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <Clock className="size-3.5" />
        Saved changes reach edges on the next config pull (~15s) — not instant (purge is the instant path).
      </p>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <fieldset className="space-y-3">
      <legend className="mb-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground">{title}</legend>
      {children}
    </fieldset>
  );
}

function Field({
  label,
  error,
  hint,
  children,
}: {
  label: string;
  error?: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1">
      <Label>{label}</Label>
      {children}
      {error ? (
        <p className="text-xs text-destructive">{error}</p>
      ) : hint ? (
        <p className="text-xs text-muted-foreground">{hint}</p>
      ) : null}
    </div>
  );
}
