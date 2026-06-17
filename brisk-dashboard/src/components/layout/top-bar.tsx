import { Link, useLocation } from "react-router-dom";
import { Menu, Plus, Server, Globe } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ThemeToggle } from "@/components/theme/theme-toggle";
import { CommandMenu } from "@/components/layout/command-menu";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import { primaryNav, secondaryNav } from "@/components/layout/nav";

function usePageTitle(): string {
  const { pathname } = useLocation();
  const all = [...primaryNav, ...secondaryNav];
  const match = all.find((i) => (i.url === "/" ? pathname === "/" : pathname.startsWith(i.url)));
  return match?.title ?? "Not found";
}

export function TopBar({ onOpenSidebar }: { onOpenSidebar: () => void }) {
  const title = usePageTitle();
  return (
    <header className="sticky top-0 z-30 flex h-14 items-center gap-3 border-b border-border bg-background/80 px-4 backdrop-blur">
      <Button variant="ghost" size="icon" className="lg:hidden" onClick={onOpenSidebar} aria-label="Open navigation">
        <Menu />
      </Button>
      <div className="text-sm font-medium text-muted-foreground">{title}</div>

      <div className="mx-auto hidden w-full max-w-md md:block">
        <CommandMenu />
      </div>

      <div className="ml-auto flex items-center gap-1.5">
        <ThemeToggle />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button size="sm" className="gap-1.5">
              <Plus className="size-4" />
              <span className="hidden sm:inline">Add</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuLabel>Create</DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem asChild>
              <Link to="/servers?add=1">
                <Server /> Add server
              </Link>
            </DropdownMenuItem>
            <DropdownMenuItem asChild>
              <Link to="/zones?add=1">
                <Globe /> Add zone
              </Link>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  );
}
