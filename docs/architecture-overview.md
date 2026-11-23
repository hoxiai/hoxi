# Hoxi 架构概览（现状、目录与规划）

本文从根目录 `AGENTS.md` 移出，内容为模块现状、目录结构与未实现规划，不常驻 AI 会话上下文。以下情况先读本文：定位模块或目录归属、判断某能力是否已经实现、在注释或 UI 文案中描述现有能力。写代码必须遵守的约束仍以 `AGENTS.md` 为准；文档与代码冲突时以代码和测试为准并同步修正文档。

## 一、现状

### 1.1 桌面端

- **项目原生配置**（底座 `internal/hoxiconfig`）：执行目录的 `hoxi.json` 只读加载 Skills 路径、MCP 与权限约束，JSONC/类型/路径校验失败明确报错；本机 Provider/Model 仍走 `.hoxi/models.json`。项目 allow 不绕过统一审批，本地 MCP 启动额外询问；有效配置不回写全局，详见 [`hoxi-config.md`](hoxi-config.md)

- **宿主与状态栏**（`internal/app` + `internal/statusbar`）：Wails v2（`WKWebView` / `WebView2`），按主屏可用工作区自适应初始窗口尺寸并记忆用户手动调整后的尺寸（`~/.hoxi/window-state.json`），单实例锁、关窗隐藏驻留；WebView 经 AssetServer 进程内交网关，回环监听桥接端口（release `127.0.0.1:5410`、dev `127.0.0.1:5420`，占用回落随机端口）供 WS 与 CLI 使用。原生托盘显示 `Running` / `Attention`，待审批新增时回调一次（不含命令文本）
- **默认原生 Agent 与 OpenCode 兼容**（`internal/agent` + `internal/runner`）：默认使用进程内 Go Agent，不在启动时运行 `opencode serve`；OpenCode 可作为按需 CLI 运行时使用，旧会话经迁移导入 Runner 存储。原生引擎提供 Anthropic、OpenAI Chat/Responses、Gemini 协议，内置文件/命令/代码索引工具及 Hoxi 宿主能力、Skills、MCP。旧守护进程监管与回收代码仍保留在 `internal/opencode` / `internal/reclaim`
- **原生能力保真边界**：子 Agent 默认继承主会话模型，显式角色/调用配置可覆盖模型。可写子任务继承父任务的推理强度，只读子任务将 `xhigh` / `max` 限为 `high`；经济模型仅在显式选择时使用。子任务继承父任务取消和截止时间，仍受步数、单次工具与网络超时限制。常规 75% 水位裁剪在 12 消息量化块内折叠已被后续编辑或覆盖重读取代的旧 `read` 快照、收紧通过态命令日志并保留中间诊断行与子目录规则块，不跨量化块改写早期前缀；手动及溢出压缩将完整文本和工具证据按模型预算分段摘要、逐层合并，失败不提交部分摘要；摘要模型回报的用量计入会话累计并单列，媒体只在摘要中保留描述。原生轮次在交接或中断恢复时以独立用户内容块携带会话摘要，普通续聊重放已有历史，用户编辑的会话笔记位于用户上下文开头，项目指引继续进入系统提示。MCP 工具目录支持按服务器游标续页，MCP 原始结果保留于工具元数据，结构化结果与嵌入文本进入上下文，图片/PDF/音频进入媒体链路，资源链接不自动读取；Anthropic/OpenAI 适配器对当前不支持的媒体（如音频）明确提示，Gemini 按 inlineData 发送。Go 同名方法图优先使用当前索引包的类型信息，未能解析的调用会标记为候选；编辑后语言诊断报告执行结果或跳过、超时状态，主要依赖 CLI 检查，尚不等同于完整 LSP；第三方 OpenCode 插件兼容及真实任务能力等效仍需独立验收
- **原生编码工具**：Build 默认按需加载浏览器、电脑控制和媒体参数。工具参数计入历史裁剪预算。补丁按定位行、文件末尾或统一 diff 坐标匹配，多文件写盘途中失败会报告已写文件；搜索支持按匹配行续页；TS/TSX 索引可按类名定位跨行方法。前台命令长输出在当前轮次保留最近 4 份、每份前 4 MiB，可用 `bash action=output` 按字节偏移回读
- **多运行时与统一审批**（`internal/runner` + 子包 `turnstream`、`shellpolicy`、`procgroup`）：统一驱动 OpenCode、Codex、Claude Code、Antigravity（`agy`）、Gemini CLI（`gemini`）、Qoder、CodeBuddy 为统一事件流（`turnstream`）；原生 CLI 遇瞬态错误由 `turn_retry.go` 退避重试，OpenCode 推理截断或过渡动作断流由 `opencode_turn_recovery.go` 自动续跑。PreToolUse / BeforeTool Hook 接入 `shellpolicy/decision.go`（`~/.hoxi/approval-policy.json`），支持桌面专属 `trusted` 完全信任与 `unattended` 无人值守模式（隔离 worktree + Seatbelt 沙箱）
- **前端工作区**（`web/`）：Vite + SolidJS + Tailwind 三栏布局；SSE 经 `requestAnimationFrame` 批处理；聊天跟随尾部时限制自动挂载窗口，阅读历史期间保留锚点，回到底部后回收旧 DOM；长流式 Markdown 按长度限制解析发布频率，聊天代码高亮由共享、有界且可回收的 Worker 执行，等待高亮时继续显示完整文本；时间线与上下文贡献值按 turn/part 缓存响应式计算；会话搜索在输入关键词后按 200 turn 分页扫描，冷页不直接挂入响应式正文，选择匹配项时才加载导航所需历史；正则匹配与高亮范围计算在独立 Worker 执行，每批超时 1.5 秒并终止，换关键词/会话或关闭时取消旧任务，DOM 高亮优先当前目标并限制节点、字符、范围与收集耗时；本地 Markdown 图片近视口加载，共享缓存按 16 MiB 字符串预算淘汰，浏览器预览帧用可回收 Blob URL；侧边栏将非归档子会话归组在根父会话下，主视图含嵌套时间线、`ThoughtBox`、`ToolCard`、`@pierre/diffs`、`@xterm/xterm`、内置浏览器检视器（`internal/browser`）及 Chrome Link 扩展（`extensions/chrome`，详见 [`BROWSER.md`](../BROWSER.md)）
- **会话存储与实时更新**（`internal/runner/sessionstore` + `memstore` + `taskledger`）：编辑路径及子会话链接按工具 part 增量维护，文本 delta 不使文件清单缓存失效；历史冷分页按 `turns.index.json` 偏移读取目标消息的 upsert 记录，索引同时缓存编辑路径与子会话摘要，后续 Diff 查询不挂载完整正文；旧日志首次访问按需建索引，索引损坏先备份重建。标题/归档、项目归属及部分运行时标量元数据更新经类型受限的 `UpdateMetadata`，不加载或复制正文、不失效编辑摘要，保留冷撤销历史及排队中的流式写入；正文/工作树/运行时迁移继续走完整更新路径。宿主生命周期启用异步持久化后，流式快照先更新内存与广播，约 25ms 合并落盘（最多 4 个后台写入任务，256 个待写键触发同步回压）；`idle`/`error` 与退出保留 flush + fsync 屏障，流式日志压缩及索引维护在后台执行
- **原生电脑控制**（`internal/computer`）：macOS 与 Windows 前台窗口读取与输入（`hoxi_computer`），按会话与工作树原生弹窗授权单个运行中应用（禁终端、密码管理器、系统设置），默认每次输入仅限本机「允许一次」（用户可在电脑控制面板为当前已授权应用开启连续输入免确认，无人值守模式始终拒绝输入），只截目标窗口（详见 [`COMPUTER.md`](../COMPUTER.md)）
- **会话、接管与 Skills**：Git Worktree 隔离（`internal/worktree`，`?directory=` 作用域）、历史分页（`session-history.ts` 首屏取最新 200 条）、Steer / Stop、Revert / Fork、`/goal` 目标（`[[goal:complete]]` / `[[goal:blocked]]` 或 `goal_report`）；8 维记忆共享 `internal/opencode/memory_compaction.js`，落盘 `<project>/.hoxi/memory/sessions/<session>.md`。`internal/handoff` 接管外部 CLI 会话；`internal/skills` + `internal/tools` 提供本地/线上 Skill 与免 Key 工具网桥（`/api/hoxi/tools/{discover,inspect,run}`）；`internal/attachment` + `hoxi doc` + 内置 `office-docs` Skill 提供 `.pdf`、`.xlsx`/`.xls`/`.ods`/`.csv`/`.tsv`、`.docx`、`.pptx` 免依赖解析、检视器预览与生成修改
- **远程协同通道**（不开放公网端口，复用同一远程路由白名单）：① **GoPanel 反向隧道**（`internal/tunnel` + `gateway/tunnel.go`），含 APNs 计数推送；② **局域网配对直连**（`internal/pairing` + `gateway/lan.go`，监听 `:5417`，仅限局域网，令牌存 `~/.hoxi/pairing.json`）；③ **Cloudflare Tunnel 与反向代理入口**（`internal/cloudflared` + `gateway/ingress.go`，监听 `127.0.0.1:5418`，仅认独立 `relayToken`，凭据存 `~/.hoxi/cloudflared.json`）
- **提供商、中转、自动选模与多模态生成**（`internal/provider` + `gateway/media_routes.go`）：① 回环 OpenAI 兼容中转（`relay.go` + `relay_keys.go`，`hk-*` 哈希存 `~/.hoxi/relay-keys.json`，永不入隧道）；② 官方账号直连（`official.go`，独立写入 `hoxi-official`，退出同步清理）；③ `hoxi-auto` 自动池与熔断（`auto_router.go` + `breaker.go` + `quota_reset.go`，存 `~/.hoxi/provider-breakers.json`），前端 `auto-models.ts` 会话粘性选模保 KV Cache，`auto-failover.ts` 遇限流/额度/鉴权/模型错误立即换候选，多轮会话遇瞬态网络/5xx 优先同模型重试 1 次保缓存后再切换；④ 本机图片、视频与语音生成（`hoxi_image` / `hoxi_video` / `hoxi_speech`、`/api/hoxi/media/{image,video,speech,roles}`，默认角色存 `~/.hoxi/media-roles.json`）执行三级自检：先查当前会话模型自身原生输出能力（`session_self`），再查用户在「默认与辅助模型分配」设定的默认媒体模型（`role_default`）或本机已配置专职模型（`configured_model`），无本机能力时返回 `missingCapability` 并由前端 `ToolCard` 展示非模态内联引导卡（去配置本地模型 / 云端 `hoxi_tools` / SVG 替代）；支持 MCP OAuth 2.1 PKCE（`mcp_oauth.go`）

- **任务总览与定时任务**（`internal/scheduler` + `web/src/store/task-overview.ts` + `web/src/store/schedules.ts`）：已完成、失败或取消的编队子任务和子会话可从桌面任务列表移除（本地显示偏好，不删除会话或代码改动；运行中的行不可移除）。工作区级定时任务存本机 `~/.hoxi/schedules.json`（受 `hoxipaths` 与审批策略保护、不入远程隧道），支持 Cron/固定间隔、绑定固定会话或按次新建会话（默认 `local` 不强制 worktree，可选 `worktree` 与命令沙箱）、可选绑定 Skill 与显式模型配置，并可通过原生 `hoxi_schedule` 工具经桌面一次性审批确认创建（定时会话自身禁止递归修改）及 `hoxi schedule` CLI 管理。

### 1.2 目录结构

- **应用联动**：[`hoxi-app/v1`](hoxi-app-protocol.md) 由 `internal/appbridge` 管理本机 PKCE、授权令牌与一次性审批，`internal/gateway` 暴露回环能力 HTTP 和 Host MCP，桌面负责深链、系统回调和授权界面；不加入远程白名单。

```text
/
├── main.go / cmd/hoxi/main.go        # Wails 桌面入口 / CLI & headless 入口（委托 internal/app）
├── internal/
│   ├── app/, cli/                    # [顶层] DesktopApp 生命周期与 Wails 绑定 / 终端 `hoxi` CLI
│   ├── gateway/                      # [网关层] 进程内反向代理、Basic Auth、WS/SSE 桥接、隧道路由白名单
│   ├── runner/ (+ sessionstore/, memstore/, shellpolicy/, turnstream/, procgroup/, taskledger/) # [业务层/底座层] 多运行时协调、存储、8维记忆、审批策略、事件流翻译、进程组与任务账本
│   ├── agent/                        # [业务层] 默认 Go Agent：模型协议、工具循环、子任务与上下文压缩
│   ├── opencode/ (+ ocapi/, ocstate/)# [业务层/底座层] OpenCode 监管与插件覆盖层 + 回环客户端与 SQLite 同步
│   ├── provider/                     # [业务层] 提供商配置、llm.Client 实现、本机中转、官方直连、自动池与熔断
│   ├── worktree/, workspace/         # [业务层] Git worktree 沙箱、Diff、快照 / 多工作区配置
│   ├── browser/, computer/, termhost/# [业务层] Chrome 检视器、Chrome Link / 原生窗口读取与前台输入 / 宿主 PTY
│   ├── handoff/, orchestration/, intake/, scheduler/ # [业务层] 跨 CLI 会话接管与桥接 / 多 Agent DAG Convoy 编排 / 单项目测试协作提单 / 本机工作区级定时任务调度
│   ├── codeindex/, recall/, forge/   # [业务层] 符号分块、调用链启发式与向量/BM25 索引 / 跨会话 BM25 召回 / GitHub & GitLab
│   ├── skills/, tools/               # [业务层] Agent Skills 与免 Key 工具网桥
│   ├── tunnel/, pairing/, cloudflared/, remotehost/ # [业务层] GoPanel 隧道 / 扫码配对与 E2EE / Cloudflare Tunnel / 跨平台 `hoxi host` 无头宿主与挂载
│   ├── auth/, attachment/, devpulse/, promptassist/, leaderboard/, sandbox/, updater/, telemetry/ # [业务层]
│   └── hoxipaths/, hoxiconfig/, canonical/, llm/, agentcli/, agyhook/, claudeconfig/, dataprofiles/, diagnostics/, buildinfo/, notify/, osopen/, reclaim/, sqlitecli/, statusbar/, subproc/, textindex/, testutil/execstub/ # [底座层/测试辅助]
├── web/src/{components,store,services,policy,i18n,__tests__}/ # 按域 UI、响应式 Store（workspace.ts 扁平导出）、网络层、审批策略 JS 镜像、双语词条与单测
├── web/scripts/                      # check-i18n.ts, check-lines.ts（800 行与分层门禁）, check-cycles.ts, run-tests.ts
├── extensions/chrome/                # Chrome Link MV3 扩展（worker.js, protocol.js, popup）
├── .hoxi/{skills,memory}/            # Hoxi 项目配置与状态根：.hoxi/skills/ 可随仓库共享，.hoxi/{memory,worktrees,attachments}/ 为 git-ignored
├── tools/{contractcheck,agenteval}/  # 架构契约门禁（DAG/800行/传输层/插件同步）与原生 Agent 真实编码评测基准
└── docs/, scripts/                   # 架构护栏、隧道、更新与发版指南 / 一键发版与打包脚本
```

---

### 1.3 远程请求与断线故障边界

- 桌面消息发送前持久保存有界请求 ID/会话/原目录，不保存提示词或附件正文；断线后按原 ID 回查，未确认（含可能先于原 POST 的 `not_admitted`）阻止重发，确认后刷新历史。桌面输入框上方针对长期停留在未知状态或本地记录损坏的会话提供重新核查与二次确认的人工解除锁定横幅（解除后绝不自动重发原命令）。业务失败后的新执行仍新 ID，等待后复核取消/轮次。暂未覆盖手机或提交/审批客户端持久恢复
- 已存在账本读取失败、JSON 损坏或版本不支持时保留原文件及私有备份，消息/审批/提交准入及回查拒绝，不以空记录覆盖；恢复有效账本并重启后继续，首次安装文件不存在仍正常初始化

- 消息提交、审批答复与工作树提交携带 `clientRequestId` 时，复用现有持久化请求账本；同 ID 内容变化返回 `409 request_conflict`，中断且无法确认的操作返回 `outcome_unknown`，不得自动生成新 ID 重放
- 执行上述操作前必须成功落盘准入记录；失败返回 `503 request_storage_unavailable` 并拒绝启动对应操作，存储恢复后使用原 ID 重试。完成回执丢失时，重启依据已落盘的准入记录保守报告 `outcome_unknown`，而不是重新执行。此保证不扩展到未携带请求 ID 的调用或其他写接口
- SSE 的初始快照、事件、心跳写失败立即结束连接并释放订阅；统一流同时取消其上游事件泵，不依赖外层请求稍后取消。补帧继续复用 `after_seq` 与 `hoxi.stream.resync`，不因重连重发业务命令

### 1.4 移动端双包协同与身份隔离

- **双包并行分发**：移动端按能力边界拆分为两个独立包名与应用名的客户端，可在同一部手机并存且互不覆盖安全存储：
  - **完整版 `Hoxi`（`ai.hoxi.app`，`hoxi://`）**：面向全功能随身工作台（含账号连接同步、语音通话、iOS APNs 审批计数提醒与完整门户生态），经官网 CDN（`hoxi-mobile`）与应用商店 / TestFlight 分发。
  - **轻量开源版 `Hoxi Remote`（仓库 `mobile/`，`ai.hoxi.remote`，`hoxi-remote://`）**：零账号、零遥测的纯净远程控制器（仅含局域网 `:5417` / Cloudflare `:5418` / GoPanel 隧道的 `hoxi-e2ee-v1` + X25519 配对、编码会话流、审批确认、Git 审查与终端），随 GitHub Releases 附带正式签名的 `Hoxi-Remote-<version>-android.apk`（支持 Obtainium 订阅更新），iOS 端提供源码供 Xcode 真机编译。

## 二、规划（未实现）

勿将以下内容当现有能力或在注释/UI 文案中宣称已支持：

| 项目 | 说明 |
| --- | --- |
| QUIC 直连 NAT 打洞、边缘配对票据服务与路径元数据隐藏 | `hoxi-e2ee-v1` 应用层端到端加密、`v1-sse` 流式加密与跨网首次 X25519 ECDH 密钥协商**已实现**；QUIC 打洞、一次性配对票据服务与请求路径隐藏未实现（[`docs/remote-access-e2e-contract.md`](remote-access-e2e-contract.md)） |
| 隧道 WebSocket / 凭据入钥匙串 / Android 推送 | 远程 `/pty` 严禁开放并返回 403；桌面隧道凭据当前为 `0600` 文件；Android 原生系统推送未实现 |
| 远程隧道公网真机验收 | Hoxi 通道 `prompt_async` 与审批 `reply` 跨重启幂等收据、单步工具排空及 `~/.hoxi/task-ledger.json` 跨重启任务账本自动续跑**已实现**；公网真机验收未完成 |
