import { CircleCheck, Wrench, HeartCrack, PowerOff, CircleHelp } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import type { RotationReason } from "@/lib/types";

type Meta = {
  label: string;
  badge: "success" | "warning" | "danger" | "muted";
  dot: string;
  icon: typeof CircleCheck;
  hint: string;
};

/** Rotation reason → label/variant/icon. Text + icon, never color alone. */
export const rotationMeta: Record<string, Meta> = {
  in_rotation: {
    label: "In rotation",
    badge: "success",
    dot: "var(--success)",
    icon: CircleCheck,
    hint: "Serving traffic — enabled in the cdn record set.",
  },
  drained: {
    label: "Draining",
    badge: "warning",
    dot: "var(--warning)",
    icon: Wrench,
    hint: "Maintenance — pulled from rotation by an operator. The box keeps serving in-flight requests; new traffic routes elsewhere.",
  },
  unhealthy: {
    label: "Unhealthy",
    badge: "danger",
    dot: "var(--danger)",
    icon: HeartCrack,
    hint: "Failing health probes — automatically removed from rotation until it recovers.",
  },
  offline: {
    label: "Offline",
    badge: "muted",
    dot: "var(--muted-foreground)",
    icon: PowerOff,
    hint: "No fresh heartbeat — the agent isn't checking in.",
  },
  unknown: {
    label: "Unknown",
    badge: "muted",
    dot: "var(--muted-foreground)",
    icon: CircleHelp,
    hint: "Rotation state not yet known.",
  },
};

export function rotationMetaFor(reason: RotationReason | undefined): Meta {
  return rotationMeta[reason ?? "unknown"] ?? rotationMeta.unknown;
}

/** Rotation/health badge driven by the effective rotation reason. */
export function RotationBadge({
  reason,
  className,
  title,
}: {
  reason: RotationReason | undefined;
  className?: string;
  title?: string;
}) {
  const meta = rotationMetaFor(reason);
  const Icon = meta.icon;
  return (
    <Badge
      variant={meta.badge}
      className={cn("gap-1", className)}
      aria-label={`Rotation: ${meta.label}`}
      title={title ?? meta.hint}
    >
      <Icon className="size-3" aria-hidden />
      {meta.label}
    </Badge>
  );
}
