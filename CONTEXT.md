# Nightloch Lite

Partner-locked DNS protection: a Protectee’s Policy is enforced on their Devices; Partners may loosen what the Protectee cannot while a Partnership is active.

## Language

**User**:
A person with a Nightloch login identity. One User may be Protectee of their own Policy and Partner in zero or more Partnerships.
_Avoid_: Account (for the person), member, customer

**Protectee**:
A User considered as the owner of a Policy and Devices—the party under restraint. Every User is the Protectee of their own Policy (even with no Partnerships and no Devices).
_Avoid_: Subject, restricted user, client, account (for this role)

**Partner**:
A User who, via an active Partnership, may loosen that Partnership’s Protectee’s Policy and exercise the other partner powers for that relationship.
_Avoid_: Accountability partner, peer (for this role), supervisor, guardian (unless edge_type is guardian later)

**Roster**:
The set of Partners who currently protect a given Protectee (visible to those Partners; they do not edit one another’s Partnerships).
_Avoid_: Accountability partners (as a type name), circle, partner list (ambiguous with “people I protect”)

**Partnership**:
A durable relationship created when a Partner invitation is accepted, binding one Partner to one Protectee through its active, Cooling-off, and ended states.
_Avoid_: Partner edge, peer edge, link, connection (for this relationship)

**Partner invitation**:
A pending invitation from a Protectee to another User to form a Partnership. It grants no Partner powers until accepted.
_Avoid_: Partner request, edge invite, relationship request

**Invitation block**:
A User’s refusal to receive future Partner invitations from a specific other User. It creates no Partnership and grants no Partner powers.
_Avoid_: Blocked Partner, blocked Partnership, email block

**Reciprocal Partnerships**:
Two independent active Partnerships in opposite directions between the same Users, with each User protecting the other as a Partner. The Partnerships do not merge or change one another’s powers.
_Avoid_: Mutual edge, two-way Partnership, merged Partnership

**Partner lock**:
The restraint active while a Protectee has at least one active Partnership: the Protectee may tighten their Policy but only a Partner may loosen it.
_Avoid_: Hard mode, locked account, partner mode

**Partner reachability**:
Nightloch’s known ability to deliver Action alerts to a Partner’s verified email. It describes mail delivery, not whether the Partner is online or attentive. It is derived from Delivery events and is a visible warning only: an unreachable Partner keeps every power, and no lifecycle deadline pauses.
_Avoid_: Online status, Partner availability, email health (alone)

**Delivery event**:
One report from the mail provider about a message Nightloch already handed over — sent, delivered, delayed, bounced, or complained. It reports on exactly one recipient’s message, arrives asynchronously and possibly out of order, and changes only Partner reachability and Delivery state.
_Avoid_: Webhook (in product language), email status, bounce report

**Device**:
An enrolled tunnel identity for a Protectee (keys, address, and enrollment lifecycle)—not the human and not the Partnership.
_Avoid_: Peer, WireGuard peer (in product language), client (for this concept)

**Policy**:
The Protectee’s complete desired DNS restraint: Extra blocks, Allowlist entries, and Curfews. Global Baseline lists apply to all Devices but are not themselves editable Policy content.
_Avoid_: Ruleset, config, profile (for this whole)

**Curfew**:
A timed block of one or more domains for a Protectee.
_Avoid_: Schedule (alone), rule (alone), timed policy

**Baseline list**:
A global always-on subscribed block list (e.g. OISD NSFW) applied to all Devices.
_Avoid_: Baseline (alone), porn list, global policy

**Extra block**:
A domain the Protectee added as an always-on block on top of Baseline lists.
_Avoid_: Baseline extra, custom baseline, always-on curfew

**Allowlist entry**:
A domain exception that carves out Baseline lists only—not Curfews.
_Avoid_: Exception, false-positive fix, whitelist, allow (alone)

**Log sharing**:
A per-Partnership permission from a Protectee that lets that Partner view the Protectee’s DNS domain log. Disabling it immediately removes that access without ending the Partnership.
_Avoid_: Log access (alone), DNS sharing, surveillance toggle

**Coverage**:
The measured fraction of time a Device was on the tunnel while enrolled.
_Avoid_: Quiet peer, uptime (alone), protection %

**Tighten**:
A Policy change that is at least as restrictive as before (never removes blocking for any domain/time in the comparison horizon).
_Avoid_: Strengthen, hardmode

**Loosen**:
A Policy change that removes blocking for some domain and time relative to before—requires a Partner while any Partnership is active (Protectee may self-loosen only when unpartnered).
_Avoid_: Unlock, relax, disable protection (for a partial edit)

**Cooling-off**:
A fixed delay after a Protectee requests ending a Partnership or deleting their User, during which Policy stays enforced and Partners keep their powers.
_Avoid_: Grace period, pending delete (alone), quarantine (for people)

**Operator**:
The human who runs the Nightloch deployment (infrastructure, enroll allowlist, AGH admin)—not a product role on a Partnership.
_Avoid_: Admin (in product UI), superuser, root (for the person)

**Enrollment allowlist**:
The Operator-controlled set of Users permitted to enroll Devices (v1 gate against open VPN exit).
_Avoid_: Invite-only (alone), beta list, device allowlist (confusable with Allowlist entry)

**Mint**:
The Operator act of creating a Device from the command line: allocate its tunnel address, record it, add its tunnel peer, and hand over its configuration once. Distinct from **enroll**, which is a User doing it for themselves through the product once the Enrollment allowlist opens.
_Avoid_: Provision, issue, generate a peer, enroll (when the Operator is the actor)

**Remove** (a Device):
The act of ending a Device record and withdrawing its tunnel peer, performed by a Protectee, a Partner, or the Operator. The Device’s configuration stops working; its tunnel address goes into Quarantine rather than back into the pool.
_Avoid_: Revoke, delete a peer, deactivate, unenroll

**Quarantine** (of a tunnel address):
The period after a Device is removed during which its tunnel address cannot be allocated to another Device. It runs from the removal instant for longer than the query-log retention, so a later occupant of the address can never resolve to the previous occupant’s retained queries. Applies to addresses only—never to people.
_Avoid_: Cooldown, hold, reserved, grace period

**Enforcement plane**:
The two things that make a Policy true on a Device: the tunnel interface carrying the Device, and the **Resolver** filtering its DNS. Reported by `/health` as one named fact each, so a resolver that is running but not filtering is distinguishable from one that is enforcing.
_Avoid_: The stack, the backend, enforcement layer

**Resolver**:
AdGuard Home in the role Nightloch uses it for - the enforcer that answers a Device's DNS and applies the Baseline list. **AdGuard Home** names the product; **Resolver** names its place in the enforcement plane, and is the term the code uses for the seam it sits behind.
_Avoid_: DNS server (when the enforcing role is meant), filter, upstream

**Action alert**:
An immediate integrity email about a discrete event (loosen, enroll, Partnership change, cooling-off start, etc.).
_Avoid_: Notification (alone), digest, quiet-peer alert (as the main integrity path)

**Activity**:
The human-readable history of attributable changes affecting a Protectee, visible to that Protectee and authorized Partners.
_Avoid_: Audit log (in product UI), event stream, history (alone)

**Deleted User**:
Who a User whose Nightloch User has been deleted is in every retained product view. Their identity is replaced everywhere a view reads it, while the authorized action and its exact time survive on another Protectee’s Activity and on a former Partner’s receipt. Email Nightloch already delivered still names the address that identified them at the time and cannot be recalled or pseudonymized.
_Avoid_: Anonymous user, removed user, former user, redacted

**Activity category**:
The fixed vocabulary Activity is filtered by — **Invitations**, **Partnerships**, **Cooling-off**, **Delivery**, and **Devices** — assigned per stable action rather than per sentence, so wording can change without changing what a filter means.
_Avoid_: Type, tag, event class, log level

**Host posture**:
The host's persistent forwarding and DNS configuration - the nftables table, the forwarding sysctl, the unit that loads them at boot, and the wg0 interface configuration - as opposed to any command typed to produce the same effect once. Posture is a property of the files, so an unattended reboot restores it. [ADR 0006](./docs/adr/0006-keep-the-nat-posture-in-its-own-nftables-table.md).
_Avoid_: Firewall rules (alone), iptables config, network setup

**Stage / Activate / Revert**:
The three acts of `deploy/nightloch-host-posture.sh`, deliberately separate. **Stage** installs the posture and generates the interface key while enabling and forwarding nothing, and an agent may do it. **Activate** is the first forwarding change on a live host, is a human's, and refuses without the checklist confirmation. **Revert** puts the host back and needs no operator, so a self-cancelling timer can run it.
_Avoid_: Deploy / enable / rollback (for this posture), install (for activate)

**Named fact**:
One thing `deploy/nightloch-nat-verify.sh` or `/health` reads back and reports by name with its cause, rather than folding into a single pass or a status code. A named fact may govern the outcome or deliberately not - a resolver that has not been installed yet is reported without failing a correct posture.
_Avoid_: Check (alone, where governance matters), assertion, probe

**Coverage report**:
A periodic summary of Coverage for a Protectee’s Devices, sent to Partners (and useful as a deliverability heartbeat).
_Avoid_: Digest (unless meaning this report), weekly email (alone)

## Product UI copy

- **Protectee side:** “Your Partners” / Roster of who protects you.
- **Protectee area:** “Your protection” / the Protectee’s own Partner lock, Policy, Devices, and Activity.
- **Partner side:** “People you protect” (not the Roster of someone else unless viewing that Protectee).
- **Roster** may appear as a section title where Partners see who else protects the same Protectee.
- **Partner lock** is active with one or more active Partnerships and inactive with zero.

## Code note

Identifiers such as `SubjectMayApplyEdit` / `subject_user_id` may lag the glossary; prefer **Protectee** in new names when touching authZ and schema.
