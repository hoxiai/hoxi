# AGENTS.md

`Hoxi` 是面向独立开发者的本地多 Agent 研发工作台，由三部分组成：**macOS 桌面端**（本仓库根目录，Go + Wails + SolidJS）、**Flutter 手机端**（[`client/`](client/)）、以及配套的 **GoPanel 服务器面板**（独立仓库 `gopanel_v2`）。手机端经 GoPanel 反向隧道远程查看与控制 Hoxi 桌面。

本文分三段：**现状**（已实现，可在代码或测试中找到）、**边界与约束**（写代码时必须遵守）、**规划**（未实现，不能当作现有能力引用）。手机端细节见 [`client/AGENTS.md`](client/AGENTS.md)；远程通道以 [远程隧道契约](docs/gopanel-hoxi-remote-tunnel.md) 为准。文档与代码冲突时以代码和测试为准，并同步修正文档。

---

## 一、现状

### 1.1 桌面端

- **宿主**（`internal/app`）：Wails v2（macOS `WKWebView` / Windows `WebView2`），窗口半透明、单实例锁、macOS 与 Windows 下关窗隐藏驻留；WebView 请求经 Wails AssetServer 进程内交给网关，另在回环地址监听桥接端口（release `127.0.0.1:5410`、dev `127.0.0.1:5420`，被占用时回落随机端口）供 WebSocket 与 CLI 使用
- **状态栏 / 系统托盘**（`internal/statusbar`）：macOS `NSStatusBar` 与 Windows `Shell_NotifyIconW` 原生系统托盘，显示 `Running` / `Attention` 状态徽标与快捷菜单；新增待审批项时只回调一次，不含命令文本（经隧道转为手机推送）
- **OpenCode 运行时**（`internal/opencode`）：官方 `opencode` 进程监管，每次启动生成随机 `OPENCODE_SERVER_PASSWORD` 并以 HTTP Basic Auth 访问；`opencode serve` 启动后按 PID、启动时间与参数序列 `serve --hostname 127.0.0.1 --port <port>` 登记到 `~/.hoxi/reclaim.json`（`internal/reclaim`），进程被回收后注销。Hoxi 未经正常退出（崩溃、`kill -9`）留下的守护进程由下次启动按账本回收，只处理所属 Hoxi 进程已退出的记录，三项都一致才结束进程；不扫描进程表，另一个仍在运行的变体、用户自己或其他工具启动的 `opencode serve` 都不会被结束
- **多运行时引擎**（`internal/runner`）：统一驱动 Claude Code、Antigravity（`agy`）、Gemini CLI（`gemini`）、Qoder、CodeBuddy 子进程，翻译为统一事件流；Claude Code、Antigravity 与 Gemini CLI 经 PreToolUse / BeforeTool Hook 接入同一审批平面，审批决策统一由 `internal/runner/approval_decision.go` 判定，策略存于 `~/.hoxi/approval-policy.json`（另支持用户显式开启轻量模型审查 `aiReview` + `aiReviewModel`，由 `internal/runner/approval_ai_review.go` 在静态高危与破坏性底线通过后对 `bash -c`、复杂循环等不透明命令做只读判定，超时或存疑一律回退人工确认）。审批模式含 `unattended`（无人值守，仅桌面端可开，且要求本机沙箱可用）：新建会话必须是隔离 worktree 加命令沙箱，否则拒绝创建。Claude Code、Antigravity、Gemini CLI、Qoder、CodeBuddy 的整个运行时进程由 `confineRuntimeCommand` 包进 Seatbelt；OpenCode 只有 bash 工具经沙箱插件包裹，文件工具跑在守护进程里，因此不享受无人值守的全部放行，仍按完全信任规则询问。Qoder / CodeBuddy 没有 PreToolUse 门控能力，无法执行任何模式都保留的硬底线审批，因此只允许在只读模式（Plan）运行，非只读模式一律拒绝启动
- **前端工作区**（`web/`）：Vite + SolidJS + Tailwind 三栏布局；SSE 增量经 `requestAnimationFrame` 批处理；Subagent 嵌套时间线、`ThoughtBox`、`ToolCard`（含内联 LSP 诊断）；`@pierre/diffs` 代码审查、`@xterm/xterm` + WebGL 终端、无头 Chrome 浏览器（`internal/browser`；异常退出留下的 Chrome、配置目录与 OpenCode 插件目录记在 `~/.hoxi/reclaim.json`，由 `internal/reclaim` 按所属 Hoxi 进程登记，下次启动只回收所属进程已退出的记录，PID、启动时间与 `--user-data-dir` 都一致才结束进程，不扫描进程表或临时目录），以及经 Chrome Link 扩展（`extensions/chrome`，手动加载的未打包扩展）接管用户已打开的 Chrome 标签页：桌面签发 2 分钟有效的一次性连接码，扩展经 `/api/hoxi/browser/extension` WebSocket 连入，仅接受 `chrome-extension://` 来源与回环地址，同一时间只允许一个连接，断开后不会静默回落到隔离浏览器（详见 [`BROWSER.md`](BROWSER.md)）
- **原生电脑控制**（`internal/computer`）：macOS（Accessibility / ScreenCaptureKit / CGEvent）与 Windows（UI Automation / PrintWindow / SendInput）的前台窗口读取与输入，首个模型入口为 OpenCode `hoxi_computer`；桌面面板按会话与工作树授权单个运行中应用，并要求原生弹窗确认。每次输入通过统一审批平面的本机「允许一次」，不支持远程批准、始终允许或无人值守输入。可暂停、撤销或停止全部授权；只截目标窗口，不回落整个桌面。Windows 在 Mac 上仅交叉编译，仍需真机验收；详见 [`COMPUTER.md`](COMPUTER.md)
- **会话能力**：Git Worktree 会话隔离（`internal/worktree`，`?directory=` 作用域）、运行中纠偏（Steer）与停止、按消息回滚（Revert）与分叉（Fork）、会话目标 `/goal`（按整轮判定进展，智能体以回复末行 `[[goal:complete]]` / `[[goal:blocked]]` 或 `goal_report` 工具上报，所有运行时可用）；runner 在原生 CLI 运行时首轮与切换运行时时注入 `<project>/.hoxi/memory/` 记忆。8 维度压缩提示词、密钥脱敏与 `project.md` 冻结抽为共享模块 `.opencode/lib/memory-compaction.js`，由桌面端 OpenCode 插件（`internal/opencode/context_compaction.js`）与仓库级 `.opencode/plugins/hoxi-memory.js` 共用；会话级压缩落盘（`<project>/.hoxi/memory/<session>.md`）与 `hoxi-goal.js` 位于 `.opencode/plugins/`，在 OpenCode 打开本仓库时加载
- **外部 CLI 会话接管**（`internal/handoff`）：从 Claude / Codex / Gemini / Grok / OpenCode / Trae / Qoder / CodeBuddy 导入会话
- **Agent Skills 与工具网桥**（`internal/skills` + `internal/tools` + `internal/workapps`）：本地 Skill 发现（OpenCode `/skill`）、内置默认 Skill `skill-authoring` 与 `tools`（`internal/skills/defaults/`，按内置 `version` 自动升级并清理废弃目录，配套本机免 Key 工具网桥 `internal/tools` `/api/hoxi/tools/{discover,inspect,run}` 与 Work 应用状态 `internal/workapps` `/api/hoxi/work/apps`），以及 hoxi.ai 线上技能目录（`GET /api/hoxi/skills/marketplace`）与显式安装（`POST /api/hoxi/skills/marketplace/install`，校验 `sha256` 后原子写入 `~/.hoxi/skills/<slug>/`，不自动安装、不执行远程脚本、不经隧道开放）
- **GoPanel 反向隧道**（`internal/tunnel` + `internal/gateway/tunnel.go`）：Mac 主动外连 GoPanel，路由白名单内的 HTTP / SSE 在进程内交给网关；Mac 不开放公网端口。待处理审批经 Notify 帧交给 GoPanel 发 APNs；无人值守或出现审批超时的任务结束时，另发一条 `kind: "summary"` 计数摘要（放行 / 拒绝 / 超时 / 待处理数量，同时弹桌面通知）
- **局域网配对二维码**（`internal/pairing` + 设置 → 远程）：桌面端生成含本机局域网 IPv4、端口 `5417` 与配对令牌的二维码（`GET /api/hoxi/pairing/qr`，返回 data:PNG），手机 App 扫码后经局域网直连；桌面监听 `:5417`（`internal/gateway/lan.go`）用配对令牌鉴权并复用远程路由白名单，仅服务局域网来源地址（loopback / RFC1918 / ULA / 链路本地 / CGNAT），令牌存 `~/.hoxi/pairing.json`（0600，属受保护状态文件）。配置了 Cloudflare Tunnel 时二维码另带 `relay`（公开主机名）与独立的 `relayToken`（中继令牌，与局域网令牌不同，同存 `~/.hoxi/pairing.json`），手机据此做「局域网优先、失败回落中继」的传输选路，走中继时只发 `relayToken`。面板打开即显示二维码，并支持**重新生成令牌**（`POST /api/hoxi/pairing/rotate`，局域网与中继两个令牌一起换，旧码立即失效）。该路由与 `5417` 监听均**不对隧道开放**；`5417` 为明文 + 令牌（跨网端到端加密仍为规划，见 [`docs/remote-access-e2e-contract.md`](docs/remote-access-e2e-contract.md)）
- **反向代理入口**（`internal/gateway/ingress.go`）：本机 loopback `127.0.0.1:5418` 上另开一个入口，供 `cloudflared` 等本地转发器把公网请求代理进来，`cloudflared` 的 `service` 指到这里；复用同一份远程路由白名单，但只认中继令牌（`relayToken`，局域网令牌在此一律 401，`servePairedRequest` 按来源区分），**不做来源地址过滤**（公网可达性由转发器前置鉴权把关，如 Cloudflare Access）。该入口同样不对 GoPanel 隧道开放
- **Cloudflare Tunnel 连接器**（`internal/cloudflared` + 设置 → 远程）：受监管的 `cloudflared` 进程，以 `--no-autoupdate tunnel run` 运行并经环境变量 `TUNNEL_TOKEN` 传入凭据（不出现在进程参数中）、断线退避重启；`connector token` 存 `~/.hoxi/cloudflared.json`（0600，属受保护状态文件），HTTP API 只回状态、从不回令牌；二进制仅在 `HOXI_CLOUDFLARED` / PATH / `~/.hoxi/bin` 解析，不自动下载。管理路由 `/api/hoxi/cloudflared/*` 桌面专属、不对隧道开放
- **本机模型中转**（`internal/provider/relay.go` + `relay_keys.go`）：在回环地址上暴露 OpenAI 兼容的 `GET /v1/models` 与 `POST /v1/chat/completions`，供 Cline / Continue / Cherry Studio / Open WebUI / aider 等本机工具复用已配置服务商；只重写 `model` 字段并注入上游 Key，body 与 SSE 原样透传（tools / 多模态不被翻译层破坏）。认证使用独立 `hk-*` Key（`~/.hoxi/relay-keys.json` 仅存 SHA-256 哈希，明文只在创建时返回一次），不把上游凭据下发给客户端；`hoxi-auto` 走自动路由池。该平面**永不进入隧道白名单**
- **官方账号 AINode 直连**（`internal/provider/official.go`）：用桌面端已登录的 Hoxi 账号 device token（经 `SetAuthSource` 注入 `internal/auth`，provider 不 import auth）走 ainode 租户路径——`GET /api/hoxi/provider/official/models` 预览该账号可调用的模型与积分价，`POST /api/hoxi/provider/official/connect` 用会员 Key（`/api/hoxi/member/keys`）与渠道模型（`/api/hoxi/member/models`）直接原子写入**独立** provider `hoxi-official`（与第三方 `ainode` 网关 provider 分开，互不覆盖/混显）；退出登录或 401 失效走 `InvalidateDeviceToken` 与 `ClearOfficialProvider` 同步清理已落盘的 `hoxi-official` 配置及引擎 `auth.json` 凭据，凭据只在 Go 侧注入、不下发渲染层，路由不经隧道开放
- **模型池候选熔断与选模**（`internal/provider/auto_router.go` + `breaker.go`）：`hoxi-auto` 自动池按策略（`round-robin` / `priority` / `smart-health`）选模；按 `provider/model` 记录失败分类（transient / rate_limit / quota / config）与延迟 EWMA，失败即冷却、到期自愈（状态存 `~/.hoxi/provider-breakers.json`，配置类错误也会过期）。**所有策略**都跳过熔断中的候选与比最快健康候选慢 3 倍以上的慢模型，`smart-health` 额外按健康分排序（快且稳优先）。判定的延迟口径为 TTFT，会话侧（`web/src/store/auto-breaker.ts`）消费同一快照并经 `POST /api/hoxi/provider/candidate-outcome` 回报结果，路由不经隧道开放。会话侧（`web/src/store/auto-models.ts`）另做**会话粘性选模**：同一会话在某池选中候选后，后续轮次只要该候选仍健康、未被排除就继续复用（保住上游 prompt 缓存前缀），仅在冷却 / 不可用 / 被剔除时才轮换；粘性命中不推进轮询游标，因此不同会话仍会分到不同候选，无 `sessionID` 的调用方（池预览、后端任务）保持原有轮询；另支持按 MCP OAuth 2.1 PKCE（`POST /api/hoxi/provider/mcp-oauth/start`、`GET /api/hoxi/provider/mcp-oauth/poll`，`internal/provider/mcp_oauth.go`）一键连接兼容服务商并在后端原子落盘凭据、模型列表与远程 MCP 配置（poll 响应不回传明文 Key/Token）

### 1.2 手机端（`client/`）

Flutter + Riverpod + GoRouter + Dio，四栏：沟通 · 工作台 · 一人公司 · 我的。已实现并有测试：节点连接与代次隔离、经 GoPanel 隧道访问 Hoxi（HTTP / SSE）、模型工具调用意图识别 + 关键词回退、修改类操作确认卡片、Hoxi 编码会话 / 审批 / Git 审查、GoPanel 资源页、iPhone 审批提醒推送。手动开启的连续语音通话与 iOS Realtime 语音客户端有代码，未做真机验收。完整清单与测试对应关系见 [`client/AGENTS.md`](client/AGENTS.md)。

### 1.3 目录结构

```text
/
├── main.go                           # Thin Wails embed.FS entrypoint delegating to internal/app
├── cmd/hoxi/main.go                  # CLI / headless entrypoint calling internal/app
├── internal/
│   ├── app/                          # DesktopApp lifecycle, Wails bindings, origin allow-list & signal handling
│   ├── agentcli/                     # [底座层] Local agent CLI (Gemini/Qoder/CodeBuddy) binary & model discovery + effort helpers
│   ├── agyhook/                      # [底座层] `hoxi agy-hook` Antigravity PreToolUse hook → runner approval gate
│   ├── attachment/                   # [业务层] Multimodal file/image attachment staging, validation & store
│   ├── auth/                         # [业务层] Hoxi account one-click authorization, token poll/exchange & store
│   ├── browser/                      # [业务层] Headless Chrome session manager, tabs, actions & inspector bridge
│   ├── buildinfo/                    # [底座层] Build version embedded by the desktop build
│   ├── claudeconfig/                 # [底座层] Claude Code model/config discovery & selection
│   ├── cli/                          # Terminal-first `hoxi` CLI (REPL, SSE streaming renderer, subcommands & gateway client)
│   ├── cloudflared/                  # [业务层] Supervised Cloudflare Tunnel connector: binary resolution, token store & process restart
│   ├── computer/                     # [业务层] Session-scoped native macOS / Windows app inspection and foreground input
│   ├── codeindex/                    # [业务层] Tree-sitter / symbol & trigram workspace code index
│   ├── diagnostics/                  # [底座层] Gateway & runtime error ring-buffer, health diagnostics & telemetry
│   ├── forge/                        # [业务层] GitHub / GitLab PR, issue & CI review integration
│   ├── gateway/                      # In-process reverse proxy, Basic Auth, WS/SSE bridge, tunnel route allow-list, dev pulse & security
│   ├── handoff/                      # [业务层] Cross-CLI session handoff (Claude/Codex/Gemini/Grok/OpenCode/Trae/Qoder/CodeBuddy) + local CLI bridge & materializer
│   ├── hoxipaths/                    # [底座层] Unified ~/.hoxi, hoxi.db & engine-data/state path resolution
│   ├── leaderboard/                  # [业务层] Model / endpoint leaderboard aggregation & cache
│   ├── llm/                          # [底座层] Narrow Client/Target/Request contract for internal single-shot LLM calls
│   ├── notify/                       # [底座层] Native desktop notifications (macOS) with stub fallback
│   ├── osopen/                       # [底座层] Native file opening and file-manager reveal (ShellExecute on Windows)
│   ├── sqlitecli/                    # [底座层] Resolve bundled Windows SQLite CLI with PATH fallback
│   ├── opencode/                     # [业务层] OpenCode binary resolver, installer, daemon supervision & Hoxi plugin overlay (browser, code_index, tools, compaction)
│   │   ├── ocapi/                    # [底座层] Shared loopback HTTP/SSE client & Basic Auth transport for `opencode serve`
│   │   └── ocstate/                  # [底座层] External OpenCode SQLite reader & Console account sync into hoxi.db
│   ├── orchestration/                # [业务层] Multi-agent DAG convoys on the runner session plane: per-task worktrees & dependency merge
│   ├── pairing/                      # [业务层] Local-network pairing QR: LAN IPv4 discovery, persisted pairing token & PNG QR
│   ├── promptassist/                 # [业务层] Composer prompt enhancement & session title summarization via llm.Client
│   ├── provider/                     # [业务层] Provider config, models.json, llm.Client implementation, probing, candidate breaker & model stats
│   ├── recall/                       # [业务层] Cross-session semantic / BM25 memory recall & indexing
│   ├── reclaim/                      # [底座层] Per-owner ledger (~/.hoxi/reclaim.json) of temp Chrome / OpenCode daemon processes & dirs; reclaims only records of exited Hoxi processes
│   ├── runner/                       # [业务层] Multi-runtime execution engine (OpenCode/Claude/Antigravity/GeminiCLI/Qoder/CodeBuddy): process adapters, translators, hook gates, session store & revert
│   ├── sandbox/                      # [业务层] OS-level Seatbelt / Bubblewrap execution sandbox policy & profiles
│   ├── skills/                       # [业务层] Local Agent Skills discovery + hoxi.ai catalog client & verified installer + embedded defaults
│   ├── statusbar/                    # [底座层] Running/Attention tracker, attention notices & macOS NSStatusBar / Windows system tray
│   ├── telemetry/                    # [业务层] Local token/cost/latency metrics & session usage ledger
│   ├── testutil/execstub/            # [测试辅助] Fake CLI executables for tests (symlink to the test binary + sidecar sh script; avoids macOS first-exec scans)
│   ├── textindex/                    # [底座层] Pure-Go BM25 / trigram full-text search index primitive
│   ├── tools/                        # [业务层] Hoxi Tools gateway (/api/hoxi/tools/{discover,inspect,run}) with anonymous fallback on 401
│   ├── tunnel/                       # [业务层] GoPanel reverse tunnel client: pairing, frame protocol, in-process dispatch & Notify
│   ├── updater/                      # [业务层] Native auto-update checker, macOS Sparkle / DMG swap & Windows SHA-256 verified zip self-update
│   ├── workapps/                     # [业务层] Hoxi Work apps catalog & atomic state store (~/.hoxi/work-apps.json)
│   ├── workspace/                    # [业务层] Multi-workspace project profile store & validation
│   └── worktree/                     # [业务层] Git worktree session sandbox manager, diffs, branches, snapshots, review audit & commit messages
├── web/                              # Frontend workspace (Vite + SolidJS + Tailwind)
│   ├── src/
│   │   ├── components/               # Modular UI by domain (layout, timeline, inspector, models, handoff, approval, codeindex, orchestration, skills, tasks, terminal, settings, icons, motion)
│   │   ├── i18n/                     # Reactive t(key, params), auto-inferred types & en-US/zh-CN locale modules
│   │   ├── services/                 # Pure HTTP/REST/SSE/Browser transport clients (http.ts, api.ts, sse.ts, browser.ts, runner-api.ts, platform-api.ts, …)
│   │   ├── store/                    # SolidJS reactive store & domain modules (flat barrel export in workspace.ts)
│   │   └── __tests__/                # Node-based unit/integration tests (sse, session-isolation, permissions, …)
│   └── scripts/                      # check-i18n.ts, check-lines.ts (line & boundary lint), run-tests.ts
├── client/                           # Hoxi Mobile App (Flutter + Riverpod + GoRouter); see client/AGENTS.md
│   ├── ios/Runner/AppDelegate.swift  # iOS .voiceChat audio engine, speech recognition, PCM16 player, barge-in & APNs bridge
│   ├── android/app/src/main/kotlin/  # Android SpeechRecognizer, AudioFocus, AudioTrack & barge-in
│   └── lib/
│       ├── app/                      # 4-tab shell (沟通·工作台·一人公司·我的), router & push launch listener
│       ├── core/                     # Network (SSE & REST ApiClient), StorageService (node activation & generations), push, compliance & theme
│       └── features/
│           ├── chat_hub/             # 沟通：意图识别（模型工具调用 + 关键词回退）、确认计划、查询上下文、语音通话
│           ├── portal/               # 一人公司：hoxi.ai 文章、省钱（专线/模型/套餐）、五维自测与工具箱
│           ├── ai_workspace/         # 工作台：Hoxi (/api/hoxi/*) 与 GoPanel (/api/code/*) 编码会话、Git 审查与终端
│           └── server/container/database/website/ssl/apps/resources/task_center/auth/settings
├── extensions/
│   └── chrome/                       # Chrome Link MV3 extension (manually loaded): worker.js bridge, protocol.js, popup & _locales; node --test *.test.mjs
├── .opencode/                        # Upstream OpenCode config root; Hoxi only reads it, never writes memory here
│   ├── plugins/                      # hoxi-memory.js & hoxi-goal.js
│   ├── lib/                          # permission-policy.js + memory-compaction.js + command-data-args.js + opaque-command.js + shell-substitution.js + shell-constants.js + shell-loops.js + script-interpreter.js + unattended-policy.js + *.cases.json (shared safety-gate & compaction rules)
│   └── skills/                       # Project-local Agent Skills
├── .hoxi/                            # Hoxi-local project state (git-ignored): memory/, worktrees/, attachments/
│   └── memory/                       # project.md & per-session compaction records (legacy .opencode/memory is read-only)
├── docs/                             # Approval hooks, remote tunnel, auto-updates, release & direct-distribution guides (+ assets/)
├── scripts/                          # AI one-click release, DMG packaging, notarization & release publishing
├── README.md / README.zh-CN.md       # Bilingual landing docs
├── BROWSER.md                        # Headless Chrome inspector, Chrome Link, manual takeover & security boundaries
├── HANDOFF_AND_CLI_BRIDGE.md         # Cross-CLI handoff IR, credential discovery & protocol bridges
├── DEVELOPMENT.md                    # Comprehensive implementation blueprint
└── AGENTS.md                         # Engineering rules & quick reference
```

### 1.4 构建与验证命令

- **Desktop Development (Hot Reloading, 本地开发默认优先对接本地 `http://127.0.0.1:5400` 服务端)**:
  ```bash
  wails dev
  # 当本机 apay (http://127.0.0.1:5400) 已启动时，Hoxi Dev 自动将 HOXI_SERVER_URL 指向 http://127.0.0.1:5400
  ```
- **Build Production macOS Application (`.app`)**:
  ```bash
  ./build-desktop.sh
  ```
- **Build Production Windows Installer (`-setup.exe` & `.zip`)**:
  ```powershell
  .\scripts\build-windows.ps1
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

## 二、边界与约束

### 2.1 远程控制与手机端边界

- **信任边界**：配对即授权。能以管理员身份登录已配对 GoPanel 的人，可在隧道白名单内远程操作这台 Mac（新建会话、下发指令、批准审批）。GoPanel 能看到经它转发的明文，没有端到端加密。隧道白名单之外的路由（账号、提供商配置、浏览器、更新器、OpenCode 原始透传等）一律 403，新增经隧道开放的路由必须同步更新契约文档并补测试
- **意图识别**：手机配置模型网关时由大模型通过工具调用提出意图；目标必须与实时数据原样匹配，找不到或有歧义时追问；修改类操作一律走确认卡片；未配置模型或网关不支持工具时回退关键词路径。实现见 `client/lib/features/chat_hub/data/chat_intent_*.dart`
- **数据外发**：手机调用模型时会把会话标题、待审批摘要、最近会话输出片段、节点资源名称等上下文发给用户配置的模型网关。目前 UI 未说明目的地与范围，列为待办（见规划）；在此之前不要扩大发送范围
- **提示词注入**：会话内容可能诱导模型调用工具。当前由目标校验与确认卡片兜底；桌面端 PreToolUse 审批门控与破坏性命令审批仍是最终防线，不能因手机端有确认而放宽
- **推送**：只发计数——待审批时发“有几项待处理”，无人值守或出现审批超时的任务结束时发一条摘要（`kind: "summary"`，正文形如“任务已结束：自动放行 X，拒绝 Y，待处理 Z”）；载荷不含命令、代码、路径、会话标题或会话内容；目前仅 iOS APNs，Android 未实现
- **语音入口**：iOS 不允许第三方 App 后台持续监听自定义唤醒词，可行入口是 Siri / App Intents、操作按钮、小组件和推送点开
- **业务归节点**：垂直业务（Shoply、轻铺等）的私有 API 与凭据只在桌面 / GoPanel 侧的 Skill 运行环境；手机不实现其 CRUD 页面，也不保存其凭据

### 2.2 工程与功能约束

1. **按领域高内聚、单向分层边界与单文件 800 行上限 (Domain Cohesion, Unidirectional Layering & 800-Line Limit)**
   - **领域高内聚与禁止张冠李戴**：模块拆分以**单一职责与业务领域高内聚**为第一原则，**严禁为了凑行数把属于模块 A 的函数跨文件塞进无关的模块 B**（如禁止把 Codex/Gemini/Claude 逻辑塞进 Grok 或 `types.go`/`helpers.go`），也禁止拆分出仅几十行的 `*-storage.ts` 脚手架文件。
   - **单文件 800 行上限**：任何源码文件（`.go`, `.ts`, `.tsx`, `.js`, `.m`, `.dart`）上限为 **800 行**（纯静态图标 SVG 字典或自动生成常量表除外）。当文件接近 800 行时，应按正交子领域或独立子组件/Hook 拆分。
   - **前端严格单向分层 (`components/ -> store/ -> services/`)**：
     - `services/` 仅负责 HTTP/REST/SSE 网络通信与协议传输，**严禁**反向 `import` 任何 `store/` 或 `components/` 模块，也不得混入纯 UI 渲染工具。
     - `store/` 负责响应式状态、领域数据计算与本地持久化，**严禁**反向 `import` 任何 `components/` 模块；所有子模块统一在 [`web/src/store/workspace.ts`](web/src/store/workspace.ts) 底部扁平重导出，禁止在子模块末尾做二级链式 `export *`。
     - `components/` 按业务域正交归位（`layout` 仅放窗口骨架与导航栏，独立业务弹窗归入 `models`、`handoff`、`settings`、`approval`、`codeindex`、`orchestration`、`skills` 等对应目录）。
   - **Go 后端四层单向依赖 (`app/cli -> gateway -> 业务领域包 -> 基础底座包`)**：
     - 业务领域包（`runner`, `handoff`, `worktree`, `provider`, `promptassist`, `browser`, `computer`, `opencode`, `workspace`, `auth`, `leaderboard`, `updater`, `attachment`, `cloudflared`, `codeindex`, `forge`, `orchestration`, `pairing`, `recall`, `sandbox`, `skills`, `telemetry`, `tools`, `tunnel`, `workapps`）与底座包（`hoxipaths`, `llm`, `agentcli`, `opencode/ocapi`, `opencode/ocstate`, `osopen`, `sqlitecli`, `statusbar`, `notify`, `claudeconfig`, `agyhook`, `diagnostics`, `buildinfo`, `textindex`, `reclaim`）**严禁反向 `import` `internal/gateway`、`internal/app` 或 `internal/cli`**。
     - 各子包必须使用各自明确的 Receiver 类型（`*Gateway` / `*Service` / `*Manager`），严禁定义跨包伪装别名（如 `type desktopGateway = Service`）；Hoxi 内部自发 LLM 请求统一由 `gateway` 注入 [`llm.Client`](internal/llm/llm.go)（底层由 `internal/provider` 实现），业务领域包严禁直接 `import internal/provider`。

2. **OpenCode 上游零侵入 (Upstream Zero-Fork)**
   - 严禁 Fork 或修改 `opencode` 内部源码；所有定制能力仅通过 Go 进程监管、反向代理或 `.opencode/plugins/` 插件钩子扩展。
   - **严禁在用户项目里创建 `.opencode/`**：该目录是上游 OpenCode 的配置根，一旦存在，`opencode serve` 会把它当作配置目录并在其中安装 `@opencode-ai/plugin` 依赖（`package.json` + `node_modules`），污染用户仓库。Hoxi 的项目级状态（记忆、工作树、附件）一律写 `<project>/.hoxi/`，路径解析走 [`internal/hoxipaths`](internal/hoxipaths/hoxipaths.go)（`ProjectMemoryDir` / `ProjectSessionMemoryFile`）；旧版 `.opencode/memory/` 仅作为只读回退，不做写入。

3. **三大统一网关平面与 Git Worktree 强隔离 (Unified Gateway Planes & Worktree Scoping)**
   - **会话、事件流与权限审批三大平面统一收口**：前端（[`web/src/services/api.ts`](web/src/services/api.ts)、[`sse.ts`](web/src/services/sse.ts)）与 CLI 的所有会话操作必须统一走 `/api/hoxi/sessions`，SSE 事件流必须统一订阅 `/api/hoxi/event`，权限审批必须统一走 `/api/hoxi/permission`（由 `gateway` 与 `runner` 合并路由 OpenCode 与 Claude/Antigravity/Qoder/CodeBuddy 运行时），**严禁前端直接裸调上游 `/session`、`/event` 或 `/permission` 接口**。
   - **工作树目录强隔离**：所有关联特定会话或沙箱的请求（`/api/hoxi/sessions`、`/api/hoxi/event`、`/api/hoxi/permission`、`/file`、`/pty`）**必须**携带该会话对应的 `?directory=<worktree-path>` 参数，严禁跨会话读写或污染其它工作树。

4. **SolidJS 响应式、生命周期清理与异步防竞态约束 (SolidJS Reactivity, Cleanup & Stale-Read Guard)**
   - **禁止解构响应式源**：严禁在 SolidJS 组件中直接解构 `props` 或 `store` 状态，必须保持细粒度 Signal/Store 访问路径以确保响应式更新。
   - **流式批处理**：`/api/hoxi/event` SSE 高频增量事件（`message.part.delta` / `updated`）必须经 `requestAnimationFrame` 批处理合并后更新 Store，禁止逐 Token 触发全量重绘。
   - **成对清理与防过期响应覆盖**：组件内注册的 `addEventListener`、`ResizeObserver`、定时器及 WebSocket/PTY 连接**必须**在 `onCleanup` 中成对销毁；跨会话或跨目录的异步读取（拉取历史、Diff、诊断等）必须校验当前激活 `sessionID` 或递增请求世代号（Stale-Read Guard），丢弃晚到的过期响应。

5. **任务流状态与回滚闭环 (Task Lifecycle & Control Integrity)**
   - 会话处于 `busy` 时必须支持实时纠偏（`Steer`）与中止（`Stop`），工具卡片（`ToolCard`）与推理抽屉（`ThoughtBox`）必须准确反映 `pending` / `running` / `completed` / `error` 四态与耗时，且状态变更需实时同步至 macOS 状态栏（`Running` / `Attention`）。

6. **安全门控、网络外发边界、子进程消毒与资源边界 (Safety, Egress, Subprocess & Resource Hygiene)**
   - **破坏性操作人工审批**：包含破坏性指令（`rm`, `sudo`, `chmod`, `chown`, `diskutil`, `mkfs`, `git push --force`, `git reset --hard`）的 Shell 操作与敏感文件读取（`.env` 等）严禁自动放行，必须由 Go 审批策略（`approvalAutoApproves` / `isDestructivePermissionRequest`）交给用户，触发状态栏 `Attention` 与 `<ApprovalGateCard />` 人工确认。破坏性判定只扫描命令本身：提交说明、搜索词、`echo` 文本等字面量数据参数先由 `maskCommandDataArguments`（Go `command_data_args.go` / JS `command-data-args.js`）替换后再匹配，无法确定语义（命令替换、heredoc、管道进 Shell/解释器）时不做替换。
     - **唯一例外——用户显式选择的「完全信任」模式（`trusted`，`internal/runner/approval_trusted.go`）**：只放行两类可恢复操作：① 隔离会话 worktree（`.hoxi/worktrees/`）内的 `rm`/`rmdir`/`unlink`/`chmod`（路径须解析后仍在 worktree 内，且不是受保护路径或密钥文件），以及 `git reset --hard`、`git checkout -- <路径>`/`checkout .`、`git restore`、`git clean`；② 删除系统临时目录（`/tmp`、`$TMPDIR`，以及 macOS `/var/folders/<id>/T/<子路径>`，`/private/var/folders` 同形，`T` 目录本身不算）下的具体条目。括号子 shell 按括号内的命令判定，隔离 worktree 里的 `(rm -rf build)` 与没有括号时一样可恢复；`(cd / && …)`、`(cd .. && …)` 和超过 2 层的括号仍然询问。
     - **无人值守模式（`unattended`，`internal/runner/approval_unattended.go`）**：前提是会话位于隔离 worktree 且命令沙箱开启，并且该运行时的进程（或它执行的每条 Shell）已被 Seatbelt 包住。满足时沙箱内的 Shell 放行，包括不透明命令和工作树内可恢复的破坏性操作。硬拒绝四类并立即拒绝、不询问：读取密钥、任何 `git push` 与发布、`sudo`/`su`/`doas`、携带凭据的外发；另外**运行时才拼出的代码一律拒绝**（类别 `dynamic`）：`eval`、把文本经管道 / heredoc / 进程替换 / `/dev/stdin` 交给 Shell 或解释器、命令位置是 `$VAR`/`$(…)`/反引号、`env -S`、`parallel`、`xargs`/`find -exec` 用输入拼出 Shell、解释器或敏感工具的子命令、git 未知子命令（可能是别名）与会执行命令的 `git -c` 配置，以及会起进程、动态加载、读环境变量或联网的解释器内联代码（`python3 -c`、`node -e`、`python3 - <<'EOF'` 等；只做计算和打印的仍放行）。命令先按 Shell 规则去引号、去反斜杠再判定（`git p\ush` 即 `git push`），`bash -c '…'`、`git rebase -x`、`submodule foreach` 的内层命令递归判定；引用凭据名变量或打印整个环境、同时调用联网工具的命令按外发拒绝。实现见 `approval_unattended_parse.go` + `approval_unattended_dynamic.go`，JS 同步 `.opencode/lib/unattended-shell.js` + `unattended-policy.js`，共用用例 `.opencode/lib/unattended.cases.json`。拒绝原因告知模型，并写入桌面端待处理清单（`/api/hoxi/permission/deferred`，不经隧道）。**静态判定是尽力而为**：会话自己写进文件再运行的脚本（`bash x.sh`、`python3 x.py`、`npm run`、`make`）按文件内容执行、内容不判定；用户 `~/.gitconfig` 里的别名只能按「未知子命令」拦。**由沙箱保证的部分**：凭据文件不可读（见下）；写入限于工作树等可写根，`.git/config` 与 hooks 不可写；运行时环境剥除凭据（`internal/runner/sandbox_env.go`：按名称约定 `*TOKEN*`/`*SECRET*`/`*PASSWORD*`/`*API_KEY*`/`*ACCESS_KEY*`/`*_KEY`、`AUTH`/`PAT` 分段，`SSH_AUTH_SOCK`、`GIT_ASKPASS`、`KUBECONFIG`、`GIT_CONFIG_*` 等名单，以及 `ghp_`/`AKIA`/`user:pass@` 等取值形状剔除；用黑名单是因为运行时和工具链依赖大量无法预先列举的普通变量；只保留该运行时调模型所需的 Key，如 Claude Code 的 `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN`/`CLAUDE_CODE_OAUTH_TOKEN`，开启 Bedrock / Vertex 时才保留对应云凭据，代理变量保留）；Seatbelt 拒绝连接 SSH/GPG agent 套接字（launchd 默认 agent、`~/.ssh`、`~/.gnupg`、1Password、Secretive 与当前 `SSH_AUTH_SOCK`）。**沙箱不保证的部分**：网络是放开的（运行时要调模型），运行时保留的模型 Key 仍能被它启动的 Shell 读到并外发；Seatbelt 放行 `com.apple.SecurityServer`，钥匙串在沙箱内可查询，经钥匙串凭据（git `osxkeychain`、`gh` keyring）的 `git push` 或 API 调用只靠静态拒绝拦截；Docker 等守护进程套接字未拦截。命令沙箱禁读 `~/.ssh`、`~/.aws`、`~/.gnupg`、`~/.kube`、`~/.config/gh`、`~/.config/gcloud`、`~/.azure`、`~/.docker/config.json`、`~/.netrc`、`~/.npmrc`、`~/.pypirc`、`~/.git-credentials`、`~/.cargo`、`~/.hoxi` 受保护状态文件，以及工作树根和 `web`/`client`/`server` 下的 `.env`、`.env.local`、`.env.production`、`.env.development`（`.env.example` 仍可读；`~/Library/Keychains` 由审批平面硬拒绝/拦截，Seatbelt 层保留可读以供 `claude`/`agy` 经 `Security.framework` 读取自身 OAuth 凭据）。没有隔离的会话不享受全部放行。开关只能在桌面端设置。
     - **在任何模式下（含完全信任）都必须询问的硬底线**：读取密钥与凭据文件、工作树外的敏感/凭据路径、可控制执行的路径（`.git`、`.opencode`、`.claude`、`.hoxi`、`.agents`、`.envrc`、`.vscode/settings.json`；其中项目内 `.hoxi/attachments/` 常规读写豁免，`.hoxi/skills/` 与 `.agents/skills/` 仅读取与执行既有脚本豁免、写入与修改仍询问）、`sudo`、`git push --force`/删除远端分支、共享 git 状态（分支删除、`stash drop`、`worktree remove`）、发布类（`npm`/`cargo publish`、`terraform apply`、`kubectl delete`）、结束进程（`kill`/`pkill`/`killall`）、磁盘与系统命令、`curl … | sh` 等管道执行、`unsandboxed` 工具和死循环检测。任何无法确定语义的命令（变量展开、子 Shell、命令替换、`cd` 后的相对路径）一律询问；以下写法不算看不清（`internal/runner/opaque_command.go` + `opaque_loops.go` + `opaque_constants.go` + `opaque_expand.go` / `.opencode/lib/opaque-command.js` + `shell-substitution.js` + `shell-constants.js` + `shell-loops.js`，共享用例 `.opencode/lib/opaque-command.cases.json`）：1) 只读循环仍是：同一命令内由安全文件列举（`rg -l`、带扩展名过滤且起始目录非敏感/受保护路径的 `rg --files`/`find`/`git ls-files`）喂给 `while read x` 的循环变量，只出现在只读工具参数里；字面量 `for x in`（含纯数字范围 `{1..N}` 或 `$(seq …)`）在列表全是字面量（不含 `$`、glob、其它命令替换、密钥或受保护路径）且不超过 64 项时，按每个值展开成字面命令再照常判定，可与常量代入叠加，展开后的写文件、覆盖和破坏性命令不因来自循环而放行；超过 64 项不展开、不截断，读循环保持原放行、写循环保持询问；glob 列表、密钥项和 `while read` 保持原规则；2) 同一命令内的常量代入：`NAME=值` 或 `export NAME=值` 且该段只有这一处赋值，值是字面量（单引号，或不含 `$`、反引号的双引号或裸词），或只由无害环境变量与字面量拼接，或第 4 项的取值类 `$(…)`（代入时用占位符，按取值类规则看使用位置）；代入后的文本必须再跑完整的危险判定和密钥判定；变量再次赋值，或被 `read`/`for`/`printf -v`/`declare`/`local`/`unset`/`mapfile` 重新绑定、用在命令名位置、`${x:-…}`/`${x/…}`/数组、值里含 glob、换行或 `$(` 以外的展开、赋值出现在管道或 `&&`/`||` 右侧之后、先用后赋，一律不透明；`set -e`/`set -o pipefail`/`set -x` 无害，其他 `set`（`set -f`、`set +o noglob`、`set -euo pipefail`）不透明；不重新绑定 `HOME`/`PATH` 等无害环境变量名；3) 无害环境变量（`$HOME`、`$PATH`、`$PWD`、`$TMPDIR`、`$GOPATH`、`$?`、`$!` 等，未在命令内重新赋值）只出现在只读工具参数或 `PATH=…`/`export PATH=…` 里；`kill $!` 仍按结束进程的硬底线询问；后台 `cmd &` 本身不造成不透明，前提是 `cmd` 可判定；4) `<(…)`、`$(…)` 拆出内部命令照常判定，`$(…)` 的结果可以交给 `echo`/`printf`/`test` 或 `git commit -m` 的提交说明；内部命令本身可判定且属于取值类（`git rev-parse`/`write-tree`/`hash-object`/`merge-base`/`symbolic-ref`/`describe`、`pwd`、`date`、`go env`、`which`、`command -v`、`ps -o …=`、`dirname`、`basename`、`wc` 计数标志 `-l`/`-c`/`-w`/`-m`、`grep`/`rg -c`、以及仅含只读标志且带 `-o <临时路径|/dev/null>` 与 `-w`、方法限 GET/HEAD、不含 `-H` 的取值类 `curl`）时，结果还可以出现在只读工具（及 `mkdir`）的参数里；`git hash-object -w` 按写入对象库放行但不算危险，`go env -w`/`-u` 不算取值类；其他内部命令、结果用在命令名位置、结果交给 `git`/`find`/`rm`/`cp`/`mv`（或把非取值类 `$(…)` 交给 `curl`）仍询问；5) 括号子 shell `(…)` 作为独立命令递归判定，`cd` 的作用域只在括号内且必须仍在 worktree 或项目内，括号内不透明则整体不透明，嵌套超过 2 层保持不透明；`$( )`、`<( )`、`{ …; }` 不按这条展开；6) 反斜杠续行合并，`nohup`/`time`/`exec`/`command`/`env X=y` 前缀剥掉后判定实际命令（裸 `env` 会打印全部环境变量，仍询问）；7) 引号定界的 heredoc 当作数据，由读取它的命令决定（喂给 `sh`/`bash`/`python3 -` 等仍询问）。只读工具另包括 `lsof`、`ps`、`du`、`df`、`cut`、`tr`、`nl`、`column`、`shasum`、`sha256sum`、`md5`、`xxd`、`od`、`sleep`（`tail -f` 仍放行，挂起交给超时）、只读行号范围 `sed -n '<N,Mp>'` 以及仅调用 `basename`/`dirname`/`echo` 的 `xargs`。读取发起请求的会话自己的 `~/.hoxi/sessions/<本会话 ID>/`（同时匹配 Hoxi 会话 ID 与运行时 ExternalID；OpenCode `external_directory` 与读取类）放行，编辑和写入仍询问；其他会话的目录、`~/.hoxi/*`、受保护状态文件、请求里没有会话 ID 时继续询问。完全信任模式额外放行内联解释器脚本（`python3 -c`、`node -e`、`python3 - <<'EOF'`），与本就放行的 `python3 script.py` 一致；Shell（`sh`/`bash`/`eval`）、非只读 `xargs`、awk 程序和仍会展开的 heredoc 在任何模式下都询问。完全信任模式只能在桌面端开启，不经隧道或局域网修改。
   - **本地网关与连接复用**：Go 网关仅监听 `127.0.0.1`，反向代理 `http.Transport` 必须配置 `Proxy: nil`（防系统代理 502）与 `FlushInterval: -1`（SSE 零缓冲）并复用长生命周期 `http.Transport`（严禁单次请求新建 Transport 导致连接泄漏）；读取外部 HTTP 响应或进程输出必须使用 `io.LimitReader` 限制体积。
   - **网络外发边界 (`Network Egress Boundary`)**：Hoxi 为开源本地工具，不提供托管的 AI 服务，AI 请求直连用户配置的提供商或 CLI，不经 Hoxi 云端中转。唯一的「中转」是本机回环的 OpenAI 兼容中转（`internal/provider/relay.go`），它同样把请求原样发往用户自己配置的服务商，且不经隧道开放。网关辅助外发（匿名启动上报、更新检查、账号登录、模型价格/排行榜同步）严禁携带提示词、代码、Diff 或会话内容；后台发起的 LLM 调用必须在 UI 中明确说明目的地，不得静默外发代码上下文。经 GoPanel 隧道的推送通知只携带待处理数量、任务结束摘要计数与标识，不含命令、代码、路径或会话内容。
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
   - 每次代码变更完成后，必须通过类型检查、行数与架构边界门禁、语言包校验、单元测试与构建验证（前端执行 `npm --prefix web run lint && npm --prefix web test && npm --prefix web run build`，后端执行 `go vet ./... && go test ./... && go build ./...`，移动端执行 `python3 client/scripts/check_source_lines.py && flutter analyze client && (cd client && flutter test)`），确保零编译错误、零越界引用、零词条缺失与测试全绿。

10. **Git 提交共同作者署名规范 (Git Commit Co-Author Attribution)**
    - 凡由 Hoxi 参与开发的项目（包括当前仓库本身及通过 Hoxi 开发的任意工作区），在执行 Git 提交时（含 Worktree UI 提交、会话合并提交、移动端语音提交及 Agent 执行 `git commit`），默认在 Commit Message 尾部追加共同作者尾注：`Co-authored-by: Hoxi <code@hoxi.ai>`。桌面端与 Runner 提交遵循桌面 Git 署名配置（`~/.hoxi/git-attribution.json`，`GET/PUT /api/hoxi/git/attribution`）：默认 `auto` 只给 Hoxi 隔离会话工作树（`<project>/.hoxi/worktrees/*`）的提交署名，用户自己仓库里的提交保持原样，避免给上游/客户仓库留下意外尾注；`always` 全部署，`never` 全部关闭。旧版布尔 `{"enabled":bool}` 仍可读（true→always、false→never）。

11. **移动端控制链路与语音约束 (Mobile Control & Voice Constraints)**
    - **聊天内直达，但修改必须确认**：移动端新增查询能力接入 [`ChatInformationEngine`](client/lib/features/chat_hub/data/chat_information_engine.dart)；新增控制动作放入 `ChatSessionActions` / `ChatInfrastructureActions`，同时接入工具清单（`chat_intent_tools.dart` + `chat_intent_dispatcher.dart`）与关键词路径（[`ChatActionExecutor`](client/lib/features/chat_hub/data/chat_action_executor.dart)），修改类动作必须生成确认计划，不得按列表首项默认执行。
    - **语音引擎常驻与打断**：语音通话保持单一 `.voiceChat` 原生音频引擎常驻（严禁每轮对话销毁重建 `AVAudioEngine`），并保留 `onUserBargeIn`（AI 播报时用户开口或点击即中止当前播报与生成流）。
    - **不伪造结果**：未取得真实数据时显示不可用；提交、接收、完成分别表述，不把“已下发”写成“已完成”。

12. **AI 编码红线 (AI Guardrails, 来自真实事故)** —— 详见 [`docs/ai-coding-guardrails.md`](docs/ai-coding-guardrails.md)，修改审批、隧道、工作区/会话迁移、持久化、进程控制前必读
    - **审批策略只解析一次、失败即 ask**：禁止把拆好的命令段重新拼成字符串再解析；含引号/反斜杠/`$(`/反引号等无法确定语义的输入不得走安全白名单；白名单按 token 锚定（禁止 `\b`）；未知工具与网络工具按策略判定，禁止按工具名前缀放行；Go 与 JS 策略必须同步修改并写入两个 fixture：`.opencode/lib/permission-policy.cases.json`（命令级）和 `.opencode/lib/permission-request.cases.json`（请求级）。
- **审批超时只拒绝，永不放行**（`approvalTimeoutSeconds`，默认 600，范围 60–3600）。硬拒绝类（密钥、推送发布、提权、凭据外发）不得被任何模式、会话授权或「始终允许」放宽。待处理清单不经隧道开放。
    - **隧道白名单只用显式「方法 + 路由」清单**，禁止前缀匹配；执行命令、`open` 程序、工作区外文件、凭据与 OpenCode 原始透传永不经隧道开放。
    - **禁止用启发式推断破坏性操作**（按数组下标或 `os.Stat` 猜"移动"、"匹配不到就扩大 kill 范围"等）；迁移/删除/杀进程只在显式指令下执行，范围严格限定在本会话。
    - **校验函数不得静默丢弃输入**后交给按位置对比的逻辑；状态文件写入必须原子且处理错误，解析失败先备份、不得覆盖。
    - **后台 LLM 不得自动回退到用户未选择的提供商**。
    - **安全相关改动必须带拒绝路径测试**；Go 改动须通过 `go test -race`；复现测试转为正式回归测试，禁止 `zz_scratch_*` 留在工作区。

### 2.3 新增代码归属

| 当你需要新增 / 修改... | 必须放入的位置 (Target Location) | 严禁放入的位置 (Forbidden) | 典范参考 (Canonical Example) |
| :--- | :--- | :--- | :--- |
| **Go 独立业务域与 HTTP 路由** | `internal/<domain>/` 独立子包，在 [`internal/gateway/gateway.go`](internal/gateway/gateway.go) 注册 | 严禁塞入 `internal/provider` 或 `internal/gateway` | [`internal/worktree/`](internal/worktree/manager.go) |
| **本机 OpenAI 兼容中转与中转 Key** | [`internal/provider/relay.go`](internal/provider/relay.go) + [`relay_keys.go`](internal/provider/relay_keys.go)，仅监听回环，Key 存 `~/.hoxi/relay-keys.json`（只存哈希） | 严禁加入 [`internal/gateway/tunnel.go`](internal/gateway/tunnel.go) 白名单，严禁把上游 `apiKey` 直接当客户端 token 下发 | [`internal/provider/relay.go`](internal/provider/relay.go) |
| **经 GoPanel 隧道开放的路由** | [`internal/gateway/tunnel.go`](internal/gateway/tunnel.go) 显式「方法 + 路由」白名单 + `tunnel_test.go`（允许与相邻拒绝用例），同步更新双仓契约文档 | 严禁前缀放行、绕过白名单，或开放账号、提供商配置、命令执行、`open`、工作区外文件等接口 | [`docs/gopanel-hoxi-remote-tunnel.md`](docs/gopanel-hoxi-remote-tunnel.md) |
| **Go 网关自发轻量 LLM 调用** | 领域包接收 `gateway` 注入的 [`llm.Client`](internal/llm/llm.go)，不 `import provider` | 严禁业务包直接 `import internal/provider`；严禁手写 `http.Post` 拼接 `/v1/messages` 或 `/chat/completions` | [`internal/promptassist/`](internal/promptassist/service.go)、[`internal/gateway/assist.go`](internal/gateway/assist.go) |
| **直连 OpenCode (`opencode serve`)** | 一律使用 [`internal/opencode/ocapi`](internal/opencode/ocapi/client.go)（共享 `Transport`、Basic Auth 与 `LimitReader`） | 严禁自建回环 `http.Client`/`Transport` 或手动调用 `SetBasicAuth("opencode", ...)` | [`internal/opencode/ocapi/client.go`](internal/opencode/ocapi/client.go) |
| **`~/.hoxi` 与引擎路径解析** | 一律使用 [`internal/hoxipaths`](internal/hoxipaths/hoxipaths.go)（`DBPath`、`EngineDataDir`、`EngineStateDir`、`Home`，项目侧用 `ProjectMemoryDir` / `ProjectSessionMemoryFile` / `ProjectRootForWorktree`） | 严禁在各包硬编码 `~/.hoxi/hoxi.db`、`~/.hoxi/engine-data`、`<project>/.hoxi/memory` 或 `<project>/.opencode/memory`，严禁在用户项目里创建 `.opencode/` | [`internal/hoxipaths/hoxipaths.go`](internal/hoxipaths/hoxipaths.go) |
| **外部 CLI 会话扫描 / 凭证桥接** | `internal/handoff/<tool>.go` 单文件高内聚 | 严禁把工具 A 的函数塞进工具 B、`types.go` 或 `service.go` | [`internal/handoff/codex.go`](internal/handoff/codex.go) |
| **多运行时 Agent 子进程驱动** | `internal/runner/` 对应进程 adapter 与协议 translator | 严禁侵入或修改 `internal/opencode` | [`internal/runner/`](internal/runner/http.go) |
| **前端 HTTP / REST / SSE 客户端** | `web/src/services/`，复用 [`requestJSON`](web/src/services/http.ts) | 严禁反向导入 `store/` 或 `components/`；严禁混入纯 UI 工具 | [`web/src/services/http.ts`](web/src/services/http.ts) |
| **前端响应式状态与纯领域计算** | `web/src/store/<domain>.ts`，在 [`workspace.ts`](web/src/store/workspace.ts) 底部扁平导出 | 严禁反向导入 `components/`；严禁新建 `*-storage.ts` 或二级 `export *` | [`web/src/store/git-commit.ts`](web/src/store/git-commit.ts) |
| **前端独立业务弹窗 / 功能面板** | `web/src/components/<domain>/`（`models/`, `handoff/`, `settings/`, `approval/`） | 严禁将非布局类的业务 Modal 塞进 `components/layout/` | [`web/src/components/handoff/`](web/src/components/handoff/ExternalHandoffModal.tsx) |
| **前端多语言词条** | 同步写入 `web/src/i18n/locales/en-US/<domain>.ts` 与 `zh-CN/<domain>.ts` | 严禁手写维护冗长 interface（由 [`types.ts`](web/src/i18n/types.ts) 自动推导） | [`web/src/i18n/types.ts`](web/src/i18n/types.ts) |
| **移动端聊天查询上下文** | [`chat_information_engine.dart`](client/lib/features/chat_hub/data/chat_information_engine.dart) | 严禁在 UI Widget 中硬编码业务拼接逻辑 | [`ChatInformationEngine`](client/lib/features/chat_hub/data/chat_information_engine.dart) |
| **移动端聊天内控制动作** | `chat_session_actions.dart` / `chat_infrastructure_actions.dart`，接入 `chat_intent_tools.dart` 与 [`chat_action_executor.dart`](client/lib/features/chat_hub/data/chat_action_executor.dart) | 严禁跳过确认计划直接执行修改；严禁要求用户退出聊天进入多层菜单才能完成常规控制 | [`ChatIntentDispatcher`](client/lib/features/chat_hub/data/chat_intent_dispatcher.dart) |
| **移动端语音** | [`chat_voice_service.dart`](client/lib/features/chat_hub/data/chat_voice_service.dart) + [`realtime_voice_model_service.dart`](client/lib/features/chat_hub/data/realtime_voice_model_service.dart) + [`chat_hub_voice_call_sheet.dart`](client/lib/features/chat_hub/presentation/widgets/chat_hub_voice_call_sheet.dart) | 严禁在每轮语音回合反复销毁重建原生 `AVAudioEngine` | [`ChatHubVoiceCallSheet`](client/lib/features/chat_hub/presentation/widgets/chat_hub_voice_call_sheet.dart) |

### 2.4 专项文档

修改以下子系统前，先阅读对应文档：

- **AI 编码红线与提交前自检清单（审批门控、隧道、迁移、持久化、进程控制、外发、前端分层）** → [`docs/ai-coding-guardrails.md`](docs/ai-coding-guardrails.md)

- **整体架构、OpenCode REST/SSE 协议契约、Worktree 状态机** → [`DEVELOPMENT.md`](DEVELOPMENT.md)
- **Hoxi ⇄ GoPanel 远程隧道与审批推送（`internal/tunnel` + 网关路由白名单，双仓同步）** → [`docs/gopanel-hoxi-remote-tunnel.md`](docs/gopanel-hoxi-remote-tunnel.md)
- **远程可达性与端到端加密（局域网直连、配对票据、盲目中继回落；LAN 已实现，跨网 E2E 为草案）** → [`docs/remote-access-e2e-contract.md`](docs/remote-access-e2e-contract.md)
- **移动端现状、控制链路与语音实现边界** → [`client/AGENTS.md`](client/AGENTS.md)、[`client/README.zh-CN.md`](client/README.zh-CN.md)、[`client/VOICE_REMOTE_ARCHITECTURE.md`](client/VOICE_REMOTE_ARCHITECTURE.md)、[`client/ROADMAP.md`](client/ROADMAP.md)
- **跨 CLI 会话接管（Handoff IR）与本地 CLI 协议桥接** → [`HANDOFF_AND_CLI_BRIDGE.md`](HANDOFF_AND_CLI_BRIDGE.md)
- **原生电脑控制、桌面应用授权与平台验证边界** → [`COMPUTER.md`](COMPUTER.md)
- **内置无头 Chrome 浏览器检视器、人工接管与安全边界** → [`BROWSER.md`](BROWSER.md)、[`internal/browser/README.md`](internal/browser/README.md)
- **统一会话体系与多运行时（Claude Code / Antigravity `agy`）执行引擎、PreToolUse 审批门控** → [`docs/handoff/2026-10-19-session-ownership.md`](docs/handoff/2026-10-19-session-ownership.md)、[`docs/antigravity-runner-probe.md`](docs/antigravity-runner-probe.md)、[`docs/antigravity-approval-hook.md`](docs/antigravity-approval-hook.md)、[`docs/claude-code-approval-hook.md`](docs/claude-code-approval-hook.md)
- **Agent Skills 开发** → [`docs/skills-development.md`](docs/skills-development.md)
- **macOS 自动更新（Sparkle）、签名公证与一键发版流水线** → [`docs/auto-updates.md`](docs/auto-updates.md)、[`docs/direct-distribution-readiness.md`](docs/direct-distribution-readiness.md)、[`docs/releases.md`](docs/releases.md)

---

## 三、规划（未实现）

不要把以下内容写成现有能力，也不要在代码注释或 UI 文案中宣称已支持：

| 项目 | 说明 |
| --- | --- |
| 远程端到端加密与盲目中继 | 跨网直连优先、打不通回落只转发密文的中继，中继读不到内容；配对票据服务与数据面分离。LAN 直连与 `5417` 令牌监听**已实现**，跨网部分见 [`docs/remote-access-e2e-contract.md`](docs/remote-access-e2e-contract.md) |
| 隧道 WebSocket | 经隧道使用远程终端（`/pty`），当前 GoPanel 返回 501 |
| 隧道凭据入钥匙串 | 当前为 `0600` 明文文件 |
| 手机端数据外发说明 | 在模型网关配置处与首次对话前说明上下文发往哪个网关、包含哪些内容 |
| 系统语音入口 | Siri / App Intents、操作按钮、小组件、语音深链；iOS 不做后台自定义唤醒词 |
| 语音验收 | iOS 识别主动轮转（当前为结束或出错后重启）、Realtime 长通话、Android 原始 PCM 上送、30 分钟连续通话与时延指标 |
| Android 推送 | 需厂商推送或第三方通道 |
| 手机委托执行业务 Skill | 通用 Skill 描述、发现、输入与结构化结果契约；手机一句话 → 节点执行指定 Skill → 可核验回执。当前本地 Skill 只能在桌面 Agent 会话内使用 |
| 服务端任务语义 | 任务 ID、幂等键、取消与最终状态查询 |
| 真机端到端验收 | 公网 GoPanel + 真实 Mac + 手机 4G 的隧道、SSE 与推送 |
