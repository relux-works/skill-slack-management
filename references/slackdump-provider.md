# Slackdump Read Provider

Slackdump is a distinct, read-only provider. It is not a Web API transport and
never gains access to `slack-mgmt` API credentials or mutation construction.

## Setup

Install an official Slackdump v4 release so `slackdump` is on `PATH`, or pass a
single executable path. Authorize and select the workspace with Slackdump's own
commands before enabling it in `slack-mgmt`; never copy Slackdump session values
into `slack-mgmt` configuration.

```bash
slackdump workspace new acme

slack-mgmt workspace set \
  --workspace acme \
  --provider slackdump \
  --slackdump-executable slackdump \
  --slackdump-workspace acme \
  --slackdump-authorize-external

slack-mgmt workspace diagnose --workspace acme
slack-mgmt q 'provider_capabilities()' --workspace acme --format json
slack-mgmt q 'conversations(limit=50) { overview }' --workspace acme --format compact
slack-mgmt q 'users(limit=50) { overview }' --workspace acme --format compact
```

`--slackdump-authorize-external` records only the explicit
`external_opt_in` mode. It is not proof of authentication and contains no
credential. `workspace diagnose` probes the executable/version but does not
perform a live Slack read.

## Supported Contract

Adapter version 1 supports `conversations`, `conversation`, and `users` using
Slackdump v4 JSON list output. Other operations return
`provider_capability_unsupported` before Slackdump is executed. Pagination is
normalized as bounded opaque `offset:N` cursors over one cached provider result.

Production execution resolves one absolute executable and invokes it directly
with argv. It never uses a shell or accepts a config-supplied command string.
The adapter checks the compatible range `>=4.0.0,<5.0.0`, bounds probe/read
deadlines, stdout/stderr bytes, and parsed record counts, rejects malformed or
trailing JSON, and runs normalized results and diagnostic text through the
existing redactor.

Adapter version 1 accepts no archive input/output path at all: it uses
`-no-json` and ingests bounded stdout. This is the strictest path bound. A future
archive-backed capability must define an allowed root, reject symlinks and
escapes, and test the production open path before that capability is declared.

## Security and Enterprise Risk

Slackdump live access relies on unsupported browser-session credentials owned
by Slackdump. Slack can change or revoke that mechanism. Use may trigger Slack
security alerts, notify workspace administrators, or hit scraping protection,
especially in Enterprise workspaces with large channel/user directories.

Get organizational approval before live use. Prefer narrow, infrequent reads;
do not use `-enterprise` or other undocumented acceleration flags through this
adapter. `slack-mgmt` never prints, copies, stores, or migrates Slackdump session
credentials.

Official upstream references:

- <https://github.com/rusq/slackdump>
- <https://github.com/rusq/slackdump/blob/master/doc/usage-list.md>
- <https://github.com/rusq/slackdump/blob/master/doc/enterprise.md>
