# Slack Browser Read Transport

Each workspace selects one read transport. `api` uses the existing secure
token resolver and Slack Web API client. `browser` delegates allowlisted reads
to the sealed `mac-chrome-session slack-read` command inside an authenticated
Slack page; it never asks Chrome for cookies, storage, authorization headers,
tokens, or opaque session state.

## API Mode

API mode remains the compatibility default when no workspace profile exists:

```bash
slack-mgmt workspace set --workspace acme --read-transport api
slack-mgmt auth set-access --workspace acme --token-stdin
slack-mgmt q 'auth_test() { default }' --workspace acme --format compact
```

Malformed or unreadable workspace configuration is a hard error. It never
falls back to API mode or another credential source.

## Browser Mode

Browser mode initially supports macOS and Google Chrome:

```bash
slack-mgmt workspace set \
  --workspace acme \
  --read-transport browser \
  --browser-url https://app.slack.com/client/T01234567

slack-mgmt q 'history(C0123456789, limit=20) { overview }' \
  --workspace acme --format compact
```

The configured URL must use HTTPS, host `app.slack.com`, contain
`/client/<workspace-id>`, and contain no credentials, query, or fragment.
`--source` overrides other than `auto` and every `--base-url` override are
refused for browser reads.

The compatible sealed reader currently allowlists `auth.test`,
`conversations.list`, `conversations.info`, `conversations.history`,
`conversations.replies`, `users.list`, and `search.messages`. API mode retains
the broader existing read surface. A browser method absent from the installed
sealed allowlist fails as `browser_transport_capability_unavailable`; it never
falls back to API credentials.

Install a compatible `mac-infra` build so `mac-chrome-session` is on `PATH`,
then manually enable Chrome's
`View -> Developer -> Allow JavaScript from Apple Events` setting. Authenticate
the configured workspace in Chrome yourself. The CLI does not automate SSO,
MFA, passkeys, CAPTCHA, consent, or permission prompts.

From a `mac-infra` source checkout, install or refresh the tool and verify the
entry point with:

```bash
./scripts/setup.sh
mac-chrome-session version
```

### Silent exact-tab lifecycle

For every read, `slack-mgmt`:

1. runs `mac-chrome-session list` and inspects only sanitized tab metadata;
2. matches only `https://app.slack.com/client/<configured-workspace-id>` and
   its descendant paths;
3. sorts matches by numeric Chrome window ID and then tab ID, so discovery is
   deterministic and independent of active-tab state or enumeration order;
4. when no match exists, runs `/usr/bin/open -g -a "Google Chrome"` with the
   canonical workspace URL and performs a bounded metadata readiness poll;
5. invokes `mac-chrome-session slack-read` with the selected exact window/tab
   IDs, `https://app.slack.com`, and the configured workspace ID.

The sealed browser command atomically checks origin and workspace path inside
both its start and poll page scripts before reading page-owned authorization or
starting a request. `slack-mgmt` supplies only a versioned allowlisted method
and bounded arguments over stdin. Page-owned authorization stays inside Chrome.

No silent path activates Chrome, raises a window, selects a tab, clicks UI,
uses the active tab, or falls back to another Slack workspace. If the selected
tab drifts after discovery, the atomic sealed guard refuses the request.

### Output and errors

The sealed response is size-bounded and schema-checked before it is decoded.
All JSON, compact, and error output then passes through the ordinary
`slack-mgmt` redaction boundary. Raw adapter stdout and stderr are never copied
into errors.

| Error | Meaning and action |
| --- | --- |
| `browser_transport_unsupported_platform` | Browser mode currently requires macOS Chrome; select API mode on other platforms. |
| `browser_transport_capability_unavailable` | `mac-chrome-session` is missing, cannot run, or does not allowlist the requested read; install/update `mac-infra`. |
| `browser_workspace_tab_unavailable` | The background-open readiness bound expired or the selected exact tab closed; retry after Chrome is available. |
| `browser_workspace_origin_mismatch` | The exact tab left `https://app.slack.com`; restore the workspace tab and retry. |
| `browser_workspace_identity_mismatch` | The tab or a `team_id` argument points at another workspace; correct the workspace and retry. |
| `browser_workspace_unauthenticated` | Sign in to the configured workspace tab, then retry. |
| `browser_transport_request_refused` | The sealed reader rejected the bounded request or its arguments; correct the query, or update `mac-infra` when the method is supported. |
| `browser_transport_timeout` | Exact-tab discovery or the sealed read exceeded its bound. |
| `browser_transport_malformed_response` | Browser metadata or sealed output was malformed, inconsistent, oversized, or too complex. |

## Writes

Writes remain API-only. Every mutation against a browser workspace fails as
`browser_write_unsupported` before API credential resolution, browser
execution, or network work. Switch explicitly to API mode before an authorized
mutation:

```bash
slack-mgmt workspace set --workspace acme --read-transport api
```

Automated validation uses injected runners, local HTTP fakes, or dry-run. It
must never perform a live Slack mutation.
