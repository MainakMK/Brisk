import * as React from "react";
import { ShieldAlert, Loader2, Save, Info, Plus, Trash2, RefreshCw, Link2, FileWarning, Ban, Lock } from "lucide-react";
import { toast } from "sonner";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Select } from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import {
  useZoneWAF,
  useSetZoneWAF,
  useCreateWAFRule,
  useDeleteWAFRule,
  useCreateWAFRateLimit,
  useDeleteWAFRateLimit,
  useZoneSecurityEvents,
} from "@/hooks/use-waf";
import {
  useZone,
  useSetZoneHotlink,
  useSetZoneErrorPage,
  useSetZoneBlockedIPs,
  useSetZoneAccessFlags,
} from "@/hooks/use-zones";
import { timeAgo } from "@/lib/format";
import type {
  WAFConfig,
  WAFCustomRule,
  WAFRateLimit,
  SecurityEvent,
  WAFRuleField,
  WAFRuleOp,
  WAFRuleAction,
} from "@/lib/types";

/** Zone → Security tab (Phase 4 Step 4): per-zone WAF (detect/block) + managed
    OWASP CRS + WordPress preset + custom rules + rate limits + the firewall log. */
export function SecurityTab({ zoneId }: { zoneId: number }) {
  const wafQ = useZoneWAF(zoneId);
  if (wafQ.isLoading && !wafQ.data) {
    return <Skeleton className="h-64 w-full" />;
  }
  if (!wafQ.data) {
    return <p className="text-sm text-danger">Couldn't load WAF config.</p>;
  }
  return (
    <div className="space-y-5">
      <WAFSettingsCard zoneId={zoneId} waf={wafQ.data} />
      {wafQ.data.enabled && (
        <>
          <CustomRulesCard zoneId={zoneId} rules={wafQ.data.rules} />
          <RateLimitsCard zoneId={zoneId} limits={wafQ.data.rate_limits} />
          <SecurityEventsCard zoneId={zoneId} mode={wafQ.data.mode} />
        </>
      )}
      {/* Hotlink protection is independent of the WAF — always available. */}
      <HotlinkCard zoneId={zoneId} />
      {/* Custom 502/504 error page is independent of the WAF — always available. */}
      <ErrorPageCard zoneId={zoneId} />
      {/* Blocked-IP denylist is independent of the WAF — always available. */}
      <BlockedIPsCard zoneId={zoneId} />
      {/* Access toggles (block root / block POST) — always available. */}
      <AccessControlCard zoneId={zoneId} />
    </div>
  );
}

// --- access toggles -------------------------------------------------------

/** Two simple per-zone request toggles (Bunny "Block root path access" + "Block POST
    requests"): 403 the bare root / directory roots; 405 POST. Both default off. */
function AccessControlCard({ zoneId }: { zoneId: number }) {
  const zoneQ = useZone(zoneId);
  const save = useSetZoneAccessFlags(zoneId);
  const z = zoneQ.data;

  const [blockRoot, setBlockRoot] = React.useState(false);
  const [blockPost, setBlockPost] = React.useState(false);

  // Re-seed from the zone whenever it refreshes (e.g. after a save).
  React.useEffect(() => {
    if (!z) return;
    setBlockRoot(!!z.block_root_path);
    setBlockPost(!!z.block_post);
  }, [z?.block_root_path, z?.block_post]);

  const submit = () => {
    save.mutate(
      { block_root_path: blockRoot, block_post: blockPost },
      {
        onSuccess: () =>
          toast.success("Access settings saved", {
            description: "Edges re-pull in ~15s (config_version bumped).",
          }),
        onError: (e) => toast.error("Save failed", { description: (e as Error).message }),
      },
    );
  };

  if (zoneQ.isLoading && !z) {
    return <Skeleton className="h-48 w-full" />;
  }

  const anyOn = !!(z?.block_root_path || z?.block_post);

  return (
    <Card>
      <CardHeader className="flex-row items-center gap-2">
        <Lock className="size-4 text-primary" />
        <CardTitle>Access control</CardTitle>
        {anyOn ? <Badge variant="warning">On</Badge> : <Badge variant="muted">Off</Badge>}
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex items-center justify-between rounded-md border border-border px-3 py-2">
          <div>
            <div className="text-sm font-medium">Block root path access</div>
            <div className="text-xs text-muted-foreground">
              Return <strong>403</strong> for the bare domain and any directory root (a path ending
              in <code>/</code>), forcing deep links to real files. Deep file URLs still work; health
              checks are unaffected.
            </div>
          </div>
          <Switch checked={blockRoot} onCheckedChange={setBlockRoot} aria-label="Block root path access" />
        </div>

        <div className="flex items-center justify-between rounded-md border border-border px-3 py-2">
          <div>
            <div className="text-sm font-medium">Block POST requests</div>
            <div className="text-xs text-muted-foreground">
              Reject <code>POST</code> with a <strong>405</strong>. Useful for a pure static / video
              zone that should never accept uploads or form posts.
            </div>
          </div>
          <Switch checked={blockPost} onCheckedChange={setBlockPost} aria-label="Block POST requests" />
        </div>

        <div className="flex justify-end">
          <Button onClick={submit} disabled={save.isPending}>
            {save.isPending ? <Loader2 className="animate-spin" /> : <Save />}
            Save access settings
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

// --- blocked IPs ----------------------------------------------------------

/** A simple per-zone IP/CIDR denylist (Bunny "Blocked IPs"). Listed clients get a 403 on
    content; /healthz is never blocked. Empty => off (byte-identical). */
function BlockedIPsCard({ zoneId }: { zoneId: number }) {
  const zoneQ = useZone(zoneId);
  const save = useSetZoneBlockedIPs(zoneId);
  const z = zoneQ.data;

  const [ips, setIps] = React.useState("");

  // Re-seed from the zone whenever it refreshes (e.g. after a save). Show the stored
  // comma list one-per-line for easy editing.
  React.useEffect(() => {
    if (!z) return;
    setIps((z.blocked_ips ?? "").split(",").filter(Boolean).join("\n"));
  }, [z?.blocked_ips]);

  const submit = () => {
    // Accept newlines OR commas; normalize to the comma list the API expects + validates.
    const list = ips
      .split(/[\n,]+/)
      .map((s) => s.trim())
      .filter(Boolean)
      .join(",");
    save.mutate(
      { blocked_ips: list },
      {
        onSuccess: () =>
          toast.success("Blocked IPs saved", {
            description: "Edges re-pull in ~15s (config_version bumped).",
          }),
        onError: (e) => toast.error("Save failed", { description: (e as Error).message }),
      },
    );
  };

  if (zoneQ.isLoading && !z) {
    return <Skeleton className="h-48 w-full" />;
  }

  const count = (z?.blocked_ips ?? "").split(",").filter(Boolean).length;

  return (
    <Card>
      <CardHeader className="flex-row items-center gap-2">
        <Ban className="size-4 text-primary" />
        <CardTitle>Blocked IPs</CardTitle>
        {count > 0 ? (
          <Badge variant="danger">{count} blocked</Badge>
        ) : (
          <Badge variant="muted">Off</Badge>
        )}
      </CardHeader>
      <CardContent className="space-y-4">
        <Field label="Blocked IPs / CIDRs (one per line, or comma-separated)">
          <textarea
            value={ips}
            onChange={(e) => setIps(e.target.value)}
            rows={5}
            spellCheck={false}
            placeholder={"203.0.113.4\n198.51.100.0/24\n2001:db8::/32"}
            className="w-full rounded-md border border-border bg-transparent px-3 py-2 font-mono text-sm outline-none placeholder:text-muted-foreground/60 focus-visible:ring-1 focus-visible:ring-ring"
          />
        </Field>
        <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
          <Info className="mt-0.5 size-3.5 shrink-0" />
          Listed clients get a <strong>403</strong> on this zone&apos;s content (everyone else is
          allowed). IPv4/IPv6 addresses or CIDR blocks. Your health checks are never blocked.
        </p>
        <div className="flex justify-end">
          <Button onClick={submit} disabled={save.isPending}>
            {save.isPending ? <Loader2 className="animate-spin" /> : <Save />}
            Save blocked IPs
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

// --- custom 502/504 error page -------------------------------------------

/** Branded HTML the edge serves when the origin is unreachable (nginx-generated 502/504),
    Bunny "502/504 error pages". Empty => off (nginx's default page; byte-identical). */
function ErrorPageCard({ zoneId }: { zoneId: number }) {
  const zoneQ = useZone(zoneId);
  const save = useSetZoneErrorPage(zoneId);
  const z = zoneQ.data;

  const [html, setHtml] = React.useState("");

  // Re-seed from the zone whenever it refreshes (e.g. after a save).
  React.useEffect(() => {
    if (!z) return;
    setHtml(z.error_5xx_html ?? "");
  }, [z?.error_5xx_html]);

  const submit = () => {
    if (html.length > 64 * 1024) {
      toast.error("Error page is too large", { description: "Keep it under 64 KB." });
      return;
    }
    save.mutate(
      { html },
      {
        onSuccess: () =>
          toast.success("Error page saved", {
            description: "Edges re-pull in ~15s (config_version bumped).",
          }),
        onError: (e) => toast.error("Save failed", { description: (e as Error).message }),
      },
    );
  };

  if (zoneQ.isLoading && !z) {
    return <Skeleton className="h-48 w-full" />;
  }

  const on = !!(z?.error_5xx_html && z.error_5xx_html.trim());

  return (
    <Card>
      <CardHeader className="flex-row items-center gap-2">
        <FileWarning className="size-4 text-primary" />
        <CardTitle>Custom error page (502 / 504)</CardTitle>
        {on ? <Badge variant="success">On</Badge> : <Badge variant="muted">Off</Badge>}
      </CardHeader>
      <CardContent className="space-y-4">
        <p className="text-xs text-muted-foreground">
          When your origin is unreachable or times out, edges show this branded HTML instead of
          nginx&apos;s default 502/504 page — the original status code is preserved. Leave it{" "}
          <strong>empty</strong> to use the default (off). Edge rate-limit/overload 503s are{" "}
          <strong>not</strong> affected.
        </p>
        <Field label="Error page HTML (served as-is on a 502 / 504)">
          <textarea
            value={html}
            onChange={(e) => setHtml(e.target.value)}
            rows={8}
            spellCheck={false}
            placeholder={"<!doctype html>\n<html><body>\n  <h1>We'll be right back</h1>\n  <p>This site is briefly unavailable. Please try again shortly.</p>\n</body></html>"}
            className="w-full rounded-md border border-border bg-transparent px-3 py-2 font-mono text-xs outline-none placeholder:text-muted-foreground/60 focus-visible:ring-1 focus-visible:ring-ring"
          />
        </Field>
        <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
          <Info className="mt-0.5 size-3.5 shrink-0" />
          Self-contained HTML works best (inline CSS, no external assets — the origin may be down).
          Max 64 KB.
        </p>
        <div className="flex justify-end">
          <Button onClick={submit} disabled={save.isPending}>
            {save.isPending ? <Loader2 className="animate-spin" /> : <Save />}
            Save error page
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

// --- hotlink protection ---------------------------------------------------

/** Referer-allowlist hotlink protection (Bunny/KeyCDN-style): block other sites from
    embedding this zone's files. Off by default; renders byte-identical nginx until on. */
function HotlinkCard({ zoneId }: { zoneId: number }) {
  const zoneQ = useZone(zoneId);
  const save = useSetZoneHotlink(zoneId);
  const z = zoneQ.data;

  const [enabled, setEnabled] = React.useState(false);
  const [referrers, setReferrers] = React.useState("");
  const [allowEmpty, setAllowEmpty] = React.useState(true);

  // Re-seed from the zone whenever it refreshes (e.g. after a save).
  React.useEffect(() => {
    if (!z) return;
    setEnabled(!!z.hotlink_enabled);
    // Show the stored comma list one-per-line for easy editing.
    setReferrers((z.hotlink_allowed_referrers ?? "").split(",").filter(Boolean).join("\n"));
    setAllowEmpty(z.hotlink_allow_empty_referer ?? true);
  }, [z?.hotlink_enabled, z?.hotlink_allowed_referrers, z?.hotlink_allow_empty_referer]);

  const submit = () => {
    // Accept newlines OR commas; normalize to the comma list the API expects.
    const list = referrers
      .split(/[\n,]+/)
      .map((s) => s.trim())
      .filter(Boolean)
      .join(",");
    if (enabled && !list && !allowEmpty) {
      toast.error("Add at least one referrer, or allow empty referrers", {
        description: "Otherwise nearly every request would be blocked.",
      });
      return;
    }
    save.mutate(
      { enabled, allowed_referrers: list, allow_empty_referer: allowEmpty },
      {
        onSuccess: () =>
          toast.success("Hotlink protection saved", {
            description: "Edges re-pull in ~15s (config_version bumped).",
          }),
        onError: (e) => toast.error("Save failed", { description: (e as Error).message }),
      },
    );
  };

  if (zoneQ.isLoading && !z) {
    return <Skeleton className="h-48 w-full" />;
  }

  return (
    <Card>
      <CardHeader className="flex-row items-center gap-2">
        <Link2 className="size-4 text-primary" />
        <CardTitle>Hotlink Protection</CardTitle>
        {z?.hotlink_enabled ? <Badge variant="success">On</Badge> : <Badge variant="muted">Off</Badge>}
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex items-center justify-between rounded-md border border-border px-3 py-2">
          <div>
            <div className="text-sm font-medium">Enable hotlink protection</div>
            <div className="text-xs text-muted-foreground">
              Stop other websites from embedding this zone&apos;s files (a Referer allowlist). Off by
              default. Requests referred from an allowed host pass; everything else gets a 403.
            </div>
          </div>
          <Switch checked={enabled} onCheckedChange={setEnabled} aria-label="Enable hotlink protection" />
        </div>

        {enabled && (
          <>
            <Field label="Allowed referrers (one per line, or comma-separated)">
              <textarea
                value={referrers}
                onChange={(e) => setReferrers(e.target.value)}
                rows={4}
                spellCheck={false}
                placeholder={"example.com\n*.example.com"}
                className="w-full rounded-md border border-border bg-transparent px-3 py-2 font-mono text-sm outline-none placeholder:text-muted-foreground/60 focus-visible:ring-1 focus-visible:ring-ring"
              />
            </Field>
            <p className="text-xs text-muted-foreground">
              Wildcards like <code>*.example.com</code> work. Your own CDN host is always allowed. Paste a
              full URL and we&apos;ll keep just the hostname.
            </p>

            <div className="flex items-center justify-between rounded-md border border-border px-3 py-2">
              <div>
                <div className="text-sm font-medium">Allow direct access (no Referer)</div>
                <div className="text-xs text-muted-foreground">
                  Keep <strong>ON</strong> (recommended) so direct hits, email clients, and privacy
                  browsers still work. Turn OFF to also block direct URL access — stricter, but may break
                  legitimate users.
                </div>
              </div>
              <Switch checked={allowEmpty} onCheckedChange={setAllowEmpty} aria-label="Allow empty referer" />
            </div>

            <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
              <Info className="mt-0.5 size-3.5 shrink-0" />
              Referer-based — stops casual embedding and bandwidth theft, but the Referer can be faked
              (e.g. with <code>curl</code>), so it isn&apos;t bulletproof. For paid or private content, use
              signed / expiring URLs instead.
            </p>
          </>
        )}

        <div className="flex justify-end">
          <Button onClick={submit} disabled={save.isPending}>
            {save.isPending ? <Loader2 className="animate-spin" /> : <Save />}
            Save hotlink settings
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

// --- settings -------------------------------------------------------------

function WAFSettingsCard({ zoneId, waf }: { zoneId: number; waf: WAFConfig }) {
  const save = useSetZoneWAF(zoneId);
  const [enabled, setEnabled] = React.useState(waf.enabled);
  const [mode, setMode] = React.useState(waf.mode || "detect");
  const [ruleset, setRuleset] = React.useState(waf.managed_ruleset || "owasp_crs");
  const [paranoia, setParanoia] = React.useState(String(waf.paranoia || 1));
  const [wp, setWp] = React.useState(waf.wp_preset);
  const [failOpen, setFailOpen] = React.useState(waf.fail_open);

  // Re-seed when the row refreshes after a save.
  React.useEffect(() => {
    setEnabled(waf.enabled);
    setMode(waf.mode || "detect");
    setRuleset(waf.managed_ruleset || "owasp_crs");
    setParanoia(String(waf.paranoia || 1));
    setWp(waf.wp_preset);
    setFailOpen(waf.fail_open);
  }, [waf.enabled, waf.mode, waf.managed_ruleset, waf.paranoia, waf.wp_preset, waf.fail_open]);

  const submit = () => {
    save.mutate(
      {
        enabled,
        mode: mode === "block" ? "block" : "detect",
        managed_ruleset: ruleset === "off" ? "off" : "owasp_crs",
        paranoia: Math.min(4, Math.max(1, Number(paranoia) || 1)),
        wp_preset: wp,
        fail_open: failOpen,
      },
      {
        onSuccess: () =>
          toast.success("WAF settings saved", {
            description: "Edges re-pull in ~15s (config_version bumped).",
          }),
        onError: (e) => toast.error("Save failed", { description: (e as Error).message }),
      },
    );
  };

  return (
    <Card>
      <CardHeader className="flex-row items-center gap-2">
        <ShieldAlert className="size-4 text-primary" />
        <CardTitle>Web Application Firewall</CardTitle>
        {waf.enabled ? (
          <Badge variant={waf.mode === "block" ? "danger" : "warning"}>
            {waf.mode === "block" ? "Blocking" : "Detect (log only)"}
          </Badge>
        ) : (
          <Badge variant="muted">Off</Badge>
        )}
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex items-center justify-between rounded-md border border-border px-3 py-2">
          <div>
            <div className="text-sm font-medium">Enable WAF for this zone</div>
            <div className="text-xs text-muted-foreground">
              Off by default. Newly enabled zones should start in <strong>Detect</strong> mode to tune
              false positives before enforcing.
            </div>
          </div>
          <Switch checked={enabled} onCheckedChange={setEnabled} aria-label="Enable WAF" />
        </div>

        {enabled && (
          <>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field label="Mode">
                <Select
                  value={mode}
                  onChange={(e) => setMode(e.target.value)}
                  options={[
                    { value: "detect", label: "Detect — log would-block, don't enforce" },
                    { value: "block", label: "Block — enforce (403)" },
                  ]}
                />
              </Field>
              <Field label="Managed ruleset">
                <Select
                  value={ruleset}
                  onChange={(e) => setRuleset(e.target.value)}
                  options={[
                    { value: "owasp_crs", label: "OWASP Core Rule Set v4" },
                    { value: "off", label: "Off (custom rules + rate limits only)" },
                  ]}
                />
              </Field>
              <Field label="CRS sensitivity (paranoia 1–4)">
                <Input
                  value={paranoia}
                  onChange={(e) => setParanoia(e.target.value)}
                  inputMode="numeric"
                  disabled={ruleset === "off"}
                />
              </Field>
              <Field label="On WAF engine error">
                <Select
                  value={failOpen ? "open" : "closed"}
                  onChange={(e) => setFailOpen(e.target.value === "open")}
                  options={[
                    { value: "open", label: "Fail open — keep serving (recommended)" },
                    { value: "closed", label: "Fail closed — block on error" },
                  ]}
                />
              </Field>
            </div>

            <div className="flex items-center justify-between rounded-md border border-border px-3 py-2">
              <div>
                <div className="text-sm font-medium">WordPress preset</div>
                <div className="text-xs text-muted-foreground">
                  One-click: rate-limit <code>/wp-login.php</code>, block <code>/xmlrpc.php</code> +
                  known scanner user-agents.
                </div>
              </div>
              <Switch checked={wp} onCheckedChange={setWp} aria-label="WordPress preset" />
            </div>

            <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
              <Info className="mt-0.5 size-3.5 shrink-0" />
              Higher paranoia catches more but raises false positives — review the Security events
              (would-block) below in Detect mode, then switch to Block. Rate-limit counters are
              per-edge and approximate; large request bodies are not deep-scanned.
            </p>
          </>
        )}

        <div className="flex justify-end">
          <Button onClick={submit} disabled={save.isPending}>
            {save.isPending ? <Loader2 className="animate-spin" /> : <Save />}
            Save WAF settings
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

// --- custom rules ---------------------------------------------------------

const RULE_FIELDS: { value: WAFRuleField; label: string }[] = [
  { value: "ip", label: "Client IP" },
  { value: "path", label: "Path" },
  { value: "method", label: "Method" },
  { value: "header", label: "Header" },
  { value: "user_agent", label: "User-Agent" },
  { value: "country", label: "Country (needs geo)" },
];
const RULE_OPS: { value: WAFRuleOp; label: string }[] = [
  { value: "eq", label: "equals" },
  { value: "prefix", label: "starts with" },
  { value: "regex", label: "matches regex" },
  { value: "cidr", label: "in CIDR" },
];
const RULE_ACTIONS: { value: WAFRuleAction; label: string }[] = [
  { value: "block", label: "Block" },
  { value: "challenge", label: "Challenge" },
  { value: "log", label: "Log" },
  { value: "allow", label: "Allow (skip WAF)" },
];

function CustomRulesCard({ zoneId, rules }: { zoneId: number; rules: WAFCustomRule[] }) {
  const create = useCreateWAFRule(zoneId);
  const del = useDeleteWAFRule(zoneId);
  const [field, setField] = React.useState<WAFRuleField>("path");
  const [op, setOp] = React.useState<WAFRuleOp>("prefix");
  const [value, setValue] = React.useState("");
  const [headerName, setHeaderName] = React.useState("");
  const [action, setAction] = React.useState<WAFRuleAction>("block");
  const [priority, setPriority] = React.useState("100");

  const add = () => {
    if (!value.trim()) {
      toast.error("Enter a value to match");
      return;
    }
    create.mutate(
      {
        priority: Number(priority) || 0,
        field,
        op,
        value: value.trim(),
        header_name: field === "header" ? headerName.trim() : undefined,
        action,
        enabled: true,
      },
      {
        onSuccess: () => {
          setValue("");
          toast.success("Rule added");
        },
        onError: (e) => toast.error("Add failed", { description: (e as Error).message }),
      },
    );
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Custom rules</CardTitle>
        <p className="text-xs text-muted-foreground">
          Evaluated in priority order <em>before</em> the managed ruleset; a terminating action
          (block/challenge/allow) short-circuits. Allow acts as an allowlist (skips the CRS).
        </p>
      </CardHeader>
      <CardContent className="space-y-3">
        {rules.length === 0 ? (
          <p className="text-xs text-muted-foreground">No custom rules.</p>
        ) : (
          <ul className="space-y-1.5">
            {rules.map((r) => (
              <li
                key={r.id}
                className="flex items-center gap-2 rounded-md border border-border px-3 py-1.5 text-sm"
              >
                <span className="tabular text-xs text-muted-foreground">#{r.priority}</span>
                <ActionBadge action={r.action} />
                <span className="truncate">
                  <strong>{r.field}</strong>
                  {r.field === "header" && r.header_name ? `[${r.header_name}]` : ""} {r.op}{" "}
                  <code className="text-xs">{r.value}</code>
                </span>
                <Button
                  size="icon"
                  variant="ghost"
                  className="ml-auto"
                  onClick={() => del.mutate(r.id)}
                  aria-label="Delete rule"
                >
                  <Trash2 className="size-4 text-danger" />
                </Button>
              </li>
            ))}
          </ul>
        )}

        <div className="grid grid-cols-2 gap-2 rounded-md border border-dashed border-border p-3 sm:grid-cols-6">
          <Field label="Priority">
            <Input value={priority} onChange={(e) => setPriority(e.target.value)} inputMode="numeric" />
          </Field>
          <Field label="Field">
            <Select value={field} onChange={(e) => setField(e.target.value as WAFRuleField)} options={RULE_FIELDS} />
          </Field>
          <Field label="Op">
            <Select value={op} onChange={(e) => setOp(e.target.value as WAFRuleOp)} options={RULE_OPS} />
          </Field>
          <Field
            label={field === "header" ? "Header name" : "Value"}
            className={field === "header" ? "" : "sm:col-span-2"}
          >
            {field === "header" ? (
              <Input value={headerName} onChange={(e) => setHeaderName(e.target.value)} placeholder="X-Header" />
            ) : (
              <Input value={value} onChange={(e) => setValue(e.target.value)} placeholder="/admin or 1.2.3.0/24" />
            )}
          </Field>
          {field === "header" && (
            <Field label="Value">
              <Input value={value} onChange={(e) => setValue(e.target.value)} placeholder="match" />
            </Field>
          )}
          <Field label="Action">
            <Select value={action} onChange={(e) => setAction(e.target.value as WAFRuleAction)} options={RULE_ACTIONS} />
          </Field>
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

// --- rate limits ----------------------------------------------------------

function RateLimitsCard({ zoneId, limits }: { zoneId: number; limits: WAFRateLimit[] }) {
  const create = useCreateWAFRateLimit(zoneId);
  const del = useDeleteWAFRateLimit(zoneId);
  const [path, setPath] = React.useState("/wp-login.php");
  const [requests, setRequests] = React.useState("5");
  const [period, setPeriod] = React.useState("60");
  const [key, setKey] = React.useState<"ip" | "ip_path">("ip");
  const [countMode, setCountMode] = React.useState<"all" | "errors_only">("all");

  const add = () => {
    if (!path.startsWith("/")) {
      toast.error("Path must start with /");
      return;
    }
    create.mutate(
      {
        path_match: path.trim(),
        match_type: "exact",
        requests: Number(requests) || 1,
        period_seconds: Number(period) || 60,
        key,
        action: "block",
        count_mode: countMode,
        enabled: true,
      },
      {
        onSuccess: () => toast.success("Rate limit added"),
        onError: (e) => toast.error("Add failed", { description: (e as Error).message }),
      },
    );
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Rate limits</CardTitle>
        <p className="text-xs text-muted-foreground">
          Throttle a path per client (Nginx native). e.g. “5 req / 60s per IP on{" "}
          <code>/wp-login.php</code>”. Counters are <strong>per-edge</strong> and approximate — target
          exact login/OTP paths.
        </p>
      </CardHeader>
      <CardContent className="space-y-3">
        {limits.length === 0 ? (
          <p className="text-xs text-muted-foreground">No rate limits.</p>
        ) : (
          <ul className="space-y-1.5">
            {limits.map((l) => (
              <li
                key={l.id}
                className="flex items-center gap-2 rounded-md border border-border px-3 py-1.5 text-sm"
              >
                <code className="text-xs">{l.path_match}</code>
                <span className="text-muted-foreground">
                  {l.requests} req / {l.period_seconds}s per {l.key === "ip_path" ? "IP+path" : "IP"}
                </span>
                {l.count_mode === "errors_only" && (
                  <Badge variant="warning" className="text-[10px]">
                    errors-only
                  </Badge>
                )}
                <Badge variant="muted" className="text-[10px]">
                  {l.action}
                </Badge>
                <Button
                  size="icon"
                  variant="ghost"
                  className="ml-auto"
                  onClick={() => del.mutate(l.id)}
                  aria-label="Delete rate limit"
                >
                  <Trash2 className="size-4 text-danger" />
                </Button>
              </li>
            ))}
          </ul>
        )}

        <div className="grid grid-cols-2 gap-2 rounded-md border border-dashed border-border p-3 sm:grid-cols-5">
          <Field label="Path (exact)" className="sm:col-span-2">
            <Input value={path} onChange={(e) => setPath(e.target.value)} placeholder="/wp-login.php" />
          </Field>
          <Field label="Requests">
            <Input value={requests} onChange={(e) => setRequests(e.target.value)} inputMode="numeric" />
          </Field>
          <Field label="Per (seconds)">
            <Input value={period} onChange={(e) => setPeriod(e.target.value)} inputMode="numeric" />
          </Field>
          <Field label="Key">
            <Select
              value={key}
              onChange={(e) => setKey(e.target.value as "ip" | "ip_path")}
              options={[
                { value: "ip", label: "per IP" },
                { value: "ip_path", label: "per IP+path" },
              ]}
            />
          </Field>
          <Field label="Count" className="sm:col-span-2">
            <Select
              value={countMode}
              onChange={(e) => setCountMode(e.target.value as "all" | "errors_only")}
              options={[
                { value: "all", label: "All requests (Nginx)" },
                { value: "errors_only", label: "Errors only · 401/403 (Lua)" },
              ]}
            />
          </Field>
          {countMode === "errors_only" && (
            <p className="col-span-2 text-[11px] text-muted-foreground sm:col-span-5">
              Errors-only counts just 401/403 responses (brute-force login/OTP), so a legitimate user
              isn&apos;t throttled. Enforced by the Lua edge — needs the Lua module on the serving edges.
            </p>
          )}
          <div className="col-span-2 flex items-end sm:col-span-5">
            <Button size="sm" onClick={add} disabled={create.isPending}>
              {create.isPending ? <Loader2 className="animate-spin" /> : <Plus />} Add rate limit
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

// --- security events ------------------------------------------------------

function SecurityEventsCard({ zoneId, mode }: { zoneId: number; mode: string }) {
  const [action, setAction] = React.useState("");
  const eventsQ = useZoneSecurityEvents(zoneId, { action });
  const events = eventsQ.data?.events ?? [];

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between">
        <div>
          <CardTitle>Security events</CardTitle>
          <p className="text-xs text-muted-foreground">
            {mode === "detect"
              ? "Detect mode: these are what WOULD be blocked — tune, then switch to Block."
              : "Recent blocks/logs (last 24h)."}{" "}
            Per-edge; newest first.
          </p>
        </div>
        <Select
          value={action}
          onChange={(e) => setAction(e.target.value)}
          options={[
            { value: "", label: "All actions" },
            { value: "block", label: "Blocked" },
            { value: "detect", label: "Would-block" },
            { value: "log", label: "Logged" },
            { value: "challenge", label: "Challenged" },
          ]}
        />
      </CardHeader>
      <CardContent>
        {eventsQ.isLoading && !eventsQ.data ? (
          <Skeleton className="h-32 w-full" />
        ) : events.length === 0 ? (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <RefreshCw className="size-3.5" /> No security events in the window. (Attacks blocked at the
            edge appear here within ~15s.)
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border text-left text-xs text-muted-foreground">
                  <th className="py-1.5 pr-3 font-medium">When</th>
                  <th className="py-1.5 pr-3 font-medium">Action</th>
                  <th className="py-1.5 pr-3 font-medium">Rule</th>
                  <th className="py-1.5 pr-3 font-medium">Client</th>
                  <th className="py-1.5 pr-3 font-medium">Request</th>
                </tr>
              </thead>
              <tbody>
                {events.slice(0, 100).map((e: SecurityEvent, i: number) => (
                  <tr key={i} className="border-b border-border/50 align-top">
                    <td className="py-1.5 pr-3 text-xs text-muted-foreground">{timeAgo(e.ts)}</td>
                    <td className="py-1.5 pr-3">
                      <ActionBadge action={e.action} />
                    </td>
                    <td className="py-1.5 pr-3 text-xs">
                      <code>{e.rule_id}</code>
                      <div className="text-[10px] text-muted-foreground">{e.rule_type}</div>
                    </td>
                    <td className="py-1.5 pr-3 text-xs tabular">
                      {e.client_ip}
                      {e.country ? ` · ${e.country}` : ""}
                    </td>
                    <td className="py-1.5 pr-3 text-xs">
                      <span className="font-medium">{e.method}</span>{" "}
                      <span className="break-all text-muted-foreground">{e.path}</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

// --- shared ---------------------------------------------------------------

function ActionBadge({ action }: { action: string }) {
  const variant =
    action === "block"
      ? "danger"
      : action === "detect" || action === "challenge"
        ? "warning"
        : action === "allow"
          ? "success"
          : "muted";
  return (
    <Badge variant={variant as never} className="capitalize">
      {action === "detect" ? "would-block" : action}
    </Badge>
  );
}

function Field({
  label,
  children,
  className,
}: {
  label: string;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={`space-y-1 ${className ?? ""}`}>
      <Label className="text-xs text-muted-foreground">{label}</Label>
      {children}
    </div>
  );
}
