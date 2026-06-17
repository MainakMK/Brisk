import * as React from "react";
import { api } from "@/lib/api";

/** The identity the control plane returns from /auth/login and /auth/me. */
export interface Identity {
  account_id: number;
  role: string;
  name: string;
  email: string;
}

interface AuthState {
  user: Identity | null;
  loading: boolean;
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = React.createContext<AuthState | undefined>(undefined);

/** AuthProvider holds the current identity. It probes /auth/me on mount (the
    HttpOnly session cookie is sent automatically), and listens for the global
    "brisk:unauthorized" event (any 401 from the API client) to drop to login. */
export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = React.useState<Identity | null>(null);
  const [loading, setLoading] = React.useState(true);

  React.useEffect(() => {
    let active = true;
    (async () => {
      try {
        const me = await api.get<Identity>("/auth/me");
        if (active) setUser(me);
      } catch {
        if (active) setUser(null);
      } finally {
        if (active) setLoading(false);
      }
    })();
    const onUnauthorized = () => setUser(null);
    window.addEventListener("brisk:unauthorized", onUnauthorized);
    return () => {
      active = false;
      window.removeEventListener("brisk:unauthorized", onUnauthorized);
    };
  }, []);

  const login = React.useCallback(async (email: string, password: string) => {
    const me = await api.post<Identity>("/auth/login", { email, password });
    setUser(me);
  }, []);

  const logout = React.useCallback(async () => {
    try {
      await api.post("/auth/logout");
    } finally {
      setUser(null);
    }
  }, []);

  return <AuthContext.Provider value={{ user, loading, login, logout }}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = React.useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
