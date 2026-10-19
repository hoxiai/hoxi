# Hoxi

[English](README.md) | [简体中文](README.zh-CN.md)

**Hoxi** ([hoxi.ai](https://hoxi.ai)) 是专为高强度 AI 研发打造的 macOS 原生多 Agent 并行工作台。

它不是又一个套壳 Electron 编辑器（仅 **~30 MB 内存**、**0.2 秒冷启动**），而是把你机器上的 **OpenCode**、**Claude Code**、**Google Antigravity (`agy`)**、**Qoder（通义灵码）**、**CodeBuddy（腾讯云）** 统一收进同一个三栏工作区——支持会话级 Git Worktree 物理隔离、8 大工具跨软件无损接管续做、中转站实时防降智测评，以及手机端远程指挥。

---

## 直击 5 个最痛的日常研发场景

### 1. 额度限流、账号被封或单工具死循环，换工具就得从头讲起
日常写代码往往同时装了 Claude Code、Codex、Antigravity、Trae、Qoder、CodeBuddy。一旦主力工具触发 Rate Limit、额度见底，或者在某个诡异 Bug 上死循环，切到别的工具就要重新贴文件、重新交代上下文。
- **跨软件会话一键无损接管（Session Handoff）**：自动扫描本机 **Claude Code**、**OpenAI Codex CLI**、**Grok CLI**、**Antigravity (Gemini)**、**OpenCode** 以及国内高频使用的 **Trae**、**Qoder**、**CodeBuddy** 历史会话。一键导入完整对话、思考链（Reasoning）、工具调用记录与已改文件，并自动提炼 **8 维交接记忆**，换个模型无缝接着干。
- **同会话多引擎热切换**：无需新建对话，同一个会话内可随时在 `OpenCode` / `Claude Code` / `Antigravity` / `Qoder` / `CodeBuddy` 运行时之间平滑切换；Claude Code 与 Antigravity 派发的子代理（Subagent）直接在时间线内以嵌套任务树实时展示。

### 2. 中转站“偷偷降智 / 掺水 / 断流”，开着 Clash / Surge 动不动 502
国内开发者大量混用 API 中转站、聚合网关或国产模型（DeepSeek、千问 Qwen、智谱 GLM、Kimi、MiniMax、豆包、硅基流动），但中转站到底稳不稳、有没有偷偷换量化模型“降智”，全靠体感玄学；而且系统一开全局代理，本地 Agent 守护进程经常报 `502 Bad Gateway`。
- **中转站质量评估与防幻觉档案**：基于真实会话实时统计每个端点的**首字延迟（TTFT）**、**超时重试率**、**工具一次成功率**、**连续失误（疑似降智/幻觉）**与**人工回滚率**，自动给出健康度评分。
- **独立裁判模型（Observer）静默审码**：支持配置独立轻量裁判模型，在每次代码修改后结合 Git Diff 与 LSP 编译诊断自动打分，量化统计各中转站与模型的**真实代码合格率**。
- **原生免疫代理 502**：Go 进程内网关显式强制 `Proxy: nil` 绕过系统 `HTTP_PROXY` / `HTTPS_PROXY`，无论 Clash / Surge 怎么切规则，本地回环通信永远零 502、零端口暴露。

### 3. 多开 Agent 互相改乱代码，或者拿不准哪个模型改得更好
在同一个仓库开两个终端跑 Agent，未提交的代码瞬间互相覆盖；想试试同一个需求用 Claude 还是 DeepSeek / Gemini 写得更干净，手动切分支太折腾。
- **Git Worktree 会话级物理隔离**：每个会话可独立分配 `.hoxi/worktrees/<session-id>` 物理沙箱分支，4 个任务并行狂奔也互不污染；空闲时也可随时一键切回本地主目录模式。
- **多模型 Worktree 赛马场（Multi-Model Arena）**：一条指令同时拉起 **2～4 个独立 Worktree** 让不同模型并发解题（例如让 Claude 与高性价比国产模型同台竞技），在右侧 `@pierre/diffs` 面板直观对比代码质量，挑最满意的一版一键 Commit & Merge 回主分支。
- **单步时光机回滚（Revert / Fork）**：任何一步改崩了，连同磁盘代码快照与对话节点一键回退（`/revert` & `/unrevert`），或从任意历史节点分叉（`/fork`）尝试另一套方案。

### 4. 长对话“越写越忘”反复踩坑，全用旗舰模型烧钱太狠
对话超过三四十轮后，上下文窗口塞满历史废话，Agent 开始遗忘最初定下的架构红线；而所有杂活都走主模型，Token 账单涨得肉疼。
- **8 维结构化长效记忆（`.opencode/memory/`）**：自动将长会话压缩沉淀为**目标、约束、关键事实、已改文件、报错与修复、待办、当前进展、下一步** 8 个结构化维度，跨轮次持久注入，长线重构不失忆。
- **角色化模型路由（省钱利器）**：主对话用顶级编码模型；会话标题生成、后台子代理（Subagent）、8 维记忆压缩、Git 提交信息生成可分别指定便宜快速的轻量模型（或本地 Ollama / vLLM），其中 Git Commit 更支持 **0 延迟纯本地规则合成**，不花一分冤枉钱。

### 5. 开自动放行怕删库，关自动放行又不敢离开工位
开了 `accept-edits` 怕 Agent 一句 `rm -rf` 或 `git reset --hard` 把心血扬了；全改手动确认又得死盯屏幕当“回车工具人”。
- **跨引擎统一高危门控（Safety Gate）**：普通读写自动丝滑放行；一旦涉及 `rm`、`sudo`、`chmod`、`diskutil`、`git push --force`、`git reset --hard` 等高危指令，无论底层是 OpenCode、Claude Code 还是 Antigravity，一律经 PreToolUse Hook 硬拦截，macOS 顶部状态栏实时亮起 `Attention` 徽章并弹出内联审批卡（`仅本次` / `始终允许` / `拒绝并附原因`）。
- **运行时中途纠偏（Steer）**：看 Agent 思路偏了无需强杀重来，运行途中随时插入补充指令实时纠偏。
- **内置无头浏览器 + 一键人工接管**：右侧面板直连会话独占的 Headless Chrome。Agent 自测页面遇到登录框或验证码时，点击**人工接管（Manual Control）**直接在画面内输入账号密码，搞定后一键交回 Agent 继续执行。
- **Hoxi 手机端开发指挥台（`client/`）**：配套 Flutter 移动端（iOS / Android），下楼拿快递或喝咖啡时，用手机随时看实时流式进度、审阅文件 Diff、发送 Steer 纠偏指令、一键审批高危操作。

---

## 核心架构一览

```text
┌─────────────────────────────────────────────────────────────────────────┐
│ 原生宿主与多端协同 (Wails v2 + Objective-C NSStatusBar + 手机端指挥台)   │
│  • macOS WKWebView (~30MB 内存, 0.2s 极速冷启动, 原生毛玻璃与交通灯)     │
│  • 状态栏常驻托盘 (实时显示 Running 并发计数 / Attention 待审批提醒)     │
├─────────────────────────────────────────────────────────────────────────┤
│ 三栏 SolidJS 极速工作区 (零虚拟 DOM, 120Hz rAF 批处理满帧流式渲染)       │
│  • 左栏：多项目管理、Git Worktree 沙箱切换、跨软件会话接管中心           │
│  • 中栏：统一时间线、Subagent 嵌套树、ThoughtBox 思考抽屉、Steer 输入框 │
│  • 右栏：Pierre Diffs 代码对比、8 维记忆库、文件树、WebGL 终端、浏览器   │
├─────────────────────────────────────────────────────────────────────────┤
│ Go 守护进程、本地回环网关与会话中枢 (`internal/`)                       │
│  • Hoxi 自主会话存储 (`~/.hoxi/sessions/<id>/session.json` + JSONL)     │
│  • Git Worktree 沙箱与快照回滚引擎 (`internal/worktree`)                │
│  • 8 大工具会话无损物化与本地 CLI 协议桥 (`internal/handoff`)           │
│  • 本地防劫持网关 (`Proxy: nil` 免疫代理 502, `FlushInterval: -1`)       │
├─────────────────────────────────────────────────────────────────────────┤
│ 可插拔多执行引擎层 (`internal/runner` & `internal/opencode`)            │
│  • OpenCode (原版 `opencode serve` 零侵入监管 + 9 级模糊替换 + LSP 诊断)│
│  • Claude Code (`claude`) / Antigravity (`agy`) / Qoder / CodeBuddy     │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 快速开始

### 环境要求
- **macOS** `>= 13.0`（主力支持 Apple Silicon `arm64`）
- **Go** `>= 1.23`
- **Node.js** `>= 20`（或 **Bun** `>= 1.2`）
- **Wails CLI v2**（`go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0`）
- 本机 `PATH` 已安装任意一个受支持的 Agent CLI：[`opencode`](https://github.com/anomalyco/opencode)、`claude`、`agy`、`qoder` 或 `codebuddy`

### 本地开发运行
```bash
# 启动桌面端开发模式（带热更新）
wails dev

# 启动无头守护进程模式（纯 CLI / 浏览器访问）
go run ./cmd/hoxi --no-desktop
```

### 构建生产级 macOS 应用 (`build/bin/Hoxi.app`)
```bash
./build-desktop.sh
```

### 质量校验与测试
```bash
go build ./... && go test ./internal/...
npx --prefix web tsc -p web/tsconfig.json --noEmit
npm --prefix web run check:i18n && npm --prefix web run check:lines
npm --prefix web test && npm --prefix web run build
```

---

## 深入文档索引

- **[README.md](README.md)**: English Documentation
- **[DEVELOPMENT.md](DEVELOPMENT.md)**: 生产级架构蓝图、OpenAPI/SSE 契约、Worktree 状态机与插件细节
- **[HANDOFF_AND_CLI_BRIDGE.md](HANDOFF_AND_CLI_BRIDGE.md)**: 8 大外部 CLI 会话解析、统一 IR 物化与本地凭证桥接规范
- **[BROWSER.md](BROWSER.md)**: 会话级隔离 Headless Chrome 审查器、人工接管机制与安全边界
- **[client/README.zh-CN.md](client/README.zh-CN.md)**: Hoxi 移动端开发指挥台（Flutter，支持 Hoxi 与 GoPanel 双节点）
- **[docs/releases.md](docs/releases.md)** & **[docs/auto-updates.md](docs/auto-updates.md)**: DMG 签名公证、Sparkle EdDSA 自动更新与一键发版流水线
- **[AGENTS.md](AGENTS.md)**: 工程分层约束与开发准则

---

## 开源协议

Apache-2.0 © [Hoxi (hoxi.ai)](https://hoxi.ai)
