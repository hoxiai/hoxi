# Claude Code Runner 审批门控：HTTP PreToolUse Hook 实测与接入方案

> 实测日期：2026-10-19 · claude：`~/.local/bin/claude` 2.1.303 · 模型：`claude-haiku-4-5-20251001`
> 启动参数与 Hoxi Runner 一致：
> `claude -p --input-format stream-json --output-format stream-json --verbose --include-partial-messages --permission-mode acceptEdits --permission-prompts none`，用户消息走 stdin NDJSON。

## 1. 问题回顾

`--permission-prompts none` 表示“没有人回答权限提示”：所有需要审批的工具调用都会被自动拒绝。
工具结果里写的是 *this session has no approval surface*，`result.permission_denials` 会列出被拒的 Bash 调用。
所以 `python3 sum.py` 这类命令永远跑不起来，Hoxi 的 ApprovalGateCard 也收不到任何请求。

## 2. 实测结果

测试命令是 `python3 -c "open('marker.txt','w').write('x')" && echo done`。
注意：`touch`、`mkdir` 这类命令在 acceptEdits 模式下本来就会自动放行，不能用来测试。

| # | 配置 | 结果 | 结论 |
|---|---|---|---|
| A | 无 hook（`--settings '{}'`） | 被拒，出现在 `permission_denials` 里 | 复现问题 |
| B | `--settings` 内联 `type: "http"` 的 PreToolUse hook，返回 `permissionDecision: "allow"` | 放行，命令真正执行 | ✅ 无头模式下 hook 的 `allow` 能授权 |
| C | 同上，返回 `deny` + reason | 被拦截，工具结果为 `PreToolUse:Bash hook error: <reason>` | ✅ reason 原样回给模型 |
| D | hook URL 不可达 | 回退到 Claude 自己的策略，被拒 | ✅ 失败即拒绝（fail-closed） |
| E | hook 返回非法 JSON | 同 D，被拒 | ✅ fail-closed |
| F | hook 等待 75s 后返回 `allow`（`timeout: 600`） | 75s 后放行 | ✅ 长时间等待可行 |
| G | `--resume <id>` | hook 载荷里的 `session_id` 和原会话一致 | ✅ 可以用 `session_id` 校验会话归属 |

其他观察：

- **headers 环境变量展开**：`"Authorization": "Bearer $HOXI_HOOK_TOKEN"` 搭配 `allowedEnvVars` 时，Claude 会从自己的环境变量里展开这个值。所以 token 不会出现在 argv 中。
- **请求特征**：axios 发出的请求，`Host: 127.0.0.1:<port>`，不带 `Origin` 头，能通过 Gateway 的 `allowRequest` 检查。
- **载荷字段**：`{session_id, transcript_path, cwd, permission_mode, hook_event_name, tool_name, tool_use_id, tool_input:{command, description}}`。`tool_use_id` 和 stream-json 里 `tool_use` 块的 `id` 相同，也就是 Hoxi ToolCard 的 `callID`。
- **事件时序**：`assistant(tool_use)` 在 hook 开始等待之前就已经输出了，所以 ToolCard 会先以 running 状态渲染出来。
- **`--settings` 不需要信任工作区**：在 `-p` 模式下，hook 在未信任的目录里也会执行。

## 3. 接入方案

- `issueClaudeHook`（`internal/runner/claude_hook_gate.go`）给每个 turn 签发一份一次性凭证，绑定 runtime、会话和消息。它会追加 `--settings <inline JSON>`，把 hook 注册到 `*` matcher 上（`Bash` 走命令审批；工作树内普通文件的非敏感 `Read` 透传给 Claude，工作树内外的 `.env` 等敏感文件读取在所有模式下询问；工作树内普通文件的 `Edit`/`Write` 按 `~/.hoxi/approval-policy.json` 策略决定，manual 模式下询问；工作树外读写、工作树内受保护路径如 `.git/`/`.claude/`/`.opencode/`/`.envrc` 的修改、以及 `WebFetch`/`WebSearch`/`mcp__*` 等外部工具走 `gateToolCall` 审批并由 Go 策略决定直接 `allow` 或挂起等待用户）。同时注入 `HOXI_HOOK_TOKEN`、`HOXI_RUNNER_SESSION_ID`，并把 `NO_PROXY` 加上回环地址。这样不会改写 `~/.claude`，在终端或 IDE 里直接启动的 Claude Code 也不受影响。
- plan 模式不挂 hook，继续由 Claude 自己做只读限制。
- `POST /api/hoxi/runner/hook/claude-pretool` 和 agy 共用 `gateCommand` / `awaitApproval`（及 `permission_policy.go` 单次解析策略、`approval_decision.go` 统一审批策略与 `pathWithin` 符号链接/悬空符号链接校验）：
  - `cwd` 超出会话 worktree（含经符号链接/悬空符号链接指向区外）→ `deny`
  - 非破坏性命令按 `~/.hoxi/approval-policy.json` 策略（`auto` / `smart` / `manual` 及工具覆盖、对象规则）与会话内 always 授权判定是否直接 `allow`（manual 模式下只读命令也会询问）
  - 其他命令（包括策略要求询问的命令、含引号/转义/`$(`/反引号的命令、危险参数如 `find -exec/-delete/-fprint*` / `fd -x` / `rg --pre` / `git --output` / `go env -w`，以及 `rm`、`unlink`、`sudo`、`git push --force`/`-uf`/`+refspec`、`git reset --hard`/`--har` 等破坏性命令）→ `per_rnr_*` 请求、ApprovalGateCard，状态栏显示 Attention
  - 破坏性命令（含转义/引号混淆变体）永远不会被记成 always 规则
- doom-loop 守卫（`internal/runner/doom_loop.go`）：同一会话连续 3 次以相同参数调用同一工具（参数按 key 排序后比较；`BashOutput`、`TaskOutput` 等轮询工具除外）时，第 3 次调用会先发出 `doom_loop` 审批。拒绝时返回 `deny`，并把原因交给模型；允许一次后重新计数；选择 always 后本会话不再检查该工具。Antigravity 的 `run_command` / `view_file` 也走同一个守卫。
- Stop、进程退出、超时（580s，早于 hook 的 600s timeout）或 hook 连接断开时，待审批的请求都会变成 `deny`。
- 手动端到端测试：`go test -tags claude_e2e -run ClaudeHookE2E -v ./internal/runner/`
