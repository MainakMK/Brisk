# Brisk CDN — Phase 2 / Step 6.0 Prompt (Dashboard Design Capture + Reference)

**For Claude Code.** Context in the repo: `CLAUDE.md` + `docs/Brisk_Phase1_Build_Spec.md` + the Phase‑2 Step‑1…5 prompts. **Phase 2 Steps 1–5 are complete:** `brisk-control` (Go + chi + pgx + TimescaleDB) with token auth, SSH add‑server provisioning, agent pull‑config, stats → TimescaleDB (`/overview`, `/servers/{id}/live`, `/stats`), and instant purge over NATS (`/zones/{id}/purge`, `/purge/all`, `/purge/jobs`). **No frontend exists yet.** Step 6 builds the dashboard, and **6.0 (this step) produces only the blueprint — NO UI code.**

> **Read `CLAUDE.md` and `docs/Brisk_Phase1_Build_Spec.md` first.** This is **Step 6.0 of Phase 2**. The output is a set of **reference documents** in a new `dashboard-reference/` folder. Do **not** write any React/Tailwind/component code in this step — that's 6.1 onward.

## Step 6.0 goal (one line)
Produce the blueprint for a **professional, top‑tier Brisk dashboard**: study how **Bunny** and **Cloudflare** dashboards **work** (features + UX flow), gather **high‑end visual design ideas from legitimate inspiration sources**, and turn it all into a **Brisk Design + Information‑Architecture spec** (our own design, mapped to our existing API) — saved under `dashboard-reference/`.

---

## ⚖️ Capture rules (important — keep Brisk legally clean and sellable)
Brisk will be **sold against** Bunny and Cloudflare, so we learn from them the professional way.

**From the competitors (Bunny, Cloudflare) capture — in your OWN WORDS:**
- ✅ What each feature does and **where it lives** in the navigation.
- ✅ The **UX flow** for each task (e.g. "add zone → enter origin → choose cache → save").
- ✅ What **data/controls** each screen shows (metrics, charts, filters, tables, actions).
- ✅ High‑level **layout structure described in words** ("sidebar of products; top bar with account switcher + search; main area = KPI cards over time‑series charts").

**Do NOT capture from the competitors:**
- ❌ Screenshots, CSS, HTML, design assets, icons, exact color values, or pixel layouts; ❌ anything intended to rebuild their UI pixel‑for‑pixel. Their **visual design is their IP**, and a sellable product can't be a derivative of it.

**Where the actual VISUAL polish comes from instead → legitimate, reusable sources (Part 2).** That's how we get a top‑tier look *legally*: inspiration galleries built for learning, and component libraries licensed for you to build on. The result is unmistakably **Brisk's own** design, at professional quality.

## 🌐 How to gather competitor features/flows (prefer public)
1. **Prefer public docs + feature/product pages** — Bunny's and Cloudflare's documentation describes every feature reliably and is fair to study. Start there.
2. **Logged‑in dashboards (optional, user‑driven):** if you have browser access, you may open the dashboards and **pause for the user to log in themselves** (never bypass logins/2FA/bot‑protection), then take **functional notes only** (features/flows in words — not assets). If blocked or logged out, fall back to public docs.
3. The user may also **describe in their own words** what they like about the competitors' look ("Cloudflare's analytics layout feels clean," etc.) — record those as the user's stated preferences.

---

## Part 1 — Competitor capture (features + UX flow, in words)
Create `dashboard-reference/bunny-notes.md` and `dashboard-reference/cloudflare-notes.md`. For each product, walk these areas; for each note *what it does, where it lives, the task flow, the data/controls shown, and layout structure (in words)*:
Overview/home · Zones/pull‑zones (list + create + per‑zone settings) · Cache/edge rules · Analytics/statistics (metrics, chart types, ranges, filters, top‑N) · Logs (real‑time/historical, fields, export) · Purge/invalidation · Security (note for later phases) · Servers/network/regions · Settings/API/tokens · the Add‑resource wizard flow · cross‑cutting UX (navigation, search, dark mode, empty/loading/error states, responsiveness).

## Part 2 — Visual design inspiration (this is where the "professional look" comes from)
Create `dashboard-reference/design-inspiration.md`. Gather **high‑end dashboard design ideas from sources made to be learned from / reused**, and summarize the patterns worth adopting (layout, spacing, color usage, chart styling, component polish):
- **Inspiration galleries** (study freely, describe patterns in your own words): **Dribbble**, **Mobbin**, **SaaSFrame**, **Land‑book**, **Godly** — search "CDN / analytics / admin / SaaS dashboard." Note recurring high‑quality patterns (hero KPI rows, bento grids, sidebar styles, chart treatments, dark‑mode palettes).
- **Reusable, licensed component libraries / templates** (these give Cloudflare‑grade polish *and are licensed for you to build on*): **shadcn/ui** blocks + dashboard examples, **Tremor** dashboard templates/blocks, **Tailwind UI** application‑shell/dashboard patterns, and permissively‑licensed admin templates (e.g. shadcn‑admin, TailAdmin). Note which blocks Brisk can use directly (stat cards, charts, data tables, app shell).
- For each, capture **the design principle/pattern** (e.g. "stat card = label + big number + delta + sparkline; 16px gutters; muted grid, single accent color"), not copied code.

> The visual quality target: as polished as Cloudflare/Bunny, achieved through these legitimate libraries + galleries, skinned in Brisk's own identity.

## Part 3 — Original Brisk mockup concepts
Create `dashboard-reference/brisk-mockup-concepts.md` (and optionally 1–2 standalone static HTML mockups in `dashboard-reference/mockups/` using the **frontend-design skill**, Tailwind via CDN, **mock data only**). Produce **original** low‑/mid‑fidelity concepts for the **Overview** and **Servers** screens in Brisk's identity, so the user has something concrete to react to and refine. These are throwaway concept art for direction — not the real app (the real build is 6.1+). Keep them clearly Brisk's own design, informed by Parts 1–2.

## Part 4 — Feature comparison
Create `dashboard-reference/feature-comparison.md`: table of **Feature | Bunny | Cloudflare | Needed for Brisk v1? | Brisk screen | Notes**. Mark must‑have‑for‑v1 vs future (security/WAF = Phase 4; billing = customer‑portal era), and map each to a Brisk screen + the API it would use.

## Part 5 — Brisk Design + IA spec (the main deliverable)
Create `dashboard-reference/brisk-design-spec.md` defining **Brisk's own dashboard**, grounded in the principles below and mapped to our **existing Go API**.

### 5a. The 6 screens (each: purpose, layout, components, API endpoints)
- **Overview** → `GET /api/v1/overview`. Hero KPIs: total bandwidth, req/s, global cache‑hit %, PoPs online/total; recent events; optional map.
- **Servers (PoPs)** → `GET /api/v1/servers` + `GET /api/v1/servers/{id}/live`; per‑server detail; **Add Server** → `POST /api/v1/servers` + stream `GET /servers/{id}/provision-log`.
- **Zones** → `/api/v1/zones` (+ create/edit), per‑zone **cache rules** → `/api/v1/zones/{id}/rules`.
- **Analytics** → `GET /api/v1/stats?...&resolution=1m` with PoP/zone/time filters (Tremor charts).
- **Logs** → real‑time request view.
- **Purge** → `/api/v1/zones/{id}/purge`, `/api/v1/purge/all`, status from `/api/v1/purge/jobs`.
- **Nav shell** → left sidebar (6 sections) + top bar (account, global search, dark‑mode toggle, quick "Add Server/Zone").
- **Role‑aware:** note admin vs future **customer portal** differences (customers see only their `account_id`); same screens, filtered. Keep components account‑scopable; don't build the portal.

### 5b. Design principles to bake in (current best practice)
- **F‑pattern:** most critical metric top‑left; **3–5 primary KPIs across the top row**; secondary down the left.
- **Limit primary KPIs to 5–7**; **progressive disclosure** (summary first, drill‑down on demand).
- **8px spacing grid / 12‑col grid, ~16px gutters**; tile size reflects **data priority**.
- **No chart junk**; generous whitespace.
- **Large tables:** server‑side pagination/filtering + virtualization.
- **Chart choice:** KPI card = single number; table = precise/compare; line/area = trends; geo/heat = distribution.
- **Dark + light** with saved preference; **skeleton loaders**, explicit **empty/error** states; **keyboard nav**.
- **WCAG 2.1 AA:** ARIA labels, DOM order = visual order, contrast ≥ 4.5:1, visible focus rings.

### 5c. Brisk design system (`dashboard-reference/brisk-design-tokens.md`)
Define Brisk's **own** identity: propose **2–3 palette options** (one recommended default) for "Brisk = fast/sharp/professional," as **Tailwind v4 CSS‑variable tokens** (`--color-primary`, `--color-bg`, `--radius`, etc.) for light + dark; typography scale; 8px spacing scale; radii; and how **shadcn/ui** + **Tremor** get skinned with these tokens. Leave the final color pick to the user (present options).

## Output files (this step produces ONLY these — no app code)
```
dashboard-reference/
├── bunny-notes.md
├── cloudflare-notes.md
├── design-inspiration.md          # patterns from galleries + reusable libraries (the visual-quality source)
├── brisk-mockup-concepts.md       # original Brisk concept directions (+ optional mockups/ HTML, mock data)
├── feature-comparison.md
├── brisk-design-spec.md           # IA + 6 screens + API mapping + principles (MAIN deliverable)
└── brisk-design-tokens.md         # Brisk palette options + tokens
```

## Acceptance (Step 6.0 definition of done)
- All docs present in `dashboard-reference/`.
- Competitor notes are **functional descriptions in your own words** (no screenshots/CSS/assets).
- `design-inspiration.md` gives concrete, professional **visual patterns** from legitimate sources + which reusable blocks Brisk will use.
- `brisk-mockup-concepts.md` shows **original** Brisk directions (optional HTML mockups use mock data only).
- `brisk-design-spec.md` defines all 6 screens mapped to the **actual API endpoints** from Steps 1–5, follows the principles, and notes role‑aware (admin vs customer) differences.
- `brisk-design-tokens.md` proposes palette options + Tailwind v4 tokens.
- **No React/Tailwind app code written.** Present the spec + palette options and wait for the user to pick a palette before 6.1.

## Pitfalls (do not skip)
1. **Competitor study = features/flows in your own words only** — no captured screenshots/CSS/assets, no pixel‑cloning. Visual polish comes from the **legitimate inspiration + reusable libraries** in Part 2.
2. **Don't bypass logins/2FA/bot‑protection** — user logs in; fall back to public docs if blocked.
3. **Map every Brisk screen to a real existing endpoint**; flag any data gap for 6.1+ rather than inventing API.
4. **No UI app code in 6.0** — concepts/mockups are throwaway direction; the real build is 6.1+.
5. **Brisk's own identity** — design tokens are ours, inspired by patterns, not derived from competitor colors/assets.

## Next — Step 6.1 (do NOT start)
Frontend skeleton + design system from `brisk-design-spec.md` (once the user picks a palette): React + TypeScript + Vite + Tailwind v4 + shadcn/ui + Tremor in Docker — app shell (sidebar, top bar, routing, dark/light, Brisk tokens), API client + auth, empty pages wired up. **6.1 will reference `dashboard-reference/` (the spec, inspiration patterns, and tokens) as the design source.** Wait for the user's go‑ahead and a Step 6.1 prompt.
