# Brisk CDN — TLS Realignment: make "Automatic SSL" (`managed`) the only certificate path

**For Claude Code.** Follows the read-only TLS audit. **Decision (user):** zones use **Automatic SSL only** = `tls_mode=managed` (control-plane lego **Bunny DNS-01** wildcard `*.cdn.a2zjav.com` / `*.a2zjav.com`, fanned to edges, auto-renewed). Remove the broken/dev modes from the user-facing path. **State:** Step 7 complete; zone 6 `cdn.a2zjav.com` intentionally deleted (not needed); only **zone 12 `testmainak`** and **zone 13 `testjim`** are active, both already `managed`.

> **Audit facts this fixes:** API default `tls_mode` is `letsencrypt` (the per-edge HTTP-01 mode that crash-looped BLR) — `zones.go:102`. The dashboard Add-zone form offers **Let's Encrypt / Self-signed** (and defaults to Let's Encrypt) but **not** `managed` — `zone-form.tsx:39,109-112`. The Add-zone **"Custom domain" field** is stored and **overrides the served `server_name`** in the agent (`source.go toZone`) but triggers **no verification and no cert** → a footgun. So a real site onboarded through the form today is born into the broken mode.

> **Scope:** control-plane API + DB default + dashboard only. **No agent change, no edge rollout.** Do NOT touch the agent's TLS consts, its self-signed *placeholder* fallback, or `managed` cert handling — those stay (the placeholder is the safety net). Live edges keep serving throughout (data-plane independent). `testmainak`/`testjim` already `managed`, so no data migration of existing zones.

## Goal (one line)
Make `managed` the sole TLS mode a user can set (API + dashboard), default to it, reject the broken/dev modes with clear messages, and remove the unverified custom-domain footgun — so onboarding a real site can't pick anything but the cert path that works.

## Part 1 — API: default + restrict to `managed`
In `createZoneInput`/`updateZoneInput` (`zones.go`):
1. **Default** `tls_mode` to `managed` when omitted (change the `letsencrypt` default at `zones.go:102`).
2. **Restrict** the validator to `managed` only: `validate:"omitempty,oneof=managed"`. If a caller sends `letsencrypt`, `mkcert`, or `selfsigned`, reject **422** with: `"Only Automatic SSL is supported (tls_mode=managed). Per-edge Let's Encrypt, self-signed, and mkcert aren't available for zones."`
3. **Covered-hostname check** (reuse the Part-3 covering-cert helper): the hostname must be covered by a managed wildcard (`*.a2zjav.com` / `*.cdn.a2zjav.com`, incl. apex SAN). If not covered (an external/custom domain) → reject **422**: `"Custom external domains aren't supported yet (Step 4.8). Onboard under *.cdn.a2zjav.com."`
4. Leave the agent's `letsencrypt`/`mkcert`/`selfsigned` code paths intact (now dead for zones, harmless) — minimal change; cleanup can wait for Step 4.8.

## Part 2 — DB: default + value constraint (fix the audit's "no CHECK" gap)
Migration `00020`:
- Change `zones.tls_mode` column **default** from `'letsencrypt'` → `'managed'`.
- Add a **CHECK constraint** `tls_mode IN ('managed','letsencrypt','selfsigned','mkcert')` (formalizes valid values without locking to managed-only at the DB layer; the API is the managed-only gate).
- **Do not mutate existing rows** — zones 12/13 are already `managed`. Verify post-migrate: `SELECT id, cdn_hostname, tls_mode FROM zones;` → both `managed`.

## Part 3 — Dashboard: single "Automatic SSL" choice
In `zone-form.tsx`:
- Replace the TLS dropdown (`TLS_OPTIONS`, the Let's Encrypt / Self-signed list) with a **single fixed "Automatic SSL"** value = `managed` — render it as a read-only/info row or a one-option select, not a dropdown of choices.
- Change the form **default** (`zone-form.tsx:39`) from `letsencrypt` → `managed`.
- Helper text: `"Free SSL via Let's Encrypt — issued and renewed automatically."`
- Remove `letsencrypt` / `selfsigned` / `mkcert` from the UI entirely.

## Part 4 — Remove the unverified "Custom domain" footgun from Add-zone
In `zone-form.tsx`:
- **Remove (or disable)** the Add-zone **"Custom domain (optional)"** field so creating a zone no longer sets `zones.custom_domain` unverified. New zones serve on their `cdn_hostname` only.
- If disabling rather than removing, show: `"Custom domains are added after creating the zone (with verification) — coming in Step 4.8."`
- **Do not** touch the agent's `toZone()` `custom_domain` override, the `custom_domains` table, or the verified Custom-Domains endpoints — those are the real Step-4.8 path; only the unverified Add-zone shortcut is removed here.

## Acceptance
```
- POST /zones with no tls_mode            -> created as managed
- POST /zones tls_mode=letsencrypt|mkcert|selfsigned -> 422 (helpful "Automatic SSL only"); nothing created
- POST /zones for an external domain      -> 422 (Step 4.8 message); nothing created
- POST /zones managed for <x>.cdn.a2zjav.com -> created; serves with *.cdn.a2zjav.com after auto-assign/DNS/TLS
- migration 00020: column default = managed; CHECK present; zones 12/13 still managed (untouched)
- dashboard Add-zone: TLS shows only "Automatic SSL"; default managed; no Let's Encrypt/Self-signed/mkcert
- dashboard Add-zone: no unverified Custom-domain field (removed or disabled w/ note)
- brisk-control build/vet/tests clean + redeployed; dashboard typechecks + redeployed (new hash); edges untouched, testmainak/testjim still 200 + valid *.cdn TLS
```

## Pitfalls
1. **Don't touch the agent** — consts, self-signed placeholder, managed handling all stay; this is API + DB-default + dashboard only. No rollout.
2. **Reuse the covering-cert helper** — don't reinvent hostname→wildcard matching.
3. **Reject, don't silently rewrite** an explicit bad `tls_mode` — the caller should learn Automatic is the only option.
4. **Don't mutate existing zone rows** in the migration — 12/13 are already correct.
5. **Custom-domain:** only the unverified Add-zone shortcut is removed; leave the real verified Custom-Domains subsystem for Step 4.8.
6. Messages user-facing and specific (they surface in the dashboard).

## Next — onboard your sites (after this lands)
With Automatic SSL the only path and the footguns gone, creating a zone under `*.cdn.a2zjav.com` is safe and fully automatic. The user will provide site label + origin (+ optional upstream host) + PoPs; a short onboarding runbook follows.
