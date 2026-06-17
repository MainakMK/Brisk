/** Typed fetch client for brisk-control.

   - Base URL from VITE_API_URL (e.g. http://localhost:8080).
   - Auth (Phase 3.7 Step 3) lives in ONE place here:
       * credentials:"include" sends the HttpOnly session cookie (dashboard UI auth);
       * non-GET requests echo the CSRF token (double-submit) from the readable
         brisk_csrf cookie in the X-CSRF-Token header;
       * authHeader() is the optional bearer seam (programmatic admin tokens).
     Call sites never change — auth is centralized.
   - Centralized error handling: every non-2xx throws a typed ApiError. A 401
     dispatches "brisk:unauthorized" so the AuthProvider can drop to the login screen. */

const BASE_URL = (import.meta.env.VITE_API_URL ?? "http://localhost:8080").replace(/\/$/, "");

/** Optional bearer seam: set an admin API token here for programmatic use. The
    dashboard UI authenticates via the session cookie, so this stays empty. */
function authHeader(): Record<string, string> {
  // const token = getAdminToken();
  // if (token) return { Authorization: `Bearer ${token}` };
  return {};
}

/** Read a cookie value by name (used for the readable CSRF token). */
function readCookie(name: string): string | null {
  const m = document.cookie.match(new RegExp("(?:^|; )" + name + "=([^;]*)"));
  return m ? decodeURIComponent(m[1]) : null;
}

export class ApiError extends Error {
  status: number;
  body: unknown;
  constructor(status: number, message: string, body?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.body = body;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const url = `${BASE_URL}/api/v1${path}`;
  const method = (init?.method ?? "GET").toUpperCase();
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...authHeader(),
    ...((init?.headers as Record<string, string>) ?? {}),
  };
  // CSRF double-submit on state-changing requests.
  if (method !== "GET" && method !== "HEAD") {
    const csrf = readCookie("brisk_csrf");
    if (csrf) headers["X-CSRF-Token"] = csrf;
  }

  let res: Response;
  try {
    res = await fetch(url, { ...init, headers, credentials: "include" });
  } catch (e) {
    throw new ApiError(0, `Cannot reach control plane at ${BASE_URL}`, e);
  }

  if (!res.ok) {
    let body: unknown = undefined;
    let msg = `${res.status} ${res.statusText}`;
    try {
      body = await res.json();
      if (body && typeof body === "object" && "error" in body) {
        msg = String((body as { error: unknown }).error);
      }
    } catch {
      /* non-JSON error body */
    }
    // Notify the app so it can drop to login. Ignore for the auth probe itself.
    if (res.status === 401 && !path.startsWith("/auth/")) {
      window.dispatchEvent(new CustomEvent("brisk:unauthorized"));
    }
    throw new ApiError(res.status, msg, body);
  }

  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export const api = {
  baseUrl: BASE_URL,
  get: <T>(path: string) => request<T>(path, { method: "GET" }),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "POST", body: body ? JSON.stringify(body) : undefined }),
  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "PUT", body: body ? JSON.stringify(body) : undefined }),
  del: <T>(path: string) => request<T>(path, { method: "DELETE" }),
};
