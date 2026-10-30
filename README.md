<p align="center">
  <a href="https://hoxi.ai"><img src="docs/assets/hoxi-banner.svg" alt="Hoxi" width="310" /></a>
</p>

<p align="center">
  Native macOS command center for <b>parallel AI software engineering</b>, paired with a mobile control plane for <b>Hoxi &amp; <a href="https://github.com/hoxiai/gopanel">GoPanel</a> servers</b>.
</p>

<p align="center">
  <a href="https://hoxi.ai"><img src="docs/assets/hoxi-pill-en.svg" alt="Desktop · Mobile · GoPanel Ops" height="54" /></a>
</p>

<p align="center">
  <a href="https://github.com/hoxiai/hoxi/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-2563EB?style=flat" alt="License" /></a>
  <a href="https://hoxi.ai"><img src="https://img.shields.io/badge/website-hoxi.ai-4F46E5?style=flat" alt="Website" /></a>
  <a href="https://github.com/hoxiai/gopanel"><img src="https://img.shields.io/badge/server_panel-GoPanel-0EA5E9?style=flat" alt="GoPanel" /></a>
  <a href="https://github.com/hoxiai/hoxi/releases"><img src="https://img.shields.io/github/v/release/hoxiai/hoxi?style=flat&color=2563EB" alt="Release" /></a>
  <a href="https://github.com/hoxiai/hoxi/stargazers"><img src="https://img.shields.io/github/stars/hoxiai/hoxi?style=flat&color=2563EB" alt="Stars" /></a>
</p>

<p align="center">
  <kbd><a href="README.md">English</a></kbd>
  <kbd><a href="README.zh-CN.md">中文(简体)</a></kbd>
</p>

---

**Hoxi** ([hoxi.ai](https://hoxi.ai)) unifies **OpenCode**, **Claude Code**, **Google Antigravity (`agy`)**, **Gemini CLI (`gemini`)**, **Qoder**, and **CodeBuddy** inside a native macOS workspace (typically **~30 MB** RAM and **~0.2s** cold start)—and extends to your phone (`client/`) to command running AI agents and manage [**GoPanel**](https://github.com/hoxiai/gopanel) Linux servers on the go.

---

## Why Hoxi Exists (The Pain Points We Solve)

### 1. Parallel Agents Clobbering Your Working Tree
Running two agents at once in the same repo overwrites uncommitted files and breaks builds. Comparing how Claude, GPT, or Gemini tackles a tricky refactor usually means manual `git stash` gymnastics.
- **Per-Session Git Worktree Sandboxes**: Each session can run in an isolated `.hoxi/worktrees/<session-id>` branch or directly in your local checkout (toggle anytime when idle).
- **Multi-Model Worktree Arena**: Dispatch the same prompt to **2–4 models concurrently**, each in its own worktree. Review hunk-level diffs side-by-side (`@pierre/diffs`), pick the winner, and merge in one click.
- **Time-Travel Revert & Fork**: Roll back both conversation history and disk files to any message or tool call (`/revert` & `/unrevert`), or `/fork` a session mid-flight to explore an alternative architecture.

### 2. Rate Limits, CLI Silos & Lost Context
When Claude Code hits a usage cap mid-refactor, or an agent loops on a bug, switching tools usually means re-explaining your entire task from scratch.
- **Multi-Runtime Unified Timeline**: Run **OpenCode** (supervised `opencode serve`, zero-fork), **Claude Code** (`claude`), **Google Antigravity** (`agy`), **Gemini CLI** (`gemini`), **Qoder**, and **CodeBuddy** natively inside the same 3-pane shell. Switch runtimes mid-session without losing your thread. Qoder and CodeBuddy run read-only (Plan mode) only—their CLIs have no PreToolUse approval gate.
- **Nested Subagent Inspection**: Subagents spawned by Claude Code or Antigravity render as interactive, collapsible task trees (`AgentStackPill`) instead of opaque stdout dumps.
- **1-Click Cross-CLI Session Handoff**: Scans local histories from **Claude Code**, **OpenAI Codex CLI**, **Grok CLI**, **Antigravity / Gemini CLI**, **OpenCode**, **Trae**, **Qoder**, and **CodeBuddy**. Imports every turn, reasoning block, and normalized tool call, and synthesizes an **8-dimension handoff memory** so the next model continues seamlessly.

### 3. Blind Auto-Approve vs. Terminal Babysitting
Giving agents blanket shell access risks `rm -rf` or `git push --force`. Approving every file read chains you to your desk. And when an agent drifts off-track, aborting wastes the whole turn.
- **Unified PreToolUse Safety Gate + Seatbelt OS Sandbox + Unattended Mode**: Safe reads and edits flow uninterrupted. Destructive commands (`rm`, `sudo`, `chmod`, `diskutil`, `git reset --hard`, `git push --force`) are intercepted across OpenCode, Claude Code, Antigravity, and Gemini CLI—triggering a macOS `NSStatusBar` **Attention** badge and an inline approval card (`Once` / `Always` / `Reject`). In **Unattended Mode** (`unattended`), sessions run inside an isolated Git worktree and macOS Seatbelt OS-level sandbox with automatic credential env stripping, sensitive path blocking (`~/.ssh`, `~/.aws`, `.env`), and hard rejection of dynamic shell evaluation, privilege escalation, and `git push`.
- **Live Mid-Stream Steering (`Steer`, OpenCode sessions)**: Inject course corrections while an agent is actively executing without killing the turn.
- **Isolated Headless Chrome + Chrome Link Extension**: Per-session Chrome inspector with one-click **Manual Control** for login walls/CAPTCHAs, plus the Chrome Link MV3 extension (`extensions/chrome`, 2-minute one-time pairing code) to attach to tabs in your existing Chrome browser.

### 4. Stepping Away from Your Desk (Mobile AI + [GoPanel](https://github.com/hoxiai/gopanel) Server Ops)
Solo builders and lean teams don't just write code—they also deploy and maintain servers. Being away from your laptop shouldn't stall a long-running agent or leave a production alert unanswered.
- **Resilient Dual-Transport Mobile Console ([`client/`](client/README.md) — `Chat · Workbench · Solo Company · Me`)**:
  - **3-Path Connectivity (LAN-First with Relay Failover)**: Pair your phone via **LAN Direct QR** (`:5417`), **Cloudflare Tunnel Connector** (`:5418`), or **[GoPanel](https://github.com/hoxiai/gopanel) Reverse Tunnel + iOS APNs Approval Push** (push payloads carry pending counts only—never raw commands, code, or file paths).
  - **Tool-Call Intent Routing & Mandatory Confirmation Cards**: Query live coding progress on your Hoxi desktop or server metrics on GoPanel, or dispatch commands to run tests, inspect/commit Git worktree changes, approve PreToolUse safety gates, restart Docker containers, and renew SSL certificates. Every mutating action requires a **2-minute, single-use confirmation card** backed by `X-Idempotency-Key` and `instructionId` deduplication.
  - **Native Voice Pipeline & Barge-In (manual start; on-device acceptance pending)**: Built-in iOS `.voiceChat` hardware AEC, 50s ASR rollover, and **Speak or Tap to Interrupt (`onUserBargeIn`)** over native ASR + gateway `/v1/audio/speech` TTS (with automatic system speech fallback), plus remote Node Skill discovery and mounting.

### 5. Context Rot, Endpoint Degradation & Electron Bloat
Long sessions forget early architectural constraints; third-party API gateways silently degrade or stall; Electron forks eat 1.5 GB+ RAM per window.
- **8-Dimension Persistent Memory**: Automatically compacts long sessions into `.hoxi/memory/` across 8 structured sections (*Goals, Constraints, Key Facts, Changed Files, Errors & Fixes, Todo, Progress, Next Steps*).
- **Smart Auto-Router (`hoxi-auto`) & Local OpenAI-Compatible Relay**: Tracks live TTFT EWMA and failure categories (`transient` / `rate_limit` / `quota` / `config`) per `provider/model`, automatically circuit-breaking unhealthy or >3× slower endpoints while keeping session-sticky routing to preserve upstream prompt caches. Exposes loopback `GET /v1/models` and `POST /v1/chat/completions` (`hk-*` SHA-256 hashed keys) so local tools (Cline, Continue, Cherry Studio, Open WebUI, aider) can share your configured providers without exposing upstream API keys.
- **Role-Based Model Routing & Observer Audits**: Use frontier models for primary coding while routing title generation, background subagents, memory compaction, and Git commit messages to fast/cheap or local models (Ollama / vLLM)—plus optional post-edit diff scoring via an independent **Observer** referee model.
- **Native Wails v2 + SolidJS 120Hz Shell**: Built on macOS `WKWebView` + fine-grained SolidJS signals (`requestAnimationFrame` token batching, zero VDOM) + WebGL Xterm.

### 6. Agents That Leave the Chat
Most AI coding tools stop at the repository. Hoxi also lets a session act on native apps, fan one goal out into parallel projects, and review what lands upstream.
- **Native Computer Control (`hoxi_computer`)**: An OpenCode session can read a window and drive foreground input on macOS (Accessibility / ScreenCaptureKit / CGEvent) and Windows (UI Automation / PrintWindow / SendInput). Authorization is bound to one session plus one running app and confirmed in a native dialog; every input needs a one-time local approval (never remote, never "always allow", never unattended). Screenshots capture only the granted window, exclude password fields, and the computer-control routes are closed to the tunnel, LAN and relay.
- **Multi-Agent DAG Orchestration**: Describe a goal once and Hoxi drafts a task DAG (a "convoy") where every task runs as its own session—one isolated worktree per task in a Git repo—so dependent tasks start from their prerequisites' merged changes while independent ones run in parallel.
- **GitHub PR & CI Review (`forge`)**: Read live PR status, checks and review threads through the `gh` CLI without leaving the workspace.

**Also built in**: symbol/chunk code index with BM25 search; Agent Skills (local discovery, built-in defaults, SHA-256-verified installs from the hoxi.ai marketplace); the Hoxi Tools gateway (220+ provider endpoints via discover/inspect/run); one-click official Hoxi account (AINode) provider and MCP OAuth 2.1 PKCE connect; multi-workspace profiles; `/goal` session goals; multimodal attachments; prompt enhancement; a model leaderboard; and a crash-safe reclaim ledger that cleans up orphaned `opencode`/Chrome processes after a crash or `kill -9`. Local-first by design: the only telemetry is a launch beacon (event, install id, os/arch, version—never prompts, code, paths or keys), and the OpenAI-compatible relay is loopback-only.

---

## Architecture at a Glance

```text
┌─────────────────────────────────────────────────────────────────────────┐
│ Native Host & Mobile Client (Wails v2 + NSStatusBar + Flutter App)      │
│  • macOS WKWebView (typically ~30MB RAM, ~0.2s cold start, vibrancy)    │
│  • Menu bar tray with live Running count & Attention approval badges    │
│  • Mobile App (`client/`): LAN & Relay control for Hoxi & GoPanel       │
├─────────────────────────────────────────────────────────────────────────┤
│ 3-Pane SolidJS Workspace (Zero-VDOM, 120Hz rAF Batched Streaming)       │
│  • Left: Projects, Worktree Sandboxes, Multi-Runtime Sessions           │
│  • Center: Unified Timeline, Subagent Trees, ThoughtBox, Steer Composer │
│  • Right: Pierre Diffs, 8-Dim Memory, Files, WebGL Xterm, Chrome View   │
├─────────────────────────────────────────────────────────────────────────┤
│ Go Supervisor, Loopback Gateway & Session Store (`internal/`)           │
│  • Hoxi-owned sessions (`~/.hoxi/sessions/<id>/session.json` + JSONL)   │
│  • Git Worktree sandboxes, Seatbelt confinement & unified Safety Gate   │
│  • 8-CLI  Session Handoff, loopback OpenAI relay & health circuit-breaker│
│  • Loopback proxy (`Proxy: nil`, `FlushInterval: -1`, per-boot auth)    │
├─────────────────────────────────────────────────────────────────────────┤
│ Pluggable Execution Runtimes (`internal/runner` & `internal/opencode`)  │
│  • OpenCode (`opencode serve` pristine upstream binary, zero-fork)      │
│  • Claude Code (`claude`), Antigravity (`agy`), Gemini CLI (`gemini`)   │
│  • Qoder, CodeBuddy CLIs (read-only Plan mode)                          │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Quick Start

### 1. Direct Install (Recommended for Users)
- **macOS (Apple Silicon `arm64`, `>= 13.0`)**: Download `Hoxi-<version>-darwin-arm64.dmg` from [GitHub Releases](https://github.com/hoxiai/hoxi/releases) and drag `Hoxi.app` into `Applications`.
- **Windows 10 / 11 (64-bit `x64`)**:
  - **Graphical Setup Installer**: Download [`Hoxi-<version>-windows-amd64-setup.exe`](https://github.com/hoxiai/hoxi/releases) and double-click to install (installs per-user into `%LOCALAPPDATA%\Programs\Hoxi` with bundled `opencode.exe`, WebView2 Runtime bootstrapper, Desktop/Start Menu shortcuts, `hoxi://` login protocol, and in-app self-update support).
  - **One-Line PowerShell Install**:
    ```powershell
    irm https://raw.githubusercontent.com/hoxiai/hoxi/main/scripts/install.ps1 | iex
    ```
  - The OS command sandbox and unattended approval mode are currently unavailable on Windows. Release builds bundle SQLite for database-backed features; source builds can use `sqlite3.exe` on `PATH`.

### 2. Prerequisites (Building from Source)
- **macOS** `>= 13.0` or **Windows 10/11** (`x64`)
- **Go** `>= 1.23`
- **Node.js** `>= 20` (or **Bun** `>= 1.2`)
- **Wails CLI v2** (`go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0`)
- **Agent engine**: release builds bundle [`opencode`](https://github.com/anomalyco/opencode), and development runs auto-download the pinned, SHA-256-verified binary into `~/.hoxi/bin` when missing. Alternatively install `opencode`, `claude`, `agy`, `gemini`, `qoder`, or `codebuddy` on `PATH`

### Run in Development
```bash
# Desktop app with hot-reload
wails dev

# Headless daemon / browser mode
go run ./cmd/hoxi --no-desktop
```

### Build Production Desktop App & Installer
```bash
# macOS (build/bin/Hoxi.app & Hoxi.dmg)
./build-desktop.sh

# Windows PowerShell (build/bin/Hoxi-<version>-windows-amd64-setup.exe & .zip)
.\scripts\build-windows.ps1
```

### Verify & Test
```bash
go build ./... && go test ./internal/...
npx --prefix web tsc -p web/tsconfig.json --noEmit
npm --prefix web run check:i18n && npm --prefix web run check:lines
npm --prefix web test && npm --prefix web run build
```

---

## Documentation & Ecosystem

- **[README.zh-CN.md](README.zh-CN.md)**: 简体中文文档（针对国内模型生态、中转站防降智测评、多工具接力与 GoPanel 手机运维）
- **[GoPanel (`hoxiai/gopanel`)](https://github.com/hoxiai/gopanel)**: Lightweight server ops panel & remote AI coding node managed by Hoxi Mobile
- **[client/README.md](client/README.md)**: Hoxi Mobile App (Flutter, iOS/Android — AI Command Center & GoPanel Server Manager)
- **[DEVELOPMENT.md](DEVELOPMENT.md)**: Deep-dive architecture, OpenAPI/SSE contracts, Worktree state machines, and plugin hooks
- **[HANDOFF_AND_CLI_BRIDGE.md](HANDOFF_AND_CLI_BRIDGE.md)**: Cross-CLI session handoff IR, local credential discovery, and protocol bridges
- **[BROWSER.md](BROWSER.md)**: Isolated Headless Chrome inspector, manual takeover, and security boundaries
- **[docs/releases.md](docs/releases.md)** & **[docs/auto-updates.md](docs/auto-updates.md)**: DMG notarization, Sparkle EdDSA appcast, and release pipeline
- **[CONTRIBUTING.md](CONTRIBUTING.md)**: Local workflow, required gates and pull-request rules
- **[SECURITY.md](SECURITY.md)**: Vulnerability reporting and the local security model
- **[docs/telemetry.md](docs/telemetry.md)**: Exactly what the launch beacon sends and how to opt out
- **[AGENTS.md](AGENTS.md)**: Engineering constraints and module boundaries

---

## License

Apache-2.0 © [Hoxi (hoxi.ai)](https://hoxi.ai)

Third-party notices: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)
