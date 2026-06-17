# Brisk Design Tokens — palette options + Tailwind v4 tokens

Brisk's **own** visual identity: *fast, sharp, professional, owned.* Three palette options below,
each as **Tailwind v4 CSS-variable tokens** (shadcn-compatible) for **light + dark**, plus a
shared **typography / spacing / radii** scale and how **shadcn/ui + Tremor** get skinned.

**Deliberately distinct from competitors:** Cloudflare = orange; Bunny = orange/black. Brisk
avoids both. The recommended default is a **sharp electric azure** ("Brisk Blue") that reads as
speed + trust. Two alternatives (teal "Signal", indigo "Voltage") are provided.

> **Decision needed:** pick one option (or ask to blend) before 6.1. Nothing here is derived from
> a competitor's colors — these are ours.

---

## Shared foundations (all options)

### Typography
- **Sans (UI):** `Inter` (or `Geist Sans`) — clean, neutral, great tabular figures.
- **Mono (numbers/logs/IDs):** `JetBrains Mono` (or `Geist Mono`) — for KPIs, log tables, tokens.
- Use **tabular-nums** for all metrics so digits don't jitter on refresh.
- **Type scale (1.25 ratio, rem):**
  `--text-xs:0.75 · --text-sm:0.875 · --text-base:1 · --text-lg:1.125 · --text-xl:1.25 ·
  --text-2xl:1.5 · --text-3xl:1.875 · --text-4xl:2.25` (KPI big numbers use 2xl–4xl, tabular).
- **Weights:** 400 body, 500 labels, 600 headings/KPIs. Letter-spacing -0.01em on large numbers.

### Spacing (8px rhythm) & layout
- `--space-1:4px · -2:8px · -3:12px · -4:16px(gutter) · -5:20px · -6:24px · -8:32px · -10:40px · -12:48px`.
- 12-col grid, ~16px gutters; card padding 20–24px; section gap 24px.

### Radii & elevation
- `--radius: 0.625rem` (10px) base → `sm:6px md:8px lg:10px xl:14px`. Sharp-but-soft (Brisk = sharp,
  so keep radii modest, not pill-round).
- Shadows: very subtle in light (`0 1px 2px rgba(16,24,40,.06)`), near-none in dark (rely on
  surface elevation + low-contrast borders).

### Motion
- 150–200ms ease-out for hovers/toggles; chart draw 300–400ms; respect `prefers-reduced-motion`.

### Status colors (shared, all palettes — color + always paired with label/icon)
- success/online `#16a34a` · warning/degraded `#d97706` · danger/offline `#dc2626` ·
  info `#2563eb` · neutral/pending `#64748b`. Dark variants ~+10% lightness.

---

## Option A — "Brisk Blue" (RECOMMENDED default)
Electric azure accent, slate neutrals. Fast, trustworthy, professional; distinct from CF/Bunny orange.

```css
/* Tailwind v4: put in @layer base / :root. shadcn-compatible variable names. */
:root {
  --background: #f8fafc;        /* slate-50 */
  --foreground: #0f172a;        /* slate-900 */
  --card: #ffffff;
  --card-foreground: #0f172a;
  --popover: #ffffff;
  --popover-foreground: #0f172a;
  --primary: #0ea5e9;           /* sky-500 — Brisk azure */
  --primary-foreground: #ffffff;
  --secondary: #f1f5f9;         /* slate-100 */
  --secondary-foreground: #0f172a;
  --muted: #f1f5f9;
  --muted-foreground: #64748b;  /* slate-500 */
  --accent: #e0f2fe;            /* sky-100 wash */
  --accent-foreground: #0c4a6e;
  --destructive: #dc2626;
  --destructive-foreground: #ffffff;
  --border: #e2e8f0;            /* slate-200 */
  --input: #e2e8f0;
  --ring: #0ea5e9;              /* focus ring = accent */
  --radius: 0.625rem;
  /* chart series (muted, single-accent-forward) */
  --chart-1: #0ea5e9;  --chart-2: #38bdf8;  --chart-3: #6366f1;
  --chart-4: #14b8a6;  --chart-5: #f59e0b;
}
.dark {
  --background: #0b1220;        /* near-black navy, not pure black */
  --foreground: #e2e8f0;
  --card: #111a2b;             /* elevated surface */
  --card-foreground: #e2e8f0;
  --popover: #111a2b;
  --popover-foreground: #e2e8f0;
  --primary: #38bdf8;           /* brighter azure in dark */
  --primary-foreground: #06121f;
  --secondary: #1e293b;
  --secondary-foreground: #e2e8f0;
  --muted: #1e293b;
  --muted-foreground: #94a3b8;
  --accent: #0c2438;
  --accent-foreground: #bae6fd;
  --destructive: #ef4444;
  --destructive-foreground: #0b1220;
  --border: rgba(255,255,255,0.08);   /* low-contrast border */
  --input: rgba(255,255,255,0.10);
  --ring: #38bdf8;
  --chart-1: #38bdf8;  --chart-2: #0ea5e9;  --chart-3: #818cf8;
  --chart-4: #2dd4bf;  --chart-5: #fbbf24;
}
```

## Option B — "Signal" (teal/emerald)
Speed + uptime feel; very distinct from competitors. Calmer, "ops/observability" vibe.

```css
:root {
  --background: #f7faf9; --foreground: #0c1512; --card: #ffffff; --card-foreground: #0c1512;
  --popover:#ffffff; --popover-foreground:#0c1512;
  --primary: #0d9488;          /* teal-600 */
  --primary-foreground:#ffffff;
  --secondary:#eef2f1; --secondary-foreground:#0c1512;
  --muted:#eef2f1; --muted-foreground:#5b6b66;
  --accent:#ccfbf1; --accent-foreground:#134e4a;
  --destructive:#dc2626; --destructive-foreground:#ffffff;
  --border:#e2e8e6; --input:#e2e8e6; --ring:#0d9488; --radius:0.625rem;
  --chart-1:#0d9488; --chart-2:#2dd4bf; --chart-3:#0ea5e9; --chart-4:#6366f1; --chart-5:#f59e0b;
}
.dark {
  --background:#08110f; --foreground:#dce7e3; --card:#0e1a17; --card-foreground:#dce7e3;
  --popover:#0e1a17; --popover-foreground:#dce7e3;
  --primary:#2dd4bf; --primary-foreground:#04130f;
  --secondary:#16241f; --secondary-foreground:#dce7e3;
  --muted:#16241f; --muted-foreground:#8aa39b;
  --accent:#0c2a24; --accent-foreground:#99f6e4;
  --destructive:#ef4444; --destructive-foreground:#08110f;
  --border:rgba(255,255,255,0.08); --input:rgba(255,255,255,0.10); --ring:#2dd4bf;
  --chart-1:#2dd4bf; --chart-2:#0d9488; --chart-3:#38bdf8; --chart-4:#818cf8; --chart-5:#fbbf24;
}
```

## Option C — "Voltage" (indigo/violet)
Modern SaaS, premium/sharp; energetic without competitor overlap.

```css
:root {
  --background:#faf9fc; --foreground:#15121f; --card:#ffffff; --card-foreground:#15121f;
  --popover:#ffffff; --popover-foreground:#15121f;
  --primary:#6366f1;            /* indigo-500 */
  --primary-foreground:#ffffff;
  --secondary:#f2f1f8; --secondary-foreground:#15121f;
  --muted:#f2f1f8; --muted-foreground:#6b6880;
  --accent:#e0e7ff; --accent-foreground:#3730a3;
  --destructive:#dc2626; --destructive-foreground:#ffffff;
  --border:#e7e5ee; --input:#e7e5ee; --ring:#6366f1; --radius:0.625rem;
  --chart-1:#6366f1; --chart-2:#818cf8; --chart-3:#0ea5e9; --chart-4:#14b8a6; --chart-5:#f59e0b;
}
.dark {
  --background:#0c0a14; --foreground:#e4e2ef; --card:#14111f; --card-foreground:#e4e2ef;
  --popover:#14111f; --popover-foreground:#e4e2ef;
  --primary:#818cf8; --primary-foreground:#0a0814;
  --secondary:#1d1930; --secondary-foreground:#e4e2ef;
  --muted:#1d1930; --muted-foreground:#9a96b5;
  --accent:#241f3d; --accent-foreground:#c7d2fe;
  --destructive:#ef4444; --destructive-foreground:#0c0a14;
  --border:rgba(255,255,255,0.08); --input:rgba(255,255,255,0.10); --ring:#818cf8;
  --chart-1:#818cf8; --chart-2:#6366f1; --chart-3:#38bdf8; --chart-4:#2dd4bf; --chart-5:#fbbf24;
}
```

---

## How shadcn/ui gets skinned
shadcn reads these exact variable names (`--background`, `--primary`, `--border`, `--ring`,
`--radius`, …). Drop the chosen option's `:root` + `.dark` blocks into the global stylesheet and
**every shadcn component (sidebar, cards, tables, dialogs, badges, buttons) inherits Brisk's
identity automatically.** Tailwind v4 maps them via `@theme inline` (e.g.
`--color-primary: var(--primary)`), so utility classes like `bg-primary text-primary-foreground`
and `rounded-[--radius]` just work. Dark mode = toggle `.dark` on `<html>` (save preference).

## How Tremor gets skinned
Tremor uses Tailwind color classes + a `colors` prop on charts. Two steps:
1. Map Tremor's accent to Brisk: set chart `colors={["primary", ...]}` or pass our hexes; align
   Tremor's gray scale to our `--muted`/`--border` so gridlines/axes read muted.
2. Chart treatment: 2px line, gradient area fill ~12% opacity of `--chart-1`, gridlines at
   `--border`, axis text `--muted-foreground`, tooltip = `--popover`/`--popover-foreground` with a
   color dot + tabular-nums. This gives the "expensive, restrained" look from `design-inspiration.md`.

## Accent usage rule (keeps it pro)
**One accent (`--primary`) carries the brand.** Charts lean on `--chart-1` (= primary) with
secondary series in muted blues/teal/violet. Status colors are the only other saturated colors,
and they always pair with a label/icon. Everything else is layered neutrals. This restraint is
what reads as "Cloudflare-grade" without copying anyone.

## Recommendation
**Go with Option A "Brisk Blue"** as the default: it's the sharpest "fast + trustworthy" read,
maximally distinct from Cloudflare/Bunny orange, and the azure pops cleanly on the near-black
navy dark mode (where a CDN dashboard mostly lives). Option B if we want an "ops/observability"
identity; Option C for a more premium-SaaS feel.

## ✅ DECISION (locked 2026-06-08, before 6.1)
**Chosen palette: Option C "Voltage" (indigo/violet).** 6.1 uses Option C's `:root` + `.dark`
token blocks as Brisk's default theme (light + dark). The two HTML mockups in `mockups/` were
drawn in Option A azure — when the real app is built in 6.1 it adopts **Voltage indigo**
(`--primary:#6366f1` light / `#818cf8` dark, indigo chart series); re-skin the mockup patterns
accordingly. All other foundations (typography, 8px spacing, radii, status colors, accent-usage
rule, shadcn/Tremor skinning) are unchanged.
