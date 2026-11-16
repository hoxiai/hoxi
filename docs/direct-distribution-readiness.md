# Direct Distribution Readiness

Review date / document version: **2026-10-18**

**Status: unfinished, not release approval.** This checklist concerns direct
website distribution of Hoxi by **Fastix Limited**, not Mac App Store (MAS)
submission. Checked items record repository implementation/local verification only;
external items remain open pending evidence and an accountable operator's sign-off.
Repository implementation or a local build alone
does not establish operational, legal, signing or publication readiness.

Privacy and support contact: **support@hoxi.ai**. Intended website:
**https://hoxi.ai**. Intended territories include mainland China, Hong Kong,
Taiwan, the United States and other legally permitted regions. Territory listing
is not regulatory approval. Website source/backend and live processing evidence
were not supplied for this review.

**Intended service model:** Hoxi is solely a BYOK engineering tool. Users connect
their own AI provider keys/accounts, custom endpoints or external CLIs. Fastix
Limited does not provide official AI inference, cloud-hosted AI chats, inference
subscriptions or credits, and is not automatically the recipient of prompts.
Optional account, update and discovery services are separate from inference.

## Legal Drafts

No legal notices are bundled with the desktop app. The canonical drafts live in
the separately operated website theme (`app/themes/hoxi/public/legal/`), which
serves them from the marketing site, and the desktop app only links out to the
published pages. The copies that previously lived in `web/public/legal/` were
deleted once the in-app AI-consent gate was removed.

These are self-contained HTML review drafts, not effective published policies.
Their date is a draft version, not an asserted effective date. They intentionally
include review warnings and `noindex, nofollow`. Do not silently publish them as
final legal commitments. No company address, governing jurisdiction, retention
deadline or universal liability waiver has been invented.

## Verified Client Scope, Not Backend Proof

The following source references support the draft's description of client flows;
they do not verify deployment behavior or complete external acceptance items:

| Area | Repository evidence | Limit of evidence |
| --- | --- | --- |
| Login and token lifecycle | `internal/auth/service.go`, `store.go`, `types.go` | Browser authorization, state/handoff exchange, polling with platform, token verification, local profile/token file, attempted logout revocation. No server account-deletion guarantee. |
| Credential storage | `internal/auth/store.go`, `internal/provider/config.go` | Local filesystem credentials/configuration, including `~/.hoxi/auth.json` and `~/.hoxi/models.json`; not a Hoxi Keychain vault. Other runtimes/CLIs have their own stores. |
| Updates | `internal/updater/checker.go` | Package, OS, architecture and installed version query; actual backend logs/retention unknown. |
| Discovery | `internal/leaderboard/service.go` | Ranking/language/limit and gateway queries; server infrastructure and analytics unknown. |
| Model-assisted features | `internal/provider/commit_message.go`, `session_title.go`, `audit.go`, `http.go` | Relevant user/project context can reach configured models, beyond manually pasted prompts. |
| Agent, memory, browser and CLI | `.opencode/plugins/`, `internal/browser/`, `internal/handoff/` | Local persistence and tool/context flows can expose code, diffs, memory, page text/DOM and screenshots to selected recipients. Permissions do not prove no onward transmission. |
| Release machinery | [Release verification](releases.md), `build-desktop.sh`, release scripts/workflow | Signing/notarization verification paths exist; no evidence here of a particular accepted production artifact. |

The default Hoxi service URL is `https://hoxi.ai`; configuration can select other
service/provider endpoints. A user-selected gateway may forward requests onward.
Local provider telemetry is not proof of either remote analytics collection or
its absence. No blanket no-collection, no-training or local-only promise is made.

## Repository Implementation And Remaining Review

The current changes implement the following client safeguards. These are source
and local-test evidence, not proof of a live service or gateway ownership.

- [x] Remove the bundled third-party inference recommendation: the gateway directory is fetched live from the official site on every request, with no local or in-memory cache and no offline fallback list. Live directory entries have self-operated/one-click flags and authorization URLs removed, with badge/caveat cleared for entries claiming self-operation. `internal/leaderboard/service_test.go` verifies the live-only behavior and normalization; this does not establish the accuracy of other remote marketing fields.
- [x] Enforce BYOK default selection and reject model-less prompts: `workspace-providers.ts` no longer falls back to free OpenCode or arbitrary catalog models; explicitly selected/configured local no-key models remain supported. Frontend provider-selection and session-AI regressions pass.
- [x] Harden Hoxi model-config writes with private atomic replacement, redact API keys/headers from the workspace mirror when a user config exists, and propagate persistence failures. Logout clears local credentials before changing authenticated state and reports unconfirmed remote revocation to logs. Provider/auth regressions pass; this is not Keychain storage or proof of server revocation/deletion.
- [ ] Review `web/src/i18n/locales/zh-CN/settings.ts`, `accountOfficialAccountDesc`: the text describes an official Hoxi cloud system with gateway and quota sync. Review corresponding account UI and both languages against the no-official-inference model; do not use this copy as evidence of real backend processing.
- [x] Update bilingual leaderboard subtitles and directory tabs to identify third-party providers and state that Hoxi does not provide inference (`en-US/header.ts`, `zh-CN/header.ts`).
- [x] Verified the custom-endpoint fields no longer name a specific third-party gateway: `CustomEndpointTab.tsx` uses a generic base-URL placeholder (`https://api.example.com/v1`) and `hoxi`/`Hoxi` provider examples. Keep copy that distinguishes a user-selected external service from a Hoxi-operated inference offering; “Official endpoint” refers only to the selected provider's own endpoint, not to Hoxi-operated inference.
- [ ] Verify live `/api/hoxi/gateways/connectable` and model discovery responses, authorization behavior and all inference routing against the intended model. `internal/provider/http.go` sends model requests to the supplied provider base URL; client source alone does not prove who owns that URL or what a live gateway does. Reconcile conflicting defaults, labels and backend responses before approving final policies. Do not promise that Fastix receives no data under any possible custom configuration.

## Operator and Counsel Blockers

- [ ] Appoint owners and obtain operator/legal approval of equivalent English and Chinese documents. Verify Fastix Limited's exact legal identity, required contact/address disclosures, governing-law and dispute requirements without inventing them. Set actual effective dates and versioned change/acceptance notices.
- [ ] Inspect the actual website/backend, infrastructure and runtime network traffic. Inventory account/profile records, tokens, IP/request logs, cookies, analytics, update/download/discovery metadata, support messages and any project content reaching Hoxi-operated endpoints. Confirm purposes, legal bases, recipients and any additional uses, including training or evaluation if applicable.
- [ ] Verify actual hosting locations, subprocessors, independent providers and onward recipients, contractual roles, access controls and service agreements. Publish the disclosures and any subprocessor information required by applicable law; do not infer vendor names or locations from desktop dependencies.
- [ ] Establish and document real server retention criteria or schedules for each category, security logs, backups and support records. Verify deletion propagation, lawful exceptions and backup expiry. Obtain third-party policies without inventing their retention dates or training promises.
- [ ] Implement and exercise the support-request account-deletion process at `support@hoxi.ai`: identity verification, account/token handling, backend deletion, processors, exceptions, confirmations and applicable statutory response requirements. Confirm mailbox ownership and staffing. Do not describe it as instant or as already operational merely because these documents provide an email address.
- [ ] Verify access, correction, export, consent-withdrawal and complaint workflows under applicable regional law. Establish actual export formats/scope and escalation; no unverified self-service export or completion deadline is promised.
- [ ] Inventory local storage for Hoxi, OpenCode, browser profiles, worktrees, memory, CLIs and backups. Validate deletion guidance and warnings about remaining copies. Test logout/revocation failure handling. Local deletion/uninstall is not server deletion or third-party erasure.
- [ ] Review filesystem credential protection, permissions of existing/imported files, backup exposure, logging/redaction and secret rotation. Do not advertise Keychain-backed or encrypted-at-rest Hoxi credential storage without implementation and verification.
- [ ] Confirm age eligibility, minors' data handling, age assurance, any guardian consent and response procedures for each launch region. Do not invent a minimum age or claim a verified child-protection program.
- [ ] Have qualified counsel assess mainland China, Hong Kong, Taiwan, US and other launch-region privacy, consumer, marketing, accessibility and applicable export requirements. Identify international transfers and any required notices, separate consents, assessments, contracts or localization measures; a configured foreign endpoint does not establish lawful transfer.
- [ ] Obtain specific mainland China advice on the actual service model and availability: applicable generative-AI/algorithm/deep-synthesis obligations, registrations or filings, content and security requirements, and website ICP filing/licensing obligations. Determine applicability from deployment and business facts; neither this repo nor a foreign operator name resolves these questions. Restrict launch scope where requirements remain unresolved.
- [ ] Verify incident response, breach assessment/notification and security support procedures. Align warranties, limitations, suspension and paid-service/refund terms with mandatory local rights. The draft is not an absolute waiver of responsibility.

## Website Publication Blockers

The following are **proposed publication URLs, not verified live URLs**. Vite's
public assets do not deploy the separately operated marketing website by
themselves. The website owner must publish the approved content or equivalent
pages and confirm any different canonical paths across all clients.

| Document | Proposed URL |
| --- | --- |
| Privacy, English | `https://hoxi.ai/legal/privacy-en.html` |
| Privacy, Chinese | `https://hoxi.ai/legal/privacy-zh.html` |
| Terms, English | `https://hoxi.ai/legal/terms-en.html` |
| Terms, Chinese | `https://hoxi.ai/legal/terms-zh.html` |

- [ ] Publish approved bilingual pages on the actual website and verify HTTPS, status/content type, language links and stable unauthenticated access. Resolve review warnings and indexing policy only after approval; retain archival versions separately.
- [ ] Link the approved URLs from website downloads, account/authorization flows, support and relevant application surfaces. Implement required disclosures/acceptance before collection or use, rather than relying on an obscure footer. Website integration is outside the supplied source and is not completed here.
- [ ] Review all four pages on mobile and desktop, keyboard-only, screen reader, zoom and print; check headings, contrast, text wrapping and translations. No remote assets or scripts are needed by these drafts. Record actual review results, not assumptions based on responsive CSS.

## Software and Artifact Blockers

- [ ] Create a release-specific dependency/license inventory for Go, frontend packages, bundled OpenCode, Wails, browser/runtime components, copied assets/fonts/icons and any bundled or downloaded CLI. Verify exact versions, licenses, redistribution permissions, attribution/notice delivery and source-offer duties where applicable. A top-level license label alone is not an inventory or legal clearance.
- [x] Run local integrated checks on the current working tree (2026-10-18): TypeScript, bilingual i18n validation, Vite production build, `go build ./...`, the four requested Go packages (`gateway`, `provider`, `auth`, `leaderboard`), and the full frontend suite pass. Frontend result: **125 passed, 0 failed** after adding the missing `dataset` field to the new-thread test's DOM mock; no production workaround was needed. Vite transformed **2557 modules** and emitted a non-fatal >500 kB chunk warning.
- [ ] Run remaining release-specific checks and record exact candidate/artifact evidence. See [release verification](releases.md). This local run is not the full release workflow, race/vet coverage, signing/notarization or a clean-machine acceptance run.

Local verification used Node `v24.15.0`, npm/npx `11.12.1`, and Go
`go1.25.1 darwin/arm64`. To reproduce, ensure `node`, `npm`, `npx`, and `go`
are on `PATH`, then run these commands from the repository root:

```sh
npx --prefix web tsc -p web/tsconfig.json --noEmit
npm --prefix web run check:i18n
npm --prefix web run build
npm --prefix web test
go build ./...
go test ./internal/gateway ./internal/provider ./internal/auth ./internal/leaderboard
```

An additional uncached `go test -count=1 -json` run over the same five packages
reported **165 passing test/subtest results**, zero failed and zero skipped:
gateway 114, provider 11, auth 26, leaderboard 3. Counts include
subtests, not only top-level test functions.

- [ ] Produce the exact versioned app/DMG with the correct Fastix Limited-controlled Developer ID identity and required signing settings. Record team identity, nested executable signatures, entitlements, architecture, minimum macOS, embedded versions and artifact SHA-256. An ad-hoc build or script's existence is not production signing evidence.
- [ ] Obtain Apple's notarization acceptance for that artifact and record submission/log evidence, stapled-ticket verification and Gatekeeper/signature assessment results. Perform a clean-machine download/install/launch acceptance run and verify runtime permissions, helper launches and updates. Never commit private keys, tokens or notary credentials to the evidence record.
- [ ] Publish the verified versioned artifacts to intended hosts/mirrors, confirm checksums and download links, and verify the live update/publisher response matches the artifact. GitHub release publication and website/update metadata are separate acceptance steps; neither is inferred from a local dry run.

## MAS Is A Separate Track

This work does **not** make Hoxi MAS-ready. Developer ID signing/notarization for
direct distribution is not App Store approval or proof of App Sandbox compliance.

- [ ] If MAS distribution is later pursued, scope a separate engineering/legal review of App Sandbox, entitlements, process execution, terminal/CLI/browser/runtime integration, filesystem access and security-scoped resources, distribution/update rules, account deletion, store disclosures and review requirements. Do not mark this item complete for a direct-distribution release.
- [ ] Assess privacy manifests and required-reason API declarations against actual APIs, dependencies and the chosen distribution channel. Do not fabricate reasons, add a placeholder manifest, or treat one as proof of compliance.

## Evidence And Sign-Off

For each item, record an owner, review date, release/artifact identity, evidence
location and remaining limitations in the release record. Keep secrets and user
data out of evidence. Legal approval, backend verification, public URL checks and
real signing/notarization evidence remain required before an operator can make a
direct-distribution go/no-go decision. This document makes no such decision.
