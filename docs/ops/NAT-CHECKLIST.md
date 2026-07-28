# NAT / full-tunnel ops checklist (draft)

**Do not execute in Slice 1.** Slice 1 only opens UDP 51820 on the cloud firewall and installs Caddy.

This checklist is for **Slice 4** before enabling `ip_forward`, MASQUERADE, DNS REDIRECT, or REJECT 853.

## Before any change

- [ ] Confirm DigitalOcean **web console** login works for `nightloch` (password in password manager).
- [ ] Take a **snapshot** of the droplet (or confirm daily backups are green).
- [ ] Open a second session / console tab before applying firewall rules.
- [ ] Schedule a **self-cancelling revert** if using iptables/nftables interactively:
  ```bash
  # Example pattern — adapt to your tool
  echo "nft -f /root/fw.good" | at now + 10 minutes
  ```
- [ ] Know recovery: DO control panel → droplet → **Access → Launch Droplet Console**.

## Intended rules (Slice 4)

- [ ] `net.ipv4.ip_forward=1` (persist in sysctl.d)
- [ ] MASQUERADE/SNAT from `10.66.0.0/24` out public iface
- [ ] REDIRECT TCP+UDP 53 from `10.66.0.0/24` → `10.66.0.1:53`
- [ ] REJECT TCP+UDP 853 from `10.66.0.0/24` (Android Private DNS strict-mode messaging prepared)
- [ ] AGH DNS bound **only** to `10.66.0.1` (not `0.0.0.0`)
- [ ] Confirm **no open recursive resolver** on public IP:53

## After change

- [ ] SSH still works from your IP
- [ ] `https://lite.nightloch.app` still serves
- [ ] From a WG peer (when exists): general web works; blocked domain fails
- [ ] Cancel self-reverting `at` job if change succeeded
- [ ] Document reboot + client blackhole recovery notes

## Explicit non-goals here

- Do not run this checklist merely because Slice 1 opened 51820/udp.
- Do not combine first NAT enable with a production `tofu destroy` experiment.
