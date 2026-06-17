import * as React from "react";
import {
  Lock,
  Clock,
  Loader2,
  AlertTriangle,
  Copy,
  Plus,
  Trash2,
  RefreshCw,
  Globe,
} from "lucide-react";
import { toast } from "sonner";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import {
  useZoneDomains,
  useAddDomain,
  useVerifyDomain,
  useDeleteDomain,
} from "@/hooks/use-domains";
import type { CustomDomain } from "@/lib/types";
import { timeAgo } from "@/lib/format";

/** Status presentation for a custom domain. Active-with-an-error is its own
    "action needed" state (renewal failing) so the operator sees it without losing
    the fact that the old cert is still serving. */
export function domainStatusBadge(d: CustomDomain) {
  if (d.status === "active") {
    if (d.last_error) {
      return (
        <Badge variant="warning" className="gap-1">
          <AlertTriangle className="size-3" /> Action needed
        </Badge>
      );
    }
    return (
      <Badge variant="success" className="gap-1">
        <Lock className="size-3" /> Active
      </Badge>
    );
  }
  const map: Record<string, { label: string; variant: "warning" | "danger" | "muted"; spin?: boolean; clock?: boolean }> = {
    pending_dns: { label: "Waiting for DNS", variant: "warning", clock: true },
    verifying: { label: "Verifying", variant: "warning", spin: true },
    issuing: { label: "Issuing certificate", variant: "warning", spin: true },
    renewing: { label: "Renewing", variant: "warning", spin: true },
    failed: { label: "Action needed", variant: "danger" },
  };
  const m = map[d.status] ?? { label: d.status, variant: "muted" as const };
  return (
    <Badge variant={m.variant} className="gap-1">
      {m.spin && <Loader2 className="size-3 animate-spin" />}
      {m.clock && <Clock className="size-3" />}
      {!m.spin && !m.clock && m.variant === "danger" && <AlertTriangle className="size-3" />}
      {m.label}
    </Badge>
  );
}

export function CustomDomainsTab({ zoneId }: { zoneId: number }) {
  const domainsQ = useZoneDomains(zoneId);
  const add = useAddDomain(zoneId);
  const [input, setInput] = React.useState("");

  const onAdd = () => {
    const domain = input.trim().toLowerCase();
    if (!domain) return;
    add.mutate(
      { domain },
      {
        onSuccess: (d) => {
          setInput("");
          toast.success("Domain added", {
            description: d.is_apex ? "Apex domain — see the guidance below." : "Create the CNAME, then Brisk verifies + issues TLS.",
          });
        },
        onError: (e) => toast.error("Couldn't add domain", { description: (e as Error).message }),
      },
    );
  };

  const domains = domainsQ.data ?? [];

  return (
    <div className="space-y-5">
      <Card>
        <CardContent className="space-y-3 p-5">
          <div>
            <h3 className="text-sm font-medium">Add a custom domain</h3>
            <p className="text-xs text-muted-foreground">
              Point a domain you own (e.g. <code className="font-mono">cdn.yoursite.com</code>) at this zone. Brisk
              verifies the DNS, then issues + auto-renews HTTPS — served from every edge.
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Input
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && onAdd()}
              placeholder="cdn.yoursite.com"
              className="max-w-xs font-mono"
              autoCapitalize="none"
              spellCheck={false}
            />
            <Button onClick={onAdd} disabled={add.isPending || !input.trim()}>
              {add.isPending ? <Loader2 className="animate-spin" /> : <Plus />}
              Add domain
            </Button>
          </div>
        </CardContent>
      </Card>

      {domainsQ.isLoading ? (
        <Skeleton className="h-32 w-full" />
      ) : domains.length === 0 ? (
        <Card>
          <CardContent className="flex flex-col items-center gap-2 py-10 text-center">
            <Globe className="size-7 text-muted-foreground" />
            <p className="text-sm text-muted-foreground">No custom domains yet. Add one above to get automatic HTTPS.</p>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-3">
          {domains.map((d) => (
            <DomainRow key={d.id} zoneId={zoneId} domain={d} />
          ))}
        </div>
      )}
    </div>
  );
}

function DomainRow({ zoneId, domain: d }: { zoneId: number; domain: CustomDomain }) {
  const verify = useVerifyDomain(zoneId);
  const del = useDeleteDomain(zoneId);
  const [confirmOpen, setConfirmOpen] = React.useState(false);

  const copy = (text: string, label: string) => {
    navigator.clipboard?.writeText(text);
    toast.success(`${label} copied`);
  };

  const isActiveClean = d.status === "active" && !d.last_error;

  return (
    <Card>
      <CardContent className="space-y-3 p-5">
        <div className="flex flex-wrap items-center gap-2">
          <code className="font-mono text-sm font-medium text-foreground">{d.domain}</code>
          {domainStatusBadge(d)}
          {d.is_apex && <Badge variant="outline">apex</Badge>}
          {d.cert_staging && <Badge variant="muted">staging cert</Badge>}
          <div className="ml-auto flex items-center gap-1">
            <Button
              size="sm"
              variant="outline"
              onClick={() =>
                verify.mutate(d.id, {
                  onSuccess: (r) =>
                    toast.message(`Checked ${r.domain}`, {
                      description: r.status === "active" ? "Active 🔒" : r.last_error || `Status: ${r.status}`,
                    }),
                  onError: (e) => toast.error("Check failed", { description: (e as Error).message }),
                })
              }
              disabled={verify.isPending}
            >
              {verify.isPending ? <Loader2 className="animate-spin" /> : <RefreshCw />}
              Check now
            </Button>
            <Button size="sm" variant="ghost" className="text-danger" onClick={() => setConfirmOpen(true)}>
              <Trash2 />
            </Button>
          </div>
        </div>

        {/* CNAME instructions — show until active, and always for apex guidance. */}
        {!isActiveClean && (
          <div className="rounded-md border border-border bg-muted/30 p-3 text-sm">
            <p className="text-muted-foreground">{d.instructions}</p>
            {!d.is_apex && d.cname_target && (
              <div className="mt-2 flex flex-wrap items-center gap-2 font-mono text-xs">
                <span className="text-muted-foreground">{d.domain}</span>
                <span className="text-muted-foreground">CNAME</span>
                <span className="text-foreground">{d.cname_target}</span>
                <Button
                  size="sm"
                  variant="ghost"
                  className="h-6 px-2"
                  onClick={() => copy(d.cname_target, "CNAME target")}
                  aria-label="Copy CNAME target"
                >
                  <Copy className="size-3.5" />
                </Button>
              </div>
            )}
          </div>
        )}

        {/* Honest error / cert info line. */}
        {d.last_error && (
          <p className="flex items-start gap-1.5 text-xs text-warning">
            <AlertTriangle className="mt-0.5 size-3.5 shrink-0" /> {d.last_error}
          </p>
        )}
        {isActiveClean && (
          <p className="text-xs text-muted-foreground">
            🔒 HTTPS active{d.cert_issuer ? ` · issued by ${d.cert_issuer}` : ""}
            {typeof d.days_remaining === "number" ? ` · cert valid ${d.days_remaining}d` : ""}
            {" · auto-renew on"}
          </p>
        )}
        <p className="text-[11px] text-muted-foreground">
          added {timeAgo(d.created_at)} · DNS propagation can take minutes to 48h; Brisk re-checks automatically.
        </p>
      </CardContent>

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Trash2 className="size-4 text-danger" /> Remove {d.domain}?
            </DialogTitle>
            <DialogDescription>
              This detaches the domain and removes its certificate from every edge. The domain will stop serving over
              Brisk until re-added. The zone&apos;s Brisk hostname is unaffected.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={() =>
                del.mutate(d.id, {
                  onSuccess: () => {
                    setConfirmOpen(false);
                    toast.success(`${d.domain} removed`);
                  },
                  onError: (e) => toast.error("Couldn't remove", { description: (e as Error).message }),
                })
              }
              disabled={del.isPending}
            >
              {del.isPending && <Loader2 className="animate-spin" />}
              Remove domain
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  );
}
