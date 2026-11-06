# hoxi 动态交接班日志 (STATE.md)

- **Status**: `READY`
- **Last Updated**: 2026-11-06
- **Active Branch / Worktree**: `main`

## Last Completed
- Antigravity split-brain 改为无损续接：agy 新建对话时常同时留下有历史的 `<id>.pb` 和 0 步骤的空壳 `<id>.db`，续接会读空壳而失忆。原处理是丢弃对话、重开并用文本回放历史（缓存全失效 + 上下文丢失）；现在若 sqlite3 能证明 `.db` 的 `steps` 为 0，就把 `.db/-wal/-shm` 改名为 `*.hoxi-empty-<ms>` 后继续 `--conversation` 原对话，否则仍走原重置流程。
- 改动：新增 `internal/runner/agy_split_brain.go`（`quarantineEmptyAgySQLiteStore`）、`internal/runner/agy_split_brain_test.go`；`internal/runner/prompt_submit.go` 调用处 +4 行。
- 同期结论（未改代码）：Antigravity 运行时思考只在每步结束后补显示，是 agy 1.4.12 stream-json 不输出思考所致；Hoxi 调 agy 整体缓存命中 94.9%，与直接用 agy 一致。

## Verification
- `./verify.sh` 退出码 0；新测试含 WAL 模式真实 SQLite 空壳、非空库、非 SQLite 桩、缺 sqlite3 四种情形，及端到端续接用例；去掉修复后端到端用例失败（反向检查）。
- 真实 agy 验证（隔离 `HOXI_HOME` 的 headless 实例）：第 1 轮自然产生 split-brain，第 2、3 轮均保持原 conversation、无历史回放、agy 正确复述首轮内容，第 2 轮缓存命中 6196/8514。
- 未在打包后的桌面应用里复核；测试期间改写过 `~/.gemini/config/hooks.json`，已恢复为 `/Applications/Hoxi.app` 路径。

## Next Actions
- 可选：VS Code Antigravity 插件常驻约 19 个 `agy --hub` 进程（每个 400–500MB），全部只读打开每个对话的 `.db`，疑似 split-brain 高发诱因，可关闭闲置窗口观察发生率。
- 小优化（未做）：`cliSystemDelivered` 仅存内存，Hoxi 重启后续接会话会重复发送一次执行指引（一周约 24 次、约 1.4 万 token）。
- 工作区另有未提交的 provider/web Gemini 协议适配改动（另一会话），与本次无关。

## [BLOCKED]
- None
