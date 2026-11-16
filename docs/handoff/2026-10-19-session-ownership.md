# 交接：Hoxi 会话体系自主化 + 本地运行时（2026-10-19）

> **历史交接归档（全部第 1～6 步均已合并且完成）**：当前实现以 [`AGENTS.md`](../../AGENTS.md) 与 [`DEVELOPMENT.md`](../../DEVELOPMENT.md) 为准。Hoxi 已全面接管 6 个运行时（`OpenCode`、`Claude Code`、`Antigravity`、`Gemini CLI`、`Qoder`、`CodeBuddy`）的会话目录（`~/.hoxi/sessions/<id>/`）、单一 SSE（`/api/hoxi/event`）、统一审批平面（`/api/hoxi/permission`）以及会话级 `revert` / `fork`；本机桥接端口为 release `127.0.0.1:5410`、dev `127.0.0.1:5420`（回落随机端口）。
> 读者：接手的 AI / 开发者。开始前先读 `AGENTS.md`（800 行上限、单向分层、i18n、OpenCode 零侵入、提交尾注 `Co-authored-by: Hoxi <code@hoxi.ai>`）。

## 1. 目标与原则

**会话归 Hoxi 管，数据格式兼容 OpenCode，OpenCode 降级为执行引擎之一。**

- **数据格式**（message / part / tool state / SSE 事件）：继续兼容 OpenCode。前端时间线就是按这套格式写的。
- **会话所有权**（ID、元数据、存储、列表、状态、事件流）：收回到 Hoxi。
- **执行引擎**（工具循环、pty、lsp、file、mcp）：继续使用 OpenCode，与 Claude Code CLI、Antigravity（`agy`）CLI 平级，都作为适配器。
- 不重写 agent runtime，不 fork OpenCode。

迁移路线（第 1、2 步已完成）：

1. 换存储
2. 统一会话 API
3. 新建的 OpenCode 会话也挂到 Hoxi 记录下
4. 合并成单一 SSE
5. 审批、revert/fork 上移到会话层
6. （可选）批量导入历史 OpenCode 会话

## 2. 已经在 main 上的提交

| 提交 | 内容 |
|---|---|
| `cd97de4` | 切换运行时（OpenCode / Claude Code / Antigravity）时保留会话。`runtime_switch.go` 把 OpenCode 会话收编为 Hoxi 记录，`OpenCodeIDs` 保存引擎句柄，`opencode_turn.go` 用会话 ID 转发 OpenCode 事件 |
| `dd44bd5` | **存储改造**。`~/.hoxi/sessions/<id>/session.json` 存元数据（原子写）；`turns.jsonl` 存记录（只追加的 upsert，过期条目占多数时压缩）。启动只读元数据，`Get` 时才懒加载记录；新增 `Store.Meta`。旧文件 `~/.hoxi/runner-sessions.json` 一次性迁移后改名为 `.migrated`。环境变量 `HOXI_SESSION_STORE_DIR` 指定存储目录。gateway 测试通过 `main_test.go` 把 HOME 指向临时目录 |
| `36fb507` | Antigravity 审批 hook：全局 PreToolUse hook（`~/.gemini/config/hooks.json`）调用 `hoxi agy-hook`，经 loopback 回调 runner，在 `ApprovalGateCard` 里生成 `per_rnr_*` 审批请求 |
| `714c2c3` | 切回会话时保留已展开的轮数和缓存的消息 |
| `ac7e96b` | **统一会话 API** `/api/hoxi/sessions`（详见下文） |

`ac7e96b` 的具体内容：

- `internal/runner/conversations.go`：负责列表、状态、创建。列表合并 Hoxi 会话和尚未收编的 OpenCode 会话，并隐藏引擎句柄；OpenCode 不可用时降级为只返回 Hoxi 会话。
- `internal/gateway/conversation_route.go`：在**所有守卫之前**把 `/api/hoxi/sessions/{id}/...` 改写为实际负责的后端路径，因此隐私授权、session scope、更新门控都按真实路径生效。
  - 已有测试锁住这个顺序：`TestConversationPromptsStillRequireConsent`。
  - 切换运行时（`runtime`）的请求一律交给 runner。
  - Hoxi 管理的会话：`summarize` 返回 `false`；`revert` / `unrevert` / `fork` 返回 501；`diff` 转发到 `/api/hoxi/worktree/diff`。
- 前端 `web/src/services/api.ts` 的所有会话方法只调这一个入口，prompt 统一使用 OpenCode 的请求体格式。

## 3. 已完成、待合并的分支（都基于 `ac7e96b`，均未 push）

| 分支 | 提交 | 内容 | 状态 |
|---|---|---|---|
| `claude/inspiring-dubinsky-e69c0d` | `44bcd4c`、`70704f3` | Antigravity 和 Claude Code 的子代理以子会话形式显示在对话里：各自的 task 卡片、实时步骤、任务描述和汇报。29 个文件 | 开发会话报告检查和实际运行都通过；**未在 Hoxi App 界面里验证** |
| `claude/upbeat-gould-b6a831` | `ffb08ac` | Claude Code runner 的 Bash 调用改走 Hoxi 审批：每轮生成凭证的 HTTP PreToolUse hook。8 个文件 | 用真实 `claude` CLI 测过：批准后 `python3` 执行，被拒的 `rm` 被拦截；**未在界面里验证** |

- 两个分支互相合并没有冲突（已用 `git merge-tree` 验证）。
- `upbeat-gould` 与 main 上未提交的改动只在 `internal/gateway/gateway.go` 重叠。

**合并步骤**：

1. 先处理第 4 节 main 上的未提交改动。
2. 依次合并两个分支。
3. 跑第 8 节的全部校验。
4. 在 App 里手动验证：
   - 让 Antigravity 派两个子代理；
   - 让 Claude Code 执行 `python3 x.py`，以及一个 `rm`，确认会弹出审批。

## 4. ⚠️ main 工作区里有未审查的改动

Hoxi 里的 Gemini 4 会话“帮我完整分析一下项目质量”（`ses_agy_1792368869079_357ecd23475a`）有以下特点：

- 使用 `gemini-4-argon-high`，本地目录模式 + accept-edits，直接在主工作区运行。
- 用户回复了“如果你确定是 bug 的，就开始帮我修复”。
- 它已经修改了约 30 个文件（+338 / −124），涉及 provider、gateway、handoff（`codex.go`、`grok_bridge.go`、`materialize.go`）、`opencode/resolver.go`、`.opencode/lib/permission-policy.js`，以及大量前端 store、组件和测试。
- 交接时它还在运行：有一个审批请求在等用户处理（`rm -rf /tmp/... && go build ./...`）。
- 它的审计报告在 `~/.gemini/antigravity-cli/brain/02a6b119-94db-4b1a-915b-f26f70855328/project_quality_audit.md`。

**处理方式**：

- 等这个会话结束后，逐个文件审查 diff，确认是真 bug 再保留。
- 重点看 `permission-policy.js`（安全门控规则）和 `provider/*`。
- 跑完整校验后再提交。
- 不要直接 `git checkout` 丢弃，也不要在没审查的情况下提交。

## 5. 剩余路线（第 3～6 步）

### 第 3 步：新建的 OpenCode 会话也生成 Hoxi 记录

- **现状**：`conversations.go` 的 `handleConversationCreate` 在运行时是 opencode 时直接调用 OpenCode 的 `POST /session`。
- **目标**：先创建 Hoxi 的 `SessionRecord`（`Runtime: "opencode"`），再创建 OpenCode 会话作为句柄，写进 `OpenCodeIDs`。
  - `opencode_turn.go`（`startOpenCodeTurn`、`steerOpenCodeTurn`、`relayOpenCodeEvent`）已经能驱动 runtime 为 opencode 的记录，主要工作是创建流程和 worktree 绑定。
- **ID 策略**：新会话使用 Hoxi 自己的 ID；已收编的旧会话保留原 ID。
  - `runtime_switch.go` 目前让 Hoxi ID 等于 OpenCode ID，需要兼容这种情况。
- **完成后**：`IsManagedOpenCodeSession` 和 `filterRunnerResponse`（`gateway/proxy_response.go`）就只用于兜底旧会话。

### 第 4 步：单一 SSE

- **现状**：前端 `web/src/services/sse.ts` 同时打开 `/event`（OpenCode，经网关过滤）和 `/api/hoxi/runner/event` 两条 EventSource。
- **目标**：提供一个 `/api/hoxi/event`，合并 runner 的事件和过滤后的 OpenCode 事件（复用 `AllowOpenCodeSSELine`），前端只保留一条连接。
- **需要保留**：状态栏 SSE 监听（`sseTapReadCloser` → `statusbar.Tracker`）、`FlushInterval: -1`、前端 requestAnimationFrame 批处理。

### 第 5 步：审批与 revert/fork 上移

- **审批**：`api.ts` 的 `listPermissions` 目前在前端合并 `/permission` 和 `/api/hoxi/runner/permission`，改为由后端统一提供。
- **revert / fork**：在会话层实现：
  - 用 worktree 快照（`internal/worktree/snapshot.go`）回滚代码；
  - 截断 `turns.jsonl`；
  - 引擎是 OpenCode 时同时调用它的 revert；
  - 引擎是 CLI 时开一个新句柄，并标记 `ContextPending`（见 `runtimeContext()`）。
- **完成后**：删除前端组件里基于 `isRunnerSessionID` 的能力判断，改为 `SessionInfo` 上的能力字段。涉及文件：`MessageStream.tsx`、`message-stream-parts.tsx`、`QueuedPromptsList.tsx`、`ComposerSandboxPill.tsx`、`store/git-commit.ts`、`store/session-sandbox.ts`、`store/workspace-sessions.ts`。

### 第 6 步（可选）

- 批量导入历史 OpenCode 会话，或者保持“首次写操作时收编”的懒方式。
- OpenCode task 工具生成的子会话（带 `parentID`）作为父会话下的子句柄，不单独暴露给前端。

## 6. 已知问题与技术债

来源：2026-10-19 对四个本地导入模型的实测（基础对话和读文件都正常），以及代码审查。

1. **Codex OAuth 导入可能导致 Codex CLI 被登出**（推断，未实测）。
   - 位置：`internal/handoff/codex.go` 的 `importCodexCLIProvider`。
   - 它把 `~/.codex/auth.json` 的 access/refresh token 原样写进 OpenCode `/auth/openai`，`expires` 写死为当前时间 + 24 小时。
   - 两边共用同一个 refresh token；OpenAI 刷新时会让旧 token 失效。
2. **Grok 桥接**（`internal/handoff/grok_bridge.go`）。
   - 使用 Grok CLI 的 OIDC key（有 `expires_at`），Hoxi 不负责刷新；依赖非公开上游 `cli-chat-proxy.grok.com`。
   - 非流式请求聚合时会丢掉 `tool_calls` 和 usage。
   - 纯对话测试里 usage 的 output 为 0。
3. **启动时改写用户配置**：`syncLocalCLIBridgeURLs`（`handoff/cli_status.go`）会把 `~/.hoxi/models.json` 里 grok-cli 的 `baseURL` 改成当前实例的端口。同时跑多个实例（开发、测试）会把正式 App 的配置改坏。
4. **检测页数据不一致**：`detectGeminiCLIStatus` 显示 Antigravity 有 0 个模型，而 `/api/hoxi/runner/antigravity/models` 能返回完整列表。
5. **Claude Code runner 不上报 token 和费用**：消息里的 `tokens` 和 `cost` 为空。
6. **runner 没有无输出超时或活跃检测**：agy 真挂住时会话会一直显示 busy。子代理分支可能已加入活跃指示，合并后请确认。
7. **Store 性能**：
   - 记录首次 `Get` 后会一直缓存在内存里，没有淘汰机制。
   - `cloneRecord` 每次 `Get` / `Update` 都做一次 JSON 往返；会话很大时有开销。
8. **路径推导不可靠**：`runtime_switch.go` 的 `adoptOpenCodeSession` 用 `filepath.Dir` 连续三次推导项目根目录，应该改为从 worktree 绑定直接获取。
9. **敏感信息**：agy 的全局权限白名单保存了完整的历史命令，其中包含数据库连接串和第三方 API key。每次启动 agy 时，这些内容会以 `permission_grant_store` 告警的形式写进 `~/.gemini/antigravity-cli/log/cli-*.log`。
   - 任何解析 agy 日志的代码都不能把原文透传给前端或写进日志。
   - 建议用户清理这份白名单，并轮换泄露的凭据。

## 7. 本地运行时的关键事实

- **Antigravity**：
  - 启动参数：`--input-format stream-json --output-format stream-json --sandbox --mode accept-edits -p=`，stdin 写 `{"event":"user","message":{...}}`。
  - 子代理：输出 `step_type:"subagent"`（`invoke_subagent`，异步派发，立刻 DONE）；随后调用 `schedule` 工具（约 30 秒后回来查看）；汇报以 `step_type:"system_message"` 注入。子代理的内部步骤**不会**出现在 stdout 上。
  - 无头模式不能通过 stream-json 回复审批，只能靠 PreToolUse hook 返回 `auto_approve` 来授权。详见 `docs/antigravity-approval-hook.md`。
- **Claude Code**：
  - 启动参数：`claude -p --input-format stream-json --output-format stream-json --verbose --include-partial-messages --permission-mode acceptEdits`（另见 `upbeat-gould` 分支对审批的改动）。
  - 子代理的内部事件带 `parent_tool_use_id`。

## 8. 校验与测试

每次改动后（AGENTS.md 要求）运行：

```bash
go build ./...
go test ./internal/...
npx --prefix web tsc -p web/tsconfig.json --noEmit
npm --prefix web run check:i18n
npm --prefix web run check:lines
npm --prefix web run build
npm --prefix web test
```

如果要起一个隔离的无头实例做端到端测试，注意以下几点：

- 换一个端口，不要用正式 App 的 5410（或 dev 的 5420）：`--no-desktop -address 127.0.0.1:5199`。
- 设置 `HOXI_SESSION_STORE_DIR` 指向临时目录。
- 用临时 git 仓库作为 `-dir`。
- `grok-cli` 磁盘配置固定写入 `http://127.0.0.1:5410/api/hoxi/bridge/grok-cli/v1`，当前进程只在内存中按实际桥接地址覆盖，不再污染 `~/.hoxi/models.json`。

端到端测试会消耗用户各个 CLI 账号的额度，请使用轻量模型和简短提示。
