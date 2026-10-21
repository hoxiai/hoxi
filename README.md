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

**Hoxi** ([hoxi.ai](https://hoxi.ai)) unifies **OpenCode**, **Claude Code**, **Google Antigravity (`agy`)**, **Qoder**, and **CodeBuddy** inside a **~30 MB**, **0.2s cold-start** native macOS workspace—and extends to your phone (`client/`) to command running AI agents and manage [**GoPanel**](https://github.com/hoxiai/gopanel) Linux servers on the go.

---

## Why Hoxi Exists (The Pain Points We Solve)

### 1. Parallel Agents Clobbering Your Working Tree
Running two agents at once in the same repo overwrites uncommitted files and breaks builds. Comparing how Claude, GPT, or Gemini tackles a tricky refactor usually means manual `git stash` gymnastics.
- **Per-Session Git Worktree Sandboxes**: Each session can run in an isolated `.hoxi/worktrees/<session-id>` branch or directly in your local checkout (toggle anytime when idle).
- **Multi-Model Worktree Arena**: Dispatch the same prompt to **2–4 models concurrently**, each in its own worktree. Review hunk-level diffs side-by-side (`@pierre/diffs`), pick the winner, and merge in one click.
- **Time-Travel Revert & Fork**: Roll back both conversation history and disk files to any message or tool call (`/revert` & `/unrevert`), or `/fork` a session mid-flight to explore an alternative architecture.

### 2. Rate Limits, CLI Silos & Lost Context
When Claude Code hits a usage cap mid-refactor, or an agent loops on a bug, switching tools usually means re-explaining your entire task from scratch.
- **Multi-Runtime Unified Timeline**: Run **OpenCode** (supervised `opencode serve`, zero-fork), **Claude Code** (`claude`), **Google Antigravity** (`agy`), **Qoder**, and **CodeBuddy** natively inside the same 3-pane shell. Switch runtimes mid-session without losing your thread.
- **Nested Subagent Inspection**: Subagents spawned by Claude Code or Antigravity render as interactive, collapsible task trees (`AgentStackPill`) instead of opaque stdout dumps.
- **1-Click Cross-CLI Session Handoff**: Scans local histories from **Claude Code**, **OpenAI Codex CLI**, **Grok CLI**, **Antigravity (Gemini)**, **OpenCode**, **Trae**, **Qoder**, and **CodeBuddy**. Imports every turn, reasoning block, and normalized tool call, and synthesizes an **8-dimension handoff memory** so the next model continues seamlessly.

### 3. Blind Auto-Approve vs. Terminal Babysitting
Giving agents blanket shell access risks `rm -rf` or `git push --force`. Approving every file read chains you to your desk. And when an agent drifts off-track, aborting wastes the whole turn.
- **Unified PreToolUse Safety Gate**: Safe reads and edits flow uninterrupted. Destructive commands (`rm`, `sudo`, `chmod`, `diskutil`, `git reset --hard`, `git push --force`) are intercepted across OpenCode, Claude Code, and Antigravity—triggering a macOS `NSStatusBar` **Attention** badge and an inline approval card (`Once` / `Always` / `Reject`).
- **Live Mid-Stream Steering (`Steer`)**: Inject course corrections while an agent is actively executing without killing the turn.
- **Isolated Headless Chrome + Manual Takeover**: Per-session Chrome inspector for UI verification. When the agent hits a login wall or CAPTCHA, flip **Manual Control** on, authenticate directly in the viewport, and hand control back.

### 4. Stepping Away from Your Desk (Mobile AI + [GoPanel](https://github.com/hoxiai/gopanel) Server Ops)
Solo builders and lean teams don't just write code—they also deploy and maintain servers. Being away from your laptop shouldn't stall a long-running agent or leave a production alert unanswered.
- **Mobile AI Command Center ([`client/`](client/README.md))**: Connect your iOS/Android phone to your Hoxi desktop or remote nodes. Watch live SSE streams, review Git worktree diffs, steer active agents, and approve/reject pending permission gates in one tap.
- **Mobile "Chat & Voice as the OS" Super-Assistant ([`client/`](client/README.md) — `Chat · Engineering · Column · Me`)**:
  - **1:1 Lingguang Full-Duplex Realtime Voice Call**: Continuous 30+ minute hands-free voice calls with native `.voiceChat` Hardware AEC, 50s seamless ASR rollover, **Speak or Tap to Interrupt (`onUserBargeIn`)**, and native `Qwen-Omni-Turbo-Realtime` / `GPT-4o-Mini-Realtime` WebSocket 24kHz PCM16 streaming.
  - **Voice/Chat Business Intelligence & Direct Node Control**: Ask in natural voice for live briefings across **Hoxi Desktop**, **[GoPanel](https://github.com/hoxiai/gopanel)**, **Qingpu AI (`qingpu.ai`)**, **Shoply (`iqingpu.com`)**, and **hoxi.ai**, or speak commands to switch between your **Hoxi Local Computer** and **GoPanel Server**, run unit tests, inspect/commit Git worktree changes, approve PreToolUse safety gates, restart Docker containers, and renew SSL certificates directly inside the conversation.

### 5. Context Rot, Endpoint Degradation & Electron Bloat
Long sessions forget early architectural constraints; third-party API gateways silently degrade or stall; Electron forks eat 1.5 GB+ RAM per window.
- **8-Dimension Persistent Memory**: Automatically compacts long sessions into `.opencode/memory/` across 8 structured sections (*Goals, Constraints, Key Facts, Changed Files, Errors & Fixes, Todo, Progress, Next Steps*).
- **Role-Based Model Routing**: Use frontier models for primary coding while routing title generation, background subagents, memory compaction, and Git commit messages to fast/cheap or local models (Ollama / vLLM)—or 0ms local rule synthesis.
- **Provider Telemetry & Observer Audits**: Tracks live TTFT, timeout rates, tool-call accuracy, and hallucination loops per endpoint, with optional automated post-edit diff scoring via an independent **Observer** referee model.
- **Native Wails v2 + SolidJS 120Hz Shell**: Built on macOS `WKWebView` + fine-grained SolidJS signals (`requestAnimationFrame` token batching, zero VDOM) + WebGL Xterm.

---

## Architecture at a Glance

```text
┌─────────────────────────────────────────────────────────────────────────┐
│ Native Host & Mobile Client (Wails v2 + NSStatusBar + Flutter App)      │
│  • macOS WKWebView (~30MB RAM, 0.2s cold start, native vibrancy)        │
│  • Menu bar tray with live Running count & Attention approval badges    │
│  • Mobile App (`client/`): Dual-node control for Hoxi & GoPanel servers │
├─────────────────────────────────────────────────────────────────────────┤
│ 3-Pane SolidJS Workspace (Zero-VDOM, 120Hz rAF Batched Streaming)       │
│  • Left: Projects, Worktree Sandboxes, Multi-Runtime Sessions           │
│  • Center: Unified Timeline, Subagent Trees, ThoughtBox, Steer Composer │
│  • Right: Pierre Diffs, 8-Dim Memory, Files, WebGL Xterm, Chrome View   │
├─────────────────────────────────────────────────────────────────────────┤
│ Go Supervisor, Loopback Gateway & Session Store (`internal/`)           │
│  • Hoxi-owned sessions (`~/.hoxi/sessions/<id>/session.json` + JSONL)   │
│  • Git Worktree sandbox manager & snapshot rollback (`internal/worktree`)│
│  • 8-tool Session Handoff & local CLI bridge (`internal/handoff`)       │
│  • Loopback proxy (`Proxy: nil`, `FlushInterval: -1`, per-boot auth)    │
├─────────────────────────────────────────────────────────────────────────┤
│ Pluggable Execution Runtimes (`internal/runner` & `internal/opencode`)  │
│  • OpenCode (`opencode serve` pristine upstream binary, zero-fork)      │
│  • Claude Code (`claude`), Antigravity (`agy`), Qoder, CodeBuddy CLIs   │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Quick Start

### Prerequisites
- **macOS** `>= 13.0` (Apple Silicon `arm64` primary release target)
- **Go** `>= 1.23`
- **Node.js** `>= 20` (or **Bun** `>= 1.2`)
- **Wails CLI v2** (`go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0`)
- **Agent engine**: release builds bundle [`opencode`](https://github.com/anomalyco/opencode), and development runs auto-download the pinned, SHA-256-verified binary into `~/.hoxi/bin` when missing. Alternatively install `opencode`, `claude`, `agy`, `qoder`, or `codebuddy` on `PATH`

### Run in Development
```bash
# Desktop app with hot-reload
wails dev

# Headless daemon / browser mode
go run ./cmd/hoxi --no-desktop
```

### Build Production macOS App (`build/bin/Hoxi.app`)
```bash
./build-desktop.sh
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
