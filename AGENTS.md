# AGENTS.md

## Project Overview & Product Direction (项目全景与最终产品形态)

`Hoxi` 是面向 **AI 时代独立开发者与「一人公司（One-Person AI Company）」** 的全栈研发与经营操作系统，由 **macOS 原生多 Agent 桌面工作台** 与 **「沟通即操作系统（Chat & Realtime Voice as the OS）」的移动端掌上总控台** 共同构成，并深度协同旗下 6 大核心业务与基础设施矩阵：

### 1. 五大核心技术层 (Five Core Architectural Layers)
1. **Desktop Host (`gopanel_v2` pattern)**: Wails v2 + `WKWebView`（~30MB 内存、0.2s 极速冷启动），原生 macOS 毛玻璃（Vibrancy）、`NSStatusBar` 托盘徽章（`Running` 忙碌计数 / `Attention` 待审批提醒）、单实例锁、点红叉隐藏驻留与零端口进程内反向代理。
2. **Execution Runtimes (多运行时 Agent 引擎)**: 官方原版 `opencode` (`anomalyco/opencode`, `v1.18+`) 零侵入进程监管（每次启动生成随机 `OPENCODE_SERVER_PASSWORD` 并自动注入 `opencode:<password>` HTTP Basic Auth）；原生多运行时引擎（`internal/runner`）统一驱动 Claude Code (`claude`)、Antigravity (`agy`)、Qoder 与 CodeBuddy 子进程。
3. **Frontend Workspace (`Kilo Code` pattern)**: Vite + SolidJS + Tailwind CSS 三栏极速工作区（左栏：项目/Git Worktree 沙箱/跨 CLI 会话接管；中栏：120Hz SSE 时间线/Subagent 嵌套树/ThoughtBox/ToolCards/Steer 输入框；右栏：Pierre Diffs 代码审查、8 维结构化记忆、文件树、WebGL Xterm 终端、隔离 Headless Chrome 浏览器）。
4. **Core Capabilities (桌面核心能力)**: Git Worktree 会话级物理隔离（`.hoxi/worktrees/<session-id>` via `?directory=`）、8 维度长效记忆（`.opencode/plugins/hoxi-memory.js`）、运行中实时纠偏（`Steer`）、单步时光机回滚/分叉（`Revert` / `Fork`）、8 大外部 CLI 会话无损接管（`internal/handoff`）、跨运行时统一 PreToolUse 安全门控、以及自动 LSP 编译诊断闭环。
5. **Mobile Super-Assistant & Control Plane (`client/` — 沟通 · 工程 · 专栏 · 我的)**:
   - **核心产品哲学与极简架构（「双底座 + 可插拔业务 Skills」）**：
     - **语音唤醒 → 实时语音即操作系统**：「**语音唤醒（“嘿 Hoxi” / “你好灵光”） → 进入 1:1 灵光全屏全双工连续语音对话 → 手机端只直接受控 `GoPanel 线上服务器` 与 `Hoxi 本地电脑（端口 5417 反向加密隧道）` 两大底座；所有垂直业务（如 `runshoply.com` / `iqingpu.com` 独立站店铺管理、`qingpu.ai` 跨境上品）统一作为可插拔 `Skill`（`internal/skills`）挂载在底座上由语音一句话触发执行**」，保持手机端主脉络极致简单、免发版无限扩展。
   - **四栏极简架构**：
     - **Tab 0 `沟通` (`features/chat_hub`)**：对标阿里「灵光」的极简 AI 对话与 **1:1 全屏实时语音通话中枢**。支持**语音唤醒**、**30 分钟以上免动手全双工连续语音对话**（iOS `AVAudioSession.Mode.voiceChat` 硬件回声消除 AEC + 50 秒 `SFSpeechRecognitionTask` 无缝轮转 + `onUserBargeIn` **开口即打断 / 点击打断**），双模支持 **端到端实时语音大模型（`Qwen-Omni-Turbo-Realtime` / `GPT-4o-Mini-Realtime` WebSocket 24kHz PCM16）** 与 **Pipecat 级 `StreamingSentenceChunker` 流式分句神经语音播报**。
     - **Tab 1 `工程` (`features/ai_workspace` & `features/server`)**：统一纳管 **Hoxi 本地电脑 (`nodeType: 'hoxi'`，局域网桥接或 GoPanel 隧道中继)** 与 **GoPanel 线上服务器 (`nodeType: 'gopanel'`)**，顶部三段切换 `待我处理 (Attention)` · `AI 编码 (Worktree Sessions & Skills)` · `主机与容器 (100% 保留 GoPanel 原有 CPU/Docker/SSL/Website/DB 能力)`。
     - **Tab 2 `专栏` (`features/portal`)**：实时直连 `hoxi.ai` 的 **大模型实战性价比排行榜**、**一人公司五维就绪度交互式自测雷达** 与 **6 大项目 / 业务 Skills 协同全景**。
     - **Tab 3 `我的` (`features/settings`)**：双底座受控节点扫码配对、经 GoPanel 反向隧道添加 Hoxi 电脑（`/api/tunnel/hoxi-nodes/:id/proxy/*`，已实现）与安全锁；局域网 `:5417` 直连尚未实现。

### 2. 双底座 + 可插拔业务 Skills 生态矩阵 (Dual-Base & Pluggable Skills Matrix)
手机端通过语音唤醒后，只与 **两大受控底座** 通信，其余业务统一作为 **Agent Skills (`SKILL.md`)** 挂载在底座执行：
1. **底座一：Hoxi Desktop (`hoxi`)**：本地电脑多 Agent 并行研发工作台（外网经 GoPanel `WSS /api/tunnel/connect` 隧道由 `internal/tunnel` 进程内接入，Mac 不开放端口；局域网 `:5417` 直连为规划项；语音直接新建隔离 Worktree 会话、下发单测/编码/Skill 指令、批准安全门控、提交 Git 代码、停止会话）。
2. **底座二：GoPanel (`gopanel_v2`)**：轻量级 Linux 服务器与容器面板（100% 保留原有运维能力，语音巡检 CPU/内存/负载，语音直接启停/重启 Docker 容器、续期 SSL 证书、触发网站 CI/CD 流水线、同步数据库实例）。
3. **网关底座：AINode (`api.ainode.run`) & `hoxi.ai`**：统一高可用大模型路由、`wss://.../v1/realtime` 实时语音网关、大模型真实会话性价比评测榜与一人公司五维就绪度评估门户。
4. **业务 Skill 1：`shoply-ops` (`runshoply.com` / `iqingpu.com`)**：多店铺独立站矩阵与 SaaS 运营 Skill（挂载于 Hoxi/GoPanel，语音触发查店/开店/免密后台链接、全平台店铺大盘、客户订阅权益对账 `reconcile`、建站咨询线索跟进 `leads` 及 `remote-ops.mjs` 双节点自动化运维）。
5. **业务 Skill 2：`qingpu-ops` (`qingpu.ai`)**：Ozon / Wildberries / Yandex 跨境电商上品 Skill（挂载于 Hoxi/GoPanel，语音触发跨境店铺连通巡检与四阶段上品漏斗调度）。

> See [DEVELOPMENT.md](DEVELOPMENT.md), [docs/gopanel-hoxi-remote-tunnel.md](docs/gopanel-hoxi-remote-tunnel.md), and [client/VOICE_REMOTE_ARCHITECTURE.md](client/VOICE_REMOTE_ARCHITECTURE.md) for the complete architecture diagrams, Port `5417` reverse tunnel protocol, Mobile Voice Wake-up architecture, and Skills integration details.

---

## Directory Structure

```text
/
├── main.go                           # Thin Wails embed.FS entrypoint delegating to internal/app
├── cmd/hoxi/main.go                  # CLI / headless entrypoint calling internal/app
├── internal/
│   ├── app/                          # DesktopApp lifecycle, Wails bindings, origin allow-list & signal handling
│   ├── agentcli/                     # [底座层] Local agent CLI (Qoder/CodeBuddy) binary & model discovery + effort helpers
│   ├── agyhook/                      # [底座层] `hoxi agy-hook` Antigravity PreToolUse hook → runner approval gate
│   ├── attachment/                   # [业务层] Multimodal file/image attachment staging, validation & store
│   ├── auth/                         # [业务层] Hoxi account one-click authorization, token poll/exchange & store
│   ├── browser/                      # [业务层] Headless Chrome session manager, tabs, actions & inspector bridge
│   ├── buildinfo/                    # [底座层] Build version embedded by the desktop build
│   ├── claudeconfig/                 # [底座层] Claude Code model/config discovery & selection
│   ├── cli/                          # Terminal-first `hoxi` CLI (REPL, SSE streaming renderer, subcommands & gateway client)
│   ├── codeindex/                    # [业务层] Tree-sitter / symbol & trigram workspace code index
│   ├── diagnostics/                  # [底座层] Gateway & runtime error ring-buffer, health diagnostics & telemetry
│   ├── forge/                        # [业务层] GitHub / GitLab PR, issue & CI review integration
│   ├── gateway/                      # In-process reverse proxy, Basic Auth, WS/SSE bridge, dev pulse & security
│   ├── handoff/                      # [业务层] Cross-CLI session handoff (Claude/Codex/Gemini/Grok/OpenCode/Trae/Qoder/CodeBuddy) + local CLI bridge & materializer
│   ├── hoxipaths/                    # [底座层] Unified ~/.hoxi, hoxi.db & engine-data/state path resolution
│   ├── leaderboard/                  # [业务层] Model / endpoint leaderboard aggregation & cache
│   ├── llm/                          # [底座层] Narrow Client/Target/Request contract for internal single-shot LLM calls
│   ├── opencode/                     # [业务层] OpenCode binary resolver, installer, daemon supervision & browser plugin overlay
│   │   ├── ocapi/                    # [底座层] Shared loopback HTTP/SSE client & Basic Auth transport for `opencode serve`
│   │   └── ocstate/                  # [底座层] External OpenCode SQLite reader & Console account sync into hoxi.db
│   ├── orchestration/                # [业务层] Multi-agent DAG convoys on the runner session plane: per-task worktrees & dependency merge
│   ├── promptassist/                 # [业务层] Composer prompt enhancement & session title summarization via llm.Client
│   ├── provider/                     # [业务层] Provider config, models.json, llm.Client implementation, probing & model stats
│   ├── recall/                       # [业务层] Cross-session semantic / BM25 memory recall & indexing
│   ├── runner/                       # [业务层] Multi-runtime execution engine (OpenCode/Claude/Antigravity/Qoder/CodeBuddy): process adapters, translators, hook gates, session store & revert
│   ├── sandbox/                      # [业务层] OS-level Seatbelt / Bubblewrap execution sandbox policy & profiles
│   ├── skills/                       # [业务层] Local & marketplace Agent Skills discovery, parser & installer
│   ├── statusbar/                    # [底座层] Running/Attention tracker & macOS NSStatusBar (CGo)
│   ├── telemetry/                    # [业务层] Local token/cost/latency metrics & session usage ledger
│   ├── textindex/                    # [底座层] Pure-Go BM25 / trigram full-text search index primitive
│   ├── updater/                      # [业务层] Sparkle-based native auto-update checker & appcast
│   ├── workspace/                    # [业务层] Multi-workspace project profile store & validation
│   └── worktree/                     # [业务层] Git worktree session sandbox manager, diffs, branches, snapshots, review audit & commit messages
├── web/                              # Frontend workspace (Vite + SolidJS + Tailwind)
│   ├── src/
│   │   ├── components/               # Modular UI by domain (layout, timeline, inspector, models, handoff, approval, codeindex, orchestration, skills, terminal, settings, icons, motion)
│   │   ├── i18n/                     # Reactive t(key, params), auto-inferred types & en-US/zh-CN locale modules
│   │   ├── services/                 # Pure HTTP/REST/SSE/Browser transport clients (http.ts, api.ts, sse.ts, browser.ts, runner-api.ts, platform-api.ts, …)
│   │   ├── store/                    # SolidJS reactive store & domain modules (flat barrel export in workspace.ts)
│   │   └── __tests__/                # Node-based unit/integration tests (sse, session-isolation, permissions, …)
│   └── scripts/                      # check-i18n.ts, check-lines.ts (line & boundary lint), run-tests.ts
├── client/                           # Hoxi Mobile App (Flutter + Riverpod + GoRouter, 沟通·工程·专栏·我的)
│   ├── ios/Runner/AppDelegate.swift  # iOS full-duplex .voiceChat (Hardware AEC) audio engine, 50s ASR rollover, PCM16 player & Barge-in
│   ├── android/app/src/main/kotlin/  # Android continuous SpeechRecognizer, AudioFocus, PCM16 AudioTrack & Barge-in
│   └── lib/
│       ├── app/                      # 4-tab HoxiNavigationShell (沟通·工程·专栏·我的), router & presentation
│       ├── core/                     # Network (SSE & REST ApiClient), StorageService (dual-node hoxi/gopanel) & theme
│       └── features/
│           ├── chat_hub/             # [核心] 沟通中枢：ChatInformationEngine (6大项目情报)、ChatActionExecutor (12类声控直达执行)、RealtimeVoiceModelService (Qwen-Omni/GPT-4o Realtime)、ChatHubVoiceCallSheet (1:1 灵光全屏流体语音通话)
│           ├── portal/               # [专栏] hoxi.ai 实时大模型性价比榜、五维就绪度交互评测与生态矩阵
│           ├── ai_workspace/         # [工程] Hoxi (/api/hoxi/*) 与 GoPanel (/api/code/*) 双模适配器、Worktree 会话与 Git 审查
│           └── server/container/database/website/ssl/apps/resources/task_center/auth/settings
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
   - **单文件 800 行上限**：任何源码文件（`.go`, `.ts`, `.tsx`, `.js`, `.m`, `.dart`）上限为 **800 行**（纯静态图标 SVG 字典或自动生成常量表除外）。当文件接近 800 行时，应按正交子领域或独立子组件/Hook 拆分。
   - **前端严格单向分层 (`components/ -> store/ -> services/`)**：
     - `services/` 仅负责 HTTP/REST/SSE 网络通信与协议传输，**严禁**反向 `import` 任何 `store/` 或 `components/` 模块，也不得混入纯 UI 渲染工具。
     - `store/` 负责响应式状态、领域数据计算与本地持久化，**严禁**反向 `import` 任何 `components/` 模块；所有子模块统一在 [`web/src/store/workspace.ts`](web/src/store/workspace.ts) 底部扁平重导出，禁止在子模块末尾做二级链式 `export *`。
     - `components/` 按业务域正交归位（`layout` 仅放窗口骨架与导航栏，独立业务弹窗归入 `models`、`handoff`、`settings`、`approval`、`codeindex`、`orchestration`、`skills` 等对应目录）。
   - **Go 后端四层单向依赖 (`app/cli -> gateway -> 业务领域包 -> 基础底座包`)**：
     - 业务领域包（`runner`, `handoff`, `worktree`, `provider`, `promptassist`, `browser`, `opencode`, `workspace`, `auth`, `leaderboard`, `updater`, `attachment`, `codeindex`, `forge`, `orchestration`, `recall`, `sandbox`, `skills`, `telemetry`）与底座包（`hoxipaths`, `llm`, `agentcli`, `opencode/ocapi`, `opencode/ocstate`, `statusbar`, `claudeconfig`, `agyhook`, `diagnostics`, `buildinfo`, `textindex`）**严禁反向 `import` `internal/gateway`、`internal/app` 或 `internal/cli`**。
     - 各子包必须使用各自明确的 Receiver 类型（`*Gateway` / `*Service` / `*Manager`），严禁定义跨包伪装别名（如 `type desktopGateway = Service`）；Hoxi 内部自发 LLM 请求统一由 `gateway` 注入 [`llm.Client`](internal/llm/llm.go)（底层由 `internal/provider` 实现），业务领域包严禁直接 `import internal/provider`。

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

6. **安全门控、网络外发边界、子进程消毒与资源边界 (Safety, Egress, Subprocess & Resource Hygiene)**
   - **破坏性操作人工审批**：包含破坏性指令（`rm`, `sudo`, `chmod`, `chown`, `diskutil`, `mkfs`, `git push --force`, `git reset --hard`）的 Shell 操作严禁自动放行，必须在 `hoxi-permissions.js` 及多运行时 PreToolUse Hook 中返回 `"ask"`，触发状态栏 `Attention` 与 `<ApprovalGateCard />` 人工确认。
   - **本地网关与连接复用**：Go 网关仅监听 `127.0.0.1`，反向代理 `http.Transport` 必须配置 `Proxy: nil`（防系统代理 502）与 `FlushInterval: -1`（SSE 零缓冲）并复用长生命周期 `http.Transport`（严禁单次请求新建 Transport 导致连接泄漏）；读取外部 HTTP 响应或进程输出必须使用 `io.LimitReader` 限制体积。
   - **网络外发边界 (`Network Egress Boundary`)**：Hoxi 为开源本地工具，不提供托管的 AI 服务，AI 请求直连用户配置的提供商或 CLI，不经 Hoxi 中转。网关辅助外发（匿名启动上报、更新检查、账号登录、模型价格/排行榜同步）严禁携带提示词、代码、Diff 或会话内容；后台发起的 LLM 调用必须在 UI 中明确说明目的地，不得静默外发代码上下文。
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
   - 每次代码变更完成后，必须通过类型检查、行数与架构边界门禁、语言包校验、单元测试与构建验证（前端执行 `npm --prefix web run lint && npm --prefix web test && npm --prefix web run build`，后端执行 `go vet ./... && go test ./... && go build ./...`，移动端执行 `flutter analyze client && (cd client && flutter test)`），确保零编译错误、零越界引用、零词条缺失与测试全绿。

10. **Git 提交共同作者署名规范 (Git Commit Co-Author Attribution)**
    - 凡由 Hoxi 参与开发的项目（包括当前仓库本身及通过 Hoxi 开发的任意工作区），在执行 Git 提交时（含 Worktree UI 提交、会话合并提交、移动端语音提交及 Agent 执行 `git commit`），必须在 Commit Message 尾部追加共同作者尾注：`Co-authored-by: Hoxi <code@hoxi.ai>`。

11. **移动端「沟通即操作系统」与全双工实时语音约束 (Mobile Chat-as-the-OS & Full-Duplex Voice Constraints)**
    - **语音与聊天直达优先**：移动端（`client/`）新增任何查询或控制能力时，必须优先接入 [`ChatInformationEngine`](client/lib/features/chat_hub/data/chat_information_engine.dart)（全域情报采集与 System Prompt 注入）与 [`ChatActionExecutor`](client/lib/features/chat_hub/data/chat_action_executor.dart)（聊天/语音内直达执行），确保用户无需跳转二级页面，仅凭语音或打字即可了解业务进展并控制 `Hoxi 本地电脑` 与 `GoPanel 服务器`。
    - **全双工常驻音频与开口即打断（Hardware AEC & Voice Barge-in）**：实时语音通话必须保持单一 `.voiceChat` 原生音频引擎常驻（严禁每轮对话销毁重建 `AVAudioEngine` 导致断连或回声自激），必须支持 `onUserBargeIn`（AI 播报时用户直接开口说话或点击即刻中止当前 TTS 与 SSE 生成流，无缝切入新一轮倾听）。

---

## Where Does New Code Go? (新增代码归属决策表)

为防止架构漂移，新增功能或重构时**必须**按以下决策表归位，并优先参考对应的典范文件（Canonical Example）：

| 当你需要新增 / 修改... | 必须放入的位置 (Target Location) | 严禁放入的位置 (Forbidden) | 典范参考 (Canonical Example) |
| :--- | :--- | :--- | :--- |
| **Go 独立业务域与 HTTP 路由** | `internal/<domain>/` 独立子包，在 [`internal/gateway/gateway.go`](internal/gateway/gateway.go) 注册 | 严禁塞入 `internal/provider` 或 `internal/gateway` | [`internal/worktree/`](internal/worktree/manager.go) |
| **Go 网关自发轻量 LLM 调用** | 领域包接收 `gateway` 注入的 [`llm.Client`](internal/llm/llm.go)，不 `import provider` | 严禁业务包直接 `import internal/provider`；严禁手写 `http.Post` 拼接 `/v1/messages` 或 `/chat/completions` | [`internal/promptassist/`](internal/promptassist/service.go)、[`internal/gateway/assist.go`](internal/gateway/assist.go) |
| **直连 OpenCode (`opencode serve`)** | 一律使用 [`internal/opencode/ocapi`](internal/opencode/ocapi/client.go)（共享 `Transport`、Basic Auth 与 `LimitReader`） | 严禁自建回环 `http.Client`/`Transport` 或手动调用 `SetBasicAuth("opencode", ...)` | [`internal/opencode/ocapi/client.go`](internal/opencode/ocapi/client.go) |
| **`~/.hoxi` 与引擎路径解析** | 一律使用 [`internal/hoxipaths`](internal/hoxipaths/hoxipaths.go)（`DBPath`、`EngineDataDir`、`EngineStateDir`、`Home`） | 严禁在各包硬编码 `~/.hoxi/hoxi.db`、`~/.hoxi/engine-data` 或重复解析环境变量 | [`internal/hoxipaths/hoxipaths.go`](internal/hoxipaths/hoxipaths.go) |
| **外部 CLI 会话扫描 / 凭证桥接** | `internal/handoff/<tool>.go` 单文件高内聚 | 严禁把工具 A 的函数塞进工具 B、`types.go` 或 `service.go` | [`internal/handoff/codex.go`](internal/handoff/codex.go) |
| **多运行时 Agent 子进程驱动** | `internal/runner/` 对应进程 adapter 与协议 translator | 严禁侵入或修改 `internal/opencode` | [`internal/runner/`](internal/runner/http.go) |
| **前端 HTTP / REST / SSE 客户端** | `web/src/services/`，复用 [`requestJSON`](web/src/services/http.ts) | 严禁反向导入 `store/` 或 `components/`；严禁混入纯 UI 工具 | [`web/src/services/http.ts`](web/src/services/http.ts) |
| **前端响应式状态与纯领域计算** | `web/src/store/<domain>.ts`，在 [`workspace.ts`](web/src/store/workspace.ts) 底部扁平导出 | 严禁反向导入 `components/`；严禁新建 `*-storage.ts` 或二级 `export *` | [`web/src/store/git-commit.ts`](web/src/store/git-commit.ts) |
| **前端独立业务弹窗 / 功能面板** | `web/src/components/<domain>/`（`models/`, `handoff/`, `settings/`, `approval/`） | 严禁将非布局类的业务 Modal 塞进 `components/layout/` | [`web/src/components/handoff/`](web/src/components/handoff/ExternalHandoffModal.tsx) |
| **前端多语言词条** | 同步写入 `web/src/i18n/locales/en-US/<domain>.ts` 与 `zh-CN/<domain>.ts` | 严禁手写维护冗长 interface（由 [`types.ts`](web/src/i18n/types.ts) 自动推导） | [`web/src/i18n/types.ts`](web/src/i18n/types.ts) |
| **移动端全项目情报查询与语音播报** | [`client/lib/features/chat_hub/data/chat_information_engine.dart`](client/lib/features/chat_hub/data/chat_information_engine.dart) | 严禁在 UI Widget 中硬编码散乱的业务拼接逻辑 | [`ChatInformationEngine`](client/lib/features/chat_hub/data/chat_information_engine.dart) |
| **移动端语音/聊天控制 Hoxi 电脑与 GoPanel** | [`client/lib/features/chat_hub/data/chat_action_executor.dart`](client/lib/features/chat_hub/data/chat_action_executor.dart) | 严禁要求用户必须退出聊天去多层二级菜单才能执行常规控制动作 | [`ChatActionExecutor`](client/lib/features/chat_hub/data/chat_action_executor.dart) |
| **移动端全双工语音与端到端 Realtime 模型** | [`chat_voice_service.dart`](client/lib/features/chat_hub/data/chat_voice_service.dart) + [`realtime_voice_model_service.dart`](client/lib/features/chat_hub/data/realtime_voice_model_service.dart) + [`chat_hub_voice_call_sheet.dart`](client/lib/features/chat_hub/presentation/widgets/chat_hub_voice_call_sheet.dart) | 严禁在每轮语音回合反复销毁重建原生 `AVAudioEngine` | [`ChatHubVoiceCallSheet`](client/lib/features/chat_hub/presentation/widgets/chat_hub_voice_call_sheet.dart) |

---

## Domain Documentation Routing (专项文档按需索引)

修改以下特定子系统前，请先按需阅读对应的专项契约文档（Single Source of Truth）：

- **整体架构、OpenCode REST/SSE 协议契约、Worktree 状态机与移动端实时语音架构** → [`DEVELOPMENT.md`](DEVELOPMENT.md)
- **Hoxi ⇄ GoPanel 远程隧道契约（`internal/tunnel` + 网关路由白名单，双仓同步）** → [`docs/gopanel-hoxi-remote-tunnel.md`](docs/gopanel-hoxi-remote-tunnel.md)
- **移动端 Hoxi App（沟通 · 工程 · 专栏 · 我的）产品形态、6 大业务情报与双节点声控指南** → [`client/README.zh-CN.md`](client/README.zh-CN.md)、[`client/README.md`](client/README.md)
- **跨 CLI 会话接管（Handoff IR）与本地 CLI 协议桥接** → [`HANDOFF_AND_CLI_BRIDGE.md`](HANDOFF_AND_CLI_BRIDGE.md)
- **内置无头 Chrome 浏览器检视器、人工接管与安全边界** → [`BROWSER.md`](BROWSER.md)、[`internal/browser/README.md`](internal/browser/README.md)
- **统一会话体系与多运行时（Claude Code / Antigravity `agy`）执行引擎、PreToolUse 审批门控** → [`docs/handoff/2026-10-19-session-ownership.md`](docs/handoff/2026-10-19-session-ownership.md)、[`docs/antigravity-runner-probe.md`](docs/antigravity-runner-probe.md)、[`docs/antigravity-approval-hook.md`](docs/antigravity-approval-hook.md)、[`docs/claude-code-approval-hook.md`](docs/claude-code-approval-hook.md)
- **macOS 自动更新（Sparkle）、签名公证与一键发版流水线** → [`docs/auto-updates.md`](docs/auto-updates.md)、[`docs/direct-distribution-readiness.md`](docs/direct-distribution-readiness.md)、[`docs/releases.md`](docs/releases.md)
