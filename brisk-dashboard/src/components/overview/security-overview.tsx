import { ShieldAlert } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { useAuth } from "@/app/auth";
import { useSecurityEventSummary } from "@/hooks/use-waf";
import type { SecurityEventCount } from "@/lib/types";

/** Admin cross-tenant WAF overview (Phase 4 Step 4): top attacked zones + top
    blocked IPs over the last 24h. Admin-only (the endpoint requires admin); a
    customer never sees it (returns null + the query stays disabled). */
export function SecurityOverview() {
  const { user } = useAuth();
  const isAdmin = user?.role === "admin";
  const sum = useSecurityEventSummary(isAdmin);
  if (!isAdmin) return null;
  const d = sum.data;

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between">
        <div className="flex items-center gap-2">
          <ShieldAlert className="size-4 text-primary" />
          <CardTitle>Security overview (24h)</CardTitle>
        </div>
        {d && (
          <div className="flex items-center gap-2">
            <Badge variant="danger">{d.total_block} blocked</Badge>
            <Badge variant="warning">{d.total_log} logged</Badge>
          </div>
        )}
      </CardHeader>
      <CardContent className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <RankList title="Top attacked zones" rows={d?.top_zones ?? []} />
        <RankList title="Top blocked IPs" rows={d?.top_ips ?? []} />
      </CardContent>
    </Card>
  );
}

function RankList({ title, rows }: { title: string; rows: SecurityEventCount[] }) {
  return (
    <div>
      <h4 className="mb-1.5 text-xs font-medium text-muted-foreground">{title}</h4>
      {rows.length === 0 ? (
        <p className="text-xs text-muted-foreground">No enforced blocks in the window.</p>
      ) : (
        <ul className="space-y-1">
          {rows.map((r, i) => (
            <li key={i} className="flex items-center justify-between gap-2 text-sm">
              <span className="truncate">{r.label}</span>
              <span className="tabular text-muted-foreground">{r.count}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
