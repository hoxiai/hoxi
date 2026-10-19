# Hoxi

[English](README.md) | [简体中文](README.zh-CN.md)

**Hoxi** ([hoxi.ai](https://hoxi.ai)) is a native macOS command center for parallel AI software engineering.

Run **OpenCode**, **Claude Code**, **Google Antigravity (`agy`)**, **Qoder**, and **CodeBuddy** in isolated Git worktrees, hand off stuck or rate-limited sessions across 8 local CLIs without losing context, and control running agents from your Mac or phone—at **~30 MB RAM** and **0.2s cold start**.

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
- **Mobile Command Center (`client/`)**: Flutter companion app (iOS/Android) to monitor live streams, inspect diffs, steer tasks, and approve permission gates away from your desk.

### 4. Context Rot, Endpoint Degradation & Electron Bloat
Long sessions forget early architectural constraints; third-party API gateways silently degrade or stall; Electron forks eat 1.5 GB+ RAM per window.
- **8-Dimension Persistent Memory**: Automatically compacts long sessions into `.opencode/memory/` across 8 structured sections (*Goals, Constraints, Key Facts, Changed Files, Errors & Fixes, Todo, Progress, Next Steps*).
- **Role-Based Model Routing**: Use frontier models for primary coding while routing title generation, background subagents, memory compaction, and Git commit messages to fast/cheap or local models (Ollama / vLLM)—or 0ms local rule synthesis.
- **Provider Telemetry & Observer Audits**: Tracks live TTFT, timeout rates, tool-call accuracy, and hallucination loops per endpoint, with optional automated post-edit diff scoring via an independent **Observer** referee model.
- **Native Wails v2 + SolidJS 120Hz Shell**: Built on macOS `WKWebView` + fine-grained SolidJS signals (`requestAnimationFrame` token batching, zero VDOM) + WebGL Xterm.

---

## Architecture at a Glance

```text
┌─────────────────────────────────────────────────────────────────────────┐
│ Native Host (Wails v2 + Objective-C NSStatusBar + Mobile Companion)     │
│  • macOS WKWebView (~30MB RAM, 0.2s cold start, native vibrancy)        │
│  • Menu bar tray with live Running count & Attention approval badges    │
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
- At least one supported agent CLI installed on `PATH`: [`opencode`](https://github.com/anomalyco/opencode), `claude`, `agy`, `qoder`, or `codebuddy`

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

## Documentation Map

- **[README.zh-CN.md](README.zh-CN.md)**: 简体中文文档（针对国内模型生态、中转站防降智测评与多工具接力场景）
- **[DEVELOPMENT.md](DEVELOPMENT.md)**: Deep-dive architecture, OpenAPI/SSE contracts, Worktree state machines, and plugin hooks
- **[HANDOFF_AND_CLI_BRIDGE.md](HANDOFF_AND_CLI_BRIDGE.md)**: Cross-CLI session handoff IR, local credential discovery, and protocol bridges
- **[BROWSER.md](BROWSER.md)**: Isolated Headless Chrome inspector, manual takeover, and security boundaries
- **[client/README.md](client/README.md)**: Hoxi Mobile Companion App (Flutter, iOS/Android)
- **[docs/releases.md](docs/releases.md)** & **[docs/auto-updates.md](docs/auto-updates.md)**: DMG notarization, Sparkle EdDSA appcast, and release pipeline
- **[AGENTS.md](AGENTS.md)**: Engineering constraints and module boundaries

---

## License

Apache-2.0 © [Hoxi (hoxi.ai)](https://hoxi.ai)
