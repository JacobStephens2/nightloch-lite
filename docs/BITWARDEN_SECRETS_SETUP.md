# Bitwarden Secrets Manager Setup

## Objective

Configure Bitwarden Secrets Manager so project secrets can be injected into commands or an interactive shell without storing plaintext secrets or the Bitwarden machine-account access token on disk.

Target workflow:

```sh
bws-shell
```

or:

```sh
bws-run npm run dev
```

Secrets must exist only in:

1. Bitwarden Secrets Manager.
2. Process memory and environment variables while needed.
3. User-controlled clipboard contents during initial setup.

## Security constraints

The assisting agent **must** follow these rules:

- Never ask the user to paste a secret or access token into chat.
- Never read, print, echo, log, summarize, or inspect secret values.
- Never place `BWS_ACCESS_TOKEN` in `.zshrc`, `.env`, shell history, source code, agent memory, configuration files, notes, or command-line arguments.
- Never run `env`, `export -p`, `set`, `printenv`, or diagnostic commands that dump the complete environment inside a secrets-enabled shell.
- Never use `set -x` while a token or injected secret is present.
- Never commit secrets, access tokens, generated `.env` files, or captured command output.
- Never pass the access token using `bws --access-token ...`, because command arguments may be observable through process inspection or logs.
- Never execute untrusted packages, scripts, or binaries with injected secrets.
- Do not install an unofficial package without explicit user approval.
- Pause for the user whenever Bitwarden credentials or secret values are needed.
- Verify secrets only by checking whether a variable is set; never display its value.
- Prefer a read-only, narrowly scoped machine account.
- Recommend an expiring access token rather than a non-expiring token.

## Expected architecture

The setup consists of:

- A Bitwarden Free organization.
- A Secrets Manager subscription enabled for that organization.
- One Secrets Manager project.
- Secrets whose keys are valid POSIX environment-variable names.
- One read-only machine account assigned only to that project.
- One access token for the user's laptop.
- The official `bws` executable.
- Shell functions that prompt for the token without echoing it.

The machine-account access token is the bootstrap credential. It cannot be stored inside the same Secrets Manager project because it is required to access that project.

For the diskless configuration in this document, the user pastes the access token into a hidden terminal prompt once per parent shell session.

## Naming conventions

Use names such as:

- `OPENAI_API_KEY`
- `ANTHROPIC_API_KEY`
- `CLOUDFLARE_API_TOKEN`
- `DATABASE_URL`

Secret names should:

- Contain only uppercase ASCII letters, digits, and underscores.
- Begin with a letter or underscore.
- Match the environment-variable name expected by the application.
- Avoid security-sensitive shell variables such as `PATH`, `SHELL`, `LD_PRELOAD`, `DYLD_INSERT_LIBRARIES`, `BASH_ENV`, `ENV`, `ZDOTDIR`, `NODE_OPTIONS`, and `PYTHONPATH`.

## Phase 1: Inspect the laptop

The agent may perform these non-sensitive checks:

```sh
uname -s
uname -m
printf '%s\n' "$SHELL"
command -v bws || true
bws --version 2>/dev/null || true
command -v zsh || true
```

Expected platforms include:

- Apple Silicon macOS: `Darwin` and `arm64`.
- Intel macOS: `Darwin` and `x86_64`.
- Linux AMD64: `Linux` and `x86_64`.
- Linux ARM64: `Linux` and `aarch64`.

Do not inspect the complete environment.

## Phase 2: Enable Secrets Manager

This phase requires the user to interact with the Bitwarden web application.

1. Sign in to the Bitwarden web vault.
2. Create a Free organization if an appropriate organization does not exist.
3. Open the organization's Admin Console.
4. Navigate to **Billing → Subscription**.
5. Find **More from Bitwarden**.
6. Enable **Subscribe to Secrets Manager**.
7. Submit the change.
8. Open Secrets Manager from the Bitwarden product switcher.
9. Refresh the browser if Secrets Manager does not appear immediately.

The agent should explain each step but should not request Bitwarden credentials.

## Phase 3: Create the project

In the Secrets Manager web application:

1. Select **New → Project**.
2. Name the project according to its scope, for example `local-development`, `chart35-development`, or `eta-development`.
3. Save the project.
4. Record only the project name for now.

Use separate projects when secrets have different trust boundaries. Do not put unrelated personal, production, and development credentials into one project.

## Phase 4: Add secrets

Add each secret through the Bitwarden web interface:

1. Select **New → Secret**.
2. Enter the environment-variable name as the secret key.
3. Paste the secret value directly into Bitwarden.
4. Assign the secret to the project created above.
5. Add a non-sensitive note describing its purpose, owner, and rotation policy.
6. Save the secret.

The agent **must not** ask the user to paste values into chat or an agent-controlled terminal command.

Do not use CLI commands like the following during initial setup because they would put the secret in shell history:

```sh
bws secret create OPENAI_API_KEY actual-secret-value PROJECT_ID
```

## Phase 5: Create a machine account

In Secrets Manager:

1. Select **New → Machine account**.
2. Name it after the device and purpose, for example `jacob-macbook-local-dev`.
3. Open the machine account.
4. Open its **Projects** tab.
5. Assign only the intended project.
6. Grant **Can read** permission, not **Can read, write**.

Read-only access is sufficient for runtime injection.

## Phase 6: Create an access token

In Secrets Manager:

1. Open **Machine accounts**.
2. Select the laptop's machine account.
3. Open **Access tokens**.
4. Select **Create access token**.
5. Use a descriptive name, such as `jacob-macbook-interactive-2026-07`.
6. Choose a reasonable expiration date.
7. Create the token.
8. Copy it when displayed.

Important:

- Bitwarden displays the token only once.
- Do not paste it into chat.
- Do not put it in a shell command.
- Keep it in the clipboard only until the hidden terminal prompt requests it.
- If the token is exposed, revoke it and create a replacement.

## Phase 7: Install `bws`

Use only the official Bitwarden Secrets Manager CLI release published in the Bitwarden SDK releases repository:

<https://github.com/bitwarden/sdk/releases>

The agent should:

1. Determine the operating system and architecture.
2. Identify the current stable release.
3. Download the matching official archive.
4. Verify a published checksum or signature when Bitwarden provides one.
5. Extract the `bws` executable.
6. Install it in an existing user-owned `PATH` directory, preferably `~/.local/bin/bws`.
7. Restrict executable permissions appropriately:

```sh
chmod 0755 ~/.local/bin/bws
```

8. Confirm installation:

```sh
command -v bws
bws --version
bws --help
```

If `~/.local/bin` is not already on `PATH`, adding the directory itself to `.zshrc` is safe:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Do not confuse `bws` with `bw`:

- `bws` is the Bitwarden Secrets Manager CLI used by this document.
- `bw` is the regular Bitwarden Password Manager CLI.

The agent must not silently install a similarly named third-party package.

## Phase 8: Authenticate temporarily

Run this in an interactive `zsh` session:

```zsh
read -rs "BWS_ACCESS_TOKEN?Paste Bitwarden access token: "
printf '\n'
export BWS_ACCESS_TOKEN
```

The prompt does not echo the token, and the token does not become part of shell history.

After pasting, clear the clipboard using the platform-appropriate command.

On macOS:

```sh
printf '' | pbcopy
```

On Linux with Wayland:

```sh
printf '' | wl-copy
```

Do not clear the clipboard until the token has been successfully accepted.

Confirm authentication without displaying secrets:

```sh
bws project list --output table
```

This command should list only projects accessible to the machine account.

If authentication fails:

1. Do not print the token.
2. Run `unset BWS_ACCESS_TOKEN`.
3. Confirm the token has not expired or been revoked.
4. Confirm the machine account is assigned to the project.
5. Prompt for the token again using `read -rs`.

## Phase 9: Find the project ID

Run:

```sh
bws project list --output table
```

Identify the UUID corresponding to the intended project.

The project ID is metadata, not a secret. It may be stored in shell configuration.

Do not use `bws secret list` merely to test access because its output can include secret values.

## Phase 10: Add shell helpers

Add the following to `~/.zshrc`, replacing the placeholder project UUID.

This configuration stores only the non-secret project ID and shell functions. It does not store the access token or application secrets.

```zsh
export BWS_PROJECT_ID="REPLACE_WITH_PROJECT_UUID"

bws-auth() {
  if [[ -n "${BWS_ACCESS_TOKEN:-}" ]]; then
    printf '%s\n' "BWS_ACCESS_TOKEN is already loaded in this shell."
    return 0
  fi

  read -rs "BWS_ACCESS_TOKEN?Paste Bitwarden access token: "
  printf '\n'
  export BWS_ACCESS_TOKEN

  if ! bws project get "$BWS_PROJECT_ID" --output none; then
    printf '%s\n' "Bitwarden authentication or project access failed." >&2
    unset BWS_ACCESS_TOKEN
    return 1
  fi

  if command -v pbcopy >/dev/null 2>&1; then
    printf '' | pbcopy
  elif command -v wl-copy >/dev/null 2>&1; then
    printf '' | wl-copy
  fi

  printf '%s\n' "Bitwarden Secrets Manager authenticated."
}

bws-logout() {
  unset BWS_ACCESS_TOKEN
  printf '%s\n' "Bitwarden access token removed from this shell."
}

bws-shell() {
  bws-auth || return 1

  bws run \
    --project-id "$BWS_PROJECT_ID" \
    --shell zsh \
    -- 'exec zsh'
}

bws-run() {
  if (( $# == 0 )); then
    printf '%s\n' "Usage: bws-run <command> [arguments...]" >&2
    return 2
  fi

  bws-auth || return 1

  local command_string
  command_string="${(j: :)${(q)@}}"

  bws run \
    --project-id "$BWS_PROJECT_ID" \
    --shell zsh \
    -- "$command_string"
}
```

Reload the configuration:

```sh
source ~/.zshrc
```

The agent should inspect the installed CLI's current help before finalizing the functions:

```sh
bws run --help
```

If the installed release uses different syntax, adapt the wrapper to the installed version while preserving all security constraints.

## Phase 11: Verify injection

Start a secrets-enabled child shell:

```sh
bws-shell
```

Inside the child shell, verify expected variables without revealing values:

```zsh
if [[ -n "${OPENAI_API_KEY:-}" ]]; then
  printf '%s\n' "OPENAI_API_KEY is set"
else
  printf '%s\n' "OPENAI_API_KEY is missing"
fi
```

A compact check for several variables is:

```zsh
for name in OPENAI_API_KEY ANTHROPIC_API_KEY; do
  if [[ -n "${(P)name:-}" ]]; then
    printf '%s\n' "$name is set"
  else
    printf '%s\n' "$name is missing"
  fi
done
```

Never verify with:

```sh
echo "$OPENAI_API_KEY"
printenv OPENAI_API_KEY
env
export -p
```

Exit the child shell when finished:

```sh
exit
```

The injected project secrets disappear with that child process. The parent shell may still contain `BWS_ACCESS_TOKEN`; remove it with:

```sh
bws-logout
```

## Phase 12: Run applications

Run an application with injected secrets:

```sh
bws-run npm run dev
bws-run pnpm dev
bws-run cargo run
bws-run python -m my_application
bws-run terraform plan
```

The launched command and every child process can access all secrets injected from the selected project. Only run trusted commands.

Prefer project-specific machine accounts and projects when an application should not receive every development secret.

## Optional clean environment

Bitwarden supports `--no-inherit-env`, which removes most inherited variables while retaining `PATH` and some shell-provided variables.

Example:

```sh
bws run \
  --project-id "$BWS_PROJECT_ID" \
  --no-inherit-env \
  --shell zsh \
  -- 'npm run dev'
```

This does not create a sandbox. The command retains the normal filesystem, network, process, and user permissions of the invoking account.

Test application compatibility before making this the wrapper default.

## Optional macOS Keychain bootstrap

This section is optional and does not meet the strictest interpretation of “never store the access token on disk.” macOS Keychain stores encrypted data on disk and may be acceptable if the goal is to avoid plaintext files.

Do not enable this mode without explicit user approval.

Store the token without placing it in shell history:

```zsh
read -rs "token?Paste Bitwarden access token: "
printf '\n'
security add-generic-password \
  -a "$USER" \
  -s "bitwarden-bws-local-dev" \
  -w "$token" \
  -U
unset token
```

Load it for the current shell:

```sh
export BWS_ACCESS_TOKEN="$(
  security find-generic-password \
    -a "$USER" \
    -s "bitwarden-bws-local-dev" \
    -w
)"
```

Delete it:

```sh
security delete-generic-password \
  -a "$USER" \
  -s "bitwarden-bws-local-dev"
```

An agent must ask the user before storing the token in Keychain.

## Troubleshooting

### `bws: command not found`

Check:

```sh
command -v bws
printf '%s\n' "$PATH"
```

Ensure the executable is in a directory on `PATH`.

### Unauthorized or authentication failure

Check only non-sensitive state:

```sh
[[ -n "${BWS_ACCESS_TOKEN:-}" ]] \
  && printf '%s\n' "token is loaded" \
  || printf '%s\n' "token is not loaded"
```

Then confirm:

- The token has not expired.
- The token has not been revoked.
- The machine account still exists.
- The machine account has read access to the project.
- The correct Bitwarden region is configured.

Never print the token while troubleshooting.

### Project not found

Run:

```sh
bws project list --output table
```

Confirm `BWS_PROJECT_ID` matches the intended project.

### EU-hosted Bitwarden

Configure the EU server:

```sh
bws config server-base https://vault.bitwarden.eu
```

This writes non-secret server configuration to the `bws` configuration directory.

### Self-hosted Bitwarden

Configure the instance URL:

```sh
bws config server-base https://bitwarden.example.com
```

Use the actual trusted HTTPS endpoint. Do not disable TLS verification.

### Variable is missing

Check:

- The secret belongs to the selected project.
- The machine account can read that project.
- The secret key exactly matches the expected variable name.
- The key is POSIX-compatible.
- The command was launched inside `bws run`.

Do not list or print secret values as part of diagnosis.

### Application behaves unexpectedly

Inspect variable names in the Bitwarden web interface. Security-sensitive names can change shell or runtime behavior before the application starts.

Do not create secrets named `PATH`, `SHELL`, `BASH_ENV`, `ENV`, `ZDOTDIR`, `LD_PRELOAD`, `DYLD_INSERT_LIBRARIES`, `NODE_OPTIONS`, or `PYTHONPATH` unless their effects are deliberate and fully understood.

## Rotation procedure

To rotate an application secret:

1. Generate the replacement at the upstream provider.
2. Update its value in Bitwarden Secrets Manager.
3. Start a new `bws-run` process or `bws-shell`.
4. Test the replacement without printing it.
5. Revoke the old credential at the upstream provider.
6. Exit older secrets-enabled processes.

Existing processes retain their original environment and will not automatically receive the rotated value.

To rotate the machine-account access token:

1. Create a new token with an expiration date.
2. Load and test it using the hidden prompt.
3. Revoke the old token in Secrets Manager.
4. Remove it from existing shells with `bws-logout`.
5. Exit any parent shells that may still contain it.

## Revocation procedure

If the laptop is lost or the access token may be exposed:

1. Open Bitwarden Secrets Manager from another trusted device.
2. Open the machine account.
3. Open **Access tokens**.
4. Revoke the affected token.
5. Rotate application credentials if there is evidence they were accessed.
6. Review available Bitwarden and upstream-provider audit information.

## Completion criteria

Setup is complete only when all checks pass:

- `bws --version` succeeds.
- Secrets Manager is active on the organization.
- A project exists.
- Secret keys are POSIX-compatible environment-variable names.
- A dedicated machine account has read-only project access.
- An expiring access token exists.
- The token is not stored in plaintext or shell history.
- `bws project get "$BWS_PROJECT_ID" --output none` succeeds.
- `bws-shell` creates a child shell.
- Expected variables are set inside that child shell.
- Secret values were never displayed during verification.
- Exiting the child shell removes injected secrets from the active context.
- `bws-logout` removes the access token from the parent shell.

## Agent stopping points

The agent **must** stop and ask the user to perform an action when:

- Bitwarden authentication is required.
- A secret value must be entered.
- The machine-account access token is displayed.
- The access token must be pasted into the hidden terminal prompt.
- Keychain or another persistent credential store is proposed.
- A downloaded binary cannot be verified as an official Bitwarden release.
- Existing shell configuration must be overwritten rather than safely appended.
- A permission broader than read-only appears necessary.
- A secret value is accidentally exposed.

When stopping, describe the required action without requesting that any secret be shared with the agent.

## Official references

- [Secrets Manager CLI](https://bitwarden.com/help/secrets-manager-cli/)
- [Secrets Manager Quick Start](https://bitwarden.com/help/secrets-manager-quick-start/)
- [Secrets Manager Plans](https://bitwarden.com/help/secrets-manager-plans/)
- [Machine Accounts](https://bitwarden.com/help/machine-accounts/)
- [Access Tokens](https://bitwarden.com/help/access-tokens/)
