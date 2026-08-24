# Slice 2 runbook — magic-link sign-in

Slice 2 is code-complete. This runbook turns it into the live two-browser demo after the Slice 1 origin exists. It does not create cloud resources or change NAT.

## Security properties in the build

- Magic-link and session bearer tokens are random 256-bit values; SQLite stores only SHA-256 hashes.
- Magic links expire after 15 minutes and are consumed atomically once.
- Sessions expire after 30 days and are revoked immediately on sign-out.
- `__Host-` cookies are host-only, `Secure`, `HttpOnly`, and `SameSite` protected. They are not sent to `agh.nightloch.app`.
- Every state-changing form POST is CSRF checked.
- Sign-in requests use persistent 15-minute quotas: 3 per normalized email and 10 per source IP.
- The public response does not reveal whether an email exists, was rate-limited, or failed delivery.
- `/health` performs a rolled-back SQLite write, rather than only checking that the process is alive.
- The app binds loopback only. Caddy overwrites `X-Forwarded-For` before the app uses it for the IP quota and Turnstile.

## Provider setup

1. Verify `nightloch.app` in Resend before using the locked sender `Nightloch <alerts@nightloch.app>`. The From mailbox must remain monitored.
2. Create a Cloudflare Turnstile widget for `lite.nightloch.app`.
3. Add a Resend webhook pointing at `https://lite.nightloch.app/webhooks/resend`, subscribed to `email.sent`, `email.delivered`, `email.delivery_delayed`, `email.bounced`, `email.complained`, `email.failed`, and `email.suppressed`. The last two are as final as a bounce — no message exists — so leaving them unsubscribed would let a Roster keep reading **Reachable** for an address Resend is refusing outright. Copy its signing secret (`whsec_…`) into `RESEND_WEBHOOK_SECRET`. The endpoint is the one route with no session and no CSRF token: the Svix signature over the request body is what authorizes it, and an unsigned callback is refused with `401` and changes nothing.
4. Put the Resend API key, the Resend webhook signing secret, the Turnstile secret, and the Turnstile site key into `/etc/nightloch/nightloch.env`. Start from [`deploy/nightloch.env.example`](../../deploy/nightloch.env.example), but never commit the filled file.

Use a root-owned file:

```bash
sudo install -d -m 0750 -o root -g nightloch /etc/nightloch
sudo install -m 0600 -o root -g nightloch /dev/null /etc/nightloch/nightloch.env
sudoedit /etc/nightloch/nightloch.env
```

Do not paste secret values into chat, shell history, systemd unit files, or Git.

## Install and migrate

Build through CI or copy a locally cross-compiled binary, then install it under `/opt/nightloch/bin`. The database migration is an explicit deploy step; `serve` refuses an old schema and never auto-migrates.

```bash
sudo install -d -m 0750 -o nightloch -g nightloch /opt/nightloch/bin /opt/nightloch/data
sudo install -m 0755 ./bin/nightloch-linux-amd64 /opt/nightloch/bin/nightloch
sudo -u nightloch env DATABASE_PATH=/opt/nightloch/data/nightloch.db \
  /opt/nightloch/bin/nightloch migrate
```

Install the examples from `deploy/` as `/etc/systemd/system/nightloch.service` and the `lite.nightloch.app` block in the Caddyfile. Then:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now nightloch
sudo systemctl reload caddy
curl --fail --silent https://lite.nightloch.app/health
```

Expected health body includes `"status":"ok"`. It must not include configuration or dependency details.

## Demo / verification

1. Open `https://lite.nightloch.app/sign-in` in two separate browser profiles.
2. Request links for two inboxes you control; confirm each link works once and an immediate reuse shows “That link is unavailable.”
3. Confirm signing out invalidates the old session even if its cookie value is restored.
4. Inspect the session cookie in browser developer tools: name begins `__Host-`, Domain is absent, Path is `/`, and Secure/HttpOnly/SameSite are present.
5. Visit `https://agh.nightloch.app` and confirm the Nightloch session cookie is absent.
6. Submit four link requests for one email in 15 minutes. The fourth public response remains generic and no fourth email is sent.

The live demo is not complete until both real emails have signed in successfully.
