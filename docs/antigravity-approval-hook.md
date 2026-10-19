# Antigravity Runner 审批门控：PreToolUse Hook 实测与接入方案

> 实测日期：2026-10-19 · agy：`~/.gemini/bin/agy` · 模型：`gemini-3.8-flash-low`
> 启动参数与 Hoxi Runner 完全一致：
> `agy --input-format stream-json --output-format stream-json --sandbox --mode accept-edits --model <m> -p=`，用户消息走 stdin NDJSON。

## 1. 问题回顾

无头模式（`-p`）没有交互式审批。需要审批的命令（例如 `rm`、`npm --prefix web run …`）会被 **soft-deny**：
`result.denied_actions = [{action:"command", display_name:"RunCommand"}]`，stderr 输出 `a tool required the "command" permission that headless mode cannot prompt for, so it was auto-denied`。
官方文档（`/docs/cli/headless/`）也确认：stream-json 输入**不接受**审批回复，发 `control_request` / `control_response` 会直接以 `ERROR` 结束、退出码 2。

## 2. 实测结果

测试命令统一为 `rm -f probe.txt`（无 hook 时必定被拒）。

| # | 配置 | 结果 | 结论 |
|---|---|---|---|
| A | 工作区 `.agents/hooks.json`，hook 返回 `allow` | 仍被拒（`denied_actions`） | `allow` 只表示"交给下一个 hook / 默认策略"，**不会授权** |
| B | `allow` + `permissionOverrides: ["command(rm -f probe.txt)"]` | 仍被拒 | `permissionOverrides` 在无头模式下不起作用 |
| C | 返回 `deny` + `reason` | 被拦截，工具报错 `tool call denied by pre-tool hook: <reason>` | `deny` 生效，`reason` 会原样回给模型 |
| D | `allow` + `overwrite: {CommandLine: "echo …"}` | 实际执行的是改写后的命令 | hook 输出被正确解析，`overwrite` 生效 |
| E–G | 工作区 `.agents/settings.json` 写 `permissions.allow` | 仍被拒 | 工作区 settings **不会被读取**，权限规则只在全局 `~/.gemini/antigravity-cli/settings.json` |
| **H** | **hook 返回 `auto_approve`** | **放行，`rm` 真正执行** | ✅ **无头模式下由 hook 授权的唯一可行方式** |
| I1 | `auto_approve`，hook 先 sleep 75s，`timeout: 120` | 75s 后放行 | ✅ 长 timeout 生效（实测到 120s） |
| I2 | hook sleep 25s，`timeout: 10` | hook 被 kill，工具报错 `pre-tool hook … timed out and was killed`，命令**未执行** | ✅ 超时**失败即拒绝**（fail-closed） |
| J | `auto_approve` + `touch <工作区外路径>` | `Operation not permitted`，文件未创建 | ✅ `--sandbox` 与审批相互独立，批准不等于越出沙箱 |
| K | 全局 `~/.gemini/config/hooks.json`（测试后已删除） | 被读取，`auto_approve` 放行 | ✅ 全局位置可用，hook 的 cwd 为 `~/.gemini/config` |

其他观察：

- **hooks.json 位置**：工作区 `<cwd>/.agents/hooks.json` 和全局 `~/.gemini/config/hooks.json` 都会被读取（工作区不需要在 `trustedWorkspaces` 里）。文档还列出 `settings.json` 内联和插件 `hooks.json` 两种方式，这两种未实测。同一事件的多个命名 hook 会合并后顺序执行。
- **环境变量**：hook 进程继承 agy 进程的环境变量（实测 `HOXI_PROBE_TOKEN` 能透传）。
- **工作目录**：hook 的 cwd 是 `hooks.json` 所在目录，不是工作区目录。
- **stdin 载荷**：`{conversationId, stepIdx, toolCall:{name, args:{CommandLine, Cwd, …}}, workspacePaths, transcriptPath, artifactDirectoryPath, modelName}`。其中 `stepIdx` 等于 stream-json 里的 `step_index`。
- **事件时序**：`step_update(state=ACTIVE, tool=run_command)` 在 hook 开始等待**之前**就已输出（I1：t=6.9s 发出 ACTIVE，t=81.9s 才 DONE）。所以 UI 能在等待期间先渲染工具卡片。
- **调用范围**：每一次 `run_command` 都会调用 hook，包括 `echo` 这类 agy 本来就会自动放行的命令（D）。因此 Hoxi 能看到全部命令，可以统一套用自己的策略。
- 文档里的完整 decision 列表：`allow` / `auto_approve` / `deny` / `ask` / `force_ask` / `deny_unless_prior_grant`。`auto_approve` 会继续遵守用户显式配置的规则（官方说明 Deny > Ask > Allow），所以用户自己写的 deny 规则依然优先。

## 3. 接入方案

### 3.1 总体流程

```text
agy 发起 run_command
 ├─ stdout: step_update ACTIVE ──► translate.go ──► ToolCard（running）
 └─ PreToolUse hook：`hoxi agy-hook`（stdin JSON，环境变量带会话凭证）
      └─ POST http://127.0.0.1:<port>/api/hoxi/runner/hook/pretool   （阻塞等待）
           Runner 校验 token ↔ session，conversationId 和 workspacePaths 必须与会话记录一致
           套用 permission-policy：
             ├─ 安全命令 / 会话内 always 规则命中 ─► auto_approve
             └─ 其余（含破坏性命令）─► 发 permission.asked
                   ├─ 前端：ApprovalGateCard（挂到对应 ToolCard）
                   ├─ 状态栏：SetPendingApproval(true) → Attention
                   └─ 用户点击：
                        once   ─► auto_approve
                        always ─► 记录会话规则 + auto_approve
                        reject ─► deny（reason 回给模型）
                   超时 / Stop / 进程退出 ─► deny
           发 permission.replied，SetPendingApproval(false)
      ◄─ hook 把 decision 输出给 agy
```

### 3.2 Hook 安装位置：全局 `~/.gemini/config/hooks.json`，加环境变量守卫（推荐）

- Hoxi 只维护自己的命名键 `hoxi-approval-gate`。读取 → 合并 → 原子写回，保留用户已有的其他 hook。在第一次启动 Antigravity Runner 时安装，二进制路径变化时更新；设置里提供"卸载"。
- 工作区 `.agents/hooks.json` **不推荐**：local 模式下会往用户仓库里写文件，还可能覆盖仓库自带的 hooks.json。worktree 模式下虽然可以用（另需写 `.git/info/exclude`），但两种模式的行为会不一致。
- 写入的 hook 配置：

```json
{
  "hoxi-approval-gate": {
    "PreToolUse": [{
      "matcher": "run_command",
      "hooks": [{
        "command": "[ -x '/Applications/Hoxi.app/Contents/MacOS/hoxi' ] && exec '/Applications/Hoxi.app/Contents/MacOS/hoxi' agy-hook || printf '{\"decision\":\"allow\"}'",
        "timeout": 600
      }]
    }]
  }
}
```

  `[ -x … ] || allow` 这层守卫是必须的。I2 证明 hook 失败会让工具报错，如果 Hoxi 被移走或卸载，用户自己的 agy / IDE 命令就会全部失败。

### 3.3 `hoxi agy-hook` 子命令（`cmd/hoxi`）

- 读取 stdin，并读取 `HOXI_RUNNER_SESSION_ID`、`HOXI_HOOK_URL`、`HOXI_HOOK_TOKEN` 三个环境变量。
- **三个变量缺任何一个**（说明不是 Hoxi 启动的 agy，比如用户在终端里直接运行或在 IDE 里）：输出 `{"decision":"allow"}`，即交还 agy 默认策略（A 已证明这是无副作用的透传）。
- 变量齐全：用 `http.Transport{Proxy: nil}` 把 stdin 原样 POST 到 `HOXI_HOOK_URL`，请求头带 `Authorization: Bearer <token>`，然后阻塞等待。客户端超时比 hook 的 `timeout` 短 15s。
- 任何错误（网关连不上、非 200、JSON 解析失败）：输出 `{"decision":"deny","reason":"Hoxi approval gate unavailable"}`。在 Hoxi 会话里**失败一律拒绝**。

### 3.4 Runner 后端（`internal/runner/`，按职责拆文件，每个不超过 500 行）

- `agy_process.go`：`cmd.Env = append(os.Environ(), HOXI_RUNNER_SESSION_ID=…, HOXI_HOOK_URL=…, HOXI_HOOK_TOKEN=<每个进程随机生成 32 字节 hex>)`。保持 `--sandbox --mode accept-edits`，**禁止** `--dangerously-skip-permissions`。
- `hook_gate.go`：
  - 路由 `POST /api/hoxi/runner/hook/pretool`，只接受来自 127.0.0.1 的请求，用 `subtle.ConstantTimeCompare` 校验 token 与会话的对应关系。
  - 校验 `conversationId` 与会话记录的 externalID 一致（首次 `init` 之前允许为空），`toolCall.args.Cwd` / `workspacePaths` 必须落在会话目录内。
  - 策略：把 `.opencode/lib/permission-policy.js` 的 `DESTRUCTIVE_RE` / `DESTRUCTIVE_GIT_RE` / `SAFE_COMMANDS` / 命令链拆分移植到 Go。JS 的 `(?<!>)` 是后行断言，Go 的 RE2 不支持，需要改写。再用同一份 fixture 做 JS 和 Go 的一致性测试。规则与 `hoxi-permissions.js` 一致：破坏性命令 → ask，安全命令 → auto_approve，其余 → ask。
  - ask：生成 `PermissionRequest{id:"per_agy_<seq>", sessionID, permission:"bash", patterns:[CommandLine], metadata:{command, cwd, source:"antigravity"}, always:[命令前缀], tool:{messageID, callID}}`，其中 `callID` 通过 `stepIdx` 映射到 translate.go 为该 step 生成的 tool part。然后推送 `permission.asked`，在 channel 上等待。
  - 会话内的 always 规则只存在 Hoxi 自己的会话记录里，**不写** agy 的 `settings.json`，避免污染用户的全局权限。
  - Stop、进程退出或会话删除时，所有挂起的请求统一以 deny 结束，并推送 `permission.replied`。
- `http.go`：新增 `POST /api/hoxi/runner/sessions/{id}/permission/{requestID}/reply`，body 为 `{reply:"once"|"always"|"reject", message?}`。`message` 作为 deny 的 reason 回给模型，用户可以顺便说明原因或给出替代方案。
- 状态栏：调用 `status.SetPendingApproval(requestID, sessionID, true/false)`。
- `translate.go`：
  - 工具报错 `tool call denied by pre-tool hook: …` → ToolCard 显示 `error`，标注"已被拒绝"。
  - `… timed out and was killed` → `error`，标注"审批超时"。
  - 原有的 `denied_actions` 提示保留作兜底（hook 没装上，或非 `run_command` 的工具需要权限时），文案改成引导用户检查 hook 安装状态。

### 3.5 前端

- `services/api.ts` 的 `replyPermission` 按会话 runtime 路由：OpenCode 会话走原接口，Runner 会话走 `/api/hoxi/runner/sessions/{id}/permission/{requestID}/reply`。组件层不需要感知。
- `ApprovalGateCard` 复用现有实现；Runner 的 SSE 已经输出与 OpenCode 相同结构的 `permission.asked` / `permission.replied`。
- 去掉或改写 Runner 会话里"该运行时不支持审批"之类的提示（i18n：同步更新 `types.ts` 与 en-US / zh-CN）。
- 设置页：显示 "Antigravity 审批 Hook：已安装 / 未安装 / 路径过期"，提供重新安装和卸载按钮。

### 3.6 测试

- Go 单元测试：
  - token 错误 → 401。
  - 会话目录外的 Cwd → deny。
  - 安全命令 → auto_approve；破坏性命令 → 挂起，reply 后得到对应 decision。
  - 超时、Stop → deny。
  - `agy-hook` 子命令：缺环境变量 → allow；网关不可达 → deny。
  - hooks.json 合并时保留用户已有的键。
- 策略一致性测试：JS 与 Go 对同一组命令给出相同结论。
- 手动验收：Runner 会话里让 agy 执行 `npm --prefix web run check:i18n && …`，界面弹出 ApprovalGateCard，状态栏显示 Attention。点 once 后命令执行，点 reject 后模型收到拒绝原因。确认 `--sandbox` 仍然阻止写入工作区外的路径。

### 3.7 实现状态（2026-10-19）

已实现：

| 模块 | 文件 |
|---|---|
| `hoxi agy-hook` 客户端 | `internal/agyhook/agyhook.go`，由 `internal/app/app.go` 在 `Run` 最开头分发 |
| 审批网关、回复与列表接口 | `internal/runner/hook_gate.go`，路由为 `/api/hoxi/runner/hook/pretool`、`/api/hoxi/runner/permission[/{id}/reply]` |
| 全局 hooks.json 安装器 | `internal/runner/agy_hook_install.go`，`GET/POST/DELETE /api/hoxi/runner/antigravity/hook` 分别对应查询状态、安装、卸载；每轮 agy 启动前自动确认已安装，仅桌面 App 开启自动安装 |
| 权限策略 Go 移植 | `internal/runner/permission_policy.go`，与 JS 共用 `.opencode/lib/permission-policy.cases.json` |
| 进程接线 | `internal/runner/turn_exec.go`（每轮签发凭证；Stop 或进程退出时拒绝所有挂起请求）、`internal/gateway/gateway.go`（传入回调地址） |
| 前端 | `services/runner-api.ts` / `services/api.ts`（`per_rnr_` 前缀的请求路由到 Runner）、`ApprovalGateCard.tsx`（拒绝说明随回复带回，Runner 请求不显示 OpenCode 规则选择器） |

与原方案的差异：

- 安全命令（策略判定为 allow）由 Go 端直接 `auto_approve`，不推送卡片，与 OpenCode 下 `hoxi-permissions.js` 的行为一致。其余命令推送 `permission.asked`，再由前端已有的 auto / smart / manual 审批模式决定是否自动回复。破坏性命令在任何模式下都需要手动确认。
- 会话内的 always 规则只保存在内存里，按完全相同的命令匹配，Hoxi 重启后失效。破坏性命令永远不会被 always 记住。

未实现：设置页里的 hook 安装状态显示与按钮（后端接口已就绪）。

端到端测试（需要真实的 agy，会消耗少量模型请求）：

```bash
go build -o /tmp/hoxi-e2e ./cmd/hoxi && HOXI_E2E_BIN=/tmp/hoxi-e2e go test -tags agy_e2e -run AgyHookE2E -v ./internal/runner/
```

2026-10-19 实测通过：点"允许一次"后 `rm` 被执行；点"拒绝"后命令被拦截，模型收到拒绝原因 `e2e says no`。

## 4. 未验证 / 风险

- `timeout` 只实测到 120s，600s 是否生效需要再验一次；文档没有给出上限。
- 全局 `~/.gemini/config/hooks.json` 同样会被 Antigravity IDE / App 读取（来自文档，未实测）。由于有环境变量守卫，IDE 里的行为是透传 allow，但每条命令会多一次进程启动（毫秒级）。
- 插件形式（`agy plugin install`）的 hooks 未实测，可以作为替代方案：生命周期更清晰，但同样是全局生效。
- hook 只匹配 `run_command`。在 `accept-edits` 下工作区内的文件编辑本来就自动放行；工作区外的读写由 `--sandbox` 拦截，这类情况会走 `denied_actions` 兜底提示。
