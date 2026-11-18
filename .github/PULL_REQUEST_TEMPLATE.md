## Summary

<!-- Briefly describe the problem and the approach taken. Link related issues (e.g. Fixes #123). -->

## Verification

- [ ] `go vet ./... && go test -race ./... && go build ./...`
- [ ] `npm --prefix web run lint && npm --prefix web test && npm --prefix web run build`
- [ ] Source files stay under 800 lines and respect unidirectional layering (`AGENTS.md`)
- [ ] Bilingual i18n keys (`en-US` & `zh-CN`) updated in lockstep (if UI text changed)
- [ ] No secrets, tokens, personal paths, or build artifacts included

## Security & Boundary Impact

- [ ] Does this touch approval gates (`internal/runner/shellpolicy`, `web/src/policy/`), Seatbelt profiles (`internal/sandbox`), or remote tunnel routes (`internal/gateway/tunnel.go`)?
  - If **yes**, confirm Go/JS shared fixture parity (`*.cases.json`) and explicit allow + reject regression tests per `docs/ai-coding-guardrails.md`.
