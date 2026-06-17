import type { SeriesPoint } from "@/lib/types";

/** Time-range presets driving from/to + refresh cadence + resolution. */
export type RangeKey = "1h" | "6h" | "24h" | "7d" | "30d";

export const RANGES: { key: RangeKey; label: string; seconds: number }[] = [
  { key: "1h", label: "Last 1h", seconds: 3600 },
  { key: "6h", label: "Last 6h", seconds: 6 * 3600 },
  { key: "24h", label: "Last 24h", seconds: 24 * 3600 },
  { key: "7d", label: "Last 7d", seconds: 7 * 86400 },
  { key: "30d", label: "Last 30d", seconds: 30 * 86400 },
];

export function rangeSeconds(key: RangeKey): number {
  return RANGES.find((r) => r.key === key)?.seconds ?? 86400;
}

/** Window for a range, anchored at `now` (ms). */
export function rangeWindow(key: RangeKey, now: number): { from: string; to: string } {
  const toMs = now;
  const fromMs = toMs - rangeSeconds(key) * 1000;
  return { from: new Date(fromMs).toISOString(), to: new Date(toMs).toISOString() };
}

/** Sane refresh cadence: short ranges refresh often, long ranges rarely. */
export function refreshMs(key: RangeKey): number {
  switch (key) {
    case "1h":
      return 30_000;
    case "6h":
      return 60_000;
    case "24h":
      return 120_000;
    default:
      return 300_000; // 7d / 30d
  }
}

/** Always 1m (continuous aggregate) for ranges; raw is only for very short
   recent windows, which we don't expose here. */
export function resolutionFor(_key: RangeKey): "1m" | "raw" {
  return "1m";
}

/** Merge multiple servers' series into a network-wide series, keyed by bucket
   time. Counts + bytes summed; cpu/ram/disk averaged over reporting servers;
   hit_ratio recomputed from summed hits/misses (NOT averaged). */
export function mergeSeries(all: SeriesPoint[][]): SeriesPoint[] {
  const byTime = new Map<string, { p: SeriesPoint; cpu: number; ram: number; disk: number; sysN: number }>();
  for (const series of all) {
    for (const pt of series) {
      const key = pt.time;
      let agg = byTime.get(key);
      if (!agg) {
        agg = {
          p: {
            time: pt.time,
            requests: 0,
            hits: 0,
            misses: 0,
            bytes_sent: 0,
            bandwidth_bps: 0,
            cpu_pct: null,
            ram_pct: null,
            disk_pct: null,
            hit_ratio: 0,
          },
          cpu: 0,
          ram: 0,
          disk: 0,
          sysN: 0,
        };
        byTime.set(key, agg);
      }
      agg.p.requests += pt.requests;
      agg.p.hits += pt.hits;
      agg.p.misses += pt.misses;
      agg.p.bytes_sent += pt.bytes_sent;
      agg.p.bandwidth_bps = (agg.p.bandwidth_bps ?? 0) + (pt.bandwidth_bps ?? 0);
      if (pt.cpu_pct != null) {
        agg.cpu += pt.cpu_pct;
        agg.ram += pt.ram_pct ?? 0;
        agg.disk += pt.disk_pct ?? 0;
        agg.sysN += 1;
      }
    }
  }
  const out: SeriesPoint[] = [];
  for (const { p, cpu, ram, disk, sysN } of byTime.values()) {
    const total = p.hits + p.misses;
    p.hit_ratio = total > 0 ? p.hits / total : 0;
    if (sysN > 0) {
      p.cpu_pct = cpu / sysN;
      p.ram_pct = ram / sysN;
      p.disk_pct = disk / sysN;
    }
    out.push(p);
  }
  out.sort((a, b) => a.time.localeCompare(b.time));
  return out;
}

/** Downsample to ~targetPoints by grouping consecutive buckets, so 7d/30d
   charts stay readable (and the DOM light) without lying about the shape. */
export function downsample(series: SeriesPoint[], targetPoints = 360): SeriesPoint[] {
  if (series.length <= targetPoints) return series;
  const groupSize = Math.ceil(series.length / targetPoints);
  const out: SeriesPoint[] = [];
  for (let i = 0; i < series.length; i += groupSize) {
    const group = series.slice(i, i + groupSize);
    const acc: SeriesPoint = {
      time: group[0].time,
      requests: 0,
      hits: 0,
      misses: 0,
      bytes_sent: 0,
      bandwidth_bps: 0,
      cpu_pct: null,
      ram_pct: null,
      disk_pct: null,
      hit_ratio: 0,
    };
    let cpu = 0,
      ram = 0,
      disk = 0,
      sysN = 0,
      bwN = 0,
      bw = 0;
    for (const p of group) {
      acc.requests += p.requests;
      acc.hits += p.hits;
      acc.misses += p.misses;
      acc.bytes_sent += p.bytes_sent;
      if (p.bandwidth_bps != null) {
        bw += p.bandwidth_bps;
        bwN += 1;
      }
      if (p.cpu_pct != null) {
        cpu += p.cpu_pct;
        ram += p.ram_pct ?? 0;
        disk += p.disk_pct ?? 0;
        sysN += 1;
      }
    }
    acc.bandwidth_bps = bwN > 0 ? bw / bwN : 0; // avg rate over the group
    const total = acc.hits + acc.misses;
    acc.hit_ratio = total > 0 ? acc.hits / total : 0;
    if (sysN > 0) {
      acc.cpu_pct = cpu / sysN;
      acc.ram_pct = ram / sysN;
      acc.disk_pct = disk / sysN;
    }
    out.push(acc);
  }
  return out;
}

export interface StatsSummary {
  totalRequests: number;
  totalBytes: number;
  hitRatio: number; // 0..1
  avgReqPerSec: number;
  missRatio: number;
}

/** Roll a series up into the KPI summary for the selected range. */
export function summarize(series: SeriesPoint[], windowSeconds: number): StatsSummary {
  let requests = 0,
    hits = 0,
    misses = 0,
    bytes = 0;
  for (const p of series) {
    requests += p.requests;
    hits += p.hits;
    misses += p.misses;
    bytes += p.bytes_sent;
  }
  const total = hits + misses;
  const hitRatio = total > 0 ? hits / total : 0;
  return {
    totalRequests: requests,
    totalBytes: bytes,
    hitRatio,
    avgReqPerSec: windowSeconds > 0 ? requests / windowSeconds : 0,
    missRatio: 1 - hitRatio,
  };
}
