# hoxi 架构速查与自进化避坑指南 (ARCH.md)

## 1. 架构定位与事实源
- **工程类型**：`go-wails`
- **母体血缘**：独立自研工程（由 GoPanel 统一部署托管）
- **权威文档入口**：详见项目根目录 `AGENTS.md`（唯一权威架构事实源，必读！）
- **硬性交付门禁**：修改后必须运行 `./verify.sh` 且退出码为 0；上线统一走 `../.hq/ship.sh`。

## 2. 核心不可触碰红线
- 严格遵循上级总工作区 `../CLAUDE.md` 与本项目 `AGENTS.md` 的边界约束。
- 若本项目包含内嵌独立 Git 仓（如 `plugins/app/*`、`admin/`、`site/`），操作 Git 前必须用 `git -C <子目录> status` 核实归属。

## Pitfalls
<!-- Agent 在排障或开发中踩到的隐蔽陷阱，请以「一行一条、合并同类项」的格式追加在下方 -->
- [初始化基线] 切勿在未读完项目根 `AGENTS.md` 的情况下跨目录盲改或臆造接口契约。
- [WebKit 容器查询夹滚动] 贴底跟随的滚动容器（如 `.timeline-result-scroll`）的任何祖先都不得设 `container-type`：WKWebView 会先用旧样式布局尺寸容器子树（新插入节点尚未计入）再补样式，导致 `scrollTop` 被夹到中间高度而只在 macOS 闪动；需要 `cqw` 时把容器声明放在与滚动容器并列的 `absolute inset-0` 浮层上，Chrome 测试测不出来，改滚动/容器样式后用 Playwright WebKit 复核。
- [agy 双存储与思考时机] agy（1.4.12）stream-json 不输出思考，思考在每步结束后才写入 `brain/<id>/.system_generated/logs/transcript*.jsonl`；其对话存储可能同时存在 `<id>.pb` 与空壳 `<id>.db`（WAL 模式，只读查询也会生成 `-wal/-shm`，且 VS Code 插件的 `agy --hub` 进程会常驻只读打开），续接只读 `.db`，所以判断"空壳"必须查 `steps` 行数而非文件大小，挪开时连同 sidecar 一起改名。
- [OpenCode 技能目录注入] OpenCode 1.19.3 每次请求都把全部已发现技能（递归扫 `~/.claude/skills/**`、`~/.agents/skills/**` 与 `skills` 路径）的名称+描述+路径写进系统提示，只有 `permission.skill` 的 `deny` 会同时隐藏并拒绝加载；Hoxi 界面隐藏不影响模型，改模型目录只能走插件 `experimental.chat.system.transform`（该钩子输入没有 directory，用插件工厂参数里的 directory），曝光判定统一读 `/api/hoxi/skills/list` 的 `exposure`。
- [插件 bundle 与并行会话] `internal/opencode/browser_plugin.mjs` 是 esbuild 产物，改 `internal/opencode/*.js` 或 `.opencode/lib/*.js` 后须按 BROWSER.md 命令重打包；重打包会把工作区里其他会话未提交的源码一起打进去，打包前先看 `git status`。
- [隔离 headless 夹具] 用 `env -i HOME=<夹具> HOXI_HOME=<夹具>/.hoxi hoxi --no-desktop` 配本地 OpenAI 兼容假服务可截获真实模型请求；端口一律取随机空闲端口并从日志 `ready url=` 读网关地址，假服务端口被占时请求会静默挂起。
