import * as React from "react";
import { Cpu } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { NginxLogo, BriskLogo, UbuntuLogo, LinuxLogo, GoLogo } from "@/components/servers/tech-logos";
import type { Server } from "@/lib/types";

/** Tech / runtime stack running on this PoP — what the agent reports each heartbeat
 *  (nginx + brisk-agent versions, OS, kernel, Go). Each row carries its real brand logo.
 *  Values are "—" until an agent that reports them has checked in. */
export function TechStackCard({ server }: { server: Server }) {
  const rows: { logo: React.ReactNode; name: string; value?: string }[] = [
    { logo: <NginxLogo className="size-5" title="nginx" />, name: "nginx", value: server.nginx_version },
    { logo: <BriskLogo className="size-5" title="brisk-agent" />, name: "brisk-agent", value: server.agent_version },
    { logo: <UbuntuLogo className="size-5" title="Ubuntu" />, name: "OS", value: server.os_pretty },
    { logo: <LinuxLogo className="size-5" title="Linux kernel" />, name: "Kernel", value: server.kernel },
    { logo: <GoLogo className="size-5" title="Go" />, name: "Go", value: server.go_version },
  ];

  return (
    <Card>
      <CardHeader className="flex-row items-center gap-2">
        <Cpu className="size-4 text-muted-foreground" />
        <CardTitle>Tech &amp; runtime</CardTitle>
      </CardHeader>
      <CardContent className="space-y-2.5 text-sm">
        {rows.map((r) => (
          <div key={r.name} className="flex items-center gap-3">
            <span className="grid size-7 shrink-0 place-items-center rounded-md border border-border bg-muted/40">
              {r.logo}
            </span>
            <span className="text-muted-foreground">{r.name}</span>
            <span className="tabular ml-auto truncate text-right font-medium" title={r.value || undefined}>
              {r.value && r.value.trim() !== "" ? r.value : <span className="text-muted-foreground">—</span>}
            </span>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}
