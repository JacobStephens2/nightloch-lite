# Slice 9 runbook — health-gated staging deploys

The `ci` workflow builds one static Linux/amd64 binary after formatting, vet, and tests pass. A push to `main` deploys that exact artifact only when the repository variable `DEPLOY_ENABLED` is `true`.

The deploy performs this sequence on the staging origin:

1. Verify the candidate reports the expected Git commit SHA.
2. Create a consistent, mode-`0600` SQLite snapshot under `/opt/nightloch/backups/` without stopping the current app.
3. Run forward-only migrations as an explicit command.
4. Preserve the current binary as `/opt/nightloch/bin/nightloch.previous`.
5. Atomically replace the binary and restart `nightloch.service`.
6. Poll the `/health` endpoint for up to 30 seconds.
7. Restore and restart the previous binary if the health gate fails. Additive migrations remain in place by design.

The gate reads the endpoint's status code, and only the writable-database check sets it. `/health` also reports the enforcement plane — `tunnel_interface` and `resolver_filtering` — as named facts that never change the status code, so a tunnel or AdGuard Home outage is loud in the payload and rolls a good binary back. Read the body, not only the code:

```json
{"status":"ok","version":"…","checks":{
  "database_writable":{"status":"ok","governs_status":true},
  "tunnel_interface":{"status":"unhealthy","governs_status":false},
  "resolver_filtering":{"status":"ok","governs_status":false}}}
```

An `unhealthy` enforcement fact is also logged by name with its cause, which is the line to search when the payload says the plane is down.

No deploy occurs from pull requests, with failing tests, or while `DEPLOY_ENABLED` is absent/false. Concurrent staging deploys are serialized.

## GitHub staging environment

Create a GitHub environment named `staging` and add:

| Kind | Name | Content |
|---|---|---|
| Repository variable | `DEPLOY_ENABLED` | `true` only after the origin and service are ready |
| Environment secret | `DEPLOY_HOST` | Staging droplet hostname or IP |
| Environment secret | `DEPLOY_USER` | The SSH deployment User (`nightloch` in Slice 1) |
| Environment secret | `DEPLOY_SSH_KEY` | Dedicated private deployment key |
| Environment secret | `DEPLOY_HOST_KEY` | A pinned `known_hosts` line collected over an already-trusted channel |

If the repository's GitHub plan does not support environment secrets for private repositories, store the four values as repository Actions secrets with the same names. The workflow resolves either scope; environment secrets are preferable because their access can be tied to staging protection rules.

Do not use `ssh-keyscan` inside CI as the trust bootstrap. Verify the host key from the DigitalOcean console or an already trusted SSH session, then store the complete `known_hosts` line.

The deployment User currently has broad passwordless sudo from Slice 1 bootstrap. Narrow that before wider production use; the workflow itself needs only `systemctl restart/stop nightloch` in addition to write access under `/opt/nightloch`.

## First-deploy prerequisites

- Slice 1 origin applied and reachable.
- `/opt/nightloch/bin`, `/opt/nightloch/data`, and `/opt/nightloch/backups` owned by the deployment User.
- `/etc/nightloch/nightloch.env`, the service unit, and the Caddy reverse proxy installed from `deploy/`.
- Database initialized once with `nightloch migrate`, or allow the first workflow candidate to snapshot the empty database and migrate it.
- `curl http://127.0.0.1:8080/health` succeeds after a manual service start.
- `ADGUARD_USERNAME` and `ADGUARD_PASSWORD` set in `/etc/nightloch/nightloch.env`. Serving requires them: a process that cannot read AdGuard Home's status cannot honestly report whether anything is being filtered. Until the Slice 4 host work lands, `resolver_filtering` reports `unhealthy`, which by design fails no deploy.

Keep `DEPLOY_ENABLED=false` until those checks pass. Enabling the variable authorizes future pushes to deploy, but does not authorize `tofu apply`, NAT changes, or DNS cutover.
