# Brisk — Turn ON fully-automatic agent deploys (do this AFTER the control plane is on a real server)

**Status as of 2026-06-17:** the self-service rollout is **built and live**. The fleet (NYC, FRA,
BLR) currently runs agent **0.4.1** and updates itself when you push + sign a release. Today you use
the **manual** path (one command + one click). This doc is the checklist to switch to **fully
automatic** (git tag → CI builds, signs, uploads → you just click Deploy) — which only becomes
possible once the **control plane is hosted on a real, internet-reachable server** instead of your
laptop.

> **Why it can't run yet:** the CI runs on GitHub's servers. They must be able to reach your control
> plane over the internet to upload the release. Right now the control plane is on your laptop at
> `http://localhost:8080` — GitHub can't reach `localhost`. The moment the control plane has a public
> address (e.g. `https://control.yourdomain.com`), the automation works.

---

## What already exists (nothing to build — just to enable later)

| Piece | Where | State |
|-------|-------|-------|
| CI workflow (build → sign → upload) | `.github/workflows/agent-release.yml` | ✅ written, waiting |
| Release uploader CLI | `brisk-control/cmd/briskctl` (+ prebuilt `brisk-control/briskctl.exe`) | ✅ done |
| Release store + verify-on-upload | control plane `POST /api/v1/releases` (verifies ed25519 sig, fail-closed) | ✅ live |
| Rollout engine + dashboard Deploy | `internal/rollout` + Servers page | ✅ live |
| Trusted public key baked into agent | `brisk-agent/selfupdate/keys.go` (k1 = your key) | ✅ live |
| Your **private** signing key | `D:\Webapps\signing.key` (and/or your password manager) | ✅ you hold it |

---

## The manual path you use TODAY (works on the laptop)

1. Claude builds the new agent and stages it (e.g. `brisk-control/tunnels/brisk-agent-vX.Y.Z`).
2. You sign + upload it:
   ```powershell
   cd D:\Webapps\Brisk\brisk-control
   $env:BRISK_CP_URL="http://localhost:8080"
   $env:BRISK_ADMIN_TOKEN="<an admin API token from dashboard Settings>"
   .\briskctl.exe release push --version X.Y.Z --bin tunnels\brisk-agent-vX.Y.Z --key D:\Webapps\signing.key
   ```
   Success looks like: `upload X.Y.Z: 201 {"signed_by":"k1",...}`
3. Dashboard → **Servers** → the **Deploy X.Y.Z** button lights up → pick PoPs → **Deploy**.
   The fleet self-updates one edge at a time, health-soaked, with pause/rollback.

Keep this as the fallback even after automation — it's handy for one-off or emergency pushes.

---

## ENABLE FULL AUTOMATION — step by step (run this once, AFTER the CP is on a real server)

### Prerequisite
- Control plane reachable at a public HTTPS URL, e.g. `https://control.yourdomain.com`.
  (Its agent-facing API is the `:8080` service; expose it behind TLS.)

### Step 1 — Put the code on GitHub
1. Create a **private** GitHub repo (e.g. `brisk`).
2. Push this project to it. (The `.github/workflows/agent-release.yml` file travels with it.)
   - Note: this project currently isn't a git repo. First time only:
     ```powershell
     cd D:\Webapps\Brisk
     git init
     git add .
     git commit -m "Brisk: initial import"
     git branch -M main
     git remote add origin https://github.com/<you>/brisk.git
     git push -u origin main
     ```
   - **Before pushing, double-check secrets are NOT committed.** `brisk-control/.env`,
     `brisk-control/tunnels/.env`, `D:\Webapps\signing.key`, and any `*.key` must be in
     `.gitignore`. The private signing key must NEVER land in the repo.

### Step 2 — Add 3 GitHub Actions secrets
In the GitHub repo → **Settings → Secrets and variables → Actions → New repository secret**, add:

| Secret name | Value |
|-------------|-------|
| `BRISK_AGENT_SIGNING_KEY` | the **private** ed25519 key (the long base64 string from `briskctl keygen` — the same one in `D:\Webapps\signing.key`) |
| `BRISK_CP_URL` | your public control-plane URL, e.g. `https://control.yourdomain.com` |
| `BRISK_ADMIN_TOKEN` | an admin API token (dashboard → Settings → API tokens) with release-upload rights |

> This is the *only* place the private key should live for CI. It's encrypted by GitHub and never
> printed in logs (the workflow shreds its temp copy). The agents still re-verify every signature
> against their own baked-in **public** key, so even a compromised CI cannot ship a forged binary.

### Step 3 — Make sure the control plane trusts the key
On the real server, the control plane must have `BRISK_AGENT_PUBKEYS` set to the **public** key
(`qBDcMheQ5P5nTrYMYWkEJNftwX45kfL8d5z0qYdkZbg=`) and `BRISK_ROLLOUT_ENABLED=true`.
(These are already wired in `brisk-control/docker-compose.yml` + `.env` from this session — carry
them over to the server's env.)

### Step 4 — Ship a release automatically
From then on, to publish a new agent version you just tag and push:
```powershell
git tag agent-v0.5.0
git push origin agent-v0.5.0
```
GitHub Actions will: build the static linux agent (stamped `0.5.0`) → ed25519-sign it → upload it to
your control plane. Watch it under the repo's **Actions** tab; success = the release appears in the
dashboard.

### Step 5 — Deploy (still a human click — on purpose)
Open the dashboard → **Servers** → **Deploy 0.5.0** → choose PoPs → **Deploy**. CI never rolls
anything out by itself; a person always chooses when the fleet actually changes. (If you ever want
*that* automated too, it'd be a separate, deliberate change.)

---

## Quick mental model

```
  (today, laptop)         you: build + briskctl push + click Deploy
  (later, real server)    git push tag  →  CI builds+signs+uploads  →  you click Deploy
                                            ▲ needs public CP URL + GitHub repo + 3 secrets
  always true:            each edge re-verifies YOUR signature before swapping (trust anchor)
                          rollout is one-edge-at-a-time, health-soaked, pause/rollback
```

## Security rules that never change
- The **private signing key** lives ONLY in: your `D:\Webapps\signing.key` / password manager, and
  (for CI) the `BRISK_AGENT_SIGNING_KEY` GitHub secret. Never in chat, never committed, never on an edge.
- Rotate the admin API token if it's ever exposed (dashboard → Settings → API tokens).
- Keep `.env` files and `*.key` gitignored.

## Related docs
- How edge deploys work (gated, byte-identical): `docs/features/Brisk_Agent_Rollout_Process.html`
- This session's build (spec + plan): `docs/superpowers/specs/2026-06-16-agent-self-service-rollout-design.md`,
  `docs/superpowers/plans/2026-06-16-agent-self-service-rollout.md`
