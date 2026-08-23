package webapp

import (
	"fmt"
	"html/template"
	"time"
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
{{if .View.PartnerLockActive}}
<p class="lede"><strong>Partner lock is active.</strong> Your Partners can loosen your Policy; you cannot.</p>
<h2>Your Partners</h2><ul class="rows">{{range .View.Partners}}<li><strong>{{.PartnerEmail}}</strong> — Partner since {{utc .Since}}. Log sharing: {{if .LogSharing}}On{{else}}Off{{end}}.</li>{{end}}</ul>
<p class="lede">Nightloch currently supports one Partner, so <strong>Add a Partner</strong> returns once this Partnership ends.</p>
{{else}}
<p class="lede"><strong>Partner lock is inactive.</strong> You have no active Partners, so you can still loosen your own Policy.</p>
{{if not .View.PendingInvitations}}<p><a class="button-link" href="/partners/invite">Add a Partner</a></p>{{end}}
{{end}}
{{if .View.PendingInvitations}}
<h2>Invitations you sent</h2>
<p class="lede">A pending invitation grants no Partner powers and does not activate Partner lock; acceptance is required.</p>
<p class="lede">Nightloch currently supports one Partner, so <strong>Add a Partner</strong> returns once this invitation is accepted, declined, canceled, or expires.</p>
<ul class="rows">{{range .View.PendingInvitations}}<li><strong>{{.InviteeEmail}}</strong><br>
Sent {{utc .SentAt}}. Expires {{utc .ExpiresAt}}.<br>
Log sharing if accepted: {{if .LogSharing}}On{{else}}Off{{end}}. Delivery: {{.DeliveryState.Label}}.<br>
<span class="muted">The invited address and the proposed Log sharing cannot be edited. To correct either, cancel this invitation and send a new one.</span>
<a href="/partners/sent-invitations/{{.ID}}/cancel">Cancel invitation</a></li>{{end}}</ul>
{{end}}
{{if .Received}}
<h2>Partner invitations</h2>
<p class="lede">These Partner invitations were sent to {{.User.Email}}. You can answer them here without the emailed link.</p>
<ul class="rows">{{range .Received}}<li><strong>{{.ProtecteeEmail}}</strong> invited you to be their Partner.<br>
Sent {{utc .SentAt}}. Expires {{utc .ExpiresAt}}.<br>
Log sharing if you accept: {{if .LogSharing}}On — you will receive domain-log access.{{else}}Off — you will not receive domain-log access.{{end}}<br>
<a href="/partners/received-invitations/{{.ID}}">Review this invitation</a></li>{{end}}</ul>
{{end}}
{{if .Protecting}}
<h2>People you protect</h2>
<ul class="rows">{{range .Protecting}}<li><strong>{{.ProtecteeEmail}}</strong> — Partner since {{utc .Since}}. Log sharing: {{if .LogSharing}}On — you can view their DNS domain log.{{else}}Off — you cannot view their DNS domain log.{{end}}<br>
Their Roster: {{range $index, $entry := .Roster}}{{if $index}}, {{end}}{{$entry.PartnerEmail}}{{end}}</li>{{end}}</ul>
{{end}}
<div class="notice"><strong>Next milestone</strong><p>Ending a Partnership and browsing Activity arrive later in Slice 3. Device enrollment remains unavailable until the Operator adds you to the Enrollment allowlist.</p></div>
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
<p><a class="button-link" href="/partners/invite">Add a Partner</a></p>
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

const stylesheet = `:root{color-scheme:dark;--ink:#f4f0e8;--muted:#aaa69d;--panel:#161b22;--line:#303741;--accent:#b9e769;--danger:#ffb4ab}*{box-sizing:border-box}body{margin:0;background:#0b0f14;color:var(--ink);font:16px/1.55 ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.shell{min-height:100vh;display:grid;place-items:center;padding:24px}.card{width:min(100%,480px);padding:36px;border:1px solid var(--line);border-radius:18px;background:linear-gradient(145deg,#18202a,var(--panel));box-shadow:0 24px 80px #0008}h1{font-size:clamp(2rem,8vw,3.2rem);line-height:1.05;margin:.2em 0}.eyebrow{color:var(--accent);font-size:.78rem;font-weight:750;letter-spacing:.12em;text-transform:uppercase}.lede{color:var(--muted);font-size:1.08rem}label{display:block;font-weight:700;margin:26px 0 8px}input[type=email]{width:100%;border:1px solid var(--line);border-radius:10px;background:#0d1218;color:var(--ink);font:inherit;padding:13px 14px}.turnstile{min-height:70px;margin:18px 0}button,.button-link{border:0;border-radius:10px;background:var(--accent);color:#111;font:inherit;font-weight:800;padding:12px 17px;cursor:pointer;text-decoration:none}a{color:var(--accent)}.error{border-left:3px solid var(--danger);color:var(--danger);padding-left:12px}.topbar{height:64px;display:flex;align-items:center;justify-content:space-between;border-bottom:1px solid var(--line);padding:0 24px}.topbar form{margin:0}.quiet{background:transparent;color:var(--muted);border:1px solid var(--line);padding:7px 11px}.dashboard{width:min(900px,calc(100% - 40px));margin:10vh auto}.dashboard section{max-width:700px}.notice{margin-top:36px;border:1px solid var(--line);border-radius:14px;background:var(--panel);padding:20px}.notice p{margin-bottom:0;color:var(--muted)}h2{font-size:1.3rem;margin-top:36px}.rows{list-style:none;padding:0}.rows li{border:1px solid var(--line);border-radius:12px;background:var(--panel);padding:14px 16px;margin-bottom:10px}.warn{border:1px solid var(--line);border-left:3px solid var(--accent);border-radius:12px;padding:14px 16px;color:var(--muted)}.consent{margin:22px 0}.inline{display:flex;gap:10px;align-items:flex-start;font-weight:400;margin:0}.inline input{margin-top:4px}.topbar a{text-decoration:none}.actions{display:flex;gap:12px;flex-wrap:wrap;align-items:center}.actions form{margin:0}.muted{color:var(--muted);font-size:.92rem}.quiet-link{color:var(--muted)}footer{color:var(--muted);font-size:.8rem;margin-top:64px}`
