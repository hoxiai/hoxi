# hoxi 架构速查与自进化避坑指南 (ARCH.md)

## 1. 架构定位与事实源
- **工程类型**：`go-wails`
- **母体血缘**：Hoxi 桌面与移动跨端多 Agent 工作台（官网主题 `site/themes/hoxi` 与 `site/{domains.env,dev.env}` 由 `../apay/scripts/workspace-sites.mjs` 自动软链挂载至 APay 宿主）
- **权威文档入口**：详见项目根目录 `AGENTS.md`（唯一权威架构事实源，必读！）
- **硬性交付门禁**：修改后必须运行 `./verify.sh` 且退出码为 0；上线统一走 `../.hq/ship.sh`。

## 2. 核心不可触碰红线
- 严格遵循上级总工作区 `../CLAUDE.md` 与本项目 `AGENTS.md` 的边界约束。
- 若本项目包含内嵌独立 Git 仓（如 `site/themes/hoxi`），操作 Git 前必须用 `git -C <子目录> status` 核实归属。

## Pitfalls
<!-- Agent 在排障或开发中踩到的隐蔽陷阱，请以「一行一条、合并同类项」的格式追加在下方 -->
- [初始化基线] 切勿在未读完项目根 `AGENTS.md` 的情况下跨目录盲改或臆造接口契约。
- [WebKit 容器查询夹滚动] 贴底跟随的滚动容器（如 `.timeline-result-scroll`）的任何祖先都不得设 `container-type`：WKWebView 会先用旧样式布局尺寸容器子树（新插入节点尚未计入）再补样式，导致 `scrollTop` 被夹到中间高度而只在 macOS 闪动；需要 `cqw` 时把容器声明放在与滚动容器并列的 `absolute inset-0` 浮层上，Chrome 测试测不出来，改滚动/容器样式后用 Playwright WebKit 复核。
- [agy 双存储与思考时机] agy（1.4.12）stream-json 不输出思考，思考在每步结束后才写入 `brain/<id>/.system_generated/logs/transcript*.jsonl`；其对话存储可能同时存在 `<id>.pb` 与空壳 `<id>.db`（WAL 模式，只读查询也会生成 `-wal/-shm`，且 VS Code 插件的 `agy --hub` 进程会常驻只读打开），续接只读 `.db`，所以判断"空壳"必须查 `steps` 行数而非文件大小，挪开时连同 sidecar 一起改名。
- [OpenCode 技能目录注入] OpenCode 1.19.3 每次请求都把全部已发现技能（递归扫 `~/.claude/skills/**`、`~/.agents/skills/**` 与 `skills` 路径）的名称+描述+路径写进系统提示，只有 `permission.skill` 的 `deny` 会同时隐藏并拒绝加载；Hoxi 界面隐藏不影响模型，改模型目录只能走插件 `experimental.chat.system.transform`（该钩子输入没有 directory，用插件工厂参数里的 directory），曝光判定统一读 `/api/hoxi/skills/list` 的 `exposure`。
- [插件 bundle 与并行会话] `internal/opencode/browser_plugin.mjs` 是 esbuild 产物，改 `internal/opencode/*.js` 或 `.opencode/lib/*.js` 后须按 BROWSER.md 命令重打包；重打包会把工作区里其他会话未提交的源码一起打进去，打包前先看 `git status`。
- [隔离 headless 夹具] 用 `env -i HOME=<夹具> HOXI_HOME=<夹具>/.hoxi hoxi --no-desktop` 配本地 OpenAI 兼容假服务可截获真实模型请求；端口一律取随机空闲端口并从日志 `ready url=` 读网关地址，假服务端口被占时请求会静默挂起；要在夹具里出现 Antigravity 模型，用 `AGY_BIN` 指向只回放 `agy models` 输出的替身脚本，不要让夹具碰真实 `~/.gemini`。
- [模型窗口双事实源] 运行器模型目录（Go `inferOfficialRunnerModelSpec`）下发的 `contextLimit` 优先于前端家族表 `inferOfficialModelPricingAndLimits`，改官方模型窗口必须两边同步，否则前端推断会被后端旧值覆盖成误报超限；排查 agy 上下文构成时，对话库 `gen_metadata` 里进行中那一行（完成后缩成 ~1KB）就是模型实际收到的完整请求。
- [渠道缓存回报] 部分中转/官方上游的 usage 不带 `prompt_tokens_details`/`completion_tokens_details`（缓存与思考 token 同时为 0，但回复里有思考文本），`@ai-sdk/openai-compatible` 只认 `prompt_tokens_details.cached_tokens`；判断渠道能否命中缓存看 `~/.hoxi/provider-telemetry.json` 的模型级 `cacheHitTurns/totalTurns`，同时看缓存和思考两个计数为 0 才是"精简用量"渠道，别按会话总量下结论。
- [审批放宽与无人值守旁路] 无人值守的隔离放行路径直接走 `unattendedDecision`，不经 `isDestructivePermissionRequest`，新增的密钥/危险模式必须共用同一正则常量（如 `envFilePattern`）进 `unattendedHardDeny`，否则会出现「auto 要问、无人值守反而放行」（曾漏 `cat apay/.env`）；放宽 shell 语法前先想它能否藏命令或拼出密钥名（`{rm,-rf,.}`、`cat {.e,}nv`），花括号只放过滤参数的整值字面量列表并逐项过密钥判定；Seatbelt 规则要覆盖任意深度时用 `PathRegex`，不要枚举子目录。
- [OpenCode 系统提示字节稳定] 每轮请求的 `system` 就是提示缓存前缀，开始、插话、恢复三条路径必须走同一个构造函数（`openCodeTurnSystem`），不能各自拼接或漏 trim；跨运行时交接的冻结历史每轮重发，渲染时不能用之后还会变的状态（未完成工具按最终的「已中断」渲染，见 `settledHandoffTurns`）；排查缓存失效先比相邻两步请求 `system` 的公共前缀。
