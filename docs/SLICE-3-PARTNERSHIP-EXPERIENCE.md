# Slice 3 Partnership experience

**Status:** Experience grill complete; shared understanding confirmed 2026-08-23; canonical copy approved through Q86, with the ending body's closing link amended 2026-08-23 (see [Recipient-specific link](#recipient-specific-link))  
**Language:** [CONTEXT.md](../CONTEXT.md)  
**Decisions:** [DECISIONS.md](../DECISIONS.md), S3-01 onward  
**Privacy boundary:** [ADR 0001](./adr/0001-bound-partner-activity-by-partnership-lifetime.md)  
**Delivery boundary:** [ADR 0002](./adr/0002-commit-partnership-state-independently-from-email-delivery.md)

The plain-text templates below are canonical. HTML mail must mirror them without changing meaning. Every `*_utc` value renders as an explicit UTC date and time. Product links open review screens; mutations require an authenticated, CSRF-protected confirmation POST.

## Delivery increments

Slice 3 ships as three vertical increments:

1. **3A — First Partnership:** complete zero-to-one invitation, acceptance, Partner lock, Activity, mail, and both ending paths. The final many-to-many schema is used, but one active-or-pending Partner per Protectee is temporarily enforced.
2. **3B — Full Roster:** lift that gate only after existing-Partner invitation/acceptance alerts, multiple pending invitations, Reciprocal Partnerships, concurrent Cooling-off, and delivery warnings work end to end.
3. **3C — Audit and abuse hardening:** complete Activity browsing and visibility tests, ended receipts, pseudonymization, reachability recovery, and adversarial tests.

Each increment may deploy to staging and receive its own demo. None alone changes Slice 3’s final experience decisions, and Slice 3 is not complete until 3C.

## Partner invitation

**Subject:** `{{protectee_email}} invited you to be their Nightloch Partner`

```text
{{protectee_email}} invited you to become their Partner in Nightloch.

Nightloch is partner-locked DNS protection. It can block domains on a
Protectee’s enrolled Devices. While a Protectee has active Partners, they
cannot loosen their own Policy; their Partners can.

A Protectee can still turn off the VPN on a Device, which removes protection.

If you accept, you can:
- view Coverage and enforcement health;
- tighten or loosen {{protectee_email}}’s Policy;
- approve Allowlist entries;
- revoke their Devices;
- view their Roster and Activity; and
- view their DNS domain log only when Log sharing is on.

Log sharing: {{On — you will receive domain-log access after acceptance. |
Off — you will not receive domain-log access.}}

{{protectee_email}} currently has {{partner_count}} active Partner(s).
Their identities will be shown after you accept.

Nightloch has told those current Partners that this invitation was sent to
{{invitee_email}}. If you accept, your verified email will remain visible to
current and future Partners in the Roster and Activity.

You may end the Partnership immediately. If {{protectee_email}} requests to
end it, the Partnership remains active for 72 hours unless they cancel the
request or you agree to end it sooner.

Review Partner invitation:
{{review_url}}

This invitation expires {{expires_at_utc}}.

If you did not expect this invitation, do not accept it. Contact
{{protectee_email}} directly.

Replies to this email go directly to {{protectee_email}}.
```

### Reciprocal invitation

> Added by #10. The frozen copy above disclosed the reciprocal effect only to
> the Protectee's current Partners. This section extends it to the invited
> User; nothing above it changed.

S3-04 requires the reciprocal effect to be disclosed when inviting, and the
invitee is the party it lands on. When the inviting Protectee already protects
the invited address, insert the paragraph below directly after the Roster
paragraphs and keep the rest of the message identical. The invitation reaches
the invited address alone, and that address is already inside the reverse
Partnership, so this discloses nothing new to anyone.

```text
{{protectee_email}} already protects you as your Partner. Accepting creates a
second, independent Partnership in the opposite direction, so you would be
their Partner too. The two Partnerships do not merge: each keeps its own Log
sharing, its own ending, and its own powers, and ending one does not end the
other.
```

Omit the paragraph entirely in the ordinary direction. There is no second
Partnership to describe, and a bare `Reciprocal Partnerships: No` would only
prompt the reader to wonder what merged authority they had just avoided.

The **Invitation landing page** carries the same paragraph under a
**Reciprocal Partnerships** heading, shown only once the invited User has
authenticated. A bearer link alone never reveals the reverse direction, for the
same reason it never reveals the invited address.

### Zero current Partners

When `partner_count` is zero the Roster paragraphs above would assert two
falsehoods — that identities will be shown and that current Partners have been
told. Replace both paragraphs with the following, and keep the rest of the
message identical. Every other `partner_count` renders the paragraphs above as
written.

```text
{{protectee_email}} currently has 0 active Partners.

If you accept, your verified email will remain visible to current and future
Partners in the Roster and Activity.
```

## Invitation landing page

```text
Review Partner invitation

{{protectee_email}} invited {{masked_invitee_email}} to become their
Nightloch Partner. Viewing this page does not accept the invitation.

[What Nightloch does]
Nightloch is partner-locked DNS protection. It can block domains on enrolled
Devices. While someone has active Partners, only those Partners can loosen
their Policy. Turning off a Device’s VPN removes protection.

[What you can do as a Partner]
<same powers listed in the invitation email>

[Who can see you]
{{partner_count}} other Partner(s) currently protect {{protectee_email}}.
They have been told this invitation was sent to your email. If you accept,
you will see their verified emails and they will continue to see yours.

[Log sharing]
{{exact On/Off explanation}}

[Ending the Partnership]
You may end immediately. A request by {{protectee_email}} takes 72 hours,
during which you keep your Partner powers.

[Accept Partnership]  [Decline invitation]
```

If authentication is needed:

```text
Sign in as {{masked_invitee_email}} to respond
```

For the wrong session:

```text
You are signed in as {{current_email}}, but this invitation was sent to
{{masked_invitee_email}}. Sign out and continue with the invited address.
```

Keep the invited address masked until the matching User authenticates.

## Invitation sent Action alert

Sent separately to every current Partner.

**Subject:** `[Nightloch] {{protectee_email}} invited {{invitee_email}} as a Partner`

```text
{{protectee_email}} sent a Partner invitation to {{invitee_email}} on
{{occurred_at_utc}}.

No Partner powers have been granted yet. If accepted, {{invitee_email}} will
be able to loosen {{protectee_email}}’s Policy, revoke their Devices, and
exercise the other Partner powers shown in Nightloch.

Log sharing after acceptance: {{On | Off}}
Reciprocal Partnerships if accepted: {{Yes | No}}
Invitation expires: {{expires_at_utc}}

You can observe this invitation in Activity, but you cannot accept, cancel,
or decline it for either person.

Review Activity:
{{activity_url}}

If this invitation is unexpected, contact {{protectee_email}} directly.

This is an integrity message from Nightloch. Replies go to the monitored
address alerts@nightloch.app.
```

## Invitation accepted

### Protectee and prior Partners

**Subject:** `[Nightloch] {{new_partner_email}} now protects {{protectee_email}}`

```text
{{new_partner_email}} accepted {{protectee_email}}’s Partner invitation on
{{accepted_at_utc}}.

The Partnership is now active. {{new_partner_email}} can loosen
{{protectee_email}}’s Policy, revoke their Devices, and exercise the other
Partner powers shown in Nightloch.

Log sharing: {{On | Off}}
Reciprocal Partnerships: {{Yes | No}}
Active Partners now: {{partner_count}}

{{If first Partner: The Partner lock is now active.}}
{{Otherwise: The Partner lock remains active.}}

Review the Roster and Activity:
{{protectee_url}}

This is an integrity message from Nightloch. Replies go to the monitored
address alerts@nightloch.app.
```

### New Partner

**Subject:** `You now protect {{protectee_email}} with Nightloch`

```text
You accepted the Partnership with {{protectee_email}} on {{accepted_at_utc}}.

You can now view their protection status, change their Policy, approve
Allowlist entries, revoke Devices, and view their Roster and Activity.

Log sharing: {{On — you can view their DNS domain log. |
Off — you cannot view their DNS domain log.}}

{{Only when reciprocal:
Reciprocal Partnerships: Yes. {{protectee_email}} already protects you, and
you now protect them. These are two independent Partnerships: ending or
changing one does not change the other, and neither one gives either of you
authority over the other’s Partnership.}}

{{protectee_email}} has {{other_partner_count}} other active Partner(s).
Sign in to see who they are:
{{protectee_url}}

Add alerts@nightloch.app to your contacts so integrity messages are less
likely to be filtered.

You may end this Partnership immediately from Nightloch.

Replies to this message go to the monitored address alerts@nightloch.app.
```

Do not include the full Roster in email.

## Invitation canceled

### Invitee

**Subject:** `{{protectee_email}} canceled your Nightloch Partner invitation`

```text
{{protectee_email}} canceled the Partner invitation sent to you on
{{canceled_at_utc}}.

No Partnership was created and no Partner powers were granted.
```

### Current Partners

**Subject:** `[Nightloch] {{protectee_email}} canceled the invitation to {{invitee_email}}`

```text
{{protectee_email}} canceled the pending Partner invitation to
{{invitee_email}} on {{canceled_at_utc}}.

No Partnership was created and no Partner powers were granted.

Review Activity:
{{activity_url}}

This is an integrity message from Nightloch. Replies go to the monitored
address alerts@nightloch.app.
```

## Invitation declined

Sent only to the Protectee.

**Subject:** `{{invitee_email}} declined your Nightloch Partner invitation`

```text
{{invitee_email}} declined your Partner invitation on {{declined_at_utc}}.

No Partnership was created. You may send a new invitation later.
```

An expired invitation produces Activity only.

## Invitation confirmations

Declining and cancelling each take one consequence-specific confirmation
(S3-46). Both confirmation screens are reads; only the confirmed POST mutates.

### Decline (invitee)

```text
Decline this Partner invitation?

Declining {{protectee_email}}’s invitation creates no Partnership and grants
no Partner powers. Nightloch will tell {{protectee_email}} that you declined.

This cannot be undone. {{protectee_email}} can send you a new invitation later.

[Decline invitation]  Keep reading the invitation
```

### Cancel (Protectee)

```text
Cancel this Partner invitation?

{{invitee_email}} has not answered yet. Cancelling creates no Partnership and
grants no Partner powers. Nightloch will tell {{invitee_email}} that you
canceled.

This cannot be undone, and the emailed link stops working. Cancelling is also
how you correct an invitation: the invited address and the proposed Log sharing
cannot be edited, so send a new invitation afterwards.

[Cancel invitation]  Keep the invitation
```

## Invitation receipts

The in-app receipt each initiator gets for a mutation that leaves nothing on
their dashboard (S3-20). Acceptance needs none: the dashboard names the new
Partner and the active Partner lock.

```text
You declined this invitation

You declined {{protectee_email}}’s Partner invitation on {{declined_at_utc}}.
No Partnership was created and no Partner powers were granted.

Nightloch has told {{protectee_email}}. They can send you a new invitation
later.
```

```text
You canceled this invitation

You canceled the Partner invitation to {{invitee_email}} on {{canceled_at_utc}}.
No Partnership was created and no Partner powers were granted.

Nightloch has told {{invitee_email}}. To invite a different address, or to offer
different Log sharing, send a new invitation.

[Add a Partner]
```

## Terminal invitation landing

What a **valid** bearer token or invitation reference shows once the invitation
is no longer pending (S3-29). The invited address stays masked here exactly as
it does on the review screen. A malformed or unknown token, and an id belonging
to someone else, show only the unavailable screen instead.

```text
This invitation was already accepted

{{You | The invited person}} accepted {{protectee_email}}’s Partner invitation
on {{accepted_at_utc}}. The Partnership is active, so there is nothing left to
answer here.
```

```text
This invitation was declined

The invitation from {{protectee_email}} was declined on {{declined_at_utc}}. No
Partnership was created and no Partner powers were granted.

{{protectee_email}} can send a new invitation later.
```

```text
This invitation was canceled

{{protectee_email}} canceled this Partner invitation on {{canceled_at_utc}}. No
Partnership was created and no Partner powers were granted.

Only {{protectee_email}} can send a replacement.
```

```text
This invitation expired

The Partner invitation from {{protectee_email}} expired on {{expires_at_utc}}.
Invitations are valid for seven days and cannot be extended. No Partnership was
created and no Partner powers were granted.

Ask {{protectee_email}} to send a new invitation.
```

For an unknown or malformed reference:

```text
That invitation is unavailable

Nightloch cannot show this Partner invitation. Ask the person who invited you
to send a new one.
```

## Cooling-off started

### Partner being removed

**Subject:** `[Nightloch] {{protectee_email}} requested to end your Partnership`

```text
{{protectee_email}} requested to end your Partnership on
{{requested_at_utc}}.

It is scheduled to end on {{ends_at_utc}}.

Until then, the Partnership remains active and you retain all Partner powers.
{{protectee_email}} may cancel the request. If you agree to end sooner, you
may choose “End now” in Nightloch.

Review the request:
{{partnership_url}}

This is an integrity message from Nightloch. Replies go to the monitored
address alerts@nightloch.app.
```

### Other current Partners

**Subject:** `[Nightloch] {{protectee_email}} requested to end a Partnership`

```text
{{protectee_email}} requested to end their Partnership with
{{affected_partner_email}} on {{requested_at_utc}}.

It is scheduled to end on {{ends_at_utc}}.

Until then, {{affected_partner_email}} remains an active Partner with all
Partner powers. You can observe the countdown, but you cannot cancel it or
end another Partner’s Partnership.

Review the Roster and Activity:
{{protectee_url}}

This is an integrity message from Nightloch. Replies go to the monitored
address alerts@nightloch.app.
```

## Cooling-off canceled

Sent to the retained Partner and every other current Partner.

**Subject:** `[Nightloch] {{protectee_email}} canceled a Partnership ending request`

```text
{{protectee_email}} canceled the request to end their Partnership with
{{affected_partner_email}} on {{canceled_at_utc}}.

The Partnership remains active. No end is scheduled, and
{{affected_partner_email}} retains the same Partner powers.

Review the Roster and Activity:
{{protectee_url}}

This is an integrity message from Nightloch. Replies go to the monitored
address alerts@nightloch.app.
```

## Cooling-off Partnership becomes final

If Roster changes make an already-Cooling-off Partnership the final active Partnership, send this separately to the Protectee and every current Partner.

**Subject:** `[Nightloch] A scheduled Partnership end will deactivate {{protectee_email}}’s Partner lock`

```text
{{ended_partner_email}}’s Partnership with {{protectee_email}} ended on
{{roster_changed_at_utc}}.

As a result, {{final_partner_email}} is now the final active Partner
protecting {{protectee_email}}. That Partnership is already in Cooling-off
and remains scheduled to end on {{ends_at_utc}}. Its deadline has not changed.

If no other Partner accepts an invitation before that deadline, the Partner
lock will become inactive and {{protectee_email}} will be able to loosen
their own Policy.

Until then, {{final_partner_email}} retains all Partner powers and the
Partner lock remains active.

Review the Roster and Activity:
{{protectee_url}}

This is an integrity message from Nightloch. Replies go to the monitored
address alerts@nightloch.app.
```

If another Partner later accepts, append this to the ordinary acceptance alert:

```text
The Partner lock will remain active after {{final_partner_email}}’s
Partnership is scheduled to end on {{ends_at_utc}}.
```

## Partnership ended

### Subjects

```text
Partner ended:
[Nightloch] {{partner_email}} ended their Partnership with {{protectee_email}}

End now:
[Nightloch] {{partner_email}} agreed to end their Partnership with {{protectee_email}} now

Timer completed:
[Nightloch] A Partnership for {{protectee_email}} ended after Cooling-off
```

### Common body

```text
{{event_specific_sentence}}

The Partnership ended on {{ended_at_utc}}.

{{partner_email}} can no longer change {{protectee_email}}’s Policy, revoke
their Devices, view their Roster or ongoing Activity, or exercise any other
Partner power. They retain only a read-only receipt for this ended
Partnership.

{{protectee_email}} now has {{partner_count}} active Partner(s).

{{If zero: The Partner lock is now inactive. {{protectee_email}} can loosen
their own Policy.}}

{{Otherwise: The Partner lock remains active.}}

{{recipient_specific_link}}

This is an integrity message from Nightloch. Replies go to the monitored
address alerts@nightloch.app.
```

### Recipient-specific link

**Amended 2026-08-23 (implementing #7).** The ending body previously closed
with `Review the Roster and Activity: {{protectee_url}}` for every recipient.
That line is false for the one recipient who has just lost Roster and ongoing
Activity access, so the closing link now names what each recipient actually
retains. Nothing else about the common body changed.

```text
Protectee and every remaining Partner:
Review the Roster and Activity:
{{protectee_url}}

The Partner whose Partnership ended:
You keep a read-only receipt for this ended Partnership:
{{receipt_url}}
```

The former Partner's receipt is the only authorized destination they retain
(S3-16), so it is the only one their copy of the message offers.

### Event-specific sentences

```text
Partner ended:
{{partner_email}} ended the Partnership immediately.

End now:
{{partner_email}} agreed to end the Partnership immediately after
{{protectee_email}} requested Cooling-off.

Timer completed:
Nightloch ended the Partnership after the Cooling-off requested by
{{protectee_email}} completed.
```

The zero-Partner consequence is always explicit because it changes who may loosen the Policy.

## Account deletion

### Deletion started

Sent separately to every active Partner protecting the deleting User.

**Subject:** `[Nightloch] {{protectee_email}} requested deletion of their Nightloch User`

```text
{{protectee_email}} requested deletion of their Nightloch User on
{{requested_at_utc}}.

Deletion is scheduled for {{deletes_at_utc}}.

Until then, your Partnership remains active and you retain your Partner
powers. Their Policy remains enforced, and their Devices remain enrolled
unless revoked through the ordinary Device controls.

Only {{protectee_email}} may cancel this request. You cannot approve,
accelerate, delay, or cancel the deletion.

If deletion completes, their Devices will be revoked, your Partnership will
end, and you will lose access to their Roster and Activity. You will retain
only a receipt identifying them as Deleted User.

Review the deletion request:
{{protectee_url}}

This is an integrity message from Nightloch. Replies go to the monitored
address alerts@nightloch.app.
```

### Deletion canceled

Sent separately to every active Partner still protecting the User.

**Subject:** `[Nightloch] {{protectee_email}} canceled deletion of their Nightloch User`

```text
{{protectee_email}} canceled their User-deletion request on
{{canceled_at_utc}}.

Their User is no longer scheduled for deletion. Your Partnership remains
active, and your Partner powers have not changed.

Review their protection:
{{protectee_url}}

This is an integrity message from Nightloch. Replies go to the monitored
address alerts@nightloch.app.
```

### Deletion completed

Sent separately to every Partner who still protected the User at completion.

**Subject:** `[Nightloch] {{protectee_email}} deleted their Nightloch User`

```text
{{protectee_email}}’s Nightloch User was deleted on {{deleted_at_utc}}.

Your Partnership ended at that time. You can no longer change their Policy,
revoke their Devices, view their Roster or Activity, or exercise any other
Partner power.

Nightloch revoked their enrolled Devices and completed the account teardown.

Your retained receipt identifies the former Protectee only as Deleted User:
{{receipt_url}}

Previously delivered emails, including this message, continue to contain
the verified email that identified the User when the event occurred.

This is an integrity message from Nightloch. Replies go to the monitored
address alerts@nightloch.app.
```

### Deleting Partner ended a Partnership

Sent separately to the Protectee, deleting Partner, and the Protectee’s remaining Roster.

**Subject:** `[Nightloch] {{partner_email}} ended their Partnership with {{protectee_email}}`

```text
{{partner_email}} requested deletion of their Nightloch User on
{{requested_at_utc}}.

Nightloch therefore ended their Partnership with {{protectee_email}}
immediately on {{ended_at_utc}}.

{{partner_email}} can no longer change {{protectee_email}}’s Policy, revoke
their Devices, view their Roster or ongoing Activity, or exercise any other
Partner power.

{{protectee_email}} now has {{partner_count}} active Partner(s).

{{If zero: The Partner lock is now inactive. {{protectee_email}} can loosen
their own Policy.}}

{{Otherwise: The Partner lock remains active.}}

Canceling User deletion will not restore this Partnership. Forming another
Partnership requires a new invitation and acceptance.

Review the Roster and Activity:
{{protectee_url}}

This is an integrity message from Nightloch. Replies go to the monitored
address alerts@nightloch.app.
```

### Invitations canceled by account deletion

Tell only the direct parties and relevant Roster members who were already entitled to know about the invitation. Use the standard integrity footer for Action-alert recipients.

When the deleting User was the Protectee who sent the invitation:

- Invitee subject: `{{protectee_email}} canceled your Nightloch Partner invitation`
- Current-Partner subject: `[Nightloch] {{protectee_email}} canceled the invitation to {{invitee_email}}`

```text
{{protectee_email}} requested deletion of their Nightloch User on
{{requested_at_utc}}.

Nightloch therefore canceled the pending Partner invitation to
{{invitee_email}}. No Partnership was created and no Partner powers were
granted.

Canceling User deletion will not restore this invitation.
```

When the deleting User was the invited Partner:

- Deleting-invitee subject: `Your Nightloch Partner invitation from {{protectee_email}} was canceled`
- Protectee/current-Partner subject: `[Nightloch] The invitation to {{invitee_email}} was canceled`

```text
{{invitee_email}} requested deletion of their Nightloch User on
{{requested_at_utc}}.

Nightloch therefore canceled {{protectee_email}}’s pending Partner
invitation to {{invitee_email}}. No Partnership was created and no Partner
powers were granted.

Canceling User deletion will not restore this invitation.
```

## Screen and state requirements

- A Partnership is created only on acceptance and remains the durable relationship record through active, Cooling-off, and ended states. Re-partnering later creates a new Partnership.
- Former parties may form a new Partnership. Its Activity-visibility window begins at the new acceptance; the old Partnership remains ended with a separate receipt, and the returning Partner cannot see the gap.
- A pending Partner invitation is immutable. Resending does not extend its expiry. Changing its invitee or proposed Log sharing requires cancellation and a new invitation.
- A signed-in User sees all pending Partner invitations addressed to their verified email in **Partner invitations** and may review, accept, or decline without the bearer link. The same disclosures and confirmation rules apply.
- The dashboard separates **Your protection** from **People you protect**. A User’s own Partner-lock status remains visible even when they protect other people. Reciprocal Partnerships appear independently in both areas.
- Reciprocal Partnerships are two independent directional Partnerships. Each keeps its own Log sharing, Cooling-off deadline, ending, Partner powers, and receipt, and every Activity entry is attributed to the Protectee and Partnership of its own direction; ending or changing one never mutates the other, and no screen or route lets a User's role in one direction act on the other direction's Partnership. The Activity *visibility window* is a read-side rule and is specified with the Activity read path, which #10 does not build.
- The invited User's own disclosures — the invitation email, the landing page, and their **Partner invitations** row — name the reciprocal effect whenever accepting would create one, and say the two Partnerships do not merge. The reverse direction stays withheld from an unauthenticated bearer-link viewer, exactly as the invited address does.
- Zero active Partners means the Partner lock is inactive. Pending invitations never activate it; say that acceptance is required.
- A Protectee may start Cooling-off on multiple Partnerships concurrently. Each receives its own 72-hour deadline.
- Before Cooling-off starts on the final active Partnership, every relevant screen names the exact deadline and says the Protectee will be able to loosen their own Policy afterward.
- If Roster changes make an already-Cooling-off Partnership the final active Partnership, keep its existing deadline, append Activity, and alert the Protectee and current Roster that the Partner lock will deactivate then. If another Partner accepts before that deadline, the acceptance alert explicitly says the lock will remain active after the scheduled end.
- A Protectee may send a Partner invitation while another Partnership is in Cooling-off. Warn that acceptance grants full powers immediately and send the normal Action alerts.
- Active Roster rows show verified email, since time, Log-sharing state, reciprocal state, Partner reachability, and any Cooling-off deadline.
- Pending invitations appear separately with invitee email, inviter, sent and expiry times, proposed Log sharing, reciprocal effect, and delivery state. Other Partners may inspect but not mutate them.
- Partner reachability renders as **Delivery delayed** for retryable failures and **Nightloch cannot reach this Partner** after a hard bounce or complaint. Later delivery clears the warning but remains in Activity. A bounced invitation reads **Invitation not delivered**; a wrong address requires cancellation and a new invitation.
- Immediate Partner termination and **End now** require typing `END`. Other mutations use one consequence-specific confirmation. A valid session is sufficient; no password re-entry.
- Activity is newest-first and paginated, with category, actor, and date-range filters. Slice 3 has no free-text search or export.
- Activity never exposes raw before/after JSON. Expandable details contain only human-readable fields authorized for the viewer.
- Concurrent lifecycle actions use first-commit-wins semantics. A losing action makes no mutation and shows the already-resulting state, actor, and exact time where authorized.

## Trust and adversarial limits

- Every active Partner acts independently; there is no quorum. Invitation and Roster UI say: **Every Partner can act independently. Only add someone you trust.**
- A Protectee can turn off a Device’s VPN immediately. A Protectee can also invite a colluding Partner who may loosen immediately after acceptance. Existing Partners receive the invitation and acceptance signals but cannot veto, freeze, remove, or escalate through a special v1 workflow.
- The quickest full relationship exit is concurrent Cooling-off on every Partnership: 72 hours, not one serial delay per Partner. Partnership powers and Action alerts remain active until each deadline.
- Log sharing defaults off. Checkbox: **Share my DNS domain log with this Partner. I can turn this off immediately; Nightloch will tell them.**
- Partnership integrity messages cannot be muted or unsubscribed while a Partnership is active, and they are never batched or intentionally delayed.
- A new verified email is a new User in v1; Partner powers are never silently transferred from an old email.
- Starting account-deletion Cooling-off immediately ends every Partnership in which the deleting User is the Partner. Canceling deletion does not restore those Partnerships; new invitations are required. Partnerships protecting the deleting User remain active until deletion completes.
- Starting account-deletion Cooling-off also cancels every pending Partner invitation involving the deleting User and prevents new invitations or acceptances involving them. Canceling deletion does not restore invitations.
- If the Protectee is deleted, a former Partner’s ended-Partnership receipt remains but identifies the Protectee only as **Deleted User** and contains only the deletion ending cause and exact time—never the former email, Roster, or Activity.
- When account deletion immediately ends a deleting Partner’s Partnerships, tell the ordinary ending recipients that the Partner requested User deletion and the Partnership therefore ended immediately; do not disguise it as a generic Partner end.
- Account-deletion signals use separate per-recipient messages: start goes to active Partners protecting the deleting User; each immediate outbound Partnership end goes to that Protectee, the deleting Partner, and that Protectee’s remaining Roster; invitation cancellation goes to both direct parties and any relevant Roster previously alerted; deletion cancellation goes only to active Partners still protecting the User; completion goes to the Partners who still protected the deleted User. Never expose unrelated relationships.
- During account-deletion Cooling-off, the deleting User may cancel deletion, tighten their Policy, revoke their Devices, and disable Log sharing. Loosening continues to follow Partner-lock rules. They cannot send or accept Partner invitations, and they hold no Partner powers over other Protectees.
- Only the deleting User may cancel account deletion. No Partner or Operator may accelerate, delay, or cancel the 72-hour timer through the product. Active Partners retain their ordinary Policy and Device powers until deletion completes.
- Starting account deletion requires typing `DELETE` after a consequence summary that separately names the immediate end of outbound Partnerships, immediate cancellation of pending invitations, non-restoration of both, continued protection until the exact deadline, and the deleting User’s ability to cancel before then.
- Show the deleting User a persistent exact-deadline banner with **Cancel deletion** in **Your protection**. Every active Partner protecting them sees a read-only warning with the same deadline. No unrelated or former User sees the countdown merely because they received a discrete ending notice.
- The completion Action email may identify the deleted User by their verified email. After completion, retained UI and receipts use **Deleted User**; already-delivered emails cannot be recalled or pseudonymized.
- Failed Partner reachability is visible but does not suspend powers or pause lifecycle clocks.
- Invitation blocks, including decline-and-block and block-management settings, are deferred outside Slice 3/v1. Ordinary decline remains.
- Cooling-off remains load-bearing Slice 3 behavior. A dedicated rate limit for repeated start/cancel churn is deferred; accepted lifecycle mutations still produce their required unbatched Action alerts.
