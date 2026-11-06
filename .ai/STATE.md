# hoxi 动态交接班日志 (STATE.md)

- **Status**: `READY`
- **Last Updated**: 2026-11-07
- **Active Branch / Worktree**: `main`（未提交）

## Last Completed
- 修正官方运行器的 Claude 上下文窗口：`internal/runner/agy_process.go` 原先把 Antigravity / Claude Code 下所有 Claude 写死为 200K，且该值优先于前端家族表，导致「Qingpu AI / 极简大卡片工作台设计」这类 Antigravity Opus 5.5 会话在 33 万 token 时显示满格红色并提示超限。现按前端规则：Opus / Sonnet / Fable 主版本 ≥5 为 1M 窗口 / 128K 输出，带 Claude Code `[1m]` 标记的为 1M，其余保持 200K；价格不变。
- 压缩能力：`SessionCapabilities` 新增 `compact`（只有 OpenCode 为 true，与 `session_summarize.go` 一致）；前端 `canCompactSession` 读该字段，旧版后端缺字段时按运行器推断。上下文弹层对不支持压缩的会话隐藏「压缩上下文」按钮，接近/超出窗口时改为提示「该运行时自行管理上下文…建议新开会话」。
- 改动文件：`internal/runner/{agy_process.go,agy_process_test.go,types.go,runner_test.go}`、`web/src/{services/sse-types.ts,store/session-capabilities.ts,components/timeline/ContextProgressPill.tsx,__tests__/session-capabilities.test.ts}`、i18n `timeline.ts` 双语。
- 排查结论（未改代码）：该会话上下文大是真实内容所致（两版 HTML 原型约 100KB、读代码与规格、历次加密思考约 22%），Hoxi 续接轮只发用户原话，无重复注入，token 记账与请求体积一致。

## Verification
- `./verify.sh` 退出码 0（go vet / go test / web lint / web test 632 通过 / web build）；`go test -race ./internal/runner/` 退出码 0。
- 隔离 headless 夹具（独立 HOME、`AGY_BIN` 替身回放真实 `agy models`、会话记录副本且项目目录指向 scratch）：`/api/hoxi/runner/antigravity/models` 的 Claude 5.5 全部返回 1000000 / 128000，`claude-code` 下 `claude-opus-5-5` 同样为 1M；会话列表返回 `capabilities.compact:false`；界面上下文弹层显示「39.58万 / 100万」40%、无压缩按钮；把副本模型改成窗口更小的旧型号后显示新超限提示且无压缩按钮。
- 未在打包后的桌面应用里点验（已安装的 Hoxi 仍是旧二进制，需重新构建安装后生效）。

## Next Actions
- `assistant-error-card.tsx` 的「上下文已超出窗口 → 压缩并重试」在原生 CLI 会话上同样无效，仅在模型真的返回超限错误时出现，未改。
- CodeBuddy / Qoder 渠道里 Claude 仍按 200K，属转售渠道真实窗口未核实，未改。
- 上轮遗留：`~/.agents/skills` 下用户自有开发技能可加按需入口；上游 `bash`/`task`/`todowrite` 工具描述约 12KB 未改。
- 工作区另有其他会话未提交改动（`internal/provider/*`、web provider 相关文件、Spinner/活动标志样式），与本次无关，提交时只取本次文件。

## [BLOCKED]
- None
