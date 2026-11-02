# Changelog

All notable releases of **Hoxi Desktop** are documented below. Full per-version release notes are archived under [`docs/release-notes/`](docs/release-notes/) and consumed directly by `.github/workflows/release.yml` and `scripts/ai-release.sh`.

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
