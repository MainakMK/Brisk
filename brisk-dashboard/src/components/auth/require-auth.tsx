import { Navigate, Outlet } from "react-router-dom";
import { useAuth } from "@/app/auth";

/** Route guard: shows the app only when authenticated; otherwise redirects to the
    login screen. A 401 from any API call flips useAuth().user to null (via the
    "brisk:unauthorized" event), so an expired session lands here automatically. */
export function RequireAuth() {
  const { user, loading } = useAuth();
  if (loading) {
    return (
      <div className="flex h-screen items-center justify-center text-sm text-muted-foreground">
        Loading…
      </div>
    );
  }
  if (!user) return <Navigate to="/login" replace />;
  return <Outlet />;
}
