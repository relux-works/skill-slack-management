# Free Human-Facing Slack CLI Research

Research snapshot: 2026-08-25. Board task: `TASK-260825-3m0fi6`.

## Context and method

This report compares free/open-source and official-free tools that a support
engineer can operate directly to find incidents, conversations, references,
files, and cross-thread context. Paid-only, trial-only, and agent-first tools
without a useful human-operated surface are excluded.

Popularity is ordered by public GitHub stars returned by the GitHub REST API on
the snapshot date. Stars measure historical reach, not maintenance, security,
or support-workflow fit. Repository `pushed_at` and the newest stable GitHub
release are shown separately. Task-scoped board outcomes retain normalized
field projections from the GitHub repository and release responses; they are
not represented as exact API response envelopes. Equal star counts are tied
and then displayed by most recent push only for deterministic ordering.

Capability claims come from each project's repository and Slack constraints
come from Slack's official documentation. `Unknown` means the primary source
does not establish the property; it is not inferred from adjacent behavior.

## Screened-candidate ledger

The dated repository projection contains twelve entries. This ledger makes the
screening decision reproducible instead of silently dropping candidates.

| Repository | Screen result | Primary-source reason |
| --- | --- | --- |
| [jpbruinsslot/slack-term](https://github.com/jpbruinsslot/slack-term) | Included | Human-operated live TUI and the historical-popularity baseline. |
| [rusq/slackdump](https://github.com/rusq/slackdump) | Included | Human archive/export/view workflow, with unsupported session credentials called out separately. |
| [wee-slack/wee-slack](https://github.com/wee-slack/wee-slack) | Included | Human-operated WeeChat client with history and threads. |
| [hfaran/slack-export-viewer](https://github.com/hfaran/slack-export-viewer) | Included | Human browser over an authorized Slack export. |
| [slackapi/slack-cli](https://github.com/slackapi/slack-cli) | Included | Official free CLI can invoke Slack Web API methods directly. |
| [openclaw/slacrawl](https://github.com/openclaw/slacrawl) | Included | Human TUI/query/report surface over API, export, or local cache ingestion. |
| [urugus/slack-cli](https://github.com/urugus/slack-cli) | Included | Human commands cover native search, threads, files, and JSON. |
| [MasonLiebe/slackatui](https://github.com/MasonLiebe/slackatui) | Included | Human-operated live TUI with threads and files. |
| [open-cli-collective/slack-chat-api](https://github.com/open-cli-collective/slack-chat-api) | Included | Human commands cover native search, threads, and files. |
| [kurenn/slack-tui](https://github.com/kurenn/slack-tui) | Included | Human keyboard-first TUI documents workspace search, threads, file links, releases, and app scopes. |
| [ainoya/slack-cli](https://github.com/ainoya/slack-cli) | Included | Human search/history CLI is free and source-available; its narrow capability and credential risks are compared rather than hidden. |
| [osodevops/slack-cli](https://github.com/osodevops/slack-cli) | Excluded from ranking and shortlist | The README explicitly says it is purpose-built for AI agents. Its manually callable surface is acknowledged, but the task excludes agent-oriented workflows; desktop/browser session extraction and `stealth`-paced export also cross the recommended corporate auth boundary. |

## Popularity ranking and maintenance health

| Rank | Tool | Stars | Last push | Latest stable release | Maintenance assessment |
| ---: | --- | ---: | --- | --- | --- |
| 1 | [slack-term](https://github.com/jpbruinsslot/slack-term) | 6,611 | 2024-04-23 | `v0.5.0`, 2020-03-14 | Historically popular but stale as a released product. |
| 2 | [Slackdump](https://github.com/rusq/slackdump) | 2,759 | 2026-08-19 | `v4.4.4`, 2026-08-19 | Active releases and development. |
| 3 | [wee-slack](https://github.com/wee-slack/wee-slack) | 2,615 | 2026-08-17 | `v2.11.0`, 2024-10-09 | Active repository, but no stable release in nearly two years. |
| 4 | [Slack Export Viewer](https://github.com/hfaran/slack-export-viewer) | 1,128 | 2026-08-23 | `4.0.0`, 2026-06-01 | Active releases and development. |
| 5 | [Official Slack CLI](https://github.com/slackapi/slack-cli) | 290 | 2026-08-24 | `v4.6.0`, 2026-07-30 | Active official project; stars understate vendor distribution. |
| 6 | [slacrawl](https://github.com/openclaw/slacrawl) | 228 | 2026-08-23 | `v0.8.5`, 2026-08-14 | Active but young; operational and security policy are still material evaluation risks. |
| 7 | [urugus/slack-cli](https://github.com/urugus/slack-cli) | 33 | 2026-08-22 | `v0.27.1`, 2026-07-17 | Active but young and lightly adopted. |
| 8 | [slackatui](https://github.com/MasonLiebe/slackatui) | 23 | 2026-03-13 | None | Small source-only project with no tagged release. |
| 9 | [slck / slack-chat-api](https://github.com/open-cli-collective/slack-chat-api) | 21 | 2026-07-30 | `v3.2.68`, 2026-07-19 | Active releases, very small community. |
| 10= | [slack-tui](https://github.com/kurenn/slack-tui) | 0 | 2026-08-20 | `v0.6.1`, 2026-08-20 | Actively released but brand new, with no adoption signal yet. |
| 10= | [ainoya/slack-cli](https://github.com/ainoya/slack-cli) | 0 | 2026-03-01 | `v0.0.13`, 2025-12-08 | Released through `v0.0.13`, but has no push for nearly six months and no adoption signal; current maintenance health is uncertain. |

All eleven ranked repositories declare an OSI license and are not archived.
For `ainoya/slack-cli`, MIT is declared in its README while the GitHub API
license field is null. The ordering above is deliberately not the
recommendation order.

## Verified capability and access matrix

| Tool | Query model | Authentication and Slack scopes | Search / threads / files | Filtering, output, and analysis | Platforms and license |
| --- | --- | --- | --- | --- | --- |
| [slack-term](https://github.com/jpbruinsslot/slack-term) | Live Slack TUI | Token in config, CLI flag, or environment; its wiki uses legacy `client` OAuth and also documents unsupported token extraction. Installation can require admin approval. | In-pane text find and a thread sidebar; workspace-wide message search, file retrieval, and export are not documented. | Interactive navigation only; no structured output or cross-thread analysis. | Legacy binary/source installation; current release coverage is too old to rely on. MIT. |
| [Slackdump](https://github.com/rusq/slackdump) | Live scrape into an archive, then local query/view | Uses Slack web-session `xoxc`/`xoxd` credentials rather than an OAuth app, so normal app scopes do not apply. The project warns that enterprise security alerts may fire. | Dumps messages, complete threads, files, users, and emojis; archive search covers messages and files; supports incremental refresh. | SQLite or JSON+gzip archive, Standard/Mattermost export, viewer, HTML conversion, merge/dedupe tools. Strong offline analysis. | Homebrew on macOS and release executables for Windows/macOS/other OSes. AGPL-3.0. |
| [wee-slack](https://github.com/wee-slack/wee-slack) | Live Slack client in WeeChat | OAuth is supported, but the README does not enumerate a minimal scope set. It says restricted workspaces need app approval and the Free plan consumes an app slot. The alternative session token/cookie is unsupported. Tokens are plaintext unless the operator manually uses WeeChat secure storage. | Channel history, threads, uploads, and interactive chat; no documented workspace-wide server search or file-download investigation command. | WeeChat buffers, local logs, and client commands; no stable structured result format. | Python plugin wherever WeeChat runs; practical support is strongest on Unix/WSL. MIT. |
| [Slack Export Viewer](https://github.com/hfaran/slack-export-viewer) | Authorized Slack export only | No Slack token or scopes. The operator needs an export ZIP/directory produced by an authorized owner/admin. | Browses exported channels and DMs, annotates thread replies, and renders file links/data present in the export. No documented full-text search. | Web/static-HTML viewer; CLI filters date, included/hidden channels, DMs, and user attributes. No JSON query output. | `pipx`/`pip` Python package; cross-platform runtime. MIT. |
| [Official Slack CLI](https://github.com/slackapi/slack-cli) | Native Slack Web API through [`slack api`](https://docs.slack.dev/tools/slack-cli/reference/commands/slack_api/) | Explicit token, installed app bot token, `SLACK_BOT_TOKEN`, or `SLACK_USER_TOKEN`. Scopes depend on the method: message/file search requires a user token with `search:read`; history and replies require the conversation-type `*:history` scope. | Can call `search.messages`, `search.files`, `conversations.history`, `conversations.replies`, and file methods directly. | Raw API JSON composes well with `jq`, `rg`, `fzf`, or a local database; no built-in incident/thread hydration workflow. | Official macOS, Windows, and Linux builds. Apache-2.0. |
| [slacrawl](https://github.com/openclaw/slacrawl) | Slack API, authorized export, Slack Desktop cache, MCP/provider ingestion into local SQLite | Bot, optional user, and optional app tokens are read from environment variables. The docs describe token roles but do not publish one exact minimal OAuth scope set; treat scope requirements as `Unknown` until a reviewed app manifest exists. Desktop and MCP sources are enabled in the example config. | API/export/cache ingestion of history and threads; file/media caching; local FTS5 search with substring fallback. | Text/JSON/log output, TUI, read-only SQL, reports, digests, analytics, retention, and incremental refresh. Strongest local analysis. | Signed/notarized macOS and Linux archives plus Debian/RPM packages; no documented native Windows release. MIT. |
| [urugus/slack-cli](https://github.com/urugus/slack-cli) | Native Slack Web API | Bot/user OAuth token encrypted with a local AES key. The README lists `search:read` (user token only), conversation `*:history`, `files:read`, `users:read`, and many write scopes for the complete tool. | Workspace message search with Slack modifiers, channel history, complete threads, permalink lookup, and file download. | Table, simple text, and JSON; server-side sort/pagination and local shell analysis. | Node.js/npm, therefore broadly cross-platform; no OS-native credential manager. MIT. |
| [slackatui](https://github.com/MasonLiebe/slackatui) | Live Slack TUI | OAuth user/bot app. Its documented manifest requests broad read and write scopes, including `search:read`, every conversation `*:history`, `files:read`, and corresponding write scopes. Keychain on macOS or encrypted JSON. | Channel browsing, threads, file open/download/upload; `/` filters channel names only, not messages. | Interactive only; no structured output or cross-thread analysis. | Rust source build; no tagged binaries or documented native Windows delivery. MIT. |
| [slck](https://github.com/open-cli-collective/slack-chat-api) | Native Slack Web API | Bot and user OAuth tokens; user `search:read` is required for search. History/thread/file operations use the matching bot or user read scopes. Keychain, Windows Credential Manager, Secret Service, or explicit encrypted-file backend. | Message/file/all search, history, complete threads, file info/download, and users/channels. | Rich query flags (`in`, `from`, dates, scope, link/reaction/type/pin), text/table output. Resource JSON was intentionally removed, reducing pipeline ergonomics. | Homebrew, Chocolatey, Winget, APT/RPM, and macOS/Linux/Windows binaries. MIT. |
| [slack-tui](https://github.com/kurenn/slack-tui) | Live Slack TUI | Browser PKCE OAuth or pasted user token; environment variables may override user/app/bot tokens. The supplied manifest requests user `search:read`, every conversation history/read/write family, `files:read/write`, and other write scopes, plus bot history scopes and an app token for Socket Mode. Tokens are stored in a mode-`0600` JSON file. | Workspace search, in-channel find, complete thread view/inbox, and opening message links/files; safe file download/export is not documented. | Fuzzy interactive navigation only. `--dump` renders a UI frame for testing, not structured search results; no case aggregation. | Current macOS/Linux installer and Go install; no documented Windows binary. MIT. |
| [ainoya/slack-cli](https://github.com/ainoya/slack-cli) | Native Slack Web API | User OAuth token with `search:read`, `channels:history`, `channels:read`, and `users:read`; bot tokens cannot search. Config-file storage is documented, and `config get` prints the token. | Workspace message search, latest public-channel messages, and user-message search with date modifiers; no thread hydration or file operations documented. | Timestamp/relevance sorting and count limits; no structured output or local cross-thread analysis documented. | Homebrew on macOS, Zig source build, and `v0.0.13` release archives for macOS ARM64, Linux AMD64, and Windows AMD64. MIT in README. |

The official [`search.messages`](https://docs.slack.dev/reference/methods/search.messages/)
and [`search.files`](https://docs.slack.dev/reference/methods/search.files)
methods require a user token with `search:read`. Thread hydration uses
[`conversations.replies`](https://docs.slack.dev/reference/methods/conversations.replies/)
with the matching public/private/DM/group-DM history scope. A bot-only support
app therefore cannot reproduce a member's workspace-wide search merely by
adding more bot scopes.

Slack marks both classic search methods as legacy and recommends
[`assistant.search.context`](https://docs.slack.dev/reference/methods/assistant.search.context/).
That method belongs to the
[Real-time Search API](https://docs.slack.dev/apis/web-api/real-time-search-api/)
for AI-feature apps, uses granular `search:read.*` scopes, and requires an event
`action_token` for bot-token calls (not user-token calls). RTS is available only
to internal or directory-published apps. Outside the Slack client, or for
private conversation data, it requires a user token and user OAuth consent.
Slack prohibits storing or copying data retrieved through RTS, prohibits using
it for training or unrelated scraping, and limits semantic search to workspaces
whose plans include Slack AI Search; keyword search remains the fallback.
Because this task excludes agent-first workflows, the shortlist treats classic
user-token search as the general human CLI baseline and RTS as a separately
governed, transient-display option rather than an archive source.

## Slack plan and admin constraints

- Live OAuth tools install a Slack app. By default a member may install one,
  but Workspace Owners can require approval on every plan. Requests show the
  app, requester, and requested scopes; scope additions can require approval
  again. Enterprise org policy can further restrict installation to Marketplace
  apps. See [Slack app approval](https://slack.com/help/articles/222386767-Manage-app-approval-for-your-workspace).
- The Free plan permits at most ten third-party/custom apps and exposes only the
  most recent 90 days of messages and files to view/search. Data older than one
  year is deleted. A CLI or token cannot bypass those server-side limits. See
  [Free workspace limits](https://slack.com/help/articles/115002422943-Usage-limits-for-free-workspaces).
- Standard exports on every plan cover public-channel messages and file links,
  not a guaranteed local copy of every file. Free-plan exports include file
  links only for the last 90 days. Private channels and DMs require approved
  Business+ or Enterprise export capability; owner/admin/export-role authority
  is required. See [export options by plan](https://slack.com/help/articles/201658943-Export-your-workspace-data)
  and [export contents](https://slack.com/help/articles/220556107-How-to-read-Slack-data-exports).
- User tokens act with the installing member's own visibility. Bot tokens see
  only resources allowed by scopes and conversation membership. An installed
  app is admin-visible; reusing web-session cookies avoids the app review
  boundary but is unsupported, auditable as unusual access, and excluded here
  as a corporate recommendation.
- RTS public search requires an admin-approved app; private, DM, MPDM, and file
  search also requires the corresponding user consent. Its result data cannot
  be copied into SQLite/FTS, an archive, or a persisted incident case bundle.
  Semantic search must be disclosed as a Business+ or Enterprise+ capability;
  guests cannot use platform-AI apps. These constraints are independent of the
  classic search and authorized-export rules above.

## Ranked shortlist for support engineers

### 1. Official Slack CLI plus shell tools: safest supported live baseline

Use this when official provenance, reviewed OAuth, structured output, and
cross-platform delivery matter most:

```sh
slack api search.messages query='incident after:2026-08-01' | jq .
slack api conversations.replies channel=C01234567 ts=1234567890.123456 | jq .
```

It is method-oriented rather than investigation-oriented: the operator must
join hits to channels, hydrate threads, page results, download files, redact
sensitive data, and aggregate context manually.

### 2. slacrawl import-only: strongest offline analysis

For an owner/admin-provided authorized export, `slacrawl import`, FTS5, TUI,
read-only SQL, reports, and analytics provide the best cross-thread local
analysis. Use a dedicated configuration with Slack API, Desktop, MCP, sharing,
and live tailing disabled. Do not use the example defaults for corporate data.

### 3. slck: strongest ready-made live human search UX

`slck` has the best purpose-built live search/thread/file command set and the
best credential backends in the screened community tools. Evaluate it with a
dedicated least-privilege app and both bot/user read tokens. The binary contains
write commands and its default manifest includes write scopes, so do not deploy
that manifest unchanged for support-only use. Lack of resource JSON is a real
analysis limitation.

### 4. urugus/slack-cli: strongest live JSON-capable community alternative

It combines Slack-native search, complete threads, file download, and JSON
output. Its small community and local-key credential encryption are weaker than
`slck`'s maintenance/security posture. Build a read-only app instead of granting
the README's complete write-capable scope list.

### 5. Slack Export Viewer: simplest authorized-export browser

Use it when the need is visual browsing with date/channel/user filters rather
than full-text or structured analysis. It is not a live Slack CLI and does not
turn exported file links into guaranteed offline file content.

## Security exclusions

- Do not use Slackdump's live login, wee-slack session-token mode, slack-term
  extraction recipes, `osodevops/slack-cli` desktop/browser extraction, or any
  desktop-cookie/session extraction for corporate access. Slackdump remains
  useful for viewing an already authorized archive.
- Do not treat `osodevops/slack-cli`'s `stealth` pacing as a compliance control.
  Random delays do not convert unsupported session credentials or bulk scraping
  into approved access, and its agent-first workflow is outside this task.
- Do not enable slacrawl Desktop/MCP ingestion, git sharing, or live tailing for
  an import-only review. The defaults cross visibility and credential
  boundaries that require a separate security decision.
- Do not deploy a support-only CLI with write scopes merely because its sample
  manifest includes them. Use a dedicated read-only app, separate bot/user
  tokens, and explicit admin review.
- Do not publish archives or structured search output outside the workspace's
  approved retention, privacy, legal, and access-control boundary.

## Remaining `slack-mgmt` product gap

The repository already provides live `search.messages`, history, replies,
users, compact/JSON output, secure-store-aware auth, and deterministic output
redaction. The viable source lanes are deliberately separate:

- Classic user-token search can drive live human investigation, subject to its
  legacy status, member visibility, app approval, and Slack retention limits.
- RTS can drive only transient, user-initiated result display and context
  lookup; its data-use policy rules out persistent local FTS, archives, and case
  bundles.
- Authorized exports are the supported input for persistent offline FTS and
  archive analysis, subject to plan, role, privacy, and file-link limitations.

The remaining human-support gap is therefore narrower and concrete:

1. Native `search.files`, file metadata, and safe attachment download.
2. One human-facing classic-search command that pages hits, hydrates each
   complete thread, resolves users/channels/permalinks, and emits a
   chronological case bundle instead of requiring manual method joins. RTS
   results must never enter this persistent path.
3. Stable table/text output and direct Slack modifier flags alongside the
   existing agent-oriented query DSL.
4. Authorized-export import plus local FTS/SQLite analysis when live Slack
   retention or plan limits hide required history; RTS is not an import source.
5. A least-privilege scope planner/app manifest and admin-facing installation
   checklist that separates bot history access, user search access, and writes.
6. Explicit local retention/deletion controls for downloaded files and case
   bundles, preserving the existing redaction boundary on every output path.

No screened tool combines those workflows with `slack-mgmt`'s current secure
credential and redaction contracts on both macOS and Windows. That is the
remaining justification for this repository CLI; raw Web API reach by itself
is no longer a gap now that the official CLI exposes `slack api`.

## Primary sources

- Candidate repositories linked in the ranking and capability tables.
- [Official Slack CLI repository](https://github.com/slackapi/slack-cli) and
  [`slack api` reference](https://docs.slack.dev/tools/slack-cli/reference/commands/slack_api/).
- [Slack token types](https://docs.slack.dev/authentication/tokens/).
- [Slack Real-time Search API governance](https://docs.slack.dev/apis/web-api/real-time-search-api/).
- [Slack app installation](https://slack.com/help/articles/202035138-Add-apps-to-your-Slack-workspace)
  and [app-request review](https://slack.com/help/articles/360024269514-Manage-app-requests-for-your-workspace).
- [Slack Free-plan feature limits](https://slack.com/help/articles/27204752526611-Feature-limitations-on-the-free-version-of-Slack).
- [Slack import/export guide](https://slack.com/help/articles/204897248-Guide-to-Slack-import-and-export-tools-Guide-to-Slack-import-and-export-tools).
