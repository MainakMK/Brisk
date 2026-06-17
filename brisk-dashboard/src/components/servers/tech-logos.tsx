/**
 * Bundled brand logos for the per-PoP "Tech / runtime" card — inline SVG so there's no
 * external dependency or network fetch at runtime (matches Brisk's "own code only" rule).
 * Each is a 24×24 brand-colored mark, recognizable at ~18–22px. `className` controls size.
 */
import * as React from "react";

type LogoProps = { className?: string; title?: string };

const box = (className?: string) => ({
  viewBox: "0 0 24 24",
  className,
  xmlns: "http://www.w3.org/2000/svg" as const,
  "aria-hidden": true,
});

/** nginx — angular green "N". */
export function NginxLogo({ className, title }: LogoProps) {
  return (
    <svg {...box(className)} role={title ? "img" : undefined}>
      {title && <title>{title}</title>}
      <path
        fill="#009639"
        d="M12 1.6 1.8 7.2v9.6L12 22.4l10.2-5.6V7.2L12 1.6Zm5.2 14.9c0 .9-.7 1.3-1.4 1.3-.5 0-.9-.2-1.3-.7l-5.4-6.6v6c0 .7-.6 1.3-1.3 1.3s-1.3-.6-1.3-1.3V8c0-.9.7-1.3 1.4-1.3.5 0 .9.2 1.3.7l5.4 6.6V8c0-.7.6-1.3 1.3-1.3s1.3.6 1.3 1.3v8.5Z"
      />
    </svg>
  );
}

/** brisk-agent — Brisk lightning bolt (Voltage gradient). */
export function BriskLogo({ className, title }: LogoProps) {
  const gid = React.useId();
  return (
    <svg {...box(className)} role={title ? "img" : undefined}>
      {title && <title>{title}</title>}
      <defs>
        <linearGradient id={gid} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" stopColor="var(--chart-2, #38bdf8)" />
          <stop offset="100%" stopColor="var(--primary, #6366f1)" />
        </linearGradient>
      </defs>
      <path fill={`url(#${gid})`} d="M13.5 1.5 4 13.2h6.2L9 22.5 20 9.8h-6.4L13.5 1.5Z" />
    </svg>
  );
}

/** Ubuntu — circle of friends (orange ring + 3 nodes). */
export function UbuntuLogo({ className, title }: LogoProps) {
  const O = "#E95420";
  // three nodes at 90°, 210°, 330° around the ring
  const nodes = [
    { x: 12, y: 3.6 },
    { x: 4.7, y: 16.2 },
    { x: 19.3, y: 16.2 },
  ];
  return (
    <svg {...box(className)} role={title ? "img" : undefined}>
      {title && <title>{title}</title>}
      <circle cx="12" cy="12" r="6.2" fill="none" stroke={O} strokeWidth="2.1" />
      {nodes.map((n, i) => (
        <g key={i}>
          <line x1="12" y1="12" x2={n.x} y2={n.y} stroke={O} strokeWidth="2.1" />
          <circle cx={n.x} cy={n.y} r="2.6" fill={O} stroke="var(--card, #fff)" strokeWidth="1.1" />
        </g>
      ))}
      <circle cx="12" cy="12" r="2" fill={O} />
    </svg>
  );
}

/** Linux — Tux penguin (simplified, recognizable). */
export function LinuxLogo({ className, title }: LogoProps) {
  return (
    <svg {...box(className)} role={title ? "img" : undefined}>
      {title && <title>{title}</title>}
      {/* body */}
      <path
        fill="#111827"
        d="M12 2.2c-2.4 0-3.8 1.8-3.8 4.3 0 1.2.2 2 .2 2.8 0 1.1-1.9 2.6-2.7 4.7-.8 2-.2 3.2.3 3.8.2.2.1.7-.1 1.2-.2.6 0 1.2.8 1.3 1.9.3 3.6.4 5.3.4s3.4-.1 5.3-.4c.8-.1 1-.7.8-1.3-.2-.5-.3-1-.1-1.2.5-.6 1.1-1.8.3-3.8-.8-2.1-2.7-3.6-2.7-4.7 0-.8.2-1.6.2-2.8 0-2.5-1.4-4.3-3.8-4.3Z"
      />
      {/* belly */}
      <ellipse cx="12" cy="15.6" rx="3.4" ry="4.6" fill="#F9FAFB" />
      {/* eyes */}
      <ellipse cx="10.4" cy="7" rx="1.3" ry="1.7" fill="#F9FAFB" />
      <ellipse cx="13.6" cy="7" rx="1.3" ry="1.7" fill="#F9FAFB" />
      <circle cx="10.6" cy="7.3" r="0.7" fill="#111827" />
      <circle cx="13.4" cy="7.3" r="0.7" fill="#111827" />
      {/* beak */}
      <path fill="#F5A623" d="M10.6 8.7h2.8L12 10.6 10.6 8.7Z" />
      {/* feet */}
      <path fill="#F5A623" d="M9 20.4c-.5.6-1.6.8-2.3.4-.5-.3-.2-.9.3-1.3.6-.5 1.4-.8 2-.6.5.2.4 1 0 1.5Zm6 0c.5.6 1.6.8 2.3.4.5-.3.2-.9-.3-1.3-.6-.5-1.4-.8-2-.6-.5.2-.4 1 0 1.5Z" />
    </svg>
  );
}

/** Go — gopher head (Go blue). */
export function GoLogo({ className, title }: LogoProps) {
  const blue = "#00ADD8";
  return (
    <svg {...box(className)} role={title ? "img" : undefined}>
      {title && <title>{title}</title>}
      {/* ears */}
      <ellipse cx="7.4" cy="4.8" rx="2" ry="2.6" fill={blue} transform="rotate(-18 7.4 4.8)" />
      <ellipse cx="16.6" cy="4.8" rx="2" ry="2.6" fill={blue} transform="rotate(18 16.6 4.8)" />
      {/* head */}
      <rect x="3.6" y="5" width="16.8" height="14" rx="7" fill={blue} />
      {/* eye whites */}
      <circle cx="9.4" cy="11" r="3" fill="#F9FAFB" />
      <circle cx="15.6" cy="11" r="3" fill="#F9FAFB" />
      {/* pupils */}
      <circle cx="10.4" cy="11.2" r="1.2" fill="#111827" />
      <circle cx="16.6" cy="11.2" r="1.2" fill="#111827" />
      {/* snout */}
      <rect x="10" y="13.2" width="4" height="3" rx="1.5" fill="#F6D2A2" />
      <rect x="11.4" y="14" width="1.2" height="1.6" rx="0.6" fill="#111827" />
    </svg>
  );
}
