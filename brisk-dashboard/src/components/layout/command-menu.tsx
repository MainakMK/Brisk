import * as React from "react";
import { useNavigate } from "react-router-dom";
import { Command } from "cmdk";
import { Search, Plus, Server as ServerIcon, Globe, Trash2 } from "lucide-react";
import { primaryNav, secondaryNav } from "@/components/layout/nav";
import { useZones } from "@/hooks/use-zones";
import { useServers } from "@/hooks/use-servers";
import { cn } from "@/lib/utils";

/** ⌘K command palette: jump to any screen, run quick actions, or jump to a
   zone/server by name. Keyboard-first (cmdk handles arrow/enter); ⌘K toggles. */
export function CommandMenu() {
  const [open, setOpen] = React.useState(false);
  const navigate = useNavigate();
  const zones = useZones();
  const servers = useServers();
  const screens = [...primaryNav, ...secondaryNav];

  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((o) => !o);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  const go = (url: string) => {
    navigate(url);
    setOpen(false);
  };

  return (
    <>
      <button
        onClick={() => setOpen(true)}
        className="flex w-full items-center gap-2 rounded-lg border border-border bg-muted/40 px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-muted"
        aria-label="Open command palette"
      >
        <Search className="size-4" />
        <span className="hidden sm:inline">Search zones, servers…</span>
        <kbd className="ml-auto hidden rounded bg-background px-1.5 py-0.5 text-[10px] tabular sm:inline">⌘K</kbd>
      </button>

      {open && (
        <div
          className="fixed inset-0 z-50 flex items-start justify-center bg-black/60 p-4 pt-[12vh]"
          onClick={() => setOpen(false)}
        >
          <div
            className="w-full max-w-lg overflow-hidden rounded-xl border border-border bg-popover shadow-2xl"
            onClick={(e) => e.stopPropagation()}
          >
            <Command
              className="[&_[cmdk-input]]:outline-none [&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:text-xs [&_[cmdk-group-heading]]:text-muted-foreground"
              loop
            >
              <div className="flex items-center gap-2 border-b border-border px-3">
                <Search className="size-4 text-muted-foreground" />
                <Command.Input
                  autoFocus
                  placeholder="Jump to a screen, zone, server, or action…"
                  className="h-11 w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground"
                />
              </div>
              <Command.List className="max-h-96 overflow-y-auto p-2">
                <Command.Empty className="px-3 py-6 text-center text-sm text-muted-foreground">
                  No results.
                </Command.Empty>

                <Command.Group heading="Actions">
                  <Item label="Add server" icon={<Plus className="size-4 text-muted-foreground" />} keywords="new create" onSelect={() => go("/servers?add=1")} />
                  <Item label="Add zone" icon={<Plus className="size-4 text-muted-foreground" />} keywords="new create" onSelect={() => go("/zones?add=1")} />
                  <Item label="Purge cache" icon={<Trash2 className="size-4 text-muted-foreground" />} keywords="invalidate flush" onSelect={() => go("/purge")} />
                </Command.Group>

                <Command.Group heading="Screens">
                  {screens.map((s) => {
                    const Icon = s.icon;
                    return (
                      <Item key={s.url} label={s.title} icon={<Icon className="size-4 text-muted-foreground" />} onSelect={() => go(s.url)} />
                    );
                  })}
                </Command.Group>

                {(zones.data?.length ?? 0) > 0 && (
                  <Command.Group heading="Zones">
                    {zones.data!.map((z) => (
                      <Item
                        key={z.id}
                        label={z.name}
                        hint={z.cdn_hostname}
                        icon={<Globe className="size-4 text-muted-foreground" />}
                        keywords={z.cdn_hostname}
                        onSelect={() => go(`/zones/${z.id}`)}
                      />
                    ))}
                  </Command.Group>
                )}

                {(servers.data?.length ?? 0) > 0 && (
                  <Command.Group heading="Servers">
                    {servers.data!.map((s) => (
                      <Item
                        key={s.id}
                        label={s.edge_id || s.name}
                        hint={s.region}
                        icon={<ServerIcon className="size-4 text-muted-foreground" />}
                        keywords={`${s.name} ${s.region} ${s.ip}`}
                        onSelect={() => go(`/servers/${s.id}`)}
                      />
                    ))}
                  </Command.Group>
                )}
              </Command.List>
            </Command>
          </div>
        </div>
      )}
    </>
  );
}

function Item({
  label,
  hint,
  icon,
  keywords,
  onSelect,
}: {
  label: string;
  hint?: string;
  icon: React.ReactNode;
  keywords?: string;
  onSelect: () => void;
}) {
  return (
    <Command.Item
      value={`${label} ${keywords ?? ""}`}
      onSelect={onSelect}
      className={cn(
        "flex cursor-pointer items-center gap-2 rounded-md px-2 py-2 text-sm",
        "data-[selected=true]:bg-secondary",
      )}
    >
      {icon}
      <span>{label}</span>
      {hint && <span className="ml-auto truncate font-mono text-xs text-muted-foreground">{hint}</span>}
    </Command.Item>
  );
}
