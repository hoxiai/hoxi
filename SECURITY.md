# Security Policy

## Supported versions

Security fixes land on the latest release and on `main`. Older releases are not
maintained; upgrade to the latest DMG before reporting an issue that may already
be fixed.

## Reporting a vulnerability

Email **support@hoxi.ai** with a subject starting with `[SECURITY]`. Do not open
a public GitHub issue for an unfixed vulnerability.

Please include:

- Hoxi version and macOS version
- The component involved: desktop host, gateway, web workspace, CLI, approval
  hooks, browser bridge, or mobile client
- Reproduction steps or a proof of concept
- Impact and any preconditions
- Whether the issue affects data confidentiality, integrity or availability

Never include real API keys, tokens, customer data or other people's session
content. Use a throwaway provider key and a local test workspace.

## Scope

In scope:

- The Go desktop host, local reverse proxy and gateway (`internal/app`,
  `internal/gateway`)
- Per-boot gateway token, origin allow-list and directory scoping
- Approval hooks and destructive-command gating (`internal/runner`,
  `internal/agyhook`, `.opencode/plugins/`)
- Headless browser bridge, manual takeover and inspector boundaries
- Handoff parsers and credential discovery
- The Flutter mobile client (`client/`)

Out of scope:

- Vulnerabilities in upstream `opencode`, model providers or third-party CLIs;
  report those to the upstream project
- Issues that require an already-compromised local account or physical access
- Missing hardening without a demonstrated impact

## Security model

Hoxi is a local-first tool. The gateway binds `127.0.0.1` only, uses a per-boot
password/token, enforces an origin allow-list and scopes session requests to a
worktree directory. Destructive shell commands require human approval. AI
requests go directly from the device to the provider or CLI the user configured;
Hoxi does not proxy them through a hosted service.

These boundaries are described in more detail in [BROWSER.md](BROWSER.md),
[DEVELOPMENT.md](DEVELOPMENT.md) and [AGENTS.md](AGENTS.md).

## Disclosure and credit

We will confirm receipt, investigate, and coordinate a fix and disclosure
timeline with the reporter. There is no paid bug bounty. Please give us a
reasonable window before publishing details, and do not test against other
people's machines, accounts or data.
