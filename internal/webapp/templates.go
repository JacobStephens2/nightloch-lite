package webapp

import (
	"fmt"
	"html/template"
	"net/url"
	"time"

	"github.com/JacobStephens2/nightloch-lite/internal/partnership"
)

var templateSources = map[string]string{
	"sign-in": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Sign in · Nightloch</title><link rel="stylesheet" href="/assets/style.css">
<script src="https://challenges.cloudflare.com/turnstile/v0/api.js" async defer></script></head>
<body><main class="shell"><section class="card"><p class="eyebrow">Nightloch Lite</p><h1>Sign in</h1>
<p class="lede">We’ll email you a one-time link. It expires after 15 minutes.</p>
{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
<form method="post" action="/sign-in"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<label for="email">Email</label><input id="email" name="email" type="email" autocomplete="email" required maxlength="254" value="{{.Email}}">
<div class="turnstile"><div class="cf-turnstile" data-sitekey="{{.TurnstileSite}}" data-action="sign-in"></div></div>
<button type="submit">Email me a sign-in link</button></form></section></main></body></html>`,
	"check-email": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Check your email · Nightloch</title><link rel="stylesheet" href="/assets/style.css"></head>
<body><main class="shell"><section class="card"><p class="eyebrow">Nightloch Lite</p><h1>Check your email</h1>
<p class="lede">If that address can receive a link, it should arrive shortly. The link works once and expires after 15 minutes.</p>
<p><a href="/sign-in">Use a different email</a></p></section></main></body></html>`,
	"invalid-link": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Link unavailable · Nightloch</title><link rel="stylesheet" href="/assets/style.css"></head>
<body><main class="shell"><section class="card"><p class="eyebrow">Nightloch Lite</p><h1>That link is unavailable</h1>
<p class="lede">It may have expired or already been used. Request a fresh link to continue.</p>
<p><a class="button-link" href="/sign-in">Request another link</a></p></section></main></body></html>`,
	"dashboard": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Nightloch</title><link rel="stylesheet" href="/assets/style.css"><script src="/assets/local-time.js" defer></script></head>
<body><header class="topbar"><strong>Nightloch</strong><form method="post" action="/sign-out"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><button class="quiet" type="submit">Sign out</button></form></header>
<main class="dashboard"><section><p class="eyebrow">Signed in as {{.User.Email}}</p><h1>Your protection</h1>
{{if .Refusal}}<p class="error" role="alert">{{.Refusal}}</p>{{end}}
{{if .View.PartnerLockActive}}
<p class="lede"><strong>Partner lock is active.</strong> Your Partners can loosen your Policy; you cannot.</p>
<h2>Your Partners</h2>
<p class="warn"><strong>Every Partner can act independently. Only add someone you trust.</strong> There is no quorum: any one of them can loosen your Policy or revoke your Devices on their own.</p>
<ul class="rows">{{range .View.Partners}}<li><strong>{{.PartnerEmail}}</strong> — Partner since {{utc .Since}}. Log sharing: {{if .LogSharing}}On{{else}}Off{{end}}. Reciprocal: {{if .Reciprocal}}Yes — you are also their Partner{{else}}No{{end}}.<br>
{{if .Reachability.Warning}}<span class="warn-inline">{{.Reachability.Label}}.</span> This does not change their Partner powers.<br>{{else}}{{.Reachability.Label}} by email.<br>{{end}}
{{if .CoolingOffRunning}}
<span class="warn-inline">Scheduled to end {{utc .CoolingOffEndsAt}} — {{countdown .CoolingOffEndsAt $.Now}}.</span><br>
Until then this Partnership stays active and {{.PartnerEmail}} keeps every Partner power. They may also agree to end it sooner.<br>
{{if .EndsPartnerLock}}At that deadline your Partner lock becomes inactive and you will be able to loosen your own Policy.{{else}}Your Partner lock stays active afterwards, because {{.PartnersAfterDeadline}} other Partner(s) outlast that deadline.{{end}}<br>
<a href="/partners/partnerships/{{.PartnershipID}}/cancel-end">Cancel the ending request</a>
{{else}}
<a href="/partners/partnerships/{{.PartnershipID}}/request-end">Request to end this Partnership</a>
{{end}}</li>{{end}}</ul>
{{else}}
<p class="lede"><strong>Partner lock is inactive.</strong> You have no active Partners, so you can still loosen your own Policy.</p>
{{end}}
<p class="actions"><a class="button-link" href="/partners/invite">Add a Partner</a> <a href="/activity">View your Activity</a></p>
{{if .View.PendingInvitations}}
<h2>Invitations you sent</h2>
<p class="lede">A pending invitation grants no Partner powers and does not activate Partner lock; acceptance is required.</p>
<ul class="rows">{{range .View.PendingInvitations}}<li><strong>{{.InviteeEmail}}</strong><br>
Sent {{utc .SentAt}}. Expires {{utc .ExpiresAt}}.<br>
Log sharing if accepted: {{if .LogSharing}}On{{else}}Off{{end}}. Reciprocal Partnership if accepted: {{if .Reciprocal}}Yes{{else}}No{{end}}.<br>
{{if .DeliveryState.Failed}}<span class="warn-inline">Delivery: {{.DeliveryState.Label}}.</span> Nightloch could not deliver this invitation to {{.InviteeEmail}}. Resending it would go to the same address. If the address is wrong, cancel this invitation and send a new one to the corrected address.<br>{{else}}Delivery: {{.DeliveryState.Label}}.<br>{{end}}
{{if .Resends}}{{if eq .Resends 1}}Resent once, on {{utc .LastSentAt}}.{{else}}Resent {{.Resends}} times, most recently {{utc .LastSentAt}}.{{end}} Resending does not extend the expiry above.<br>{{end}}
<span class="muted">The invited address and the proposed Log sharing cannot be edited. To correct either, cancel this invitation and send a new one.</span>
{{if $.Now.Before .ResendDueAt}}<span class="muted">You can resend this invitation from {{utc .ResendDueAt}}. Resending is available once every 24 hours.</span>{{else}}<a href="/partners/sent-invitations/{{.ID}}/resend">Resend invitation</a>{{end}}
<a href="/partners/sent-invitations/{{.ID}}/cancel">Cancel invitation</a></li>{{end}}</ul>
{{end}}
{{if .Received}}
<h2>Partner invitations</h2>
<p class="lede">These Partner invitations were sent to {{.User.Email}}. You can answer them here without the emailed link.</p>
<ul class="rows">{{range .Received}}<li><strong>{{.ProtecteeEmail}}</strong> invited you to be their Partner.<br>
Sent {{utc .SentAt}}. Expires {{utc .ExpiresAt}}.<br>
Log sharing if you accept: {{if .LogSharing}}On — you will receive domain-log access.{{else}}Off — you will not receive domain-log access.{{end}}<br>
{{if .Reciprocal}}<span class="warn-inline">Reciprocal Partnerships if you accept: Yes.</span> {{.ProtecteeEmail}} already protects you; accepting adds a second, independent Partnership in the opposite direction. The two do not merge.<br>{{end}}
<a href="/partners/received-invitations/{{.ID}}">Review this invitation</a></li>{{end}}</ul>
{{end}}
{{if .Protecting}}
<h2>People you protect</h2>
<p class="lede">This area is separate from Your protection above. Protecting someone else never changes your own Partner lock, and a Partnership listed here is independent of any Partnership listed there.</p>
<p class="warn"><strong>Every Partner can act independently. Only add someone you trust.</strong> Each Partner below acts on their own; you cannot veto, remove, or escalate over another Partner.</p>
<ul class="rows">{{range .Protecting}}<li><strong>{{.ProtecteeEmail}}</strong> — Partner since {{utc .Since}}. Log sharing: {{if .LogSharing}}On — you can view their DNS domain log.{{else}}Off — you cannot view their DNS domain log.{{end}}<br>
Reciprocal: {{if .Reciprocal}}Yes — {{.ProtecteeEmail}} is also one of your own Partners, listed under Your protection above. That is a separate Partnership with its own Log sharing, ending, and deadline; nothing you do here changes it.{{else}}No{{end}}<br>
<strong>Their Roster</strong>
<ul class="rows">{{range .Roster}}<li>{{.PartnerEmail}} — Partner since {{utc .Since}}. Log sharing: {{if .LogSharing}}On{{else}}Off{{end}}. Reciprocal: {{if .Reciprocal}}Yes{{else}}No{{end}}. {{if .Reachability.Warning}}<span class="warn-inline">{{.Reachability.Label}}.</span>{{else}}{{.Reachability.Label}}.{{end}}{{if .CoolingOffRunning}} <span class="warn-inline">Scheduled to end {{utc .CoolingOffEndsAt}} — {{countdown .CoolingOffEndsAt $.Now}}.</span>{{end}}</li>{{end}}</ul>
{{if .PendingInvitations}}
<strong>Invitations {{.ProtecteeEmail}} sent</strong>
<ul class="rows">{{range .PendingInvitations}}<li>{{.InviteeEmail}} — Invited by {{.InviterEmail}}. Sent {{utc .SentAt}}. Expires {{utc .ExpiresAt}}.<br>
Log sharing if accepted: {{if .LogSharing}}On{{else}}Off{{end}}. Reciprocal Partnership if accepted: {{if .Reciprocal}}Yes{{else}}No{{end}}. Delivery: {{.DeliveryState.Label}}.<br>
{{if .Resends}}{{if eq .Resends 1}}Resent once, on {{utc .LastSentAt}}.{{else}}Resent {{.Resends}} times, most recently {{utc .LastSentAt}}.{{end}} Resending did not extend the expiry above, and Nightloch did not email you again about it.<br>{{end}}
<span class="muted">You can observe this invitation, but you cannot accept, cancel, or decline it. Accepting grants full Partner powers immediately.</span></li>{{end}}</ul>
{{end}}
{{if .CoolingOffRunning}}
<span class="warn-inline">{{.ProtecteeEmail}} requested to end this Partnership. It is scheduled to end {{utc .CoolingOffEndsAt}} — {{countdown .CoolingOffEndsAt $.Now}}.</span><br>
Until then it stays active and you keep every Partner power. {{.ProtecteeEmail}} may cancel the request.<br>
{{if .EndsPartnerLock}}At that deadline {{.ProtecteeEmail}}&rsquo;s Partner lock becomes inactive and they will be able to loosen their own Policy.{{else}}{{.ProtecteeEmail}}&rsquo;s Partner lock stays active afterwards, because {{.PartnersAfterDeadline}} other Partner(s) outlast that deadline.{{end}}<br>
<a href="/partners/partnerships/{{.PartnershipID}}/end">End now</a>
{{else}}
<a href="/partners/partnerships/{{.PartnershipID}}/end">End this Partnership</a>
{{end}}
<a href="/activity/{{.ProtecteeID}}">View their Activity</a></li>{{end}}</ul>
{{end}}
{{if .Ended}}
<h2>Partnerships you ended</h2>
<p class="lede">You keep a read-only receipt for each Partnership you no longer hold. A receipt shows nothing about that Protectee&rsquo;s remaining Partners or anything that happened after it ended.</p>
<ul class="rows">{{range .Ended}}<li><strong>{{.ProtecteeLabel}}</strong> — ended {{utc .EndedAt}}.<br>
<a href="/partners/ended-partnerships/{{.PartnershipID}}">View receipt</a></li>{{end}}</ul>
{{end}}
<div class="notice"><strong>Next milestone</strong><p>Device enrollment remains unavailable until the Operator adds you to the Enrollment allowlist. <a href="/devices/enroll">Read what enrolling a Device will mean</a>, including how a Device behaves if Nightloch is unreachable.</p></div>
</section><footer>Version {{.Version}}</footer></main></body></html>`,
	"invitation-review": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Review Partner invitation · Nightloch</title><link rel="stylesheet" href="/assets/style.css"><script src="/assets/local-time.js" defer></script></head>
<body><header class="topbar"><strong>Nightloch</strong>{{if .SignedIn}}<a href="/">Your protection</a>{{else}}<a href="/sign-in">Sign in</a>{{end}}</header>
<main class="dashboard"><section><h1>Review Partner invitation</h1>
<p class="lede">{{.Review.ProtecteeEmail}} invited {{if .Review.ViewerMatches}}{{.Review.InviteeEmail}}{{else}}{{.Review.MaskedInviteeEmail}}{{end}} to become their Nightloch Partner. Viewing this page does not accept the invitation.</p>
<h2>What Nightloch does</h2>
<p>Nightloch is partner-locked DNS protection. It can block domains on enrolled Devices. While someone has active Partners, only those Partners can loosen their Policy. Turning off a Device’s VPN removes protection.</p>
<h2>What you can do as a Partner</h2>
<ul class="rows"><li>View Coverage and enforcement health</li><li>Tighten or loosen {{.Review.ProtecteeEmail}}’s Policy</li><li>Approve Allowlist entries</li><li>Revoke their Devices</li><li>View their Roster and Activity</li><li>View their DNS domain log only when Log sharing is on</li></ul>
<p class="warn"><strong>Every Partner can act independently. Only add someone you trust.</strong></p>
<h2>Who can see you</h2>
{{if .Review.OtherPartnerCount}}
<p>{{.Review.OtherPartnerCount}} other Partner(s) currently protect {{.Review.ProtecteeEmail}}. They have been told this invitation was sent to your email. If you accept, you will see their verified emails and they will continue to see yours.</p>
{{else}}
<p>0 other Partners currently protect {{.Review.ProtecteeEmail}}. If you accept, your verified email will remain visible to current and future Partners in the Roster and Activity.</p>
{{end}}
{{if .Review.Reciprocal}}
<h2>Reciprocal Partnerships</h2>
<p>{{.Review.ProtecteeEmail}} already protects you as your Partner. Accepting creates a second, independent Partnership in the opposite direction, so you would be their Partner too.</p>
<p>The two Partnerships do not merge: each keeps its own Log sharing, its own ending, and its own powers, and ending one does not end the other.</p>
{{end}}
<h2>Log sharing</h2>
<p>{{if .Review.LogSharing}}On — you will receive domain-log access after acceptance.{{else}}Off — you will not receive domain-log access.{{end}} {{.Review.ProtecteeEmail}} can turn Log sharing off at any time; Nightloch will tell you when they do.</p>
<h2>Ending the Partnership</h2>
<p>You may end immediately. A request by {{.Review.ProtecteeEmail}} takes 72 hours, during which you keep your Partner powers.</p>
<p class="lede">This invitation expires {{utc .Review.ExpiresAt}}.</p>
{{if .Review.MayAccept}}
<div class="actions">
<form method="post" action="{{.ActionBase}}/accept"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<button type="submit">Accept Partnership</button></form>
<a class="quiet-link" href="{{.ActionBase}}/decline">Decline invitation</a>
</div>
<p class="lede">Declining creates no Partnership and grants no Partner powers. {{.Review.ProtecteeEmail}} can send a new invitation later.</p>
{{else if .SignedIn}}
<p class="error" role="alert">You are signed in as {{.Review.ViewerEmail}}, but this invitation was sent to {{.Review.MaskedInviteeEmail}}. Sign out and continue with the invited address.</p>
<form method="post" action="/sign-out"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><button type="submit">Sign out</button></form>
{{else}}
<p class="lede">Sign in as {{.Review.MaskedInviteeEmail}} to respond.</p>
<p><a class="button-link" href="/sign-in">Sign in to respond</a></p>
{{end}}
</section><footer>Version {{.Version}}</footer></main></body></html>`,
	"invitation-decline-confirm": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Decline Partner invitation · Nightloch</title><link rel="stylesheet" href="/assets/style.css"></head>
<body><header class="topbar"><strong>Nightloch</strong><a href="/">Your protection</a></header>
<main class="dashboard"><section><h1>Decline this Partner invitation?</h1>
<p class="lede">Declining {{.CounterpartyEmail}}&rsquo;s invitation creates no Partnership and grants no Partner powers. Nightloch will tell {{.CounterpartyEmail}} that you declined.</p>
<p class="lede">This cannot be undone. {{.CounterpartyEmail}} can send you a new invitation later.</p>
<div class="actions">
<form method="post" action="{{.Action}}"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<button type="submit">Decline invitation</button></form>
<a class="quiet-link" href="{{.Back}}">Keep reading the invitation</a>
</div>
</section><footer>Version {{.Version}}</footer></main></body></html>`,
	"invitation-cancel-confirm": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Cancel Partner invitation · Nightloch</title><link rel="stylesheet" href="/assets/style.css"></head>
<body><header class="topbar"><strong>Nightloch</strong><a href="/">Your protection</a></header>
<main class="dashboard"><section><h1>Cancel this Partner invitation?</h1>
<p class="lede">{{.CounterpartyEmail}} has not answered yet. Cancelling creates no Partnership and grants no Partner powers. Nightloch will tell {{.CounterpartyEmail}} that you canceled.</p>
<p class="lede">This cannot be undone, and the emailed link stops working. Cancelling is also how you correct an invitation: the invited address and the proposed Log sharing cannot be edited, so send a new invitation afterwards.</p>
<div class="actions">
<form method="post" action="{{.Action}}"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<button type="submit">Cancel invitation</button></form>
<a class="quiet-link" href="{{.Back}}">Keep the invitation</a>
</div>
</section><footer>Version {{.Version}}</footer></main></body></html>`,
	"invitation-resend-confirm": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Resend Partner invitation · Nightloch</title><link rel="stylesheet" href="/assets/style.css"><script src="/assets/local-time.js" defer></script></head>
<body><header class="topbar"><strong>Nightloch</strong><a href="/">Your protection</a></header>
<main class="dashboard"><section><h1>Resend this Partner invitation?</h1>
<p class="lede">Nightloch will email {{.CounterpartyEmail}} the same invitation again. Nothing about it changes: the same address is invited on the same terms, and it still expires {{utc .ExpiresAt}}. Resending does not give {{.CounterpartyEmail}} more time to answer.</p>
<p class="lede">The link in the earlier email stops working, and the new email carries its place. Your current Partners are not emailed again; they were already told this invitation exists.</p>
<p class="lede">You can resend an invitation once every 24 hours.</p>
<div class="actions">
<form method="post" action="{{.Action}}"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<button type="submit">Resend invitation</button></form>
<a class="quiet-link" href="{{.Back}}">Leave it as it is</a>
</div>
</section><footer>Version {{.Version}}</footer></main></body></html>`,
	"invitation-declined-receipt": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Invitation declined · Nightloch</title><link rel="stylesheet" href="/assets/style.css"><script src="/assets/local-time.js" defer></script></head>
<body><header class="topbar"><strong>Nightloch</strong><a href="/">Your protection</a></header>
<main class="dashboard"><section><h1>You declined this invitation</h1>
<p class="lede">You declined {{.CounterpartyEmail}}&rsquo;s Partner invitation on {{utc .At}}. No Partnership was created and no Partner powers were granted.</p>
<p class="lede">Nightloch has told {{.CounterpartyEmail}}. They can send you a new invitation later.</p>
<p><a class="button-link" href="/">Go to Nightloch</a></p>
</section><footer>Version {{.Version}}</footer></main></body></html>`,
	"invitation-canceled-receipt": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Invitation canceled · Nightloch</title><link rel="stylesheet" href="/assets/style.css"><script src="/assets/local-time.js" defer></script></head>
<body><header class="topbar"><strong>Nightloch</strong><a href="/">Your protection</a></header>
<main class="dashboard"><section><h1>You canceled this invitation</h1>
<p class="lede">You canceled the Partner invitation to {{.CounterpartyEmail}} on {{utc .At}}. No Partnership was created and no Partner powers were granted.</p>
<p class="lede">Nightloch has told {{.CounterpartyEmail}}. To invite a different address, or to offer different Log sharing, send a new invitation.</p>
<p class="actions"><a class="button-link" href="/partners/invite">Add a Partner</a> <a href="/activity">View your Activity</a></p>
</section><footer>Version {{.Version}}</footer></main></body></html>`,
	"invitation-outcome": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Partner invitation · Nightloch</title><link rel="stylesheet" href="/assets/style.css"><script src="/assets/local-time.js" defer></script></head>
<body><header class="topbar"><strong>Nightloch</strong>{{if .SignedIn}}<a href="/">Your protection</a>{{else}}<a href="/sign-in">Sign in</a>{{end}}</header>
<main class="dashboard"><section>
{{if eq .Review.Outcome "accepted"}}
<h1>This invitation was already accepted</h1>
<p class="lede">{{if .Review.ViewerMatches}}You accepted{{else}}The invited person accepted{{end}} {{.Review.ProtecteeEmail}}&rsquo;s Partner invitation on {{utc .Review.ResolvedAt}}. The Partnership is active, so there is nothing left to answer here.</p>
{{else if eq .Review.Outcome "declined"}}
<h1>This invitation was declined</h1>
<p class="lede">The invitation from {{.Review.ProtecteeEmail}} was declined on {{utc .Review.ResolvedAt}}. No Partnership was created and no Partner powers were granted.</p>
<p class="lede">{{.Review.ProtecteeEmail}} can send a new invitation later.</p>
{{else if eq .Review.Outcome "canceled"}}
<h1>This invitation was canceled</h1>
<p class="lede">{{.Review.ProtecteeEmail}} canceled this Partner invitation on {{utc .Review.ResolvedAt}}. No Partnership was created and no Partner powers were granted.</p>
<p class="lede">Only {{.Review.ProtecteeEmail}} can send a replacement.</p>
{{else}}
<h1>This invitation expired</h1>
<p class="lede">The Partner invitation from {{.Review.ProtecteeEmail}} expired on {{utc .Review.ResolvedAt}}. Invitations are valid for seven days and cannot be extended. No Partnership was created and no Partner powers were granted.</p>
<p class="lede">Ask {{.Review.ProtecteeEmail}} to send a new invitation.</p>
{{end}}
<p><a class="button-link" href="/">Go to Nightloch</a></p>
</section><footer>Version {{.Version}}</footer></main></body></html>`,
	"invitation-unavailable": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Invitation unavailable · Nightloch</title><link rel="stylesheet" href="/assets/style.css"></head>
<body><main class="shell"><section class="card"><p class="eyebrow">Nightloch Lite</p><h1>That invitation is unavailable</h1>
<p class="lede">Nightloch cannot show this Partner invitation. Ask the person who invited you to send a new one.</p>
<p><a class="button-link" href="/">Go to Nightloch</a></p></section></main></body></html>`,
	"partnership-end-confirm": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>End Partnership · Nightloch</title><link rel="stylesheet" href="/assets/style.css"><script src="/assets/local-time.js" defer></script></head>
<body><header class="topbar"><strong>Nightloch</strong><a href="/">Your protection</a></header>
<main class="dashboard"><section>{{if .Ending.CoolingOffRunning}}<h1>End this Partnership now?</h1>
<p class="lede">{{.Ending.ProtecteeEmail}} requested to end this Partnership, which is scheduled to end {{utc .Ending.CoolingOffEndsAt}}. You are not required to agree: if you do nothing, the Partnership stays active with all your Partner powers until that deadline, and {{.Ending.ProtecteeEmail}} may cancel the request before then.</p>
<p class="lede">Choosing <strong>End now</strong> waives the remaining time. It takes effect immediately and cannot be undone. You have protected them since {{utc .Ending.Since}}.</p>
{{else}}<h1>End this Partnership now?</h1>
<p class="lede">Ending your Partnership with {{.Ending.ProtecteeEmail}} takes effect immediately and cannot be undone. You have protected them since {{utc .Ending.Since}}.</p>
{{end}}
<p class="lede">You will no longer be able to change their Policy, approve Allowlist entries, revoke their Devices, or view their Roster and ongoing Activity{{if .Ending.LogSharing}}, and you will lose access to their DNS domain log{{end}}.</p>
{{if .Ending.PartnerLockActive}}
<p class="warn">{{.Ending.ProtecteeEmail}} will still have {{.Ending.RemainingPartners}} active Partner(s), so their Partner lock remains active.</p>
{{else}}
<p class="warn">{{.Ending.ProtecteeEmail}} will have 0 active Partners, so their Partner lock becomes inactive and they will be able to loosen their own Policy themselves.</p>
{{end}}
<p class="lede">Nightloch will tell {{.Ending.ProtecteeEmail}} and their remaining Partners. You will keep only a read-only receipt for this ended Partnership.</p>
{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
<form method="post" action="{{.Action}}"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<label for="confirmation">Type END to confirm</label>
<input id="confirmation" name="confirmation" type="text" autocomplete="off" required maxlength="16" value="">
<div class="actions"><button type="submit">{{if .Ending.CoolingOffRunning}}End now{{else}}End Partnership now{{end}}</button>
<a class="quiet-link" href="{{.Back}}">{{if .Ending.CoolingOffRunning}}Let the Cooling-off run{{else}}Keep this Partnership{{end}}</a></div></form>
</section><footer>Version {{.Version}}</footer></main></body></html>`,
	"partnership-receipt": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Ended Partnership · Nightloch</title><link rel="stylesheet" href="/assets/style.css"><script src="/assets/local-time.js" defer></script></head>
<body><header class="topbar"><strong>Nightloch</strong><a href="/">Your protection</a></header>
<main class="dashboard"><section><h1>Ended Partnership</h1>
{{if .Receipt.ProtecteeDeleted}}
<p class="lede">Your Partnership with {{.Receipt.ProtecteeLabel}} ended on {{utc .Receipt.EndedAt}}.</p>
<ul class="rows"><li>Protectee: {{.Receipt.ProtecteeLabel}}<br>
Ended: {{utc .Receipt.EndedAt}}<br>
How it ended: {{if .Receipt.EndedByUserDeletion}}They deleted their Nightloch User, so Nightloch ended the Partnership at that instant.{{else if .Receipt.PartnerEnded}}You ended the Partnership immediately.{{else if .Receipt.AgreedToEndNow}}You agreed to end the Partnership immediately after they requested Cooling-off.{{else if .Receipt.EndedAfterCoolingOff}}Nightloch ended the Partnership after the Cooling-off they requested completed.{{else}}Nightloch ended the Partnership.{{end}}</li></ul>
<p class="lede">This is everything Nightloch retains about that Partnership. Their verified email, their Roster, and their Activity are gone, and no Partnership can be formed with a deleted User. Emails Nightloch already sent you still name the address that identified them when each event happened; a delivered message cannot be recalled or pseudonymized.</p>
{{else}}
{{if .JustEnded}}
<p class="lede">{{if .Receipt.AgreedToEndNow}}You agreed to end your Partnership with {{.Receipt.ProtecteeEmail}} now, on {{utc .Receipt.EndedAt}}.{{else}}You ended your Partnership with {{.Receipt.ProtecteeEmail}} on {{utc .Receipt.EndedAt}}.{{end}} Nightloch has told them and their remaining Partners.</p>
{{else}}
<p class="lede">Your Partnership with {{.Receipt.ProtecteeEmail}} ended on {{utc .Receipt.EndedAt}}.</p>
{{end}}
<p class="lede">You can no longer change their Policy, approve Allowlist entries, revoke their Devices, view their Roster or ongoing Activity, or exercise any other Partner power.</p>
<ul class="rows"><li>Protectee: {{.Receipt.ProtecteeEmail}}<br>
Partner since: {{utc .Receipt.Since}}<br>
Ended: {{utc .Receipt.EndedAt}}<br>
Log sharing while active: {{if .Receipt.LogSharing}}On{{else}}Off{{end}}<br>
How it ended: {{if .Receipt.PartnerEnded}}You ended the Partnership immediately.{{else if .Receipt.AgreedToEndNow}}You agreed to end the Partnership immediately after {{.Receipt.ProtecteeEmail}} requested Cooling-off.{{else if .Receipt.EndedAfterCoolingOff}}Nightloch ended the Partnership after the Cooling-off {{.Receipt.ProtecteeEmail}} requested completed.{{else}}Nightloch ended the Partnership.{{end}}</li></ul>
<p class="lede">You keep only this read-only receipt, and it covers only this ended Partnership. It shows nothing about {{.Receipt.ProtecteeEmail}}&rsquo;s remaining Partners or anything that happened afterwards. To protect them again, ask them to send a new Partner invitation.</p>
{{end}}
<p><a class="button-link" href="/">Go to Nightloch</a></p>
</section><footer>Version {{.Version}}</footer></main></body></html>`,
	"activity": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Activity · Nightloch</title><link rel="stylesheet" href="/assets/style.css"><script src="/assets/local-time.js" defer></script></head>
<body><header class="topbar"><strong>Nightloch</strong><a href="/">Your protection</a></header>
<main class="dashboard"><section><p class="eyebrow">Signed in as {{.User.Email}}</p>
{{if not .View.Windowed}}<h1>Your Activity</h1>
<p class="lede">Every change to your protection, newest first. Nightloch keeps your complete history for as long as your Nightloch User exists.</p>
{{else}}<h1>Activity for {{.View.ProtecteeEmail}}</h1>
<p class="lede">Every change to {{.View.ProtecteeEmail}}&rsquo;s protection since you became their Partner, newest first. Your view begins at {{utc .View.VisibleFrom}}, when you accepted; nothing from before then is shown, and this view ends if the Partnership ends.</p>
{{end}}
{{if .FilterError}}<p class="error" role="alert">{{.FilterError}}</p>{{end}}
<form class="filters" method="get" action="{{.BasePath}}">
<label for="category">Category</label>
<select id="category" name="category"><option value="">Everything</option>
{{range .Categories}}<option value="{{.}}"{{if eq . $.View.Query.Category}} selected{{end}}>{{.Label}}</option>{{end}}</select>
<label for="actor">Who acted</label>
<select id="actor" name="actor"><option value="">Anyone</option>
{{range .View.Actors}}<option value="{{.}}"{{if eq . $.View.Query.Actor}} selected{{end}}>{{.}}</option>{{end}}</select>
<label for="from">From (UTC date)</label><input id="from" name="from" type="date" value="{{.From}}">
<label for="to">To (UTC date)</label><input id="to" name="to" type="date" value="{{.To}}">
<button type="submit">Apply filters</button>
{{if .View.Query.Filtered}}<a class="quiet-link" href="{{.ClearURL}}">Clear filters</a>{{end}}
</form>
{{if .View.Entries}}
<ul class="rows">{{range .View.Entries}}<li id="entry-{{.ID}}">
{{if .Delivery}}<span class="muted">{{.Category.Label}}</span> {{end}}{{.Summary}}<br>
<span class="muted">{{utc .OccurredAt}}{{if .Effective}} — takes effect {{utc .EffectiveAt}}{{end}}</span>
{{with requestLink $.BasePath .}}<br><a href="{{.}}">See the request Nightloch carried out</a>{{end}}
{{if .Details}}<details><summary>Details</summary><ul class="rows">
{{range .Details}}<li>{{.Label}}: {{if .Instant}}{{utc .At}}{{else}}{{.Text}}{{end}}</li>{{end}}
</ul></details>{{end}}
</li>{{end}}</ul>
{{if or .OlderURL .NewestURL}}<p class="actions">{{if .OlderURL}}<a class="button-link" href="{{.OlderURL}}">Older entries</a>{{end}}{{if .NewestURL}}<a class="quiet-link" href="{{.NewestURL}}">Back to the newest entries</a>{{end}}</p>{{end}}
{{else if .View.Query.Filtered}}
<p class="lede">Nothing in this chronology matches those filters. <a href="{{.ClearURL}}">Clear them</a> to read it in full.</p>
{{else}}
<p class="lede">There is nothing to show yet.</p>
{{end}}
<p class="muted">Every time is shown in your browser&rsquo;s local timezone with UTC beside it. Activity has no free-text search and no export.</p>
</section><footer>Version {{.Version}}</footer></main></body></html>`,
	"activity-unavailable": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Activity unavailable · Nightloch</title><link rel="stylesheet" href="/assets/style.css"></head>
<body><main class="shell"><section class="card"><p class="eyebrow">Nightloch Lite</p><h1>That Activity is unavailable</h1>
<p class="lede">Nightloch cannot show this Activity. A Protectee reads their own history, and a Partner reads it only while their Partnership is active. A Partnership you no longer hold leaves you the receipt for it and nothing else.</p>
<p><a class="button-link" href="/">Go to Nightloch</a></p></section></main></body></html>`,
	"partnership-unavailable": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Partnership unavailable · Nightloch</title><link rel="stylesheet" href="/assets/style.css"></head>
<body><main class="shell"><section class="card"><p class="eyebrow">Nightloch Lite</p><h1>That Partnership is unavailable</h1>
<p class="lede">Nightloch cannot show this Partnership. Only the two people on a Partnership reach its controls: the Protectee may request or cancel an ending, and the Partner may end it and read the receipt they keep.</p>
<p><a class="button-link" href="/">Go to Nightloch</a></p></section></main></body></html>`,
	"cooling-off-confirm": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Request to end a Partnership · Nightloch</title><link rel="stylesheet" href="/assets/style.css"><script src="/assets/local-time.js" defer></script></head>
<body><header class="topbar"><strong>Nightloch</strong><a href="/">Your protection</a></header>
<main class="dashboard"><section>
{{if not .Request.Running}}
<h1>Request to end this Partnership?</h1>
<p class="lede">Your Partnership with {{.Request.PartnerEmail}} does not end straight away. Nightloch schedules it to end on {{utc .Request.EndsAt}}, 72 hours from now, and that deadline does not move.</p>
{{else}}
<h1>This Partnership is already scheduled to end</h1>
<p class="lede">You requested this ending on {{utc .Request.RequestedAt}}. Your Partnership with {{.Request.PartnerEmail}} is scheduled to end on {{utc .Request.EndsAt}} — {{countdown .Request.EndsAt .Now}}. That deadline does not move, and requesting again would not shorten it.</p>
{{end}}
<p class="lede">Until then nothing changes: the Partnership stays active, your Policy stays enforced, and {{.Request.PartnerEmail}} keeps every Partner power{{if .Request.LogSharing}}, including access to your DNS domain log{{end}}. Nightloch will tell them, and they may agree to end it sooner. You can cancel the request at any point before the deadline.</p>
{{if .Request.FinalPartnership}}
<p class="warn">{{.Request.PartnerEmail}} is your final active Partner. At {{utc .Request.EndsAt}} your Partner lock becomes inactive and you will be able to loosen your own Policy.</p>
{{else}}
<p class="warn">{{.Request.PartnersAfterDeadline}} other active Partner(s) outlast {{utc .Request.EndsAt}}, so your Partner lock remains active afterwards.</p>
{{end}}
{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
<div class="actions">
{{if not .Request.Running}}
<form method="post" action="{{.Action}}"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<button type="submit">Request to end this Partnership</button></form>
<a class="quiet-link" href="{{.Back}}">Keep this Partnership</a>
{{else}}
<a class="button-link" href="/partners/partnerships/{{.Request.PartnershipID}}/cancel-end">Cancel the ending request</a>
<a class="quiet-link" href="{{.Back}}">Your protection</a>
{{end}}
</div>
</section><footer>Version {{.Version}}</footer></main></body></html>`,
	"cooling-off-cancel-confirm": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Cancel a Partnership ending request · Nightloch</title><link rel="stylesheet" href="/assets/style.css"><script src="/assets/local-time.js" defer></script></head>
<body><header class="topbar"><strong>Nightloch</strong><a href="/">Your protection</a></header>
<main class="dashboard"><section><h1>Cancel this ending request?</h1>
{{if not .Request.Running}}
<p class="lede">Your Partnership with {{.Request.PartnerEmail}} has no scheduled ending, so there is nothing to cancel. It remains active with the same Partner powers it has always had.</p>
{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
<div class="actions"><a class="button-link" href="{{.Back}}">Your protection</a></div>
{{else}}
<p class="lede">Your Partnership with {{.Request.PartnerEmail}} is scheduled to end on {{utc .Request.EndsAt}} — {{countdown .Request.EndsAt .Now}}. Cancelling removes that scheduled ending; the Partnership simply stays active, as it has been throughout.</p>
<p class="lede">Nightloch will tell {{.Request.PartnerEmail}} that you canceled. You may request an ending again later, and a new request starts a fresh 72 hours.</p>
{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
<div class="actions">
<form method="post" action="{{.Action}}"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<button type="submit">Cancel the ending request</button></form>
<a class="quiet-link" href="{{.Back}}">Leave the ending scheduled</a>
</div>
{{end}}
</section><footer>Version {{.Version}}</footer></main></body></html>`,
	"cooling-off-receipt": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Partnership ending request · Nightloch</title><link rel="stylesheet" href="/assets/style.css"><script src="/assets/local-time.js" defer></script></head>
<body><header class="topbar"><strong>Nightloch</strong><a href="/">Your protection</a></header>
<main class="dashboard"><section>
{{if .Canceled}}
<h1>Ending request canceled</h1>
<p class="lede">You canceled the request to end your Partnership with {{.Request.PartnerEmail}} on {{utc .Request.CanceledAt}}. No ending is scheduled, and the Partnership remains active with the same Partner powers it has always had.</p>
<p class="lede">Nightloch has told {{.Request.PartnerEmail}}. You may request an ending again later; a new request starts a fresh 72 hours.</p>
{{else}}
<h1>Ending requested</h1>
<p class="lede">Your Partnership with {{.Request.PartnerEmail}} is scheduled to end on {{utc .Request.EndsAt}} — {{countdown .Request.EndsAt .Now}}. That deadline does not move.</p>
<p class="lede">Until then the Partnership stays active and {{.Request.PartnerEmail}} keeps every Partner power. Nightloch has told them; they may agree to end it sooner, and you may cancel the request at any point before the deadline.</p>
{{if .Request.FinalPartnership}}
<p class="warn">{{.Request.PartnerEmail}} is your final active Partner. At {{utc .Request.EndsAt}} your Partner lock becomes inactive and you will be able to loosen your own Policy.</p>
{{else}}
<p class="warn">{{.Request.PartnersAfterDeadline}} other active Partner(s) outlast that deadline, so your Partner lock remains active afterwards.</p>
{{end}}
{{end}}
<p><a class="button-link" href="/">Go to Nightloch</a></p>
</section><footer>Version {{.Version}}</footer></main></body></html>`,
	"invite": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Add a Partner · Nightloch</title><link rel="stylesheet" href="/assets/style.css"></head>
<body><header class="topbar"><strong>Nightloch</strong><a href="/">Your protection</a></header>
<main class="dashboard"><section><h1>Add a Partner</h1>
<p class="lede">Nightloch emails an invitation that expires in seven days. No Partner powers exist until the invited person signs in and accepts.</p>
<p class="warn"><strong>Every Partner can act independently. Only add someone you trust.</strong> An active Partner can loosen your Policy, approve Allowlist entries, revoke your Devices, and see your Roster and Activity.</p>
{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
<form method="post" action="/partners/invite"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<label for="invitee_email">Partner email</label>
<input id="invitee_email" name="invitee_email" type="email" autocomplete="off" required maxlength="254" value="{{.InviteeEmail}}">
<p class="consent"><label class="inline"><input type="checkbox" name="log_sharing" value="on"> Share my DNS domain log with this Partner. I can turn this off immediately; Nightloch will tell them.</label></p>
<button type="submit">Send Partner invitation</button></form>
<p><a href="/">Cancel</a></p></section></main></body></html>`,
	"device-enroll": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Before you enroll a Device · Nightloch</title><link rel="stylesheet" href="/assets/style.css"></head>
<body><header class="topbar"><strong>Nightloch</strong><a href="/">Your protection</a></header>
<main class="dashboard"><section><p class="eyebrow">Signed in as {{.User.Email}}</p><h1>Before you enroll a Device</h1>
<p class="lede">A Device is one phone or laptop carrying a full tunnel to Nightloch. While that tunnel is up, everything the Device sends goes through Nightloch and every name it looks up is resolved and filtered there. Read this page before you enroll one, because a full tunnel has a failure that can take the Device off the internet.</p>
<h2>If Nightloch is unreachable, the Device stops reaching the internet</h2>
<p class="warn"><strong>That is deliberate, and it is not a bug.</strong> If the server is down, the path to it is broken, or the tunnel cannot be established, the Device keeps sending everything into a tunnel that goes nowhere. Nightloch does not fall open: it will not hand the Device back its unfiltered internet because protection stopped working. Until you turn the tunnel off yourself, the Device stops reaching the internet at all - no browsing, no apps, no mail.</p>
<p>The alternative would mean the moment protection is least available is also the easiest moment to be without it. The cost of choosing the other way is real and lands on you, so the recovery steps below are part of the product rather than a support article you have to find while offline.</p>
<h2>Getting a Device back online</h2>
<p>Turning the tunnel off needs no internet and never needs the Operator. Each supported platform has its own route, and each has at least one setting that turns a recoverable blackhole into one you cannot leave from the app you turned the tunnel on in.</p>
{{range .Platforms}}
<h3>{{.Name}}</h3>
<ul class="rows">{{range .Recovery}}<li>{{.}}</li>{{end}}</ul>
<p class="lede">Settings to understand before you turn them on:</p>
<ul class="rows">{{range .Traps}}<li><strong>{{.Setting}}</strong><br>
{{.Consequence}}<br>
<span class="warn-inline">Recovery:</span> {{.Recovery}}</li>{{end}}</ul>
{{end}}
<h2>What this does not protect you from</h2>
<p>With the tunnel off, you are not protected. Nothing is filtered and nothing is logged; your Partners see your Coverage fall, which is a signal to them rather than a control over you. Turning the tunnel off stays available to you by design - Nightloch restrains what you may loosen, not whether you may disconnect.</p>
<p>Nightloch filters DNS domains. On hardware you own, a determined bypass - encrypted DNS to a resolver Nightloch does not control, a second network path, a device that was never enrolled as a Device - is not fully preventable. Nightloch closes the routes it can reach, tells your Partners what it observes, and does not claim a guarantee it cannot keep.</p>
{{if .Allowlisted}}
<h2>Enroll a Device</h2>
{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
<form method="post" action="/devices/enroll"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<label for="device_name">Device name</label><input id="device_name" name="device_name" type="text" maxlength="64" required value="{{.DeviceName}}" placeholder="e.g. Phone or Laptop">
<button type="submit">Enroll Device</button></form>
{{else}}
<h2>Enrollment is invite-only</h2>
<p class="notice">Device enrollment is invite-only during beta. Your account is not currently allowlisted to enroll Devices.</p>
{{end}}
<p><a href="/">Back to your protection</a></p>
</section><footer>Version {{.Version}}</footer></main></body></html>`,
	"device-enrolled": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Device enrolled · Nightloch</title><link rel="stylesheet" href="/assets/style.css"></head>
<body><header class="topbar"><strong>Nightloch</strong><a href="/">Your protection</a></header>
<main class="dashboard"><section><p class="eyebrow">Signed in as {{.User.Email}}</p><h1>Device enrolled</h1>
<p class="lede">Device <strong>{{.Device.Name}}</strong> enrolled at tunnel address <strong>{{.Device.Address}}</strong>.</p>
<p class="warn"><strong>This configuration and private key are displayed once and cannot be viewed again. Download or scan it now.</strong></p>
<h2>Scan QR code</h2>
<p>Scan this QR code in the WireGuard app on your device to import the tunnel configuration directly:</p>
<div class="qr-container"><img class="qr-image" src="{{.QRDataURI}}" alt="WireGuard QR code for {{.Device.Name}}" width="256" height="256"></div>
<p><a class="button-link" href="{{.ConfDataURI}}" download="{{.DownloadName}}">Download .conf file</a></p>
<h2>WireGuard configuration</h2>
<p>Or copy and paste this configuration into WireGuard manually:</p>
<pre class="conf-block"><code>{{.Configuration}}</code></pre>
<p><a href="/">Back to your protection</a></p>
</section><footer>Version {{.Version}}</footer></main></body></html>`,
}

// templateFuncs keeps every rendered instant explicitly labelled. The server
// always writes UTC; /assets/local-time.js adds the viewer's local time and
// timezone alongside it when scripting is available (S3-27).
var templateFuncs = template.FuncMap{
	"utc": func(at time.Time) template.HTML {
		utc := at.UTC()
		return template.HTML(fmt.Sprintf(`<time datetime="%s" data-utc>%s UTC</time>`,
			utc.Format(time.RFC3339), utc.Format("2006-01-02 15:04")))
	},
	"countdown":   countdown,
	"requestLink": requestLink,
}

// requestLink is the address of the Protectee request a completed timer
// carried out (S3-18). It carries the page position holding that request and
// no filter at all, because the request and the completion are 72 hours and
// two categories apart: a bare fragment would only resolve on the rare page
// that happened to hold both. It is empty when this entry completes no
// request, and when the request lies before this reader's window, where the
// module has already withheld the reference.
func requestLink(basePath string, entry partnership.ActivityEntry) string {
	position := entry.RequestPage()
	if position.Zero() {
		return ""
	}
	return fmt.Sprintf("%s?after=%s#entry-%d",
		basePath, url.QueryEscape(position.String()), entry.RequestEntryID())
}

// countdown states how long is left before a Cooling-off deadline. The exact
// instant is always rendered beside it, so this is the readable half of the
// same fact rather than the fact itself (S3-07). It is rendered by the server
// from one instant per page, so every row of a Roster agrees.
func countdown(deadline, now time.Time) string {
	remaining := deadline.UTC().Sub(now.UTC())
	if remaining <= 0 {
		return "ending now"
	}
	switch days, hours := int(remaining.Hours())/24, int(remaining.Hours())%24; {
	case days > 0:
		return fmt.Sprintf("%s and %s remaining", plural(days, "day"), plural(hours, "hour"))
	case hours > 0:
		return fmt.Sprintf("%s and %s remaining", plural(hours, "hour"),
			plural(int(remaining.Minutes())%60, "minute"))
	case int(remaining.Minutes()) > 0:
		return plural(int(remaining.Minutes()), "minute") + " remaining"
	default:
		return "less than a minute remaining"
	}
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

func parseTemplates() (map[string]*template.Template, error) {
	parsed := make(map[string]*template.Template, len(templateSources))
	for name, source := range templateSources {
		tmpl, err := template.New(name).Funcs(templateFuncs).Parse(source)
		if err != nil {
			return nil, fmt.Errorf("parse %s template: %w", name, err)
		}
		parsed[name] = tmpl
	}
	return parsed, nil
}

const localTimeScript = `// Add the viewer's local time beside the server-rendered UTC stamp.
for (const element of document.querySelectorAll("time[data-utc]")) {
  const instant = new Date(element.getAttribute("datetime"));
  if (Number.isNaN(instant.getTime())) continue;
  const local = instant.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short", timeZoneName: "short" });
  element.textContent = local + " (" + element.textContent + ")";
}
`

const stylesheet = `:root{color-scheme:dark;--ink:#f4f0e8;--muted:#aaa69d;--panel:#161b22;--line:#303741;--accent:#b9e769;--danger:#ffb4ab}*{box-sizing:border-box}body{margin:0;background:#0b0f14;color:var(--ink);font:16px/1.55 ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.shell{min-height:100vh;display:grid;place-items:center;padding:24px}.card{width:min(100%,480px);padding:36px;border:1px solid var(--line);border-radius:18px;background:linear-gradient(145deg,#18202a,var(--panel));box-shadow:0 24px 80px #0008}h1{font-size:clamp(2rem,8vw,3.2rem);line-height:1.05;margin:.2em 0}.eyebrow{color:var(--accent);font-size:.78rem;font-weight:750;letter-spacing:.12em;text-transform:uppercase}.lede{color:var(--muted);font-size:1.08rem}label{display:block;font-weight:700;margin:26px 0 8px}input[type=email],input[type=text]{width:100%;border:1px solid var(--line);border-radius:10px;background:#0d1218;color:var(--ink);font:inherit;padding:13px 14px}.turnstile{min-height:70px;margin:18px 0}button,.button-link{border:0;border-radius:10px;background:var(--accent);color:#111;font:inherit;font-weight:800;padding:12px 17px;cursor:pointer;text-decoration:none}a{color:var(--accent)}.error{border-left:3px solid var(--danger);color:var(--danger);padding-left:12px}.topbar{height:64px;display:flex;align-items:center;justify-content:space-between;border-bottom:1px solid var(--line);padding:0 24px}.topbar form{margin:0}.quiet{background:transparent;color:var(--muted);border:1px solid var(--line);padding:7px 11px}.dashboard{width:min(900px,calc(100% - 40px));margin:10vh auto}.dashboard section{max-width:700px}.notice{margin-top:36px;border:1px solid var(--line);border-radius:14px;background:var(--panel);padding:20px}.notice p{margin-bottom:0;color:var(--muted)}h2{font-size:1.3rem;margin-top:36px}.rows{list-style:none;padding:0}.rows li{border:1px solid var(--line);border-radius:12px;background:var(--panel);padding:14px 16px;margin-bottom:10px}.warn-inline{color:var(--accent);font-weight:700}.warn{border:1px solid var(--line);border-left:3px solid var(--accent);border-radius:12px;padding:14px 16px;color:var(--muted)}.consent{margin:22px 0}.inline{display:flex;gap:10px;align-items:flex-start;font-weight:400;margin:0}.inline input{margin-top:4px}.topbar a{text-decoration:none}.actions{display:flex;gap:12px;flex-wrap:wrap;align-items:center}.actions form{margin:0}.muted{color:var(--muted);font-size:.92rem}.quiet-link{color:var(--muted)}.filters{display:flex;gap:12px;flex-wrap:wrap;align-items:flex-end;border:1px solid var(--line);border-radius:14px;background:var(--panel);padding:14px 16px;margin:24px 0}.filters label{margin:0 0 6px;font-size:.85rem}.filters select,.filters input[type=date]{border:1px solid var(--line);border-radius:10px;background:#0d1218;color:var(--ink);font:inherit;padding:9px 10px}.filters button{padding:9px 14px}details summary{color:var(--muted);cursor:pointer;font-size:.92rem;margin-top:8px}details .rows{margin:10px 0 0}details .rows li{background:#0d1218;padding:8px 12px;margin-bottom:6px;font-size:.92rem}pre.conf-block{background:#0d1218;border:1px solid var(--line);border-radius:12px;padding:16px;overflow-x:auto;font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace;font-size:.9rem;color:var(--ink);white-space:pre}.qr-container{margin:20px 0;display:inline-block;padding:16px;background:#fff;border-radius:12px}.qr-image{display:block}footer{color:var(--muted);font-size:.8rem;margin-top:64px}`
