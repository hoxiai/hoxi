# AGENTS.md

## Project Overview

`Hoxi` is a high-performance, native-feel desktop workspace for AI agentic engineering. It merges four core architectural pillars:
1. **Desktop Shell & Native Host (Borrowed from `gopanel_v2`)**: Built on Wails v2 with native macOS integration—including `WKWebView` with vibrancy/acrylic backdrop, macOS `NSStatusBar` tray with live status badges (`Running` / `Attention`), single-instance lock, window hide-on-close, and in-process reverse proxy (zero external browser popup).
2. **Core Agent Runtime (Upstream Zero-Fork)**: Official pristine `opencode` (`anomalyco/opencode`, `v1.18+`) running as a supervised subprocess via `opencode serve`. The Go daemon manages lifecycle, random password generation (`OPENCODE_SERVER_PASSWORD`), and transparent HTTP Basic Auth (`opencode:<password>`) injection.
3. **Workspace Outer Shell & Task Execution Engine**:
   - **Outer Shell**: Three-pane dark minimalist workspace (Left Nav: Projects, Worktrees & Sessions; Center Canvas: Conversation & Composer; Right Inspector: Diffs, 8-Dim Memory, Files, Terminal).
   - **Reactivity & Fluidity (from `Kilo Code`)**: Built on Vite + SolidJS + Tailwind CSS (zero-VDOM, 120Hz `requestAnimationFrame`-batched token rendering).
   - **Kilo Task Execution Experience**:
     - Real-time `/event` SSE streaming (`message.part.updated`, `message.part.delta`, `permission.asked`, `session.status`, `session.diff`).
     - Collapsible reasoning streams (`type: "reasoning"`) distinct from final assistant output (`type: "text"`).
     - Dynamic tool status cards (`type: "tool"`, states: `pending`, `running`, `completed`, `error`) displaying live elapsed timers, inputs, stdout/stderr, and inline file diffs.
     - Inline interactive approval gates (`POST /permission/{requestID}/reply` with `once` / `always` / `reject`) for destructive shell commands.
     - Unified hunk-level diff viewer (`@pierre/diffs`) and WebGL-accelerated Xterm terminal (`@xterm/xterm` + `/pty/{ptyID}/connect` WebSocket).
4. **Architectural Innovations**:
   - **Git Worktree Session Isolation**: Each session operates inside an isolated Git worktree (`.hoxi/worktrees/<session-id>`) scoped via OpenCode's `?directory=<worktree-path>` query parameter so multi-agent workflows run concurrently without branch collision.
   - **8-Dimension Structured Memory Compaction**: `.opencode/plugins/hoxi-memory.js` hooks into `experimental.chat.system.transform`, `experimental.session.compacting`, and `session.idle` to maintain structured session knowledge (Goals, Constraints, Key Facts, Changed Files, Errors & Fixes, Todo, Progress, Next Steps) in `.opencode/memory/`.
   - **Mid-Stream Live Steering (`Steer`)**: While a session is `busy`, the composer remains active with a `[Steer]` action (`POST /session/{id}/prompt_async`), injecting live course-corrections before the next tool step without aborting progress.
   - **Checkpoint Time-Travel Revert & Session Fork**: Every message and file-edit tool card supports one-click code + conversation rollback (`POST /session/{id}/revert` & `/unrevert`) and branching exploration (`POST /session/{id}/fork`).
   - **Millisecond Auto LSP Diagnostics Loop**: Surfaces live language server diagnostics (`GET /lsp`) in the inspector and automatically feeds post-edit compiler/type errors back to the agent for immediate self-healing.

> See [DEVELOPMENT.md](DEVELOPMENT.md) for the complete implementation-ready API contracts, SSE TypeScript schemas, Worktree state machines, and phase-by-phase checklist.

---

## Architectural Principles & System Boundaries

```text
┌────────────────────────────────────────────────────────────────────────┐
│ Desktop Host: Wails v2 + macOS NSStatusBar Tray (gopanel_v2 pattern)    │
│  - WKWebView (30MB RAM, native vibrancy, 0.2s launch)                   │
│  - Objective-C status bar badge: Running (busy) / Attention (approval)  │
│  - Single-instance lock, window hide-on-close                           │
├────────────────────────────────────────────────────────────────────────┤
│ Frontend UI: Vite + SolidJS (3-Pane Workspace Shell + Kilo Task UX)    │
│  - Timeline: SSE event stream, collapsible reasoning, tool call cards   │
│  - Inspection: Pierre Diff Viewer, 8-Dim Memory Bank, file tree         │
│  - Terminal: WebGL-accelerated @xterm/xterm via /pty/{id}/connect WS   │
├────────────────────────────────────────────────────────────────────────┤
│ Go Supervisor & Gateway: cmd/hoxi/                                   │
│  - Process lifecycle: supervise pristine `opencode serve`               │
│  - In-process reverse proxy & Basic Auth injection (Proxy: nil, Flush:-1│
│  - Worktree Manager: create, diff, apply & cleanup `.hoxi/worktrees` │
├────────────────────────────────────────────────────────────────────────┤
│ Agent Core: Official OpenCode (Upstream Pristine)                      │
│  - Multi-model adaptors (Claude, OpenAI, Gemini, DeepSeek, Local)      │
│  - Robust 9-stage fuzzy code replacer & native LSP integration         │
│  - Local plugins: hoxi-permissions.js, hoxi-memory.js (8-dim)      │
└────────────────────────────────────────────────────────────────────────┘
```

### 1. Upstream Zero-Fork for Core
Never fork or modify OpenCode internals. OpenCode executes as an external, pristine daemon. Upgrades happen by replacing the `opencode` binary.

### 2. Go Daemon & Desktop Shell (from `gopanel_v2`)
- **Process Supervision**: Spawns `opencode serve --hostname 127.0.0.1 --port <free-port> --print-logs` with `OPENCODE_SERVER_PASSWORD=<random-hex>`, `OPENCODE_SERVER_USERNAME=opencode`, and `OPENCODE_DISABLE_CONFIG_DEPENDENCY_INSTALL=1`, polling `GET /global/health` until ready.
- **Zero-Port Internal Gateway**: Uses Wails `AssetServer.Handler` (`desktop_gateway.go`) with `Transport: &http.Transport{Proxy: nil}` (preventing system HTTP proxies like Clash/Surge from returning `502` on `127.0.0.1`), `FlushInterval: -1` (instant SSE streaming), and `req.SetBasicAuth("opencode", password)`.
- **macOS Status Bar Integration**: Employs native Objective-C (`NSStatusBar` in `desktop_statusbar_darwin.m`) driven by `desktop_code_status.go`:
  - `Running`: Active `session.status` (`busy`) count.
  - `Attention`: Pending `permission.asked` or `question.asked` count.
- **Git Worktree Isolation**: Provisions `.hoxi/worktrees/<session-id>` on branch `hoxi/<session-id>`, passes `?directory=<worktree-path>` to OpenCode endpoints, and supports one-click `Apply to Main` (`git apply --3way` or `git merge`).

### 3. Frontend & Task Execution Feel
- **Execution Visibility**: Every `ToolPart` (`type: "tool"`) renders as an animated status card (`pending` -> `running` -> `completed` / `error`) with live elapsed duration (`0.1s` ticker), collapsible input/output, and embedded Pierre Diffs for file edits.
- **Reasoning Stream**: `ReasoningPart` (`type: "reasoning"`) tokens stream into a dedicated collapsible `<ThoughtBox />` with elapsed thought duration (`Thought for X.Xs`), keeping final answers clean.
- **Interactive Permission Approval**: Safe read/search/test commands auto-pass via `hoxi-permissions.js`; destructive commands trigger `permission.asked` and render an inline `<ApprovalGateCard />` calling `POST /permission/{requestID}/reply` (`once` | `always` | `reject`).

---

## Directory Structure

```text
/
├── main.go                           # Wails entrypoint, OpenCode supervisor & headless mode
├── desktop_gateway.go                # In-process reverse proxy, Basic Auth & WS bridge
├── desktop_statusbar_darwin.go       # macOS status bar Go CGo binding
├── desktop_statusbar_darwin.m        # Objective-C NSStatusBar implementation
├── desktop_statusbar_stub.go         # Non-darwin stub
├── desktop_worktree.go               # Git worktree session sandbox manager
├── desktop_code_status.go            # Running/Attention status aggregator
├── cmd/
│   └── hoxi/
│       └── main.go                   # CLI forwarding wrapper
├── web/                              # Frontend workspace (Vite + SolidJS + Tailwind)
│   ├── src/
│   │   ├── components/
│   │   │   ├── layout/
│   │   │   │   ├── ThreePaneShell.tsx    # 3-pane workspace shell
│   │   │   │   ├── CompactHeader.tsx     # 38px header with macOS traffic light inset
│   │   │   │   └── LeftNav.tsx           # Sessions & worktrees navigation
│   │   │   ├── timeline/
│   │   │   │   ├── MessageStream.tsx     # Conversation stream
│   │   │   │   ├── ThoughtBox.tsx        # Collapsible reasoning stream (type="reasoning")
│   │   │   │   ├── ToolCard.tsx          # Dynamic tool execution cards (type="tool")
│   │   │   │   └── Composer.tsx          # Multi-modal prompt input & Build/Plan toggle
│   │   │   ├── inspector/
│   │   │   │   ├── InspectorPane.tsx     # Right inspector (Diffs / Memory / Files / Terminal)
│   │   │   │   ├── PierreDiffViewer.tsx  # @pierre/diffs code diff inspection
│   │   │   │   ├── MemoryBankView.tsx    # 8-dimension memory viewer/editor
│   │   │   │   └── FileTreeView.tsx      # Workspace file finder & preview
│   │   │   ├── terminal/
│   │   │   │   └── WebGLTerminal.tsx     # GPU-accelerated Xterm connected to /pty WS
│   │   │   └── approval/
│   │   │       └── ApprovalGateCard.tsx  # Inline permission.asked confirmation card
│   │   ├── i18n/
│   │   │   ├── index.ts                  # Reactive t(key, params) & locale signal
│   │   │   ├── types.ts                  # Strongly-typed domain i18n interfaces (TxKey)
│   │   │   ├── validator.ts              # Key & placeholder parity validator
│   │   │   └── locales/
│   │   │       ├── en-US/                # Modular English dictionaries by domain
│   │   │       └── zh-CN/                # Modular Chinese dictionaries by domain
│   │   ├── services/
│   │   │   ├── sse-types.ts              # OpenCode v1.18+ event & part TypeScript contracts
│   │   │   ├── sse.ts                    # /event SSE client with rAF batching
│   │   │   └── api.ts                    # OpenCode REST & Worktree API client
│   │   ├── store/
│   │   │   └── workspace.ts              # SolidJS reactive store
│   │   ├── App.tsx                       # Root layout assembly
│   │   └── index.tsx                     # SolidJS entry
│   ├── scripts/
│   │   └── check-i18n.ts                 # CLI parity checker for all locales
│   ├── package.json
│   ├── vite.config.ts
│   └── dist/                             # Compiled web bundle for Wails embed
├── .opencode/
│   ├── package.json                      # Pre-declared plugin dependency
│   ├── plugins/
│   │   ├── hoxi-permissions.js         # Permission auto-approver & destructive gate
│   │   └── hoxi-memory.js              # 8-dimension structured memory plugin
│   └── memory/
│       ├── project.md                    # Long-term workspace knowledge bank
│       └── sessions/                     # Historical session compaction records
├── build-desktop.sh                      # Production macOS .app packaging script
├── wails.json                            # Wails v2 desktop configuration
├── go.mod                                # Go module definition
├── DEVELOPMENT.md                        # Comprehensive implementation blueprint
└── AGENTS.md                             # Architectural manual & engineering rules
```

---

## Build & Run Commands

- **Desktop Development (Hot Reloading)**:
  ```bash
  wails dev
  ```
- **Build Production macOS Application (`.app`)**:
  ```bash
  ./build-desktop.sh
  ```
- **Run Headless Daemon (CLI / Browser Mode)**:
  ```bash
  go run ./cmd/hoxi --no-desktop
  ```
- **Sync Web Build & Validate i18n**:
  ```bash
  npm --prefix web run check:i18n && npm --prefix web run build
  ```

---

## Engineering & Functional Constraints (工程与功能约束)

1. **单文件 500 行红线与模块化拆分 (500-Line Hard Limit)**
   - 任何源码文件（`.go`, `.ts`, `.tsx`, `.js`, `.m`）严禁超过 **500 行**（纯静态图标 SVG 字典或自动生成的常量表除外）。
   - 一旦文件接近或超过 500 行，**必须立即按职责拆分**：大型 UI 组件拆分为独立子组件/视图块，复杂状态或数据处理抽离为独立 Hook、Helper 或按领域拆分的 Store 模块。
   - 视图层（`components/`）、通信层（`services/`）与状态层（`store/`）职责分离，禁止在 UI 组件内直接编写复杂的协议解析或长篇数据转换逻辑。

2. **OpenCode 上游零侵入 (Upstream Zero-Fork)**
   - 严禁 Fork 或修改 `opencode` 内部源码；所有定制能力仅通过 Go 进程监管、反向代理或 `.opencode/plugins/` 插件钩子扩展。

3. **多会话与 Git Worktree 强隔离 (Worktree & Directory Scoping)**
   - 所有关联特定会话的 OpenCode 接口请求（如 `/session`、`/file`、`/pty`、`/permission`）**必须**携带该会话对应的 `?directory=<worktree-path>` 参数，严禁跨会话读写或污染其它工作树。

4. **SolidJS 响应式与流式性能约束 (SolidJS Reactivity & 120Hz Streaming)**
   - 严禁在 SolidJS 组件中直接解构 `props` 或 `store` 状态，必须保持细粒度 Signal/Store 访问路径以确保响应式更新。
   - `/event` SSE 高频增量事件（`message.part.delta` / `updated`）必须经 `requestAnimationFrame` 批处理合并后更新 Store，禁止逐 Token 触发全量重绘。

5. **任务流状态与回滚闭环 (Task Lifecycle & Control Integrity)**
   - 会话处于 `busy` 时必须支持实时纠偏（`Steer`）与中止（`Stop`），工具卡片（`ToolCard`）与推理抽屉（`ThoughtBox`）必须准确反映 `pending` / `running` / `completed` / `error` 四态与耗时，且状态变更需实时同步至 macOS 状态栏（`Running` / `Attention`）。

6. **安全门控与本地网关约束 (Safety Gate & Local Gateway)**
   - 包含破坏性指令（`rm`, `sudo`, `chmod`, `chown`, `diskutil`, `mkfs`, `git push --force`, `git reset --hard`）的 Shell 操作严禁自动放行，必须在 `hoxi-permissions.js` 中返回 `"ask"`，触发状态栏 `Attention` 与 `<ApprovalGateCard />` 人工确认。
   - Go 网关仅监听 `127.0.0.1`，反向代理 `http.Transport` 必须配置 `Proxy: nil`（防系统代理 502）与 `FlushInterval: -1`（SSE 零缓冲），并在进程内自动注入随机生成的 `OPENCODE_SERVER_PASSWORD`。

7. **多语言包模块化与强类型对齐规范 (Modular i18n & Parity Constraint)**
   - **禁止内联硬编码文案**：严禁在 UI 组件中使用 `locale() === "zh-CN" ? "..." : "..."` 三元硬编码或裸写中英文文案；所有用户可见文案必须统一通过 `web/src/i18n` 的 `t("domain.key", params)` 调用。
   - **按领域模块化拆分**：语言包按领域拆分存放在 `web/src/i18n/locales/en-US/<domain>.ts` 与 `web/src/i18n/locales/zh-CN/<domain>.ts`（如 `common`, `header`, `leftNav`, `timeline`, `toolCard`, `approval`, `inspector`, `models`），禁止退化为巨型单文件字典。
   - **强类型契约与双语 100% 对齐**：新增或修改任何翻译词条时，必须同步更新 `web/src/i18n/types.ts` 接口定义以及 `en-US` / `zh-CN` 对应模块，确保键名与 `{param}` 插值占位符完全一致，并通过 `npm --prefix web run check:i18n` 校验。

8. **修改后必验闭环 (Verification Before Done)**
   - 每次代码变更完成后，必须通过类型检查、语言包校验与构建验证（前端执行 `npx --prefix web tsc -p web/tsconfig.json --noEmit`、`npm --prefix web run check:i18n` 与 `npm --prefix web run build`，后端执行 `go build ./...`），确保零编译错误与零词条缺失。


