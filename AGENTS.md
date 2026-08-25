# AGENTS.md — Nightloch Lite

Brief orientation for coding agents. Prefer this file for **where to look** and **what not to break**; product law lives in the linked docs.

## What this is

Self-hosted **partner-locked DNS protection**: full-tunnel WireGuard + AdGuard Home, controlled by a small **Go** app (SQLite). Protectees cannot freely loosen their Policy while Partnered; Partners can. Not the old multi-tenant Nightloch/Cloudflare control plane.

## Read first (in order)

| Doc | Why |
|---|---|
| [CONTEXT.md](./CONTEXT.md) | Ubiquitous language — use these terms in code, UI, and prose |
| [status.md](./status.md) | Project status and what’s next |
| [HANDOFF.md](./HANDOFF.md) | Latest session handoff for fresh agents |
| [shared-understanding.md](./shared-understanding.md) | Product freeze |
| [docs/SECURITY-POLICY-DESIGN.md](./docs/SECURITY-POLICY-DESIGN.md) | Perimeter, tighten/loosen, AGH model, fail modes |
| [plan/plan.md](./plan/plan.md) | Vertical-slice implementation plan |

Skip full re-reads of [DECISIONS.md](./DECISIONS.md) / adversarial reviews unless changing a locked decision.

## Language (do not invent synonyms)

- **Protectee** — User under restraint (not “Subject”)
- **Partner** / **Partnership** / **Roster** — who protects a Protectee
- **Device** — enrolled tunnel identity (not “peer” in product language)
- **Policy**, **Curfew**, **Baseline list**, **Extra block**, **Allowlist entry**, **Coverage**
- UI: Protectee sees **Your Partners**; Partner sees **People you protect**

## Layout

```text
cmd/nightloch/          # binary entry (HTTP later)
internal/policy/        # tighten/loosen predicate (Slice 0) — pure, well-tested
internal/enforcement/   # Slice 4 enforcement plane: tunnel address allocation
                        # and quarantine, Device records, canonical Device
                        # configuration, and the nightloch-wg helper contract
                        # behind an injected Tunnel interface
internal/partnership/   # Slice 3 relationship perimeter: invitations, Activity,
                        # authorization, durable per-recipient mail jobs, and
                        # the provider delivery events behind reachability
infra/opentofu/         # DO droplet + CF DNS (Slice 1)
deploy/                 # systemd/Caddy examples + the real nightloch-wg
                        # root helper and its sudoers grant
docs/                   # security design, secrets, ops runbooks
plan/                   # plan.md + adversarial review
reviews/                # slice reviews
CONTEXT.md              # glossary
```

## Implementation status

| Slice | Status |
|---|---|
| −1 Security/policy design | Done (docs) |
| 0 Policy package + skeleton | Done (`go test ./...`) |
| 1 Live origin (OpenTofu + Caddy staging) | **Code in repo**; live demo needs human `tofu apply` |
| 2 Magic-link sign-in | **Code in repo**; live demo pending origin |
| 9 Continuous deploy | **Code in repo**; live demo pending origin |
| 3 Partner graph + Activity | **Complete — 3A, 3B, and 3C all done (#3–#17); the 3C promotion gate passed** — `internal/partnership/` owns the invitation lifecycle (send, review, accept, decline, cancel, expire), Partnerships and every way they end (immediate Partner ending, 72-hour Protectee Cooling-off with cancel, End now, and the durable deadline runner), Activity, authorization, and durable mail jobs. The one-Partner exposure gate is lifted: a Protectee may hold any number of Partners and pending invitations, and every current Partner receives their own invitation, cancellation, and acceptance alert. Reciprocal Partnerships and mixed-role Users are supported: opposite-direction Partnerships stay independent in Log sharing, Cooling-off, ending, powers, and receipts, and the dashboard separates **Your protection** from **People you protect**. Cooling-off runs concurrently: a Protectee may schedule any number of their Partnerships to end, each keeping its own unchanged 72-hour deadline, so the full exit takes one horizon rather than one delay per Partner, and every Roster row states the Partner-lock consequence of its own deadline. When a Roster change leaves a running Cooling-off as the final Partnership, Nightloch appends its own Activity entry and alerts the Protectee and that Partner without touching the countdown; a later acceptance says the lock will outlive the scheduled end. Multiple pending invitations are now bounded rather than merely permitted: at most five pending per Protectee, at most ten send/resend operations per Protectee in a rolling 24 hours, and one resend per invitation per 24 hours. A resend repeats the immutable invitation to the invitee alone on a fresh bearer link, without touching the seven-day expiry (each send supersedes the previous bearer link — ADR 0003). Reachability is now derived from the provider rather than guessed from send attempts: `POST /webhooks/resend` authenticates Resend's Svix-signed delivery, bounce, and complaint callbacks, applies each one idempotently on its provider event id and in provider-clock order, and renders **Delivery delayed**, **Nightloch cannot reach this Partner**, or **Invitation not delivered** to authorized viewers alone. Every reachability change appends its own Activity entry, so a recovery clears the warning without erasing the failure, and none of it suspends a power, rolls back a mutation, pauses Cooling-off, or changes CanLoosen. Activity is now readable at `GET /activity` and `GET /activity/{protectee_id}`, projected through the viewer's own window: a Protectee reads their complete chronology while their User exists, a Partner reads from the exact instant of their own acceptance — ties inside that second settled by insertion order, so the invitation that created the Partnership stays outside — and a former Partner reads nothing but the receipt they keep. Re-partnering the same two Users opens a new window over a new durable record and never discloses the gap, reciprocal directions keep separate windows, and every other reference answers the one unavailable page. #15 makes a long Activity usable without widening it: newest-first keyset pagination over a fixed page size, category, actor, and date-range filters applied strictly inside the viewer's own window, no free-text search and no export, typed expandable details that translate every stored field and withhold any instant from before a Partner's window, reachability rendered as its own category apart from lifecycle prose, prose that states its consequence, and Nightloch's completed timers linking back to the Protectee request they carried out. #16 adds the deletion hook the later account-deletion slice calls: one transactional `DeleteUser` that ends every remaining Partnership in both directions, removes the deleted User's own chronology, Roster access, and open invitations, and replaces their verified email with **Deleted User** everywhere a retained view reads it — another Protectee's Activity keeps the authorized action and exact instant with a pseudonymized actor, a former Partner keeps a receipt carrying only Deleted User, deletion as the cause, and the exact time, and a deleted User holds no session and no acceptable invitation. It is idempotent, erases no event owned by a surviving Protectee, leaves active, ended, reciprocal, and repeated-Partnership authorization unchanged, and treats mail as external history — the delivery ledger is left intact and no delivered message is claimed to be recallable. #17 closes 3C and the Slice 3 gate adversarially. The whole perimeter is asserted from every side at once — Protectee, invitee, current Partner, another current Partner, former Partner, unrelated signed-in User, and unauthenticated visitor, across invitations, Roster, Activity, receipts, and delivery state. CanLoosen and CanViewLogs are table-tested deny-closed over pending, active, Cooling-off, ended, deleted, malformed, unknown, unrelated, reciprocal, and repeated relationship states. Terminal invitation actions and every ending path race each other — accept against the expiry sweep, concurrent requests, a Partner's ending against the Protectee's request, deletion against an ending — and settle on exactly one transition, one Activity entry, and one recipient-job set. Due timers and queued mail resume after a restart, and across two overlapping processes, without a duplicate message or a skipped lifecycle change. Every canonical message is contract-tested for its recipients, reply routing, UTC stamps, links, HTML meaning, and Roster non-disclosure. Every state-changing route is authenticated, CSRF protected, and consequence confirmed, and no link Nightloch has ever emailed mutates anything when followed by anyone. One end-to-end demo walks invitation, acceptance, a multi-Partner Roster, concurrent Cooling-off, a reachability warning and its recovery, an ended receipt, Activity browsing, and cross-User isolation. Three defects the gate found are fixed: an unknown or deleted identity no longer reads as an unpartnered Protectee who is free to loosen; Cooling-off mail is keyed on its own Activity entry, so a request cancelled and made again inside one second sends both signals instead of colliding; and provider acceptance of a later message no longer quiets a standing reachability warning — only a reported delivery clears one. **Slice 3 is complete.** |
| 4 WG, AGH, Devices, Policy, … | **In progress** — see #33. `/health` is a set of named self-governing checks (#34), and the `nightloch-wg` root helper and its sudoers grant are in `deploy/` (#35): three allowlisted subcommands, every argument validated before anything is invoked, membership rendered into the interface configuration before it reaches the running interface, and `show` reporting handshake data without disclosing the interface's private key. The app reaches the tunnel through sudo to that one path — [ADR 0004](./docs/adr/0004-reach-the-tunnel-through-a-root-helper.md), which is why `NoNewPrivileges` is off on the unit. #36 adds the enforcement module: `internal/enforcement` owns tunnel address allocation out of `10.66.0.0/24` with the host permanently reserved, Device records that hold a public key and one address and never a private key, and the canonical Device configuration as a pure function (full tunnel, DNS at the tunnel address, `PersistentKeepalive = 25`, published tunnel hostname as the endpoint). `nightloch mint-device PROTECTEE-EMAIL DEVICE-NAME` mints a Device and prints its configuration once; `nightloch devices` lists Devices against tunnel membership. WireGuard is an injected `Tunnel` interface with a recording fake, so the module tests without a droplet, and the helper's three exit codes stay three distinct errors rather than an empty Device list. #37 adds removal and address quarantine: `nightloch remove-device PROTECTEE-EMAIL DEVICE-NAME` ends the record and withdraws the tunnel peer in the mirror of minting's ordering, and the Device's tunnel address goes into quarantine rather than back into the pool. A tunnel address is the principal AdGuard Home keys client rules and query-log rows on, so quarantine is what makes the per-Protectee log isolation of a later slice sound rather than hopeful. The window runs from the removal instant for the query-log retention plus a day — strictly longer, asserted as its own test — and only `removed_at` is recorded: the expiry is derived, so it cannot drift from the retention in force, and nothing sweeps ([ADR 0005](./docs/adr/0005-derive-tunnel-address-quarantine-from-the-removal-instant.md)). `QueryLogRetention` lives beside the allocation rule, and the AdGuard Home slice must configure retention from it rather than restate it. Device-table uniqueness on address and name is now scoped to live Devices, so a replacement phone may carry the removed one's name the same day while its address waits; a public key is never reissued. Removing an already-removed Device is refused and never reaches the helper a second time or restarts the clock, a tunnel that will not withdraw the peer leaves the record live, and minting refused for exhaustion names the remedy the pool actually has — remove a Device, or wait until a stated instant. `nightloch devices` reports quarantined addresses alongside Devices and unaccounted peers. Everything downstream — AGH, enroll copy, enforcement health facts — is not started. |
| 5+ Enrollment allowlist, Curfews, … | Not started — follow plan |

## Commands

```bash
go test ./...
go build -o bin/nightloch ./cmd/nightloch
# Infra: see docs/ops/SLICE-1-RUNBOOK.md and docs/SECRETS.md
# Do not tofu apply without explicit user approval (creates paid droplet + DNS).
```

## Hard rules

1. **Deny-closed on Policy edits** — invalid or unknown state does not silently permit a Protectee loosen (`SubjectMayApplyEdit` / future Protectee-named APIs).
2. **Fail modes** — VPN off → unprotected; server down with tunnel up → client blackhole (not “fail open”). Document in Device enroll UI when built.
3. **AGH** — global Baseline lists + `$client` rules; never `filtering_enabled: false` to “turn off baseline”; curfews/extra blocks use `$important` so allows don’t lift them.
4. **No open VPN** — Device enrollment is gated (Operator enrollment allowlist in v1).
5. **Secrets** — never commit tokens, `terraform.tfvars`, or state. Prefer Bitwarden SM (`docs/BITWARDEN_SECRETS_SETUP.md`); never paste secrets into chat.
6. **Cloud/NAT** — no `tofu apply`, no live NAT/iptables without explicit user go-ahead (`docs/ops/NAT-CHECKLIST.md`).
7. **Scope** — v1 has no passes, no TLS MITM/search terms, no custom mobile apps, no enforcement on stephens.page.

## When changing the domain

Update [CONTEXT.md](./CONTEXT.md) when terms are resolved. ADRs only for hard-to-reverse, surprising, trade-off decisions (`docs/adr/` when needed). Prefer linking freeze docs over forking new “source of truth” files.

## Agent skills

### Issue tracker

Issues and specs are tracked in this repository’s GitHub Issues using the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Triage uses the canonical `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, and `wontfix` labels. See `docs/agents/triage-labels.md`.

### Domain docs

This is a single-context repository with `CONTEXT.md` at the root and repository-wide ADRs under `docs/adr/`. See `docs/agents/domain.md`.
