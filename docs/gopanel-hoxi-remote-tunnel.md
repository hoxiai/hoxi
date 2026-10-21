# Hoxi ⇄ GoPanel 远程隧道契约

> 双仓同一份契约：`hoxi/docs/gopanel-hoxi-remote-tunnel.md` 与 `gopanel_v2/docs/hoxi-remote-tunnel.md` 内容保持一致，修改任一侧必须同步另一侧。
> 本文只描述**已实现并有测试覆盖**的行为；规划项单独列在「尚未实现」中，不能当作现有能力引用。

## 1. 目标与现状

让手机 Hoxi App 在任意网络下控制家里或办公室的 Hoxi 桌面：Mac 主动外连公网 GoPanel，手机只访问 GoPanel，由 GoPanel 经已有的隧道把请求转给 Mac。Mac 不开放任何监听端口。

| 能力 | 状态 |
| --- | --- |
| Hoxi 桌面与 GoPanel 配对、长连接、断线重连 | 已实现 |
| 手机经 GoPanel 访问 Hoxi 的 HTTP 接口 | 已实现 |
| 手机经 GoPanel 订阅 Hoxi SSE（`/api/hoxi/event`） | 已实现，逐块转发不缓冲 |
| 手机在服务器列表中发现并添加 Hoxi 电脑 | 已实现 |
| 经隧道的 WebSocket（终端 `/pty`） | 未实现，GoPanel 返回 501 |
| 同局域网手机直连 Mac（`5417` 监听） | 未实现，见第 9 节 |
| iPhone 审批提醒推送（APNs），点开进入语音审批 | 已实现，待真机验收；需在 GoPanel 配置 APNs 密钥 |
| Android 推送 | 未实现（国内无统一通道，需厂商推送或第三方） |

## 2. 拓扑

```text
手机 Hoxi App
  │  HTTPS  https://<gopanel>/api/tunnel/hoxi-nodes/<nodeId>/proxy/api/hoxi/...
  │  X-Auth: <GoPanel 登录 JWT>（可带 EntranceCode）
  ▼
公网 GoPanel（gopanel_v2）
  │  JWT 管理员鉴权 → 请求头白名单 → 本机隧道入口 127.0.0.1:<随机端口>
  │  （复用网站隧道的 serveTunnelHTTP，按 serviceId=hoxi-gateway 选路）
  ▼  WSS /api/tunnel/connect（Mac 主动外连，9 字节头二进制多路复用帧）
Hoxi 桌面（hoxi，internal/tunnel）
  │  路由白名单 → 进程内交给 Gateway.ServeHTTP（不经端口）
  ▼
/api/hoxi/sessions · /api/hoxi/event · /api/hoxi/permission · /api/hoxi/worktree …
```

隧道底座（配对、帧协议、心跳、服务注册、本机入口）是 GoPanel 原有的「双端网站隧道」，本方案没有新增协议，只新增了 Hoxi 客户端、GoPanel 的两条手机路由和手机端中继连接。

## 3. 使用流程

1. **GoPanel 签发配对码**：GoPanel 管理后台 → 节点 → 隧道设置，生成 10 分钟有效、一次性的 32 位十六进制配对码（`POST /api/tunnel/pairing`）。
2. **Hoxi 桌面配对**：Hoxi 设置 → 系统 → 经 GoPanel 远程控制，填写 GoPanel 地址、配对码和设备名，点「配对」。Hoxi 调用 GoPanel `POST /api/tunnel/pair/exchange` 换取长期凭据，随即连接并上报服务。GoPanel 节点列表中出现一个 `connectMode = tunnel` 的节点。
3. **手机添加**：手机「服务器列表」中已登录的 GoPanel 卡片右侧点电脑图标，列出该 GoPanel 下在线的 Hoxi 电脑，选中后保存为一个 Hoxi 节点（中继地址），之后与普通 Hoxi 节点一样使用。

解除：Hoxi 设置中点「解除配对」删除本机凭据；GoPanel 上的节点需管理员在 GoPanel 删除。

## 4. 线缆协议（与 `gopanel_v2/app/service/tunnel_protocol.go` 逐字节一致）

### 4.1 配对

`POST /api/tunnel/pair/exchange`（无需登录，配对码即凭证）

```json
{"code": "<32 hex>", "name": "Studio", "hostname": "Mac-Studio.local", "version": "0.2.9"}
```

响应使用 GoPanel 包装，**`code` 为 0 表示成功**：

```json
{"code": 0, "msg": "Success", "data": {"nodeId": 3, "clientId": "<uuid>", "token": "<64 hex>"}}
```

### 4.2 长连接

`GET /api/tunnel/connect`（WebSocket Upgrade），请求头 `X-GoPanel-Tunnel-Client: <clientId>`、`X-GoPanel-Tunnel-Token: <token>`。公网 GoPanel 必须使用 HTTPS（WSS）；明文 HTTP 只允许回环地址用于本地开发。

### 4.3 帧

```text
Type(1B) | StreamID(8B, BigEndian uint64) | Payload(≤ 4MB)
```

| Type | 名称 | 方向 | 载荷 |
| --- | --- | --- | --- |
| 1 | Open | GoPanel → Hoxi | JSON `serviceId, method, requestUri, headers, hasBody, upgrade` |
| 2 | Response | Hoxi → GoPanel | JSON `status, headers` |
| 3 | Data | 双向 | 首字节 `0`=HTTP 字节流，`1`/`2`=WS 文本/二进制 |
| 4 | End | 双向 | 空 |
| 5 | Reset | 双向 | 错误原因文本 |
| 6 / 7 | Ping / Pong | 双向 | 时间戳 |
| 8 | ServiceSync | Hoxi → GoPanel | 见 4.4 |
| 9 | Notify | Hoxi → GoPanel | 待处理提醒，StreamID 为 0，见第 11 节；旧版 GoPanel 会忽略 |

心跳 20 秒，读超时 65 秒，写超时 15 秒；断线后 1s 起指数退避至 30s 重连，连上后退避归零。GoPanel 每条请求流缓冲 32 帧、Hoxi 每条请求体缓冲 16 块，超出即 Reset（见第 8 节）。

### 4.4 服务注册

连接建立后 Hoxi 立即发送：

```json
{
  "hostname": "Mac-Studio.local",
  "version": "0.2.9",
  "services": [{
    "publicId": "hoxi-gateway",
    "name": "Hoxi Desktop Gateway",
    "scheme": "http",
    "targetHost": "127.0.0.1",
    "targetPort": 5410,
    "enabled": true
  }]
}
```

`publicId = "hoxi-gateway"` 是 GoPanel 识别 Hoxi 电脑的唯一依据。`targetPort` 是 Hoxi 本机桥接端口（release 5410、dev 5420），**仅用于展示**：Hoxi 在进程内处理请求，不会回连这个端口。

## 5. GoPanel 侧接口（`gopanel_v2`）

两条路由挂在 `/api/tunnel` 管理组，与其他管理接口一样要求管理员 JWT（`X-Auth`）。

### 5.1 `GET /api/tunnel/hoxi-nodes`

返回注册了启用状态 `hoxi-gateway` 服务的隧道节点；`status` 按隧道是否在线实时计算。

```json
{"code": 0, "msg": "Success", "data": {"items": [{
  "nodeId": 3, "name": "Studio", "hostname": "Mac-Studio.local", "version": "0.2.9",
  "status": "online", "serviceId": "hoxi-gateway", "targetPort": 5410,
  "lastSeenAt": "2026-10-21T10:25:00Z"
}]}}
```

### 5.2 `ALL /api/tunnel/hoxi-nodes/:nodeId/proxy/*`

把 `/<*>?<query>` 转发给该节点的 Hoxi 网关。

- **请求头白名单**：只透传 `Accept`、`Accept-Language`、`Cache-Control`、`Content-Type`、`Last-Event-ID`、`X-Hoxi-Directory`。`X-Auth`、`Authorization`、`Cookie`、`EntranceCode` 等 GoPanel 凭据不会离开 GoPanel；查询串经 `SanitizeNodeProxyQuery` 去掉 GoPanel 凭据参数。
- **响应**：状态码与响应头原样回传，但丢弃 `Set-Cookie`（Hoxi 不能在 GoPanel 域名下种 Cookie）与逐跳头；响应体每读到一块就写出并 Flush，SSE 实时到达手机。
- **错误**：隧道层错误使用 Hoxi 网关格式 `{"error": "..."}` 与真实 HTTP 状态码，便于手机端按 Hoxi 协议处理：
  - `503`：节点离线、节点未注册 `hoxi-gateway`、隧道入口未启动
  - `502`：转发途中失败
  - `501`：WebSocket Upgrade（暂不支持）
  - JWT 失效仍由 GoPanel 中间件返回其标准包装（HTTP 200，`code = 401`）
- 实现：`app/api/tunnel_hoxi.go`、`app/service/tunnel_hoxi.go`；转发经现有 `serveTunnelIngress`，没有复制帧处理逻辑。写操作照常记入 GoPanel 操作日志。

## 6. Hoxi 桌面侧（`hoxi`）

- **`internal/tunnel`**：配对、连接管理、帧协议与进程内分发。收到 Open 帧后构造请求：`Host` 改为 `127.0.0.1`，丢弃逐跳头、`Origin`、`Referer`、`Cookie`、`Authorization`、`X-Hoxi-Token`、`Sec-*`、`X-Forwarded-*`、`X-GoPanel-*`；`serviceId` 不是 `hoxi-gateway`、`upgrade = true` 或 URI 非法时直接 Reset。
- **路由白名单（`internal/gateway/tunnel.go`）**：采用显式「HTTP 方法 + 路径模式」逐段匹配（`*` 仅匹配单个非空路径段，**不做任何前缀匹配**），路径必须已是 `path.Clean` 规范形式且不得包含编码斜杠（`%2F`）。经隧道仅开放：
  - **基础与模型列表（只读）**：`GET /healthz`、`GET /api/hoxi/bootstrap`、`GET /api/hoxi/system-info`、`GET /api/hoxi/provider/models`（只读脱敏模型列表：仅 `{source, providers: [{id, name, models: [{id, name}]}]}`，不含密钥、`baseURL` 或请求头）、`GET /api/hoxi/runner/{antigravity,claude-code,codebuddy,qoder}/models`
  - **会话平面**：`GET /api/hoxi/sessions`、`POST /api/hoxi/sessions`、`GET /api/hoxi/sessions/status`、`GET /api/hoxi/sessions/*`、`GET /api/hoxi/sessions/*/message`、`GET /api/hoxi/sessions/*/diff`、`POST /api/hoxi/sessions/*/{prompt,prompt_async,abort,stop-tool,revert,unrevert,fork}`
  - **事件与审批平面**：`GET /api/hoxi/event`、`GET /api/hoxi/permission`、`POST /api/hoxi/permission/*/reply`（经隧道仅允许 `reply=once` 或 `reject`，携带 `always` 或非法 JSON 返回 403；审批审计 `/api/hoxi/permission/audit` 与会话级授权撤销 `/api/hoxi/permission/grants` 仅限桌面本机）
  - **Worktree 审查与提交**：`GET /api/hoxi/worktree/{status,diff,branches,verify-status}`、`POST /api/hoxi/worktree/commit`（请求体仅允许 `push=false`，携带 `push=true` 或非法 JSON 返回 403）、`POST /api/hoxi/worktree/apply`。命令执行与写配置/分支变更类接口（`verify-run`、`verify-config`、`verify-stop`、`push`、`checkout`、`setup-retry`、`prune-sandboxes`、`create`、`sync`、`revert-file`、`rename-branch`、`DELETE` 等）一律仅限桌面本机
  - **附件、Skill 与工作区（只读）**：`POST /api/hoxi/attachments`、`GET /api/hoxi/attachments/*`、`GET /api/hoxi/attachments/*/content`、`GET /api/hoxi/skills/list`、`GET /api/hoxi/workspaces`（新建/修改/删除工作区仅限桌面本机）
  - **工作区内文件读取与搜索（`internal/gateway/files.go`）**：`GET /file/content`、`GET /api/file/content`、`GET /api/hoxi/file/content`（均必须携带非空 `?path=`，绝不回退到 OpenCode 原始代理）、`GET /api/hoxi/file/list`、`GET /api/hoxi/file/search`。经隧道访问的目标路径与搜索根目录必须位于网关根目录、已注册工作区目录或会话绑定的 worktree 内（解析软链接防越界，不信任请求自带的越界 `directory` 参数，并拦截敏感凭据文件）；`/file`、`/api/file`、`POST /api/hoxi/file/open`、`POST /api/hoxi/file/validate` 一律 403

  其余所有路由（包括隧道与账号管理、`/api/hoxi/models-config` 及其它 `/api/hoxi/provider/*` 接口、Runner Hook 与旧版 `/api/hoxi/runner/*` 会话接口、浏览器、剪贴板、对话框、更新器以及 `/session`、`/pty`、`/config`、`/auth`、`/file` 等 OpenCode 原始透传）一律返回 403。
- **本机网关令牌不出机器**：网关在转发隧道请求前显式剥离 `X-Hoxi-Token` 且绝不注入本机令牌（受令牌保护的 OpenCode 原始透传对隧道始终不可达）；`/api/hoxi/bootstrap` 对隧道请求不返回 `gatewayToken`，也不种令牌 Cookie。
- **凭据文件**：release `~/.hoxi/tunnel.json`，dev `~/.hoxi/tunnel-dev.json`（两个变体可同时运行，互不挤占隧道）。临时文件 + `os.Rename` 原子写入，权限 `0600`，已加入受保护状态文件列表；状态接口从不返回 token。
- **管理接口**（只在本机可访问，隧道白名单不包含）：
  - `GET /api/hoxi/tunnel/status` → `{enabled, state: disabled|connecting|online|offline, controllerUrl, nodeId, name, message, connectedAt}`
  - `POST /api/hoxi/tunnel/pair` `{controllerUrl, code, name}`
  - `POST /api/hoxi/tunnel/disable`：断开并删除本机凭据
- 只有桌面主进程（含 `--no-desktop` 守护模式）启用隧道；CLI 临时拉起的 headless 网关没有凭据路径，配对会在兑换配对码**之前**被拒绝，避免在 GoPanel 留下孤儿节点。
- 桌面 UI：设置 → 系统 → 经 GoPanel 远程控制（`web/src/components/settings/RemoteTunnelControls.tsx`）。

## 7. 手机端（`client/`）

- **中继地址**：`https://<gopanel>/api/tunnel/hoxi-nodes/<nodeId>/proxy`，作为 `nodeType = 'hoxi'` 的 `ServerConnection.url` 保存；现有 Hoxi 适配器把 `/api/hoxi/...` 直接拼在后面，无需改业务代码。
- **凭据**：中继连接复用所属 GoPanel 的登录 JWT 与安全入口，由现有鉴权拦截器发送 `X-Auth` 与 `EntranceCode`。GoPanel 重新登录保存连接时，会同步刷新它名下所有中继连接的凭据。
- **地址规范化**：`normalizeServerAddress` 不再把中继路径当作安全入口剥离（`isHoxiRelayUrl`），App 每次启动时的迁移也会保留中继路径。
- **入口**：服务器列表 → GoPanel 卡片的电脑图标 → `HoxiRelayNodesSheet`；离线电脑不可选。
- 代码：`lib/shared/models/server_connection.dart`、`lib/features/server/data/hoxi_relay_repository.dart`、`lib/features/server/presentation/widgets/hoxi_relay_nodes_sheet.dart`。

## 8. 安全模型与已知限制

- **信任边界**：配对即授权。能以管理员身份登录这台 GoPanel 的人，可以在白名单范围内远程操作这台 Mac（新建会话、下发指令、批准审批）。Hoxi 自身的破坏性命令审批门控照常生效，但审批本身也能经手机远程批准。只与自己控制的 GoPanel 配对。
- **凭据**：GoPanel JWT 只在手机与 GoPanel 之间；隧道 token 只在 Mac 与 GoPanel 之间，以 `0600` 明文文件保存在 Mac 上（尚未接入钥匙串）；Hoxi 本机网关令牌不离开 Mac。
- **无端到端加密**：GoPanel 能看到经它转发的请求和响应明文（会话内容、Diff）。传输加密依赖手机 ↔ GoPanel、Mac ↔ GoPanel 两段 TLS。
- **无流控**：手机读得慢时 GoPanel 单流 32 帧缓冲会溢出并 Reset 该流（SSE 断开后由手机端重连）；大文件下载不适合走隧道。
- **单连接**：同一凭据只保留最新的一条隧道，旧连接会被替换。
- **JWT 有效期**：GoPanel 管理员登录默认 72 小时，过期后需在手机重新登录 GoPanel（中继连接随之刷新）。

## 9. 尚未实现

- 经隧道转发 WebSocket（手机远程终端）。GoPanel 侧已支持，Hoxi 侧需要回连本机端口或实现进程内 Hijack。
- 同局域网直连：Hoxi 额外监听 `5417`（受令牌保护），手机按「局域网探测 → 失败回落中继」双路由。`5417` 端口号保留给该功能，当前版本不监听。
- 隧道 token 存入 macOS 钥匙串；Android 推送；节点身份与传输地址分离后的自动切换。

## 10. 验证

| 范围 | 命令 / 测试 |
| --- | --- |
| Hoxi 隧道客户端 | `go test ./internal/tunnel/`（配对、`0600` 凭据、ServiceSync、请求转发、凭据头剥离、Reset 场景、解除配对） |
| Hoxi 路由白名单 | `go test ./internal/gateway/ -run Tunnel` |
| GoPanel 代理 | `go test ./app/service/ -run HoxiTunnel`（SSE 逐块到达、GoPanel 凭据不泄漏、丢弃 Set-Cookie、未注册服务拒绝） |
| 手机中继连接 | `flutter test test/hoxi_relay_test.dart` |
| 双仓互通 | 用临时 `go.work` 同时引入两个模块，让真实 GoPanel `HandleTunnelConnection` 与真实 Hoxi `tunnel.Manager` 对接：SSE 首块约 2ms 到达、200KB 请求体完整。该测试不入库，修改帧协议时需重跑 |

## 11. 审批提醒推送

Agent 等待审批或向用户提问时，iPhone 收到通知；点开后 App 连接对应的 Hoxi 电脑、打开语音通话，并由助手读出待处理事项，用户用语音回答，经意图识别与确认卡片执行。

```text
Hoxi statusbar.Tracker（OpenCode permission/question.asked 与 Runner PreToolUse 审批）
  → 新增待处理项只通知一次 → tunnel.Manager.Notify → Notify 帧（类型 9）
  → GoPanel HandleHoxiNotice（仅限注册了 hoxi-gateway 的节点，按 requestId 去重 10 分钟）
  → APNs（.p8 token 认证，HTTP/2，collapse-id = hoxi-node-<nodeId>）→ 已注册的 iPhone
  → 点开：按 panel + nodeId 找到中继连接 → 切换节点 → 打开语音通话 → 自动询问“有哪些操作在等我审批”
```

- **内容最小化**：Notify 载荷只有 `kind`（approval / question）、`requestId`、`sessionId`、`permission`、`pending`；推送正文只说“有 N 项操作等待审批”，**不含命令、代码或会话内容**（这些会经过 Apple 并显示在锁屏上）。细节由手机点开后经隧道读取
- **设备注册**：手机在存在中继节点时申请通知权限，并向对应 GoPanel `POST /api/push/devices` 注册 `{platform: ios, token, environment, name, panelUrl}`（管理员 JWT）；`environment` 由构建配置决定（Debug / Profile 为 sandbox，Release 为 production，与 `aps-environment` 授权一致）；同一 token 重复注册即更新
- **APNs 配置**：GoPanel 后台 → 节点 → 手机推送，填写 Team ID、Key ID、Bundle ID（默认 `ai.hoxi.app`）与 `.p8` 内容，可发送测试推送。接口：`GET/POST /api/push/apns`、`POST /api/push/test`。`.p8` 加密存储且不经接口返回
- **失效设备**：APNs 返回 410 / BadDeviceToken / DeviceTokenNotForTopic 时自动停用该设备
- **限制**：离线期间产生的提醒不排队（重新打开 App 仍能看到待处理项）；OpenCode 运行时的审批依赖网关 SSE 旁路观测，桌面端没有任何客户端订阅事件流时可能漏报；未做真机验收

验证：Hoxi `go test ./internal/statusbar/ ./internal/tunnel/`（只通知一次、不含命令文本、Notify 帧类型固定为 9）；GoPanel `go test ./app/service/ -run 'APNs|HoxiNotice|TunnelNotify|RegisterPushDevice'`（ES256 JWT 与请求头、经真实隧道读循环触发推送、去重、停用失效设备、通用正文）；手机 `flutter test test/push_notification_test.dart`，iOS 模拟器构建通过。

尚未做真机联调：真实公网 GoPanel + 真实 Mac + 手机 4G 下的端到端验收、长时间 SSE 稳定性与重连表现。
