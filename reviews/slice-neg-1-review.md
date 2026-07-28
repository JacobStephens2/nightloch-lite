# Review — Slice −1 (Security & Policy Design)

**Reviewing:** unstaged working-tree changes as of 2026-07-28  
**Base:** `5f76a17` *Revise plan based on adversarial review*  
**Scope:** docs only — no application code in this change  
**Resolution:** **R1–R11 addressed** in working tree (see SECURITY-POLICY-DESIGN + DECISIONS + shared-understanding + plan). Re-verify before commit if needed.

| File | Change |
|---|---|
| `docs/SECURITY-POLICY-DESIGN.md` | **new**, 354 lines — the Slice −1 deliverable |
| `shared-understanding.md` | +195/−… substantial rewrite of the freeze |
| `DECISIONS.md` | new Slice −1 amendment table; 5 rows marked superseded |
| `plan.md` | Slice −1 marked complete; amendment pointer updated |
| `status.md (was current-decision.md)` | pointer moved to Slice 0 |
| `README.md` | doc index updated |
| `reviews/slice-neg-1-review.md` | this file (was empty) |

---

## Verdict

**This closes Slice −1. It should be committed, with two spec fixes first.**

The change does what Slice −1 was supposed to do: it answers all 19 questions from the adversarial review with concrete defaults rather than deferrals, it corrects the fail-mode language that was factually wrong, and it writes down the AGH rule-generation model that Slices 5–6 would otherwise have had to invent under time pressure. The traceability table (§7) maps design sections back to findings, so the reasoning is auditable rather than asserted.

Two findings are worth fixing before this is committed, because both are in the tighten/loosen predicate — the one piece of this document that becomes executable code in Slice 0, and the piece where a subtle error is a silent bypass rather than a visible bug:

- **[R1](#r1) — the predicate as specified cannot catch allowlist additions**, which contradicts its own worked example and lets a partnered subject self-loosen.
- **[R2](#r2) — the stated composition order and the rule sketch in §4 disagree** about whether an allowlist entry overrides an active curfew. AGH's real precedence matches neither reliably.

Everything else is either a smaller correctness note, a doc-hygiene issue from partially updating `DECISIONS.md`, or a caveat to record rather than fix.

---

## What this change gets right

Worth saying, because the findings below are all corrections:

- **It answered the questions instead of deferring them.** All 19 review §8 questions have a decision, not a "TBD." Several (zero-partner self-loosen, no wholesale baseline disable, enroll gating) are decisions that were genuinely open and are now closed with a rationale.
- **The fail-mode correction is complete and propagated.** "VPN off → open; server down → blackhole" now appears consistently in `shared-understanding.md`'s limits section, the runtime table, `DECISIONS.md` Q6′, and design §5 — and the enroll screen is required to state it. That was the single most important factual fix and it landed everywhere it needed to.
- **§4's "Never do" list is the most valuable half-page in the change.** `filtering_enabled: false`, per-user list copies, reading rules back to compute desired state, `$ctag` for per-user policy, keying `$client` by ClientID — each of those is a plausible implementation choice that would have failed in a specific, hard-to-debug way. Writing them down as prohibitions is exactly right.
- **The integrity model was genuinely restructured**, not relabeled. "Alert on actions, report on coverage" now shows up as four distinct channels in §5 with different delivery mechanisms, and the quiet-peer alarm is correctly demoted rather than deleted.
- **The `guardian` reservation is handled well** — one column, no implementation, explicit "do not conflate." That is the cheapest possible way to keep the option open.
- **Worked examples in §2 are the right medium** for the predicate. Thirteen rows covering timezone change, subdomain narrowing, window splitting, and typo-substitution will catch more implementation bugs than the formal statement alone.

---

## Findings

### R1

**The predicate cannot detect allowlist additions, contradicting its own worked example.** — **High** · `docs/SECURITY-POLICY-DESIGN.md` §2

Three statements in §2 are mutually inconsistent:

| Line | Statement |
|---|---|
| `:95` | Domains(P) includes "All **allowlist** domains (allowlist *removes* blocks)" |
| `:96` | "Global OISD is **not** part of subject-editable P for predicate purposes" |
| `:123` | Worked example: "Add allowlist `cdn.example.com` → **No** (loosen vs global/baseline)" |

Trace the predicate for a domain blocked *only* by the global OISD list:

- `P_old(cdn.example.com, t)` — OISD is excluded from P per `:96`, and there is no curfew or extra-block covering it → **allowed**
- `P_new(cdn.example.com, t)` — the allowlist removes a block that P does not model → **allowed**
- `allowed ⊒ allowed` holds at every breakpoint → **predicate passes**

So the subject may self-approve an OISD allowlist entry while partnered. That contradicts the worked example at `:123`, contradicts §1 Q9 ("user requests → partner approves"), and defeats the mechanism that §1 Q7 chose as the *only* per-user baseline control. Since allowlisting is now the sole path to loosening the baseline ([F10](../adversarial-review.md#f10)/[F27](../adversarial-review.md#f27)), an unguarded allowlist is an unguarded baseline.

The root cause is that `:96` excludes global-list membership from P for what looks like a performance reason — avoiding a 150k-domain evaluation.

**Fix — make P total by evaluating global-list membership lazily.** The predicate only ever evaluates P at domains in `Domains(P_old) ∪ Domains(P_new)`, which is tens of domains, not 150k. Membership testing *those* domains against the loaded list is cheap. Change `:96` to:

> Global list membership **is** part of `P`, evaluated on demand for domains in the comparison set. The subject cannot edit the list itself; they can only add allowlist entries, which the predicate correctly classifies as loosening.

This makes the worked example at `:123` fall out of the predicate rather than being asserted alongside it, and it removes the need for any allowlist special-case.

An acceptable weaker fix, if you prefer to keep P list-free: state that **allowlist additions bypass the predicate and are always partner-gated by construction**. That works, but it means the predicate is no longer the single authority on tighten-vs-loosen, which was the property that made [F18](../adversarial-review.md#f18) worth adopting.

---

### R2

**The stated composition order and §4's rule sketch disagree about allow-vs-curfew precedence.** — **High** · §2 `:100-106`, §4 `:251-262`

§2 states a name is blocked if:

```
1. matches a subscribed global list, AND
2. does not match an effective allow for that client, OR
3. matches a curfew or extra block active at t
```

Read with standard precedence this is `(1 ∧ 2) ∨ 3` — **a curfew block wins even when an allow exists.** The allowlist carves out the global list only.

§4's generated-rule sketch produces the opposite:

```
@@||allowed.example^$client=10.66.0.N
||instagram.com^$client=10.66.0.N
```

In AdGuard rule semantics, `@@` exception rules override blocking rules unless the blocking rule carries `$important`. The sketch has no `$important`, so **an allowlist entry beats a curfew** — the reverse of §2's stated intent.

This is not hypothetical. The intended flow is "user hits a false positive → partner approves an allowlist entry." If that entry later overlaps a curfew target (or a shared CDN domain the curfew depends on), the allowlist silently punches a hole in the curfew, and nothing in the alert model surfaces it — the loosen alert fired weeks earlier for a different, benign-looking reason.

The document hedges this at `:106` — *"Exact AGH evaluation order is enforcer-defined; reconciler must generate rules that realize this intent."* But the intent itself is what is ambiguous. Deferring to the enforcer here means the enforcer decides a security question.

**Fix — decide it explicitly, and it is expressible either way:**

- If curfews should win (recommended — a curfew is a deliberate, scheduled restraint; an allowlist entry is a fix for a broken page): emit curfew and extra-block rules with **`$important`**, and say so in §4's sketch. `important` is in AGH's supported modifier set.
- If allows should win: keep the sketch and rewrite §2's composition to `(1 ∨ 3) ∧ ¬2`.

Either way, scope the decision in §2, show the modifier in §4, and add a worked example. Also worth adding a rule to the approval flow: **flag at approval time when a requested allowlist domain overlaps an existing curfew target**, so the partner is approving with full information rather than discovering the interaction later.

---

### R3

**`H = 14 days` is unsound unless the data model forbids policy effective beyond the horizon.** — Medium · §2 `:79-87`

The predicate checks at-least-as-restrictive over `[now, now + 14d]`. That is sufficient **only if** no policy element can change state for the first time after `now + 14d`. The document never constrains the data model to guarantee that.

If curfews (or allowlist entries, or baseline extras) can carry an effective date, an end date, or any non-weekly recurrence, then a subject can make an edit whose loosening begins on day 15 and the predicate will pass it. "Add an end date to my Instagram curfew, 30 days out" is both a natural feature request and a clean bypass.

The DST reasoning at `:130` has the same shape: the claim that DST needs no special handling is correct *for transitions inside the horizon*. A timezone edit made on 20 October does not reach the November transition within 14 days.

**Fix — add an explicit soundness constraint to §2:**

> Policy elements are restricted to schedules that are fully periodic within `H` (daily or weekly windows). No effective-from, effective-until, or one-shot dates. Any future extension that violates this requires revisiting the horizon.

Then add a property test asserting the constraint holds for every policy the model can represent — which is the kind of invariant that only stays true if something checks it. If future-dated policy is ever wanted, the correct move is comparing at the union of breakpoints over *all* time (still finite for periodic policies) rather than lengthening `H`.

---

### R4

**Last-device revocation cooling-off blocks the stolen-device case.** — Medium · §1 Q5, §3 cooling-off list

The rule: revoking a non-last device is immediate; revoking the **last** device carries a 72-hour cooling-off as a perimeter shrink.

Two problems.

**It breaks the case where speed matters most.** A user whose only enrolled phone is lost or stolen cannot revoke its peer for 72 hours. The thief holds a working full-tunnel config bound to that user's client IP — meaning their traffic is attributed to the user, appears in the user's query log, and is shown to the user's partner. The one revocation that is genuinely urgent is the one the rule delays.

**It buys almost nothing.** The delay is meant to prevent perimeter escape, but the same escape is available instantly and invisibly by toggling the VPN off — which the design already accepts as the honest fail-open path ([F1](../adversarial-review.md#f1)). Adding a 72-hour delay to the slow, visible, logged path while the fast, invisible path stays open is friction without protection.

In fairness, this came from my own [F18](../adversarial-review.md#f18) recommendation, which reasoned from perimeter symmetry and did not consider device loss. The reasoning was wrong.

**Fix:** make all subject-initiated device revocation immediate. Handle the perimeter concern where it actually belongs — in the coverage report, which §5 already specifies. Zero enrolled devices is a coverage of 0%, visible to partners on the next report, plus an immediate action alert on the revocation itself. That is the same signal, without the hostage-taking.

Keep cooling-off for edge termination and account deletion, where there is no equivalent instant bypass and the delay does real work.

---

### R5

**Log-sharing disable is a subject-initiated perimeter shrink but is the only one without cooling-off.** — Medium · §2 `:139`, `shared-understanding.md` log-sharing section

§2's "outside the predicate" table classifies log-sharing toggle as: *"Subject may disable (email partner); not a P() edit."* That is inherited unchanged from the original Q35–36 decision.

But this change establishes a principle — **subject-initiated reductions of partner capability get a 72-hour cooling-off** — and applies it to edge termination, account deletion, and last-device revocation. Log-sharing disable is the same category: it unilaterally and immediately removes a partner power, with notification arriving after the fact. Under [F6](../adversarial-review.md#f6)'s logic ("notification without delay is a receipt, not a control"), it should either get the delay or get an explicit justification for why it is different.

There is a reasonable argument that it *is* different: log sharing is consent to surveillance, and consent should be instantly revocable on principle, even at the cost of the accountability property. That argument is defensible and may well be the right call. But it should be **written down as a deliberate exception**, not left as an unexamined inheritance — otherwise the next reader cannot tell whether the principle was applied and rejected, or simply not applied.

**Fix:** add one row to §3's cooling-off section stating that log-sharing disable is intentionally exempt, with the consent rationale. Or apply the delay. Either is fine; silence is not.

---

### R6

**`DECISIONS.md` base tables are only partially updated, and the absence of a supersede marker now reads as "still valid."** — Medium · `DECISIONS.md`

The amendment table header says it supersedes conflicting rows below, which is a real precedence rule. But five rows were *also* marked inline (`Q6`, `Q13`, `Q22`, `Q34`, `Q39` → "**Superseded by …**"), and once some rows carry markers, an unmarked row reads as unaffected. Four rows are now stale and unmarked:

| Row | Current text | Contradicted by |
|---|---|---|
| `:96` **Q11** Asymmetry | "Restricted may tighten; only **partner** may loosen" | Q11′ (zero-partner self-loosen) and F18 (predicate, not a blanket rule) |
| `:109` **Q21** Reconcile cadence | "Every **5 minutes** + immediate on write" | `shared-understanding.md` runtime table and design §4: boundary scheduler + 60s early + 5-min *backstop* |
| `:85` **Q33** Query log retention | "AGH retention **~7 days**" | Design §4: "explicitly 7 days" — the AGH *default* is 90, so this must be set, not assumed |
| `:87` **Q38** WG network | "**IPv6 off**" | Design §1 Q17: IPv6 is an open question pending the Slice 4 dual-stack test |

Q11 is the one that matters. It is the single most quoted line in the whole project — the one-sentence statement of what the product does — and as written it is now wrong in two independent ways.

Q50 (NAT) is also incomplete relative to the REDIRECT :53 / REJECT :853 requirements, though the F15/F16 amendment row covers it adequately.

**Fix:** mark all four with the same `**Superseded by …**` convention used elsewhere. Q38 should read "**Open — pending Slice 4 dual-stack test**" rather than pointing at a decision, since there isn't one yet. Five minutes of work, and it makes the "unmarked means current" reading safe.

---

### R7

**Four documents now state the same facts with no global precedence rule.** — Medium · repo-wide

After this change, the 72-hour cooling-off is stated in `shared-understanding.md`, `DECISIONS.md`, `docs/SECURITY-POLICY-DESIGN.md`, and `plan.md`. So are the enroll gate, the fail modes, the allowlist model, and the staging hostname. Each document declares a different kind of authority:

- `shared-understanding.md` — "canonical freeze"
- `docs/SECURITY-POLICY-DESIGN.md` `:7` — "authoritative for perimeter, policy classification, AGH mapping, fail modes, and integrity"
- `DECISIONS.md` — amendment table "supersedes conflicting interview rows below"
- `plan.md` — "adopts the review defaults"

Those overlap without a stated order. [R6](#r6) is the first instance of the resulting drift, one commit in.

**Fix:** put a single precedence line at the top of each document, e.g.:

> **Precedence:** `SECURITY-POLICY-DESIGN` (for perimeter/policy/AGH/fail/integrity) → `shared-understanding` (product freeze) → `DECISIONS` (history + amendments) → `plan` (sequencing). On conflict, the leftmost wins and the others are bugs.

And prefer linking over restating. `shared-understanding.md`'s policy table already does this well — it states the model and links to design §2 for the predicate. The partner-lifecycle table, by contrast, restates cooling-off rules in full, which is where the next drift will happen.

---

### R8

**The dogfood configuration cannot validate the asymmetry — only the mechanics.** — Low · caveat, not a defect

The dogfood plan is two accounts on two mailboxes, both controlled by one person. Under this design, the partner may loosen the subject's policy without restriction. So during dogfood, every restraint in the system is one inbox switch away, with no delay and no meaningful alert — the partner-add alert and roster ([F5](../adversarial-review.md#f5)) both go to the same human.

This is expected and not worth fixing. It is worth **recording**, because a successful dogfood week will otherwise read as evidence the security model works, when it will only be evidence the *plumbing* works. The mechanisms that need real validation — cooling-off actually delaying someone, a partner-add alert reaching someone who did not expect it — cannot be tested until there is a second person.

Related and worth noting in the same place: **mutual partner edges make the asymmetry cosmetic by mutual consent.** If A and B are partners for each other, either can loosen the other's policy on request. That is inherent to peer accountability (Covenant Eyes has the same property) and is not a flaw, but it should be stated in `shared-understanding.md`'s honest-limits list alongside the DNS-only and encrypted-DNS caveats. It is exactly the kind of limit this project has been good about naming.

**Fix:** add a line to Slice 11's dogfood checklist — "asymmetry and cooling-off are unvalidated until a second human holds the partner account" — and a bullet to the honest-limits section about mutual edges.

---

### R9

**`ENROLL_ALLOWLIST` as an env var means adding a user requires a deploy.** — Low · §1 Q6

The enroll gate is the right call and closes [F9](../adversarial-review.md#f9). The env-var mechanism is fine for v1 — it is the smallest thing that works, and at dogfood scale the allowlist changes approximately never.

Note the operational consequence: under `systemd EnvironmentFile`, adding a person is an edit plus a service restart. Given [F4](../adversarial-review.md#f4) (restarts are a connectivity event for anyone on the tunnel) and [F25](../adversarial-review.md#f25) (health-gated deploys), that is a slightly heavier action than it appears.

**Fix:** none needed now. Record the intended v2 shape — an `enrollment_allowed` boolean on the user row, operator-togglable from a UI — so it is a known follow-up rather than a rediscovery.

---

### R10

**Small specification ambiguities in §3.** — Low · `docs/SECURITY-POLICY-DESIGN.md` §3

Three items that will produce divergent implementations if left as-is:

1. **`:174`** — "Invite accepted | All partners of subject including new one optional; at least all **prior** partners." The phrase "including new one optional" is not parseable as a requirement. State it: *"all prior partners (required); the new partner (optional)."*
2. **`:162`** — the lifecycle diagram's `invite_sent → (7d expiry)` arrow has no target state. Should terminate in `expired`.
3. **No `declined` state.** An invitee can presumably decline rather than let the invite lapse. If so, add the state and decide whether declining notifies the subject; if not, say invites can only be accepted or expire.

---

### R11

**`noreply@nightloch.app` is a poor local-part for this product.** — Low · §6

§6 offers `noreply@nightloch.app` or `login@nightloch.app`, local-part flexible.

Every email this system sends is either a login link or an integrity alert to a partner. Partners are exactly the people who may want to reply — "what does this mean?", "is this normal?", "I did not authorize this." A `noreply` address teaches them their reply went nowhere, at the moment they are most engaged. Some filters also treat `noreply` senders slightly less favorably, which matters given that deliverability *is* the integrity model ([F29](../adversarial-review.md#f29)).

**Fix:** prefer a monitored address. `nightloch@nightloch.app` or `alerts@nightloch.app` routed to your inbox costs nothing and preserves the reply path.

---

## Verification notes

Checks run against the working tree, so these are not assumed:

- **Every review §8 question is answered.** All 19 appear in design §1 with a decision, none deferred.
- **Ops findings are not missing — they live in `plan.md`.** `swap`, `unattended`, `VACUUM`, `forced-command`, `console`, and the restore drill all appear there ([F4](../adversarial-review.md#f4), [F22](../adversarial-review.md#f22), [F24](../adversarial-review.md#f24), [F30](../adversarial-review.md#f30), [F32](../adversarial-review.md#f32)). The design doc correctly scopes itself to the five topics it claims and does not duplicate them.
- **Token hashing ([F21](../adversarial-review.md#f21))** appears in `shared-understanding.md`, `DECISIONS.md`, and `plan.md` — absent from the design doc, which is correct given its stated scope.
- **`$important` is available** in AGH's modifier set (`client`, `denyallow`, `dnstype`, `dnsrewrite`, `important`, `badfilter`, `ctag`, `respgeo`), so [R2](#r2)'s recommended fix is expressible.
- **Design §8's completion checklist is accurate** — including the final two items, which I verified by diffing the tracked files rather than trusting the checkboxes.

---

## Before committing

Blocking:

1. **[R1](#r1)** — make global-list membership part of `P`, evaluated lazily over the comparison set. Without this the predicate does not do the job it was adopted for.
2. **[R2](#r2)** — decide allow-vs-curfew precedence, add `$important` to §4's sketch if curfews win, and add a worked example.

Worth doing in the same commit (all small):

3. **[R3](#r3)** — add the periodicity constraint that makes `H = 14d` sound.
4. **[R6](#r6)** — mark Q11, Q21, Q33, Q38 superseded or open.
5. **[R4](#r4)** — drop the last-device cooling-off.
6. **[R5](#r5)** — record the log-sharing exemption as deliberate, or apply the delay.

Follow-ups, not blocking: [R7](#r7) precedence header, [R8](#r8) dogfood caveat, [R10](#r10) §3 wording.

Once [R1](#r1) and [R2](#r2) are settled, Slice 0 has what it needs: the predicate is the first real code, and it is now specified precisely enough to write property tests against — which was the whole point of doing Slice −1 before touching a Go file.
