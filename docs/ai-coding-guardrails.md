# AI 编码红线（Guardrails）

本文汇总 AI 在本仓库**真实犯过**的错误（来源：2026-10-21 提交审查），把每类错误写成可执行的规则与自检步骤。AI Agent 在修改对应子系统前必须读本文；提交前逐条自检。规则与 [`AGENTS.md`](../AGENTS.md) 冲突时以更严格者为准。

> 核心原则：**静态检查和测试全绿 ≠ 安全**。当天全部 lint / 测试通过，仍漏出 3 个可利用的安全问题和 1 个会改写无关仓库的数据 bug。新增能力时必须为"攻击者 / 异常输入"写测试，而不只为正常路径写测试。

---

## 1. 审批门控（PreToolUse Hook / `approval_decision.go`）

**事故**：`normalizeCommandForPolicy` 先剥离 `cd <tok>`（按空白切分、不识别引号），再用 `" && "` 拼回字符串重新解析。`cd .' ls' ; r\m -rf .` 被判为 `allow`。

规则：
- **只解析一次**。禁止把解析结果重新序列化成 Shell 字符串再解析；拆好的命令段逐段判定。
- **失败即 ask（fail closed）**。无法确定语义的输入（词内反斜杠、引号、`$'…'`、反引号、`$(`、未闭合引号）一律不得走安全白名单。
- **白名单按 token 锚定**，禁止用 `\b` 作命令名边界（`npx tsc-x`、`ls.x` 会命中）。
- 安全命令的危险参数必须显式拦截：`find -exec/-ok/-delete/-fprint*/-fls`、`fd -x/-X/--exec`、`rg --pre`、`git diff|log|show --output`、`go env -w`、`go * -exec/-toolexec/-vettool/-o`。
- 破坏性判定覆盖变体：`+refspec` 强推、组合短参数（`-uf`）、长参数缩写（`--forc`、`--har`）。
- **未知工具一律 ask**。禁止按工具名前缀（`read_*`、`list_*`…）自动放行；网络类工具（`search_web`、`read_url_content`）视为外发，必须走策略判定。
- worktree 内的写操作仍需对**能控制执行的路径**询问：`.git/**`、`.agents/**`、`.opencode/**`、`.claude/**`、`.hoxi/**`、`.envrc`、`.vscode/settings.json`；worktree 内外读取 `.env`（`.env.example` 除外）等敏感凭据文件在任何模式下都必须询问。Agent 不得能自行关掉门控。
- **OpenCode 的 `permission` 必须保持 `ask`，对象规则必须有 `"*": "ask"` 兜底**（`provider.normalizeApprovalPermissions`），否则 OpenCode 会在内部直接放行并绕过 Hoxi。
- **Go 与 JS 两套策略必须对齐**：任何规则改动同时修改 `internal/runner/permission_policy.go` / `approval_decision.go` 与 `.opencode/lib/permission-policy.js`，并把用例写进共享的两个 fixture：`.opencode/lib/permission-policy.cases.json`（命令级）和 `.opencode/lib/permission-request.cases.json`（请求级）。只改一边视为未完成。
- **破坏性判定不扫描数据参数**：提交说明（`git commit -m`）、搜索词（`rg`/`grep` 的模式）、`echo`/`printf` 文本先经 `maskCommandDataArguments` 替换再匹配，避免 `-m "restore …"`、`rg "Shutdown"` 误报。只替换整词字面量；含 `$`/反引号/`$(`/heredoc/子 Shell 的命令、链中出现 `sh`/`bash`/`eval`/`xargs`/`sudo` 等执行器、或 `echo` 文本被管道或文件喂给 `awk`/`sed`/`python`/`node` 时一律不替换。重定向目标永远不算数据。
- **完全信任模式（`trusted`）只放行可恢复的破坏性操作**（实现见 `internal/runner/approval_trusted.go`）：隔离 worktree 内的路径删除与 `git reset --hard`/`checkout --`/`restore`/`clean`，以及临时目录下的具体条目。新增放行形式前必须同时写允许用例和“越界/伪装”拒绝用例（`approval_trusted_test.go`），硬底线清单见 AGENTS.md 第 6 条，不得因“用户已选择完全信任”而放宽。
- 每发现一个绕过，必须作为回归用例写进对应 `*.cases.json`。

自检：对你新增的每条"allow"路径，尝试用引号、反斜杠、`;`/`&&`/`|` 链接、`$()` 构造绕过，并写成用例。

## 2. GoPanel 隧道白名单

**事故**：白名单按**前缀**放行 `/api/hoxi/worktree`、`/api/hoxi/file`，导致手机端可经 `verify-run` 执行任意 `sh -c`、经 `file/open` 启动任意程序、经 `file/content` 读取 `~/.claude/.credentials.json` 等凭据；`/file` 原始 OpenCode 透传还被注入网关令牌。

规则：
- 白名单必须是**显式的"方法 + 路由"清单**，禁止前缀匹配。领域包新增的路由默认**不可**经隧道访问。
- 以下能力永不经隧道开放：执行命令或写执行配置（`verify-run`、`verify-config`、Hook 安装）、`open`/启动程序、工作区外文件读写与列目录、账号 / 提供商 / 凭据 / 更新器 / 浏览器、OpenCode 原始透传。
- 经隧道的文件读取必须限制在已注册工作区与会话 worktree 内。
- 开放任何新路由必须同时：更新 [`docs/gopanel-hoxi-remote-tunnel.md`](gopanel-hoxi-remote-tunnel.md)、在 `TestTunnelRouteAllowlist` 增加允许用例**和**相邻的拒绝用例。
- 牢记信任边界：手机侧确认卡片不能替代桌面审批门控；任何经隧道可达的接口都不得绕过 PreToolUse 审批。

## 3. 状态与身份迁移（工作区 / 项目 / 会话）

**事故**：`changedFolderPaths` 按数组下标对比新旧文件夹，而 `validateWorkspaceFolders` 会静默丢弃不存在的路径并重排主文件夹。删除中间文件夹后仅改名保存，就把该文件夹的会话迁到无关的兄弟目录，并在那个 git 仓库里新建 worktree 与分支。

规则：
- **禁止用启发式推断破坏性操作**（迁移、合并、删除）。按下标、按 `os.Stat` 是否存在去猜"移动"都不允许；只有客户端**显式**提交（如 `folderID → newPath`）才执行。
- 校验函数**不得静默丢弃输入**再交给按位置对比的逻辑。要丢就返回错误或显式标记。
- 迁移前确认目标归属：新路径不得已属于其他文件夹 / 工作区；会话正在运行时拒绝迁移或先持有会话锁。
- 外部副作用（`git worktree add`、建分支）必须检查返回的实际状态（如 `Isolated`），失败时回滚内存绑定。
- 身份 ID 一旦被会话引用，删除 / 重建实体时必须可恢复（墓碑或按规范路径复用），不得让历史数据永久不可见。
- 每个此类函数必须有"删除中间项 / 删除主项 / 重排 / 显式替换"四类测试。

## 4. 持久化

**事故**：`workspaces.json` 成为会话项目归属的唯一来源后仍用 `os.WriteFile` 直写、错误被 `_ =` 吞掉；解析失败时重新播种并覆盖原文件。

规则：
- `~/.hoxi/`、`.hoxi/` 下所有状态文件：加锁 → 写同目录临时文件 → fsync → `os.Rename`。禁止 `_ = os.WriteFile(...)`。
- 解析失败时**先备份损坏文件**，不得用新数据覆盖。
- 禁止在持有全局锁时做磁盘 I/O / fsync；锁内取快照，锁外写入，并用序号丢弃过期写入。

## 5. 进程控制

**事故**：单条命令的"停止"在会话没有 runner 进程时，会扫描共享 `opencode serve` 守护进程的所有子进程；匹配不到时**回退为杀掉全部候选**，可能杀掉其他会话、终端与 MCP 服务。

规则：
- 杀进程的范围必须严格限定在**本会话自己的进程树**。找不到目标就什么也不做并返回未找到。
- **禁止"匹配不到就扩大范围"的回退**，尤其是破坏性操作（kill、删除、reset）。
- 共享资源（OpenCode 守护进程、PTY、MCP）不属于任何单个会话，不得被会话级操作波及。
- 有并发入口的安装 / 启动流程（后台自举与用户点击安装）必须走同一个锁或 singleflight；下载写唯一临时文件，校验后再原子替换。
- 校验（SHA256）放在每个下载源的循环内，失败就尝试下一个源；校验失败绝不能落到"未校验安装"。

## 6. 网络外发

**事故**：commit message、会话标题等后台 LLM 调用在没有合适模型时，静默回退到用户未选择的提供商（包括未连接的托管模型），把 Diff 和首条提示词发出去。

规则：
- 后台 LLM 调用只能使用用户为该角色**明确配置**的模型；没有就走本地启发式或跳过，禁止自动挑一个"最佳"提供商。
- 新增任何外发前，在 UI 中说明目的地与内容范围；推送载荷只含数量与标识。

## 7. 前端分层与异步

**事故**：组件里直接调用 `window.runtime.EventsOn/EventsOff`（且 `EventsOff(name)` 会清掉所有监听器）；组件自己做拉取、轮询和世代号；`ModelRolePicker` 复制并改歪了 store 的排序逻辑；`<For>` 每次重算都重建节点对象，导致展开状态被重置；跨项目深链一直挂起，之后突然触发。

规则：
- Wails / 浏览器平台 API、HTTP、SSE 只能出现在 `services/`；组件只调用 store。订阅类 API 必须返回并使用 unsubscribe 函数。
- 数据加载、合并、轮询、世代号属于 store；组件只渲染。
- **禁止复制已有的 store 逻辑**到组件里"改一改再用"。需要变体就把 store 函数参数化。
- 列表行的局部 UI 状态（展开等）必须按稳定 ID 存储，不能依附于每次重算都会新建的对象。
- 任何"挂起等待"（pending deep link、pending action）都必须有过期条件（切换项目 / 超时），不得在无关时刻突然执行。
- 所有异步写入 store 的地方都要有防过期响应校验，包括弹窗（目录变化 / 关闭重开）。
- 只有一个地方可以写派生的全局状态（如 `activeProjectID`）；SSE 事件不得顺手改它。

## 8. 测试纪律

- 新增 Go 代码必须通过 `go test -race`。测试不得在 `t.Cleanup` 中修改仍被后台 goroutine 读取的全局变量；先等 goroutine 退出。
- 复现 bug 的测试写好就转为正式回归测试，禁止以 `zz_scratch_*` 形式留在工作区让主干变红。
- 安全相关改动必须包含**拒绝路径**的测试（被拦截的命令、被拒绝的路由、被拒绝的路径），而不只是正常路径。

---

## 提交前自检清单

- [ ] 我新增的每个 allow / 放行 / 白名单项，是否有对应的绕过尝试用例与拒绝用例？
- [ ] 是否有任何"推断"或"回退到更大范围"的逻辑执行了破坏性操作？
- [ ] 新路由是否会被隧道前缀自动暴露？是否更新了契约文档与测试？
- [ ] 状态文件写入是否原子、错误是否处理、解析失败是否备份？
- [ ] Go / JS 审批策略是否同步、`cases.json` 是否更新？
- [ ] 后台外发的目的地是否是用户明确选择的？
- [ ] 前端是否有平台 API / 网络调用 / 数据加载出现在组件里？是否复制了 store 逻辑？
- [ ] `go test -race`、`npm --prefix web run lint && npm --prefix web test && npm --prefix web run build` 是否全绿？
