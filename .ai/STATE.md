# hoxi 动态交接班日志 (STATE.md)

- **Status**: `READY`
- **Last Updated**: 2026-11-06
- **Active Branch / Worktree**: `main`（未提交）

## Last Completed
- 提示词组装与技能注入瘦身：OpenCode 1.19.3 每次请求把全部 49 个已发现技能（含 Claude 应用同步的 15 个专用技能、Hoxi 托管的 19 个电商/交易技能）的长描述写进系统提示。现由 `internal/skills/exposure.go` 给每个技能定曝光 `auto` / `on-demand` / `unavailable`（依据 `permission.skill` 规则、缺失的 `mcp__<server>__` 工具、安装来源；Hoxi 业务技能在 Work 工作区仍常驻，只在编码项目按需），技能面板、`/` 补全、显式选择校验和模型目录共用这一结果。
- 模型侧：插件 `internal/opencode/skill_catalog.js` 在官方 `experimental.chat.system.transform` 里改写 `<available_skills>`（按需技能只留名字索引，不可用技能隐藏），`tool.execute.before` 拒绝加载不可用技能；决定按上游目录块缓存，多轮逐字节稳定。
- 提示词统一：runner `commandExecutionGuidance` 去掉无条件 edit，新增"审查/问答默认只读、已授权工作不反复确认"；插件浏览器/电脑控制说明按真实审批（连续输入免确认、无人值守拒绝）改正并去掉与工具描述重复的部分。
- `AGENTS.md` 23.6KB → 17.7KB：现状、目录树、规划表原文移到 `docs/architecture-overview.md`（附读取条件），约束全部保留，补 Go 分层速查。`docs/skills-development.md` 新增 2.5 曝光规则。
- 改动文件：`internal/skills/{exposure.go,exposure_test.go,skills.go}`、`internal/gateway/{skills.go,skills_surface_test.go}`、`internal/opencode/{skill_catalog.js,skill_catalog_test.mjs,hoxi_plugin.js,browser_plugin.mjs}`、`internal/runner/prompt_submit.go`、`web/src/{services/skills-types.ts,store/skills.ts,components/skills/SkillExposureBadge.tsx,components/skills/SkillsInspectorView.tsx,components/timeline/composer-autocomplete.ts,__tests__/skills.test.ts}`、i18n `skills.ts` 双语、`.github/workflows/ci.yml`、`AGENTS.md`、`docs/architecture-overview.md`、`docs/skills-development.md`。

## Verification
- `./verify.sh` 退出码 0；最终 `go vet`/`go build`/`go test`/`go test -race ./...` 退出码 0；插件与扩展 `node --test` 80/80 通过；新插件测试对旧 bundle 反向运行 8/9 失败。
- 隔离 headless 夹具（复制本机 48 个 SKILL.md + 真实 AGENTS.md + 本地假模型服务，截获真实请求）：每次请求 105.5KB → 72.9KB；技能目录 38.9KB → 12.3KB（49 条 → 17 条 + 名字索引）；AGENTS.md 23.7KB → 17.9KB；工具定义 29.7KB 不变；多轮与跨会话系统提示逐字节一致；按需技能经原生 skill 工具正常加载；显式选择不兼容技能返回 409 与缺失工具名；模型自行加载 `computer-use` 被插件拒绝。
- Work 工作区夹具（`~/.hoxi/workspaces/default`）：Hoxi 业务技能 18 个保持 `auto`，Claude 同步技能仍按需，4 个缺工具技能仍不可用。
- 夹具在网络慢时首次启动要等引擎装 `~/.config/opencode/node_modules`（约 40s），与本次改动无关；修复前二进制同样如此。
- 未在打包后的桌面应用里点验技能面板徽标。

## Next Actions
- 剩余 12.3KB 技能目录主要是 `~/.agents/skills` 下 15 个用户自有开发技能（按 `auto` 保留）；如需再省，可给这类技能加按需配置入口。
- 上游 `bash`/`task`/`todowrite` 工具描述约 12KB、基础模板约 9KB 未改（插件 `tool.definition` 钩子可改，但会偏离上游调优，未做）。
- 导出里"只能输出 tool_call 代码块"协议在 Hoxi 与 OpenCode 1.19.3 中均未找到，疑为模型服务适配层或导出格式，未确认。
- 工作区另有其他会话未提交改动（provider/web Gemini 协议适配、`context_compaction.js` 失败识别、`store_disk.go`），与本次无关；`browser_plugin.mjs` 重打包时已包含其当时源码。

## [BLOCKED]
- None
