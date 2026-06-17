import * as React from "react";
import { Outlet, useLocation } from "react-router-dom";
import { SidebarNav } from "@/components/layout/sidebar-nav";
import { TopBar } from "@/components/layout/top-bar";
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet";

/** Persistent layout: fixed sidebar (desktop) + sheet drawer (mobile) + top bar. */
export function AppShell() {
  const [mobileOpen, setMobileOpen] = React.useState(false);

  // Scroll to top whenever the route (pathname) changes, so opening a detail page
  // always starts at the header — not wherever the previous page was scrolled.
  // Keyed on pathname only, so in-page tab switches (?tab=...) keep their position.
  const { pathname } = useLocation();
  React.useEffect(() => {
    window.scrollTo(0, 0);
  }, [pathname]);

  return (
    <div className="flex min-h-screen bg-background">
      {/* Desktop sidebar */}
      <aside className="hidden w-60 shrink-0 border-r border-sidebar-border bg-sidebar lg:block">
        <div className="sticky top-0 h-screen">
          <SidebarNav />
        </div>
      </aside>

      {/* Mobile sidebar (sheet) */}
      <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
        <SheetContent side="left" className="w-72 bg-sidebar p-0">
          <SheetTitle className="sr-only">Navigation</SheetTitle>
          <SidebarNav onNavigate={() => setMobileOpen(false)} />
        </SheetContent>
      </Sheet>

      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar onOpenSidebar={() => setMobileOpen(true)} />
        <main className="flex-1 p-4 sm:p-6">
          {/* Fixed-width content column (centered) so pages don't stretch edge-to-edge
              on wide monitors. Sidebar + top bar are unaffected. */}
          <div className="mx-auto w-full max-w-[1400px]">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}
