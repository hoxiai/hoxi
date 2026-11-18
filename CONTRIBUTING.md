# Contributing to Hoxi

Thanks for helping improve Hoxi. This guide covers the local workflow and the
rules CI enforces; the architectural rationale lives in
[DEVELOPMENT.md](DEVELOPMENT.md) and [AGENTS.md](AGENTS.md).

## License of contributions

Hoxi is licensed under Apache-2.0 (see [LICENSE](LICENSE)). By submitting a pull
request you agree that your contribution is licensed under the same terms and
that you have the right to submit it. There is currently no separate CLA; do not
contribute code you cannot license this way.

## Prerequisites

- macOS 13 or newer (Apple Silicon is the primary release target) or Windows 10/11 (x64 / ARM64)
- Go 1.25+
- Node.js 20+
- Wails CLI v2.13.0
- Hoxi includes a built-in Go Native Agent Engine (`internal/agent`) out of the
  box; optionally install `claude`, `codex`, `opencode`, `agy`, `gemini`, `qoder`
  or `codebuddy` on `PATH` when testing external CLI runtime adapters

```bash
wails dev                    # desktop app with hot reload
go run ./cmd/hoxi --no-desktop   # headless gateway
./build-desktop.sh           # production .app
```

## Required checks

Run these before opening a pull request; CI runs the same gates:

```bash
go vet ./... && go test ./... && go build ./...
npm --prefix web run lint
npm --prefix web test
npm --prefix web run build
```

`npm --prefix web run lint` also runs `check:i18n` and `check:lines`. The latter
enforces the 800-line source limit and the frontend layering boundaries.

If your change touches the Flutter mobile companion (`mobile/`), also run:

```bash
cd mobile
python3 scripts/check_source_lines.py
python3 scripts/check_network_boundary.py
python3 scripts/check_feature_boundaries.py
flutter pub get && flutter gen-l10n
flutter analyze && flutter test
```

## Engineering rules

- **Native-first & external CLI zero-fork**: Hoxi runs on its built-in Go Native
  Agent Engine (`internal/agent`, `runtime: "native"`) by default, and drives
  external CLIs through `internal/runner` without forking upstream tools. Store
  project state and defaults under `.hoxi/` and `~/.hoxi/`; never create
  `.opencode/` in user repositories.
- **Layering**: `components/ -> store/ -> services/` in the frontend;
  `app/cli -> gateway -> domain packages -> foundation packages` in Go. Domain
  packages must not import `internal/gateway`, `internal/app` or `internal/cli`.
- **One domain per file**: put new code in the package that owns the domain, not
  in a catch-all `types.go` or `helpers.go`. Source files stay under 800 lines.
- **Gateway planes**: session operations go through `/api/hoxi/sessions`, events
  through `/api/hoxi/event`, approvals through `/api/hoxi/permission`, and host
  services through `/api/hoxi/{pty,agents,commands,lsp,file,provider}`. Requests
  tied to a session or sandbox must carry `?directory=<worktree-path>`.
- **Safety**: destructive commands must stay behind the approval gate. Never add
  an auto-approve path for `rm`, `sudo`, `chmod`, `chown`, `diskutil`, `mkfs`,
  `git push --force` or `git reset --hard`.
- **Network egress**: prompts, code, diffs and session content may only go to the
  destinations the user configured. Auxiliary calls must not include them.
- **i18n**: user-visible text goes through `t("domain.key")`; update both
  `en-US` and `zh-CN` modules with identical keys and placeholders. Chinese
  entries do not end with `。`.
- **SolidJS**: never destructure props or store state; clean up listeners,
  observers and timers in `onCleanup`; guard cross-session async reads against
  stale responses.
- **Telemetry**: any new network egress must respect the opt-out documented in
  [docs/telemetry.md](docs/telemetry.md) and must not carry user content.

## Tests

- Go: table-driven tests next to the package, under 800 lines per file.
- Frontend: Node-based tests in `web/src/__tests__/`. Register new files in
  `web/scripts/run-tests.ts`; they do not run automatically otherwise.
- Prefer a regression test that fails before the fix.

## Commits and pull requests

- Keep commits focused and write an imperative subject, for example
  `fix: scope worktree diffs to the active session`.
- Every commit developed with Hoxi must end with:

  ```text
  Co-authored-by: Hoxi <code@hoxi.ai>
  ```

- Describe the problem, the approach and the verification commands you ran.
- Link related issues, and call out user-visible or security-sensitive changes.
- Do not include secrets, tokens, personal paths or generated build artifacts.

## Reporting security issues

Do not use a public issue. Follow [SECURITY.md](SECURITY.md).
