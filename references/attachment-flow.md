# Attachment Flow

Current local contract:

1. Runtime or helper materializes incoming attachments into a local manifest.
2. `local_path` is the source of truth for agent file access.
3. `slack-mgmt q 'attachments() { overview }'` inspects what is available.
4. `slack-mgmt attachment stage ...` copies one resolved input into the destination the workflow needs.

Important rules:

- Attachments are treated as read-only inputs.
- Agents may copy, move, transform, or derive outputs from the local file.
- The output destination is workflow-specific and may be a board resource, generated artifact, cache, or export.
- Board resources are optional persistence, not a hard requirement for every attachment flow.
