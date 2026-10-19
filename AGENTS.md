# AGENTS.md

## Project Overview

`Hoxi` is a native-feel desktop workspace for AI agentic engineering built on four core layers, plus a mobile control plane:
1. **Desktop Host (`gopanel_v2` pattern)**: Wails v2 + `WKWebView` with native macOS vibrancy, `NSStatusBar` tray (`Running` / `Attention` badges), single-instance lock, window hide-on-close, and zero-port in-process reverse proxy.
2. **Execution Runtimes**: Official pristine `opencode` (`anomalyco/opencode`, `v1.18+`) supervised via `opencode serve` with per-boot random `OPENCODE_SERVER_PASSWORD` and transparent HTTP Basic Auth (`opencode:<password>`) injection; native multi-runtime adapters (`internal/runner`) drive Claude Code (`claude`), Antigravity (`agy`), Qoder, and CodeBuddy as pluggable subprocess runs.
3. **Frontend Workspace (`Kilo Code` pattern)**: Vite + SolidJS + Tailwind CSS 3-pane shell (Left: Projects/Worktrees/Sessions; Center: SSE Timeline/ThoughtBox/ToolCards/Composer; Right: Pierre Diffs, 8-Dim Memory, Files, WebGL Xterm, Browser).
4. **Core Capabilities**: Git worktree session isolation (`.hoxi/worktrees/<session-id>` via `?directory=`), 8-dimension structured memory (`.opencode/plugins/hoxi-memory.js`), live mid-stream steering (`Steer`), checkpoint revert/fork, cross-CLI session handoff, unified PreToolUse safety gate, and auto LSP diagnostics loop.
5. **Mobile Control Plane**: Flutter iOS/Android app (`client/`) that watches live streams, steers agents, approves safety gates, and manages Hoxi & GoPanel server nodes.

> See [DEVELOPMENT.md](DEVELOPMENT.md) for the complete architecture diagrams, OpenAPI/SSE contracts, Worktree state machines, and implementation details.

---

## Directory Structure

```text
/
├── main.go                           # Thin Wails embed.FS entrypoint delegating to internal/app
├── cmd/hoxi/main.go                  # CLI / headless entrypoint calling internal/app
├── internal/
│   ├── app/                          # DesktopApp lifecycle, Wails bindings, origin allow-list & signal handling
│   ├── agyhook/                      # `hoxi agy-hook` Antigravity PreToolUse hook → runner approval gate
│   ├── auth/                         # Hoxi account one-click authorization, token poll/exchange & store
│   ├── browser/                      # Headless Chrome session manager, tabs, actions & inspector bridge
│   ├── buildinfo/                    # Build version embedded by the desktop build
│   ├── claudeconfig/                 # Claude Code model/config discovery & selection
│   ├── cli/                          # Terminal-first `hoxi` CLI (REPL, SSE streaming renderer, subcommands & gateway client)
│   ├── gateway/                      # In-process reverse proxy, Basic Auth, WS/SSE bridge, dev pulse & security
│   ├── handoff/                      # Cross-CLI session handoff (Claude/Codex/Gemini/Grok/OpenCode/Trae/Qoder/CodeBuddy) + local CLI bridge & materializer
│   ├── leaderboard/                  # Model / endpoint leaderboard aggregation & cache
│   ├── opencode/                     # OpenCode binary resolver, installer, daemon supervision & browser plugin overlay
│   ├── privacy/                      # Versioned consent store for gateway-initiated AI execution
│   ├── provider/                     # Provider config, models.json, LLM client, probing, audit & telemetry
│   ├── runner/                       # Multi-runtime execution engine (OpenCode/Claude/Antigravity/Qoder/CodeBuddy): process adapters, translators, hook gates, session store & revert
│   ├── statusbar/                    # Running/Attention tracker & macOS NSStatusBar (CGo)
│   ├── updater/                      # Sparkle-based native auto-update checker & appcast
│   ├── workspace/                    # Multi-workspace project profile store & validation
│   └── worktree/                     # Git worktree session sandbox manager, diffs, branches & snapshots
├── web/                              # Frontend workspace (Vite + SolidJS + Tailwind)
│   ├── src/
│   │   ├── components/               # Modular UI by domain (layout, timeline, inspector, models, handoff, approval, terminal, settings, icons)
│   │   ├── i18n/                     # Reactive t(key, params), auto-inferred types & en-US/zh-CN locale modules
│   │   ├── services/                 # Pure HTTP/REST/SSE/Browser transport clients (api.ts, sse.ts, browser.ts, runner-api.ts, privacy.ts)
│   │   ├── store/                    # SolidJS reactive store & domain modules (flat barrel export in workspace.ts)
│   │   └── __tests__/                # Node-based unit/integration tests (sse, session-isolation, permissions, …)
│   └── scripts/                      # check-i18n.ts, check-lines.ts (line & boundary lint), run-tests.ts
├── client/                           # Hoxi Mobile App (Flutter + Riverpod + GoRouter, Hoxi & GoPanel dual-node)
│   └── lib/
│       ├── app/                      # App shell, router & presentation
│       ├── core/                     # Network, storage & theme primitives
│       └── features/                 # Domain features: ai_workspace, server, container, database, website, ssl, apps, resources, task_center, auth, settings
├── .opencode/
│   ├── plugins/                      # hoxi-permissions.js & hoxi-memory.js
│   ├── lib/                          # permission-policy.js + cases.json (shared safety-gate rules)
│   └── memory/                       # Long-term project.md & per-session compaction records
├── docs/                             # Approval hooks, auto-updates, release & direct-distribution guides (+ assets/)
├── scripts/                          # AI one-click release, DMG packaging, notarization & release publishing
├── README.md / README.zh-CN.md       # Bilingual landing docs
├── BROWSER.md                        # Headless Chrome inspector, manual takeover & security boundaries
├── HANDOFF_AND_CLI_BRIDGE.md         # Cross-CLI handoff IR, credential discovery & protocol bridges
├── DEVELOPMENT.md                    # Comprehensive implementation blueprint
└── AGENTS.md                         # Engineering rules & quick reference
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
- **Sync Web Build & Validate i18n / Architecture**:
  ```bash
  npm --prefix web run check:i18n && npm --prefix web run check:lines && npm --prefix web run build
  ```
- **Mobile App (`client/`) Analyze & Test**:
  ```bash
  flutter analyze client && (cd client && flutter test)
  ```
- **AI One-Click Release (一键发版上线与同步官网发版记录)**:
  ```bash
  ./scripts/ai-release.sh <version> "<release-notes>"
  ```

---

## Engineering & Functional Constraints (工程与功能约束)

1. **按领域高内聚、单向分层边界与单文件 800 行上限 (Domain Cohesion, Unidirectional Layering & 800-Line Limit)**
   - **领域高内聚与禁止张冠李戴**：模块拆分以**单一职责与业务领域高内聚**为第一原则，**严禁为了凑行数把属于模块 A 的函数跨文件塞进无关的模块 B**（如禁止把 Codex/Gemini/Claude 逻辑塞进 Grok 或 `types.go`/`helpers.go`），也禁止拆分出仅几十行的 `*-storage.ts` 脚手架文件。
   - **单文件 800 行上限**：任何源码文件（`.go`, `.ts`, `.tsx`, `.js`, `.m`）上限为 **800 行**（纯静态图标 SVG 字典或自动生成常量表除外）。当文件接近 800 行时，应按正交子领域或独立子组件/Hook 拆分。
   - **前端严格单向分层 (`components/ -> store/ -> services/`)**：
     - `services/` 仅负责 HTTP/REST/SSE 网络通信与协议传输，**严禁**反向 `import` 任何 `store/` 或 `components/` 模块，也不得混入纯 UI 渲染工具。
     - `store/` 负责响应式状态、领域数据计算与本地持久化，**严禁**反向 `import` 任何 `components/` 模块；所有子模块统一在 [`web/src/store/workspace.ts`](web/src/store/workspace.ts) 底部扁平重导出，禁止在子模块末尾做二级链式 `export *`。
     - `components/` 按业务域正交归位（`layout` 仅放窗口骨架与导航栏，独立业务弹窗归入 `models`、`handoff`、`settings`、`approval` 等对应目录）。
   - **Go 后端包边界与 Receiver 规范**：各子包（`gateway`、`provider`、`handoff`、`worktree` 等）必须使用各自明确的 Receiver 类型（`*Gateway` / `*Service` / `*Manager`），严禁定义跨包伪装别名（如 `type desktopGateway = Service`）；Hoxi 内部自发 LLM 请求统一复用 `internal/provider/http.go` 的 `callSimpleLLM`。

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

7. **多语言包模块化与自动类型推导规范 (Modular i18n & Inferred Type Safety)**
   - **禁止内联硬编码文案**：严禁在 UI 组件中使用 `locale() === "zh-CN" ? "..." : "..."` 三元硬编码或裸写中英文文案；所有用户可见文案必须统一通过 `web/src/i18n` 的 `t("domain.key", params)` 调用。
   - **按领域模块化拆分与自动类型推导**：语言包按领域存放在 `web/src/i18n/locales/en-US/<domain>.ts` 与 `web/src/i18n/locales/zh-CN/<domain>.ts`。`web/src/i18n/types.ts` 自动从 `en-US` 推导 `I18nDictionary` 与各领域类型（无需手写维护冗长的 interface 文件）。
   - **双语 100% 对齐**：新增或修改任何翻译词条时，只需同步更新 `en-US` 与 `zh-CN` 对应模块，确保键名与 `{param}` 插值占位符完全一致，并通过 `npm --prefix web run check:i18n` 校验。

8. **修改后必验闭环 (Verification Before Done)**
   - 每次代码变更完成后，必须通过类型检查、行数与架构边界门禁、语言包校验与构建验证（前端执行 `npx --prefix web tsc -p web/tsconfig.json --noEmit`、`npm --prefix web run check:lines`、`npm --prefix web run check:i18n` 与 `npm --prefix web run build`，后端执行 `go build ./...`），确保零编译错误、零越界引用与零词条缺失。

9. **Git 提交共同作者署名规范 (Git Commit Co-Author Attribution)**
   - 凡由 Hoxi 参与开发的项目（包括当前仓库本身及通过 Hoxi 开发的任意工作区），在执行 Git 提交时（含 Worktree UI 提交、会话合并提交及 Agent 执行 `git commit`），必须在 Commit Message 尾部追加共同作者尾注：`Co-authored-by: Hoxi <code@hoxi.ai>`。

---

## Where Does New Code Go? (新增代码归属决策表)

为防止架构漂移，新增功能或重构时**必须**按以下决策表归位，并优先参考对应的典范文件（Canonical Example）：

| 当你需要新增 / 修改... | 必须放入的位置 (Target Location) | 严禁放入的位置 (Forbidden) | 典范参考 (Canonical Example) |
| :--- | :--- | :--- | :--- |
| **Go 独立业务域与 HTTP 路由** | `internal/<domain>/` 独立子包，在 [`internal/gateway/gateway.go`](internal/gateway/gateway.go) 注册 | 严禁塞入 `internal/provider` 或 `internal/gateway` | [`internal/worktree/`](internal/worktree/manager.go) |
| **Go 网关自发轻量 LLM 调用** | 业务归各自领域包，底层复用 `provider` 的 `callSimpleLLM` | 严禁手写 `http.Post` 拼接 `/v1/messages` 或 `/chat/completions` | [`internal/provider/http.go`](internal/provider/http.go) |
| **外部 CLI 会话扫描 / 凭证桥接** | `internal/handoff/<tool>.go` 单文件高内聚 | 严禁把工具 A 的函数塞进工具 B、`types.go` 或 `service.go` | [`internal/handoff/codex.go`](internal/handoff/codex.go) |
| **多运行时 Agent 子进程驱动** | `internal/runner/` 对应进程 adapter 与协议 translator | 严禁侵入或修改 `internal/opencode` | [`internal/runner/`](internal/runner/http.go) |
| **前端 HTTP / REST / SSE 客户端** | `web/src/services/`，复用 [`requestJSON`](web/src/services/api.ts) | 严禁反向导入 `store/` 或 `components/`；严禁混入纯 UI 工具 | [`web/src/services/api.ts`](web/src/services/api.ts) |
| **前端响应式状态与纯领域计算** | `web/src/store/<domain>.ts`，在 [`workspace.ts`](web/src/store/workspace.ts) 底部扁平导出 | 严禁反向导入 `components/`；严禁新建 `*-storage.ts` 或二级 `export *` | [`web/src/store/git-commit.ts`](web/src/store/git-commit.ts) |
| **前端独立业务弹窗 / 功能面板** | `web/src/components/<domain>/`（`models/`, `handoff/`, `settings/`, `approval/`） | 严禁将非布局类的业务 Modal 塞进 `components/layout/` | [`web/src/components/handoff/`](web/src/components/handoff/ExternalHandoffModal.tsx) |
| **前端多语言词条** | 同步写入 `web/src/i18n/locales/en-US/<domain>.ts` 与 `zh-CN/<domain>.ts` | 严禁手写维护冗长 interface（由 [`types.ts`](web/src/i18n/types.ts) 自动推导） | [`web/src/i18n/types.ts`](web/src/i18n/types.ts) |

---

## Domain Documentation Routing (专项文档按需索引)

修改以下特定子系统前，请先按需阅读对应的专项契约文档（Single Source of Truth）：

- **整体架构、OpenCode REST/SSE 协议契约与 Worktree 状态机** → [`DEVELOPMENT.md`](DEVELOPMENT.md)
- **跨 CLI 会话接管（Handoff IR）与本地 CLI 协议桥接** → [`HANDOFF_AND_CLI_BRIDGE.md`](HANDOFF_AND_CLI_BRIDGE.md)
- **内置无头 Chrome 浏览器检视器、人工接管与安全边界** → [`BROWSER.md`](BROWSER.md)、[`internal/browser/README.md`](internal/browser/README.md)
- **统一会话体系与多运行时（Claude Code / Antigravity `agy`）执行引擎、PreToolUse 审批门控** → [`docs/handoff/2026-10-19-session-ownership.md`](docs/handoff/2026-10-19-session-ownership.md)、[`docs/antigravity-runner-probe.md`](docs/antigravity-runner-probe.md)、[`docs/antigravity-approval-hook.md`](docs/antigravity-approval-hook.md)、[`docs/claude-code-approval-hook.md`](docs/claude-code-approval-hook.md)
- **macOS 自动更新（Sparkle）、签名公证与一键发版流水线** → [`docs/auto-updates.md`](docs/auto-updates.md)、[`docs/direct-distribution-readiness.md`](docs/direct-distribution-readiness.md)、[`docs/releases.md`](docs/releases.md)
