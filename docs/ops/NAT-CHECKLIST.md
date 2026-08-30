# NAT / full-tunnel ops checklist

This is the checklist for the first forwarding change on a live host: `ip_forward`,
MASQUERADE, the port 53 REDIRECT, and the port 853 REJECT. Slice 1 only opens UDP 51820 on
the cloud firewall and installs Caddy, and opening a port is not a reason to run this.

**Nothing here is typed as a one-off command.** The whole posture is files:

| Artifact | Installed at | What it is |
|---|---|---|
| `deploy/nightloch-nat.nft` | `/etc/nightloch/nightloch-nat.nft` | MASQUERADE, the 53 redirect, the 853 reject |
| `deploy/nightloch-nat.service` | `/etc/systemd/system/nightloch-nat.service` | what loads them, at every boot |
| `deploy/99-nightloch-forward.conf` | `/etc/sysctl.d/99-nightloch-forward.conf` | forwarding, across a reboot |
| `deploy/wg0.conf.template` | `/etc/wireguard/wg0.conf` | the interface, rendered once with a key made on the host |
| `deploy/nightloch-host-posture.sh` | `/usr/local/sbin/nightloch-host-posture.sh` | stage / activate / revert |
| `deploy/nightloch-nat-verify.sh` | `/usr/local/sbin/nightloch-nat-verify.sh` | reads the host and says whether the posture holds |

Why the posture is one nftables table of its own rather than ufw rules or wg-quick `PostUp`:
[ADR 0006](../adr/0006-keep-the-nat-posture-in-its-own-nftables-table.md).

## Who may do what

An agent may run `stage` and `nightloch-nat-verify.sh`. Neither enables forwarding, loads a
rule, or starts a unit. **`activate` is a human's**, after this checklist, and it refuses
without `--i-have-run-the-nat-checklist`. That flag is not a formality: it is the only thing
standing between a staged host and the first forwarding change.

## Before any change

- [ ] Confirm the DigitalOcean **web console** login for `nightloch` works. Log in now, not
      after locking yourself out. Password is in the password manager.
- [ ] Take a **snapshot** of the droplet, or confirm daily backups are green and recent.
- [ ] Open a **second SSH session** and leave it open. Do not close the first one either.
- [ ] Know the recovery path: DO control panel → droplet → **Access → Launch Droplet Console**.
- [ ] Record the current state so there is something to compare against:
      ```bash
      sudo /usr/local/sbin/nightloch-nat-verify.sh || true
      ```

## Stage

Safe before approval. Installs every file, generates the interface key, enables nothing.

Run it from the repository checkout: it reads the artifacts from its own directory.

```bash
sudo ./deploy/nightloch-host-posture.sh stage
sudo install -m 0755 deploy/nightloch-host-posture.sh /usr/local/sbin/nightloch-host-posture.sh
sudo install -m 0755 deploy/nightloch-nat-verify.sh /usr/local/sbin/nightloch-nat-verify.sh
```

`activate` and `revert` read no artifact, so from here on the copies in `/usr/local/sbin`
are the ones to run - including from the DO console, where there is no checkout.

- [ ] Staged. It prints the interface public key.
- [ ] Put that key in `/etc/nightloch/nightloch.env` as `TUNNEL_PUBLIC_KEY` and restart
      `nightloch.service`. A minted Device's configuration is wrong without it.
- [ ] `/etc/wireguard/wg0.conf` is mode `0600` and the private key is not anywhere else.

## Schedule the way back before opening the door

The revert takes no argument and asks nothing, so it works when nobody is there to answer.

```bash
sudo systemd-run --on-active=10min --unit=nightloch-nat-revert \
  /usr/local/sbin/nightloch-host-posture.sh revert
```

- [ ] Scheduled. Confirm with `systemctl list-timers nightloch-nat-revert.timer`.
- [ ] Know how to cancel it: `sudo systemctl stop nightloch-nat-revert.timer`.

## Activate - the change itself

**This is the step that needs an explicit human go-ahead.** Not an agent's.

```bash
sudo /usr/local/sbin/nightloch-host-posture.sh activate --i-have-run-the-nat-checklist
```

It applies the posture in the one order that has no unsafe window: ufw's forward policy and
the wg0 DNS allowances first, then the rules, then forwarding, then the interface.

- [ ] Activated without error.

## After the change, before cancelling the revert

- [ ] **The second session still responds.** Type something in it.
- [ ] SSH from your own address still connects: `ssh nightloch@<host> true`
- [ ] The staging site still serves: `curl -sSf https://lite.nightloch.app >/dev/null`
- [ ] Every named fact holds:
      ```bash
      sudo /usr/local/sbin/nightloch-nat-verify.sh
      ```
      `resolver_listening` reports **WARN** until the AdGuard Home ticket lands and binds
      DNS to `10.66.0.1`. That is expected here and does not fail the run: the redirect is
      correct, and until something answers on that address a Device resolves nothing.
- [ ] **From somewhere that is not the droplet**, the public address answers nothing on 53:
      ```bash
      dig +time=3 +tries=1 @<public-ip> example.com     # expect no answer
      ```
      An open recursive resolver is the one failure of this posture that harms people who are
      not using it at all. The verifier checks what is listening; only this checks what the
      internet can reach.

Only now:

- [ ] `sudo systemctl stop nightloch-nat-revert.timer`

## Then prove it survives being left alone

- [ ] Reboot the droplet with a Device on the tunnel.
- [ ] After it comes back: `sudo /usr/local/sbin/nightloch-nat-verify.sh` still passes with
      nobody having typed anything.
- [ ] The Device reconnects and browsing works.
- [ ] From the Device, DNS pointed elsewhere still reaches the resolver:
      `dig @8.8.8.8 <a-blocked-domain>` is blocked, not answered.
- [ ] From the Device, DNS-over-TLS fails fast rather than hanging: Android strict
      **Private DNS** reports that it cannot be reached within seconds.

## If it goes wrong

```bash
sudo /usr/local/sbin/nightloch-host-posture.sh revert
```

Off the host, from the DO console, the same command. Revert turns forwarding off, unloads the
table, withdraws the wg0 DNS allowances, and puts back whatever ufw's forward policy was
before activation rather than assuming it was ufw's default. It leaves `wg0` up on purpose: the tunnel is not
the forwarding change, and dropping it would disconnect anyone reverting from inside it. The
staged files stay in place, so `activate` turns it back on.

## Explicit non-goals here

- Do not run this checklist merely because Slice 1 opened 51820/udp.
- Do not combine the first NAT enable with a production `tofu destroy` experiment.
- Do not add rules by hand. A rule that is not in `deploy/nightloch-nat.nft` does not survive
  a reboot, and the verifier will not know to look for it.
