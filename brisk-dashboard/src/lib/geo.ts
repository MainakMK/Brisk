import { api } from "@/lib/api";
import type { DnsRegion, Server } from "@/lib/types";

// Resolve a Brisk region code (e.g. "IN-BLR") to a display location using the CONTROL
// PLANE's RegionMap — shipped in GET /dns/routing as `regions[]`. This is the SINGLE
// source of truth: the exact same map drives Bunny Smart-Record geo-routing
// (dns/regions.go), so the dashboard's flag + label can never drift from where the
// edge actually routes users. Add a PoP region in ONE place (the backend) and both the
// routing and the UI update together.
//
// `latency_zone` doubles as the ISO-2 country code for the flag (true for every region
// in the map: IN, US, DE, GB, FR, NL, SG, JP, AU, AE, BR, ZA). `label` is the human
// "City, Country". An unknown code falls back to deriving the country from the ISO-2
// prefix via Intl.DisplayNames; null when nothing can be derived.

export type ServerLocation = {
  cc: string; // ISO-2 country code for the flag
  label: string; // "City, Country"
  lat?: number; // present only when resolved from a DnsRegion hit (has real coordinates)
  long?: number;
};

let regionNames: Intl.DisplayNames | null = null;
try {
  regionNames = new Intl.DisplayNames(["en"], { type: "region" });
} catch {
  regionNames = null; // older runtime — fall back to the raw code
}

export function resolveLocation(
  region?: string | null,
  regions?: DnsRegion[],
): ServerLocation | null {
  if (!region) return null;
  const key = region.trim().toUpperCase();

  if (regions && regions.length) {
    const byCode = new Map(regions.map((r) => [r.region.toUpperCase(), r]));
    // Most-specific first, then broaden: "US-IL-x" -> "US-IL" -> "US" (mirrors the
    // backend LookupRegion fallback exactly).
    let hit = byCode.get(key);
    if (!hit) {
      const parts = key.split("-");
      for (let i = parts.length - 1; i >= 1 && !hit; i--) {
        hit = byCode.get(parts.slice(0, i).join("-"));
      }
    }
    if (hit) {
      const cc = (hit.latency_zone || "").toUpperCase();
      // City from the backend label ("Frankfurt, EU" -> "Frankfurt"); country NAME
      // standardized from the country code via Intl ("DE" -> "Germany"), so the UI
      // reads "Frankfurt, Germany" while staying 100% backend-sourced.
      const parts = (hit.label || "").split(",");
      const city = parts[0]?.trim() ?? "";
      const country =
        (regionNames && /^[A-Z]{2}$/.test(cc) ? regionNames.of(cc) : "") ||
        parts[1]?.trim() ||
        "";
      const label = city && country ? `${city}, ${country}` : country || city || key;
      return { cc, label, lat: hit.lat, long: hit.long };
    }
  }

  // Fallback: derive the country from the ISO-2 prefix when the map has no entry yet.
  const prefix = key.split("-")[0];
  if (/^[A-Z]{2}$/.test(prefix) && regionNames) {
    const country = regionNames.of(prefix);
    if (country && country !== prefix) return { cc: prefix, label: country };
  }
  return null;
}

// --- IP -> region auto-detect (Add Server convenience) -------------------------
// Runs ONLY in the admin dashboard when adding a server (a rare manual action), never
// in the edge serving path — so it doesn't touch the "own code, no third party in the
// runtime" rule. It's a SUGGESTION: it pre-selects the nearest region, the operator
// still confirms/overrides, and if the lookup is blocked/offline the region dropdown
// still works by hand.

export type GeoGuess = { lat: number; long: number; label: string };

// geoLocateIP resolves a server IP to coordinates + a "City, Country" label via the
// CONTROL PLANE's /geoip endpoint (which proxies a keyless GeoIP lookup SERVER-SIDE).
// Going through our own backend avoids the browser CORS failures that stopped the
// direct third-party lookup from auto-picking. Returns null on any failure (private IP,
// offline) so the caller falls back to manual region selection.
export async function geoLocateIP(ip: string): Promise<GeoGuess | null> {
  try {
    const r = await api.get<{ lat: number; long: number; label: string }>(
      `/geoip?ip=${encodeURIComponent(ip)}`,
    );
    if (!r || typeof r.lat !== "number" || typeof r.long !== "number") return null;
    return { lat: r.lat, long: r.long, label: r.label || "" };
  } catch {
    return null;
  }
}

function haversineKm(aLat: number, aLong: number, bLat: number, bLong: number): number {
  const toRad = (d: number) => (d * Math.PI) / 180;
  const dLat = toRad(bLat - aLat);
  const dLong = toRad(bLong - aLong);
  const s =
    Math.sin(dLat / 2) ** 2 +
    Math.cos(toRad(aLat)) * Math.cos(toRad(bLat)) * Math.sin(dLong / 2) ** 2;
  return 6371 * 2 * Math.asin(Math.min(1, Math.sqrt(s)));
}

// nearestRegion picks the Brisk region code geographically closest to a coordinate. On
// near-ties it prefers the MORE specific code (e.g. "EU-FRA" over "EU", which share
// coordinates) so a Frankfurt server maps to the city, not the continent center.
export function nearestRegion(
  lat: number,
  long: number,
  regions?: DnsRegion[],
): string | null {
  if (!regions?.length) return null;
  let best: string | null = null;
  let bestD = Infinity;
  let bestSpec = -1;
  for (const r of regions) {
    const d = haversineKm(lat, long, r.lat, r.long);
    const spec = (r.region.match(/-/g) || []).length; // city-level codes have a dash
    if (d < bestD - 1 || (Math.abs(d - bestD) <= 1 && spec > bestSpec)) {
      best = r.region;
      bestD = d;
      bestSpec = spec;
    }
  }
  return best;
}

// --- PoP World Map points ------------------------------------------------------

export type PopStatus =
  | "assigned" // serving this zone, online & healthy
  | "available" // online, not serving this zone
  | "unhealthy" // assigned/online but health-failed or draining
  | "offline" // no fresh heartbeat
  | "pending-add" // staged to add (per-zone map only)
  | "pending-remove"; // staged to remove (per-zone map only)

export interface PopPoint {
  edgeId: string;
  lat: number;
  long: number;
  cc?: string;
  label?: string;
  status: PopStatus;
}

/** Base status from a server's online/health/drain state, independent of any zone. */
export function popBaseStatus(opts: {
  online: boolean;
  inRotation?: boolean; // from health edge.in_rotation
  drained?: boolean;
}): "ok" | "offline" | "unhealthy" {
  if (!opts.online) return "offline";
  if (opts.drained || opts.inRotation === false) return "unhealthy";
  return "ok";
}

/**
 * Build map points for a set of servers. `assignedEdgeIds` (present => per-zone map)
 * marks which edges serve the current zone; `pendingAdd`/`pendingRemove` override status
 * for staged edges. Without `assignedEdgeIds` (global map) every serving-capable edge
 * gets the "assigned" tone. Servers whose region has no coordinates are skipped (still
 * shown in the assignment list elsewhere).
 */
export function pointsFromServers(args: {
  servers: Server[];
  regions: DnsRegion[] | undefined;
  edgeState: Record<string, { online: boolean; inRotation?: boolean; drained?: boolean }>;
  assignedEdgeIds?: Set<string>;
  pendingAdd?: Set<string>;
  pendingRemove?: Set<string>;
}): PopPoint[] {
  const out: PopPoint[] = [];
  for (const s of args.servers) {
    const loc = resolveLocation(s.region, args.regions);
    if (!loc || loc.lat == null || loc.long == null) continue; // unmapped / no coords -> skip on map
    const st = args.edgeState[s.edge_id] ?? { online: false };
    let status: PopStatus;
    if (args.pendingAdd?.has(s.edge_id)) status = "pending-add";
    else if (args.pendingRemove?.has(s.edge_id)) status = "pending-remove";
    else if (args.assignedEdgeIds) {
      const base = popBaseStatus(st);
      status = args.assignedEdgeIds.has(s.edge_id) ? (base === "ok" ? "assigned" : base) : "available";
    } else {
      const base = popBaseStatus(st);
      status = base === "ok" ? "assigned" : base;
    }
    out.push({ edgeId: s.edge_id, lat: loc.lat, long: loc.long, cc: loc.cc, label: loc.label, status });
  }
  return out;
}
