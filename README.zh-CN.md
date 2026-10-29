<p align="center">
  <a href="https://hoxi.ai"><img src="docs/assets/hoxi-banner.svg" alt="Hoxi" width="310" /></a>
</p>

<p align="center">
  专为高强度 AI 研发打造的 <b>macOS 原生多 Agent 并行工作台</b>，配套 <b>Hoxi &amp; <a href="https://github.com/hoxiai/gopanel">GoPanel</a> 手机端开发与服务器掌上管家</b>。
</p>

<p align="center">
  <a href="https://hoxi.ai"><img src="docs/assets/hoxi-pill-zh.svg" alt="多 Agent 研发工作台 & 手机服务器管家" height="54" /></a>
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

**Hoxi** ([hoxi.ai](https://hoxi.ai)) 不是又一个吃掉 1.5GB 内存的套壳 Electron 编辑器（仅 **~30 MB 内存**、**0.2 秒冷启动**）。

它把桌面端的 **OpenCode**、**Claude Code**、**Google Antigravity (`agy`)**、**Gemini CLI (`gemini`)**、**Qoder（通义灵码）**、**CodeBuddy（腾讯云）** 统一收进同一个三栏工作区，并通过配套移动端（[`client/`](client/README.zh-CN.md)）把**桌面 AI 研发指挥**与 **[GoPanel](https://github.com/hoxiai/gopanel) 服务器运维管理**装进你的口袋。

---

## 直击 5 个最痛的日常研发与运维场景

### 1. 额度限流、账号被封或单工具死循环，换工具就得从头讲起
日常写代码往往同时装了 Claude Code、Codex、Antigravity、Gemini CLI、Trae、Qoder、CodeBuddy。一旦主力工具触发 Rate Limit、额度见底，或者在某个诡异 Bug 上死循环，切到别的工具就要重新贴文件、重新交代上下文。
- **跨软件会话一键无损接管（Session Handoff）**：自动扫描本机 **Claude Code**、**OpenAI Codex CLI**、**Grok CLI**、**Antigravity / Gemini CLI**、**OpenCode** 以及国内高频使用的 **Trae**、**Qoder**、**CodeBuddy** 历史会话。一键导入完整对话、思考链（Reasoning）、工具调用记录与已改文件，并自动提炼 **8 维交接记忆**，换个模型无缝接着干。
- **同会话多引擎热切换**：无需新建对话，同一个会话内可随时在 `OpenCode` / `Claude Code` / `Antigravity` / `Gemini CLI` / `Qoder` / `CodeBuddy` 运行时之间平滑切换；Claude Code 与 Antigravity 派发的子代理（Subagent）直接在时间线内以嵌套任务树实时展示。

### 2. 中转站“偷偷降智 / 掺水 / 断流”，开着 Clash / Surge 动不动 502
国内开发者大量混用 API 中转站、聚合网关或国产模型（DeepSeek、千问 Qwen、智谱 GLM、Kimi、MiniMax、豆包、硅基流动），但中转站到底稳不稳全靠体感；而且系统一开全局代理，本地 Agent 守护进程经常报 `502 Bad Gateway`。
- **模型池自动熔断与会话粘性选模（`hoxi-auto`）**：按 `provider/model` 实时统计首字延迟（TTFT EWMA）与失败分类（瞬态 / 限流 / 额度 / 配置），故障即冷却、自动跳过比最快健康候选慢 3 倍以上的慢节点；同一会话内粘性复用健康候选以保住上游 Prompt 缓存前缀。
- **本机 OpenAI 兼容模型中转（`/v1/chat/completions`）**：在回环地址暴露标准 OpenAI 接口（独立 `hk-*` SHA-256 哈希密钥），供 Cline、Continue、Cherry Studio、Open WebUI、aider 等本机工具直接复用 Hoxi 已配置的服务商与 `hoxi-auto` 智能路由池，上游凭据绝不下发客户端。
- **中转站质量评估与独立裁判（Observer）审码**：实时记录超时重试率、工具一次成功率与人工回滚率，支持配置独立轻量裁判模型结合 Git Diff 与 LSP 编译诊断自动打分；Go 进程内网关显式强制 `Proxy: nil` 绕过系统代理，本地回环通信永远零 502。

### 3. 多开 Agent 互相改乱代码，或者拿不准哪个模型改得更好
在同一个仓库开两个终端跑 Agent，未提交的代码瞬间互相覆盖；想试试同一个需求用 Claude 还是 DeepSeek / Gemini 写得更干净，手动切分支太折腾。
- **Git Worktree 会话级物理隔离**：每个会话可独立分配 `.hoxi/worktrees/<session-id>` 物理沙箱分支，4 个任务并行狂奔也互不污染；空闲时也可随时一键切回本地主目录模式。
- **多模型 Worktree 赛马场（Multi-Model Arena）**：一条指令同时拉起 **2～4 个独立 Worktree** 让不同模型并发解题（例如让 Claude 与高性价比国产模型同台竞技），在右侧 `@pierre/diffs` 面板直观对比代码质量，挑最满意的一版一键 Commit & Merge 回主分支。
- **单步时光机回滚（Revert / Fork）**：任何一步改崩了，连同磁盘代码快照与对话节点一键回退（`/revert` & `/unrevert`），或从任意历史节点分叉（`/fork`）尝试另一套方案。

### 4. 长对话“越写越忘”反复踩坑，全用旗舰模型烧钱太狠
对话超过三四十轮后，上下文窗口塞满历史废话，Agent 开始遗忘最初定下的架构红线；而所有杂活都走主模型，Token 账单涨得肉疼。
- **8 维结构化长效记忆（`.hoxi/memory/`）**：自动将长会话压缩沉淀为**目标、约束、关键事实、已改文件、报错与修复、待办、当前进展、下一步** 8 个结构化维度，跨轮次持久注入，长线重构不失忆。
- **角色化模型路由（省钱利器）**：主对话用顶级编码模型；会话标题生成、后台子代理（Subagent）、8 维记忆压缩、Git 提交信息生成可分别指定便宜快速的轻量模型（或本地 Ollama / vLLM），其中 Git Commit 更支持 **0 延迟纯本地规则合成**，不花一分冤枉钱。

### 5. 离开工位不敢放手，线上服务器还得背电脑管（Seatbelt 沙箱 + 手机端双栖遥控）
独立开发者与精干团队既要写代码，又要管线上服务器。全自动放行怕 Agent 一句 `rm -rf` 删库或泄露密钥；全手动又得死盯屏幕点确认；出门在外还得随时处理审批与服务器告警。
- **跨引擎统一高危门控 + Seatbelt 进程沙箱 + 无人值守模式（`unattended`）**：无论底层是 OpenCode、Claude Code、Antigravity 还是 Gemini CLI，涉及 `rm`、`sudo`、`git push --force`、敏感文件读取等高危指令一律经 PreToolUse Hook 硬拦截并点亮 macOS 状态栏 `Attention` 徽章。开启**无人值守模式**后，会话强制运行在「隔离 Git Worktree + macOS Seatbelt 操作系统沙箱」内，自动剥离环境变量凭据、封锁 `~/.ssh` / `~/.aws` / `.env` 读取，并对动态拼接命令、`git push`、提权与外发执行零妥协硬拒绝。
- **运行时中途纠偏（Steer）+ 隔离浏览器 & Chrome Link 扩展**：Agent 思路偏了无需强杀重来，随时插入指令实时纠偏；既可使用会话独占的 Headless Chrome，也可通过 Chrome Link 扩展（`extensions/chrome`，一次性 2 分钟连接码）安全接管本机已打开并登录的 Chrome 标签页。
- **手机端随身控制台（[`client/`](client/README.zh-CN.md) — `沟通 · 工作台 · 一人公司 · 我的`）**：
  1. **三通道弹性连接（局域网优先、失败回落中继）**：支持**局域网扫码直连**（`:5417`）、**Cloudflare Tunnel 中继**（`:5418`）以及 **[GoPanel](https://github.com/hoxiai/gopanel) 反向隧道 + iPhone APNs 审批提醒推送**（推送载荷仅含待处理计数，绝不经第三方通道传输代码或命令明文）。
  2. **工具意图识别 + 修改类操作强制确认闭环**：一句话查询 **Hoxi 桌面编码进展**与 **GoPanel 服务器水位**，或下发指令跑单测、审查/提交 Git 改动、批准安全门控、重启容器、续期 SSL 证书；所有修改类动作强制生成 **2 分钟有效、单次消费的确认卡片**，并携带 `X-Idempotency-Key` 与 `instructionId` 幂等对账，杜绝误触与弱网重放。
  3. **原生语音输入与连续通话**：内置 iOS `.voiceChat` 硬件回声消除、50 秒识别轮转、开口或点击即打断（`onUserBargeIn`），采用原生 ASR + 模型网关 `/v1/audio/speech` 服务端 TTS（失败自动回落系统语音），并支持挂载节点端发现的 Agent Skills。

---

## 核心架构一览

```text
┌─────────────────────────────────────────────────────────────────────────┐
│ 原生宿主与移动端双栖控制 (Wails v2 + NSStatusBar + Flutter 手机客户端)   │
│  • macOS WKWebView (~30MB 内存, 0.2s 极速冷启动, 原生毛玻璃与交通灯)     │
│  • 状态栏常驻托盘 (实时显示 Running 并发计数 / Attention 待审批提醒)     │
│  • 移动端 (`client/`): 局域网/中继双通道接管 Hoxi 桌面与 GoPanel 服务器  │
├─────────────────────────────────────────────────────────────────────────┤
│ 三栏 SolidJS 极速工作区 (零虚拟 DOM, 120Hz rAF 批处理满帧流式渲染)       │
│  • 左栏：多项目管理、Git Worktree 沙箱切换、跨软件会话接管中心           │
│  • 中栏：统一时间线、Subagent 嵌套树、ThoughtBox 思考抽屉、Steer 输入框 │
│  • 右栏：Pierre Diffs 代码对比、8 维记忆库、文件树、WebGL 终端、浏览器   │
├─────────────────────────────────────────────────────────────────────────┤
│ Go 守护进程、本地回环网关与会话中枢 (`internal/`)                       │
│  • Hoxi 自主会话存储 (`~/.hoxi/sessions/<id>/session.json` + JSONL)     │
│  • Git Worktree 沙箱、Seatbelt 进程沙箱与统一审批门控 (`internal/runner`)│
│  • 8 大工具会话无损物化、本机 OpenAI 兼容中转与健康度熔断选模            │
│  • 本地防劫持网关 (`Proxy: nil` 免疫代理 502, `FlushInterval: -1`)       │
├─────────────────────────────────────────────────────────────────────────┤
│ 可插拔多执行引擎层 (`internal/runner` & `internal/opencode`)            │
│  • OpenCode (原版 `opencode serve` 零侵入监管 + 9 级模糊替换 + LSP 诊断)│
│  • Claude Code (`claude`) / Antigravity (`agy`) / Gemini CLI (`gemini`) │
│  • Qoder / CodeBuddy                                                    │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 快速开始

### 1. 直接下载安装（普通用户推荐）
- **macOS (Apple Silicon `arm64`, `>= 13.0`)**：前往 [GitHub Releases](https://github.com/hoxiai/hoxi/releases) 下载 `Hoxi-<version>-darwin-arm64.dmg`，拖入 `Applications` 即可。
- **Windows 10 / 11 (64-bit `x64`)**：
  - **图形化安装包**：下载 [`Hoxi-<version>-windows-amd64-setup.exe`](https://github.com/hoxiai/hoxi/releases) 双击安装（自动部署至 `%LOCALAPPDATA%\Programs\Hoxi`、内置 `opencode.exe` 引擎、自动检测安装 WebView2 Runtime、创建桌面与开始菜单快捷方式并注册 `hoxi://` 登录回调协议，支持应用内自更新）。
  - **PowerShell 一行命令安装**：
    ```powershell
    irm https://raw.githubusercontent.com/hoxiai/hoxi/main/scripts/install.ps1 | iex
    ```
  - Windows 暂不支持操作系统命令沙箱和无人值守审批模式。OpenCode Console 账号导入、Qoder 会话发现等 SQLite 功能需要在 `PATH` 中安装 `sqlite3.exe`，安装包目前不包含它。

### 2. 源码构建环境要求
- **macOS** `>= 13.0` 或 **Windows 10/11** (`x64`)
- **Go** `>= 1.23`
- **Node.js** `>= 20`（或 **Bun** `>= 1.2`）
- **Wails CLI v2**（`go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0`）
- **Agent 引擎**：正式发布包已内置 [`opencode`](https://github.com/anomalyco/opencode)；开发模式会在缺失时自动下载经 SHA-256 校验的固定版本到 `~/.hoxi/bin`。也可以自行在 `PATH` 安装 `opencode`、`claude`、`agy`、`gemini`、`qoder` 或 `codebuddy`

### 本地开发运行
```bash
# 启动桌面端开发模式（带热更新）
wails dev

# 启动无头守护进程模式（纯 CLI / 浏览器访问）
go run ./cmd/hoxi --no-desktop
```

### 构建生产级桌面应用与安装包
```bash
# macOS (build/bin/Hoxi.app & Hoxi.dmg)
./build-desktop.sh

# Windows PowerShell (build/bin/Hoxi-<version>-windows-amd64-setup.exe & .zip)
.\scripts\build-windows.ps1
```

### 质量校验与测试
```bash
go build ./... && go test ./internal/...
npx --prefix web tsc -p web/tsconfig.json --noEmit
npm --prefix web run check:i18n && npm --prefix web run check:lines
npm --prefix web test && npm --prefix web run build
```

---

## 深入文档与生态索引

- **[README.md](README.md)**: English Documentation
- **[GoPanel (`hoxiai/gopanel`)](https://github.com/hoxiai/gopanel)**: 轻量级服务器运维与远程 AI 编程面板（可由本项目 `client/` 手机端直接管理）
- **[client/README.zh-CN.md](client/README.zh-CN.md)**: Hoxi 移动端开发指挥台与 GoPanel 手机服务器管家（Flutter，iOS / Android）
- **[DEVELOPMENT.md](DEVELOPMENT.md)**: 生产级架构蓝图、OpenAPI/SSE 契约、Worktree 状态机与插件细节
- **[HANDOFF_AND_CLI_BRIDGE.md](HANDOFF_AND_CLI_BRIDGE.md)**: 8 大外部 CLI 会话解析、统一 IR 物化与本地凭证桥接规范
- **[BROWSER.md](BROWSER.md)**: 会话级隔离 Headless Chrome 审查器、人工接管机制与安全边界
- **[docs/releases.md](docs/releases.md)** & **[docs/auto-updates.md](docs/auto-updates.md)**: DMG 签名公证、Sparkle EdDSA 自动更新与一键发版流水线
- **[CONTRIBUTING.md](CONTRIBUTING.md)**: 本地开发流程、必跑门禁与 PR 规范
- **[SECURITY.md](SECURITY.md)**: 漏洞上报渠道与本地安全边界
- **[docs/telemetry.md](docs/telemetry.md)**: 启动上报的确切字段与关闭方式
- **[AGENTS.md](AGENTS.md)**: 工程分层约束与开发准则

---

## 开源协议

Apache-2.0 © [Hoxi (hoxi.ai)](https://hoxi.ai)

第三方组件声明：[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)
