# hoxi 动态交接班日志 (STATE.md)

- **Status**: `READY`
- **Last Updated**: 2026-11-07
- **Active Branch / Worktree**: `main`（未提交）

## Last Completed
- 自动模型池避开不回报缓存的渠道（2026-11-07）：本地数据显示 `subapi/Trae-deepseek-v4.1-flash`（6479 轮）与 `deepseek-v4.1-flash-solo`（9553 轮）缓存命中与思考 token 永远为 0（回复里有思考文本，是上游 usage 不带 details），轮询池把会话分到它们后，会话粘性会让整段对话每一步都按全量上下文计费（10-30 以来本仓库 8420 万未缓存输入）。`auto-breaker.ts` 新增 `candidateNeverReportsCache` / `filterNoCacheCandidates`（模型级遥测 ≥20 轮且 `cacheHitTurns===0`，同池有其他渠道时跳过，永不清空）；`resolveAutoModelTarget` 在会话粘性判断前应用，已粘在这类渠道上的会话会换走；模型池列表新增「无缓存」状态（`auto-candidate-status.ts`、`AutoModelPoolTab.tsx`、i18n `models.ts` 双语）。测试：`auto-session-affinity.test.ts` +2。
- 官方 `hoxi-official/deepseek-flash`（AINode）从 11-02 起同样成片返回这种精简用量（全项目 11-02 以来约 1947/6743 条回复，却占本地估算费用约 85%；10-30～11-01 几乎全命中），疑与 AINode 10-31/11-02 的客户端模型别名改动有关，属 AINode 服务端选路或配置问题，未在本仓库改，已另提会话卡片。
- 修正官方运行器的 Claude 上下文窗口：`internal/runner/agy_process.go` 原先把 Antigravity / Claude Code 下所有 Claude 写死为 200K，且该值优先于前端家族表，导致「Qingpu AI / 极简大卡片工作台设计」这类 Antigravity Opus 5.5 会话在 33 万 token 时显示满格红色并提示超限。现按前端规则：Opus / Sonnet / Fable 主版本 ≥5 为 1M 窗口 / 128K 输出，带 Claude Code `[1m]` 标记的为 1M，其余保持 200K；价格不变。
- 压缩能力：`SessionCapabilities` 新增 `compact`（只有 OpenCode 为 true，与 `session_summarize.go` 一致）；前端 `canCompactSession` 读该字段，旧版后端缺字段时按运行器推断。上下文弹层对不支持压缩的会话隐藏「压缩上下文」按钮，接近/超出窗口时改为提示「该运行时自行管理上下文…建议新开会话」。
- 改动文件：`internal/runner/{agy_process.go,agy_process_test.go,types.go,runner_test.go}`、`web/src/{services/sse-types.ts,store/session-capabilities.ts,components/timeline/ContextProgressPill.tsx,__tests__/session-capabilities.test.ts}`、i18n `timeline.ts` 双语。
- 排查结论（未改代码）：该会话上下文大是真实内容所致（两版 HTML 原型约 100KB、读代码与规格、历次加密思考约 22%），Hoxi 续接轮只发用户原话，无重复注入，token 记账与请求体积一致。

## Verification
- 无缓存渠道：`./verify.sh` 退出码 0（Go 44 包、web 636 测试、lint、build）；临时浏览器测试在真实 DOM 中渲染模型池，不回报缓存的候选显示「无缓存」及说明、下一轮目标为另一候选（临时文件已删）。未在打包后的桌面应用里点验。
- `./verify.sh` 退出码 0（go vet / go test / web lint / web test 632 通过 / web build）；`go test -race ./internal/runner/` 退出码 0。
- 隔离 headless 夹具（独立 HOME、`AGY_BIN` 替身回放真实 `agy models`、会话记录副本且项目目录指向 scratch）：`/api/hoxi/runner/antigravity/models` 的 Claude 5.5 全部返回 1000000 / 128000，`claude-code` 下 `claude-opus-5-5` 同样为 1M；会话列表返回 `capabilities.compact:false`；界面上下文弹层显示「39.58万 / 100万」40%、无压缩按钮；把副本模型改成窗口更小的旧型号后显示新超限提示且无压缩按钮。
- 未在打包后的桌面应用里点验（已安装的 Hoxi 仍是旧二进制，需重新构建安装后生效）。

## Next Actions
- 本机中转 `relay.go` 对 `hoxi-auto` 逐请求轮询、无会话粘性；目前本机没有 `relay-keys.json`、也没有工具接入中转，暂不改，等真有外部工具用中转跑多轮会话时再加粘性。
- 用户自建池 `deepseek-flash`（轮询）与 `free` 都含 Trae 渠道，新逻辑会自动跳过；如需彻底移除由用户在模型池界面操作。
- `assistant-error-card.tsx` 的「上下文已超出窗口 → 压缩并重试」在原生 CLI 会话上同样无效，仅在模型真的返回超限错误时出现，未改。
- CodeBuddy / Qoder 渠道里 Claude 仍按 200K，属转售渠道真实窗口未核实，未改。
- 上轮遗留：`~/.agents/skills` 下用户自有开发技能可加按需入口；上游 `bash`/`task`/`todowrite` 工具描述约 12KB 未改。
- 工作区另有其他会话未提交改动（`internal/provider/*`、web provider 相关文件、Spinner/活动标志样式），与本次无关，提交时只取本次文件。

## [BLOCKED]
- None
