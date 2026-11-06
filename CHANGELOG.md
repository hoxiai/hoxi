# Changelog

All notable releases of **Hoxi Desktop** are documented below. Full per-version release notes are archived under [`docs/release-notes/`](docs/release-notes/) and consumed directly by `.github/workflows/release.yml` and `scripts/ai-release.sh`.

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
- **Bundled Skills**: Seed `ashare-ops`, `hk-warrant-ops` and `us-options-ops` as default skills.

## [v0.6.12](docs/release-notes/v0.6.12.md) — 2026-11-04

- **Hotfix**: Fix the blank window on launch after upgrading to v0.6.11 — the Work sidebar read `isLoaded` before it was initialized (TDZ) whenever a Work workspace existed; the release build had bundled a stale `hoxi-work` revision without the fix.
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
