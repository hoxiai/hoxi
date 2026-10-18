# Hoxi

**Hoxi** ([hoxi.ai](https://hoxi.ai)) is a high-performance, native-feel desktop workspace for AI agentic engineering.

It combines the battle-tested **OpenCode** agent runtime with a **three-pane workspace**, **Kilo Code-style task execution flow**, and a **lightweight Wails v2 desktop shell** (borrowed from `gopanel_v2`).

---

## ⚡ Core Architecture

```text
┌─────────────────────────────────────────────────────────────┐
│ Desktop Host: Wails v2 + macOS NSStatusBar Tray             │
│  - Native WKWebView (30MB RAM, 0.2s cold start, vibrancy)   │
│  - Objective-C system tray with live Running/Attention badge│
├─────────────────────────────────────────────────────────────┤
│ Frontend UI: Vite + SolidJS (Zero-VDOM, 120Hz high refresh) │
│  - Shell Layout: 3-Pane Workspace (Left Nav + Chat + Inspect)│
│  - Task Execution: Kilo-style tool cards & thinking drawer  │
│  - Code Inspection: Real-time Pierre Diff & WebGL Xterm     │
├─────────────────────────────────────────────────────────────┤
│ Go Supervisor & Internal Gateway (main.go / desktop_*.go)   │
│  - Supervises pristine upstream opencode serve daemon       │
│  - In-process reverse proxy with zero network ports         │
│  - Git Worktree session isolation (.hoxi/worktrees/<id>)    │
├─────────────────────────────────────────────────────────────┤
│ Agent Core: Pristine OpenCode (Upstream Zero-Fork)          │
│  - 9-stage fuzzy code replacer & multi-model support        │
│  - Local plugins: 8-dimension structured memory & auto-auth │
└─────────────────────────────────────────────────────────────┘
```

---

## 🚀 Key Highlights

1. **Upstream Zero-Fork**: Never forks OpenCode. Runs pristine `opencode serve` as a supervised daemon—upgrade effortlessly by swapping the binary.
2. **Kilo-Style Task Execution**: Every tool call (`read_file`, `edit_file`, `run_command`) displays as an animated status card with live elapsed time, stdout streaming, and inline permission approval gates.
3. **3-Pane Workspace Shell**: Dedicated left navigation for sessions and worktrees, central conversation canvas, and persistent right inspector for live Pierre Diffs and 8-dimension memory reflections.
4. **Git Worktree Isolation & Time-Travel Revert**: Sessions run in physical Git worktrees (`.hoxi/worktrees/<session-id>`), with single-step code + conversation rollback (`/revert` & `/unrevert`) and session branching (`/fork`).
5. **Live Mid-Stream Steering & Auto LSP Diagnostics**: Steer running agents on the fly without aborting progress, backed by millisecond post-edit LSP compiler diagnostics.
6. **Native macOS Integration**: Built with Wails v2 and Objective-C bindings—includes native window vibrancy, single instance lock (`ai.hoxi.desktop`), window hide-on-close, and status bar badges.

---

## 📖 Documentation

- **[DEVELOPMENT.md](DEVELOPMENT.md)**: Full technical architecture, communication protocols, implementation specs, and packaging guidelines.
- **[AGENTS.md](AGENTS.md)**: Engineering rules, project constraints, and conventions for AI assistants.
- **[docs/releases.md](docs/releases.md)**: Versioned builds, DMG verification, dry runs, signing, notarization, and publication.

---

## 🛠️ Quick Start

### Prerequisites
- Go `>= 1.23`
- Node.js `>= 20` or Bun `>= 1.2`
- `opencode` installed on `PATH`
- Wails CLI v2 (`go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0`)

### Development
```bash
# Run with desktop window & hot-reload
wails dev

# Run in headless daemon mode
go run . --no-desktop
```

### Local Website and Login Testing

`HOXI_SERVER_URL` overrides the website base URL used for desktop login and
update checks. If unset or empty, it defaults to `https://hoxi.ai`.

For a single launch:

```bash
HOXI_SERVER_URL=http://127.0.0.1:5400 wails dev
```

Alternatively, put `HOXI_SERVER_URL=http://127.0.0.1:5400` in the Git-ignored
root `.env.local`, then export it before starting the app (env files are not
loaded automatically):

```bash
set -a
source .env.local
set +a
wails dev
# Or: go run . --no-desktop
```

Restart the app after changing the variable. This is separate from
`HOXI_BACKEND_URL`, which points the frontend at the local desktop gateway.
Use a test profile without production login credentials: startup verification
sends the saved login token to the configured server.

### Build Production Application (`build/bin/Hoxi.app`)
```bash
./build-desktop.sh
```

---

## 📄 License

Apache-2.0 © [Hoxi (hoxi.ai)](https://hoxi.ai)
