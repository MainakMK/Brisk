import { cn } from "@/lib/utils";

/**
 * Small SVG country flag via flag-icons (self-hosted, works on every OS incl. Windows
 * — unlike emoji flags, which Windows does not render). `cc` is an ISO-2 country code.
 * Decorative, so it's aria-hidden (the country name sits next to it in text).
 */
export function Flag({ cc, className }: { cc?: string | null; className?: string }) {
  if (!cc || !/^[A-Za-z]{2}$/.test(cc)) return null;
  return (
    <span
      aria-hidden="true"
      className={cn(
        "fi shrink-0 rounded-[2px] align-[-0.12em] shadow-[0_0_0_1px_rgba(0,0,0,0.08)]",
        `fi-${cc.toLowerCase()}`,
        className,
      )}
    />
  );
}
