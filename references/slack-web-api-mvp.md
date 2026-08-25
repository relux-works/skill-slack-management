# Slack Web API MVP

The normative security, authentication, setup, output, write-safety, and
rate-limit contract is
[`../.spec/slack-security-auth-api.md`](../.spec/slack-security-auth-api.md).
This reference summarizes the supported method surface and separates current
implementation from the required secure-delivery state.

## Method Surface

The live Slack surface is intentionally small:

- `auth.test`
- `conversations.list`
- `conversations.info`
- `conversations.history`
- `conversations.replies`
- `search.messages`
- `assistant.search.info`
- `assistant.search.context`
- `chat.postMessage`
- `chat.update`
- `chat.delete`
- `reactions.add`
- `reactions.remove`
- `users.list`

Required assumptions:

- authenticate with a bearer token in the `Authorization` header
- recommend bot tokens for app-identity operations and user tokens only for
  intentional user semantics
- keep `auto` OS-secure-store-only on macOS and Windows
- use canonical `auto`, `keychain`, `env`, and explicit lower-security `file`
  sources; retain `env_or_file` only as a deprecated explicit compatibility
  selector with `SLACK_ACCESS_TOKEN` before `SLACK_BOT_TOKEN` before the
  workspace file
- send resolved credentials only to `https://slack.com/api` over HTTPS
- keep reads compact and agent-facing through the `q` DSL
- sanitize all JSON, compact, grep, auth, attachment-metadata, mutation, and
  stderr output through one deterministic boundary
- use a typed, bounded, context-cancellable HTTP 429 policy

Current CLI mapping:

- `slack-mgmt auth whoami` performs local inspection plus optional live `auth.test`
- `slack-mgmt q 'auth_test() { ... }'`
- `slack-mgmt q 'conversations(...) { ... }'`
- `slack-mgmt q 'conversation(general) { ... }'`
- `slack-mgmt q 'history(general, limit=20) { ... }'`
- `slack-mgmt q 'replies(general, ts="1710000000.000100") { ... }'`
- `slack-mgmt q 'search_messages("error in:#alerts", count=20) { ... }'`
- `slack-mgmt q 'search_info() { ... }'`
- `slack-mgmt q 'search_context("What is project gizmo?", content_types="messages,files") { ... }'`
- `slack-mgmt m 'post_message(general, text="hello world")'`
- `slack-mgmt m 'update_message(C123, ts="1710000005.000600", text="edited text")'`
- `slack-mgmt m 'delete_message(C123, ts="1710000005.000600")'`
- `slack-mgmt m 'add_reaction(C123, ts="1710000005.000600", name="thumbsup")'`
- `slack-mgmt m 'remove_reaction(general, ts="1710000005.000600", name=":wave:")'`
- `slack-mgmt q 'users(...) { ... }'`

## Read Transport Selection

The query engine consumes one common typed read transport. Workspace profiles
select `api` or `browser`; a missing profile means `api` for compatibility, but
a malformed or unreadable workspace config is a hard error and never becomes
an API fallback.

API mode resolves the access token through the existing secure-store boundary.
Browser profiles validate and retain a canonical
`https://app.slack.com/client/<workspace-id>` target. On macOS they discover a
matching exact Chrome window/tab from sanitized metadata, background-open a
missing target, then send a versioned allowlisted request to the sealed
`mac-chrome-session slack-read` path. That compiled path atomically verifies
the origin and workspace inside every browser start and poll evaluation;
page-owned authorization never leaves Chrome. Generic `run-js`, active-tab
fallbacks, tokenless fetches, and credential export are not accepted browser
credential mechanisms. See [`browser-transport.md`](browser-transport.md) for
setup, deterministic target selection, and failure behavior.

Mutation construction has a separate API-only factory. A browser workspace is
refused as `browser_write_unsupported` before credential resolution or any
network/browser call. Unsupported operating systems return
`browser_transport_unsupported_platform`; there is no transport fallback.
Detailed setup and error behavior are in
[`browser-transport.md`](browser-transport.md).

## Query Contract

Current query notes and required delivery boundaries:

- `conversations(types="public_channel,private_channel")` should quote the comma-separated types string
- `conversation(...)` and `history(...)` accept either a direct conversation id or a human-readable name such as `general` or `#general`
- `history(...)` returns message rows plus pagination metadata with the resolved `conversation_id`
- `replies(...)` requires `ts` for the parent/root message and returns thread rows plus `thread_ts` in page metadata
- `search_messages(...)` maps to the legacy `search.messages` endpoint, not the newer assistant search APIs
- `search.messages` requires a user token with `search:read`; bot tokens are not sufficient
- `search_info()` reports whether AI search is enabled on the current team
- `search_context(...)` maps to `assistant.search.context`, the newer Real-time Search API
- `assistant.search.context` uses granular scopes such as `search:read.public`, `search:read.private`, `search:read.im`, `search:read.mpim`, `search:read.files`, and `search:read.users`
- bot-token calls to `search_context(...)` require an `action_token`; user-token calls do not
- `post_message(...)`, `update_message(...)`, and `delete_message(...)` map to `chat.postMessage`, `chat.update`, and `chat.delete`
- `add_reaction(...)` and `remove_reaction(...)` map to `reactions.add` and `reactions.remove`
- message mutations need `chat:write`; `chat:write.public` is additionally required for posting to public channels the app has not joined
- reaction mutations need `reactions:write`
- prefer channel IDs over channel names even though `post_message(...)` resolves names through `conversations.list`
- `update_message(...)` and `delete_message(...)` also accept named channels as a convenience, but direct-message edits/deletes need a DM id (`D...`) rather than a user id
- `add_reaction(...)` and `remove_reaction(...)` also accept named channels as a convenience, but this slice only supports message reactions via `channel` and `timestamp`, matching Slack's preferred path after file/file_comment deprecation
- replies are still the same mutation: pass `thread_ts` for the parent message and optionally `reply_broadcast=true`
- `update_message(...)` currently exposes only text edits even though Slack also supports blocks, attachments, metadata, and file ids
- Slack only lets the authenticated actor update/delete messages they are allowed to modify, and `chat.delete` cannot remove impersonated messages
- reaction names accept either raw emoji identifiers like `thumbsup` or convenience wrappers like `:thumbsup:`; skin-tone suffixes such as `thumbsup::skin-tone-6` are preserved
- `users.list` email requires `users:read.email` in addition to `users:read`
- required secure presets omit email from `default` and `overview`; explicit
  email projection is still deterministically redacted
- both list methods expose cursor pagination through `page.next_cursor`
- `history(...)` surfaces both cursor pagination and Slack's `has_more` / `latest` hints in `page`
- `search_messages(...)` exposes Slack paging metadata and marks the result page as `legacy=true`
- `search_context(...)` exposes mixed result rows via `content_type` and reports result-family counts in `page`

All query statements must pass operation, argument, type, enum, and field
validation before the first network request. JSON and compact output carry the
same semantic fields. Empty results are not substituted for malformed, partial,
or failed reads. Errors use stable safe codes and never contain credentials,
headers, request bodies, or raw Slack response bodies.

Attachment paths are sensitive metadata. Before attachment list, lookup, or
stage output, the CLI must copy the source byte-for-byte to a generated,
user-only relative alias under `.temp/slack-mgmt/attachment-aliases/` and return
that existing usable alias as `local_path`. Alias publication is atomic and
no-replace: a collision preserves the existing file and retries with a new
random name, while copy/sync/chmod/rename failure leaves no final or temporary
alias. Multi-item publication rolls back every alias newly created by a failed
invocation, and any symlinked alias-root component is refused before alias bytes
can escape the invocation working directory. It never renders or persists the incoming manifest path, requested stage
destination, or an alias derived from either value. Alias publication fails
closed as `attachment_alias_error`; binary content remains unmodified and is
never falsely described as sanitized.

For `attachment stage`, the alias supplements rather than replaces the workflow
copy. The real CLI entry point must still invoke `attachments.Stage`: success
creates or, only with `--force`, replaces the requested destination with
source-identical bytes, then returns a distinct private alias. Without
`--force`, an existing destination is refused and left unchanged. The raw
destination stays out of output in every case.

## Auth and Scope Rules

The concrete source, storage, inspection, and setup behavior is documented in
[`auth-config.md`](auth-config.md).

- `auth.test` requires no scope and proves identity only; it does not authorize
  another operation.
- Live auth inspection may report sorted granted and accepted scope headers,
  token-family metadata, and presence booleans, but never a token or action
  token.
- Bot/user access tokens are the supported Web API credential families.
  Workflow, app-level, configuration, and service tokens are not general
  credentials for this facade. Rotating access tokens may be used, but refresh
  and renewal are not implemented.
- `search_messages(...)` is a legacy compatibility surface requiring a user
  token with `search:read`.
- Bot calls to `search_context(...)` require the event `action_token`; user-token
  calls do not.
- Every method must still handle `missing_scope`; token prefixes and
  `auth.test` are not authorization attestations.

Official sources: [tokens](https://docs.slack.dev/authentication/tokens/),
[`auth.test`](https://docs.slack.dev/reference/methods/auth.test/),
[`users:read.email`](https://docs.slack.dev/reference/scopes/users.read.email/),
[`search:read`](https://docs.slack.dev/reference/scopes/search.read/), and
[`assistant.search.context`](https://docs.slack.dev/reference/methods/assistant.search.context/).

## Mutation Safety

- The supported mutation catalog remains `post_message`, `update_message`,
  `delete_message`, `add_reaction`, and `remove_reaction`.
- Required secure delivery accepts one mutation statement per `m` invocation.
- `m --dry-run` validates and renders a sanitized plan with zero credential
  resolution, client creation, or HTTP requests.
- `delete_message` and `remove_reaction` require `--confirm` before any
  credential or network work.
- `reply_broadcast=true` requires `thread_ts`, and `thread_ts` identifies the
  parent/root message.
- Update/delete/reaction calls require the actual conversation ID after local
  resolution. A User ID is accepted only by `post_message` for Slack's direct
  message behavior.
- Automated tests, setup smoke, CI, reviewer probes, and agent validation must
  refuse live Slack mutation origins. Mutation evidence comes only from injected
  fakes, synthetic tokens, or dry-run. This evidence prohibition does not remove
  the explicit production mutation feature for an authorized operator.

Official sources: [`chat.postMessage`](https://docs.slack.dev/reference/methods/chat.postMessage/)
and [security best practices](https://docs.slack.dev/concepts/security/).

## Rate Limits

Slack returns HTTP 429 plus integer-seconds `Retry-After`. Required delivery
detects 429 before JSON decoding, rejects missing/malformed retry metadata,
allows at most three total read attempts and two total write attempts, and never
retries ambiguous write transport/5xx/decode failures. Each request waits in
isolation, accepts at most 60 seconds for each integer `Retry-After` wait (at
most 120 seconds total retry waiting for a read and 60 seconds for a write), and
is cancelled by its context or CLI interrupt/termination signal. There is no global or
method/workspace retry lock.

Official source: [Web API rate limits](https://docs.slack.dev/apis/web-api/rate-limits/).

## Implemented Safety Boundary

The CLI now applies the shared final-output sanitizer across JSON, compact,
grep, auth, mutation, attachment metadata, and stderr; publishes opaque private
attachment aliases; omits email from the `users` overview preset; validates one
mutation locally before provider creation; exposes zero-request `m --dry-run`;
requires `--confirm` for delete/remove execution at the engine boundary; and
implements typed, bounded, signal-cancellable HTTP 429 handling. Delivery tests actively refuse a live
Slack mutation origin and use only local fakes or dry-run.

The accepted source analysis is
[`../.research/260824_official-slack-and-zendesk-contracts.md`](../.research/260824_official-slack-and-zendesk-contracts.md).
