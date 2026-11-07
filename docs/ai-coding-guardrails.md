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
- **会执行 shell 命令的运行时工具一律走命令策略**（`gateCommand`）：Claude `Bash`/`Monitor`、Gemini `run_shell_command`、agy `run_command`。走通用工具门会把命令当成普通工具，auto/完全信任下 `sudo`、强推、读 `.env` 直接放行（`Monitor` 曾如此）。新运行时或新工具接入时先确认它是否执行命令。
- worktree 内的写操作仍需对**能控制执行的路径**询问：`.git/**`、`.agents/**`、`.opencode/**`、`.claude/**`、`.hoxi/**`、`.envrc`、`.vscode/settings.json`；worktree 内外读取 `.env`（`.env.example` 除外）等敏感凭据文件在任何模式下都必须询问。Agent 不得能自行关掉门控。
- **OpenCode 的 `permission` 必须保持 `ask`，对象规则必须有 `"*": "ask"` 兜底**（`provider.normalizeApprovalPermissions`），否则 OpenCode 会在内部直接放行并绕过 Hoxi。
- **Go 与 JS 两套策略必须对齐**：任何规则改动同时修改 `internal/runner/permission_policy.go` / `approval_decision.go` 与 `.opencode/lib/permission-policy.js`，并把用例写进共享的两个 fixture：`.opencode/lib/permission-policy.cases.json`（命令级）和 `.opencode/lib/permission-request.cases.json`（请求级）。只改一边视为未完成。
- **破坏性判定不扫描数据参数**：提交说明（`git commit -m`）、搜索词（`rg`/`grep` 的模式）、`echo`/`printf` 文本先经 `maskCommandDataArguments` 替换再匹配，避免 `-m "restore …"`、`rg "Shutdown"` 误报。只替换整词字面量；含 `$`/反引号/`$(`/heredoc/子 Shell 的命令、链中出现 `sh`/`bash`/`eval`/`xargs`/`sudo` 等执行器、或 `echo` 文本被管道或文件喂给 `awk`/`sed`/`python`/`node` 时一律不替换。重定向目标永远不算数据。
- **完全信任模式（`trusted`）只放行可恢复的破坏性操作**（实现见 `internal/runner/approval_trusted.go`）：隔离 worktree 内的路径删除与 `git reset --hard`/`checkout --`/`restore`/`clean`，以及临时目录下的具体条目（`/tmp`、`$TMPDIR`，以及 macOS `/var/folders/<id>/T/<子路径>`，`T` 目录本身不算）。判定前先按与不透明规则相同的方式展开字面量代入、字面量循环和不超过 2 层的子 shell；密钥代入直接拒绝。新增放行形式前必须同时写允许用例和“越界/伪装”拒绝用例（`approval_trusted_test.go`），硬底线清单见 AGENTS.md 第 6 条，不得因“用户已选择完全信任”而放宽。
- **不透明命令一律询问，放宽只有以下几类且都要有拒绝用例**。改写只用于判定，展开或代入后的文本必须再跑完整的危险判定和密钥判定；无法确定语义仍询问。实现在 `opaque_command.go`、`opaque_loops.go`、`opaque_constants.go`、`opaque_expand.go` 与 `.opencode/lib/opaque-command.js`、`shell-substitution.js`、`shell-constants.js`、`shell-loops.js`。
  - **常量代入**：同一条命令里，`NAME=值` 或 `export NAME=值` 且该段只有这一处赋值时，把字面值代入后再判定。值必须是字面量（单引号，或不含 `$`、反引号的双引号或裸词），或只由无害环境变量与字面量拼接，或下面的取值类 `$(…)`（代入占位符，按取值类看使用位置）。拒绝：再次赋值；`read`/`for`/`printf -v`/`declare`/`local`/`unset`/`mapfile` 重新绑定；变量当命令名（`$CMD args`）；`${x:-…}`/`${x/…}`/数组；值里有 glob、换行或 `$(` 以外的展开；赋值在管道、子 shell、`&&`/`||` 右侧之后，或先用后赋；`set -f`、`set +o noglob`、`set -euo pipefail`。`set -e`/`set -o pipefail`/`set -x` 无害。不重新绑定 `HOME`/`PATH` 等无害环境变量名。`D=~/.ssh; cat "$D/id_ed25519"`、`F=.env; cat "$F"`、`D=/repo; git push --force "$D"` 代入后仍命中密钥或硬底线。
  - **字面量 for 展开**：列表全是字面量（不含 `$`、glob、命令替换、密钥或受保护路径）且不超过 64 项时，按每个值展开成字面命令再照常判定，可与常量代入叠加。超过 64 项不展开、不截断（读循环保持原放行，写循环保持询问）。`while read`、glob 列表（`for f in *.go`）、密钥项（`for f in .env a.go`）保持原规则。展开后的写文件和破坏性命令不因来自循环而放行。
  - **取值类 `$(…)`**：内部命令本身可判定，且是 `git rev-parse`/`write-tree`/`hash-object`/`merge-base`/`symbolic-ref`/`describe`、`pwd`、`date`、`go env`、`which`、`command -v`、`ps -o …=`、`dirname`、`basename`、`wc -l` 时，结果可以出现在只读工具参数里，`mkdir` 也接受这类结果。`git hash-object -w` 会写对象库，按允许的写入处理，不算危险；`go env -w`/`-u` 不算取值类。`echo`/`printf`/`test` 和 `git commit -m` 仍接受任意 `$(…)`。拒绝：其他内部命令（`cat`、`curl`）、结果用在命令名位置（`$(git rev-parse HEAD) --help`）、结果交给 `git`/`find`/`rm`/`curl`/`cp`/`mv`。
  - **子 shell**：`(…)` 作为独立命令递归判定，`cd` 只作用于括号内；括号内不透明则整体不透明；嵌套超过 2 层保持不透明。`$( )`、`<( )`、`{ …; }` 不按这条展开。`(cd / && rm -rf tmp)`、`(cd .. && cat .env)`、`( ( ( ls ) ) )` 仍询问。完全信任下，括号里的可恢复删除与没有括号时一样（隔离 worktree 的 `(rm -rf build)` 可放行）。
  - **相对 `cd` 与绝对 `cd` 同等判定**：从 worktree 内 `cd ../sibling` 离开 worktree，与 `cd /…/sibling` 一样只接受安全的外部目录（非根目录、非密钥/凭据目录、非 `.git`/`.hoxi` 等受保护路径）。`cd ../../etc`、`cd ../.ssh`、`cd ../..` 到根、`cd ..` 进 `.hoxi/worktrees` 仍不透明；离开后的删除不算可恢复，`.env` 读取照样询问。用例带 `worktree` 字段。
  - **只读工具与 `$!`**：只读工具还包括 `lsof`、`ps`、`du`、`df`、`cut`、`tr`、`nl`、`column`、`shasum`、`sha256sum`、`md5`、`xxd`、`od`、`sleep`。`tail -f` 仍放行，挂起交给超时。无害环境变量增加 `$!`（上一个后台进程 PID）。`kill $!` 仍询问。后台 `cmd &` 本身不造成不透明，前提是 `cmd` 可判定。`sleep 1 & kill %1` 不因 `&` 不透明，但 `kill` 仍是破坏性命令。
  - **本会话记录**：读取发起请求的会话自己的 `~/.hoxi/sessions/<id>/`（`session.json`、`turns.jsonl`；同时匹配 Hoxi 会话 ID 和运行时 ExternalID）放行，包括 OpenCode `external_directory`。编辑和写入仍询问。其他会话、`~/.hoxi/*`、受保护状态文件、请求里没有会话 ID，继续询问。无人值守仍把全部 `external_directory` 当作工具类拒绝，只在密钥硬拒绝里豁免本会话读取。
  - **过滤参数的花括号列表**：`--exclude-dir=`、`--exclude=`、`--include=` 的整个值是字面量列表 `{a,b,…}` 时，等同于按项重复该参数，不算不透明。条件：至少两项，每项只含字母、数字和 `._*-`，`}` 后紧跟词边界。拒绝：命令位置的花括号（`{rm,-rf,.}`）、拼词（`cat {.e,}nv`、`{a,b}c`）、含 `/`、`$`、嵌套或只有一项的列表、其他参数（`--file={…}`）、用引号拼出的前缀，以及任一项以 `.en` 开头或是密钥文件名（`--include={.env,x}`）。反斜杠转义（`a\ b`）仍按词内反斜杠一律询问。实现在 `opaque_loops.go` 的 `isFilterFlagBraceList` 与 `shell-constants.js` 同名函数。
  - 原有规则保持：`while read` 的输入来自不会列出隐藏/密钥文件的列举命令；可信变量只出现在只读工具参数或 `PATH=` 里；继承的其他环境变量（`$OPENAI_API_KEY`、`$KUBECONFIG`）、`$1`、变量当命令名一律不透明。`<(…)`/`$(…)` 拆出内部命令独立判定，危险判定同时扫描拆开后的文本和代入后的文本。引号定界 heredoc 视为数据；未加引号且正文含 `$`/反引号的 heredoc、here-string（`<<<`）仍不透明。`nohup`/`time`/`exec`/`command`/`env X=y` 剥掉前缀判定，裸 `env` 不透明。只有完全信任放行内联解释器脚本，以及 shell 运行磁盘上已有的脚本文件（`bash build.sh` 等同 `./build.sh`）；`sh -c`、`bash -s`、管道喂入、附件目录、受保护路径、`/tmp` 下脚本、带 `$` 的参数，以及同一条命令里还出现该脚本文件名（可能刚写入或下载，如 `curl -o i.sh … && bash i.sh`）仍不透明。用例写进 `.opencode/lib/opaque-command.cases.json`（`trustedOpaque` 表示完全信任下的结果）和 `.opencode/lib/permission-request.cases.json`（请求级，本会话用例显式带 `sessionID` 或 `externalID`），Go 与 JS 共用。
- 每发现一个绕过，必须作为回归用例写进对应 `*.cases.json`。
- **审批超时只拒绝，永不放行**。到点的请求回复 reject，不得改写成放行。
- **硬拒绝类不得放宽**：无人值守模式的密钥读取、任何 `git push` 与发布、提权、凭据外发，不能被模式、会话授权或「始终允许」绕过。无人值守放行路径跳过通用危险判定，所以它的密钥判定与通用判定共用同一组 `.env`/`.envrc` 正则（Go `envFilePattern`、JS `ENV_FILE_PATTERN`），任意目录前缀（`apay/.env`）都拒绝。用例在 `.opencode/lib/unattended.cases.json`，Go 与 `.opencode/lib/unattended-policy.js` 共用。
- **「始终允许」只承诺门控真会记住的授权**：本会话精确命令授权只记住非破坏性、且完全信任下可归约的命令（`python3 -c '…'`、`bash build.sh` 这类固定文本）；完全信任下仍不透明的命令（`bash -c "$X"`、`env | grep`、同一命令里下载再执行的脚本）和破坏性命令不记，卡片也不显示该按钮并说明原因（Go `sessionGrantRefusal` 与 `.opencode/lib/opaque-command.js` 同名函数，用例是 `permission-policy.cases.json` 的 `sessionGrant` 字段）。无人值守硬拒绝在会话授权之前判定，其他模式下授权过的 `git push` 照样拒绝。
- **无人值守不能直接放行动态代码**：静态分类器看不穿 `eval`、管道/heredoc/进程替换/`/dev/stdin` 进 Shell 或解释器、`$CMD args`、`env -S`、`parallel`、`xargs`/`find -exec` 拼出的敏感子命令、git 未知子命令（可能是别名）与会执行命令的 `git -c` 配置，以及会起进程、动态加载、读环境变量或联网的解释器内联代码（`python3 -c`、`node -e`、`python3 - <<'EOF'` 等），这些一律按 `dynamic` 拒绝；每发现一种绕过写法，就把它作为拒绝用例加进 `unattended.cases.json`（Go `approval_unattended_parse.go` + `approval_unattended_dynamic.go` 与 JS `.opencode/lib/unattended-shell.js` + `unattended-policy.js` 同时通过）。静态判定只是尽力而为，凭据隔离靠沙箱：
  - **沙箱保证**：命令沙箱禁读 `~/.ssh`、`~/.aws`、`~/.gnupg`、`~/.kube`、`~/.config/gh`、`~/.config/gcloud`、`~/.azure`、`~/.docker/config.json`、`~/.netrc`、`~/.npmrc`、`~/.pypirc`、`~/.git-credentials`、`~/.cargo`、`~/.hoxi` 受保护状态文件，以及工作树内任意深度的 `.env`、`.env.local`、`.env.production`、`.env.development`（`internal/sandbox/profile.go` 的 `worktreeEnvRules` 用正则匹配；`.env.example` 和名为 `.env` 的目录里的文件仍可读；`~/Library/Keychains` 由审批平面硬拒绝，Seatbelt 层保留可读以供 `claude`/`agy` 读取自身 OAuth 凭据）；写入限于工作树等可写根，`.git/config` 与 hooks 不可写；运行时环境由 `internal/runner/sandbox_env.go` 剥除凭据变量（只保留该运行时调模型所需的 Key，开启 Bedrock/Vertex 时才保留对应云凭据）；Seatbelt 拒绝连接 SSH/GPG/1Password/Secretive agent 套接字。
  - **沙箱不保证**：网络放开（运行时需调模型），钥匙串查询（`git osxkeychain`、`gh` keyring）及模型 Key 外发靠静态拒绝拦截。仅隔离 worktree + 命令沙箱且运行时进程被 Seatbelt 包裹的会话可启用无人值守（OpenCode 文件工具跑在守护进程内，不享受无人值守全部放行）。
- **待处理清单不经隧道**。`/api/hoxi/permission/deferred` 保持在白名单之外，并保留隧道 403 用例。

自检：对你新增的每条"allow"路径，尝试用引号、反斜杠、`;`/`&&`/`|` 链接、`$()` 构造绕过，并写成用例。

## 2. GoPanel 隧道白名单

**事故**：白名单按**前缀**放行 `/api/hoxi/worktree`、`/api/hoxi/file`，导致手机端可经 `verify-run` 执行任意 `sh -c`、经 `file/open` 启动任意程序、经 `file/content` 读取 `~/.claude/.credentials.json` 等凭据；`/file` 原始 OpenCode 透传还被注入网关令牌。

规则：
- 白名单必须是**显式的"方法 + 路由"清单**，禁止前缀匹配。领域包新增的路由默认**不可**经隧道访问。
- 以下能力永不经隧道开放：执行命令或写执行配置（`verify-run`、`verify-config`、Hook 安装）、`open`/启动程序、工作区外文件读写与列目录、账号 / 提供商 / 凭据 / 更新器 / 浏览器、OpenCode 原始透传。
- 经隧道的文件读取必须限制在已注册工作区与会话 worktree 内。
- **桌面端读取/打开敏感凭据路径必须每次弹原生确认**（`internal/gateway/file_access.go` + `internal/app/file_access.go`）：`GET /file/content` 与 `POST /api/hoxi/file/open`（在访达中显示、用默认应用打开）命中 `isSensitiveCredentialPath` 时，桌面端弹一次原生对话框，确认后仅本次放行；**永不记忆、永不写入「始终允许」、并发不叠加**，无对话框（headless）、超时、取消、报错一律拒绝。远程控制器永远直接 403，绝不触发桌面对话框（用例见 `internal/gateway/file_access_test.go`）。
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
- 审批侧「结束本会话自己的进程」放行（`internal/runner/owned_process.go` + `process_control.go`，仅 Go 侧动态判定）同样遵守这条：归属只认 Hoxi 自己在命令运行时记下的进程组（OpenCode 按 `# hoxi-stop:<会话>:<调用>` 标记，原生 CLI 按运行时进程树内的组长），不按命令文本或进程名猜；仅限顶层 `pkill [-信号] [-f|-x|-i] <字面量模式>` 与 `kill [-信号] <字面量 PID…>`，核实所有目标均属本会话后原地替换为 `true`（审计来源 `own_process`）；`pkill` 只要有一个匹配目标不属于本会话就照常询问，绝不因为「大部分是本会话的」而放行。`killall`、动态参数、进程组/作业号、`-u`/`-v`/`-F`、前缀包装（`sudo`/`timeout`/`env`/`FOO=1`）、`$(…)` 内命令及同命令含 `alias`/`source`/改 `PATH` 等一律不遮蔽；AI 审查不使用遮蔽结果，Windows 不核实 `pkill`。改动这里必须同时补允许与拒绝两类测试（`owned_process_unix_test.go`、`process_control_test.go`）。
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
- 数据加载、合并、轮询、世代号与跨视图会话分组/聚合（如子 Agent 归组 `groupSubagentsByRoot`、运行态计数）属于 `store/`；**严禁在 `components/` 下新建领域模型文件直接读取 `workspace.sessions` 或复制改写 `store/` 的父/根会话解析逻辑**。
- **禁止复制已有的 store 逻辑**到组件里"改一改再用"。需要变体就把 store 函数参数化。
- 列表行与卡片的局部 UI 状态（如侧边栏子 Agent 抽屉 `manualExpanded`、`ThoughtBox` 展开集合）必须按稳定 ID（`session.id` / `part.id`）存储在 Store 或有容量上限（FIFO/LRU）的模块级集合中，不能依附于组件局部 Signal（切换视图或搜索重挂载即丢失），也不得无上限增长。
- 避免脱离 Solid 响应式代理后用多组平行数组或首键抽样（如只比对 `firstSessionKey`）手写脏检查；需要规避深层 Proxy 开销时，在写入口统一递增单调修订版本号（Revision Signal）。
- 任何"挂起等待"（pending deep link、pending action）都必须有过期条件（切换项目 / 超时），不得在无关时刻突然执行。
- 所有异步写入 store 的地方都要有防过期响应校验，包括弹窗（目录变化 / 关闭重开）。
- 只有一个地方可以写派生的全局状态（如 `activeProjectID`）；SSE 事件不得顺手改它。

## 8. 会话级内存状态、轮询幂等与跨层契约卫生

**事故**：`lastDispatchBySession` 在轮次正常 `idle` 完成或会话删除时未清理，迟到的 `session.error` 把已完成的上一轮提示词与大图附件重新发送；`FilterOpenCodeQuestions` 在每 4s 轮询路径无幂等保护地派生后台 `/reject` 请求；`runner.Service` 上的 `openCodeChildParent` 等多个 `sync.Map` 在会话删除时漏清理且跨重启丢失子会话映射；后端把 OpenCode 子会话审批的 `sessionID` 覆写为根会话 ID 导致前端子会话级清理失效。

规则：
- **会话级与轮次级内存状态成对清理**：凡在 `runner.Service`（如 `openCodeChildParent`、`managedOpenCode`、`editedFiles`、`userSkillByMsg`、`stoppedTools`、`cliSystemDelivered`）或前端 Store（如 `lastDispatchBySession`、`autoFailoverPendingAtBySession`）中按 `sessionID` / `messageID` 索引的运行时状态，**必须**在会话删除（`deleteConversation` / `purgeSessionsFromStoreState`）及轮次正常完成（`session.idle` / `session.status(idle)`）时统一清理，严禁新增"只写不删"的旁路 Map。
- **跨重启身份映射持久化**：凡影响会话归属或父子层级的关系（如 OpenCode 子会话到 Hoxi 父会话的映射），必须落盘到 `SessionRecord` 并在启动时恢复，不得仅存内存 `sync.Map`。
- **重写父会话 ID 时保留原始子会话元数据**：后端将子会话审批/问题事件的 `sessionID` 归一化为根会话 `hoxiID` 时，必须在 `metadata`（如 `subagentSessionID`）保留原始子会话 ID，确保前端按单个子会话生命周期精确清理残留卡片。
- **轮询/过滤路径触发的后台写操作必须幂等去重**：在 `GET /permission`、`GET /question` 等高频轮询读取路径中顺带清理孤儿条目（如 `rejectStaleOpenCodePermission` / `rejectStaleOpenCodeQuestion`）时，必须通过单次 claim/settled 状态机防重，严禁每次轮询无锁派生后台请求。
- **跨层错误与冷却解析保持一致**：Go `runner`（`turn_retry.go`）、Go `provider`（`quota_reset.go`）与前端 `auto-failover.ts` 的错误分类及重置时间解析规则必须保持语义对齐，避免宽泛正则（如无上下文前缀的 `in 2m`）误判冷却时间。

## 9. 测试纪律

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
- [ ] 前端是否有平台 API / 网络调用 / 数据加载 / 领域聚合出现在 `components/` 里？是否复制了 store 逻辑？列表局部状态是否按稳定 ID 且有界存储？
- [ ] 新增的按 `sessionID` / `messageID` 索引的内存 Map/Set，是否在轮次 `idle` 和会话删除时成对清理？轮询触发的后台清理是否幂等？
- [ ] `go test -race`、`npm --prefix web run lint && npm --prefix web test && npm --prefix web run build` 是否全绿？
