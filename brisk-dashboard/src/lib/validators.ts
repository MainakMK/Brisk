/** Lightweight client-side validators (no Zod dep). Each returns an error
   string or null. Mirrors the control plane's validate tags. */

const HOSTNAME = /^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}$/i;

export function validateHostname(v: string): string | null {
  if (!v.trim()) return "Required";
  if (!HOSTNAME.test(v.trim())) return "Enter a valid hostname (e.g. cdn.example.com)";
  return null;
}

export function validateURL(v: string): string | null {
  if (!v.trim()) return "Required";
  try {
    const u = new URL(v.trim());
    if (u.protocol !== "http:" && u.protocol !== "https:") return "Must be http(s)://";
    return null;
  } catch {
    return "Enter a valid URL (e.g. http://origin.example.com)";
  }
}

export function validateRequired(v: string): string | null {
  return v.trim() ? null : "Required";
}

/** Optional custom domain — empty is allowed; if present must be a hostname. */
export function validateOptionalHostname(v: string): string | null {
  if (!v.trim()) return null;
  return validateHostname(v);
}

/** A regex the edge would compile — reject patterns JS can't parse. */
export function validateRegex(v: string): string | null {
  if (!v.trim()) return "Required";
  try {
    new RegExp(v);
    return null;
  } catch (e) {
    return "Invalid regex: " + (e as Error).message;
  }
}

/** Accept nginx-style durations: 30, 30s, 5m, 2h, 7d, 1w. */
const TTL = /^\d+(ms|s|m|h|d|w)?$/;
export function validateTTL(v: string): string | null {
  if (!v.trim()) return "Required";
  if (!TTL.test(v.trim())) return "Use a duration like 2s, 5m, 12h, 30d";
  return null;
}

export function validateMatchValue(matchType: string, v: string): string | null {
  if (!v.trim()) return "Required";
  if (matchType === "regex") return validateRegex(v);
  if (matchType === "extension") {
    if (/[./]/.test(v)) return "Extension only, no dots or slashes (e.g. m3u8)";
    return null;
  }
  if (matchType === "path_prefix") {
    if (!v.startsWith("/")) return "Path prefix should start with / (e.g. /assets/)";
    return null;
  }
  return null;
}
