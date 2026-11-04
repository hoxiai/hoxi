# AGENTS.md

`Hoxi` 是面向独立开发者的本地多 Agent 研发工作台（**macOS 与 Windows 桌面端**，Go + Wails + SolidJS），可与配套 **GoPanel 服务器面板**（`gopanel_v2`）及移动端远程协同（局域网直连、Cloudflare Tunnel 或 GoPanel 反向隧道）。

本文分三段：**现状**（已实现）、**边界与约束**（写代码必须遵守）、**规划**（未实现，勿当现有能力引用）。远程通道以 [远程隧道契约](docs/gopanel-hoxi-remote-tunnel.md) 为准；文档与代码冲突时以代码和测试为准并同步修正文档。

---

## 一、现状

### 1.1 桌面端

- **宿主与状态栏**（`internal/app` + `internal/statusbar`）：Wails v2（`WKWebView` / `WebView2`），单实例锁、关窗隐藏驻留；WebView 经 AssetServer 进程内交网关，回环监听桥接端口（release `127.0.0.1:5410`、dev `127.0.0.1:5420`，占用回落随机端口）供 WS 与 CLI 使用。原生托盘显示 `Running` / `Attention`，待审批新增时回调一次（不含命令文本）
- **OpenCode 运行时**（`internal/opencode`）：监管官方 `opencode serve`（随机密码 + Basic Auth），按 PID、启动时间与参数登记到 `~/.hoxi/reclaim.json`（`internal/reclaim`），仅回收已退出 Hoxi 的孤儿守护进程（不扫进程表）；Windows 经 `subproc.StartTied` 绑 Job Object，NSIS 按完整路径结束进程
- **多运行时与统一审批**（`internal/runner`）：统一驱动 OpenCode、Claude Code、Antigravity（`agy`）、Gemini CLI（`gemini`）、Qoder、CodeBuddy 为统一事件流；原生 CLI 遇瞬态错误由 `turn_retry.go` 退避重试，OpenCode 推理截断或过渡动作断流由 `opencode_turn_recovery.go` 自动续跑。PreToolUse / BeforeTool Hook 接入 `approval_decision.go`（`~/.hoxi/approval-policy.json`），支持轻量模型审查 `aiReview` + `aiReviewModel`（`approval_ai_review.go`，仅调用户明确配置的模型或显式选择的 `hoxi-auto/*` 池，超时/存疑回退人工确认）及桌面专属 `unattended` 无人值守模式（隔离 worktree + Seatbelt 沙箱）
- **前端工作区**（`web/`）：Vite + SolidJS + Tailwind 三栏布局；SSE 经 `requestAnimationFrame` 批处理；侧边栏将非归档子会话归组在根父会话下，主视图含嵌套时间线、`ThoughtBox`、`ToolCard`、`@pierre/diffs`、`@xterm/xterm`、内置浏览器检视器（`internal/browser`）及 Chrome Link 扩展（`extensions/chrome`，详见 [`BROWSER.md`](BROWSER.md)）
- **原生电脑控制**（`internal/computer`）：macOS 与 Windows 前台窗口读取与输入（`hoxi_computer`），按会话与工作树原生弹窗授权单个运行中应用（禁终端、密码管理器、系统设置），每次输入仅限本机「允许一次」，只截目标窗口（详见 [`COMPUTER.md`](COMPUTER.md)）
- **会话、接管与 Skills**：Git Worktree 隔离（`internal/worktree`，`?directory=` 作用域）、历史分页（`session-history.ts` 首屏取最新 200 条）、Steer / Stop、Revert / Fork、`/goal` 目标（`[[goal:complete]]` / `[[goal:blocked]]` 或 `goal_report`）；8 维记忆共享 `.opencode/lib/memory-compaction.js`，落盘 `<project>/.hoxi/memory/sessions/<session>.md`。`internal/handoff` 接管外部 CLI 会话；`internal/skills` + `internal/tools` + `internal/workapps` 提供本地/线上 Skill、免 Key 工具网桥（`/api/hoxi/tools/{discover,inspect,run}`）与 Work 应用状态
- **远程协同通道**（不开放公网端口，复用同一远程路由白名单）：① **GoPanel 反向隧道**（`internal/tunnel` + `gateway/tunnel.go`），含 APNs 计数推送；② **局域网配对直连**（`internal/pairing` + `gateway/lan.go`，监听 `:5417`，仅限局域网，令牌存 `~/.hoxi/pairing.json`）；③ **Cloudflare Tunnel 与反向代理入口**（`internal/cloudflared` + `gateway/ingress.go`，监听 `127.0.0.1:5418`，仅认独立 `relayToken`，凭据存 `~/.hoxi/cloudflared.json`）
- **提供商、中转与自动选模**（`internal/provider`）：① 回环 OpenAI 兼容中转（`relay.go` + `relay_keys.go`，`hk-*` 哈希存 `~/.hoxi/relay-keys.json`，永不入隧道）；② 官方账号直连（`official.go`，独立写入 `hoxi-official`，退出同步清理）；③ `hoxi-auto` 自动池与熔断（`auto_router.go` + `breaker.go` + `quota_reset.go`，存 `~/.hoxi/provider-breakers.json`），前端 `auto-models.ts` 会话粘性选模保 KV Cache，`auto-failover.ts` 遇限流/额度/鉴权/模型错误立即换候选，多轮会话遇瞬态网络/5xx 优先同模型重试 1 次保缓存后再切换；支持 MCP OAuth 2.1 PKCE（`mcp_oauth.go`）

### 1.2 目录结构

```text
/
├── main.go / cmd/hoxi/main.go        # Wails 桌面入口 / CLI & headless 入口（委托 internal/app）
├── internal/
│   ├── app/, cli/                    # [顶层] DesktopApp 生命周期与 Wails 绑定 / 终端 `hoxi` CLI
│   ├── gateway/                      # [网关层] 进程内反向代理、Basic Auth、WS/SSE 桥接、隧道路由白名单
│   ├── runner/                       # [业务层] 多运行时执行引擎、审批门控与回滚
│   ├── opencode/ (+ ocapi/, ocstate/)# [业务层/底座层] OpenCode 监管与插件覆盖层 + 回环客户端与 SQLite 同步
│   ├── provider/                     # [业务层] 提供商配置、llm.Client 实现、本机中转、官方直连、自动池与熔断
│   ├── worktree/, workspace/         # [业务层] Git worktree 沙箱、Diff、快照、审查 / 多工作区配置
│   ├── browser/, computer/           # [业务层] Chrome 检视器、Chrome Link / 原生窗口读取与前台输入
│   ├── handoff/, orchestration/      # [业务层] 跨 CLI 会话接管与桥接 / 多 Agent DAG Convoy 编排
│   ├── codeindex/, recall/, forge/   # [业务层] Tree-sitter 与 trigram 索引 / 跨会话 BM25 召回 / GitHub & GitLab
│   ├── skills/, tools/, workapps/    # [业务层] 本地与线上 Agent Skills / 免 Key 工具网桥 / Work 应用状态
│   ├── tunnel/, pairing/, cloudflared/# [业务层] GoPanel 反向隧道 / 局域网扫码配对 / Cloudflare Tunnel 监管
│   ├── auth/, attachment/, promptassist/, leaderboard/, sandbox/, updater/, telemetry/ # [业务层]
│   └── hoxipaths/, llm/, agentcli/, agyhook/, claudeconfig/, diagnostics/, buildinfo/, notify/, osopen/, reclaim/, sqlitecli/, statusbar/, subproc/, textindex/, testutil/execstub/ # [底座层/测试辅助]
├── web/src/{components,store,services,i18n,__tests__}/ # 按域 UI、响应式 Store（workspace.ts 扁平导出）、网络层、双语词条与单测
├── web/scripts/                      # check-i18n.ts, check-lines.ts（800 行与分层门禁）, check-cycles.ts, run-tests.ts
├── extensions/chrome/                # Chrome Link MV3 扩展（worker.js, protocol.js, popup）
├── .opencode/{plugins,lib,skills}/   # 上游 OpenCode 配置根（只读）：hoxi-memory.js, hoxi-goal.js, 共享策略与 *.cases.json
├── .hoxi/memory/                     # Hoxi 项目状态（git-ignored）：project.md 与 sessions/<session>.md
└── docs/, scripts/                   # 架构护栏、隧道、更新与发版指南 / 一键发版与打包脚本
```

### 1.3 构建与验证命令

- **Desktop Dev**: `wails dev`（本机 `:5400` 已启动时自动对接本地服务端）
- **Build macOS / Windows**: `./build-desktop.sh`（或 `--release-only --dmg --no-install --arch amd64`） / `.\scripts\build-windows.ps1`（`-Arch arm64`）
- **Run Headless**: `go run ./cmd/hoxi --no-desktop`
- **Web Lint / Test / Build**: `npm --prefix web run lint && npm --prefix web test && npm --prefix web run build`
- **AI One-Click Release**: `./scripts/ai-release.sh <version> "<release-notes>"`

---

## 二、边界与约束

### 2.1 远程控制与隧道接入边界

- **信任边界**：配对即授权。管理员登录已配对 GoPanel 后可在隧道白名单内远程操作桌面；隧道明文转发无 E2E 加密。白名单外路由（账号、提供商、浏览器、更新器、OpenCode 透传等）一律 403；新增隧道开放路由须同步更新契约文档并补允许/相邻拒绝测试
- **审批最终防线**：远程会话内容可能诱导模型调工具，桌面端 PreToolUse 门控与破坏性命令审批仍是最终防线，不得因客户端前置确认而放宽
- **推送与业务凭据**：APNs 仅发计数与任务摘要计数（`kind: "summary"`），绝不含命令、代码、路径或会话内容；垂直业务私有凭据仅留桌面/GoPanel Skill 环境

### 2.2 工程与功能约束

1. **领域高内聚、单向分层与单文件 800 行上限**
   - **高内聚与禁凑行数**：严禁把模块 A 的函数塞进无关模块 B（如把 Codex/Gemini/Claude 塞进 Grok 或 `types.go`/`helpers.go`），禁止拆分仅几十行的 `*-storage.ts` 脚手架。
   - **800 行上限**：任何源码文件（`.go`, `.ts`, `.tsx`, `.js`, `.m`）上限 **800 行**（纯静态 SVG 字典或生成常量表除外）。
   - **前端严格单向分层 (`components/ -> store/ -> services/`)**：
     - `services/` 仅负责 HTTP/REST/SSE 通信，**严禁**反向 `import` `store/` 或 `components/`，禁混入 UI 工具。
     - `store/` 负责响应式状态、领域计算（含会话父子解析与跨视图分组聚合）与持久化，**严禁**反向 `import` `components/`；子模块统一在 [`web/src/store/workspace.ts`](web/src/store/workspace.ts) 底部扁平重导出，禁二级 `export *`。
     - `components/` 按域归位（`layout` 仅放窗口骨架与导航栏，业务弹窗归入对应域目录）；**严禁在 `components/` 下新建领域聚合文件复制或改写 `store/` 已有的会话/状态计算逻辑**。
   - **Go 后端四层单向依赖 (`app/cli -> gateway -> 业务领域包 -> 基础底座包`)**：
     - 业务领域包与底座包（见 `1.2`）**严禁反向 `import` `internal/gateway`、`internal/app` 或 `internal/cli`**。
     - 各子包用明确 Receiver（`*Gateway`/`*Service`/`*Manager`），禁跨包伪装别名；内部 LLM 请求统一由 `gateway` 注入 [`llm.Client`](internal/llm/llm.go)，业务包**严禁直接 `import internal/provider`**。

2. **OpenCode 上游零侵入**
   - 严禁 Fork 或修改 `opencode` 内部源码；定制能力仅经 Go 进程监管、反向代理或插件钩子扩展。
   - **严禁在用户项目创建 `.opencode/`**：否则 `opencode serve` 会在其中安装 `node_modules` 污染仓库。项目状态（记忆、工作树、附件）一律写 `<project>/.hoxi/`，路径走 [`internal/hoxipaths`](internal/hoxipaths/hoxipaths.go)（`ProjectMemoryDir` / `ProjectSessionMemoryFile`）；旧版 `.opencode/memory/` 仅只读回退。

3. **三大统一网关平面与 Git Worktree 强隔离**
   - 前端（[`api.ts`](web/src/services/api.ts)、[`sse.ts`](web/src/services/sse.ts)）与 CLI 会话操作统一走 `/api/hoxi/sessions`，事件流走 `/api/hoxi/event`，权限审批走 `/api/hoxi/permission`，**严禁裸调上游 `/session`、`/event` 或 `/permission`**。
   - 关联特定会话或沙箱的请求（`/api/hoxi/sessions`、`/api/hoxi/event`、`/api/hoxi/permission`、`/file`、`/pty`）**必须**带 `?directory=<worktree-path>`，严禁跨会话读写。

4. **SolidJS 响应式、成对清理与防过期响应**
   - **禁解构响应式源**：组件内严禁解构 `props` 或 `store`；`/api/hoxi/event` 高频增量事件须经 `requestAnimationFrame` 批处理合并后更新 Store。
   - **局部状态按稳定 ID 有界存储**：列表行或卡片展开态（子 Agent 抽屉、`ThoughtBox`）须按 `session.id` / `part.id` 存入 Store 或有界（FIFO/LRU）模块级集合，禁挂组件局部 Signal 或无界累积。
   - **成对清理与世代号校验**：`addEventListener`、`ResizeObserver`、定时器及 WS/PTY 须在 `onCleanup` 销毁；按会话/消息索引的内存 Map（Go `runner.Service` 各 `sync.Map` 与前端 `lastDispatchBySession` 等）须在轮次 `idle` 与会话删除时成对清理；异步读取须校验当前 `sessionID` 或请求世代号丢弃过期响应。

5. **任务流状态与回滚闭环**
   - 会话 `busy` 时支持实时纠偏（`Steer`）与中止（`Stop`），`ToolCard` 与 `ThoughtBox` 准确反映 `pending` / `running` / `completed` / `error` 四态与耗时，并同步至系统托盘（`Running` / `Attention`）。

6. **安全门控、网络外发、子进程消毒与原子持久化**
   - **破坏性操作与审批门控底线**（完整 AST/Shell 展开、不透明命令、无人值守与本会话进程归属细则必读 [`docs/ai-coding-guardrails.md`](docs/ai-coding-guardrails.md) 第 1、5 节）：
     - 含破坏性指令（`rm`, `sudo`, `chmod`, `chown`, `diskutil`, `mkfs`, `git push --force`, `git reset --hard`）与敏感文件读取（`.env` 等）默认须人工确认；字面量数据参数先经 `maskCommandDataArguments`（`command_data_args.go` / `command-data-args.js`）遮蔽，语义不明时不替换。
     - **完全信任（`trusted`，`approval_trusted.go`，仅桌面端）**：仅额外放行隔离 worktree（`.hoxi/worktrees/`）内非受保护路径删除与 `git reset --hard`/`checkout --`/`restore`/`clean`，及临时目录（`/tmp`、`$TMPDIR`、macOS `/var/folders/<id>/T/<子路径>`）下具体条目。
     - **无人值守（`unattended`，`approval_unattended.go`，仅桌面端）**：要求隔离 worktree + 命令沙箱且运行时受 Seatbelt 包裹；硬拒绝读密钥、任何 `git push` 与发布、提权、凭据外发及动态代码（`dynamic` 类，共用 `.opencode/lib/unattended.cases.json`），拒绝项记入 `/api/hoxi/permission/deferred`（不入隧道）。
     - **结束本会话进程（`owned_process.go` + `process_control.go`）**：除手动模式外，顶层字面量 `pkill`/`kill` 仅在核实所有目标均属本会话进程组时放行（审计来源 `own_process`），任一目标不属本会话或含动态参数一律询问。
     - **任何模式（含完全信任）必问硬底线**：读密钥与凭据、工作树外敏感路径、受保护路径（`.git`、`.opencode`、`.claude`、`.hoxi`、`.agents`、`.envrc`、`.vscode/settings.json`；`.hoxi/attachments/` 读写豁免，`.hoxi/skills/` 与 `.agents/skills/` 仅读与执行既有脚本豁免，读本会话 `~/.hoxi/sessions/<id>/` 豁免）、`sudo`、`git push --force`/删远端分支、共享 git 状态、发布命令、非本会话 `kill`/`pkill`/`killall`、磁盘系统命令、管道执行、`unsandboxed` 工具与无法按 `opaque_command.go` / `opaque-command.js` 归约的不透明命令（共用 `.opencode/lib/opaque-command.cases.json`）。
   - **本地网关与外发边界**：网关仅监听 `127.0.0.1`，反向代理 `Transport` 配 `Proxy: nil` 与 `FlushInterval: -1` 并长生命周期复用，外部响应与进程输出用 `io.LimitReader` 限流。AI 请求直连用户服务商（回环中转 `relay.go` 同样直发且不入隧道）；辅助外发（遥测、更新、登录、榜单）严禁含提示词、代码、Diff 或会话内容；后台 LLM 调用须在 UI 标明目的地。
   - **子进程消毒与原子落盘**：子进程一律用 [`internal/subproc`](internal/subproc/subproc.go) 创建（Windows `CREATE_NO_WINDOW`，修改而非替换 `SysProcAttr`）；外部 `git`/CLI 绑超时并设 `GIT_TERMINAL_PROMPT=0`、`GCM_INTERACTIVE=never`，剥离 `GIT_ASKPASS`/`SSH_ASKPASS`/`GIT_EDITOR`；读外部 SQLite 只读 + `_busy_timeout`。`~/.hoxi/` 与 `.hoxi/` 状态文件须「加锁 + 同目录临时文件 + fsync + `os.Rename`」；日志自动脱敏 `Authorization`、`Bearer`、`x-api-key`、`OPENCODE_SERVER_PASSWORD`。

7. **多语言包模块化与自动类型推导**
   - UI 文案统一走 `web/src/i18n` 的 `t("domain.key", params)`，禁内联硬编码；词条存于 `web/src/i18n/locales/{en-US,zh-CN}/<domain>.ts`（`types.ts` 自动推导类型），双语键名与 `{param}` 占位符须 100% 对齐，且 **`zh-CN` 词条结尾严禁加中文句号「。」**（`npm --prefix web run check:i18n`）。

8. **命名规范**
   - Go 包名全小写无下划线，源文件 `snake_case.go`（禁重复包名前缀），核心结构体 `Service`/`Manager`/`Store`，路由注册 `RegisterRoutes(mux)` 与 `handle<Action>`；前端 `store/` 与 `services/` 用 `kebab-case.ts`，`components/<domain>/` 组件用 `PascalCase.tsx`、辅助用 `kebab-case.ts(x)`，Action 用动词前缀（`fetch*`, `load*`, `create*`, `update*`, `delete*`, `apply*`, `switch*`），布尔派生 `is*`/`has*`/`can*`。自有路由挂 `/api/hoxi/<domain>/<action>`（`kebab-case`，JSON `camelCase`）；ID 前缀 `ses_*`, `msg_*`, `prt_*`, `per_*` / `per_rnr_*`；`localStorage` 键以 `hoxi.` 或 `hoxi:` 开头；i18n 键为 `<domain>.<camelCaseKey>`。

9. **修改后必验闭环**
   - 每次变更完成后须执行 `npm --prefix web run lint && npm --prefix web test && npm --prefix web run build` 与 `go vet ./... && go test ./... && go build ./...`（Go 改动须过 `go test -race`）。

10. **Git 提交共同作者署名**
    - 遵循 `~/.hoxi/git-attribution.json`（`GET/PUT /api/hoxi/git/attribution`）：默认 `auto` 在 Hoxi 隔离工作树（`<project>/.hoxi/worktrees/*`）及本仓库追加 `Co-authored-by: Hoxi <code@hoxi.ai>`，`always` 全部署，`never` 关闭。

11. **AI 编码红线 (AI Guardrails)** —— 详见 [`docs/ai-coding-guardrails.md`](docs/ai-coding-guardrails.md)，改审批、隧道、工作区/会话迁移、持久化、进程控制前必读
    - **审批策略只解析一次、失败即 ask**：禁重拼命令字符串再解析；语义不明输入禁走白名单；白名单按 token 锚定（禁 `\b`）；未知与网络工具按策略判定（禁按前缀放行）；Go 与 JS 策略同步修改并写入 `.opencode/lib/permission-policy.cases.json` 与 `permission-request.cases.json`。
    - **审批超时只拒绝永不放行**（默认 600s，范围 60–3600）；硬拒绝类不得被任何模式或「始终允许」放宽；待处理清单不入隧道。
    - **隧道白名单只用显式「方法 + 路由」清单**，禁前缀匹配；命令执行、`open`、工作区外文件、凭据与 OpenCode 原始透传永不入隧道。
    - **禁启发式推断破坏性操作**（按下标或 `os.Stat` 猜移动、匹配不到扩大 kill 范围等）；迁移/删除/杀进程仅在显式指令下执行且限本会话范围。
    - **校验函数禁静默丢弃输入**；状态文件原子写入且解析失败先备份；**后台 LLM 严禁自动回退到用户未选择的提供商**；安全改动必带拒绝路径测试，禁留 `zz_scratch_*`。

### 2.3 新增代码归属

| 新增 / 修改目标 | 必须放入的位置 (Target) | 严禁放入的位置 (Forbidden) | 典范参考 (Example) |
| :--- | :--- | :--- | :--- |
| **Go 独立业务域与路由** | `internal/<domain>/`，在 [`gateway.go`](internal/gateway/gateway.go) 注册 | 严禁塞入 `internal/provider` 或 `internal/gateway` | [`internal/worktree/`](internal/worktree/manager.go) |
| **本机 OpenAI 兼容中转** | [`relay.go`](internal/provider/relay.go) + [`relay_keys.go`](internal/provider/relay_keys.go)（仅存哈希） | 严禁入 [`tunnel.go`](internal/gateway/tunnel.go) 白名单或下发上游 `apiKey` | [`internal/provider/relay.go`](internal/provider/relay.go) |
| **经隧道开放的路由** | [`tunnel.go`](internal/gateway/tunnel.go) 显式「方法+路由」+ `tunnel_test.go` + 契约文档 | 严禁前缀放行或开放账号、提供商、命令执行、`open`、工作区外文件 | [`docs/gopanel-hoxi-remote-tunnel.md`](docs/gopanel-hoxi-remote-tunnel.md) |
| **网关自发轻量 LLM 调用** | 领域包接收 `gateway` 注入的 [`llm.Client`](internal/llm/llm.go) | 严禁业务包 `import internal/provider` 或手写 `http.Post` | [`internal/promptassist/`](internal/promptassist/service.go) |
| **直连 `opencode serve`** | 一律用 [`internal/opencode/ocapi`](internal/opencode/ocapi/client.go) | 严禁自建回环 `http.Client`/`Transport` 或手写 Basic Auth | [`internal/opencode/ocapi/client.go`](internal/opencode/ocapi/client.go) |
| **`~/.hoxi` 与记忆路径** | 一律用 [`internal/hoxipaths`](internal/hoxipaths/hoxipaths.go)（`DBPath`、`ProjectMemoryDir` 等） | 严禁硬编码路径，严禁在用户项目创建 `.opencode/` | [`internal/hoxipaths/hoxipaths.go`](internal/hoxipaths/hoxipaths.go) |
| **外部 CLI 会话接管** | `internal/handoff/<tool>.go` 单文件高内聚 | 严禁跨工具混放或塞入 `types.go` / `service.go` | [`internal/handoff/codex.go`](internal/handoff/codex.go) |
| **多运行时子进程驱动** | `internal/runner/` 对应 adapter 与 translator | 严禁侵入或修改 `internal/opencode` | [`internal/runner/http.go`](internal/runner/http.go) |
| **前端网络层 (`services/`)** | `web/src/services/`，复用 [`requestJSON`](web/src/services/http.ts) | 严禁反向导入 `store/` 或 `components/`，禁混入 UI 工具 | [`web/src/services/http.ts`](web/src/services/http.ts) |
| **前端状态层 (`store/`)** | `web/src/store/<domain>.ts`，在 [`workspace.ts`](web/src/store/workspace.ts) 扁平导出 | 严禁反向导入 `components/`，禁建 `*-storage.ts` 或二级 `export *` | [`web/src/store/git-commit.ts`](web/src/store/git-commit.ts) |
| **前端独立业务弹窗** | `web/src/components/<domain>/`（`models/`, `handoff/`, `settings/`, `approval/`） | 严禁将非布局业务 Modal 塞进 `components/layout/` | [`ExternalHandoffModal.tsx`](web/src/components/handoff/ExternalHandoffModal.tsx) |
| **前端多语言词条** | 同步写 `web/src/i18n/locales/{en-US,zh-CN}/<domain>.ts` | 严禁手写 interface（由 [`types.ts`](web/src/i18n/types.ts) 自动推导） | [`web/src/i18n/types.ts`](web/src/i18n/types.ts) |

### 2.4 专项文档

- **AI 编码红线与自检清单** → [`docs/ai-coding-guardrails.md`](docs/ai-coding-guardrails.md)
- **架构蓝图、远程隧道与 E2E 契约** → [`DEVELOPMENT.md`](DEVELOPMENT.md)、[`docs/gopanel-hoxi-remote-tunnel.md`](docs/gopanel-hoxi-remote-tunnel.md)、[`docs/remote-access-e2e-contract.md`](docs/remote-access-e2e-contract.md)
- **跨 CLI 接管、电脑控制、内置浏览器** → [`HANDOFF_AND_CLI_BRIDGE.md`](HANDOFF_AND_CLI_BRIDGE.md)、[`COMPUTER.md`](COMPUTER.md)、[`BROWSER.md`](BROWSER.md)
- **多运行时、审批 Hook、Skills 与更新发版** → [`docs/handoff/2026-10-19-session-ownership.md`](docs/handoff/2026-10-19-session-ownership.md)、[`docs/antigravity-approval-hook.md`](docs/antigravity-approval-hook.md)、[`docs/claude-code-approval-hook.md`](docs/claude-code-approval-hook.md)、[`docs/skills-development.md`](docs/skills-development.md)、[`docs/auto-updates.md`](docs/auto-updates.md)、[`docs/releases.md`](docs/releases.md)

---

## 三、规划（未实现）

勿将以下内容当现有能力或在注释/UI 文案中宣称已支持：

| 项目 | 说明 |
| --- | --- |
| 远程跨网 E2E 加密与盲目中继 | LAN 直连与 `:5417` 令牌监听**已实现**；跨网 E2E 未实现（[`docs/remote-access-e2e-contract.md`](docs/remote-access-e2e-contract.md)） |
| 隧道 WebSocket / 凭据入钥匙串 / Android 推送 | 远程 `/pty` 当前返回 501；隧道凭据当前为 `0600` 文件；Android 推送未实现 |
| 跨重启任务账本 / 真机公网验收 | Hoxi 通道 `prompt_async` 与审批 `reply` 幂等键**已实现**；跨重启任务账本与公网真机验收未完成 |
