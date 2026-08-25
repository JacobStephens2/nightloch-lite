# Status

**As of:** 2026-08-24
**Full handoff:** [HANDOFF.md](./HANDOFF.md)

## Now

| Slice | State |
|---|---|
| −1 Security & policy design | Done |
| 0 Policy package + skeleton | Done (`go test ./...`) |
| 1 Live origin (OpenTofu + Caddy staging) | **Code in repo**; live demo needs human apply |
| 2 Magic-link sign-in | **Code in repo**; live two-email demo follows Slice 1 apply |
| 9 Continuous deploy (ordered after Slice 2) | **Code in repo**; enable only after staging service setup |
| 3 Partner graph + Activity | **3A and 3B complete; 3C next** — issues #3–#9 done (mail envelopes, durable invitation, first Partnership accepted with Partner lock, invitation discovery/decline/cancel/expiry with precise terminal outcomes, immediate Partner ending with typed `END`, zero-state restoration, retained receipts, and the 72-hour Protectee Cooling-off end to end: retained powers, cancel, End now, and a restart-safe deadline runner attributed to Nightloch). #9 lifts the one-Partner exposure gate: a second Partner joins end to end with the full recipient matrix, the full Roster, and read-only visibility of pending invitations. #10 adds Reciprocal Partnerships and mixed-role dashboards: the reverse-direction invitation is permitted and disclosed to the invitee, each direction keeps its own Log sharing, Cooling-off, ending, powers, and receipt, and neither direction's controls or authorization reach the other. #11 runs Cooling-off concurrently: independent countdowns on any number of Partnerships, one shared exit horizon, per-row Partner-lock consequences, the final-Partnership transition alert when a Roster change makes a running countdown the last one, and the acceptance disclosure that undoes it. #12 scales invitations safely: the approved pending cap, the rolling send/resend quota, once-a-day resend of an otherwise immutable invitation to the invitee alone, and pending rows that show the reminders without moving the expiry. #13 makes reachability honest: Resend's signed delivery, bounce, and complaint callbacks are authenticated at `POST /webhooks/resend`, applied once per provider event id and in provider-clock order, and surfaced as **Delivery delayed**, **Nightloch cannot reach this Partner**, or **Invitation not delivered** with the cancel-and-reinvite remedy — while powers, mutations, Cooling-off deadlines, and CanLoosen stay untouched. #14 opens 3C with the Activity read path: one chronology per Protectee, projected in the Partnership module through the viewer's own visibility window, reachable at `/activity` and `/activity/{protectee_id}`, and linked from the Action alerts that used to point at a dashboard. A Protectee keeps everything, a Partner sees from their acceptance instant onward, a former Partner keeps only their receipt, re-partnering opens a fresh window that hides the gap, reciprocal directions never merge, and an unrelated User, a pending invitee, or an unauthenticated visitor infers nothing from a direct id; next is #15–#17 |

OpenTofu + `scripts/slice1-up.sh` ready. **No droplet until you apply.** Bitwarden laptop wiring verified (tokens inject; values never logged).

```bash
source ~/.zshrc && bws-auth   # if needed
cd ~/GitHub/JacobStephens2/nightloch-lite
./scripts/slice1-up.sh plan
# then: ./scripts/slice1-up.sh apply
```

## Next

1. **You:** `./scripts/slice1-up.sh plan` then `apply` (optional: adversarial review of Slice 1 code first)  
2. **Deploy:** follow [docs/ops/SLICE-2-AUTH.md](./docs/ops/SLICE-2-AUTH.md) for Resend, Turnstile, migration, and the two-email auth demo
3. **Deploy:** configure the staging GitHub environment using [docs/ops/SLICE-9-DEPLOY.md](./docs/ops/SLICE-9-DEPLOY.md), then set `DEPLOY_ENABLED=true`
4. **Code:** continue Slice 3 in ticket order — 3A, 3B, and #14 are done; 3C continues with #15–#17

## Secrets (non-secret metadata)

| Item | Value |
|---|---|
| Bitwarden SM project | `nightloch-lite` |
| Project UUID | `19dfca05-8d2f-491b-981a-b4950159eb9c` |
| Machine account | `jacob-macbook-local-dev` (Can read) |
| Token name / expires | `jacob-macbook-interactive-2026-07` / ~2026-10-26 |
| Secret keys present | `DIGITALOCEAN_TOKEN`, `CLOUDFLARE_API_TOKEN` |
| Local helpers | `bws-auth`, `bws-shell`, `bws-run`, `bws-logout` in `~/.zshrc` |

Never print secret values. Inject via `bws-run` / `bws-shell` only.

## Orient

| Doc | Role |
|---|---|
| [AGENTS.md](./AGENTS.md) | Agent onboarding |
| [CONTEXT.md](./CONTEXT.md) | Domain language |
| [shared-understanding.md](./shared-understanding.md) | Product freeze |
| [plan/plan.md](./plan/plan.md) | Full slice plan |
| [docs/BITWARDEN_SECRETS_SETUP.md](./docs/BITWARDEN_SECRETS_SETUP.md) | Full SM + CLI setup |
