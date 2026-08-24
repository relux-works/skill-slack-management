---
name: slack-management
description: Use slack-mgmt for secure Slack workspace reads, cross-thread search, message and reaction mutations, workspace-scoped API/browser/Slackdump provider routing, attachment staging, and deterministic redaction of emails, phone numbers, tokens, passwords, and other sensitive CLI output.
---

# Slack Management

Use this repo when the user needs to:

- bootstrap or refresh `slack-mgmt`
- authorize a workspace-scoped Slack API token without exposing it in argv or output
- search messages and context across channels and threads
- inspect conversations, histories, replies, users, provider capabilities, and auth state
- post or update messages, reply in threads, or manage message reactions through the guarded mutation surface
- route reads per workspace through Slack Web API, a silent authenticated Chrome tab, or an explicitly authorized Slackdump provider
- materialize incoming attachments into local readable files
- stage those files into workflow-specific destinations for downstream agent processing
- inspect the local attachment manifest through the same query surface

## Working Surface

Current CLI surface:

- `auth` for token bootstrap and live auth inspection
- `workspace` for per-workspace provider selection, diagnostics, and Web API `api`/silent Chrome `browser` transport selection
- `q` for projected, batched structured reads over Slack and the local attachment manifest
- `grep` for scoped local text discovery
- `attachment materialize` for runtime bootstrap via `agents-attachments`
- `attachment stage` for copying a resolved input into the destination the workflow needs
- live Slack reads for `auth.test`, `conversations.list`, `conversations.info`, `conversations.history`, `conversations.replies`, `search.messages`, `assistant.search.info`, `assistant.search.context`, and `users.list`
- `m` for narrow write mutations, currently covering `post_message(...)`, `update_message(...)`, `delete_message(...)`, `add_reaction(...)`, and `remove_reaction(...)`

Prefer the installed binary:

```bash
slack-mgmt version
```

If you are working from the source repo and need to refresh the installed copy:

```bash
./setup.sh
```

On Windows use `./setup.ps1`. Optional integrations are never assumed:
`--with-attachments`, `--with-browser`, and `--with-slackdump` on macOS;
`-WithAttachments` and `-WithSlackdump` on Windows. Browser transport is
macOS-only. Read `README.md` before enabling an external provider.

## Auth Bootstrap

Set a workspace-scoped token:

```bash
printf '%s\n' "$SLACK_ACCESS_TOKEN" |
  slack-mgmt auth set-access --workspace acme --token-stdin
```

Inspect auth state:

```bash
slack-mgmt auth whoami --workspace acme
slack-mgmt auth resolve --workspace acme
```

Clear stored access:

```bash
slack-mgmt auth clear-access --workspace acme
```

Storage behavior:

- on macOS and Windows, `auto` defaults to the native secure store through
  `keychain` and fails closed instead of creating plaintext credentials
- on other platforms, `auto` reads only the environment
- explicit `env_or_file` compatibility mode reads `SLACK_ACCESS_TOKEN`, then
  `SLACK_BOT_TOKEN`, then the selected workspace profile under
  `os.UserConfigDir()/slack-mgmt/auth.json`
- prefer `--token-stdin` when storing access so the token does not enter shell
  history

## Sensitive Output Boundary

Every stdout/stderr path exposed by the CLI passes through the shared
redactor, including JSON, compact output, grep, auth inspection, mutations,
attachments, and errors. Tokens, bearer values, labeled passwords/secrets/API
keys, email addresses, supported phone forms, and sensitive URL parameters are
replaced with stable private markers. Slack IDs and message timestamps remain
usable for follow-up queries. Never bypass this boundary with ad hoc HTTP,
provider stdout, debug prints, raw fixture capture, or direct browser storage
inspection.

The redactor protects CLI egress, not arbitrary staged binary file contents.
Treat staged attachments as sensitive inputs until separately inspected and
sanitized.

## Workspace Read Transport

API mode is the backwards-compatible default and resolves the workspace token
through the existing secure-store boundary:

```bash
slack-mgmt workspace set --workspace acme --read-transport api
```

Browser profiles use the authenticated exact workspace tab in macOS Chrome:

```bash
slack-mgmt workspace set --workspace acme --read-transport browser \
  --browser-url https://app.slack.com/client/T01234567
```

Install a compatible `mac-infra` build, manually enable Chrome's
`View -> Developer -> Allow JavaScript from Apple Events` setting, and sign in
to the configured workspace tab. `slack-mgmt` discovers only matching
sanitized tab metadata, opens a missing workspace with `/usr/bin/open -g`, and
uses the sealed `mac-chrome-session slack-read` path with pinned exact tab,
origin, and workspace guards. Browser credentials remain inside Chrome and all
returned values still cross the CLI redaction boundary. Never replace this
with generic `run-js`, tokenless fetches, credential export, focus, tab
selection, or UI scripting. Writes remain API-only and fail closed. See
`references/browser-transport.md` for setup and platform errors.

Slackdump is a separate provider with tool-owned authorization:

```bash
slack-mgmt workspace set --workspace acme --provider slackdump \
  --slackdump-executable slackdump --slackdump-workspace acme \
  --slackdump-authorize-external
slack-mgmt workspace diagnose --workspace acme
slack-mgmt q 'provider_capabilities()' --workspace acme --format json
```

Do not copy Slackdump session credentials into `slack-mgmt`. Live Slackdump
access uses unsupported browser-session credentials and may trigger Enterprise
security alerts. Read `references/slackdump-provider.md` before enabling it.

## Typical Attachment Flow

1. Materialize chat or ticket attachments:

```bash
slack-mgmt attachment materialize --format compact
```

2. Inspect what the runtime made available:

```bash
slack-mgmt q 'attachments() { overview }' --format compact
slack-mgmt q 'attachment(att-1) { full }' --format json
```

3. Stage one file into the workflow destination:

```bash
slack-mgmt attachment stage att-1 --destination .temp/artifacts/ --format json
slack-mgmt attachment stage ./local-input.png --destination .temp/artifacts/processed-input.png --force
```

Important contract:

- incoming attachments must become local readable files
- agents read `local_path` as the source of truth
- agents may place derived outputs wherever the task needs them
- board resources are only one possible destination, not the only one

## Query Surface

Discover the current DSL with:

```bash
slack-mgmt q 'schema()' --format json
slack-mgmt m 'schema()' --format json
```

Current operations:

- `schema()`
- `provider_capabilities()`
- `attachments()`
- `attachment(ref)`
- `auth_test()`
- `conversations(...)`
- `conversation(...)`
- `history(...)`
- `replies(...)`
- `search_messages(...)`
- `search_info()`
- `search_context(...)`
- `users(...)`

Current mutations:

- `post_message(...)`
- `update_message(...)`
- `delete_message(...)`
- `add_reaction(...)`
- `remove_reaction(...)`

Mutation commands accept one statement only. Use `--dry-run` first to obtain a
sanitized plan without resolving credentials or creating a Slack client.
`delete_message(...)` and `remove_reaction(...)` require `--confirm` for real
execution. Automated tests and agent validation remain fake-only or dry-run-only;
never use a live Slack mutation as delivery evidence.

The query layer has two surfaces in one CLI:

- local attachment/runtime inspection
- live Slack read operations

Keep extending the live surface through the same `agent-facing-api` shape instead of bolting on a second interface.

## References

Read these files as needed:

- `README.md` for the current user-facing CLI surface
- `references/attachment-flow.md` for the local attachment processing contract
- `references/auth-config.md` for secure-store behavior and token-source precedence
- `references/slack-web-api-mvp.md` for the current Slack auth/read contract
- `references/browser-transport.md` for per-workspace API/browser routing and Chrome setup
- `references/providers.md` for the normalized provider protocol and extension rules
- `references/slackdump-provider.md` for Slackdump setup, bounds, authorization ownership, and Enterprise risk

## Implementation Rules

- Keep the CLI compatible with the `agent-facing-api` pattern.
- Keep attachment flows local-file-first.
- Use `agents-attachments` as the bridge when runtime materialization is needed.
- Prefer table-driven tests and closed-loop local verification.
- Treat `search_messages(...)` as a legacy compatibility surface and document its token/scope constraints.
- Treat `search_context(...)` as the preferred search path for assistant-facing workflows and document `action_token` expectations for bot tokens.
- Keep the hardened `m` safety boundary intact: one statement, local validation before provider creation, zero-request dry-run, and confirmation for destructive paths.
- Add new Slack mutations only through the existing one-statement validation,
  dry-run, confirmation, origin, retry, and redaction boundary.
