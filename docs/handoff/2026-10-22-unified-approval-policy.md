# 交接：审批决策统一收口到 Go（2026-10-22）

> 状态：**已完成（Go 后端、前端与文档均已完成并通过测试）**。接手前请完整阅读本文、[`AGENTS.md`](../../AGENTS.md) 和 [`docs/ai-coding-guardrails.md`](../ai-coding-guardrails.md) 第 1 节。

## 1. 要解决的问题

安全确认（审批）逻辑原先分散在 4 个地方，各自独立判定，规则互相打架：

| # | 位置 | 问题 |
|---|---|---|
| ① | OpenCode 自身的 `permission` 配置（`~/.hoxi/models.json`） | 只决定“问不问 Hoxi” |
| ② | `.opencode/plugins/hoxi-permissions.js`（`permission.ask` 钩子） | **已失效**：OpenCode 1.19.3 二进制里 `permission.ask` 钩子从未被触发（只出现在帮助文档文字里），插件是死代码 |
| ③ | Go 门控 `hook_gate.go` / `claude_hook_gate.go` | 不知道审批模式；manual 模式下仍放行白名单命令和工作树内编辑；Antigravity 写工作树外硬拒绝，Claude 却是询问 |
| ④ | 前端 `web/src/store/approval-mode.ts` 的 `shouldAutoApprovePermission` | 审批模式只存在 localStorage。“auto 模式”实际是前端收到事件后再替用户点批准 |

由此产生的问题：

- **没开窗口时 auto 不生效**：`--no-desktop` 无头模式、只用 CLI、只用手机时，会话卡在审批上。
- **幽灵推送**：每个本该自动批准的请求，都会先让状态栏 Attention 加 1，并经隧道触发一次 APNs 推送，然后才被前端批掉。
- **CLI 和桌面端抢答**同一个请求。
- **`.env` 读取被静默放行（安全漏洞）**：OpenCode 默认对 `*.env` 读取要询问，但前端“读类权限一律批准”，manual 模式也一样。而在 bash 里 `cat .env` 却被判为破坏性。
- **单条规则会扩大授权范围（安全漏洞）**：审批卡“总是允许 + 规则”会写入 `permission.bash = {"git *": "allow"}`，没有 `"*"` 兜底。OpenCode 内置默认是 `"*": "allow"`，所以未匹配的命令（包括 `rm -rf`）从此在 OpenCode 层直接放行，Hoxi 根本看不到。
- **对象规则让 auto 模式失效**：前端把“未匹配的对象规则”当成 ask。用户加一条 allow 规则后，其它所有命令都要人工确认。

> 更正上一轮分析：我曾说“切换模式后 OpenCode 规则要重启才生效”。这不完全对。`persistHoxiConfigDoc` 每次写配置都会调用 `reloadOpenCodeGlobalConfig()`，在空闲时对 OpenCode 执行 dispose，所以改动会在空闲后生效。新设计下 OpenCode 配置固定为 `ask`，切换模式只改 Go 内存中的策略，立即生效，这个问题不再存在。

## 2. 新设计

**唯一决策点：`internal/runner/approval_decision.go` 中的 `approvalAutoApproves(item, policy, rules, worktree)`。** 所有运行时、所有入口都走它；前端只负责展示和编辑策略。

```
OpenCode 配置 edit/webfetch/websearch/codesearch/bash 一律 "ask"（对象规则补 "*":"ask"）
        │ permission.asked
        ▼
runner ──► approvalAutoApproves ──允许──► Go 直接回复 OpenCode "once"（来源 policy，记审计）
  │                              └不允许─► 广播 permission.asked → 状态栏/推送/桌面卡片/CLI/手机
Claude/Antigravity PreToolUse ──► 同一函数 ──允许──► 直接放行（不发事件，不计 Attention）
                                           └不允许─► awaitReply 等用户
```

- **策略持久化**：`~/.hoxi/approval-policy.json`，结构为 `{mode, edit, webfetch, bash, updatedAt}`，原子写入，权限 0600，并已加入 `hoxipaths` 受保护状态文件列表。
  - 非法值返回 400，不会静默丢弃。
  - 文件损坏时先备份为 `*.corrupt-*`，退回默认值 `auto`，等下次显式保存再覆盖。
- **HTTP 接口**：`GET` / `PUT /api/hoxi/permission/policy`，返回 `{policy, exists}`。
  - 隧道来源返回 403；隧道白名单也不包含此路由（已加拒绝用例）。
  - PUT 成功后会做两件事：
    1. 回调 `provider.EnforceAskPermissions`，把 OpenCode 配置改成 ask；
    2. 执行 `reconcilePendingApprovals`，把按新策略可以放行的等待中请求直接放行。
- **对象规则（审批卡“总是允许”写入的 pattern）**：只有显式匹配才算数——任意位置出现 `deny`（含 `"*":"deny"`）或命中某个具体 pattern。未命中时交给审批模式决定。规则对所有运行时生效。字符串 `"ask"` / `"allow"` 与对象规则里的 `"*":"ask"` 是 Hoxi 自己写入的强制兜底值，不算用户选择；字符串 `"deny"` 仍然生效。
- **破坏性判定（所有模式都必须问用户）**：`isDestructivePermissionRequest` 是 JS 版的 Go 移植。两边共用 fixture `.opencode/lib/permission-request.cases.json`（31 条，Go 和 JS 已逐条比对一致）。审计里原先的第三套判定 `isDestructivePermissionItem` 已删除，改用此函数。
- **新增敏感读取判定**：`isSecretFilePath`（Go）/ `isSecretPath`（JS）。覆盖 `.env` 系列（`.env.example` 除外）、凭据 dotfile、`.ssh` / `.aws` 等目录。`read` 权限，以及 Claude / Antigravity 在工作树内读取这类文件时，任何模式都要询问。

### 模式语义（以 Go 测试 `TestApprovalModesDecideEveryCategory` 为准）

| 请求 | auto | smart | manual |
|---|---|---|---|
| 普通读取 | 放行 | 放行 | 放行 |
| `.env` 等敏感读取 / 破坏性命令 / 凭据路径 | 问 | 问 | 问 |
| 编辑（含工作树外的常规路径） | 放行 | 放行 | 问 |
| 没有路径的编辑 | 问 | 问 | 问 |
| 只读白名单命令 | 放行 | 放行 | **问**（行为变化） |
| 其它非破坏性命令 | 放行 | 问 | 问 |
| webfetch 类 | 放行 | 放行 | 问 |
| 未知工具 / MCP | 放行 | 问 | 问 |

`edit` / `webfetch` / `bash` 的单项覆盖（`allow` / `ask`，bash 另有 `smart`）会替换该模式下的预设。

## 3. 已完成（均已通过 `go vet` 和 `go test`）

`go test ./internal/runner/ ./internal/provider/ ./internal/gateway/ ./internal/hoxipaths/` 全部通过。**`go test -race` 尚未跑完**（runner 包加 `-race` 编译很慢，建议放到后台跑）。

| 文件 | 改动 |
|---|---|
| `internal/runner/approval_decision.go`（新） | 纯决策逻辑：模式 / 覆盖 / 对象规则 / 工具分类 / 破坏性判定 / `commandCandidates`（含解包 sandbox-exec 包装） |
| `internal/runner/approval_policy.go`（新） | 策略存储与 HTTP 处理、`SetPermissionRulesSource`、`SetApprovalPolicyHook` |
| `internal/runner/approval_auto.go`（新） | `policyApproves`、`policyApprovesWorktreeEdit`、审计记录（来源 `policy`）、OpenCode 自动回复（`openCodeAutoReplies` 去重；失败后不重试，退回为普通待处理请求）、`claimOpenCodeSSELine`、`reconcilePendingApprovals` |
| `internal/runner/hook_gate.go` | 新增 `/policy` 子路由；`gateCommand` 改走策略（只读命令放行时不记审计）；Antigravity 的读取 / 编辑分支改走策略（工作树外编辑从硬拒绝改为进入门控）；`listUnifiedPermissions` 过滤掉策略已放行的请求 |
| `internal/runner/agy_hook_tools.go` | `gateToolCall` 在 `awaitReply` 之前先问策略 |
| `internal/runner/claude_hook_gate.go` | 工作树内编辑按策略决定（manual 模式会询问）；工作树内读取敏感文件时询问 |
| `internal/runner/opencode_turn.go` | 受管会话收到 `permission.asked` 时先由策略认领；回复失败时通过 `publishOpenCodeEvent` 再发出事件 |
| `internal/runner/sse.go` | 非受管会话的原始 OpenCode 流同样认领（`relayOpenCodeFrames` 新增 `directory` 参数） |
| `internal/runner/external_path_risk.go` | 新增 `isSecretFilePath`；`externalDirectoryTargets` 补充读取 `metadata.files` |
| `internal/runner/approval_audit.go` | 删除 `isDestructivePermissionItem` |
| `internal/runner/http.go` | Service 新增字段 `policy`、`permissionRules`、`onPolicySaved`、`openCodeAutoReplies` |
| `internal/provider/approval_permissions.go`（新） | `normalizeApprovalPermissions`、`PermissionRules`、`EnforceAskPermissions` |
| `internal/provider/config.go` | 在 `EnsureHoxiModelsConfig`（守护进程启动）和 `persistHoxiConfigDoc`（每次写入）中执行规范化 |
| `internal/gateway/gateway.go` | 把 provider 的两个函数注入 runner |
| `internal/hoxipaths/hoxipaths.go` | 新增 `ApprovalPolicyPath()`，并把它加入受保护文件列表 |
| `.opencode/lib/permission-policy.js` | 修复 `.env` 读取；更新文件头注释，说明决策已移到 Go |
| `.opencode/lib/permission-request.cases.json`（新） | Go 与 JS 共用的请求级 fixture |
| 测试 | 新增 `approval_decision_test.go`、`approval_policy_test.go`（持久化、隧道 403、损坏文件备份、auto 模式不发事件也不计 Attention、manual 模式、敏感读取、改模式后放行等待中的请求、OpenCode 三条路径的自动回复）、`provider/approval_permissions_test.go`；`tunnel_test.go` 加入 policy 路由的拒绝用例；更新 `hook_gate_test.go`、`claude_hook_gate_test.go`、`approval_audit_test.go`（门控测试的 fixture 固定为 smart 模式） |
| 前端（已开头） | `sse-types.ts` 新增 `ApprovalPolicy` / `ApprovalPolicyState` 类型，审计来源增加 `"policy"`；`api.ts` 新增 `fetchApprovalPolicy` / `updateApprovalPolicy`；`ApprovalAuditGrantsSection.tsx` 和中英文 `approval.ts` 新增 `sourcePolicy` 文案 |

**有意的行为变化（提交说明里要写明）**

1. manual 模式下，`ls`、`git status` 这类只读命令也会询问。
2. Antigravity 写工作树外的文件：从“硬拒绝”改为“按模式决定”。auto / smart 模式下常规路径放行，敏感路径询问；与 OpenCode 的 `external_directory` 一致。
3. smart 模式下，Claude / Antigravity 的 webfetch 类工具和常规的工作树外读取由 Go 直接放行。最终结果与之前前端替用户点批准一致，只是不再弹卡片、不再推送。
4. 审计里会出现来源为 `policy` 的条目。只读白名单命令不记审计。

## 4. 剩余工作（按顺序做）

### A. 重写 `web/src/store/approval-mode.ts`（核心）

**删除**：

- 工具名集合：`READ_TOOL_NAMES` / `EDIT_TOOL_NAMES` / `WEBFETCH_TOOL_NAMES` / `BASH_TOOL_NAMES` / `SMART_AUTO_PERMISSIONS`
- 分类与规则函数：`classifyToolToken`、`resolvePermissionCategory`、`isRuleObject` 之外的规则求值（`evaluatePermissionRuleObject`、`matchesRulePattern`、`globPatternToRegExp`、`extractCategoryTargets`、`parseMetadataArgs`、`hasConfiguredPermissionEntry`）
- 自动批准相关：`shouldAutoApprovePermission`、`inFlightAutoApprovals`、`syncApprovalModeToOpenCodeConfig`、`reevaluatePendingPermissions`

**保留**：

- `markPermissionResolved`、`isPermissionGoneError`、`addPendingPermissionToStore`
- `syncPendingPermissions` 及其依赖：`permissionSyncScopes`、`resolvePermissionDirectory`、`isDirectoryInScopes`
- `replyPermissionRequest`：rules 仍然写入 `updateGlobalPermissions`，后端会自动补 `"*":"ask"`
- 授权与审计：grants / audit 相关函数
- Antigravity 钩子：agy hook 的三个函数
- 导出：`isDestructivePermissionRequest`（`ApprovalGateCard` 在用）和 `isRunnerPermissionID`

**新增或改写**：

```ts
const [approvalPolicy, setApprovalPolicySignal] = createSignal<ApprovalPolicy>({ mode: "auto" });
export { approvalPolicy };

function applyApprovalPolicy(p: ApprovalPolicy) {
  setApprovalPolicySignal(p);
  updateAppSetting("approvalMode", p.mode);   // appSettings.approvalMode 只作为 UI 缓存
  if (appSettings.bashApprovalRule) updateAppSetting("bashApprovalRule", "");
}

// 一次性迁移：Go 策略文件不存在时，用旧的 localStorage 设置和 OpenCode 配置字符串生成策略
function legacyApprovalPolicy(): ApprovalPolicy {
  const mode = appSettings.approvalMode || "auto";
  const perm = workspace.globalConfig?.permission ?? {};
  const preset = mode === "manual" ? "ask" : "allow";
  const pick = (v: unknown) => ((v === "allow" || v === "ask") && v !== preset ? v : "");
  const presetBash = mode === "manual" ? "ask" : mode === "smart" ? "smart" : "allow";
  const bash = appSettings.bashApprovalRule && appSettings.bashApprovalRule !== presetBash ? appSettings.bashApprovalRule : "";
  return { mode, edit: pick(perm.edit), webfetch: pick(perm.webfetch), bash };
}

export async function loadApprovalPolicy(): Promise<void> {
  let state = await hoxiAPI.fetchApprovalPolicy();
  if (!state.exists) {
    try { await fetchGlobalConfig(); } catch { /* use what is cached */ }
    state = await hoxiAPI.updateApprovalPolicy(legacyApprovalPolicy());
  }
  applyApprovalPolicy(state.policy);
}

export async function setApprovalMode(mode: ApprovalMode): Promise<void> {
  // 切换模式会清空单项覆盖（与旧行为一致）
  applyApprovalPolicy((await hoxiAPI.updateApprovalPolicy({ mode })).policy);
}

export async function updateToolPermissionRule(tool: "edit" | "webfetch" | "bash", value: "allow" | "smart" | "ask") {
  const cur = approvalPolicy();
  const v = tool === "bash" ? value : value === "ask" ? "ask" : "allow";
  const preset = presetRule(cur.mode, tool); // manual→ask；smart 下 bash→smart；其余→allow
  applyApprovalPolicy((await hoxiAPI.updateApprovalPolicy({ ...cur, [tool]: v === preset ? "" : v })).policy);
}

export function handleIncomingPermissionRequest(req: PermissionRequest): void {
  if (!req?.id || resolvedPermissionIDs.has(req.id)) return;
  addPendingPermissionToStore(req); // 只负责停靠展示，绝不自动回复
}
```

`resolveEffectivePermissionSummary(mode, permissionConfig, policy = approvalPolicy())` 只用于展示：

- 用 policy 的覆盖值，或模式预设。只有 `policy.mode === mode` 时才套用覆盖。
- 配置里的字符串 `"deny"` 显示为 `"ask"`。
- `hasCustomToolOverrides`：有覆盖，或 edit / webfetch 是对象规则或 deny 时为真。

文件不能超过 800 行（`npm --prefix web run lint` 会检查）。重写后应明显变短。

### B. 组件与启动流程

- `web/src/App.tsx`
  - 启动时把 `syncApprovalModeToOpenCodeConfig(...)` 换成 `void loadApprovalPolicy().catch((e) => console.warn(...))`。
  - 把 import 列表里的 `syncApprovalModeToOpenCodeConfig` 换成 `loadApprovalPolicy`。
  - 第 317 行 `handleIncomingPermissionRequest(evt.properties)` 不用改。
- `setApprovalMode` 现在返回 Promise：
  - `components/timeline/ApprovalModePill.tsx:106`、`components/approval/ApprovalGateCard.tsx:424` 改为 `void setApprovalMode(m).catch(...)`。
  - `components/models/AgentPolicyMcpTab.tsx:92` 改为 `await` 并在 try 中显示保存成功或失败。
  - 上述组件仍可读取 `appSettings.approvalMode`，它已经同步。
- `AgentPolicyMcpTab.tsx:81`：`resolveEffectivePermissionSummary` 需要读取 `approvalPolicy()`，否则覆盖值变化后摘要不会刷新。
- `web/src/store/settings.ts`：`bashApprovalRule` 只保留用于迁移，在注释里写明“legacy，已迁移到 Go 策略”。注意：这个文件里还有别人的未提交改动，只改这一处注释。

### C. 删除失效插件

- `git rm .opencode/plugins/hoxi-permissions.js`
- `internal/runner/hook_gate_test.go:289` 只是把这个路径当作受保护路径的例子，可以保留。
- 在 OpenCode 1.19.3 下，`permission.ask` 钩子不会被触发。删除前可以再确认一次：

  ```bash
  strings "$(readlink -f "$(which opencode)")" | grep -c 'trigger("permission.ask"'
  ```

  结果应为 0。

### D. 前端测试 `web/src/__tests__/permissions.test.ts`

**删除**：

- 插件相关用例：第 1–2 行的 import，以及 “exports only the single OpenCode plugin entrypoint”。
- 所有 `shouldAutoApprovePermission`、`syncApprovalModeToOpenCodeConfig` 的断言。这部分语义已经搬到 Go 的 `TestApprovalModesDecideEveryCategory`、`TestApprovalOverridesReplaceTheModePreset`、`TestPermissionRuleObjectsOnlyDecideTheirOwnTargets`。

**保留**：

- `decidePermission` / `isDestructivePermissionRequest` 的断言。注意 `decidePermission` 要改成从 `../../../.opencode/lib/permission-policy.js` 导入。

**新增**：

1. 遍历 `permission-request.cases.json`，断言 `isDestructivePermissionRequest` 结果一致（与 Go 侧对应）。
2. `handleIncomingPermissionRequest` 只停靠、不调用 `replyPermission`（mock 掉 `hoxiAPI.replyPermission`，断言调用 0 次）。
3. `loadApprovalPolicy`：`exists:false` 时按旧设置 PUT；例如 `manual` 加 `edit:"allow"` 应得到 `{mode:"manual", edit:"allow"}`。`exists:true` 时不应 PUT。
4. `setApprovalMode` 会 PUT `{mode}`；PUT 失败时 `appSettings.approvalMode` 保持不变（拒绝路径，红线要求）。
5. `updateToolPermissionRule`：值等于预设时写 `""`。

“auto-approve dock stability” 那组测试要改成 “dock stability”，断言 404 或已处理的请求不会停靠。

### E. 文档（必须与代码一致）

- `AGENTS.md`
  - 1.1 节“多运行时引擎”一行：补充“审批决策统一由 `internal/runner/approval_decision.go` 判定，策略存于 `~/.hoxi/approval-policy.json`”。
  - 1.3 节目录树：`.opencode/plugins/` 中删掉 `hoxi-permissions.js`；`.opencode/lib/` 补充 `permission-request.cases.json`。
  - 2.2 节第 6 条：把“必须在 `hoxi-permissions.js` 及多运行时 PreToolUse Hook 中返回 ask”改为“必须由 Go 审批策略（`approvalAutoApproves` / `isDestructivePermissionRequest`）在所有模式下交给用户”。
  - 2.2 节第 12 条：“Go 与 JS 策略必须同步修改并写入 `.opencode/lib/cases.json`”改为“两个 fixture：`permission-policy.cases.json`（命令级）和 `permission-request.cases.json`（请求级）”。
- `docs/ai-coding-guardrails.md` 第 1 节：同上；另补一条：“OpenCode 的 `permission` 必须保持 ask，对象规则必须有 `*` 兜底（`provider.normalizeApprovalPermissions`），否则 OpenCode 会绕过 Hoxi”。
- `DEVELOPMENT.md`、`docs/antigravity-approval-hook.md`、`docs/claude-code-approval-hook.md`：搜索 `hoxi-permissions` 和“工作树外编辑硬拒绝”的描述并改正。
- `docs/gopanel-hoxi-remote-tunnel.md`：在不开放的路由中注明 `/api/hoxi/permission/policy`。`TestTunnelRoutesMatchContractDoc` 会对比文档，改完要跑一下。
- 双仓契约：GoPanel 仓不受影响，推送载荷没变。

### F. 验证（全部要做，不能只跑单测）

```bash
go vet ./... && go test ./... && go build ./...
```

```bash
go test -race ./internal/runner/ ./internal/provider/ ./internal/gateway/
```

`-race` 很慢，放到后台跑，预计 5 分钟以上。

```bash
npm --prefix web run lint && npm --prefix web test && npm --prefix web run build
```

在真实应用里验证（`wails dev`，按用户要求必须亲自验证）：

1. 首次启动后 `~/.hoxi/approval-policy.json` 已生成，模式与之前界面里的一致；`~/.hoxi/models.json` 的 `permission` 里 edit / webfetch / bash 都是 `"ask"`。
2. auto 模式下让 OpenCode 会话跑 `npm install` 和一次文件编辑：不弹卡片，状态栏不出现 Attention，审批记录里有 `policy` 来源的条目。
3. 任意模式下让 Agent 读取 `.env`、执行 `rm -rf x`：必须弹卡片。
4. 切到 manual 模式后立刻让 Agent 改文件：必须弹卡片，且不需要重启。
5. 关闭窗口（`--no-desktop` 或隐藏）、用 CLI 运行一个会话：auto 模式下不应卡在审批上。
6. 在审批卡上点“总是允许 + 规则”后，检查 `models.json` 的对象规则里有 `"*":"ask"`，且其它命令仍按模式处理。

### G. 提交

- 工作区里还有**其他会话的未提交改动**，例如 usage-history、dev_pulse、model_limits、worktree、orchestration、SettingsModal 等。**只提交本任务相关的文件**（第 3 节列表 + 本文 + A–E 改到的文件），不要 `git add -A`。
- 提交信息末尾按 AGENTS.md 规则加上 `Co-authored-by: Hoxi <code@hoxi.ai>`。

## 5. 坑与注意事项

- **门控测试的 `stepIdx` 和 callID**：同一会话、同一 callID 的请求会被 `pendingCallKey` 合并成一个。测试里连发两个请求时要用不同的 `stepIdx`，否则第二个永远等不到 `permission.asked`，测试会挂到超时。
- **隧道的 reply 通配**：`POST /api/hoxi/permission/policy/reply` 会命中 `/api/hoxi/permission/*/reply` 白名单。这是无害的（相当于回复一个 id 为 "policy" 的请求，结果 404），不要把它加进拒绝列表。
- **OpenCode 不可用时不认领请求**：`claimOpenCodePermission` 在 `!s.openCode.Available()` 时直接返回 false，请求照常交给用户。
- **自动回复失败后不重试**：状态会记为 failed，请求作为普通待处理项交给用户，避免循环。`openCodeAutoReplies` 中的 failed 条目不会清理；数量很少，可以接受。
- **策略文件不存在时**：Go 默认 `auto`，与前端旧的默认值一致。从应用启动到前端完成迁移之间有几百毫秒的窗口，期间按 auto 处理。
- **迁移时机**：`normalizeApprovalPermissions` 只有在策略文件存在后，才会把 edit / webfetch 的 `"allow"` 改成 `"ask"`。这样前端迁移时还能读到旧的覆盖值。对象规则补 `"*":"ask"` 不受此限制，总是执行。
- **`provider.loadHoxiConfigDoc` 到 `persistHoxiConfigDoc` 之间没有加锁**：这是原有问题，本次没有处理。

## 6. 建议的后续（不在本次范围）

- Qoder / CodeBuddy 仍然没有 PreToolUse 门控（启动时有诊断警告）。
- “总是允许”在两边语义仍不同：OpenCode 写入的是全局持久的 pattern 规则；Claude / Antigravity 是会话内存里的授权，重启后丢失。可以考虑统一成 Go 侧的持久规则。
- 手机端数据外发说明等待办见 AGENTS.md 第三节。
