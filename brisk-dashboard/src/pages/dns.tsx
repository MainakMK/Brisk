import * as React from "react";
import { toast } from "sonner";
import {
  Lock,
  LockOpen,
  ShieldAlert,
  Plus,
  Trash2,
  Loader2,
  AlertTriangle,
  RefreshCw,
  CheckCircle2,
  Timer,
} from "lucide-react";
import { PageHeader } from "@/components/layout/page-header";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";
import { ApiError } from "@/lib/api";
import { RoutingSet } from "@/components/dns/routing-set";
import {
  useDnsStatus,
  useDnsRecords,
  useDnsLock,
  useTlsStatus,
  useAddDnsRecord,
  useDeleteDnsRecord,
  useRequestUnlock,
  useCancelUnlock,
  useRelock,
  useSetUnlockDelay,
} from "@/hooks/use-dns";
import type { DnsLock, DnsRecord, TlsCert } from "@/lib/types";

const IPV4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/;
function isIPv4(v: string) {
  const m = IPV4.exec(v.trim());
  return !!m && m.slice(1).every((o) => +o >= 0 && +o <= 255);
}

const DELAY_OPTIONS = [
  { value: "60", label: "1 minute" },
  { value: "900", label: "15 minutes" },
  { value: "3600", label: "1 hour" },
  { value: "86400", label: "24 hours" },
];

function fmtCountdown(s: number) {
  const m = Math.floor(s / 60);
  const sec = s % 60;
  return `${m}:${String(sec).padStart(2, "0")}`;
}

export default function DnsPage() {
  const status = useDnsStatus();
  const lock = useDnsLock();
  const records = useDnsRecords(status.data?.configured ?? false);
  const unlocked = lock.data?.state === "unlocked";

  return (
    <div className="space-y-5">
      <PageHeader
        title="DNS"
        description="Bunny DNS routing records for the Brisk edge network."
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              status.refetch();
              records.refetch();
              lock.refetch();
            }}
          >
            <RefreshCw className={status.isFetching || records.isFetching ? "animate-spin" : ""} />
            Refresh
          </Button>
        }
      />

      {status.isLoading ? (
        <Skeleton className="h-20 w-full" />
      ) : !status.data?.configured ? (
        <Card className="border-warning/40 bg-warning/5">
          <CardContent className="flex items-center gap-3 p-4 text-sm">
            <AlertTriangle className="size-5 text-warning" />
            <div>
              <div className="font-medium text-warning">DNS not configured</div>
              <div className="text-muted-foreground">
                {status.data?.error ?? status.data?.reason ?? "Set BUNNY_API_KEY + zone in the control plane."}
              </div>
            </div>
          </CardContent>
        </Card>
      ) : (
        <>
          <section className="grid grid-cols-1 gap-4 sm:grid-cols-3">
            <StatCard label="Zone" value={status.data.zone || "—"} mono />
            <StatCard label="Zone ID" value={String(status.data.zone_id ?? "—")} mono />
            <StatCard label="Records" value={String(status.data.record_count ?? records.data?.length ?? 0)} />
          </section>

          <LockBanner lock={lock.data} loading={lock.isLoading} />

          <RoutingSet />

          <ManagedTlsCard />

          <AddRecordForm />

          <Card className="overflow-hidden">
            <CardHeader>
              <CardTitle>Records</CardTitle>
              <p className="text-xs text-muted-foreground">
                Brisk-tagged records are managed here; others are shown read-only and never modified.
              </p>
            </CardHeader>
            <CardContent className="p-0">
              <RecordsTable records={records.data ?? []} loading={records.isLoading} unlocked={unlocked} />
            </CardContent>
          </Card>
        </>
      )}
    </div>
  );
}

/** Managed TLS: both control-plane wildcards (apex *.a2zjav.com + tenant
   *.cdn.a2zjav.com), issuer + expiry + days remaining. The tenant wildcard is what
   covers every <tenant>.cdn.a2zjav.com hostname regardless of its PoP set. */
function ManagedTlsCard() {
  const tls = useTlsStatus();
  if (tls.isLoading) return <Skeleton className="h-28 w-full" />;
  if (!tls.data?.managed) return null;
  const certs = tls.data.certs ?? [];

  const fmtDate = (s?: string) => (s ? new Date(s).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }) : "—");
  const expiryTone = (d?: number) => (d == null ? "" : d < 14 ? "text-destructive" : d < 30 ? "text-warning" : "text-success");

  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <CheckCircle2 className="size-4 text-success" />
          Managed TLS
        </CardTitle>
        <p className="text-xs text-muted-foreground">
          Wildcard certs issued + auto-renewed centrally via Let&apos;s Encrypt (Bunny DNS-01) and fanned to every edge.
        </p>
      </CardHeader>
      <CardContent className="p-0">
        {certs.length === 0 ? (
          <p className="px-4 py-6 text-sm text-muted-foreground">No managed certs issued yet.</p>
        ) : (
          <ul className="divide-y">
            {certs.map((c: TlsCert) => (
              <li key={c.name} className="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3 text-sm">
                <span className="font-mono font-medium">{c.domains}</span>
                {c.staging ? <Badge variant="warning">staging</Badge> : <Badge variant="success">production</Badge>}
                <span className="text-xs text-muted-foreground">issuer {c.issuer || "—"}</span>
                <span className="ml-auto text-xs text-muted-foreground">expires {fmtDate(c.not_after)}</span>
                {c.days_remaining != null && (
                  <span className={cn("text-xs font-medium", expiryTone(c.days_remaining))}>{c.days_remaining}d left</span>
                )}
              </li>
            ))}
          </ul>
        )}
        <p className="border-t px-4 py-2 text-xs text-muted-foreground">
          Tenant hostnames (<span className="font-mono">&lt;name&gt;.cdn.a2zjav.com</span>) are covered by{" "}
          <span className="font-mono">*.cdn.a2zjav.com</span>; the apex <span className="font-mono">cdn.a2zjav.com</span> by{" "}
          <span className="font-mono">*.a2zjav.com</span>. New names also resolve via the{" "}
          <span className="font-mono">*.cdn</span> catch-all CNAME until their per-zone A-set is created.
        </p>
      </CardContent>
    </Card>
  );
}

function StatCard({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <Card className="p-4">
      <div className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">{label}</div>
      <div className={cn("mt-1.5 text-lg font-semibold", mono && "font-mono")}>{value}</div>
    </Card>
  );
}

function LockBanner({ lock, loading }: { lock?: DnsLock; loading: boolean }) {
  const requestUnlock = useRequestUnlock();
  const cancelUnlock = useCancelUnlock();
  const relock = useRelock();
  const setDelay = useSetUnlockDelay();

  const [now, setNow] = React.useState(() => Date.now());
  React.useEffect(() => {
    if (lock?.state !== "pending") return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [lock?.state]);

  if (loading || !lock) return <Skeleton className="h-24 w-full" />;

  const remaining =
    lock.state === "pending" && lock.unlock_available_at
      ? Math.max(0, Math.round((new Date(lock.unlock_available_at).getTime() - now) / 1000))
      : (lock.seconds_remaining ?? 0);

  const tone =
    lock.state === "unlocked"
      ? "border-danger/40 bg-danger/5"
      : lock.state === "pending"
        ? "border-warning/40 bg-warning/5"
        : "border-success/40 bg-success/5";

  return (
    <Card className={cn("p-4", tone)}>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
        <div className="flex flex-1 items-start gap-3">
          {lock.state === "locked" && <Lock className="mt-0.5 size-5 text-success" />}
          {lock.state === "pending" && <Timer className="mt-0.5 size-5 text-warning" />}
          {lock.state === "unlocked" && <LockOpen className="mt-0.5 size-5 text-danger" />}
          <div>
            <div className="flex items-center gap-2 text-sm font-medium">
              {lock.state === "locked" && <span className="text-success">DNS is locked</span>}
              {lock.state === "pending" && <span className="text-warning">Unlock pending</span>}
              {lock.state === "unlocked" && <span className="text-danger">DNS is unlocked</span>}
              <Badge variant="muted" className="capitalize">
                {lock.state}
              </Badge>
            </div>
            <p className="mt-0.5 text-xs text-muted-foreground">
              {lock.state === "locked" &&
                "Records are protected from deletion. Unlocking takes effect after a cooldown so an accidental click can't remove routing."}
              {lock.state === "pending" && (
                <>
                  Deletes unlock in <span className="font-mono font-medium text-foreground">{fmtCountdown(remaining)}</span>.
                  Cancel any time to stay protected.
                </>
              )}
              {lock.state === "unlocked" && "Deletes are enabled. Re-lock when you're done — locking is instant."}
            </p>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          {lock.state === "locked" && (
            <>
              <Select
                className="h-8 w-32"
                value={String(lock.unlock_delay_seconds)}
                onChange={(e) => setDelay.mutate(Number(e.target.value))}
                options={DELAY_OPTIONS}
                aria-label="Unlock cooldown"
              />
              <Button size="sm" variant="outline" onClick={() => requestUnlock.mutate()} disabled={requestUnlock.isPending}>
                {requestUnlock.isPending ? <Loader2 className="animate-spin" /> : <LockOpen />}
                Request unlock
              </Button>
            </>
          )}
          {lock.state === "pending" && (
            <Button size="sm" variant="outline" onClick={() => cancelUnlock.mutate()} disabled={cancelUnlock.isPending}>
              {cancelUnlock.isPending ? <Loader2 className="animate-spin" /> : <Lock />}
              Cancel
            </Button>
          )}
          {lock.state === "unlocked" && (
            <Button size="sm" onClick={() => relock.mutate()} disabled={relock.isPending}>
              {relock.isPending ? <Loader2 className="animate-spin" /> : <Lock />}
              Re-lock now
            </Button>
          )}
        </div>
      </div>
    </Card>
  );
}

function AddRecordForm() {
  const add = useAddDnsRecord();
  const [name, setName] = React.useState("");
  const [value, setValue] = React.useState("");
  const [ttl, setTtl] = React.useState(60);
  const [comment, setComment] = React.useState("");
  const [err, setErr] = React.useState<string | null>(null);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!isIPv4(value)) {
      setErr("Enter a valid IPv4 address.");
      return;
    }
    setErr(null);
    add.mutate(
      { name: name.trim(), value: value.trim(), ttl, comment: comment.trim() || undefined },
      {
        onSuccess: (res) => {
          toast.success(res.created ? `Added ${res.record.Name || "@"}` : "Record already exists (no duplicate)", {
            description: `${res.record.Value} · TTL ${res.record.Ttl ?? ttl}s · tag ${res.record.Comment}`,
          });
          setName("");
          setValue("");
          setComment("");
        },
        onError: (e2) => toast.error("Couldn't add record", { description: (e2 as Error).message }),
      },
    );
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Plus className="size-4 text-muted-foreground" /> Add A record
        </CardTitle>
        <p className="text-xs text-muted-foreground">
          Adding is always allowed (the lock only guards deletion). Records are tagged{" "}
          <span className="font-mono">brisk:</span> so Brisk only ever manages its own.
        </p>
      </CardHeader>
      <CardContent>
        <form onSubmit={submit} className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_1fr_auto_1fr_auto] sm:items-end">
          <Field label="Name (subdomain)">
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="cdn  ( @ = apex )" className="font-mono text-xs" />
          </Field>
          <Field label="IPv4" error={err ?? undefined}>
            <Input value={value} onChange={(e) => setValue(e.target.value)} placeholder="203.0.113.10" className="font-mono text-xs" />
          </Field>
          <Field label="TTL">
            <Input type="number" min={1} value={ttl} onChange={(e) => setTtl(Math.max(1, Number(e.target.value) || 60))} className="w-20" />
          </Field>
          <Field label="Comment (tag)">
            <Input value={comment} onChange={(e) => setComment(e.target.value)} placeholder="brisk:test" className="font-mono text-xs" />
          </Field>
          <Button type="submit" disabled={add.isPending || !value}>
            {add.isPending ? <Loader2 className="animate-spin" /> : <Plus />}
            Add
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}

function Field({ label, error, children }: { label: string; error?: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1">
      <Label>{label}</Label>
      {children}
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  );
}

function RecordsTable({ records, loading, unlocked }: { records: DnsRecord[]; loading: boolean; unlocked: boolean }) {
  if (loading) {
    return (
      <div className="space-y-2 p-4">
        {[0, 1].map((i) => (
          <Skeleton key={i} className="h-10 w-full" />
        ))}
      </div>
    );
  }
  if (records.length === 0) {
    return <p className="px-4 py-10 text-center text-sm text-muted-foreground">No records in this zone yet.</p>;
  }
  return (
    <ul className="divide-y divide-border">
      <li className="hidden grid-cols-[auto_1.2fr_1fr_auto_1.2fr_auto] gap-3 px-4 py-2 text-xs font-medium uppercase tracking-wider text-muted-foreground md:grid">
        <span>Type</span>
        <span>Name</span>
        <span>Value</span>
        <span>TTL</span>
        <span>Comment</span>
        <span className="text-right">Action</span>
      </li>
      {records.map((r) => (
        <RecordRow key={r.Id} record={r} unlocked={unlocked} />
      ))}
    </ul>
  );
}

function RecordRow({ record, unlocked }: { record: DnsRecord; unlocked: boolean }) {
  const del = useDeleteDnsRecord();
  const [confirm, setConfirm] = React.useState(false);
  const brisk = (record.Comment ?? "").startsWith("brisk:");
  const typeLabel = record.Type === 0 ? "A" : record.Type === 1 ? "AAAA" : record.Type === 2 ? "CNAME" : `#${record.Type}`;

  const doDelete = () => {
    del.mutate(record.Id, {
      onSuccess: () => {
        toast.success(`Deleted ${record.Name || "@"}`);
        setConfirm(false);
      },
      onError: (e) => {
        if (e instanceof ApiError && e.status === 423) {
          toast.error("DNS is locked", { description: "Request an unlock and wait the cooldown first." });
        } else if (e instanceof ApiError && e.status === 403) {
          toast.error("Protected record", { description: "Brisk won't delete records it didn't create." });
        } else {
          toast.error("Delete failed", { description: (e as Error).message });
        }
        setConfirm(false);
      },
    });
  };

  return (
    <li className="grid grid-cols-1 items-center gap-2 px-4 py-3 text-sm md:grid-cols-[auto_1.2fr_1fr_auto_1.2fr_auto] md:gap-3">
      <Badge variant="outline">{typeLabel}</Badge>
      <span className="truncate font-mono text-xs">{record.Name || "@"}</span>
      <span className="truncate font-mono text-xs text-muted-foreground">{record.Value}</span>
      <span className="text-xs text-muted-foreground tabular">{record.Ttl ?? "—"}s</span>
      <span className="flex items-center gap-1.5 truncate text-xs">
        {brisk ? (
          <Badge variant="default" className="gap-1">
            <CheckCircle2 className="size-3" /> {record.Comment}
          </Badge>
        ) : (
          <span className="flex items-center gap-1 text-muted-foreground">
            <ShieldAlert className="size-3" /> {record.Comment || "manual"}
          </span>
        )}
      </span>
      <div className="flex justify-end">
        <Button
          variant="ghost"
          size="icon"
          aria-label="Delete record"
          className="text-muted-foreground hover:text-destructive"
          disabled={!brisk || !unlocked}
          title={!brisk ? "Not a Brisk record" : !unlocked ? "Locked — request unlock first" : "Delete"}
          onClick={() => setConfirm(true)}
        >
          <Trash2 />
        </Button>
      </div>

      <Dialog open={confirm} onOpenChange={setConfirm}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2 text-destructive">
              <ShieldAlert className="size-4" /> Delete {record.Name || "@"}?
            </DialogTitle>
            <DialogDescription>
              Removes the A record <span className="font-mono">{record.Value}</span> from DNS. This changes routing —
              clients stop resolving to this IP.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirm(false)}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={doDelete} disabled={del.isPending}>
              {del.isPending && <Loader2 className="animate-spin" />}
              Delete record
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </li>
  );
}
