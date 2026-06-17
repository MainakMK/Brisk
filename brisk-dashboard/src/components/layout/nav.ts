import {
  LayoutDashboard,
  Server,
  Globe,
  BarChart3,
  ScrollText,
  Trash2,
  Network,
  ShieldCheck,
  Settings,
  type LucideIcon,
} from "lucide-react";

export interface NavItem {
  title: string;
  url: string;
  icon: LucideIcon;
}

/** Primary nav — the 6 screens from brisk-design-spec.md, plus reserved slots. */
export const primaryNav: NavItem[] = [
  { title: "Overview", url: "/", icon: LayoutDashboard },
  { title: "Servers", url: "/servers", icon: Server },
  { title: "Zones", url: "/zones", icon: Globe },
  { title: "Analytics", url: "/analytics", icon: BarChart3 },
  { title: "Logs", url: "/logs", icon: ScrollText },
  { title: "Purge", url: "/purge", icon: Trash2 },
  { title: "DNS", url: "/dns", icon: Network },
];

export const secondaryNav: NavItem[] = [
  { title: "Security", url: "/security", icon: ShieldCheck },
  { title: "Settings", url: "/settings", icon: Settings },
];
