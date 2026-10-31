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
- **`view_file` 同样需要 matcher**：2026-10-20 用 agy 1.4.2 复测，无 hook 时读取工作区外文件会软拒并返回 `denied_actions:[{action:"read_file", display_name:"ViewFile"}]`；在 hooks.json 增加 `matcher:"view_file"` 并返回 `auto_approve` 后同一读取成功。读取不受 `--sandbox` 的写入限制影响。
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
- 写入的 hook 配置（macOS / Linux）：

```json
{
  "hoxi-approval-gate": {
    "PreToolUse": [{
      "matcher": "*",
      "hooks": [{
        "command": "[ -x '/Applications/Hoxi.app/Contents/MacOS/hoxi' ] && exec '/Applications/Hoxi.app/Contents/MacOS/hoxi' agy-hook || echo '{\"decision\":\"allow\"}'",
        "timeout": 600
      }]
    }]
  }
}
```

  在 Windows（`runtime.GOOS == "windows"`，`internal/runner/agy_hook_install.go`）下命令为：
  `if exist "C:\...\Hoxi.exe" ("C:\...\Hoxi.exe" agy-hook) else (echo {"decision":"allow"})`。

  matcher 是正则：`"*"` / `""` 匹配所有工具，`"run_command|view_file"` 匹配其中之一，`"browser_.*"` 匹配前缀。Hoxi 用通配符 `"*"` 覆盖全部工具，再由 `hook_gate.go` / `agy_hook_tools.go` 按工具分类，避免 agy 新增工具（如 1.4.3 的 `read_file`）时漏配 matcher 被无头模式软拒。

  `[ -x … ] || echo '{"decision":"allow"}'`（Windows 下 `if exist … else …`）这层守卫是必须的。I2 证明 hook 失败会让工具报错，如果 Hoxi 被移走或卸载，用户自己的 agy / IDE 命令就会全部失败。

### 3.3 `hoxi agy-hook` 子命令（`cmd/hoxi`）

- 读取 stdin，并读取 `HOXI_RUNNER_SESSION_ID`、`HOXI_HOOK_URL`、`HOXI_HOOK_TOKEN` 三个环境变量。
- **三个变量缺任何一个**（说明不是 Hoxi 启动的 agy，比如用户在终端里直接运行或在 IDE 里）：输出 `{"decision":"allow"}`，即交还 agy 默认策略（A 已证明这是无副作用的透传）。
- 变量齐全：用 `http.Transport{Proxy: nil}` 把 stdin 原样 POST 到 `HOXI_HOOK_URL`，请求头带 `Authorization: Bearer <token>`，然后阻塞等待。客户端超时比 hook 的 `timeout` 短 15s。
- 任何错误（网关连不上、非 200、JSON 解析失败）：输出 `{"decision":"deny","reason":"Hoxi approval gate unavailable"}`。在 Hoxi 会话里**失败一律拒绝**。

### 3.4 Runner 后端（`internal/runner/`，按职责拆文件，每个不超过 500 行）

- `agy_process.go`：`cmd.Env = append(os.Environ(), HOXI_RUNNER_SESSION_ID=…, HOXI_HOOK_URL=…, HOXI_HOOK_TOKEN=<每个进程随机生成 32 字节 hex>)`。保持 `--sandbox --mode accept-edits`，**禁止** `--dangerously-skip-permissions`。
- `hook_gate.go`：
  - 路由 `POST /api/hoxi/runner/hook/pretool`，只接受来自 127.0.0.1 的请求，用 `subtle.ConstantTimeCompare` 校验 token 与会话的对应关系。
  - 校验 `conversationId` 与会话记录的 externalID 一致（首次 `init` 之前允许为空），`toolCall.args.Cwd` / `workspacePaths` 必须落在会话目录内（`pathWithin` 同时解析多层符号链接与悬空符号链接，防止经符号链接越界）。
  - 策略：把 `.opencode/lib/permission-policy.js` 移植到 Go（`internal/runner/permission_policy.go` 与 `internal/runner/approval_decision.go`），只解析一次命令链（绝不把命令段拼回字符串二次解析），对含转义/引号拼接/`$(`/反引号/变量展开的模糊语法一律 fail-closed（`ask`），按完整 token 匹配只读安全白名单并拦截高危参数（`find -exec/-delete/-fprint*`、`fd -x`、`rg --pre`、`git diff|log|show --output`、`go -exec/-toolexec/-o`、`go env -w` 等），同时对去混淆文本检测破坏性指令（含 git 参数缩写 `--forc`/`--har`、组合短参数 `-uf`、`+refspec` 强推）。再用 `.opencode/lib/permission-policy.cases.json`（命令级）与 `.opencode/lib/permission-request.cases.json`（请求级）做 JS 和 Go 的一致性测试。所有审批决策统一由 Go 的 `approvalAutoApproves` 根据 `~/.hoxi/approval-policy.json` 判定：破坏性命令与敏感凭据读取在所有模式下都必须询问用户。
  - ask：生成 `PermissionRequest{id:"per_agy_<seq>", sessionID, permission:"bash", patterns:[CommandLine], metadata:{command, cwd, source:"antigravity"}, always:[命令前缀], tool:{messageID, callID}}`，其中 `callID` 通过 `stepIdx` 映射到 translate.go 为该 step 生成的 tool part。然后推送 `permission.asked`，在 channel 上等待。
  - 会话内的 always 规则只存在 Hoxi 自己的会话记录里，**不写** agy 的 `settings.json`，避免污染用户的全局权限。
  - Stop、进程退出或会话删除时，所有挂起的请求统一以 deny 结束，并推送 `permission.replied`。
- `http.go`：新增 `POST /api/hoxi/runner/sessions/{id}/permission/{requestID}/reply`，body 为 `{reply:"once"|"always"|"reject", message?}`。`message` 作为 deny 的 reason 回给模型，用户可以顺便说明原因或给出替代方案。
- 状态栏：调用 `status.SetPendingApproval(requestID, sessionID, true/false)`。
- `translate.go`：
  - 工具报错 `tool call denied by pre-tool hook: …` → ToolCard 显示 `error`，标注"已被拒绝"。
  - `… timed out and was killed` → `error`，标注"审批超时"。
  - 工具分类（`hookToolAction`，仅使用精确名称白名单，禁止按 `read_*` / `list_*` 等前缀自动放行）：
    - 只读类（`read_file` / `view_file` / `list_dir` / `find_by_name` / `grep_search` / `list_resources` / 浏览器读取等）→ 携带路径参数的读/列/搜工具当路径落在会话 worktree 内（含符号链接与悬空符号链接校验）且非 `.env` 等敏感凭据文件时直接 `auto_approve`；`.env` 等敏感文件在所有模式下推送 `permission:"tool"` 审批卡片；路径在 worktree 外或缺失时进入门控并按 `~/.hoxi/approval-policy.json` 策略决定。
    - 写/改类（`write_to_file` / `write_file` / `replace_file_content` / `multi_replace_file_content` / `sed_file` / `notebook_edit`）→ 相对路径按 worktree 解析；落在 worktree 内外的常规路径按 `~/.hoxi/approval-policy.json` 策略决定（`auto` / `smart` 放行，`manual` 询问）；命中控制执行的敏感路径（`.git/**`、`.agents/**`、`.opencode/**`、`.claude/**`、`.hoxi/**`、`.vscode/settings.json`、`.envrc`、`.ssh/**` 等）或未携带可识别路径参数时在所有模式下推送 `permission:"tool"` 审批卡片。
    - 网络外发与高风险工具（`read_url_content` / `search_web` / `call_mcp_tool` / `mcp` / `execute_browser_javascript` / `execute_url` / `unsandboxed` / `run_workflow`）→ 推送 `permission:"tool"` 审批卡片；`always` 只记住完全相同的工具 + 参数，不写 agy 全局配置。
    - 控制类工具（`finish` / `task_boundary` / `ask_question`）→ 返回 `allow`，不阻塞轮次结束。
    - 未知工具 → 推送 `permission:"tool"` 审批卡片。无头模式会把需要权限的未知工具直接 soft-deny 并结束轮次，交给 Hoxi 审批才能让用户有机会放行。
  - 命令归一化：`permission_policy.go` 在单次解析后的命令段上校验并去掉位于会话 worktree 内的无引号简单 `cd <dir>` 首段（不重新拼接字符串解析），并把受信目录下的绝对工具链路径（如 `/usr/local/go/bin/go`、`/opt/homebrew/bin/npm`）按 basename 归一，避免 agent 常见写法被误判为 `ask`。
  - 原有的 `denied_actions` 提示保留作兜底（hook 没装上，或其它未覆盖工具需要权限时），文案改成引导用户检查 hook 安装状态。

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
| 前端 | `services/runner-api.ts` / `services/api.ts`（`per_rnr_` 前缀的请求路由到 Runner，经统一网关 `/api/hoxi/permission/{id}/reply` 回复）、`ApprovalGateCard.tsx`（拒绝说明随回复带回，Runner 请求不显示 OpenCode 规则选择器）、`components/models/AgentPolicyMcpTab.tsx`（显示 Antigravity 审批 Hook 安装状态并提供安装/卸载按钮） |

与原方案的差异：

- 审批决策统一由 Go 端 `approvalAutoApproves`（`internal/runner/approval_decision.go`）按 `~/.hoxi/approval-policy.json`（5 种模式：`auto` / `trusted` / `smart` / `manual` / `unattended`，及工具覆盖与可选轻量模型审查 `aiReview`）判定：策略允许的请求由 Go 直接 `auto_approve`，不推送 `permission.asked` 事件、不计入状态栏 Attention；策略不允许的请求（含任何模式下的破坏性命令、敏感凭据读取及受保护路径修改）推送 `permission.asked` 等待用户确认。前端只负责展示与编辑策略，不再替用户自动回复。
- 会话内的 always 规则只保存在内存里，按完全相同的命令匹配，Hoxi 重启后失效。破坏性命令永远不会被 always 记住。

端到端测试（需要真实的 agy，会消耗少量模型请求）：

```bash
go build -o /tmp/hoxi-e2e ./cmd/hoxi && HOXI_E2E_BIN=/tmp/hoxi-e2e go test -tags agy_e2e -run AgyHookE2E -v ./internal/runner/
```

2026-10-19 实测通过：点"允许一次"后 `rm` 被执行；点"拒绝"后命令被拦截，模型收到拒绝原因 `e2e says no`。

## 4. 未验证 / 风险

- `timeout` 只实测到 120s，600s 是否生效需要再验一次；文档没有给出上限。
- 全局 `~/.gemini/config/hooks.json` 同样会被 Antigravity IDE / App 读取（来自文档，未实测）。由于有环境变量守卫，IDE 里的行为是透传 allow，但每条命令会多一次进程启动（毫秒级）。
- 插件形式（`agy plugin install`）的 hooks 未实测，可以作为替代方案：生命周期更清晰，但同样是全局生效。
- hook 用 `"matcher": "*"` 覆盖全部工具（2026-10-21 起）。在 `accept-edits` 下工作区内的文件编辑本来就自动放行；工作区外的写入由 `--sandbox` 拦截。未识别的工具仍走 `denied_actions` 兜底提示。
- 2026-10-21 复现：agy 1.4.3 用 `read_file`（Step_Generic，权限名 `ReadFile`）读取工作区外文件时，旧的两个 matcher 匹配不到，无头模式直接软拒；改为通配 matcher + 只读分类后覆盖。
