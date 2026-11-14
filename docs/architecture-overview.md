# Hoxi 架构概览（现状、目录与规划）

本文从根目录 `AGENTS.md` 移出，内容为模块现状、目录结构与未实现规划，不常驻 AI 会话上下文。以下情况先读本文：定位模块或目录归属、判断某能力是否已经实现、在注释或 UI 文案中描述现有能力。写代码必须遵守的约束仍以 `AGENTS.md` 为准；文档与代码冲突时以代码和测试为准并同步修正文档。

## 一、现状

### 1.1 桌面端

- **宿主与状态栏**（`internal/app` + `internal/statusbar`）：Wails v2（`WKWebView` / `WebView2`），单实例锁、关窗隐藏驻留；WebView 经 AssetServer 进程内交网关，回环监听桥接端口（release `127.0.0.1:5410`、dev `127.0.0.1:5420`，占用回落随机端口）供 WS 与 CLI 使用。原生托盘显示 `Running` / `Attention`，待审批新增时回调一次（不含命令文本）
- **OpenCode 运行时**（`internal/opencode`）：监管官方 `opencode serve`（随机密码 + Basic Auth），按 PID、启动时间与参数登记到 `~/.hoxi/reclaim.json`（`internal/reclaim`），仅回收已退出 Hoxi 的孤儿守护进程（不扫进程表）；Windows 经 `subproc.StartTied` 绑 Job Object，NSIS 按完整路径结束进程
- **多运行时与统一审批**（`internal/runner`）：统一驱动 OpenCode、Claude Code、Antigravity（`agy`）、Gemini CLI（`gemini`）、Qoder、CodeBuddy 为统一事件流；原生 CLI 遇瞬态错误由 `turn_retry.go` 退避重试，OpenCode 推理截断或过渡动作断流由 `opencode_turn_recovery.go` 自动续跑。PreToolUse / BeforeTool Hook 接入 `approval_decision.go`（`~/.hoxi/approval-policy.json`），支持桌面专属 `trusted` 完全信任与 `unattended` 无人值守模式（隔离 worktree + Seatbelt 沙箱）
- **前端工作区**（`web/`）：Vite + SolidJS + Tailwind 三栏布局；SSE 经 `requestAnimationFrame` 批处理；聊天跟随尾部时限制自动挂载窗口，阅读历史期间保留锚点，回到底部后回收旧 DOM；长流式 Markdown 按长度限制解析发布频率，聊天代码高亮由共享、有界且可回收的 Worker 执行，等待高亮时继续显示完整文本；时间线与上下文贡献值按 turn/part 缓存响应式计算；会话搜索在输入关键词后按 200 turn 分页扫描，冷页不直接挂入响应式正文，选择匹配项时才加载导航所需历史；正则匹配与高亮范围计算在独立 Worker 执行，每批超时 1.5 秒并终止，换关键词/会话或关闭时取消旧任务，DOM 高亮优先当前目标并限制节点、字符、范围与收集耗时；本地 Markdown 图片近视口加载，共享缓存按 16 MiB 字符串预算淘汰，浏览器预览帧用可回收 Blob URL；侧边栏将非归档子会话归组在根父会话下，主视图含嵌套时间线、`ThoughtBox`、`ToolCard`、`@pierre/diffs`、`@xterm/xterm`、内置浏览器检视器（`internal/browser`）及 Chrome Link 扩展（`extensions/chrome`，详见 [`BROWSER.md`](../BROWSER.md)）
- **会话存储与实时更新**（`internal/runner`）：编辑路径及子会话链接按工具 part 增量维护，文本 delta 不使文件清单缓存失效；历史冷分页按 `turns.index.json` 偏移读取目标消息的 upsert 记录，索引同时缓存编辑路径与子会话摘要，后续 Diff 查询不挂载完整正文；旧日志首次访问按需建索引，索引损坏先备份重建。标题/归档、项目归属及部分运行时标量元数据更新经类型受限的 `UpdateMetadata`，不加载或复制正文、不失效编辑摘要，保留冷撤销历史及排队中的流式写入；正文/工作树/运行时迁移继续走完整更新路径。宿主生命周期启用异步持久化后，流式快照先更新内存与广播，约 25ms 合并落盘（最多 4 个后台写入任务，256 个待写键触发同步回压）；`idle`/`error` 与退出保留 flush + fsync 屏障，流式日志压缩及索引维护在后台执行
- **原生电脑控制**（`internal/computer`）：macOS 与 Windows 前台窗口读取与输入（`hoxi_computer`），按会话与工作树原生弹窗授权单个运行中应用（禁终端、密码管理器、系统设置），默认每次输入仅限本机「允许一次」（用户可在电脑控制面板为当前已授权应用开启连续输入免确认，无人值守模式始终拒绝输入），只截目标窗口（详见 [`COMPUTER.md`](../COMPUTER.md)）
- **Work 店铺商品管理与 MCP**（`internal/workcatalog` + `web/src/components/workcatalog` + 可选 `work/web/WorkCatalogSurface.tsx`）：店铺级商品主视图无需聊天即可浏览，提供本机接口授权配置、后台同步进度与续传、轻量列表/搜索/分页、详情、修改草稿、发布记录和选品导入上新草稿；界面 HTTP 门面与官方 MCP Go SDK 共用领域服务，MCP 保留会话/目录/店铺绑定，发布沿用桌面审批和回读收据。当前线上可写标题、详情和主图，新建商品仅本地草稿、远端创建尚未开放；SKU/价格/库存等保留原始字段用于分析；真实店铺仍需开放平台授权验收，详见 [`work-store-mcp.md`](work-store-mcp.md)
- **会话、接管与 Skills**：Git Worktree 隔离（`internal/worktree`，`?directory=` 作用域）、历史分页（`session-history.ts` 首屏取最新 200 条）、Steer / Stop、Revert / Fork、`/goal` 目标（`[[goal:complete]]` / `[[goal:blocked]]` 或 `goal_report`）；8 维记忆共享 `.opencode/lib/memory-compaction.js`，落盘 `<project>/.hoxi/memory/sessions/<session>.md`。`internal/handoff` 接管外部 CLI 会话；`internal/skills` + `internal/tools` + `internal/workapps` + `internal/ecom` 提供本地/线上 Skill、免 Key 工具网桥（`/api/hoxi/tools/{discover,inspect,run}`）、Work 多店铺防关联代理/指纹隔离档案与确定性跨境电商测算引擎（`hoxi ecom` + 内置 `ecommerce-ops` Skill）；`internal/attachment` + `hoxi doc` + 内置 `office-docs` Skill 提供 `.pdf`、`.xlsx`/`.xls`/`.ods`/`.csv`/`.tsv`、`.docx`、`.pptx` 免依赖解析、检视器预览与生成修改
- **远程协同通道**（不开放公网端口，复用同一远程路由白名单）：① **GoPanel 反向隧道**（`internal/tunnel` + `gateway/tunnel.go`），含 APNs 计数推送；② **局域网配对直连**（`internal/pairing` + `gateway/lan.go`，监听 `:5417`，仅限局域网，令牌存 `~/.hoxi/pairing.json`）；③ **Cloudflare Tunnel 与反向代理入口**（`internal/cloudflared` + `gateway/ingress.go`，监听 `127.0.0.1:5418`，仅认独立 `relayToken`，凭据存 `~/.hoxi/cloudflared.json`）
- **提供商、中转与自动选模**（`internal/provider`）：① 回环 OpenAI 兼容中转（`relay.go` + `relay_keys.go`，`hk-*` 哈希存 `~/.hoxi/relay-keys.json`，永不入隧道）；② 官方账号直连（`official.go`，独立写入 `hoxi-official`，退出同步清理）；③ `hoxi-auto` 自动池与熔断（`auto_router.go` + `breaker.go` + `quota_reset.go`，存 `~/.hoxi/provider-breakers.json`），前端 `auto-models.ts` 会话粘性选模保 KV Cache，`auto-failover.ts` 遇限流/额度/鉴权/模型错误立即换候选，多轮会话遇瞬态网络/5xx 优先同模型重试 1 次保缓存后再切换；支持 MCP OAuth 2.1 PKCE（`mcp_oauth.go`）

### 1.2 目录结构

```text
/
├── main.go / cmd/hoxi/main.go        # Wails 桌面入口 / CLI & headless 入口（委托 internal/app）
├── internal/
│   ├── app/, cli/                    # [顶层] DesktopApp 生命周期与 Wails 绑定 / 终端 `hoxi` CLI
│   ├── gateway/                      # [网关层] 进程内反向代理、Basic Auth、WS/SSE 桥接、隧道路由白名单
│   ├── runner/                       # [业务层] 多运行时执行引擎、审批门控与回滚
│   ├── opencode/ (+ ocapi/, ocstate/)# [业务层/底座层] OpenCode 监管与插件覆盖层 + 回环客户端与 SQLite 同步
│   ├── provider/                     # [业务层] 提供商配置、llm.Client 实现、本机中转、官方直连、自动池与熔断
│   ├── worktree/, workspace/         # [业务层] Git worktree 沙箱、Diff、快照 / 多工作区配置
│   ├── browser/, computer/           # [业务层] Chrome 检视器、Chrome Link / 原生窗口读取与前台输入
│   ├── handoff/, orchestration/      # [业务层] 跨 CLI 会话接管与桥接 / 多 Agent DAG Convoy 编排
│   ├── codeindex/, recall/, forge/   # [业务层] Tree-sitter 与 trigram 索引 / 跨会话 BM25 召回 / GitHub & GitLab
│   ├── skills/, tools/, workapps/, ecom/ # [业务层] Agent Skills / 免 Key 工具网桥 / Work 多店铺防关联档案 / 跨境电商测算与合规引擎
│   ├── workcatalog/                  # [业务层] Work 店铺商品 MCP、平台适配、同步快照与草稿发布收据
│   ├── tunnel/, pairing/, cloudflared/# [业务层] GoPanel 反向隧道 / 局域网扫码配对 / Cloudflare Tunnel 监管
│   ├── auth/, attachment/, devpulse/, promptassist/, leaderboard/, sandbox/, updater/, telemetry/ # [业务层]
│   └── hoxipaths/, llm/, agentcli/, agyhook/, claudeconfig/, diagnostics/, buildinfo/, notify/, osopen/, reclaim/, sqlitecli/, statusbar/, subproc/, textindex/, testutil/execstub/ # [底座层/测试辅助]
├── web/src/{components,store,services,i18n,__tests__}/ # 按域 UI、响应式 Store（workspace.ts 扁平导出）、网络层、双语词条与单测
├── web/scripts/                      # check-i18n.ts, check-lines.ts（800 行与分层门禁）, check-cycles.ts, run-tests.ts
├── extensions/chrome/                # Chrome Link MV3 扩展（worker.js, protocol.js, popup）
├── .opencode/{plugins,lib,skills}/   # 上游 OpenCode 配置根（只读）：hoxi-memory.js, hoxi-goal.js, 共享策略与 *.cases.json
├── .hoxi/memory/                     # Hoxi 项目状态（git-ignored）：project.md 与 sessions/<session>.md
└── docs/, scripts/                   # 架构护栏、隧道、更新与发版指南 / 一键发版与打包脚本
```

---

## 二、规划（未实现）

勿将以下内容当现有能力或在注释/UI 文案中宣称已支持：

| 项目 | 说明 |
| --- | --- |
| 远程跨网 E2E 加密与盲目中继 | LAN 直连与 `:5417` 令牌监听**已实现**；跨网 E2E 未实现（[`docs/remote-access-e2e-contract.md`](remote-access-e2e-contract.md)） |
| 隧道 WebSocket / 凭据入钥匙串 / Android 推送 | 远程 `/pty` 当前返回 501；隧道凭据当前为 `0600` 文件；Android 推送未实现 |
| 远程隧道公网真机验收 | Hoxi 通道 `prompt_async` 与审批 `reply` 跨重启幂等收据、单步工具排空及 `~/.hoxi/task-ledger.json` 跨重启任务账本自动续跑**已实现**；公网真机验收未完成 |
