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
│   ├── diagnostics/                  # Gateway & runtime error ring-buffer, health diagnostics & telemetry
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
- **Sync Web Build & Validate Lint / Tests / Architecture**:
  ```bash
  npm --prefix web run lint && npm --prefix web test && npm --prefix web run build
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
   - **Go 后端四层单向依赖 (`app/cli -> gateway -> 业务领域包 -> 基础底座包`)**：
     - 业务领域包（`runner`, `handoff`, `worktree`, `provider`, `browser`, `opencode`, `workspace`, `auth`, `leaderboard`, `updater`, `privacy`）与底座包（`statusbar`, `claudeconfig`, `agyhook`, `diagnostics`, `buildinfo`）**严禁反向 `import` `internal/gateway`、`internal/app` 或 `internal/cli`**。
     - 各子包必须使用各自明确的 Receiver 类型（`*Gateway` / `*Service` / `*Manager`），严禁定义跨包伪装别名（如 `type desktopGateway = Service`）；Hoxi 内部自发 LLM 请求统一复用 `internal/provider/http.go` 的 `callSimpleLLM`。

2. **OpenCode 上游零侵入 (Upstream Zero-Fork)**
   - 严禁 Fork 或修改 `opencode` 内部源码；所有定制能力仅通过 Go 进程监管、反向代理或 `.opencode/plugins/` 插件钩子扩展。

3. **三大统一网关平面与 Git Worktree 强隔离 (Unified Gateway Planes & Worktree Scoping)**
   - **会话、事件流与权限审批三大平面统一收口**：前端（[`web/src/services/api.ts`](web/src/services/api.ts)、[`sse.ts`](web/src/services/sse.ts)）与 CLI 的所有会话操作必须统一走 `/api/hoxi/sessions`，SSE 事件流必须统一订阅 `/api/hoxi/event`，权限审批必须统一走 `/api/hoxi/permission`（由 `gateway` 与 `runner` 合并路由 OpenCode 与 Claude/Antigravity/Qoder/CodeBuddy 运行时），**严禁前端直接裸调上游 `/session`、`/event` 或 `/permission` 接口**。
   - **工作树目录强隔离**：所有关联特定会话或沙箱的请求（`/api/hoxi/sessions`、`/api/hoxi/event`、`/api/hoxi/permission`、`/file`、`/pty`）**必须**携带该会话对应的 `?directory=<worktree-path>` 参数，严禁跨会话读写或污染其它工作树。

4. **SolidJS 响应式、生命周期清理与异步防竞态约束 (SolidJS Reactivity, Cleanup & Stale-Read Guard)**
   - **禁止解构响应式源**：严禁在 SolidJS 组件中直接解构 `props` 或 `store` 状态，必须保持细粒度 Signal/Store 访问路径以确保响应式更新。
   - **120Hz 流式批处理**：`/api/hoxi/event` SSE 高频增量事件（`message.part.delta` / `updated`）必须经 `requestAnimationFrame` 批处理合并后更新 Store，禁止逐 Token 触发全量重绘。
   - **成对清理与防过期响应覆盖**：组件内注册的 `addEventListener`、`ResizeObserver`、定时器及 WebSocket/PTY 连接**必须**在 `onCleanup` 中成对销毁；跨会话或跨目录的异步读取（拉取历史、Diff、诊断等）必须校验当前激活 `sessionID` 或递增请求世代号（Stale-Read Guard），丢弃晚到的过期响应。

5. **任务流状态与回滚闭环 (Task Lifecycle & Control Integrity)**
   - 会话处于 `busy` 时必须支持实时纠偏（`Steer`）与中止（`Stop`），工具卡片（`ToolCard`）与推理抽屉（`ThoughtBox`）必须准确反映 `pending` / `running` / `completed` / `error` 四态与耗时，且状态变更需实时同步至 macOS 状态栏（`Running` / `Attention`）。

6. **安全门控、隐私授权、子进程消毒与资源边界 (Safety, Privacy, Subprocess & Resource Hygiene)**
   - **破坏性操作人工审批**：包含破坏性指令（`rm`, `sudo`, `chmod`, `chown`, `diskutil`, `mkfs`, `git push --force`, `git reset --hard`）的 Shell 操作严禁自动放行，必须在 `hoxi-permissions.js` 及多运行时 PreToolUse Hook 中返回 `"ask"`，触发状态栏 `Attention` 与 `<ApprovalGateCard />` 人工确认。
   - **本地网关与连接复用**：Go 网关仅监听 `127.0.0.1`，反向代理 `http.Transport` 必须配置 `Proxy: nil`（防系统代理 502）与 `FlushInterval: -1`（SSE 零缓冲）并复用长生命周期 `http.Transport`（严禁单次请求新建 Transport 导致连接泄漏）；读取外部 HTTP 响应或进程输出必须使用 `io.LimitReader` 限制体积。
   - **网关自发 AI 调用隐私门控 (`internal/privacy`)**：凡由 Hoxi 网关后台主动发起的 LLM 调用（如 Commit Message 生成、分支命名、代码审计），必须先通过 `privacy.Store` 授权检查，未经授权严禁静默外发代码上下文。
   - **外部子进程非交互消毒与 SQLite 防锁 (`Subprocess & SQLite Hygiene`)**：执行 `git` 或外部 CLI 子进程必须绑定 `context.Context` 超时并剥离交互式环境变量（设置 `GIT_TERMINAL_PROMPT=0`、`GCM_INTERACTIVE=never`，剥离 `GIT_ASKPASS`/`SSH_ASKPASS`/`GIT_EDITOR`）；读取外部 CLI 的 SQLite 库必须携带只读模式与 `_busy_timeout`，严禁阻塞外部进程。
   - **本地状态原子落盘与日志脱敏 (`Atomic Persistence & Secret Redaction`)**：`~/.hoxi/` 与 `.hoxi/` 状态文件写入必须采用「写临时文件 + `os.Rename` 原子替换」并加锁保护；写入诊断环（`internal/diagnostics`）或日志前必须自动脱敏 `Authorization`、`Bearer`、`x-api-key`、`OPENCODE_SERVER_PASSWORD` 等敏感凭据。

7. **多语言包模块化与自动类型推导规范 (Modular i18n & Inferred Type Safety)**
   - **禁止内联硬编码文案**：严禁在 UI 组件中使用 `locale() === "zh-CN" ? "..." : "..."` 三元硬编码或裸写中英文文案；所有用户可见文案必须统一通过 `web/src/i18n` 的 `t("domain.key", params)` 调用。
   - **按领域模块化拆分与自动类型推导**：语言包按领域存放在 `web/src/i18n/locales/en-US/<domain>.ts` 与 `web/src/i18n/locales/zh-CN/<domain>.ts`。`web/src/i18n/types.ts` 自动从 `en-US` 推导 `I18nDictionary` 与各领域类型（无需手写维护冗长的 interface 文件）。
   - **双语 100% 对齐**：新增或修改任何翻译词条时，只需同步更新 `en-US` 与 `zh-CN` 对应模块，确保键名与 `{param}` 插值占位符完全一致，并通过 `npm --prefix web run check:i18n` 校验。
   - **中文词条结尾不加句号**：`zh-CN` 词条结尾严禁添加中文句号「。」（英文同样不加句末标点），`check:i18n` 会强制拦截以「。」结尾的中文词条。

8. **文件、符号与协议命名规范 (Naming Conventions)**
   - **目录与文件命名**：
     - Go 包名（`internal/<pkg>`）使用全小写无下划线单词（如 `worktree`, `handoff`, `agyhook`）；Go 源文件统一使用小写 `snake_case.go`（测试为 `*_test.go`），禁止在文件名中重复包名前缀（如 `internal/worktree/` 下命名为 `diff.go` 而非 `worktree_diff.go`）。
     - 前端 `web/src/store/` 与 `web/src/services/` 统一使用小写 `kebab-case.ts`（如 `session-sandbox.ts`, `api-types.ts`）；`web/src/components/<domain>/` 下独立导出组件使用 `PascalCase.tsx`（如 `ApprovalGateCard.tsx`），局部子部件/纯辅助逻辑使用 `kebab-case.ts(x)`（如 `tool-card-helpers.tsx`）；移动端 `client/lib/` 统一使用 `snake_case.dart`。
   - **代码符号与函数命名**：
     - Go 各领域包核心结构体统一命名为 `Service` / `Manager` / `Store`（构造函数 `NewService` / `NewManager` / `NewStore`），HTTP 路由注册统一使用 `RegisterRoutes(mux)` 与 `handle<Action>`。
     - 前端 `services/api.ts` 请求函数与 Store 异步 Action 统一使用语义化动词前缀（`fetch*`, `load*`, `create*`, `update*`, `delete*`, `apply*`, `switch*`），布尔派生状态使用 `is*` / `has*` / `can*`。
   - **路由、实体 ID 与存储键名**：
     - Hoxi 自有 HTTP 路由统一挂载在 `/api/hoxi/<domain>/<action>`（路径使用 `kebab-case`，JSON 字段统一使用 `camelCase`）。
     - 核心实体 ID 严格保留可辨识前缀：会话 `ses_*`、消息 `msg_*`、消息片段 `prt_*`、权限审批 `per_*`（Runner 侧为 `per_rnr_*`）。
     - 前端 `localStorage` 键名必须以 `hoxi.` 或 `hoxi:` 命名空间开头，严禁使用无前缀通用词；i18n 键名严格采用 `<domain>.<camelCaseKey>` 两级扁平点分结构。

9. **修改后必验闭环 (Verification Before Done)**
   - 每次代码变更完成后，必须通过类型检查、行数与架构边界门禁、语言包校验、单元测试与构建验证（前端执行 `npm --prefix web run lint && npm --prefix web test && npm --prefix web run build`，后端执行 `go vet ./... && go test ./... && go build ./...`），确保零编译错误、零越界引用、零词条缺失与测试全绿。

10. **Git 提交共同作者署名规范 (Git Commit Co-Author Attribution)**
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
