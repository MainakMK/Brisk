import { BrowserRouter, Routes, Route } from "react-router-dom";
import { Providers } from "@/app/providers";
import { AuthProvider } from "@/app/auth";
import { RequireAuth } from "@/components/auth/require-auth";
import { AppShell } from "@/components/layout/app-shell";
import LoginPage from "@/pages/login";
import OverviewPage from "@/pages/overview";
import ServersPage from "@/pages/servers";
import ServerDetailPage from "@/pages/server-detail";
import ZonesPage from "@/pages/zones";
import ZoneDetailPage from "@/pages/zone-detail";
import AnalyticsPage from "@/pages/analytics";
import LogsPage from "@/pages/logs";
import SecurityPage from "@/pages/security";
import PurgePage from "@/pages/purge";
import DnsPage from "@/pages/dns";
import SettingsPage from "@/pages/settings";
import NotFoundPage from "@/pages/not-found";

export default function App() {
  return (
    <Providers>
      <BrowserRouter>
        <AuthProvider>
          <Routes>
            {/* Public: login screen. */}
            <Route path="/login" element={<LoginPage />} />
            {/* Everything else requires an authenticated admin session. */}
            <Route element={<RequireAuth />}>
              <Route element={<AppShell />}>
                <Route path="/" element={<OverviewPage />} />
                <Route path="/servers" element={<ServersPage />} />
                <Route path="/servers/:id" element={<ServerDetailPage />} />
                <Route path="/zones" element={<ZonesPage />} />
                <Route path="/zones/:id" element={<ZoneDetailPage />} />
                <Route path="/analytics" element={<AnalyticsPage />} />
                <Route path="/logs" element={<LogsPage />} />
                <Route path="/security" element={<SecurityPage />} />
                <Route path="/purge" element={<PurgePage />} />
                <Route path="/dns" element={<DnsPage />} />
                <Route path="/settings" element={<SettingsPage />} />
                <Route path="*" element={<NotFoundPage />} />
              </Route>
            </Route>
          </Routes>
        </AuthProvider>
      </BrowserRouter>
    </Providers>
  );
}
