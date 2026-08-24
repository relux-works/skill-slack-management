# Read Provider Development

`internal/provider.ReadProvider` is the only live Slack data boundary consumed
by `internal/query.Engine`. The query engine parses and projects DSL results; it
does not resolve workspace configuration, credentials, executables, browser
state, or provider kinds.

## Contract

Every provider must:

- return normalized provider-domain records and pages rather than transport
  envelopes
- expose deterministic `Capabilities` with protocol and adapter versions
- return `provider_capability_unsupported` before credentials, browser work,
  subprocesses, or network calls for unsupported operations
- preserve `next_cursor` semantics through the normalized page types
- keep mutation construction separate from the read protocol
- supply contract fixtures for every operation it claims

The Web API provider is one provider family. `api` and `browser` are delivery
transports inside that family and must not appear as provider branches in the
query engine.

## Adding a Provider

1. Add a `provider.Kind` and a validated `ProviderSpec` shape.
2. Implement `ReadProvider` and declare only operations that reproduce.
3. Add the provider to the runtime factory. Construction must be injectable and
   side-effect free for capability discovery.
4. Add normalized fixture tests shared with existing providers.
5. Add negative tests that drive `query.Engine.Execute` and prove an
   unsupported operation never reaches provider I/O.
6. Document authorization ownership, setup, diagnostics, limits, and risks.

`slack-mgmt q 'provider_capabilities()' --format json` is the stable discovery
surface. A provider-specific diagnostic belongs behind `workspace diagnose`,
not in the query engine.
