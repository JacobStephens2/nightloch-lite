# Nightloch Lite — Adversarial Review of `plan.md`

**Reviewing:** [plan.md](./plan.md) (as of 2026-07-28), against [shared-understanding.md](./shared-understanding.md) and [DECISIONS.md](./DECISIONS.md)
**Posture:** Adversarial. This document looks for what breaks, not what works.
**Status:** Review only. Nothing here changes a locked decision; it argues that some of them should be reopened before Slice 0.

---

## Method, and why it matters

Claims in this review about third-party behavior were verified against primary sources before being written down, not recalled:

- **AdGuard Home v0.107.78** — read from source at that tag (filtering engine, HTTP API handlers, query log search, client storage) plus `openapi/openapi.yaml` and the CHANGELOG.
- **Android DNS precedence** — AOSP `DnsManager.sendDnsConfigurationForNetwork()`, `ConnectivityService.handlePerNetworkPrivateDnsConfig`, `Vpn.java`.
- **WireGuard client failure behavior** — `wireguard-android` `GoBackend.java`, `wireguard-apple` `PacketTunnelSettingsGenerator.swift`, `wireguard-go/device/constants.go`.
- **Chrome / Firefox encrypted DNS** — Chromium `doh_provider_entry.h`, `stub_resolver_config_reader.cc`; Gecko `DoHHeuristics.sys.mjs`.
- **DigitalOcean / Caddy / Cloudflare** — vendor docs and the `terraform-provider-digitalocean` droplet resource source.

Full citations are in the [appendix](#appendix--verified-facts-and-citations). Where a source was ambiguous, the finding says so explicitly rather than asserting. **Four findings below contradict statements the plan and shared-understanding treat as settled fact.** That is the main reason this review is worth reading.

---

## Verdict

The plan is well-organized, honest about its scope, and correctly structured as vertical slices. Its central sequencing instinct — get packets right (Slice 4) before productizing peers (Slice 5) — is right, and is the thing most plans of this shape get wrong.

But it plans the *construction* of a system whose *security model* has not been designed. Specifically:

1. **The stated fail mode is factually wrong.** The system does not fail open when the path is down. It fails closed, in the worst possible way: the device appears to have no internet, and the only remedy available to the user is to disable the VPN. See [F1](#f1).
2. **The one compensating control for fail-open cannot be built under the plan's own constraints.** Quiet-peer detection cannot distinguish "user disabled the VPN" from "phone was asleep," because the only available signal is a WireGuard handshake and custom mobile apps are an explicit non-goal. See [F2](#f2).
3. **The always-on baseline — the highest-stakes protection in the product — has no tamper detection at all**, because quiet-peer alerting is scoped to active curfews. See [F3](#f3).
4. **The asymmetry the product exists to provide is trivially defeated by the protected user**, who can invite a sock-puppet partner or unilaterally end the real partner edge — no alert on the first, no delay on the second. See [F5](#f5), [F6](#f6).
5. **AdGuard Home cannot express the per-user policy model the plan assumes.** Filter list subscriptions are global; the only per-client lever is an all-or-nothing boolean whose use would disable that user's curfews along with their baseline. See [F10](#f10).

Findings 1–3 mean the integrity story does not hold. Finding 4 means the access-control story does not hold. Finding 5 means Slices 5–6, as written, cannot be implemented against the chosen enforcer.

None of this is fatal to the product. All of it is far cheaper to fix now, in a document, than in Slice 6.

**Recommendation: do not start Slice 0 until the questions in [Section 8](#8-questions-that-must-be-answered-before-slice-0) are answered.** Slices 0–3 are safe to build regardless; the design work should run in parallel with them, not wait for Slice 6 to surface it.

---

## What the plan gets right

Worth stating plainly, because everything after this is negative:

- **The slice structure is genuinely vertical.** "Done means demoable" is the correct discipline, and most slices honor it.
- **Slice 4 before Slice 5** — proving packet flow with an operator-minted peer before automating peer minting — is correct.
- **The honesty about DNS-only enforcement** (no MITM, no search terms) is stated in three separate documents and never quietly walked back. That discipline is rare and worth protecting.
- **Ops gates on NAT and cloud spend** are explicit and survive into the slice bodies, not just the preamble.
- **OpenTofu from day one** avoids the usual "we'll codify it later" debt.

The critique below is not that the plan is careless. It is that a *build* plan is standing in for a *design* document that was never written.

---

## Severity index

| # | Finding | Severity | Slice |
|---|---|---|---|
| [F1](#f1) | "Fail open" is false; both clients blackhole when the server is down | **Blocker** | 4, 8, 11 |
| [F2](#f2) | Quiet-peer alert cannot distinguish VPN-off from device-asleep | **Blocker** | 8 |
| [F3](#f3) | Always-on baseline has zero tamper detection | **Blocker** | 8 |
| [F4](#f4) | Single droplet is a single point of failure for all internet access | High | 1, 11 |
| [F5](#f5) | Sock-puppet partner: no alert when a partner is *added* | **Blocker** | 3 |
| [F6](#f6) | Protected user can unilaterally end the partner edge, with no delay | **Blocker** | 3 |
| [F7](#f7) | Zero-partner state is undefined — who may loosen? | **Blocker** | 6 |
| [F8](#f8) | Self-service account deletion is an undocumented total bypass | High | 3 |
| [F9](#f9) | Open signup + self-service enrollment = open VPN exit for strangers | **Blocker** | 5 |
| [F10](#f10) | AGH per-client baseline disable also disables that user's curfews | **Blocker** | 5, 6 |
| [F11](#f11) | `set_rules` is a whole-blob write with no concurrency control | High | 6 |
| [F12](#f12) | Query log `search` is substring — cross-user DNS history leak | High | 7 |
| [F13](#f13) | WireGuard IP recycling leaks the prior tenant's query log | High | 5, 7 |
| [F14](#f14) | Per-client filter lists don't exist; `$client` won't scale to a 150k-domain list | High | 5, 6 |
| [F15](#f15) | Encrypted DNS (DoT/DoH) bypass is entirely unaddressed | **Blocker** | 4 |
| [F16](#f16) | Hardcoded plaintext resolvers are not intercepted | High | 4 |
| [F17](#f17) | 5-minute reconcile is coarser than curfew boundaries | High | 6 |
| [F18](#f18) | Tighten-vs-loosen gets one line for the hardest problem in the product | High | 6 |
| [F19](#f19) | IPv6 disabled in-tunnel may leak routing on macOS | Medium | 4 |
| [F20](#f20) | Slice 1's TLS goal and Slice 11's cutover goal are mutually exclusive | High | 1, 11 |
| [F21](#f21) | Auth tokens likely unhashed, then copied to a weaker host by design | High | 2, 10 |
| [F22](#f22) | Backup pull gives stephens.page an SSH key into the enforcement box | Medium | 10 |
| [F23](#f23) | No external dead-man's-switch — nothing watches the watcher | High | 8 |
| [F24](#f24) | Backup cadence and snapshot-consistency mechanism unspecified | Medium | 10 |
| [F25](#f25) | Migrate-on-boot plus auto-deploy can crash-loop the alerting system | Medium | 9 |
| [F26](#f26) | Security work is concentrated in Slice 11, after everything is live | High | 3, 11 |
| [F27](#f27) | OISD false-positive allow path is marked "optional" | High | 6, 11 |
| [F28](#f28) | Audit log arrives in Slice 8, after Slices 3 and 6 mutate policy | Medium | 3, 6, 8 |
| [F29](#f29) | Alert deliverability is the whole integrity model, with no bounce handling | Medium | 8 |
| [F30](#f30) | Restore is "documented," not executed — violates the plan's own rule | Medium | 10 |
| [F31](#f31) | Instagram is a poor first curfew target for DNS-level blocking | Medium | 6 |
| [F32](#f32) | No staging environment; NAT changes risk SSH lockout | Medium | 4 |
| [F33](#f33) | Guardian relationships (a second Protectee) need a different consent model | Medium | 3, 11 |

---

## 1. The integrity story does not hold

This is the most important section. The product's honest pitch is: *"we block at the DNS layer, you can defeat it by turning off the VPN, and your partner gets an email when you do."* The third clause is what makes the first two acceptable. It does not currently work.

### F1

**"Fail open" is false. Both clients blackhole when the server is down.** — **Blocker**

`shared-understanding.md` states: *"**No kill-switch**; VPN off or path down → fail **open**."* The first half is true. The second half is not.

The Android app makes this an explicit, commented decision in `GoBackend.java`:

```java
// "Kill-switch" semantics
if (!(sawDefaultRoute && config.getPeers().size() == 1)) {
    builder.allowFamily(OsConstants.AF_INET);
    builder.allowFamily(OsConstants.AF_INET6);
}
```

`VpnService.Builder.allowFamily` is what permits traffic to *"fall-through to the underlying network."* With exactly one peer and `AllowedIPs = 0.0.0.0/0` — precisely this design — the app **deliberately withholds it**. Traffic is routed into the tun device, encrypted, and fired at a dead endpoint. DNS blackholes with it, because `addDnsServer` is on the tunnel.

macOS reaches the same outcome by a different route: `PacketTunnelSettingsGenerator.swift` maps each `AllowedIPs` entry into `ipv4Settings.includedRoutes`, so `0.0.0.0/0` becomes a default route on the utun and everything enters the tunnel.

Compounding it, Android registers the VPN network with `NET_CAPABILITY_INTERNET` and `requiresInternetValidation = false`. The OS treats the tunnel as **permanently validated**: no connectivity check, no re-scoring, no failover, no warning. And WireGuard is stateless by design — the client just retries handshakes every ~5s forever (`RekeyTimeout = 5s`, `RekeyAttemptTime = 90s`), with no teardown signal and no error surfaced.

**Why this is a Blocker and not a documentation nit:**

The user's experience of a droplet reboot, an AGH crash, an OOM, or a bad deploy is *"my phone's internet is broken."* The only remedy available to them is to **turn off the VPN** — which is exactly the action the product is designed to detect and discourage. Every outage actively trains the bypass, and it trains it as a *legitimate troubleshooting step* rather than a lapse. Over a dogfood month, this is the single most likely path to the product being abandoned.

There is a second-order harm: it corrupts the alert signal. Every outage produces a burst of quiet-peer alerts that are *not* evasion, teaching the partner that these emails are noise. See [F2](#f2).

**Recommendations:**

1. **Correct the claim in `shared-understanding.md` and `DECISIONS.md` (Q6).** Replace "path down → fail open" with: *"VPN off → unprotected (fail open). Server unreachable → device loses all connectivity until the user disables the VPN (fail closed)."* These are opposite behaviors and must not share a row in a table.
2. **Treat droplet availability as a P0 product requirement, not an ops nicety.** The design has no graceful degradation; uptime *is* the graceful degradation.
3. **Tell the user this in the UI, before they enroll a device.** A device-enroll screen that says "if the server goes down, your device will lose internet until you turn this off — here's how" converts a scary failure into an expected one, and makes the resulting VPN-off event legible to the partner rather than suspicious.
4. **Do not describe the blackhole as a security feature.** It is an emergent consequence of static routing. It does not survive the tunnel being stopped, the app being killed, or the extension crashing — all of which restore cleartext routing instantly.
5. Add to Slice 4's demo: *reboot the droplet with the phone on the tunnel; observe and document the actual client behavior and the recovery steps, per platform.* This takes ten minutes and is currently absent.

**Platform traps to document while you're there:** on Android, if the user has enabled Always-on VPN with "Block connections without VPN," toggling the tunnel off is not sufficient — the system switch must also be disabled. On macOS, if On-Demand is enabled, plain "Deactivate" is silently undone; the user must choose "Disable On-Demand and Deactivate."

---

### F2

**The quiet-peer alert cannot distinguish "VPN disabled" from "device asleep."** — **Blocker**

Slice 8 specifies: *"WG peer quiet ≥ 1 hour during active curfew (per peer per night dedupe)."* The only server-side signal is `wg show`'s last-handshake timestamp. WireGuard performs a handshake when there is traffic to send, or every `PersistentKeepalive` interval if configured. Slice 4's client config spec — endpoint, DNS, AllowedIPs — **does not mention `PersistentKeepalive` at all**.

Without it, an idle phone produces no handshakes. A phone that is asleep from 23:00 to 07:00 — the normal case, inside the exact window the curfew covers — looks identical to a phone with the VPN switched off.

Setting `PersistentKeepalive = 25` helps but does not solve it. Android Doze can suspend the app's network activity during extended idle; iOS/macOS sleep states do the same. And it costs battery, on a device where a battery complaint is itself a reason to uninstall the VPN.

The deeper problem is architectural: **you cannot distinguish "device off" from "protection off" from the server side alone.** Doing so requires an agent on the device — and *"custom iOS / macOS Nightloch apps"* is an explicit v1 non-goal. The compensating control for fail-open is therefore not implementable under the chosen constraints.

**Why this is a Blocker:** if the alert fires most nights and is almost always benign, the partner learns to ignore it within a week. An ignored alert is worse than no alert, because the system reports itself as protected.

**Recommendations, in order of preference:**

1. **Reframe from alarm to coverage report.** Instead of "peer went quiet, something may be wrong," compute and show *coverage*: "of the 12 curfew hours last night, the device was on the tunnel for 4h20m." A partner reading a weekly coverage trend will notice a drop from 90% to 20%. A partner reading nightly false alarms will notice nothing. **This conflicts with DECISIONS Q13 ("No digests in v1") — that decision should be reopened**, because the digest is the only form in which this signal is usable.
2. **If you keep the real-time alert, raise the bar substantially.** Require a *sustained* gap during a window in which the device was demonstrably reachable at some point — e.g. handshake seen at 20:30, then silence until 06:00 with no re-handshake, is stronger evidence than silence from 20:00 onward. Still weak, but it excludes "phone was off all evening."
3. **Set `PersistentKeepalive = 25` in every generated client config regardless** (Slice 4/5), and record the tradeoff. It also fixes NAT mapping expiry on mobile networks, which you want anyway.
4. **Instrument before alerting.** In Slice 4, before building any alert, log handshake timestamps for a week of real personal use and look at the actual distribution. The correct threshold — if one exists — is an empirical question, and right now "1 hour" is a guess presented as a decision.

---

### F3

**The always-on baseline has no tamper detection whatsoever.** — **Blocker**

Quiet-peer alerting is scoped, in both `shared-understanding.md` and Slice 8, to *"during an active curfew."*

The baseline OISD NSFW list is *always on* and is the product's primary protection. The Instagram curfew is, by the owner's own framing, the secondary feature. So the protection that matters most has **zero** tamper detection, while the protection that matters less has (nominally) full coverage.

The bypass is one tap, at any time outside 20:00–08:00, and generates no signal of any kind:

> Turn off the VPN → the baseline is gone → no alert, because no curfew is active.

This is not a corner case. It is the obvious and most likely evasion path, available for twelve hours a day, and the system is blind to it by design.

I suspect this scoping was inherited from thinking of curfews as "the feature" — the quiet-peer alert was designed alongside curfews and simply never re-examined against the baseline. But it inverts the product's priorities.

**Recommendation:** **Protection coverage must be measured whenever the user has any enrolled device, not only during curfew windows.** Combined with [F2](#f2), the concrete shape is:

- Track tunnel coverage continuously per peer.
- Report it as a rolling summary to partners (daily or weekly).
- Alert in real time only on unambiguous, high-signal events — a device revoked, a partner edge ended, a policy loosened, log sharing disabled — where the server has direct evidence rather than an inference from absence.

This inverts the current design: **alert on actions, report on coverage.** Actions are observable and unambiguous; coverage is statistical and belongs in a trend, not an alarm.

---

### F4

**One droplet is a single point of failure for all internet access on every enrolled device.** — High

A full tunnel makes the droplet load-bearing for connectivity, not just for filtering. Given [F1](#f1), any of the following takes the user's phone offline until they manually intervene:

- DigitalOcean maintenance or a host migration
- Kernel/package upgrade requiring reboot (unattended-upgrades on Ubuntu will do this on its own)
- AGH restart during a blocklist refresh (memory spikes to roughly 2× steady state while the new engine is built alongside the old — the documented OOM path on small droplets)
- A Slice 9 deploy that fails to start
- Caddy renewing a cert badly, WireGuard config reload, `tofu apply` touching the firewall

Note the interaction with [F25](#f25): a bad migration crash-loops the Go app. The Go app is not in the DNS data path, so connectivity survives — but *alerting* dies silently, which is the worse failure for this product.

**Recommendations:**

1. Add to Slice 1: **disable automatic reboots** from unattended-upgrades; schedule patching deliberately, ideally outside curfew hours.
2. Add to Slice 4: **AGH memory ceiling and restart policy.** Set `filters_update_interval` conservatively, keep the subscribed list count small, and consider a systemd `MemoryMax` with `Restart=always` so a refresh spike restarts AGH in seconds instead of OOM-killing something else.
3. Add swap to cloud-init. A 2 GB droplet running AGH + Caddy + Go + WireGuard with no swap has no margin for the refresh spike.
4. Accept the SPOF (correct for v1 — HA is an explicit non-goal) but **monitor it externally**. See [F23](#f23).

---

## 2. The access-control model assumes a cooperative adversary

The entire product rests on one sentence: *"Restricted user may **tighten**; only partner may **loosen**."* The plan implements this as an authorization check on curfew mutations (`CanLoosen(actor, subject)`, Slice 3/6).

That check is correct and irrelevant. **The threat model is not "the protected user submits an unauthorized POST." It is "the protected user, in a moment of wanting to bypass, uses only authorized actions to arrive at an unrestricted state."** The plan has no analysis of that path, and several such paths are wide open.

The findings in this section share a root cause: the plan treats partner edges as *relationship metadata* rather than as *the security perimeter*. Every operation that changes the set of people who can restrain a user is a privilege escalation and needs to be modeled as one.

### F5

**Sock-puppet partner: nothing alerts when a partner is added.** — **Blocker**

Slice 8's alert list is: curfew/baseline loosened, device enrolled/revoked, log sharing disabled, AGH unreachable, reconcile failure, drift, quiet peer. **"Partner added" is not on it.** Neither is it in `shared-understanding.md`'s integrity-email list.

Combined with open signup (Q26: *"No global role at signup. An account is a person"*) and the rule that *"Partners may **not** manage the user's other partners"*:

1. The protected user creates a second account with an email they control (an alias, a throwaway — trivially, Gmail plus-addressing).
2. From their main account, they invite that address as a partner. **No alert to the existing real partner** — they are explicitly barred from seeing or managing other partners.
3. They accept from the second inbox.
4. That sock puppet now has full loosen powers: shorten the curfew, remove baseline domains, revoke devices.

The asymmetry is gone, and the real partner has received exactly zero emails. The user never took an unauthorized action.

Worse, the loosen alert *will* fire — and it will name the sock puppet as the actor. If the real partner does not know that account is not a real person, the alert reads as legitimate partner activity. **The alert system actively launders the bypass.**

**Recommendations:**

1. **Add "partner added / invite sent / invite accepted" to the integrity alert list**, delivered to *all existing partners* of the subject. This is the single highest-value alert in the system and it is currently missing.
2. **Reconsider the "partners may not see other partners" rule.** It was presumably meant to prevent partners from fighting over a user. But a partner who cannot see that a second partner exists cannot detect the sock-puppet attack at all. At minimum, partners should see *the roster* (who else protects this person, and since when) even if they cannot modify it. Transparency among partners is a feature in every comparable accountability product.
3. **Consider requiring an existing partner to approve a new partner**, once the user has at least one. This makes the partner set monotonic-by-consent and closes the attack completely. It is a meaningful UX cost and should be an explicit product decision, not an omission.

---

### F6

**The protected user can unilaterally end the partner edge, with no delay.** — **Blocker**

`shared-understanding.md`: *"Either party may end the relationship; notify the other."*

This is the cleanest bypass in the system, and it needs no second account:

> End partner edge → (now unrestrained, or the sole partner is gone) → loosen everything → re-invite the partner later.

The partner receives an email — *after* the fact, when they have already lost the ability to intervene. Notification without a delay is not a control; it is a receipt.

Symmetric termination is right for a *relationship*, and wrong for a *restraint*. Ending a restraint from the restrained side is the archetypal action that should be slow.

**Recommendation: introduce a cooling-off period on user-initiated edge termination.** Concretely:

- Partner-initiated termination: **immediate.** They are declining to serve; never trap them.
- User-initiated termination: **enters a pending state**, effective after a fixed delay (48–72 hours is the industry norm). The partner is emailed at request time, retains full powers throughout, and can see the pending expiry.
- **All policy stays enforced during the cooling-off period.** This is the point.
- The user may cancel their own request at any time.

The same delay should apply to any other action that shrinks the restraint perimeter: removing the last device, disabling the baseline, deleting the account ([F8](#f8)).

This is the highest-leverage single change in this review. It converts an instant bypass into a 72-hour conversation, which is the actual mechanism by which accountability software works.

---

### F7

**What happens with zero partners is undefined — and both possible answers are bad.** — **Blocker**

The rule is *"only partner may loosen."* The plan never says what happens when a user has **no** partners. A new account has none (Q37: *"devices/partners empty"*), and after [F6](#f6) any user can return to that state.

Two possible semantics, neither specified, both broken:

**(a) No partners → nobody can loosen.** The user is permanently locked into whatever they configured. Set a 24-hour block on a domain you need for work, discover the mistake, and there is no recovery path short of operator intervention on the database. For a solo-dogfooding owner this is a foot-gun; for anyone else it is a support burden with no support channel.

**(b) No partners → the user can loosen their own.** Then the bypass is: remove all partners ([F6](#f6)) → loosen freely. Every restraint in the product becomes advisory.

**This is a genuine hole at the exact center of the product, and the plan cannot be implemented without picking one.** Slice 6's demo criteria ("Protected user cannot shorten window; partner can") silently assumes a partner exists.

**Recommendation:** pick **(b)** — self-loosening is allowed when unpartnered — and neutralize the bypass with [F6](#f6)'s cooling-off period plus [F5](#f5)'s alerting. Rationale:

- (a) creates an unrecoverable state, which will eventually require exactly the kind of SSH surgery Slice 11 defines as failure.
- Under (b) with a 72-hour delay, "remove all partners to loosen" costs three days *during which the partner is notified and can intervene* — which is the correct outcome. The restraint's strength comes from the delay, not from a permanent lock.
- It makes the unpartnered state coherent: the product is a self-imposed commitment device that gets teeth when you add a partner.

Also specify: **onboarding must push toward adding a partner.** A user who enrolls a device and never invites anyone has bought a mildly inconvenient DNS filter. Nothing in Slices 2–5 prompts them.

---

### F8

**Self-service account deletion is an unexamined total bypass.** — High

`shared-understanding.md` says partners *"may not... delete the user's account."* It never says whether the **user** can. If they can — the default assumption for any web app, and required by GDPR-style expectations — then account deletion is [F6](#f6) with no notification requirement and no partial state to reason about.

It also raises unhandled questions the plan does not touch: what happens to enrolled WireGuard peers, allocated tunnel IPs, AGH client entries, and the partner edges where this user was the *protector* of someone else? A dangling peer whose owner no longer exists is a live tunnel with no policy attached to it — the worst possible orphan in this system.

**Recommendations:**

1. State the policy explicitly in `shared-understanding.md`.
2. Route deletion through the same cooling-off machinery as [F6](#f6): request → notify all partners → 72 hours → execute.
3. Specify teardown as a single transaction: revoke all peers (remove from `wg0` *and* from AGH clients), release IPs to a quarantine pool ([F13](#f13)), end all edges in both directions with notification, then delete.
4. Add a test for it in Slice 5. Orphaned peers are the kind of bug found in production, at the worst time.

---

### F9

**Open signup plus self-service device enrollment means you are operating a free VPN exit for strangers.** — **Blocker**

This one is not about the protected user. Compose two locked decisions:

- **Q26 / Slice 2:** open signup. Anyone with an email address gets an account.
- **Slice 5:** *"Signed-in user adds 'Android' → installs config without SSH → tunnel works."* Any account holder mints a WireGuard peer with `AllowedIPs = 0.0.0.0/0`.

Therefore **anyone on the internet who finds nightloch.app can obtain full-tunnel VPN egress from your droplet.** All of their traffic exits your IP address, on your DigitalOcean account, under your name.

The consequences are not primarily financial. Bandwidth is a non-issue (2 TiB included, outbound-only, $0.01/GiB overage — a 500 GiB overrun costs $5). The real exposure is:

- **Abuse attribution.** Spam, credential stuffing, scanning, fraud, or worse egresses from an IP registered to you. DigitalOcean's response to an abuse report is account-level.
- **IP reputation.** A shared exit IP gets onto blocklists quickly, which degrades the service for the actual users, including you.
- **Resource exhaustion.** Each peer is an AGH client entry and a set of rules in a *global*, single-blob rule list ([F11](#f11), [F14](#f14)). A few hundred signups is a denial of service against your own control plane.

Slice 11's security pass lists "no open resolver" but not "no open VPN." The DNS side was considered; the tunnel side was not.

**Recommendation: gate device enrollment, not signup.** This preserves everything Q23–24 were trying to fix (open signup is what makes the partner graph work — invitees must be able to create accounts freely) while closing the hole:

- Signup stays open. Accounts, partner invites, and accepting invites need no gate.
- **Device enrollment requires authorization**: an operator allowlist for v1 (an env var of permitted emails is sufficient and takes an hour), or an entitlement granted when an existing enrolled user vouches for you.
- Unauthorized accounts see "device enrollment is invite-only during beta."

This must land **in Slice 5, not Slice 11**, because Slice 5 is the moment the hole opens. Add to Slice 5's out-of-scope-until-gated list, and add "no unauthorized user can mint a peer" to Slice 5's demo criteria.

**Also add rate limiting to the magic-link endpoint's *email* dimension specifically.** An open link-request endpoint is a mail-bomb amplifier: an attacker submits arbitrary victim addresses and your service sends the mail. Because the From is `nightloch@stephens.page` ([F29](#f29)), the reputational damage lands on your **personal** mail domain, not a product domain you could afford to burn. Slice 2 lists "Rate limit: per email + per IP," which is right — this note is to flag *why* it is load-bearing and must not slip.

---

## 3. AdGuard Home cannot express the policy model the plan assumes

Slices 5 and 6 describe a per-user policy engine: *"Apply **per-user baseline** desired state for that client (OISD + custom adds)"*, *"Desired rules per client from baseline + active curfews"*, and from `shared-understanding.md`, *"only **partner** may remove/disable baseline."*

AdGuard Home does not work this way. The findings here were read from the v0.107.78 source, and they change the shape of Slices 5–6.

### F10

**Disabling the baseline for one user also disables that user's curfews.** — **Blocker**

The plan requires per-user control over the baseline. AGH offers exactly one per-client filtering lever: the pair `use_global_settings` / `filtering_enabled`. There is no per-client filter-list field on the client object at all.

And `filtering_enabled` is all-or-nothing. `matchHost` returns immediately when it is false — which disables **both** the global list subscriptions *and* the global custom rules for that client. Since the curfew rules must live in the global rule list (there is nowhere else to put them), the interaction is:

> Partner disables the baseline for user A → A's curfews stop being enforced too.

A partner performing an intended *narrow* loosening (turn off the porn list) silently performs a *total* loosening (turn off everything, including the Instagram curfew). The alert email will say "baseline disabled." It will be wrong about the blast radius.

**This is a correctness bug that would ship silently**, because the demo in Slice 6 tests baseline and curfew independently and would pass.

**Recommendation: drop the whole-baseline off-switch and replace it with per-domain allowlisting.** This is better product design *and* the only thing AGH implements cleanly:

- Keep OISD as a **global subscription**, enabled.
- Keep `filtering_enabled: true` for **every** client, always. Never touch it.
- "Partner removes a domain from the baseline" becomes a per-client allowlist exception in the global rule list: `@@||example.com^$client=10.66.0.2`. Narrow, auditable, per-user, and expressible.
- **There is no supported way to except an entire subscribed list for one client while keeping other rules active.** If a blanket per-user off-switch is genuinely required, the only clean implementation is one AGH instance per user — which is disproportionate for a 1–3 user v1 and should be rejected.

Amend `shared-understanding.md`'s policy table accordingly: *"Baseline edits: user may add domains; only partner may allowlist a specific domain. The baseline cannot be disabled wholesale."* Notably, a blanket off-switch is a bad affordance for an accountability tool regardless of the technical constraint — this finding forces a better design.

This also resolves [F27](#f27): the per-domain allow path is not an optional nicety, it is the *only* mechanism, so it must exist in Slice 6.

---

### F11

**`set_rules` is a whole-blob write with last-writer-wins semantics.** — High

`POST /control/filtering/set_rules` replaces the entire custom rule list: the handler is literally `d.conf.UserRules = req.Rules`. There is no append, no patch, no ETag, no `If-Match` — zero occurrences of optimistic concurrency in the codebase or the OpenAPI spec.

Slice 6 says *"Idempotent AGH API updates"* and *"reconciler is source of truth for AGH client rules."* Idempotence is necessary but not sufficient. The failure modes:

1. **Concurrent writers lose rules.** Slice 6 runs a 5-minute reconcile *and* kicks on write. Two overlapping read-modify-write cycles silently drop whichever rules the loser had added.
2. **The operator's AGH admin UI is a second writer.** `shared-understanding.md` grants the operator the AGH admin UI at `agh.nightloch.app`. Any manual rule edit there is destroyed by the next reconcile, with no warning and no record. Conversely, a manual edit made between the reconciler's read and write destroys the reconciler's output.
3. **Any rule not in the reconciler's model is deleted.** This is correct behavior for a declarative reconciler, but it must be stated, because the natural operator instinct — "let me just add a quick block in the UI" — silently fails.

**Recommendations:**

1. **Single writer.** All `set_rules` calls go through one goroutine or one mutex. The 5-minute sweep and the write-kick must be the same serialized code path.
2. **SQLite is authoritative; the AGH rule list is a projection.** Never read rules back from AGH to compute desired state. Generate the full blob from the database every time.
3. **Emit a generated-file banner as rule #1**: `! GENERATED BY nightloch-lite — DO NOT EDIT. Changes here are destroyed on the next reconcile (≤5 min).`
4. **Read-back verification, not just write.** After writing, `GET /control/filtering/status` and diff `user_rules` against the intended blob. A mismatch is the "desired ≠ enforced drift" event Slice 8 wants and currently has no defined detection mechanism.
5. Document in the ops checklist that the AGH UI is **read-only in practice** for filtering rules.

Auth note for the client implementation: AGH has **no API tokens**. Every endpoint accepts HTTP Basic, and the same credential is full admin. Lite must hold admin credentials in its environment — there is no scoped-down option. Worth stating in the threat model, since it means app compromise implies enforcer compromise.

---

### F12

**The query log `search` parameter is a substring match — this is a cross-user DNS history leak.** — High

Slice 7: *"Lite proxies/filters AGH query log API by client IPs for a user."*

`GET /control/querylog` accepts `older_than`, `offset`, `limit`, `search`, `response_status`, `reason`. **There is no exact-match client parameter.** The non-strict search path is pure substring containment, OR'd across four fields:

```go
func ctDomainOrClientCaseNonStrict(term, asciiTerm, clientID, name, host, ip string) bool {
	return stringutil.ContainsFold(clientID, term) ||
		stringutil.ContainsFold(host, term) ||
		(asciiTerm != "" && stringutil.ContainsFold(host, asciiTerm)) ||
		stringutil.ContainsFold(ip, term) ||
		stringutil.ContainsFold(name, term)
}
```

**Concretely: `search=10.66.0.2` returns rows for `10.66.0.20` through `10.66.0.29`.** Upstream's own test asserts this behavior for a `203.0.113` prefix.

Given the plan's addressing scheme (`10.66.0.2+`, sequential), users 2 and 20–29 collide by construction. Under the log-sharing feature, **user 2's partner would see users 20–29's browsing history** — a category of data this product treats as maximally sensitive, shown to a person with no relationship to those users.

There is an undocumented exact-match escape hatch: wrapping the value in literal double quotes (`search="10.66.0.2"`) switches to equality. But (a) it is absent from the OpenAPI spec and therefore not a stable contract, and (b) even in strict mode the term is OR'd across host, client ID, IP, *and* client name — exact on value, but not scoped to a field.

**Recommendation: treat the AGH query log as an untrusted multi-tenant source.**

1. Use the quoted form as a bandwidth optimization.
2. **Re-filter every returned row server-side** against the expected client IP before it reaches a template. Never pass AGH rows through to a response without an explicit equality check on the client field.
3. Add a test that seeds AGH with entries for `10.66.0.2` and `10.66.0.20` and asserts user 2's view contains none of user 20's. This is a five-line test that prevents a serious privacy incident.
4. **Consider allocating tunnel IPs to avoid prefix collisions** as cheap defense-in-depth — e.g. start at `10.66.0.11` and skip any address that is a string-prefix of another in use. Ugly, but free.

While here: **AGH's default query log retention is 90 days, not 7.** `shared-understanding.md` and Slice 7 both state *"AGH retention ~7 days."* That is one of the five presets the API accepts, but it is not the default and will not be true unless explicitly configured. Set it deliberately in Slice 4 — a 90-day store of a user's full browsing history is a much larger liability than the documents assume, and it makes [F13](#f13) far worse.

---

### F13

**Recycling a tunnel IP hands the new owner's partner the previous owner's browsing history.** — High

Lite allocates client IPs (`10.66.0.2+`). AGH's query log is keyed by IP and retains for a configured window ([F12](#f12): default 90 days, plan assumes 7). Slice 5 supports device revocation; Slice 7 renders logs by IP.

So: device A (user 1) at `10.66.0.5` is revoked. Later, device B (user 2) is allocated `10.66.0.5`. User 2's log view — and user 2's partner's log view — now includes **user 1's DNS history** from before the reallocation, presented as user 2's.

The plan's *"Document AGH retention ~7 days"* is the only place retention appears, and it is treated as a UI caveat rather than a correctness constraint on IP allocation.

**Recommendations:**

1. **Never reuse a tunnel IP within the AGH retention window.** Allocate monotonically; on revocation, move the IP to a quarantine pool with a `reusable_after` timestamp of `now + retention + 1 day`. A /24 gives 253 addresses, which at dogfood scale will never exhaust.
2. **Bound every query-log read by the device's `created_at`.** Even with quarantine, never return rows older than the device record itself. This is one extra predicate and closes the hole independently of allocation policy.
3. Add the assertion to Slice 5's tests: revoke, reallocate, confirm the new owner sees nothing from before their enrollment.

---

### F14

**Per-client filter *lists* do not exist, and `$client` rules will not scale to a per-user 150k-domain baseline.** — High

Slice 5 says *"Apply **per-user baseline** desired state for that client (OISD + custom adds)."* If read literally — materialize the baseline per user — this is not viable. AGH's only per-rule client scoping is the `$client` modifier in the single global rule list. A per-user copy of a ~150k-domain list would mean 150k rules × N users in one blob, rewritten wholesale on every reconcile ([F11](#f11)).

The workable decomposition, which [F10](#f10) already implies:

| Layer | Mechanism | Scope |
|---|---|---|
| OISD NSFW baseline | Subscribed filter list | **Global** — same for everyone |
| Per-user baseline additions | `\|\|domain^$client=10.66.0.N` | Per-client rules |
| Per-user baseline exceptions | `@@\|\|domain^$client=10.66.0.N` | Per-client rules |
| Curfews | `\|\|domain^$client=10.66.0.N`, written/removed at boundaries | Per-client rules |

Total rule count stays in the tens per user. This works, and it should be written into the plan explicitly, because "per-user baseline" as currently phrased invites the wrong implementation.

Two constraints to encode in the AGH client abstraction:

- **`$client` accepts an IP, a CIDR, or a persistent client *name* — but not a ClientID.** Only the resolved name is passed into the match request; the AGH knowledge base confirms ClientIDs are unsupported, and there is a standing TODO in `internal/filtering/http.go` about it. **Key rules on the tunnel IP**, which lite already owns and allocates. (This makes [F13](#f13)'s quarantine rule doubly important: the IP is the security principal, not just an address.)
- **Client tags (`$ctag`) use a fixed predefined vocabulary** (`device_*`, `os_*`, `user_admin`/`user_regular`/`user_child`). Arbitrary tags are rejected, so tags cannot be used to model per-user policy groups.

**One genuinely useful discovery:** AGH has native **per-client blocked-services schedules** (backend v0.107.33, UI v0.107.37) with an IANA timezone and per-weekday ranges — which is nearly exactly the curfew feature, implemented for you. Two caveats before adopting it:

1. **It applies only to the fixed blocked-services catalogue** (~136 entries, app-level things like YouTube/Facebook/TikTok), not arbitrary domains. Instagram is very likely present — **verify before relying on it.**
2. **The schedule semantics are inverted.** `Schedule.Contains(now)` denotes a period of **inactivity** (a pause in blocking), not a period of blocking: `if !setts.BlockedServices.Schedule.Contains(time.Now()) { ApplyBlockedServicesList(...) }`. Getting this backwards produces a curfew that blocks Instagram from 08:00–20:00 and allows it all night — and the Slice 6 demo, if run in the evening, would appear to pass. Property-test the boundary in both directions.

If Instagram is in the catalogue, using the native schedule for it eliminates the boundary-write machinery in [F17](#f17) for the flagship curfew, and confines rule rewriting to arbitrary-domain curfews only. Worth an hour of investigation in Slice 4.

---

## 4. Enforcement holes in the DNS path

Slice 4 is titled "enforcement spine" and its demo is *"adult domain from list does not load."* That demo passes on a stock device today and still leaves the enforcement trivially and **silently** bypassable. Silence is the problem: a bypass that produces no alert is worse than one that does.

### F15

**Encrypted DNS bypass is not mentioned anywhere in any of the three documents.** — **Blocker**

Nothing in `plan.md`, `shared-understanding.md`, or `DECISIONS.md` mentions DoT, DoH, port 853, or Private DNS. This is the largest technical omission in the plan.

**The Android case is the serious one.** Verified through AOSP `DnsManager.sendDnsConfigurationForNetwork()`:

```java
paramsParcel.servers    = makeStrings(lp.getDnsServers());   // your 10.66.0.1
paramsParcel.tlsName    = strictMode ? privateDnsCfg.hostname : "";
paramsParcel.tlsServers = strictMode ? IPs of the user's chosen DoT provider
                        : useTls    ? paramsParcel.servers   // opportunistic
                        : new String[0];                     // off
```

And Private DNS **does** apply to VPN networks: `isPrivateDnsValidationRequired` gates on `INTERNET && NOT_RESTRICTED && TRUSTED`; a `VpnService` network has all three (it removes only `NOT_VPN`).

| Android Private DNS setting | Behavior with your tunnel up |
|---|---|
| **Off** | Plaintext DNS → `10.66.0.1`. AGH sees everything. ✅ |
| **Automatic** (opportunistic) | DoT to `10.66.0.1:853`, falls back to plaintext. Still your resolver. ✅ |
| **Hostname** (strict) | **DoT to the user's chosen provider. `10.66.0.1` is bypassed entirely.** ❌ |

Strict mode is **three taps in Settings**, is a documented privacy feature that thousands of guides recommend for benign reasons, and completely disables filtering. The queries still egress through your tunnel (`0.0.0.0/0` captures them), so the tunnel looks healthy, the handshakes keep coming, and **no alert fires** — quiet-peer detection sees a perfectly active peer. You lose all filtering and all visibility while every dashboard reads green.

Browser-level DoH is a lesser but real concern, and the defaults are better than I expected:

- **Chrome does not bypass you by default.** Auto-upgrade is a static table match against the *system resolver's IP*; `10.66.0.1` matches no known provider, so Chrome sends plaintext to AGH. But a user who manually selects a provider in `chrome://settings/security` bypasses completely.
- **Firefox** enables DoH by default in some regions and has heuristics that disable it — including a `VPN_DETECTED` platform check, though the documented wording is Windows-specific and macOS/Android coverage is **unverified**. Serving `NXDOMAIN` for the canary domain `use-application-dns.net` reliably disables Firefox's *default-on* DoH. It does nothing against a user who enabled it deliberately.
- **macOS:** Apple's documented rule (WWDC20 session 10047) is that VPN DNS settings take precedence over system-wide encrypted DNS, and the WireGuard app sets `matchDomains = [""]` to force all queries through the tunnel. However, there are credible, repeated user reports of iOS 16+ auto-upgrading a VPN's declared cleartext DNS to DoT/DoH against the VPN's stated preference — **with no Apple acknowledgment**. Treat as a real risk to test, not an established fact.

**Recommendations — add a hard requirement to Slice 4:**

1. **REJECT (not DROP) TCP+UDP 853 from `10.66.0.0/24`.** This is what defeats Android strict mode. **Understand the tradeoff before shipping it:** Android strict mode has *no plaintext fallback*. Blocking 853 gives that device **no DNS at all**, presented to the user as "Private DNS server cannot be accessed" — indistinguishable from broken internet, and therefore a direct trigger for the [F1](#f1) failure loop. REJECT at least fails fast rather than hanging.
2. **Because of that, detect-and-inform beats block-and-confuse.** Alert on 853 attempts from the tunnel and tell the user in plain language what to change, rather than leaving them staring at a broken phone. This is one of the few places where a *good* alert exists: a strict-mode DoT attempt is unambiguous evidence of a configuration that defeats filtering.
3. **Subscribe to a DoH/DoT bypass blocklist.** AGH ships a purpose-built one in its list chooser: **HaGeZi's Encrypted DNS/VPN/TOR/Proxy Bypass** (filterId 52, security group). Its own description says *"Prevention of bypassing is not guaranteed."* Note that AGH's "Blocked services" catalogue contains **zero** DoH/DoT entries — this must come from the blocklist, not the services feature.
4. **Serve NXDOMAIN for `use-application-dns.net`** to disable Firefox default-on DoH.
5. **Accept that this is mitigation, not prevention.** Blocking DoH by domain only works if the client resolves the bootstrap hostname through you first; hardcoded bootstrap IPs sail past. Blocking DoH endpoint IPs is a losing game — they churn and share CDN addresses with unrelated sites. Generic DoH-on-443 blocking is impossible without breaking the web. **None of this constrains a determined user on hardware they own.** It prevents *accidental* and *casual* bypass, which is the honest goal.
6. **Write this limitation into the product's honest-limits section**, alongside "DNS domains only." Right now the documents claim a stronger guarantee than the implementation can deliver.

---

### F16

**Hardcoded plaintext resolvers are not intercepted.** — High

Slice 4 sets the client's DNS to `10.66.0.1` and enables NAT/MASQUERADE for the full tunnel. Nothing stops a client from sending DNS to `8.8.8.8` — the packet enters the tunnel, gets NAT'd, and exits to Google. Smart TVs, IoT devices, some Android apps, and any user who edits the WireGuard config's `DNS =` line do exactly this. Filtering is bypassed, and again **silently**.

**Recommendation:** in the Slice 4 ops checklist, alongside `ip_forward` and MASQUERADE, add a **REDIRECT of outbound TCP+UDP port 53 from `10.66.0.0/24` to `10.66.0.1:53`.** This is the standard technique for a filtering exit node and is documented in OpenWrt's intercept-DNS guide. High value, low risk, and it makes the `DNS =` line in the client config advisory rather than load-bearing.

Note the dependency: **the DoH blocklist in [F15](#f15) is useless without this.** HaGeZi's own README states it — bootstrap resolution must go through you for domain-based DoH blocking to work at all. Ports 53 and 853 must be handled together or neither is worth doing.

For calibration: **Algo VPN, a hardened reference implementation, does not force-redirect DNS** — it only ACCEPTs port 53 to its local resolver. You are choosing a stricter posture than a well-regarded reference, which is correct for this product but means you will not find much prior art.

---

### F17

**A 5-minute reconcile is coarser than the curfew boundaries it enforces.** — High

Slice 6: *"Run every 5 minutes + kick on write."* A curfew *boundary* is not a write. So at 20:00, the block lands **whenever the next sweep happens** — up to 5 minutes late, every single night. At 08:00, blocks persist up to 5 minutes too long.

The asymmetry matters: **a late block is an unprotected window; a late unblock is an annoyance.** A nightly, predictable 0–5 minute gap at exactly the moment protection is supposed to engage is a real defect, and a user will notice the pattern quickly.

Client-side DNS caching makes the real-world gap worse, though the mechanism is not the obvious one. AGH's own cache is *not* the problem — filtering runs before the cache lookup in the handler chain (`processFilteringBeforeRequest` precedes `processUpstream`), so a new rule bites at AGH immediately. The stale window is entirely **client-side**: the device's stub resolver and browser hold the previously returned positive answer until its TTL expires. Expect up to the domain's natural TTL (commonly 300s, sometimes much longer) on top of reconcile lag.

**Recommendations:**

1. **Add a boundary scheduler.** Compute the next curfew transition across all active curfews and sleep until it, in addition to the 5-minute safety sweep. The sweep becomes a backstop for drift, not the enforcement mechanism.
2. **Bias early on the protective edge.** Apply blocks ~60 seconds *before* the boundary; lift them exactly at, or slightly after, the boundary. Users will not notice 60 seconds of early blocking; they will notice five minutes of late blocking.
3. **Consider clamping `cache_ttl_max`** to shorten the client-side stale window. Verify empirically that it rewrites the TTL delivered to clients rather than only bounding AGH's internal cache lifetime — and note the adjacent footgun: `cache_ttl_min` *raises* TTLs and would *lengthen* the stale window. Do not set it.
4. **Do not attempt to fix this with `POST /control/cache_clear`** — it clears AGH's cache, not the clients'.
5. **State the honest bound in the UI**: "changes take effect within about a minute; already-open apps may take a few minutes longer." Better than a silent, unexplained gap. Blocked responses carry a 10-second TTL by default, so the *unblock→block* direction is the only slow one.

---

### F18

**"Classify edits: tighten vs loosen" is one bullet for the hardest correctness problem in the product.** — High

The entire asymmetry — the reason this product exists — reduces to correctly classifying every possible policy edit. Slice 6 gives it a single line. That is where the bugs will be, and each bug is a silent bypass.

Consider edits that are loosenings in disguise, all of which a naive "did the window get shorter?" check misses:

- Changing the curfew's timezone from `America/New_York` to `America/Los_Angeles` — same wall-clock numbers, window shifts three hours.
- Narrowing subdomain match mode from "domain + subdomains" to "exact domain only."
- Deleting a target domain from a curfew while leaving the curfew intact.
- Disabling a curfew rather than editing it.
- Deleting the curfew outright.
- Editing a domain to a typo'd variant (`instagrarn.com`).
- Splitting one 20:00–08:00 curfew into 20:00–23:00 and 01:00–08:00.
- Any edit that is a tightening today and a loosening after the next DST transition.

Ad-hoc classification will not catch all of these, and the ones it misses are invisible.

**Recommendation: replace classification with a single testable predicate.**

Model policy as a pure function over a forward horizon:

```
P(user, domain, t) → blocked | allowed
```

An edit initiated by the protected user is permitted **iff it is at-least-as-restrictive everywhere**:

```
∀ domain ∈ (domains_old ∪ domains_new),
∀ t ∈ [now, now + H]:
    P_new(domain, t) ⊒ P_old(domain, t)        where blocked ⊒ allowed
```

Any edit failing the check requires a partner. Properties that make this practical:

- The policy is **piecewise-constant in time**, so the check is finite: evaluate only at the union of all boundary timestamps from both policies within the horizon, plus `now`.
- The domain set is the union of both policies' domains, so removals are caught.
- Everything is evaluated in **absolute time (UTC instants)**, which is what makes timezone edits and DST transitions fall out correctly instead of needing special cases.
- `H = 14 days` covers weekly patterns and both DST directions if you additionally test around transition dates.

This turns fifteen ad-hoc rules into one function with one invariant, and it is exactly the shape that **property-based testing** verifies well: generate random policy pairs, assert the predicate agrees with a brute-force sampling of `P` at minute granularity. That test suite is worth more than the rest of Slice 6's tests combined.

Two cases to specify explicitly, since the predicate alone does not decide them:

- **Adding a new curfew** is always a tightening (strict superset) → always allowed for the user. Good.
- **Device revocation** is not a policy edit and sits outside the predicate. Decide separately whether a user may revoke their own device — under [F6](#f6)'s logic, removing your last device shrinks the restraint perimeter and should carry the same cooling-off delay.

Also flag the DST edge for the demo: a 20:00–08:00 `America/New_York` window is 11 hours on spring-forward night and 13 on fall-back night. That is correct behavior, but confirm it is *intended* behavior and note it in the docs so it is not later "fixed" into a bug.

---

### F19

**Disabling IPv6 in the tunnel may leak routing on macOS.** — Medium

Q38 specifies **IPv6 off**, and Slice 4's client config lists `AllowedIPs` as full tunnel — meaning `0.0.0.0/0` only, with no `::/0`.

The two platforms diverge:

- **Android:** as shown in [F1](#f1), with one peer and a default route the app withholds `allowFamily` for **both** families, so IPv6 is blackholed rather than leaked. Safe, but IPv6-only services will fail — worth knowing when diagnosing "the internet is broken."
- **macOS:** `PacketTunnelSettingsGenerator` installs only the routes you list. With no `ipv6Settings`, **IPv6 traffic uses the physical interface and never enters the tunnel.**

The severity depends on a detail I could not confirm from source and which you should test rather than assume. The macOS WireGuard app sets `dnsSettings.matchDomains = [""]`, whose stated intent is that *all* DNS queries go through the tunnel's DNS. If that holds, DNS is still filtered by AGH and this is a routing/privacy leak rather than a filtering bypass — AAAA answers come back filtered, but the resulting connections egress outside the tunnel. If it does not hold on a dual-stack network, IPv6 DNS servers are reachable directly and **filtering is bypassed entirely**.

**Recommendation:** add a concrete test to Slice 4, on a **dual-stack network** (many home ISPs, most mobile carriers):

```bash
# On the Mac, tunnel up:
curl -6 https://ifconfig.co        # does IPv6 egress the droplet or the local ISP?
scutil --dns | head -40            # which resolvers, on which interface, in which order?
dig AAAA <a-domain-on-the-oisd-list>   # is it filtered?
```

If IPv6 leaks and the network is dual-stack, either add `::/0` to `AllowedIPs` with matching server-side v6 addressing and NAT, or explicitly disable IPv6 on the client. **Do not leave this untested** — "IPv6 off" reads like a simplification and may be an enforcement hole. Note also that the mobile-carrier case is common: many US carriers are IPv6-primary.

---

## 5. Infrastructure and operations

### F20

**Slice 1's TLS goal and Slice 11's cutover goal are mutually exclusive as written.** — High

Slice 1's demo: *"Browser opens `https://nightloch.app` (or agreed staging host) with valid cert."* Slice 1's gate: *"If old Nightloch still owns apex: use staging hostname until Slice 11, **or** cut apex only when ready."* Slice 11: *"Final CF cutover for `nightloch.app` if still staged."*

The "or" hides a real cost. Caddy's default automatic HTTPS uses HTTP-01 / TLS-ALPN-01, both of which require **the domain's public DNS to already resolve to that server** — Caddy performs an authoritative A/AAAA lookup and the CA connects back. So you cannot have Caddy serving a valid cert for `nightloch.app` while the old stack still owns the apex. Slice 1's stated demo and Slice 1's stated gate contradict each other.

The escape hatch is the DNS-01 challenge with the Cloudflare plugin, and it is **not free**:

> *"This module does not come with Caddy. It can be added by using xcaddy or our download page."*

Official packages — apt/Cloudsmith repo, COPR RPMs, **and the official Docker image** — ship standard modules only. `apt install caddy` plus `tls { dns cloudflare ... }` **fails at config load** with an unknown-module error. That is a surprising, mid-Slice-1 failure for something cloud-init treats as a one-line package install.

**Recommendation: pick one path in Slice 1 and write it down. Do not carry the "or" forward.**

- **Path A — staging hostname (recommended).** Use `lite.nightloch.app` or similar. HTTP-01 works normally, stock Caddy from apt, no custom build. The apex stays untouched until Slice 11. Cost: `BASE_URL` changes at cutover, so magic-link URLs, cookie domain, and Turnstile allowed-domains must all be configurable and re-verified — put that on the Slice 11 checklist explicitly.
- **Path B — DNS-01.** Requires a custom build. Options: `xcaddy build --with github.com/caddy-dns/cloudflare`; the download-page build service; or `caddy add-package` (marked **experimental**, and it replaces the on-disk binary — so a later `apt upgrade caddy` **silently reverts you** to a build without the plugin, and your certs stop renewing weeks later). If you take this path, build the binary in CI and deploy it as an artifact; do not use `add-package` on a box you also apt-manage.
  - Token permissions: **`Zone.Zone:Read` + `Zone.DNS:Edit`**. Sources disagree on whether `Zone:Read` can be scoped to one zone or needs All Zones — **test this**, it is a common setup failure.
  - Watch the Caddy 2.10 libdns 1.0 migration: pinning an old plugin version against 2.10+ will not compile.

Either way, add to Slice 11: **lower the apex A record TTL to 60s at least a day before cutover.** Unproxied Cloudflare records accept a 60s minimum (proxied ones are locked to 300s auto), and this is the cheapest possible insurance on a cutover.

One more Slice 1 trap worth pre-empting: Let's Encrypt allows **5 authorization failures per hostname per hour**. A misconfigured challenge burns through that quickly and then you are debugging a rate limit instead of the actual problem. Caddy falls back to LE staging during its own backoff, which is helpful but confusing if you do not expect it.

---

### F21

**Auth tokens are probably stored in plaintext, and then copied by design to a less-hardened host.** — High

Slice 2 creates `magic_link_tokens` and `sessions`. Slice 3 creates `partner_invites` with a token. **Nothing in the plan says these are hashed at rest.**

Slice 10 then pulls the SQLite database to stephens.page on a schedule.

Composed, that means **live session tokens, live magic-link tokens, and live partner-invite tokens are replicated to a second host** — one the documents describe as a legacy box with tight disk, used for "mail secrets habits and backups," and explicitly *not* hardened as part of this project. Anyone who can read that backup file can mint a session for any user, including a partner, without touching the production droplet.

**Recommendations:**

1. **Store only hashes.** SHA-256 of the token is sufficient for high-entropy random tokens (no need for a slow KDF — these are not user-chosen passwords). Lookup by hash; the plaintext exists only in the email link or the cookie. Applies to all three token types.
2. This is a **Slice 2 requirement, not a Slice 11 hardening item.** Retrofitting it means invalidating every session and rewriting the lookup path.
3. **Encrypt the backup at rest** before it leaves the droplet (`age` or `gpg`), so the pulled artifact is opaque on stephens. `shared-understanding.md` already says *"encrypt sensitive archives at rest when practical"* — with unhashed tokens it is not "when practical," it is mandatory.
4. Confirm session cookies are **host-only** (no `Domain=` attribute). A cookie scoped to `.nightloch.app` is sent to `agh.nightloch.app` — needlessly exposing the product session to the AGH admin origin.
5. Add to Slice 11's security pass: **the backup file must not be web-reachable on either host.** `shared-understanding.md` says this; make it a checklist item with an actual `curl` test rather than an intention.

---

### F22

**The backup pull gives stephens.page an SSH key into the enforcement box.** — Medium

`shared-understanding.md` reasons: *"Prefer pull-from-stephens over broad push rights onto stephens."* The reasoning is sound in isolation — you do not want the droplet holding credentials that can write anywhere on stephens.

But the conclusion inverts the trust relationship. **Pull means stephens.page holds an SSH credential that can read from the droplet** — the box running your VPN, your DNS enforcement, your database, and (optionally, per Slice 10) your WireGuard server private key. If stephens is the weaker host — and the documents imply it is older, more multi-purpose, and disk-constrained — then this design lets the weaker host read the stronger one.

**Recommendation:** keep the pull direction, but constrain it so the credential is worth little if stolen:

1. **Forced-command SSH key** on the droplet:
   ```
   command="/usr/local/bin/nightloch-backup-dump",restrict ssh-ed25519 AAAA...
   ```
   `restrict` disables port forwarding, agent forwarding, PTY, and X11. The key can invoke exactly one script and nothing else.
2. That script emits an **already-encrypted** snapshot to stdout and takes no arguments. Stephens never gets a shell, never chooses a path, cannot read `/etc`, and cannot read the WireGuard key.
3. Encrypt to a key whose private half lives **neither** on the droplet nor on stephens — offline, or in a password manager. Both hosts can be compromised without the backups being readable.
4. If the WireGuard server private key is included, encrypt it separately and note that its compromise permits impersonating the server to all clients.
5. Add to Slice 10's demo: *from stephens, attempt `ssh nightloch ls /` and confirm it fails.* One command, proves the whole constraint.

---

### F23

**Nothing watches the watcher.** — High

Slice 8 builds alerting *into the application*, on the droplet, which is the thing most likely to fail. Every alert path in the plan — quiet peer, drift, AGH unreachable, loosen, enroll — originates from a process on the same host as the thing it monitors.

Failure modes with **zero** alerting, all of which leave the system reporting nothing while protection is degraded or absent:

| Failure | Alerts fire? | Protection state |
|---|---|---|
| Go app crashed / crash-looping ([F25](#f25)) | No | Curfews frozen at last state; no reconcile |
| Droplet powered off | No | Devices blackholed ([F1](#f1)) → user turns VPN off → unprotected |
| Resend API key expired or account suspended | No | Total alerting silence |
| Outbound SMTP/API blocked | No | Total alerting silence |
| Partner's mail provider silently spam-filing ([F29](#f29)) | Appears to | Nobody reads them |

**The system's failure mode is silence, and its healthy mode is also mostly silence.** A partner cannot distinguish them. That is the defining characteristic of an accountability system that has quietly stopped working.

**Recommendation: add an external dead-man's-switch. This is a Slice 8 requirement, not a nice-to-have.**

1. The app pings an external service on a schedule (healthchecks.io has a free tier; a cron on stephens.page also works and adds no dependency). **If the ping stops, the external service emails you and the partner.** This inverts the logic: absence of signal becomes an alarm instead of a silence.
2. The ping should be **conditional on health**, not a bare heartbeat: only ping if the last reconcile succeeded *and* AGH's API answered *and* the DB is writable. A liveness check that passes while the app is broken is worse than none.
3. **Test the email path independently of app health** — e.g. a weekly "Nightloch is alive, here is your coverage summary" message. Under [F2](#f2)/[F3](#f3) this is the coverage report anyway, so it does double duty: it *is* the integrity signal *and* it proves the pipe works. That is a strong argument for reopening Q13's "no digests."
4. Add to the Slice 11 dogfood checklist: *kill the app for an hour and confirm you find out from something other than the app.*

---

### F24

**Backup cadence and snapshot consistency are both unspecified.** — Medium

Slice 10 says *"cron **pull** consistent SQLite snapshot"* and *"Retention: small N (disk-aware)."* Two gaps:

**Consistency.** "Consistent" is asserted, not mechanized. `cp` of a live SQLite database in WAL mode yields a corrupt or stale artifact — a fact usually discovered during a restore, which is the worst possible time. Specify **`VACUUM INTO '/path/snapshot.db'`** (SQLite 3.27+, single statement, safe on a live database, produces a compacted standalone file) or `sqlite3 .backup`. Also state the journal mode: WAL is the right choice here and should be a deliberate decision, since it affects both backup and the reconciler's write pattern.

**Cadence and the DO backup policy.** The plan never states either. Worth correcting an assumption the documents seem to carry: **DigitalOcean backups are no longer weekly-only.** Current options:

| Plan | Cost | Retention | Worst-case RPO |
|---|---|---|---|
| Weekly | 20% of droplet cost (~$2.40/mo) | 4 backups / 4 weeks | ~7 days + 4h window |
| Daily | 30% (~$3.60/mo) | 7 backups / 7 days | ~28 hours |

The Terraform/OpenTofu provider exposes this properly: `backups` is a plain bool (**no `ForceNew`** — toggling does not replace the droplet), and `backup_policy` takes `plan` (`daily` or `weekly`), `weekday`, and `hour`. Two gotchas: the registry docs note that if `backup_policy` is omitted while `backups = true`, the plan **defaults to daily** — so state it explicitly rather than relying on that; and setting `backup_policy` with `backups = false` is a hard error. The sub-daily usage-based frequencies (4h/6h/12h) exist on DO but are **not reachable through the provider**, whose validator accepts only `daily`/`weekly`.

**Recommendation:** set `backup_policy { plan = "daily" }` explicitly in Slice 1's OpenTofu, and pull the SQLite snapshot **hourly**. The database is small (users, edges, curfews, devices — kilobytes to low megabytes), so hourly costs nothing and reduces worst-case loss from ~28 hours to ~1. Keep, say, 24 hourly + 7 daily.

While specifying retention, remember [F12](#f12): if AGH's query log retention is left at its **90-day default**, every VM-level backup contains up to 90 days of full browsing history for every user. Setting AGH retention to the intended 7 days is also a backup-scope decision.

---

### F25

**Migrate-on-boot plus auto-deploy can crash-loop the alerting system.** — Medium

Cross-cutting concerns specify *"Simple SQL files or embed migrations; run on boot."* Slice 9 auto-deploys on push to `main` and restarts systemd. There is no health gate and rollback is *"previous binary keep"* — manual.

A bad migration or a startup panic therefore produces a crash-looping service. The saving grace is real and worth stating: **the Go app is not in the DNS data path.** AGH keeps enforcing the last rules it was given, so users stay protected and online. But the reconciler stops (curfews freeze at whatever state they were in — including *unblocked*, if it died at 19:58) and **all alerting stops**, with nobody notified ([F23](#f23)).

**Recommendations:**

1. **Health gate in the deploy job.** After restart, poll `/health` for up to 30 seconds; on failure, restore the previous binary and restart automatically. Slice 9 already keeps the previous binary — this makes it useful without a human.
2. **`/health` must be honest**: DB writable, AGH API reachable, last reconcile within threshold. A handler returning `200 OK` unconditionally makes the gate theater.
3. **Back up before migrating.** Snapshot the database at startup before applying migrations, keeping the last few. SQLite makes this nearly free and it converts a bad migration from a restore-from-yesterday event into a thirty-second rollback.
4. **Migrations must be forward-only and additive** (add columns/tables; never drop or rewrite in the same release as the code that stops using them). With auto-deploy and no staging ([F32](#f32)), a destructive migration has no safety net.
5. Consider **not** running migrations automatically on boot. A separate `nightloch migrate` invoked explicitly by the deploy job — before the restart, with its result gating the restart — is a small change that removes the crash-loop class entirely.

---

## 6. Where the plan contradicts itself

These are cheaper to fix than anything above — they are internal inconsistencies, not external discoveries.

### F26

**Security is concentrated in Slice 11, after everything is already live.** — High

Slice 11 contains: *"Security pass: cookies, CSRF on POSTs, Turnstile, no open resolver, AGH not public without auth."*

By the time Slice 11 begins, Slice 2 has shipped session cookies to production, Slice 3 has shipped state-changing partner mutations, Slice 5 has shipped device enrollment, and Slice 6 has shipped policy mutation — **all reachable on the public internet without CSRF protection.** A CSRF against "end partner edge" or "loosen curfew" is precisely the bypass this product exists to prevent, and it is live for the entire middle of the plan.

This contradicts the plan's own framing. "Thin then thicken" is about *features*; it is not a license to defer security properties, because unlike features, security properties must be retrofitted through every handler already written.

**Recommendation: move the security-relevant items to the slice that creates the exposure.**

| Item | Move to | Why |
|---|---|---|
| CSRF on state-changing POSTs | **Slice 2** | The first POST handler exists there; adding middleware once is trivial, retrofitting twenty handlers is not |
| Cookie flags (HttpOnly, Secure, SameSite=Lax, host-only) | **Slice 2** | Already listed in Slice 2 — just delete it from Slice 11 to avoid implying it is deferred |
| Token hashing at rest ([F21](#f21)) | **Slice 2** | Retrofit invalidates all sessions |
| No open resolver | **Slice 4** | Already a Slice 4 gate — Slice 11 restating it implies it might have slipped |
| AGH behind auth | **Slice 4** | Same |
| Device-enrollment gating ([F9](#f9)) | **Slice 5** | Slice 5 opens the hole |
| Query-log tenant isolation ([F12](#f12), [F13](#f13)) | **Slice 7** | Slice 7 opens the hole |

What should remain in Slice 11 is genuinely end-state work: the final cutover, a review pass over accumulated decisions, and the dogfood drill. **Slice 11 should verify security properties, not create them.**

---

### F27

**The OISD false-positive allow path is marked "Optional."** — High

Slice 11: *"Optional: second peer, false-positive handling for OISD (partner allow path)."*

OISD NSFW is a broad list. Over a week of ordinary browsing on a full tunnel it will block something the user legitimately needs — a CDN, an image host, an ad/analytics domain a real site depends on, a false categorization. This is not speculative; it is the normal operating condition of any large blocklist.

When it happens, and there is no allow path, the user's **only** recourse is to turn off the VPN. Which:

- removes all protection, including the baseline,
- is the exact behavior the product exists to prevent,
- generates no alert outside curfew hours ([F3](#f3)),
- and teaches the user that VPN-off is the standard fix for "site won't load" — reinforcing [F1](#f1)'s training loop.

**A missing allow path converts every false positive into a total protection bypass.** That is not optional.

It also compounds with [F10](#f10): the per-domain allowlist is now the *only* per-user baseline mechanism available, so it must exist for the policy model to function at all.

**Recommendation: move the partner allow path into Slice 6 as a core deliverable.** Concretely — the user can *request* an allowlist entry (one click from a "this site is blocked" page), the partner approves it, and it becomes `@@||domain^$client=10.66.0.N`. This is a small feature that turns the product's most common friction point into a moment of contact between user and partner, which is the intended dynamic.

Slice 4's demo should also be extended: browse normally for a day on the tunnel and **record what breaks**. That list is the real requirements document for this feature.

---

### F28

**The audit log arrives in Slice 8, after Slices 3 and 6 have been mutating policy for weeks.** — Medium

Slice 8 creates the *"Event/audit table (append-only enough for dedupe keys)."* But Slice 3 ships partner invite/accept/end, and Slice 6 ships every curfew and baseline mutation. Both are exactly the events that must be attributable, and neither is recorded until later.

Two costs: the dogfood period between Slices 3 and 8 produces no history at all (so the first real "who loosened this?" question is unanswerable), and the audit table gets designed *around alerting needs* rather than around the events themselves — the plan's own phrasing, *"append-only enough for dedupe keys,"* shows the table being scoped by what the alert deduper happens to need.

**Recommendation:** create the audit/event table in **Slice 3**, when the first attributable mutation ships. Write to it from every mutation onward. Slice 8 then adds *alerting on top of an existing event stream*, which is both easier and better factored: alerts become a projection of events rather than a parallel mechanism.

Minimum event shape: `id, occurred_at, actor_user_id, subject_user_id, action, target, before_json, after_json`. The before/after pair is what makes [F18](#f18)'s tighten/loosen decisions reviewable after the fact — and gives you the data to check whether the predicate is behaving correctly in the wild.

---

### F29

**Alert deliverability is the entire integrity model and has no failure handling.** — Medium

Every integrity guarantee terminates in an email. The plan has no bounce handling, no delivery verification, and no fallback. If the partner's provider spam-files Nightloch mail — plausible for a low-volume domain sending automated notifications — the system is silently unprotected while every internal indicator reads healthy.

The From address compounds this: `Nightloch <nightloch@stephens.page>`. Users receive magic links and integrity alerts from a domain that is **not** the domain they signed up on. That is bad for deliverability (no alignment between the product domain and the sending domain), bad for user trust, and bad training — it is the exact shape of a phishing email. It also means [F9](#f9)'s mail-bomb amplification damages your **personal** mail domain's reputation.

**Recommendations:**

1. **Send from `nightloch.app`.** Verify the domain in Resend with proper SPF/DKIM/DMARC. The apex is already yours and already in Cloudflare DNS under OpenTofu — adding the records is a small addition to Slice 1, and it removes a permanent coupling to stephens.page. Keep `stephens.page` only if there is a specific reason, and record the reason.
2. **Handle bounces.** Resend exposes delivery webhooks. A hard bounce to a partner address must surface in the UI as "we cannot reach your partner" — visible to *both* parties.
3. **Verify partner deliverability at invite time**: the accept flow already proves the address receives mail. Record that timestamp and treat a long gap since the last *successful* delivery as a degraded state.
4. **Ask partners to allowlist the sender during onboarding.** Low-tech and effective.
5. This is another argument for [F23](#f23)'s external heartbeat: something outside the app must be able to say "alerts have not been delivered in N days."

---

### F30

**The restore is "documented," not executed — which violates the plan's own core rule.** — Medium

Slice 10's demo: *"Fresh snapshot file appears on stephens after cron; restore **dry-run documented** once."*

The plan opens with *"**Done means demoable** — someone can click/SSH/use a device and see the outcome."* A documented dry-run is neither. An untested restore procedure is an assumption, and backup restores fail for mundane reasons that only appear on a real attempt: schema drift, WAL files, wrong SQLite version, file permissions, a missing env var, a WireGuard key that was never actually included, a config the runbook forgot.

**Recommendation:** make Slice 10's demo an **actual restore**:

1. `tofu apply` a second droplet from the same module (~$0.02/hour — destroy it after).
2. Restore the pulled SQLite snapshot onto it.
3. Deploy the current binary.
4. Sign in as a real user against the restored database and confirm devices, partner edges, and curfews are intact.
5. `tofu destroy`.

This is under an hour and converts the single highest-consequence untested path in the project into a verified one. It also validates the OpenTofu module's reproducibility — which nothing else in the plan tests, since Slice 1's droplet is created once and never recreated.

Write down the wall-clock time it took. That number is your actual RTO, and it is worth knowing before you need it.

---

### F31

**Instagram is a poor first curfew target for DNS-level blocking.** — Medium

The flagship demo — *"Inside window: `instagram.com` blocked on enrolled device. Outside window: allowed"* — will be flakier than the plan expects, for reasons unrelated to code correctness:

- **Shared infrastructure.** Instagram serves content from `cdninstagram.com` and `fbcdn.net`, shared across Meta properties. Blocking only `instagram.com` + subdomains may leave a partly-functional app; blocking the CDNs risks collateral damage to Facebook and WhatsApp, and WhatsApp breakage is the kind of thing that gets a tool uninstalled.
- **Client caching.** Per [F17](#f17), an app with a warm DNS cache and open connections keeps working past the boundary. The block appears not to work; the user concludes the product is broken.
- **Trivial substitution.** Blocking Instagram at DNS does nothing about the same content via `facebook.com` reels, a browser, or the cellular connection with the VPN off.

None of this makes Instagram wrong as a *product* target. It makes it a **bad first demo**, because a flaky demo of the flagship feature will read as "the curfew engine doesn't work" when the engine is fine.

**Recommendations:**

1. **Test the engine with a domain you control** — a subdomain of `stephens.page`, or any low-TTL domain with no CDN sprawl. Deterministic, instant, and it proves the boundary logic exactly. Do this first in Slice 6.
2. **Then** apply the Instagram curfew as a separate acceptance step, with its own realistic expectations documented ("the app may work for a few minutes after 20:00; web is blocked immediately").
3. Investigate AGH's native blocked-services entry for Instagram ([F14](#f14)) — Meta's own domain sprawl is exactly what a maintained service definition handles better than a hand-written list.
4. Set expectations in `shared-understanding.md`: DNS-level app blocking is **partial** for apps with shared CDN infrastructure. This belongs next to the existing honest-limits paragraph.

---

### F32

**No staging environment, and NAT changes risk SSH lockout.** — Medium

Everything happens on one droplet: OpenTofu changes, NAT/firewall changes, migrations, deploys. Slice 4's ops checklist gates NAT behind approval, which is good, but approval is not a rollback.

A firewall or NAT misconfiguration can lock you out of SSH on the box that is also your devices' internet path. The plan never mentions the recovery path.

**Recommendations:**

1. **Document the DigitalOcean web console** as the out-of-band recovery path in the Slice 4 checklist. It is a browser-based serial console that works without SSH or networking. Confirm the deploy user has a working password (console login needs one — key-only auth is useless there) and store it in the password manager **before** touching NAT.
2. **Self-cancelling firewall changes.** Before applying rules, schedule a revert: `echo "iptables-restore < /root/fw.good" | at now + 10 minutes`. If the change locks you out, it undoes itself; if it works, cancel the job. Standard practice for remote firewall work and it costs nothing.
3. **Snapshot before the NAT step.** A DigitalOcean snapshot immediately before the first NAT/firewall change gives a whole-VM rollback for a few cents.
4. **Consider a throwaway second droplet for the Slice 4 network work.** Combined with [F30](#f30)'s restore drill, one temporary droplet serves both purposes for a couple of dollars, and derisks the two most dangerous steps in the plan.

---

### F33

**Guardian relationships need a different model than peer accountability.** — Medium

`shared-understanding.md` mentions *"a second Protectee later"* without specifying the relationship. The current model is strictly symmetric — *"Either party may end the relationship"* — and consent-based, with log sharing opt-in per edge and controlled by the protected user.

That model is correct for **peer accountability between consenting adults**. It is wrong for a **guardian relationship**, where the protected person should not be able to unilaterally end oversight or disable log sharing, and where the guardian's powers derive from a different basis than mutual consent.

Building the peer model first is right, and there is no need to build the guardian model in v1. But the two should not be silently conflated, because retrofitting asymmetric termination onto a symmetric edge is a schema change plus an authorization rewrite.

**Recommendations:**

1. **Add an explicit `edge_type` column** (`peer` | `guardian`) in Slice 3, even if only `peer` is implemented. One column now; a migration and an authorization audit later.
2. **Decide and write down what the second Protectee's relationship is** before Slice 3, since it determines whether termination is symmetric and whether log sharing is user-controlled.
3. If a minor is involved, note that the consent model, data retention ([F12](#f12) — the 90-day default matters much more here), and the "protected user may disable log sharing" affordance all need separate consideration.
4. Keep the peer model's consent properties intact for adults. The strength of this product's design is that an adult opts in; do not weaken that to accommodate a guardian case that can be modeled separately.

---

## 7. Proposed re-cut of the slice plan

The slice *structure* is sound. What follows keeps all eleven slices and their demos, and changes what lands in each. Changes are marked **[+]** added, **[→]** moved, **[!]** changed.

### Slice −1 (new): Security & policy design — *no code*

The gap this review is really about. Half a day of writing, before Slice 0.

- **[+]** Answer every question in [Section 8](#8-questions-that-must-be-answered-before-slice-0)
- **[+]** Write the tighten/loosen predicate ([F18](#f18)) as a spec with worked examples, including DST and timezone-change cases
- **[+]** Write the partner-perimeter rules: add, remove, cooling-off, zero-partner state ([F5](#f5)–[F7](#f7))
- **[+]** Correct the fail-mode language in `shared-understanding.md` and `DECISIONS.md` Q6 ([F1](#f1))
- **[+]** Decide the AGH policy mapping ([F10](#f10), [F14](#f14)) and write the rule-generation model down
- **Done when:** `shared-understanding.md` is amended and `DECISIONS.md` records the reopened decisions (Q6 fail mode, Q13 digests, Q22 quiet peer, baseline off-switch, partner lifecycle)

### Slice 0 — Repo skeleton

Unchanged. It is correctly scoped.

- **[+]** Add the policy predicate package with property-based tests as the first real code. It has no dependencies, it is the highest-risk logic in the product, and it is pure — ideal for Slice 0.

### Slice 1 — Live origin

- **[!]** **Pick Path A or Path B for TLS and delete the "or"** ([F20](#f20))
- **[+]** `backup_policy { plan = "daily" }` explicitly in OpenTofu ([F24](#f24))
- **[+]** Disable unattended-upgrade auto-reboots; add swap ([F4](#f4))
- **[+]** Set the console password and record it before any network work ([F32](#f32))
- **[+]** Resend domain verification for `nightloch.app` — SPF/DKIM/DMARC records in the same Cloudflare module ([F29](#f29))

### Slice 2 — Sign in

- **[→]** CSRF middleware on all state-changing POSTs — from Slice 11 ([F26](#f26))
- **[→]** Token hashing at rest for magic links and sessions — from nowhere ([F21](#f21))
- **[+]** Host-only session cookie ([F21](#f21))
- **[+]** Honest `/health` endpoint — needed by Slice 9's gate and Slice 8's heartbeat ([F25](#f25))

### Slice 3 — Partner graph

- **[→]** Audit/event table — from Slice 8 ([F28](#f28))
- **[+]** `edge_type` column (`peer` | `guardian`), only `peer` implemented ([F33](#f33))
- **[+]** Cooling-off state machine for user-initiated termination ([F6](#f6))
- **[+]** "Partner added" notification to all existing partners ([F5](#f5))
- **[+]** Partner roster visible to partners ([F5](#f5))
- **[!]** Demo adds: *A requests removal of partner B → B is emailed → A's curfews stay enforced for 72h → A can cancel*

### Slice 4 — Tunnel + baseline block

The most under-scoped slice in the original plan. It carries the entire network security posture.

- **[+]** REDIRECT outbound TCP+UDP 53 from `10.66.0.0/24` → `10.66.0.1` ([F16](#f16))
- **[+]** REJECT TCP+UDP 853, plus the decision about how to communicate it to users ([F15](#f15))
- **[+]** HaGeZi Encrypted DNS/Bypass blocklist; NXDOMAIN for `use-application-dns.net` ([F15](#f15))
- **[+]** **Set AGH query log retention explicitly to 7 days** — the default is 90 ([F12](#f12))
- **[+]** `PersistentKeepalive = 25` in generated client configs ([F2](#f2))
- **[+]** systemd ordering for AGH after `wg0`, or `net.ipv4.ip_nonlocal_bind=1` (see appendix)
- **[+]** IPv6 leak test on a dual-stack network ([F19](#f19))
- **[+]** Droplet-reboot test: observe and document real client behavior per platform ([F1](#f1))
- **[+]** Investigate whether Instagram is in AGH's blocked-services catalogue ([F14](#f14))
- **[+]** Self-cancelling firewall change procedure; snapshot before NAT ([F32](#f32))
- **[+]** A week of normal browsing on the tunnel, recording OISD false positives ([F27](#f27))

### Slice 5 — Device enroll

- **[+]** **Enrollment authorization gate** — operator allowlist for v1 ([F9](#f9))
- **[!]** Per-user baseline via per-client `$client` rules, not per-client lists ([F14](#f14))
- **[+]** IP quarantine pool: no reuse within the AGH retention window ([F13](#f13))
- **[+]** Account/device teardown as one transaction ([F8](#f8))
- **[!]** Demo adds: *an account without the entitlement cannot mint a peer*

### Slice 6 — Curfews + asymmetry

- **[!]** Implement the at-least-as-restrictive predicate, not ad-hoc classification ([F18](#f18))
- **[+]** Boundary scheduler; apply blocks ~60s early ([F17](#f17))
- **[+]** Single-writer serialization for `set_rules`; generated-file banner; read-back drift check ([F11](#f11))
- **[→]** Partner allow path for false positives — from Slice 11's "optional" ([F27](#f27))
- **[!]** Replace whole-baseline disable with per-domain allowlisting ([F10](#f10))
- **[!]** Demo first with a domain you control, then Instagram ([F31](#f31))

### Slice 7 — Domain logs

- **[+]** Server-side re-filtering of every query-log row ([F12](#f12))
- **[+]** Bound reads by device `created_at` ([F13](#f13))
- **[+]** The two-tenant leak test ([F12](#f12))

### Slice 8 — Integrity alerts

- **[!]** **Restructure: alert on actions, report on coverage** ([F2](#f2), [F3](#f3))
- **[!]** Coverage measured whenever a device is enrolled, not only during curfews ([F3](#f3))
- **[+]** External dead-man's-switch ([F23](#f23))
- **[+]** Resend bounce webhook handling ([F29](#f29))
- **[!]** Alerts become a projection of Slice 3's event table ([F28](#f28))

### Slice 9 — Continuous deploy

- **[→]** Land right after Slice 2, as the plan's own dependency graph already says
- **[+]** Health gate with automatic rollback ([F25](#f25))
- **[+]** Migrations as an explicit deploy step, not on boot ([F25](#f25))

### Slice 10 — Backups

- **[!]** Demo is an **actual restore onto a fresh droplet**, not a documented dry-run ([F30](#f30))
- **[+]** `VACUUM INTO`; state the journal mode ([F24](#f24))
- **[+]** Hourly pull ([F24](#f24))
- **[+]** Forced-command SSH key; encrypt before it leaves the droplet ([F22](#f22))

### Slice 11 — Hardening & cutover

- **[!]** **Verifies** security properties rather than creating them ([F26](#f26))
- **[+]** Apex TTL lowered to 60s a day ahead ([F20](#f20))
- **[+]** `BASE_URL` / cookie domain / Turnstile domain re-verification if staged ([F20](#f20))
- **[+]** Dogfood drill: kill the app for an hour, confirm you learn it from elsewhere ([F23](#f23))

### Revised exit criteria

Keep the original ten, and add:

- [ ] Droplet reboot behavior observed and documented per platform
- [ ] Android strict-mode Private DNS attempted and correctly handled
- [ ] Hardcoded `8.8.8.8` from a device is intercepted
- [ ] Restore executed onto a fresh droplet with a recorded RTO
- [ ] Two-tenant query-log isolation test passes
- [ ] Partner cooling-off drill completed end to end
- [ ] Unauthorized account confirmed unable to mint a peer
- [ ] External heartbeat fires when the app is killed

---

## 8. Questions that must be answered before Slice 0

Ranked by how much downstream work they block. Each has a proposed default in brackets — none of them require a long deliberation, but they do require a decision that is written down.

**Perimeter**

1. Can a user unilaterally end a partner edge? If so, with what delay? *[Yes, 72-hour cooling-off; partner-initiated is immediate — [F6](#f6)]*
2. Are existing partners notified when a new partner is added? Can they see the roster? *[Yes to both — [F5](#f5)]*
3. With zero partners, can a user loosen their own policy? *[Yes, with the cooling-off delay providing the teeth — [F7](#f7)]*
4. Can a user delete their own account? What happens to peers, IPs, AGH clients, and edges where they are the protector? *[Yes, via the same cooling-off path, with transactional teardown — [F8](#f8)]*
5. Can a user revoke their own device? *[Treat as perimeter-shrinking; same delay for the last device — [F18](#f18)]*
6. Is device enrollment gated? *[Yes, operator allowlist for v1 — [F9](#f9)]*

**Policy semantics**

7. Is "baseline disabled for one user" a supported operation? *[No — replace with per-domain allowlisting, since AGH cannot express it without also disabling curfews — [F10](#f10)]*
8. What exactly is the tighten/loosen rule? *[The at-least-as-restrictive predicate over a 14-day horizon — [F18](#f18)]*
9. Who may allowlist a false positive, and through what flow? *[User requests, partner approves — [F27](#f27)]*

**Integrity**

10. What is the actual fail mode, stated per scenario? *[VPN off → open; server unreachable → closed until manual intervention — [F1](#f1)]*
11. How is protection coverage measured when no curfew is active? *[Continuously, per enrolled device — [F3](#f3)]*
12. Does Q13 ("no digests") survive [F2](#f2) and [F3](#f3)? *[No — the coverage report is the only usable form of this signal]*
13. What watches the app? *[External heartbeat, conditional on real health — [F23](#f23)]*
14. Is the sending domain `stephens.page` or `nightloch.app`? *[`nightloch.app` — [F29](#f29)]*

**Enforcement**

15. Are ports 53 and 853 intercepted/blocked at the tunnel? *[Yes to both, together — [F15](#f15), [F16](#f16)]*
16. What is the answer when a user's Android strict-mode Private DNS stops working because of it? *[Detect, alert, and explain in-product — [F15](#f15)]*
17. Is IPv6 blocked, tunneled, or leaking, per platform? *[Test before deciding — [F19](#f19)]*

**Scope**

18. Is the second Protectee's relationship peer or guardian? *[Decide now; implement `peer` only, with the column present — [F33](#f33)]*
19. Path A or Path B for TLS in Slice 1? *[Path A, staging hostname — [F20](#f20)]*

---

## 9. Two things the plan does not say

**There is no sizing anywhere.** Eleven slices, no estimates. Based on the deliverables, Slice 4 alone — WireGuard, NAT, AGH, Caddy, the DNS interception rules, plus the additions above — is several days of network debugging for one person, and it is the slice most likely to expand. Slices 0–3 are comparatively quick. A rough per-slice estimate is worth adding, not for scheduling but as an over-scope detector: if Slice 4 is estimated at half a day, that estimate is wrong, and finding out now is better than finding out at 1 a.m. with a phone that has no internet.

**The plan's numbering and its dependency graph disagree.** The closing instruction says *"Complete slices in order unless a dependency allows parallel work above,"* while the sequencing diagram puts 9 right after 1 and 4 parallel to 2–3. Since Slice 9 (deploy) makes every subsequent slice faster, it should simply be renumbered rather than left as a permitted exception — the ordering that appears in the table is the one people follow.

---

## Appendix — verified facts and citations

Facts used in this review that were checked against primary sources. Where a source was ambiguous it is marked **[uncertain]** and the finding hedges accordingly.

### AdGuard Home (read at tag `v0.107.78`)

| Fact | Source |
|---|---|
| Custom filtering rules are a single **global** list; `set_rules` does `d.conf.UserRules = req.Rules` | `internal/filtering/http.go:361` |
| **No** optimistic concurrency on any write — no ETag/If-Match anywhere in the codebase or spec | codebase + `openapi/openapi.yaml` |
| `$client` accepts IP, CIDR, or persistent client **name**; **not** ClientID | `internal/filtering/filtering.go:896-901`; standing TODO at `internal/filtering/http.go:567`; AdGuard KB |
| `$ctag` uses a fixed vocabulary (`device_*`, `os_*`, `user_admin`/`user_regular`/`user_child`) | DNS filtering syntax KB |
| Per-client **filter lists** do not exist; only `use_global_settings` / `filtering_enabled` | Client schema; maintainer reply in Discussion #6838; open request #8103 |
| `filtering_enabled: false` disables **both** subscriptions and user rules — `matchHost` returns early | `internal/filtering/filtering.go` |
| Query log `search` is **substring**, OR'd across clientID/host/IP/name | `internal/querylog/searchcriterion.go` (`ctDomainOrClientCaseNonStrict`); upstream test `by_client_ip_non-strict` |
| Double-quoted `search` value switches to exact match — **undocumented in the OpenAPI spec** | `internal/querylog/http.go:340-347` |
| Query log retention **defaults to 90 days**; API accepts 6h/1d/7d/30d/90d | `internal/home/config.go:535`; `checkInterval` |
| Blocked-services **schedules** exist (backend v0.107.33, UI v0.107.37); schedule denotes **inactivity**, not blocking | CHANGELOG; `Schedule.Contains` usage |
| No time-of-day modifier for custom rules — scheduling arbitrary domains needs an external writer | full modifier set: `client`, `denyallow`, `dnstype`, `dnsrewrite`, `important`, `badfilter`, `ctag`, `respgeo` |
| Blocked-services catalogue is ~136 app-level entries with **zero** DoH/DoT entries | HostlistsRegistry `services/` |
| Filtering runs **before** the cache, so a new rule bites immediately at AGH; stale window is client-side | handler chain: `processFilteringBeforeRequest` → `processUpstream` |
| `blocked_response_ttl` defaults to **10 seconds** | `internal/home/config.go:565` |
| Auth is session cookie or HTTP Basic; **no API tokens**; Basic works on every endpoint | `internal/home/authhttp.go:501-506` |
| Binding to a nonexistent address is **fatal** (`log.Fatal`), but the shipped unit has `Restart=always`/`RestartSec=10`, so boot converges with a DNS gap | `internal/home/home.go:748-752, 885-891`; `internal/ossvc/config_linux.go` |
| RAM ~100–250 MB steady state at ~150k domains; **~2× spike during blocklist refresh** | community reports (issues #1599, #6540) — **[uncertain]**, no official figures published |

### WireGuard clients

| Fact | Source |
|---|---|
| Android **deliberately withholds** fall-through when `sawDefaultRoute && peers == 1` | `GoBackend.java`, comment `// "Kill-switch" semantics` |
| `allowFamily` is what permits fall-through to the underlying network | AOSP `VpnService.Builder` javadoc |
| VPN network registers `NET_CAPABILITY_INTERNET` with `requiresInternetValidation = false` — treated as permanently validated | `Vpn.java` / `VpnConfig` |
| macOS maps `AllowedIPs` to `includedRoutes`; unlisted families use the physical interface | `PacketTunnelSettingsGenerator.swift` |
| macOS forces DNS through the tunnel via `matchDomains = [""]` | `PacketTunnelSettingsGenerator.swift` |
| Client retries handshakes indefinitely; no failure signal | `wireguard-go/device/constants.go` (`RekeyTimeout` 5s, `RekeyAttemptTime` 90s) |
| Splitting `0.0.0.0/0` into two halves does **not** restore failover — it only unblocks IPv6 as a leak | `GoBackend.java` logic |

### Android / browser DNS

| Fact | Source |
|---|---|
| Strict-mode Private DNS **overrides** VPN-provided DNS; Off/Automatic defer to the VPN | AOSP `DnsManager.sendDnsConfigurationForNetwork()` |
| Private DNS applies to VPN networks (`INTERNET && NOT_RESTRICTED && TRUSTED` all hold) | `ConnectivityService.handlePerNetworkPrivateDnsConfig`, `Vpn.java` |
| Strict mode has **no plaintext fallback** — blocking 853 yields no DNS at all | AOSP behavior |
| Chrome auto-upgrade matches the system resolver IP against a static table; `10.66.0.1` matches nothing | Chromium `doh_provider_entry.h` |
| Chrome has **zero** VPN awareness in its DoH config logic | `stub_resolver_config_reader.cc` |
| Firefox disables DoH on canary `use-application-dns.net` and on `VPN_DETECTED` | Gecko `DoHHeuristics.sys.mjs` — **[uncertain]** whether the VPN check covers macOS/Android; documented wording is Windows-specific |
| Apple states VPN DNS takes precedence over system-wide encrypted DNS | WWDC20 session 10047 |
| Reports of iOS 16+ overriding a VPN's cleartext DNS with DoT/DoH | Apple Developer Forums thread 720428 — **[uncertain]**, no Apple acknowledgment |
| AGH ships "HaGeZi's Encrypted DNS/VPN/TOR/Proxy Bypass" (filterId 52) in its list chooser | AGH filter registry; list description states prevention is not guaranteed |
| Domain-based DoH blocking requires ports 53/853 to be handled first | HaGeZi README |

### DigitalOcean / Caddy / Cloudflare

| Fact | Source |
|---|---|
| DO backups: weekly (20%, 4 kept) or **daily (30%, 7 kept)** — daily is a first-class option | DO backups pricing docs |
| Backups fire within a documented 4-hour window → RPO ≈ 7.2 days (weekly) / ~28h (daily) | DO features docs (RPO figures derived) |
| Provider: `backups` bool has **no `ForceNew`**; `backup_policy` accepts `plan` = `daily`\|`weekly`, `weekday`, `hour`; omitting the policy with `backups = true` defaults to **daily** | `terraform-provider-digitalocean` `resource_droplet.go` + registry docs |
| Sub-daily DO frequencies (4h/6h/12h) are **not reachable** via the provider | provider validator |
| `s-1vcpu-2gb` includes 2,000 GiB outbound, pooled at team level; inbound free; **$0.01/GiB** overage | DO droplet pricing; bandwidth billing docs |
| Caddy's default HTTP-01/TLS-ALPN-01 requires DNS to already resolve to the server | Caddy automatic-HTTPS docs |
| The Cloudflare DNS plugin is **not** in official packages or the official Docker image | Caddy module page; install docs |
| `caddy add-package` is experimental and replaces the binary — `apt upgrade` silently reverts it | Caddy docs |
| Plugin needs `Zone.Zone:Read` + `Zone.DNS:Edit` | caddy-dns/cloudflare README — **[uncertain]** whether Zone:Read can be zone-scoped; libdns docs disagree |
| Let's Encrypt: 5 authorization failures per hostname per hour | LE rate limit docs |
| Cloudflare-proxied records carry **HTTP/HTTPS on fixed TCP ports only** — UDP 51820 cannot work | CF network ports docs |
| Generic UDP via Spectrum is Enterprise-only and a paid add-on | CF protocols-per-plan |
| Proxied records lock TTL to 300s auto; **unproxied allows 60s minimum** | CF DNS docs |
| Universal SSL is provisioned but only presented when proxied — irrelevant to a grey-cloud origin | CF SSL docs |

### Not verified — test these yourself

1. Whether Instagram appears in AGH's blocked-services catalogue ([F14](#f14), [F31](#f31))
2. Whether macOS leaks IPv6 outside the tunnel on a dual-stack network, and whether DNS still reaches AGH if it does ([F19](#f19))
3. Whether `cache_ttl_max` rewrites the TTL delivered to clients or only bounds AGH's internal cache ([F17](#f17))
4. Whether Cloudflare `Zone:Read` can be scoped to a single zone ([F20](#f20))
5. Actual OISD NSFW false-positive rate during ordinary browsing ([F27](#f27))

---

## Closing

The plan is a good build plan. The gap is that it has no design document behind it, and the decisions that were locked in the interview were product decisions, not security decisions. Every Blocker in this review is a question that was never asked rather than a question answered wrongly.

The five that matter most, in order:

1. **[F6](#f6) — add a cooling-off period on user-initiated partner removal.** Highest leverage single change in this review. It converts the product's cleanest bypass into a 72-hour conversation, which is the actual mechanism by which accountability software works.
2. **[F1](#f1)/[F3](#f3) — fix the integrity story.** Correct the fail-mode language, measure coverage continuously rather than only during curfews, and alert on actions rather than on inferences from absence.
3. **[F9](#f9) — gate device enrollment before Slice 5 ships.** Otherwise the first stranger to find the site gets VPN egress on your IP and your DigitalOcean account.
4. **[F10](#f10)/[F14](#f14) — redesign the AGH policy mapping now.** It is a document change today and a rewrite of Slices 5–6 later.
5. **[F15](#f15)/[F16](#f16) — handle ports 53 and 853 in Slice 4.** Together or not at all; either one alone is close to useless.

None of this changes the architecture. Go, SQLite, AdGuard Home, WireGuard, Caddy, one droplet, OpenTofu — all still the right calls for this product at this scale. What changes is that the perimeter gets designed before it gets built.

The best evidence that this plan is worth fixing rather than replacing: every finding above lands inside an existing slice. The structure absorbed all of it.







