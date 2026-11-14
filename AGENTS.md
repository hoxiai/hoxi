# AGENTS.md

`Hoxi` 是面向独立开发者的本地多 Agent 研发工作台（**macOS 与 Windows 桌面端**，Go + Wails + SolidJS），可与配套 **GoPanel 服务器面板**（`gopanel_v2`）及移动端远程协同（局域网直连、Cloudflare Tunnel 或 GoPanel 反向隧道）。

本文常驻 AI 会话上下文，只保留**写代码必须遵守的约束**与**验证入口**。模块现状、完整目录结构与规划明细见 [`docs/architecture-overview.md`](docs/architecture-overview.md)：定位模块归属、判断能力是否已实现、或在注释/UI 文案中描述现有能力前先读它。远程通道以 [远程隧道契约](docs/gopanel-hoxi-remote-tunnel.md) 为准；文档与代码冲突时以代码和测试为准并同步修正文档。

---

## 一、构建、验证与分层速查

- **Desktop Dev**: `wails dev`（本机 `:5400` 已启动时自动对接本地服务端）
- **Build macOS / Windows**: `./build-desktop.sh`（或 `--release-only --dmg --no-install --arch amd64`） / `.\scripts\build-windows.ps1`（`-Arch arm64`）
- **Run Headless**: `go run ./cmd/hoxi --no-desktop`
- **Web Lint / Test / Build**: `npm --prefix web run lint && npm --prefix web test && npm --prefix web run build`
- **AI One-Click Release**: `./scripts/ai-release.sh <version> "<release-notes>"`
- **Go 分层归属**（完整目录树见 `docs/architecture-overview.md`）：顶层 `main.go`、`cmd/hoxi`、`internal/app`、`internal/cli`；网关层 `internal/gateway`；底座层 `internal/opencode/ocapi`、`internal/opencode/ocstate`、`hoxipaths`、`llm`、`agentcli`、`agyhook`、`claudeconfig`、`diagnostics`、`buildinfo`、`notify`、`osopen`、`reclaim`、`sqlitecli`、`statusbar`、`subproc`、`textindex`、`testutil/execstub`；其余 `internal/*` 为业务层
- **配置根**：`.opencode/{plugins,lib,skills}/` 是本仓库自用的上游 OpenCode 配置根（只读）；`.hoxi/memory/` 为 git-ignored 项目状态

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
     - 业务领域包与底座包（见第一节分层速查）**严禁反向 `import` `internal/gateway`、`internal/app` 或 `internal/cli`**。
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
     - 含破坏性指令（`rm`, `sudo`, `chmod`, `chown`, `diskutil`, `mkfs`, `git push --force`, `git reset --hard`）与敏感文件读取（`.env` 等）默认须人工确认；字面量数据参数先经 `maskCommandDataArguments`（`command_data_args.go` / `command-data-args.js`）遮蔽，语义不明时不替换。`auto` 模式额外放行临时目录（`/tmp`、`/private/tmp`、`os.TempDir()`、macOS `/var/folders/<id>/T/<子路径>`）下具名条目的删除，以及对工作目录/临时目录内非受保护具体文件的非递归常规 `chmod`（禁 setuid/setgid/sticky 与 `000`）；只操作暂存区的 `git restore --staged`/`-S`（不含 `-W`/`--worktree`/`--source`）与只读预览 `git clean -n`/`--dry-run` 不计入破坏性指令。
     - **完全信任（`trusted`，`approval_trusted.go`，仅桌面端）**：在 `auto` 基础上额外放行隔离 worktree（`.hoxi/worktrees/`）内非受保护路径删除、递归/通配 `chmod` 与 `git reset --hard`/`checkout --`/`restore`/`clean`。
     - **无人值守（`unattended`，`approval_unattended.go`，仅桌面端）**：要求隔离 worktree + 命令沙箱且运行时受 Seatbelt 包裹；硬拒绝读密钥、任何 `git push` 与发布、提权、凭据外发及动态代码（`dynamic` 类，共用 `.opencode/lib/unattended.cases.json`），拒绝项记入 `/api/hoxi/permission/deferred`（不入隧道）。
     - **结束本会话进程（`owned_process.go` + `process_control.go`）**：除手动模式外，顶层字面量 `pkill`/`kill` 仅在核实所有目标均属本会话进程组时放行（审计来源 `own_process`），任一目标不属本会话或含动态参数一律询问；同链仅把固定绝对目录追加到 `PATH` 尾部时，还须确认继承环境解析到系统工具，前置/替换 PATH、工具来源不明或停止前出现其他可能改写工具的操作均不豁免。
     - **任何模式（含完全信任）必问硬底线**：读密钥与凭据、工作树外敏感路径、受保护路径（`.git`、`.opencode`、`.claude`、`.hoxi`、`.agents`、`.envrc`、`.vscode/settings.json`；`.hoxi/attachments/` 读写豁免，`.hoxi/skills/` 与 `.agents/skills/` 仅读与执行既有脚本豁免，`.claude/skills/` 普通资料仅读豁免（凭据、越界软链与实际通配内容读取不豁免，目录范围授权不等于写入或执行授权），读本会话 `~/.hoxi/sessions/<id>/` 豁免）、`sudo`、`git push --force`/删远端分支、共享 git 状态、发布命令、非本会话 `kill`/`pkill`/`killall`、磁盘系统命令、管道执行、`unsandboxed` 工具与无法按 `opaque_command.go` / `opaque-command.js` 归约的不透明命令（共用 `.opencode/lib/opaque-command.cases.json`）。
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
- **模块现状、完整目录结构与规划明细** → [`docs/architecture-overview.md`](docs/architecture-overview.md)
- **架构蓝图、远程隧道与 E2E 契约** → [`DEVELOPMENT.md`](DEVELOPMENT.md)、[`docs/gopanel-hoxi-remote-tunnel.md`](docs/gopanel-hoxi-remote-tunnel.md)、[`docs/remote-access-e2e-contract.md`](docs/remote-access-e2e-contract.md)
- **跨 CLI 接管、电脑控制、内置浏览器** → [`HANDOFF_AND_CLI_BRIDGE.md`](HANDOFF_AND_CLI_BRIDGE.md)、[`COMPUTER.md`](COMPUTER.md)、[`BROWSER.md`](BROWSER.md)
- **多运行时、审批 Hook、Skills 与更新发版** → [`docs/handoff/2026-10-19-session-ownership.md`](docs/handoff/2026-10-19-session-ownership.md)、[`docs/antigravity-approval-hook.md`](docs/antigravity-approval-hook.md)、[`docs/claude-code-approval-hook.md`](docs/claude-code-approval-hook.md)、[`docs/skills-development.md`](docs/skills-development.md)、[`docs/auto-updates.md`](docs/auto-updates.md)、[`docs/releases.md`](docs/releases.md)

---

## 三、未实现能力

勿将以下能力当作现有能力，也勿在注释/UI 文案中宣称已支持（明细见 `docs/architecture-overview.md` 第二节）：跨网 E2E 加密与盲目中继（LAN 直连与 `:5417` 令牌监听已实现）、隧道 WebSocket（远程 `/pty` 当前返回 501）、隧道凭据入钥匙串（当前为 `0600` 文件）、Android 推送、远程隧道公网真机验收（`prompt_async` 与审批 `reply` 跨重启幂等收据及 `~/.hoxi/task-ledger.json` 跨重启任务账本自动续跑已实现）。
