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
An active relationship binding one Partner to one Protectee (invite → accept → end / cooling-off).
_Avoid_: Partner edge, peer edge, link, connection (for this relationship)

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

**Action alert**:
An immediate integrity email about a discrete event (loosen, enroll, Partnership change, cooling-off start, etc.).
_Avoid_: Notification (alone), digest, quiet-peer alert (as the main integrity path)

**Coverage report**:
A periodic summary of Coverage for a Protectee’s Devices, sent to Partners (and useful as a deliverability heartbeat).
_Avoid_: Digest (unless meaning this report), weekly email (alone)

## Product UI copy

- **Protectee side:** “Your Partners” / Roster of who protects you.
- **Partner side:** “People you protect” (not the Roster of someone else unless viewing that Protectee).
- **Roster** may appear as a section title where Partners see who else protects the same Protectee.

## Code note

Identifiers such as `SubjectMayApplyEdit` / `subject_user_id` may lag the glossary; prefer **Protectee** in new names when touching authZ and schema.
