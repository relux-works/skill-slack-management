# Authentication and Setup

`slack-mgmt` stores workspace-scoped Slack Web API access tokens without
printing them during setup, resolution, inspection, or cleanup.

## Token Semantics

Bot access tokens (`xoxb-`) are the recommended credential for app-identity
operations. User access tokens (`xoxp-`) are supported where acting as a user is
intentional. Rotating access-token forms `xoxe.xoxb-` and `xoxe.xoxp-` are
accepted as opaque access tokens, but refresh-token storage and renewal are not
implemented; auth output reports `rotation_supported=false`.

Workflow (`xwfp-`), app-level (`xapp-`), refresh/configuration (`xoxe-`), and
unknown token families are refused as `unsupported_token_family`. Prefix
classification is metadata, not authorization: each Slack method remains
responsible for scope errors such as `missing_scope`.

## Source Selection

Canonical sources are `auto`, `keychain`, `env`, and `file`.
`env_or_file` remains an explicit compatibility selector.

| Source | Behavior |
| --- | --- |
| `auto` | macOS Keychain on Darwin, Windows Credential Manager on Windows, environment only elsewhere |
| `keychain` | Native secure store only; absence or failure never falls back |
| `env` | `SLACK_ACCESS_TOKEN`, then `SLACK_BOT_TOKEN` |
| `file` | Selected workspace profile in `os.UserConfigDir()/slack-mgmt/auth.json` only |
| `env_or_file` | Environment precedence above, then the selected file profile |

`file` and `env_or_file` are explicit lower-security modes. Auth output marks
them with `security=lower` and `plaintext_file_possible=true`. A child process
cannot persistently set or clear its parent shell, so `auth set-access --source
env` and `auth clear-access --source env` refuse.

## Workspace Profiles and File Safety

Workspace labels are trimmed and lowercased; an empty label selects the legacy
top-level default token. Named profiles use this shape:

```json
{
  "profiles": {
    "acme": {
      "access_token": "your-token-here"
    }
  }
}
```

Writes preserve unrelated profiles and publish `auth.json` through a
same-directory temporary file, restrictive protection, file sync/close, atomic
rename, and directory sync. Unix directory and file modes are repaired to
`0700` and `0600`; Windows applies and validates a protected current-user-only
DACL on the directory and file. The native secure store remains the Windows
`auto` default. Failed publication removes its temporary file. Malformed or
unreadable files and files whose current-user protection cannot be validated are
errors, not credential absence.

Prefer stdin so the token does not enter shell history:

```bash
printf '%s\n' "$SLACK_ACCESS_TOKEN" |
  slack-mgmt auth set-access --workspace acme --token-stdin
```

Explicit Windows-friendly file setup is available when the lower-security
tradeoff is intentional:

```powershell
$env:SLACK_ACCESS_TOKEN | slack-mgmt.exe auth set-access --workspace acme --source file --token-stdin
```

Inspection never includes a token:

```bash
slack-mgmt auth whoami --workspace acme --check=false
slack-mgmt auth resolve --workspace acme
slack-mgmt auth clear-access --workspace acme
slack-mgmt auth config-path
```

Resolved credentials are pinned to `https://slack.com/api`. A non-Slack
`--base-url`, TLS downgrade, host change, userinfo confusion, nonstandard port,
or redirect outside the normalized API prefix is refused as
`credential_origin_refused` before authorization can be forwarded.

## Setup Contracts

Run `./setup.sh --install-only` on macOS or `.\setup.ps1 -InstallOnly` on
Windows. Both canonical installers build and install the CLI, install the shared
skill, refresh the Claude/Codex links, write secret-free install state, and
verify `version`, `q schema()`, `m schema()`, and `auth config-path` against the
installed executable.

Windows additionally installs an extensionless UTF-8-no-BOM `slack-mgmt` shell
shim beside `slack-mgmt.exe`, forwards every argument to the adjacent
executable, and adds the bin directory to both the current process and user
`PATH`. Root `setup.sh` and `setup.ps1` wrappers delegate to the canonical
scripts while preserving arguments and exit status.
