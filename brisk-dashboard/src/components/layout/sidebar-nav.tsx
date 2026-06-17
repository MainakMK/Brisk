import { NavLink } from "react-router-dom";
import { cn } from "@/lib/utils";
import { Brand } from "@/components/layout/brand";
import { primaryNav, secondaryNav, type NavItem } from "@/components/layout/nav";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";

function NavRow({ item, onNavigate }: { item: NavItem; onNavigate?: () => void }) {
  const Icon = item.icon;
  return (
    <NavLink
      to={item.url}
      end={item.url === "/"}
      onClick={onNavigate}
      className={({ isActive }) =>
        cn(
          "relative flex items-center gap-3 rounded-lg px-3 py-2 text-sm transition-colors",
          isActive
            ? "bg-sidebar-accent font-medium text-sidebar-accent-foreground shadow-[inset_3px_0_0_0_var(--sidebar-primary)]"
            : "text-sidebar-foreground/75 hover:bg-sidebar-accent/50 hover:text-sidebar-foreground",
        )
      }
    >
      <Icon className="size-4" />
      <span>{item.title}</span>
    </NavLink>
  );
}

/** Sidebar body — shared by the desktop rail and the mobile sheet. */
export function SidebarNav({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <div className="flex h-full flex-col">
      <div className="flex h-14 items-center border-b border-sidebar-border px-4">
        <Brand />
      </div>
      <nav className="flex-1 space-y-0.5 overflow-y-auto p-2">
        {primaryNav.map((item) => (
          <NavRow key={item.url} item={item} onNavigate={onNavigate} />
        ))}
        <div className="px-3 pb-1 pt-4 text-[10px] uppercase tracking-wider text-muted-foreground/60">
          Platform
        </div>
        {secondaryNav.map((item) => (
          <NavRow key={item.url} item={item} onNavigate={onNavigate} />
        ))}
      </nav>
      <div className="border-t border-sidebar-border p-3">
        <div className="flex items-center gap-2 rounded-lg px-2 py-1.5">
          <Avatar>
            <AvatarFallback>AD</AvatarFallback>
          </Avatar>
          <div className="text-xs leading-tight">
            <div className="text-sidebar-foreground">admin</div>
            <div className="text-muted-foreground">brisk.local</div>
          </div>
        </div>
      </div>
    </div>
  );
}
