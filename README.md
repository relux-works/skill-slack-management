# slack-mgmt

`slack-mgmt` is a Go CLI and reusable agent skill for secure Slack
investigation and bounded message management. It provides compact structured
reads, cross-channel and cross-thread search, guarded message/reaction writes,
per-workspace provider routing, attachment staging, and built-in redaction of
PII and secrets.

## Highlights

- Agent-facing `q`, `grep`, and `m` interfaces with stable JSON and compact output.
- Workspace-scoped bot or user tokens stored in macOS Keychain or Windows Credential Manager.
- Web API reads and writes, silent authenticated Chrome reads on macOS, and an opt-in Slackdump v4 read provider.
- Deterministic redaction for tokens, passwords, API keys, emails, phone numbers, and sensitive URL parameters on every CLI output path.
- Thread-aware history, replies, search, posting, updates, deletes, and reactions.
- Local attachment materialization and safe staging without leaking source paths.
- Idempotent macOS and Windows setup for the CLI and shared Claude/Codex skill artifact.

## Install

From a source checkout:

```bash
./setup.sh
```

On Windows PowerShell:

```powershell
.\setup.ps1
```

The default installation needs no external runtime tools beyond the platform's
native secure store. It installs `slack-mgmt` into `~/.local/bin`, copies a
de-gitized skill artifact to `~/.agents/skills/slack-management`, links it into
the Claude and Codex skill directories, and runs credential-free smokes.

Optional integrations are explicit:

```bash
./setup.sh --with-attachments   # installs the agents-attachments bridge; requires agents-infra
./setup.sh --with-browser       # verifies mac-chrome-session; macOS only
./setup.sh --with-slackdump     # verifies an installed official Slackdump v4
```

```powershell
.\setup.ps1 -WithAttachments
.\setup.ps1 -WithSlackdump
```

`-WithBrowser` fails explicitly on Windows because browser transport is
macOS-only. External provider credentials remain owned by their provider and
are never copied into `slack-mgmt`.

## Project Layout

- `SKILL.md`: installed agent workflow contract.
- `cmd/slack-mgmt/`: CLI entrypoint.
- `internal/config/`: workspace config and secure credential resolution.
- `internal/slack/`: Web API and sealed browser transports.
- `internal/provider/`: normalized provider protocol and Slackdump adapter.
- `internal/query/`: structured query DSL and projections.
- `internal/mutate/`: guarded write DSL.
- `internal/redact/`: shared structured/free-text egress redaction.
- `internal/attachments/`: attachment manifest and staging boundary.
- `references/`: detailed auth, provider, browser, API, and attachment contracts.
- `testdata/`: sanitized fixtures and golden outputs.

The [free human-facing Slack CLI comparison](references/free-human-slack-cli-research.md)
ranks maintained and historical tools for hands-on support investigation,
thread inspection, filtering, and offline analysis.

## Safety Rules

- Prefer `q` for structured reads, `grep` for scoped text discovery, and `m` for guarded mutations.
- Keep Slack secrets in native secure storage; use `--token-stdin`, never argv.
- Use `m --dry-run` before a write. Never use a live Slack mutation as automated delivery evidence.
- Treat staged attachment bytes as sensitive until separately inspected and sanitized.

## Current CLI Surface

Current commands:

- `slack-mgmt version`
- `slack-mgmt auth config-path`
- `printf '%s\n' "$SLACK_ACCESS_TOKEN" | slack-mgmt auth set-access --workspace acme --token-stdin`
- `slack-mgmt auth whoami --workspace acme`
- `slack-mgmt auth resolve --workspace acme`
- `slack-mgmt auth clear-access --workspace acme`
- `slack-mgmt workspace set --workspace acme --read-transport api`
- `slack-mgmt workspace set --workspace acme --read-transport browser --browser-url https://app.slack.com/client/T01234567`
- `slack-mgmt workspace show --workspace acme`
- `slack-mgmt workspace set --workspace acme --provider slackdump --slackdump-executable slackdump --slackdump-workspace acme --slackdump-authorize-external`
- `slack-mgmt workspace diagnose --workspace acme`
- `slack-mgmt q 'provider_capabilities()' --workspace acme --format json`
- `slack-mgmt q 'schema()' --format json`
- `slack-mgmt m 'schema()' --format json`
- `slack-mgmt q 'attachments() { overview }' --format compact`
- `slack-mgmt q 'attachment(att-1) { full }' --format json`
- `slack-mgmt q 'auth_test() { default }' --workspace acme --format compact`
- `slack-mgmt q 'conversations(limit=20, types="public_channel,private_channel") { overview }' --workspace acme --format compact`
- `slack-mgmt q 'conversation(general) { full }' --workspace acme --format compact`
- `slack-mgmt q 'history(general, limit=20) { overview }' --workspace acme --format compact`
- `slack-mgmt q 'replies(general, ts="1710000000.000100") { full }' --workspace acme --format compact`
- `slack-mgmt q 'search_messages("error in:#alerts", count=20, page=1) { overview }' --workspace acme --format compact`
- `slack-mgmt q 'search_info() { default }' --workspace acme --format compact`
- `slack-mgmt q 'search_context("What is project gizmo?", content_types="messages,files", include_context_messages=true) { overview }' --workspace acme --format compact`
- `slack-mgmt m 'post_message(general, text="hello world")' --workspace acme --dry-run --format compact`
- `slack-mgmt m 'post_message(general, text="hello world")' --workspace acme --format compact`
- `slack-mgmt m 'post_message(C123, text="reply", thread_ts="1710000000.000100")' --workspace acme --format json`
- `slack-mgmt m 'update_message(C123, ts="1710000005.000600", text="edited text")' --workspace acme --format compact`
- `slack-mgmt m 'delete_message(C123, ts="1710000005.000600")' --workspace acme --dry-run --format json`
- `slack-mgmt m 'delete_message(C123, ts="1710000005.000600")' --workspace acme --confirm --format json`
- `slack-mgmt m 'add_reaction(C123, ts="1710000005.000600", name="thumbsup")' --workspace acme --format compact`
- `slack-mgmt m 'remove_reaction(general, ts="1710000005.000600", name=":wave:")' --workspace acme --confirm --format json`
- `slack-mgmt q 'users(limit=50) { overview }' --workspace acme --format compact`
- `slack-mgmt grep 'attachment flow' --format compact`
- `slack-mgmt attachment materialize --format compact`
- `slack-mgmt attachment stage att-1 --destination .temp/artifacts/ --format json`

Current scope is local-first:

- runtime attachment manifest inspection
- local attachment staging into workflow destinations
- keychain-first Slack token bootstrap with explicit env/file compatibility modes
- live Slack reads for `auth.test`, `conversations.list`, `conversations.info`, `conversations.history`, `conversations.replies`, `search.messages`, `assistant.search.info`, `assistant.search.context`, and `users.list`
- provider-neutral normalized reads with stable capability discovery; Web API uses `api` or `browser` transport and Slackdump v4 is a separate read-only provider
- narrow writes for message lifecycle and reactions through `m`
- repo-scoped grep for references and fixtures
- setup/install bootstrap for the local skill artifact and CLI

Next live Slack layer can branch from message writes and reactions into files and broader workflow mutations.

## Sensitive Output

All JSON, compact, grep, auth, mutation, attachment, and stderr output passes
through one deterministic redactor. Markers use an installation-stable private
salt stored under the user config directory; Slack IDs and message timestamps
remain usable, while tokens, bearer values, labeled secrets/API keys, email
addresses, supported phone forms, and sensitive URL parameters are replaced.
`users() { overview }` omits email; an explicit email projection returns a
marker.
Explicit projections and presets are fail-closed: an unsupported or misspelled
field returns a local error before provider, credential, browser, or network
initialization instead of widening output to the default preset.

Attachment commands return a relative private `local_path` under
`.temp/slack-mgmt/attachment-aliases/`, never the manifest source path or stage
destination. The alias is a byte-identical copy published with no-replace
collision handling and current-user-only protection: mode `0600` on Unix and a
protected current-user DACL on Windows. The installation salt uses the same
platform protection. Batch publication rolls back every alias created by a
failed invocation, and symlinked alias-root components are refused before alias
bytes are written. `attachment stage` still creates the requested destination
first; it refuses same-file, symlink, and hard-link source/destination identities
before truncation. Binary content is copied unchanged and is not claimed to be
sanitized.

## Auth Bootstrap

Supported token sources:

- `auto`
- `keychain`
- `env`
- `file`
- `env_or_file`

Default behavior:

- macOS and Windows default to the native secure store through `keychain`
  (macOS Keychain / Windows Credential Manager)
- other platforms default to environment variables only
- `auto` never falls back to the plaintext auth file

Useful commands:

- `printf '%s\n' "$SLACK_ACCESS_TOKEN" | go run ./cmd/slack-mgmt auth set-access --workspace acme --token-stdin`
- `go run ./cmd/slack-mgmt auth whoami --workspace acme`
- `go run ./cmd/slack-mgmt auth resolve --workspace acme`
- `go run ./cmd/slack-mgmt auth clear-access --workspace acme`

Source precedence:

1. Explicit `keychain`, `env`, or `file` selects only that source.
2. Explicit compatibility source `env_or_file` checks
   `SLACK_ACCESS_TOKEN`, then `SLACK_BOT_TOKEN`, then the selected workspace
   profile in `os.UserConfigDir()/slack-mgmt/auth.json`.
3. Missing secure-store credentials and secure-store read failures do not fall
   back to the file.

`file` and `env_or_file` are explicit lower-security modes. Their status output
reports `security=lower` and `plaintext_file_possible=true`. File replacement
uses a same-directory temporary file, sync/close, atomic rename, and mode `0600`
plus directory mode `0700` on Unix. Windows applies and validates a protected
current-user-only DACL on both the auth directory and file. Profiles are keyed
by normalized workspace name. File-backed reads fail closed when that
protection is invalid; `env_or_file` does not touch the file after resolving a
valid environment token. Inspection reports only presence, source, token
family, storage metadata, and `rotation_supported=false`; it never prints the
credential. Bot (`xoxb-`), user (`xoxp-`), and their rotating access-token forms
are accepted; workflow, app-level, refresh/configuration, and unknown credential
families are refused.

See [the auth configuration reference](references/auth-config.md) for the full
storage and lifecycle contract.

Search caveat:

- `search_messages(...)` maps to Slack's legacy `search.messages`
- it needs a user token with `search:read`
- Slack recommends newer search APIs for assistants, so keep this as a compatibility read path, not the final long-term interface

Real-time search caveat:

- `search_info()` and `search_context(...)` map to Slack's newer Real-time Search API
- `search_context(...)` with bot tokens requires an `action_token`; user tokens do not
- modern scopes are granular: `search:read.public` is the base, with `search:read.private`, `search:read.im`, `search:read.mpim`, `search:read.files`, and `search:read.users` depending on the search surface you need
- Slack recommends keeping search calls low per user inquiry because `assistant.search.context` has special rate limits

Mutation safety:

- `m --dry-run` validates one mutation and renders a sanitized zero-request plan before credential or client resolution
- mutation batches, duplicate/unknown parameters, conflicting targets, invalid booleans, and field projections are rejected locally
- reaction mutations require exactly one of `ts` or `timestamp`; supplying both is rejected before credential resolution
- `delete_message(...)` and `remove_reaction(...)` require `--confirm` for execution; confirmation does not bypass validation, auth, origin, or redaction gates
- valid HTTP 429 responses are retried with bounded, signal-cancellable `Retry-After` handling: at most three read attempts and two write attempts, with each wait above 60 seconds refused instead of sleeping (at most 120 seconds total retry waiting for a read and 60 seconds for a write)
- missing, malformed, negative, or over-bound `Retry-After` metadata and non-429 transport, 5xx, decode, or Slack API failures are never retried as writes
- automated delivery evidence remains fake-only or dry-run-only; do not use these commands for live-write validation

## Per-Workspace Read Providers

Each workspace profile selects one provider. Profiles without an entry default
to the Web API provider over `api`, preserving the existing Keychain-backed
behavior:

```bash
slack-mgmt workspace set --workspace acme --read-transport api
slack-mgmt workspace show --workspace acme
```

Browser profiles reuse an authenticated macOS/Google Chrome workspace tab:

```bash
slack-mgmt workspace set \
  --workspace acme \
  --read-transport browser \
  --browser-url https://app.slack.com/client/T01234567
```

Install a compatible `mac-infra` build so `mac-chrome-session` is on `PATH`,
manually enable Chrome's
`View -> Developer -> Allow JavaScript from Apple Events` setting, and sign in
to that workspace tab. Reads inspect only sanitized tab metadata, select the
matching exact window/tab deterministically, and open a missing workspace in
the background with `/usr/bin/open -g`. The sealed `slack-read` command pins
the exact tab, `https://app.slack.com` origin, and workspace ID inside every
start and poll script. Page-owned authorization never leaves Chrome, while
returned data still passes through the normal CLI redactor.

The compatible sealed reader currently supports `auth.test`, conversation
list/info/history/replies, `users.list`, and `search.messages`. Other browser
reads fail as an explicit capability error instead of falling back to API.

Browser mode never focuses Chrome, selects a tab, clicks UI, uses the active
tab, or falls back to another workspace/API credential. Mutations remain
API-only and fail with `browser_write_unsupported` before credential or browser
work. Linux and Windows fail explicitly with
`browser_transport_unsupported_platform`.
See [the browser transport reference](references/browser-transport.md) for the
full setup and failure contract.

Slackdump is a separate provider, not another Web API transport:

```bash
slack-mgmt workspace set \
  --workspace acme \
  --provider slackdump \
  --slackdump-executable slackdump \
  --slackdump-workspace acme \
  --slackdump-authorize-external
slack-mgmt workspace diagnose --workspace acme
```

Slackdump authorization remains external and tool-owned. Adapter version 1
accepts official v4 releases and exposes only conversation and user reads;
unsupported operations fail before execution. See
[the Slackdump provider reference](references/slackdump-provider.md) for
credential ownership, execution bounds, and Enterprise security-alert risks,
and [the provider development guide](references/providers.md) for extension
rules.

Write caveat:

- `post_message(...)` maps to `chat.postMessage`
- `update_message(...)` maps to `chat.update`
- `delete_message(...)` maps to `chat.delete`
- `add_reaction(...)` maps to `reactions.add`
- `remove_reaction(...)` maps to `reactions.remove`
- message mutations use `chat:write`; `chat:write.public` is additionally needed when the app must post to public channels it has not joined
- reaction mutations use `reactions:write`
- use channel IDs when possible; channel names are accepted here only as a convenience and get resolved through `conversations.list`
- for direct-message updates and deletes, pass the DM conversation id (`D...`), not a user id (`U...`/`W...`)
- for reaction mutations, this slice intentionally supports only message targets via `channel + ts`; deprecated file/file_comment reaction paths are not exposed
- `thread_ts` should point to the parent/root message; `reply_broadcast=true` makes the thread reply visible in-channel
- `update_message(...)` currently exposes text edits only; blocks, attachments, metadata, and file_ids are not surfaced yet
- Slack only allows update/delete for messages the authenticated actor is allowed to modify; `chat.delete` cannot remove impersonated messages
- reaction `name` is normalized for convenience, so both `thumbsup` and `:thumbsup:` work; skin-tone suffixes like `thumbsup::skin-tone-6` are passed through

## Attachment Flow

The working contract is:

1. Materialize inputs with `slack-mgmt attachment materialize` or the underlying `agents-attachments` helper.
2. Inspect available files with `slack-mgmt q 'attachments() { overview }'`.
3. Stage one file into the task-specific destination with `slack-mgmt attachment stage ...`.
4. Let the agent process or transform the staged file and persist outputs where the workflow needs them.

`attachment stage` accepts either:

- a manifest reference such as attachment id or name
- an already local file path

`--destination` can be:

- a full file path
- a directory path ending with `/`

## Development Tools

| Tool | Purpose | Command | Outputs |
| --- | --- | --- | --- |
| Go | Build, test, format, and vet the CLI | `go test ./...`; `go fmt ./...`; `go vet ./...` | Test/build output on stdout; temporary artifacts in `.temp/` |
| `agents-attachments` (optional) | Bootstrap a runtime attachment manifest for `slack-mgmt attachment materialize` | `./setup.sh --with-attachments`; `slack-mgmt attachment materialize` | Materialized inputs and manifest under `.temp/` |
| `mac-chrome-session` | Discover sanitized exact Chrome targets and execute sealed, origin/workspace-guarded Slack reads | `mac-chrome-session version`; `mac-chrome-session list`; `mac-chrome-session slack-read --help` | Bounded method responses only; browser credentials remain inside Chrome |
| Slackdump v4 (optional) | Supply externally authorized read-only channel/user data through the Slackdump provider | `slackdump version`; `slackdump workspace new NAME`; `slack-mgmt workspace diagnose --workspace NAME` | Slackdump-owned auth/cache plus bounded, redacted `slack-mgmt` results; no credentials copied |
| Setup wrappers | Build, install, de-gitize, and verify the CLI plus shared skill | `./setup.sh`; on Windows: `.\setup.ps1` | CLI in the user bin directory, shared skill links, Apache license, and user-scoped `install.json` |

Project-local agent instructions, task-board state, and rendered runtime files
are intentionally ignored and are not part of the published package.

## Development

Useful commands:

- `go test ./...`
- `go fmt ./... && go vet ./...`
- `go run ./cmd/slack-mgmt version`
- `go run ./cmd/slack-mgmt q 'schema()' --format compact`
- `go run ./cmd/slack-mgmt q 'provider_capabilities()' --format json`
- `go run ./cmd/slack-mgmt m 'schema()' --format compact`
- `go run ./cmd/slack-mgmt auth whoami --source env_or_file --workspace acme`
- `go run ./cmd/slack-mgmt workspace set --workspace acme --read-transport api`
- `go run ./cmd/slack-mgmt workspace set --workspace acme --read-transport browser --browser-url https://app.slack.com/client/T01234567`
- `go run ./cmd/slack-mgmt workspace show --workspace acme`
- `go run ./cmd/slack-mgmt workspace diagnose --workspace acme`
- `go run ./cmd/slack-mgmt q 'auth_test() { default }' --workspace acme --format compact`
- `go run ./cmd/slack-mgmt q 'conversations(limit=20) { overview }' --workspace acme --format compact`
- `go run ./cmd/slack-mgmt q 'conversation(general) { full }' --workspace acme --format compact`
- `go run ./cmd/slack-mgmt q 'history(general, limit=20) { overview }' --workspace acme --format compact`
- `go run ./cmd/slack-mgmt q 'replies(general, ts="1710000000.000100") { full }' --workspace acme --format compact`
- `go run ./cmd/slack-mgmt q 'search_messages("error in:#alerts", count=20, page=1) { overview }' --workspace acme --format compact`
- `go run ./cmd/slack-mgmt q 'search_info() { default }' --workspace acme --format compact`
- `go run ./cmd/slack-mgmt q 'search_context("What is project gizmo?", content_types="messages,files", include_context_messages=true) { overview }' --workspace acme --format compact`
- `go test ./internal/mutate ./internal/slack`: validate write paths only through local fakes.
- `go run ./cmd/slack-mgmt q 'users(limit=50) { overview }' --workspace acme --format compact`
- `go run ./cmd/slack-mgmt grep 'attachment' --format compact`

## Cross-Platform Setup

The root wrappers delegate to the canonical installers while preserving all
arguments and exit status:

- macOS: `./setup.sh`
- Windows PowerShell: `.\setup.ps1`

Both flows build the CLI, install the shared skill at
`~/.agents/skills/slack-management`, refresh the Claude/Codex links, write a
secret-free user-scoped install record, and verify `version`, query/provider
capability and mutation schemas, and `auth config-path`. Windows installs both
`slack-mgmt.exe` and a UTF-8-no-BOM extensionless `slack-mgmt` shim that forwards
all arguments to the adjacent executable; its bin directory is added to the
current process and user `PATH`.

Declared release builds are checked with `GOOS/GOARCH` for `darwin/amd64`,
`darwin/arm64`, `windows/amd64`, and `windows/arm64`. Put cross-build outputs
under `.temp/`; no generated binary belongs in git.

## License

Licensed under the [Apache License 2.0](LICENSE). Copyright 2026 Alexey
Grigorev.
