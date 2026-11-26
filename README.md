<p align="center">
  <a href="https://hoxi.ai"><img src="docs/assets/hoxi-banner.svg" alt="Hoxi" width="310" /></a>
</p>

<p align="center">
  Native macOS &amp; Windows command center for <b>parallel AI software engineering</b>, with built-in remote channels for the optional mobile companion &amp; <b><a href="https://github.com/hoxiai/gopanel">GoPanel</a> servers</b>.
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

**Hoxi** ([hoxi.ai](https://hoxi.ai)) ships with a **built-in Go Native Agent Engine** (zero external sidecar binaries required) and unifies **Claude Code**, **OpenAI Codex CLI**, **OpenCode**, **Google Antigravity (`agy`)**, **Gemini CLI (`gemini`)**, **Qoder**, and **CodeBuddy** inside a native macOS & Windows workspace (typically **~30 MB** RAM, **~0.2s** cold start, and **~53 MB** unpacked app bundle)—while exposing E2EE remote channels so the optional Hoxi mobile companion app and [**GoPanel**](https://github.com/hoxiai/gopanel) Linux servers can supervise running AI agents on the go.

---

## Why Hoxi Exists (The Pain Points We Solve)

### 1. Parallel Agents Clobbering Your Working Tree
Running two agents at once in the same repo overwrites uncommitted files and breaks builds. Comparing how Claude, GPT, or Gemini tackles a tricky refactor usually means manual `git stash` gymnastics.
- **Per-Session Git Worktree Sandboxes**: Each session can run in an isolated `.hoxi/worktrees/<session-id>` branch or directly in your local checkout (toggle anytime when idle).
- **Multi-Model Worktree Arena**: Dispatch the same prompt to **2–4 models concurrently**, each in its own worktree. Review hunk-level diffs side-by-side (`@pierre/diffs`), pick the winner, and merge in one click.
- **Time-Travel Revert & Fork**: Roll back both conversation history and disk files to any message or tool call (`/revert` & `/unrevert`), or `/fork` a session mid-flight to explore an alternative architecture.

### 2. Rate Limits, CLI Silos & Lost Context
When Claude Code hits a usage cap mid-refactor, or an agent loops on a bug, switching tools usually means re-explaining your entire task from scratch.
- **Multi-Runtime Unified Timeline**: Run out of the box on Hoxi's in-process **Go Native Agent Engine** (`native`, zero external CLI or sidecar required), or orchestrate **Claude Code** (`claude`), **OpenAI Codex CLI** (`codex`), **OpenCode** (`opencode`), **Google Antigravity** (`agy`), **Gemini CLI** (`gemini`), **Qoder**, and **CodeBuddy** natively inside the same 3-pane shell. Switch runtimes mid-session without losing your thread. Qoder and CodeBuddy run read-only (Plan mode) only—their CLIs have no PreToolUse approval gate.
- **Nested Subagent Inspection**: Bounded parallel subagents spawned by the Hoxi Native Engine, Claude Code, or Antigravity render as interactive, collapsible task trees instead of opaque stdout dumps.
- **1-Click Cross-CLI Session Handoff**: Scans local histories from **Claude Code**, **OpenAI Codex CLI**, **Grok CLI**, **Antigravity / Gemini CLI**, **OpenCode**, **Trae**, **Qoder**, and **CodeBuddy**. Imports every turn, reasoning block, and normalized tool call, and synthesizes an **8-dimension handoff memory** so the next model continues seamlessly.

### 3. Blind Auto-Approve vs. Terminal Babysitting
Giving agents blanket shell access risks `rm -rf` or `git push --force`. Approving every file read chains you to your desk. And when an agent drifts off-track, aborting wastes the whole turn.
- **Unified PreToolUse Safety Gate + Seatbelt OS Sandbox + Unattended Mode**: Safe reads and edits flow uninterrupted. Destructive commands (`rm`, `sudo`, `chmod`, `diskutil`, `git reset --hard`, `git push --force`) are intercepted across the Hoxi Native Engine, Claude Code, Codex, OpenCode, Antigravity, and Gemini CLI—triggering a macOS `NSStatusBar` / Windows `Shell_NotifyIconW` **Attention** badge and an inline approval card (`Once` / `Always` / `Reject`). In **Unattended Mode** (`unattended`), sessions run inside an isolated Git worktree and macOS Seatbelt OS-level sandbox with automatic credential env stripping, sensitive path blocking (`~/.ssh`, `~/.aws`, `.env`), and hard rejection of dynamic shell evaluation, privilege escalation, and `git push`.
- **Live Mid-Stream Steering (`Steer`, Hoxi Native & OpenCode sessions)**: Inject course corrections while an agent is actively executing without killing the turn.
- **Desktop Browser Window, Isolated Headless Chrome & Chrome Link Extension**: Opens a visible desktop Chrome window backed by a persistent `~/.hoxi/browser-profile` by default (with isolated headless Chrome as an opt-in mode) and one-click **Manual Control** for login walls/CAPTCHAs, plus the Chrome Link MV3 extension (`extensions/chrome`, 2-minute one-time pairing code with persistent `linkToken` auto-reconnect and multi-tab/popup support) to attach to tabs in your existing Chrome browser.

### 4. Stepping Away from Your Desk (Mobile AI + [GoPanel](https://github.com/hoxiai/gopanel) Server Ops)
Solo builders and lean teams don't just write code—they also deploy and maintain servers. Being away from your laptop shouldn't stall a long-running agent or leave a production alert unanswered.
- **Resilient Multi-Transport Remote Control (LAN Direct, Cloudflare Relay & GoPanel Tunnel)**:
  - **3-Path Connectivity + 1-RTT X25519 E2EE**: Pair the open-source Flutter mobile controller (`mobile/`) via **LAN Direct QR** (`:5417`), **Cloudflare Tunnel Connector** (`:5418`), or **[GoPanel](https://github.com/hoxiai/gopanel) Reverse Tunnel** (with optional APNs pending-count notifications on GoPanel), protected by `hoxi-e2ee-v1` zero-knowledge directional AES-256-GCM + X25519 key exchange (push payloads carry pending counts only—never raw commands, code, or file paths).
  - **Remote Action Safety & Idempotency**: Query live coding progress on your Hoxi desktop or server metrics on GoPanel, or dispatch commands to run tests, inspect Git worktree changes, and approve PreToolUse safety gates with `X-Idempotency-Key` and `instructionId` deduplication.

### 5. Context Rot, Endpoint Degradation & Electron Bloat
Long sessions forget early architectural constraints; third-party API gateways silently degrade or stall; Electron forks eat 1.5 GB+ RAM per window.
- **8-Dimension Persistent Memory**: Automatically compacts long sessions into `<project>/.hoxi/memory/project.md` and `<project>/.hoxi/memory/sessions/<session>.md` across 8 structured sections (*Goals, Constraints, Key Facts, Changed Files, Errors & Fixes, Todo, Progress, Next Steps*).
- **Smart Auto-Router (`hoxi-auto`) & Local OpenAI-Compatible Relay**: Tracks live TTFT EWMA and failure categories (`transient` / `rate_limit` / `quota` / `config`) per `provider/model`, automatically circuit-breaking unhealthy or >3× slower endpoints while keeping session-sticky routing to preserve upstream prompt caches. Exposes loopback `GET /v1/models` and `POST /v1/chat/completions` (`hk-*` SHA-256 hashed keys) so local tools (Cline, Continue, Cherry Studio, Open WebUI, aider) can share your configured providers without exposing upstream API keys.
- **Role-Based Model Routing & Observer Audits**: Use frontier models for primary coding while routing title generation, background subagents, memory compaction, and Git commit messages to fast/cheap or local models (Ollama / vLLM)—plus optional post-edit diff scoring via an independent **Observer** referee model.
- **Native Wails v2 + SolidJS 120Hz Shell**: Built on macOS `WKWebView` / Windows `WebView2` + fine-grained SolidJS signals (`requestAnimationFrame` token batching, zero VDOM) + WebGL Xterm.

### 6. Agents That Leave the Chat
Most AI coding tools stop at the repository. Hoxi also lets a session act on native apps, fan one goal out into parallel projects, and review what lands upstream.
- **Native Computer Control (`hoxi_computer`)**: Hoxi Native Engine and OpenCode sessions can read a window and drive foreground input on macOS (Accessibility / ScreenCaptureKit / CGEvent) and Windows (UI Automation / PrintWindow / SendInput). Authorization is bound to one session plus one running app and confirmed in a native dialog; input defaults to a one-time local confirmation per action (with an optional session-scoped continuous-input toggle in the Computer Control panel for the currently authorized app; never remote, never cross-session "always allow", never unattended). Screenshots capture only the granted window, exclude password fields, and the computer-control routes are closed to the tunnel, LAN and relay.
- **Multi-Agent DAG Orchestration**: Describe a goal once and Hoxi drafts a task DAG (a "convoy") where every task runs as its own session—one isolated worktree per task in a Git repo—so dependent tasks start from their prerequisites' merged changes while independent ones run in parallel.
- **GitHub PR & CI Review (`forge`)**: Read live PR status, checks and review threads through the `gh` CLI without leaving the workspace.

**Also built in**: embedded pure-Go SQLite (`internal/sqlitecli`, zero external `sqlite3` binary required); symbol/chunk code index with BM25 search; Agent Skills (local discovery, built-in defaults, SHA-256-verified installs from the hoxi.ai marketplace); the Hoxi Tools gateway (220+ provider endpoints via discover/inspect/run); optional one-click official Hoxi account provider and MCP OAuth 2.1 PKCE connect; multi-workspace profiles; `/goal` session goals; multimodal attachments; prompt enhancement; and a crash-safe reclaim ledger that cleans up orphaned child CLI/Chrome processes after a crash or `kill -9`. Local-first by design: the only telemetry is a launch beacon (event, install id, os/arch, version—never prompts, code, paths or keys), and the OpenAI-compatible relay is loopback-only.

---

## Architecture at a Glance

```text
┌─────────────────────────────────────────────────────────────────────────┐
│ Native Host & Remote Access (Wails v2 + Tray + E2EE LAN/Relay Channels) │
│  • macOS WKWebView & Windows WebView2 (~30MB RAM, ~0.2s cold start)     │
│  • Native tray (`NSStatusBar` / `Shell_NotifyIconW`) Running/Attention  │
│  • Remote Channels: 1-RTT X25519 E2EE over LAN / Relay / GoPanel tunnel │
├─────────────────────────────────────────────────────────────────────────┤
│ 3-Pane SolidJS Workspace (Zero-VDOM, 120Hz rAF Batched Streaming)       │
│  • Left: Projects, Worktree Sandboxes, Multi-Runtime Sessions           │
│  • Center: Unified Timeline, Subagent Trees, ThoughtBox, Steer Composer │
│  • Right: Pierre Diffs, 8-Dim Memory, Files, WebGL Xterm, Chrome & Apps │
├─────────────────────────────────────────────────────────────────────────┤
│ Go Supervisor, Loopback Gateway & Session Store (`internal/`)           │
│  • Hoxi-owned sessions (`~/.hoxi/sessions/<id>/session.json` + JSONL)   │
│  • Git Worktree sandboxes, Seatbelt confinement & unified Safety Gate   │
│  • Native Computer Control (`internal/computer`) & Browser (`browser/`) │
│  • 8-CLI Session Handoff, loopback OpenAI relay & health circuit-breaker│
│  • Embedded pure-Go SQLite (`sqlitecli`) & proxy-immune loopback gateway│
├─────────────────────────────────────────────────────────────────────────┤
│ Native Kernel & Pluggable Runtimes (`internal/agent` & `internal/runner`)│
│  • Hoxi Native Engine (built-in Go agent loop, zero external sidecars)  │
│  • On-demand CLIs: Claude Code, Codex, OpenCode, Antigravity, Gemini CLI│
│  • Qoder (`qoder`), CodeBuddy (`codebuddy`) CLIs (read-only Plan mode)  │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Quick Start

> **Local-First, Zero Mandatory Login & Silent Offline Mode**: Hoxi runs 100% locally with its built-in Go Native Agent Engine and your own API keys (Anthropic, OpenAI, Gemini, DeepSeek, Ollama/vLLM, or any OpenAI-compatible endpoint)—plus optional local CLIs (`claude`, `codex`, `opencode`, `gemini`, `agy`)—so registering a `hoxi.ai` account is completely optional. Anonymous launch telemetry is **off by default** in source and development builds, and **Silent offline mode** (**Settings → Network & Privacy** or `HOXI_OFFLINE=1`) silences all background `hoxi.ai` update/catalog sync requests at once (see [docs/telemetry.md](docs/telemetry.md)).

### 1. Direct Install (Recommended for Users)
- **macOS (Apple Silicon `arm64`, `>= 13.0`) & Linux (`x86_64` Desktop / Headless Host)**:
  - **macOS DMG**: Download `Hoxi-<version>-darwin-arm64.dmg` from [GitHub Releases](https://github.com/hoxiai/hoxi/releases) and drag `Hoxi.app` into `Applications`. Register the `hoxi` terminal command in one click under **Settings → Runtime & Updates → Command-Line Tool (`hoxi`)** (or run `/Applications/Hoxi.app/Contents/MacOS/Hoxi install-cli`).
  - **macOS & Linux CLI Installer ([`scripts/install.sh`](scripts/install.sh))**: Verifies official SHA-256 checksums and macOS code signatures, deploys Linux desktop entries or headless hosts, and links `hoxi` on your terminal path (supports `--version`, `--mirror <prefix>`, `--proxy`, `--uninstall`, and `--purge`).
- **Windows 10 / 11 (64-bit `x64`)**:
  - **Graphical Setup Installer**: Download [`Hoxi-<version>-windows-amd64-setup.exe`](https://github.com/hoxiai/hoxi/releases) and double-click to install (installs per-user into `%LOCALAPPDATA%\Programs\Hoxi` with the built-in Go Native Agent Engine and embedded pure-Go SQLite, WebView2 Runtime bootstrapper, Desktop/Start Menu shortcuts, `hoxi://` login protocol, and in-app self-update support).
  - **One-Line PowerShell Install**:
    ```powershell
    irm https://raw.githubusercontent.com/hoxiai/hoxi/main/scripts/install.ps1 | iex
    ```
  - The OS command sandbox and unattended approval mode are currently unavailable on Windows. Both release and source builds embed pure-Go SQLite (`internal/sqlitecli`) out of the box—no external `sqlite3` binary is required.

### 2. Prerequisites (Building from Source)
- **macOS** `>= 13.0` or **Windows 10/11** (`x64`)
- **Go** `>= 1.25`
- **Node.js** `>= 20` (or **Bun** `>= 1.2`)
- **Wails CLI v2** (`go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0`)
- **Agent engine**: Hoxi ships with a **built-in Go Native Agent Engine** out of the box—no external CLI binary or sidecar download is required. Optionally install `claude`, `codex`, `opencode`, `agy`, `gemini`, `qoder`, or `codebuddy` on `PATH` to use external CLI runtimes alongside the native engine

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

### Mobile Companion (`mobile/`, Optional)
Pre-built signed Android APKs for the lightweight open-source **`Hoxi Remote`** controller (`ai.hoxi.remote`, zero-account / zero-telemetry, Obtainium compatible) are attached to every [GitHub Release](https://github.com/hoxiai/hoxi/releases) (`Hoxi-Remote-<version>-android.apk`) and coexist side-by-side with the full **`Hoxi`** mobile app (`ai.hoxi.app` at [hoxi.ai](https://hoxi.ai)). To run or build from source:
```bash
cd mobile
flutter pub get && flutter gen-l10n
flutter analyze && flutter test
flutter run
```

---

## Documentation & Ecosystem

- **[README.zh-CN.md](README.zh-CN.md)**: 简体中文文档（针对国内模型生态、中转站防降智测评、多工具接力与 GoPanel 手机运维）
- **[GoPanel (`hoxiai/gopanel`)](https://github.com/hoxiai/gopanel)**: Lightweight server ops panel & remote AI coding node (supports reverse tunnel integration with Hoxi)
- **[DEVELOPMENT.md](DEVELOPMENT.md)**: Deep-dive architecture, OpenAPI/SSE contracts, Worktree state machines, and plugin hooks
- **[HANDOFF_AND_CLI_BRIDGE.md](HANDOFF_AND_CLI_BRIDGE.md)**: Cross-CLI session handoff IR, local credential discovery, and protocol bridges
- **[COMPUTER.md](COMPUTER.md)**: Native macOS & Windows computer control (`hoxi_computer`), per-app authorization, and validation boundaries
- **[BROWSER.md](BROWSER.md)**: Desktop Window & Headless Chrome inspector, Chrome Link extension, manual takeover, and security boundaries
- **[docs/releases.md](docs/releases.md)** & **[docs/auto-updates.md](docs/auto-updates.md)**: DMG notarization, Sparkle EdDSA appcast, and release pipeline
- **[CONTRIBUTING.md](CONTRIBUTING.md)**: Local workflow, required gates and pull-request rules
- **[SECURITY.md](SECURITY.md)**: Vulnerability reporting and the local security model
- **[docs/telemetry.md](docs/telemetry.md)**: Exactly what the launch beacon sends and how to opt out
- **[AGENTS.md](AGENTS.md)**: Engineering constraints and module boundaries

---

## Open-Core & Official Releases

This repository (`hoxiai/hoxi`) is the **upstream Apache-2.0 development repository** for Hoxi's core desktop/CLI workspace (`Code` mode) and the open-source Flutter mobile controller (`mobile/`, **`Hoxi Remote`**). Daily host development, bug fixes, and community pull requests land directly on `main`.

- **Building from source** produces the complete, self-contained open-source `Code` mode desktop app and CLI (`go build` / `wails build` / `./build-desktop.sh`) with zero proprietary dependencies.
- **Official signed installers** published to [GitHub Releases](https://github.com/hoxiai/hoxi/releases) and [hoxi.ai](https://hoxi.ai) are built from the tagged commit in this repository together with optional commercial extensions (such as the `Work` workspace surface) and signed/notarized in a private release pipeline.

---

## License

Apache-2.0 © [Hoxi (hoxi.ai)](https://hoxi.ai)

Third-party notices: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)
