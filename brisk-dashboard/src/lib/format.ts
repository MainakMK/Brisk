/** Human-friendly formatters for KPIs. tabular-nums in the UI keeps digits steady. */

/** bits-per-second -> "6.42 Gbps" / "812 Mbps" / "4.1 kbps". */
export function formatBps(bps: number): { value: string; unit: string } {
  if (!isFinite(bps) || bps <= 0) return { value: "0", unit: "bps" };
  const units = ["bps", "kbps", "Mbps", "Gbps", "Tbps"];
  let i = 0;
  let v = bps;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  const value = v >= 100 ? v.toFixed(0) : v >= 10 ? v.toFixed(1) : v.toFixed(2);
  return { value, unit: units[i] };
}

/** One-line bandwidth string, e.g. "6.42 Gbps". */
export function bps(bps: number): string {
  const { value, unit } = formatBps(bps);
  return value + " " + unit;
}

/** Requests/sec -> "18,204". */
export function formatReqPerSec(rps: number): string {
  if (!isFinite(rps) || rps < 0) return "0";
  return rps.toLocaleString(undefined, { maximumFractionDigits: 0 });
}

/** 0..1 ratio -> "96.8" (percent number, no sign). */
export function formatRatioPct(ratio: number): string {
  if (!isFinite(ratio) || ratio < 0) return "0.0";
  return (ratio * 100).toFixed(1);
}

/** 0..100 percent (nullable) -> "38%" or "—". */
export function formatPct(p: number | null | undefined): string {
  if (p == null || !isFinite(p)) return "—";
  return Math.round(p) + "%";
}

/** Plain integer with grouping. */
export function formatInt(n: number): string {
  return Math.round(n).toLocaleString();
}

/** Bytes -> human size, base-1024 like `free -h`/`df -h`: "7.8 GB", "512 MB", "1.2 TB". */
export function bytesSize(bytes: number | null | undefined): string {
  if (bytes == null || !isFinite(bytes) || bytes <= 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let i = 0;
  let v = bytes;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  const value = v >= 100 || i <= 1 ? v.toFixed(0) : v >= 10 ? v.toFixed(1) : v.toFixed(2);
  return value + " " + units[i];
}

/** "X free of Y" from a hardware total (bytes) + live used% — e.g. "1.5 GB free of 2.0 GB".
   Returns undefined until the total is known, so the caller can show just the % meanwhile. */
export function resourceFree(
  total: number | null | undefined,
  usedPct: number | null | undefined,
): string | undefined {
  if (total == null || !isFinite(total) || total <= 0) return undefined;
  const pct = usedPct == null || !isFinite(usedPct) ? 0 : Math.max(0, Math.min(100, usedPct));
  return `${bytesSize(total * (1 - pct / 100))} free of ${bytesSize(total)}`;
}

/** Mbps capacity -> "1 Gbps" / "10 Gbps" / "500 Mbps". */
export function formatCapacity(mbps: number): string {
  if (!isFinite(mbps) || mbps <= 0) return "—";
  if (mbps >= 1000) return mbps / 1000 + " Gbps";
  return mbps + " Mbps";
}

/** Relative time, e.g. "3s ago", "14m ago", "2h ago", or "never". */
export function timeAgo(iso: string | null | undefined, now: number = Date.now()): string {
  if (!iso) return "never";
  const t = new Date(iso).getTime();
  if (!isFinite(t)) return "never";
  const sec = Math.max(0, Math.round((now - t) / 1000));
  if (sec < 5) return "just now";
  if (sec < 60) return sec + "s ago";
  const min = Math.round(sec / 60);
  if (min < 60) return min + "m ago";
  const hr = Math.round(min / 60);
  if (hr < 24) return hr + "h ago";
  const d = Math.round(hr / 24);
  return d + "d ago";
}
