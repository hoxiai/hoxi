# Changelog

All notable releases of **Hoxi Desktop** are documented below. Full per-version release notes are archived under [`docs/release-notes/`](docs/release-notes/) and consumed directly by `.github/workflows/release.yml` and `scripts/ai-release.sh`.

## [v0.7.3](docs/release-notes/v0.7.3.md) — 2026-11-22

- **Shell Approval Noise Reduction, Reason Banner & Inline-Script Grants**: Added Go/JS approval policy support for fail-fast `set` options, quoted `mktemp` + `trap` cleanup idioms, safe value-class substitutions (`gofmt -l`, `realpath`, checksums), non-arithmetic `[[ ... ]]`, read-only piped `xargs`, and literal path brace lists with rebuilt `browser_plugin.mjs` and expanded bypass-denial fixtures; added a sanitized trigger reason banner to `ApprovalGateCard` (`approval-reason.js`); added desktop-only, session-memory, revocable inline-script class grants under strict lexical/capability hard floors.
- **Remote App Overview, Scoped Session Creation & Mobile File Workflows**: Added `GET /api/hoxi/work/apps-overview`, enriched `GET /api/hoxi/work/app-sessions/{appId}` with launchpad metadata, pending approval counts, and sanitized read-only `hasAccountBinding`/`activeAccountLabel` display labels, and added `POST /api/hoxi/work/app-sessions/{appId}` with strict JSON allowlisting, singleflight `clientRequestId` idempotency, and bind-failure rollback; upgraded mobile clients with native document attachments, workspace binary file viewing/system share export, and published `Hoxi-Remote-0.7.3-android.apk` (`ai.hoxi.remote`).
- **Release Pipeline Hardening**: Added target repository tag pre-checks and hardened cross-platform desktop release workflows and open-source mirror release isolation.

## [v0.7.2](docs/release-notes/v0.7.2.md) — 2026-11-22

- **Windows Frameless Top Bar & Native Window Controls**: Integrated a frameless Windows title bar with native-style minimize/maximize/close controls, header double-click maximize hit-testing, active canvas background sync, and z-index layering above onboarding overlays.
- **Windows Updater, CLI Shims, Shell & Path Hardening**: Ordered `Hoxi.exe` replacement before sidecars with strict `ErrorActionPreference=Stop` retry cleanup; unwrapped standard `npm`/`pnpm`/`yarn` `.cmd`/`.bat` and extensionless Node shebang shims directly to `node.exe` so `cmd.exe` never mangles JSON/TOML quotes; extracted shared `subproc.ResolveWindowsBash` preferring Git Bash over WSL stubs with case-insensitive askpass/mixed-case env handling; fixed Explorer `/select` for paths with spaces and drive roots, normalized Windows paths, and prevented spurious native sessions.
- **Cross-Platform Path Normalization & Approval Policy Parity**: Added shared `stripRootPrefix` for Windows backslash/drive-casing normalization across Inspector, ToolCard, ShellSummary, Diffs, and CommandPalette; aligned Go and JS shell approval policy (`policy-paths.js`) on Windows drive paths, temp directories, sensitive system paths, and variable-width Unicode case-folding with shared parity fixtures and rebuilt `browser_plugin.mjs`.
- **Cross-Platform Build & Release Pipeline**: Hardened Windows `amd64`/`arm64` cross-arch binary verification in `build-windows.ps1`, resolved `gofmt` and frontend layering checks, and published `Hoxi-Remote-0.7.2-android.apk` (`ai.hoxi.remote`).

## [v0.7.1](docs/release-notes/v0.7.1.md) — 2026-11-22

- **Native Agent Engine & Orchestration**: Added built-in `web_search`, proactive context-window compaction, history-aware subdirectory rule deduplication, refined step loop guards and graceful finalization, session-scoped Fast Mode across native and CLI runtimes, PageRank RepoMap indexing, foreground bash promotion on Steer, dynamic per-session MCP injection, read-only turn inspection, long-line read/grep guards, and isolated subagent cache keys.
- **Multimodal Media Workflows & Inline Previews**: Added `hoxi_video` video generation, `hoxi_speech` TTS, native/local capability self-checks, media role defaults with inline setup cards, unified loopback workspace media staging with video playback modal previews, <=20MB native video inlining, automatic staged audio ASR, inline generated media & local SVG previews, inline Mermaid diagrams, on-demand document/HTML preview overlays, and SSRF + binary MIME hardening for remote media fetches.
- **Cross-App Protocol, 6-Digit Pairing & Task Workflows**: Implemented `hoxi-app/v1` loopback discovery, PKCE grants, host MCP exposure, and desktop approval UI; added zero-knowledge 6-digit one-time pairing codes with countdown/cancel and standalone `cmd/hoxi-pairing-relay`; shipped the scoped QA task intake portal with Markdown and sanitized live progress; added dismiss/undo controls to Task Overview, a dedicated Recent Tasks sidebar tab, and a sensitive-directory-guarded native directory picker API.
- **Brand Icons, CLI Installers & Runtime Hardening**: Unified desktop, web, status bar, and mobile apps on the crisp dot-matrix H mark (10x11 / 7x8 without moire), upgraded lossless companion art, fixed dark terminal backgrounds under wallpaper mode, added cross-platform CLI install scripts with managed-update detection and auto-upgrading symlinks, enforced 4-tier Go DAG / transport / plugin `contractcheck` gates, hardened OAuth refresh rotation, system proxy inheritance, Codex model catalogs, and Antigravity hook self-healing, and published `Hoxi-Remote-0.7.1-android.apk` (`ai.hoxi.remote`).

## [v0.7.0](docs/release-notes/v0.7.0.md) — 2026-11-18

- **Go Native Agent Engine**: Default in-process Go native execution engine and pure-Go SQLite store without external sidecar processes; built-in `build`/`plan`/`ask`/`debug` modes, `general`/`explore`/`scout` subagents, heterogeneous subagent model routing with economy auto-selection, custom Markdown/JSON agents and slash commands, in-turn `plan_consent` Plan-to-Build promotion, reactive context-overflow compaction, revision-checked file edit leases, background bash streaming, `.gitignore` doublestar search, multi-language post-edit diagnostics (Go, TypeScript, Vue, offline Rust, PHP, Dart), and Stdio MCP startup budgets with circuit breaking.
- **Native Project Config & Policy Modularity**: Shared repository-root `hoxi.json` execution configuration isolated from local `.hoxi/models.json` credentials (read-only overlay without relaxing host approval), unified `.hoxi/` skill/command/agent roots, retired root `.opencode/`, and modularized `internal/runner` into 6 unidirectional subpackages with shared `shellpolicy` + `web/src/policy/` test suites.
- **Zero-Credential Data Profiles, Headless Host & X25519 E2EE**: Added `hoxi_data` zero-credential profiles for HTTP, read-only gated `sqlite`/`postgres`/`mysql`, and allowlisted `ssh` with automatic secret redaction; added `hoxi host` headless daemon pairing, health probes, and streaming reverse proxy; shipped `hoxi-e2ee-v1` (`v1-sse`) directional AES-256-GCM encryption, 1-RTT X25519 ECDH key exchange, monotonic `seq` SSE replay, and a durable 3-state request admission ledger.
- **Desktop Personalization & UX**: Added ACG color palettes, adaptive optical frosted-glass wallpaper engine, 2x lossless companion standees over vector SVG backdrops, screen work-area adaptive initial window geometry, denoised `AssistantErrorCard` and account menu, and lower Wails dev/idle CPU and memory overhead.
- **Security, Runtime Hardening & Open-Source Mobile**: Blocked `~/.codex/`, `~/.local/share/opencode/`, and private key/certificate reads (`*.pem`/`*.key`/`*.p12`/`*.pfx`), made `hoxi -p` EOF fail-closed, hardened native on-demand Seatbelt tickets and symlink guards, defaulted telemetry off in source builds with silent offline mode, preserved thinking signatures on unified proxies, fixed Antigravity 2.0 subagent routing and daemon-free SQLite session deletion tombstones, and published the open-source Flutter mobile controller (`mobile/`) with signed `Hoxi-Remote-0.7.0-android.apk` (`ai.hoxi.remote`) alongside the full `Hoxi` mobile app (`ai.hoxi.app`).

## [v0.6.19](docs/release-notes/v0.6.19.md) — 2026-11-17

- **Native Engine & Lean Packaging**: Ship the in-process Go native execution engine by default and omit bundled OpenCode sidecars across macOS, Windows, and Linux installers (over 70% smaller desktop bundle; ~52 MB unpacked / ~16 MB compressed on macOS), treating OpenCode as an optional external CLI alongside Claude Code and Codex.
- **Models & Routing**: Align 2026 frontier model params, modalities and context limits; preserve runtime reasoning tiers; require explicit evidence for free models; multimodal pool routing, auto-ASR fallback, persistent vectors and multi-protocol relay; Gemini and `/v1/responses` parity.
- **Approval**: Reduce read-only false positives (bounded numeric loops, quoted directories, composite heredoc chains, test filters, value lookups); keep OpenCode always-allow in Hoxi with per-file session grants; tighten command-sandbox sessions to the on-demand profile.
- **Browser & Runtime**: Configurable Chrome idle auto-close (30m default), graceful close and SingletonLock guards; rotate poisoned OpenCode sessions and preserve failed turn errors; support standalone `antigravity-cli` and surface local CLI subscription quota.
- **Performance**: Lossless startup, memory and GPU compositing optimizations; indexed history and batched stream persistence; faster project switching and worktree polling; offloaded transcript search/highlighting.

## [v0.6.18](docs/release-notes/v0.6.18.md) — 2026-11-10

- **Sandbox Hardening**: Deny every loopback TCP/UDP port already listening when an on-demand profile is written (refusing to confine when the snapshot fails), allow outbound Unix sockets only under the workspace, and deny other agents' sign-in tokens and IDE credential locks.
- **Cross-Restart Resume**: Persist in-flight turns and idempotent `prompt_async`/approval receipts, drain the current tool step before a graceful restart, resume OpenCode turns with a byte-stable system prompt and native CLIs by `ExternalID`, and resume convoy DAGs without double-merging.
- **Approval & Sandbox Fallback**: Fall back to an on-demand Seatbelt sandbox for opaque commands in auto mode; match literal approval rules without regex so thousands of always-allow rules stay fast.
- **Attachments & Docs**: Native `.xls` parsing, inline spreadsheet cell editing, PDF/spreadsheet previews, lossless xlsx/csv edits, a `hoxi doc` CLI and clickable timeline previews.
- **Runtime & Provider**: Resume split-brain Antigravity conversations, repair torn JSONL tails, keep the OpenCode system prompt byte-stable across steer/handoff, route official models by category, list Google/Anthropic models and align reasoning-effort variants.
- **Tunnel, Skills & Perf**: Auto-register the default relay with token auth; expose skills by fit and slim the per-request context; confirm desktop access to sensitive credential files; move busy spinners to the WebKit compositor and release the chime's audio device.

## [v0.6.17](docs/release-notes/v0.6.17.md) — 2026-11-06

- **Steer & Approval**: Enable in-run steering for Antigravity with a rebuilt record after the steer abort; keep `always` grants the gate actually honors and refuse unattended before allowing; gate Claude Monitor as a command and fix trusted-mode misjudgments.
- **Official Models**: Route official account models by category and carry category, modality and default flags through the platform API.
- **Timeline & Stability**: Remove the follow-up send layout/scroll flash and the macOS transcript blink; simplify the thought header; backfill Antigravity reasoning; preserve the latest assistant turn and Path edits on handoff fallback; fix prompt dispatch, AGY thinking, stall watchdog, Windows UTF-8 and Git NUL path edge cases.

## [v0.6.16](docs/release-notes/v0.6.16.md) — 2026-11-05

- **Dev Pulse**: Count commits on every local branch for a project root while a worktree stays scoped to its own branch; key file sets and the cache by repo root; dedupe uncommitted files and skip Hoxi snapshot commits on every target.
- **Antigravity & Terminal**: Clear a stale result error once a later step completes the final response, live and on transcript hydration; repair CP936/Latin-1 mojibake in Windows terminal records.
- **Views**: Surface provider health and telemetry in the usage and pulse views; stop the layout flash when sending a follow-up after a completed task.
- **Structure**: Extract daily and history aggregation into `internal/devpulse` so both share one git-target and caching model.

## [v0.6.15](docs/release-notes/v0.6.15.md) — 2026-11-05

- **Approval**: Remove the AI review pass so risky commands prompt for manual confirmation immediately instead of blocking on a synchronous model call.
- **Dev Pulse & Usage**: Resolve pulse directories from the session worktree plus project root, count today's uncommitted tracked and untracked changes, bucket by committer date, dedupe and skip snapshot commits; narrow the ignore rule to `build/bin/`.
- **Hot Paths & Updater**: Force the session-message-part index join order with time bounds, cache global daily usage, and stop re-cloning session records; add a Windows busy-update confirmation, widen the status probe timeout, and clear stale subagent busy state.

## [v0.6.14](docs/release-notes/v0.6.14.md) — 2026-11-05

- **Theme & Sidebar**: Align light surface, border and elevation tokens with the default blue palette; use lucide folder icons, selected-surface active rows, accent user-turn cards and a quiet completed badge.
- **Timeline**: Stop yanking the viewport back to an older activity group after sending while the transcript follows the live tail; restore the floating changed-files pill in the composer shelf while busy; add a send-scroll-anchor browser regression.
- **Runtime & Storage**: Decode CP936/GB18030 to fix garbled Windows terminal output; wait for OpenCode turn teardown in `CloseAll` and cancel process scans on forget; tighten session cleanup, atomic persistence and store boundaries.

## [v0.6.13](docs/release-notes/v0.6.13.md) — 2026-11-05

- **Turn Retry Bounds**: Abort engine retry events beyond 15s, 3 attempts or a permanent/quota fault, stamp the error on the last assistant message, and stop auto-retrying long quota resets; keep `Retry-After` inside the retry ceiling.
- **Computer & Pairing**: Tighten computer-control grants per session and app with inspector state; harden the pairing poll lifecycle and surface progress in the settings menu.
- **Bundled Skills**: Seed built-in default skills automatically on startup with version-gated updates.

## [v0.6.12](docs/release-notes/v0.6.12.md) — 2026-11-04

- **Hotfix**: Fix the blank window on launch after upgrading to v0.6.11 — the workspace sidebar read `isLoaded` before initialization (TDZ) when persisted workspace state existed.
- **Tests**: Pin `core.autocrlf` off in the worktree merge fixture so it passes on Windows runners.

## [v0.6.11](docs/release-notes/v0.6.11.md) — 2026-11-04

- **Timeline**: Keep only the active execution group expanded and auto-collapse earlier completed groups; add entrance transitions for streamed steps and thinking indicators; de-noise Level-2 tool and thought rows; fold brief pre-tool narration into the activity group; compact non-document markdown; fix blank screens when scrolling up in long sessions by skipping zero-height folded turns and prefetching history.
- **Runtime & Storage**: Gate same-model retry on the auto pool so non-auto sessions stop silently re-sending; recover stale `index.lock`; deep-copy and atomically persist provider telemetry; invalidate relay-key cache on file change; preserve custom `.env`, guard `.git/info/exclude`, include untracked binaries in diffs, and clean discovered repo caches.
- **Safety & Cleanup**: Guard local file open against symlink swap, skip body buffering for runner-owned routes, reject oversized archives, and clear subagent links, diff revisions and deleted-session timers in the store.

## [v0.6.10](docs/release-notes/v0.6.10.md) — 2026-11-04

- **Updater**: Align cross-architecture update URLs to the running host arch for Darwin DMG and Windows ZIP; resolve the checksum from the sibling `checksums.txt` by filename; keep polling native install progress after the install API returns; report a failed user-initiated install instead of ending silently; reuse a pending install reply on a repeated click.
- **Gate, Skills & Release**: Block updates while a permission approval is pending as well as while a session runs; honor the service `baseURL` and default `hoxi.ai` catalog host in `marketplaceHostAllowed` when `HOXI_SERVER_URL` is overridden; publish per-DMG `.sha256` files with each release.

## [v0.6.9](docs/release-notes/v0.6.9.md) — 2026-11-02

- **Approval**: Stop treating `$"` / `$'` inside double quotes as ANSI-C or locale quoting; allow safe `python3 -m` pipelines while blocking REPL, code-exec and publish modules; mask data arguments after benign literal prefixes; avoid duplicate pending-approval broadcasts when AI review falls back to manual confirmation.
- **Window & Dev Server**: Fall back to embedded assets when `assetdir` lacks `index.html`; bypass the system proxy for loopback hosts; restore original Host/URL and response headers after a failed Vite proxy; accept the macOS WKWebView Host in the Vite dev server and allow repo-root assets.

## [v0.6.8](docs/release-notes/v0.6.8.md) — 2026-11-02

- **Fault Classification & Failover**: Classify stream EOF and network interruptions as retryable faults so automatic failover retries transient errors instead of failing the turn; align candidate health and failover on one shared fault structure.
- **Timeline & Approval**: Remove streaming markdown stalls and hidden context rescans; animate step groups and thoughts on first expand; tighten static approval gates and isolate browser profile directories; guard runaway duplicate tool loops and dedupe shell command headers.
- **Engineering Gates**: Enforce Wails host isolation, gateway/CLI layering and gofmt checks in CI; keep Windows version metadata and the root CHANGELOG in sync with release notes.

## [v0.6.7](docs/release-notes/v0.6.7.md) — 2026-11-02

- **Approval Gate**: Treat read-only `manage_task` / `manage_subagents` (`status`, `list`) and wait tools (`wait`, `wait_5_seconds`) as read-only without triggering confirmation or doom-loop false positives; parse quoted `Action` fields accurately and allow "Always allow" on doom-loop cards.
- **Session & Timeline**: Scope message jump and deep-link navigation strictly to the originating session and directory; fetch full live transcript snapshots before session export; preserve transition animations on sandbox merge card collapse; guard smart commit message drafts against stale responses; auto-expand subagent drawers by default.

## [v0.6.6](docs/release-notes/v0.6.6.md) — 2026-11-01

- **Updater & Timeline**: Display live download and installation progress for in-app updates; eliminate timeline jump when assistant replies replace the thinking indicator; synchronize pending question cards across workspaces.
- **Runtime & Approval**: Ignore stale Antigravity trajectory errors on session resume; mask literal data inside subshells and `bash -c` before destructive command checks; raise open-file limits (`RLIMIT_NOFILE`) and skip oversized home/system roots in code indexing.

## [v0.6.5](docs/release-notes/v0.6.5.md) — 2026-11-01

- **Subagent Grouping & Turn Retry**: Group non-archived subagent and convoy sessions under their root parent session in the sidebar; automatically retry native CLI turns on transient network/timeout/5xx/429 errors.
- **Approval & AI Review**: Expand safe read-only loop and constant substitution rules; allow inline interpreter scripts via AI review with automatic fallback when the configured review model is unavailable; clean up stale pending approvals/questions at turn end.

## [v0.6.4](docs/release-notes/v0.6.4.md) — 2026-10-31

- **Single-Instance & Windows Hygiene**: Hand off repeat launches to the running desktop instance before starting any daemon; route `hoxi://` deep links to the desktop app on Windows and tie `opencode.exe` to a kill-on-close Job Object.
- **Quota Failover & History Paging**: Classify HTTP 402 and insufficient-balance errors as quota exhaustion with automatic candidate failover; page long session histories (`limit` / `before`) while preserving reading position.

## [v0.6.3](docs/release-notes/v0.6.3.md) — 2026-10-30

- **Windows ARM64 Release Gate**: Verify cross-compiled `windows/arm64` binaries via embedded Go build metadata (`go version -m`) and PE machine headers when running on x64 CI hosts.

## [v0.6.2](docs/release-notes/v0.6.2.md) — 2026-10-30

- **Windows on ARM & Official Channel**: Ship native `windows/arm64` NSIS installers and portable archives alongside `windows/amd64`; auto-heal official provider configurations after provider ID migration; require native per-session confirmation for computer control.

## [v0.6.1](docs/release-notes/v0.6.1.md) — 2026-10-30

- **Intel macOS Release Verification**: Validate `darwin-amd64` DMGs via Mach-O slice, Developer ID signature, `Info.plist` metadata, and pinned `opencode.lock` SHA-256 checksums without cold-starting the x86_64 sidecar under Rosetta in CI.

## [v0.6.0](docs/release-notes/v0.6.0.md) — 2026-10-30

- **Intel macOS & Session-Owned Process Control**: Ship notarized `darwin-amd64` DMGs alongside `darwin-arm64`; auto-approve `kill` / `pkill` when every target belongs to process groups spawned by the requesting session; introduce paginated session history loading.

## Earlier Releases

- **[v0.5.6](docs/release-notes/v0.5.6.md)** · **[v0.5.5](docs/release-notes/v0.5.5.md)** · **[v0.5.4](docs/release-notes/v0.5.4.md)** · **[v0.5.3](docs/release-notes/v0.5.3.md)** · **[v0.5.2](docs/release-notes/v0.5.2.md)** · **[v0.5.1](docs/release-notes/v0.5.1.md)** · **[v0.5.0](docs/release-notes/v0.5.0.md)**
- **[v0.4.4](docs/release-notes/v0.4.4.md)** · **[v0.4.3](docs/release-notes/v0.4.3.md)** · **[v0.4.2](docs/release-notes/v0.4.2.md)** · **[v0.4.1](docs/release-notes/v0.4.1.md)** · **[v0.4.0](docs/release-notes/v0.4.0.md)**
- **[v0.3.3](docs/release-notes/v0.3.3.md)** · **[v0.3.2](docs/release-notes/v0.3.2.md)** · **[v0.3.1](docs/release-notes/v0.3.1.md)** · **[v0.3.0](docs/release-notes/v0.3.0.md)**
- **[v0.2.12](docs/release-notes/v0.2.12.md)** · **[v0.2.11](docs/release-notes/v0.2.11.md)** · **[v0.2.10](docs/release-notes/v0.2.10.md)** · **[v0.2.9](docs/release-notes/v0.2.9.md)** · **[v0.2.8](docs/release-notes/v0.2.8.md)** · **[v0.2.7](docs/release-notes/v0.2.7.md)** · **[v0.2.6](docs/release-notes/v0.2.6.md)**
