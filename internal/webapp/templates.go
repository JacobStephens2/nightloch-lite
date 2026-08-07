package webapp

import (
	"fmt"
	"html/template"
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
	"home": `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Nightloch</title><link rel="stylesheet" href="/assets/style.css"></head>
<body><header class="topbar"><strong>Nightloch</strong><form method="post" action="/sign-out"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><button class="quiet" type="submit">Sign out</button></form></header>
<main class="dashboard"><section><p class="eyebrow">Signed in as {{.User.Email}}</p><h1>Your protection starts with a Partner</h1>
<p class="lede">You do not have any Partners yet. Until you add one, you can loosen your own Policy; Nightloch’s partner lock is not active.</p>
<div class="notice"><strong>Next milestone</strong><p>Partner invitations and Your Partners arrive in Slice 3. Device enrollment remains unavailable until the Operator adds you to the Enrollment allowlist.</p></div>
</section><footer>Version {{.Version}}</footer></main></body></html>`,
}

func parseTemplates() (map[string]*template.Template, error) {
	parsed := make(map[string]*template.Template, len(templateSources))
	for name, source := range templateSources {
		tmpl, err := template.New(name).Parse(source)
		if err != nil {
			return nil, fmt.Errorf("parse %s template: %w", name, err)
		}
		parsed[name] = tmpl
	}
	return parsed, nil
}

const stylesheet = `:root{color-scheme:dark;--ink:#f4f0e8;--muted:#aaa69d;--panel:#161b22;--line:#303741;--accent:#b9e769;--danger:#ffb4ab}*{box-sizing:border-box}body{margin:0;background:#0b0f14;color:var(--ink);font:16px/1.55 ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.shell{min-height:100vh;display:grid;place-items:center;padding:24px}.card{width:min(100%,480px);padding:36px;border:1px solid var(--line);border-radius:18px;background:linear-gradient(145deg,#18202a,var(--panel));box-shadow:0 24px 80px #0008}h1{font-size:clamp(2rem,8vw,3.2rem);line-height:1.05;margin:.2em 0}.eyebrow{color:var(--accent);font-size:.78rem;font-weight:750;letter-spacing:.12em;text-transform:uppercase}.lede{color:var(--muted);font-size:1.08rem}label{display:block;font-weight:700;margin:26px 0 8px}input[type=email]{width:100%;border:1px solid var(--line);border-radius:10px;background:#0d1218;color:var(--ink);font:inherit;padding:13px 14px}.turnstile{min-height:70px;margin:18px 0}button,.button-link{border:0;border-radius:10px;background:var(--accent);color:#111;font:inherit;font-weight:800;padding:12px 17px;cursor:pointer;text-decoration:none}a{color:var(--accent)}.error{border-left:3px solid var(--danger);color:var(--danger);padding-left:12px}.topbar{height:64px;display:flex;align-items:center;justify-content:space-between;border-bottom:1px solid var(--line);padding:0 24px}.topbar form{margin:0}.quiet{background:transparent;color:var(--muted);border:1px solid var(--line);padding:7px 11px}.dashboard{width:min(900px,calc(100% - 40px));margin:10vh auto}.dashboard section{max-width:700px}.notice{margin-top:36px;border:1px solid var(--line);border-radius:14px;background:var(--panel);padding:20px}.notice p{margin-bottom:0;color:var(--muted)}footer{color:var(--muted);font-size:.8rem;margin-top:64px}`
