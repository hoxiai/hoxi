# Hoxi 应用联动与能力开放协议（`hoxi-app/v1`）

`hoxi-app/v1` 是 Hoxi **桌面本机**应用协议，由三层组成：系统深链、回环 HTTP 能力 API、Host MCP。它既不是远程配对协议，也不是 `/v1/*` 本机中转 Key 协议。应用凭据不授予桌面管理、其他应用身份、提供商密钥、会话文件、命令或桌面输入权限。

## 1. 系统深链

用标准 URL 编码构造参数，整条链接最多 8192 字节。以下链接由系统交给 Hoxi 桌面，不是 HTTP 路由。

| 链接 | 参数 | 桌面行为 |
| --- | --- | --- |
| `hoxi://session/<sessionId>` | `message` 可选 | 定位已有会话与消息 |
| `hoxi://app/<appId>[/<tab>]` | `store`、`item` 可选保留 | 联编 Work Surface 导航到已安装应用及存在的主标签；不会安装或执行应用，`store`、`item` 暂不切换账号/条目 |
| `hoxi://run` | `prompt`（最多 4096 字节）或 `skill`，可选 `app`、`directory`；`mode=draft\|confirm` | 仅在当前 Code 工作区确认后填入未发送的草稿；`skill` 作为待审阅文本，不自动启用技能。拒绝 `mode=silent`，不自动切项目或发送，不改变审批策略 |
| `hoxi://browser/open` | `url` 为 HTTP(S) 且无用户信息；`profile` 可选；`mode=readonly\|interactive` | 确认后在当前浏览器检视器导航；参数不授予新 profile、控制权或免审批 |
| `hoxi://doc/preview` | `path` 为绝对路径 | 确认后预览当前工作区文档，仍经文件访问门控；`.env`、凭据和受保护状态路径不接受 |
| `hoxi://settings/<section>` | `providers`、`media-roles`、`app-grants`、`approval`、`general` | 打开相应设置 |
| `hoxi://connect` | 重定向参数见 §2.2，或仅 `requestId` | 新建待授权连接，或打开已有轮询请求的授权页面；不是批准 |

`appId` 以 ASCII 字母或数字开头，其余可用 ASCII 字母、数字、`.`、`_`、`-`，长度 1–64，鉴权时大小写敏感。应用显示名只是申请者自报的文字，不是可信身份凭证。只批准自己发起的请求。深链导航不会调用 Agent、Shell 或原生输入工具。

## 2. 本机 HTTP

### 2.1 发现与信任边界

1. 读取 `$HOXI_HOME/runtime-endpoint.json`（默认 `~/.hoxi/runtime-endpoint.json`）。它只包含 `protocol`、`baseURL`（实际绑定端口，含回退端口）、`pid`、`startedAt`（Unix 毫秒），不含密钥或工作区数据。桌面原子写入 `0600`，正常退出按 PID 清理。文件存在不等于进程仍在运行。
2. 使用不走 HTTP 代理的回环客户端，对文件中 `http://<回环 IP>:<端口>` 请求 `GET /api/hoxi/cap/v1/discovery`，核对 `protocol: "hoxi-app/v1"`。响应还含 `version`、`baseURL`、`capabilityPath`、`mcpPath`、`mcpVersion`、Scope 词表 `scopes` 和已实现能力的路径到 Scope 映射 `routes`。不要从 `Host`、外部重定向或远程控制接口推导地址；文件失效时启动桌面再发现。
3. 只接受回环 TCP 和受信 `Host`。浏览器 CORS 只允许明确的回环 HTTP(S) Origin（`localhost`、回环 IP），不接受 `null`、公网 Origin 或任意 Origin 通配，也不接受 Cookie 鉴权。GoPanel、局域网配对、Cloudflare/反代入口及远程开发宿主不可调用这三组路由，返回 `403 REMOTE_FORBIDDEN`。不要自行将回环端口反代到外部。

### 2.2 PKCE S256 握手

每次生成新的高熵 `codeVerifier`（43–128 个 RFC 7636 unreserved 字符）、`codeChallenge = BASE64URL_NO_PADDING(SHA256(codeVerifier))` 及独立的随机 `state`。`codeChallengeMethod` 使用 `S256`；不支持 `plain`。Verifier 留在应用进程，不写入深链和日志。待审连接有效 120 秒；超时或拒绝不签发令牌。下文 HTTP 路径相对于 `/api/hoxi/cap/v1`，POST JSON 用 `Content-Type: application/json`。

**原生应用（重定向）**

1. 打开 `hoxi://connect?appId=…&name=…&scopes=llm%3Amodels%2Cnotify%3Asend&redirectUri=…&codeChallenge=…&codeChallengeMethod=S256&state=…`。
2. 回调 URI 最多 1024 字节，使用带显式端口的回环 IP HTTP（如 `http://127.0.0.1:3211/cb` 或 `http://[::1]:3211/cb`），或应用注册的反域名自定义 scheme（如 `com.example.demo://callback`）。不允许用户信息、fragment、公网 HTTP(S) 或 `javascript:`、`file:`、`hoxi:` 等保留 scheme。使用专用回调而非执行动作的 URL。桌面批准后，通过操作系统返回一次性 `hxc_*` `code`、原 `state` 和 `baseURL`；拒绝返回 `error=access_denied` 和 `state`。应用须校验 `state` 和回环地址。
3. `POST /auth/token`：`{"code":"hxc_…","codeVerifier":"…","redirectUri":"原始 URI"}`。代码有效 60 秒，校验 verifier 与完全一致的 URI 后只兑换一次。成功返回 `{"status":"approved","grant":{…},"token":"hk_app_…"}`；无效证明不能换票。若响应丢失，重新发起授权，不假定可重放。

**CLI（轮询）**

1. `POST /auth/poll-init`：`{"appId":"com.example.demo","name":"Demo","scopes":["llm:models"],"codeChallenge":"…","codeChallengeMethod":"S256"}`。`202` 返回 `requestId`、`status=pending`、`expiresAt` 和 `openURL`（`hoxi://connect?requestId=…`）。客户端可交给系统打开该链接以唤起桌面。
2. 每隔约 1–2 秒 `POST /auth/poll-token`：`{"requestId":"con_…","codeVerifier":"…"}`。`200` 的 `status` 是 `pending`、`approved`、`denied` 或 `expired`；只有 `approved` 携带 `grant` 和 `token`。同一请求不会签发第二个令牌。

保存令牌到应用自己的系统凭据库；不要放进 URL、Work manifest、浏览器 `localStorage`、日志或模型上下文。桌面端只把令牌的 SHA-256 摘要和授权元数据写到 `app-grants.json`；损坏保留原文件及私有备份并拒绝使用。`lastUsedAt` 最多每分钟更新一次。内部可信调用者可签发有 TTL（1 秒至 24 小时）的 `hk_app_ephemeral_*`，仅在当前宿主内存有效；本协议没有对外临时令牌签发接口。授权被撤回或收窄后，新调用立即按最新权限判定，不承诺撤回已交给上游的请求。

### 2.3 能力分级与 API

以下已实现接口都需要 `Authorization: Bearer hk_app_…` **及** `X-Hoxi-App-Id: <原始 appId>`。不接受通配 Scope。`discovery.scopes` 是 17 项词表，不等同于全部已实现；以 `routes`/`tools/list` 为准。

| 级别 | Scope | HTTP / MCP | 边界 |
| --- | --- | --- | --- |
| A：显式授权 | `llm:models` | `GET /llm/models` / `llm_models` | OpenAI 模型列表 |
| A | `llm:chat` | `POST /llm/chat/completions` / `llm_chat` | OpenAI Chat，REST 可 `stream:true`；只用已配置服务商，不发送应用令牌给上游 |
| A | `media:capabilities` | `GET /media/capabilities` / `media_capabilities` | 查看已配置的媒体能力和角色模型 |
| A | `media:image` | `POST /media/image` / `media_image` | JSON 传 `prompt`，可选 `providerID`、`modelID`、`aspectRatio`、`imageSize`、`count`，返回 `images[]` |
| A | `media:video` | `POST /media/video` / `media_video` | JSON 传 `prompt`，可选 `providerID`、`modelID`、`aspectRatio`、`resolution`、`durationSeconds`，返回 `videos[]` |
| A | `media:speech` | `POST /media/speech` / `media_speech` | JSON 传 `text`，可选 `providerID`、`modelID`、`voice`、`format`、`speed`，返回 `audios[]` |
| A | `media:transcribe` | `POST /media/transcribe` / `media_transcribe` | JSON `dataUrl`（可选 `filename/mime/language/prompt/providerID/modelID`），或 multipart `file`；原始音频最多 25 MiB，返回 `text` |
| A | `notify:send` | `POST /notify` / `notify` | `{title,body?}`；标题加 `[AppName]`，同一 `appId` 每 60 秒滑窗 10 次，不附带会话导航 |
| A | `approval:request` | `POST /approval/ask`、`GET /approval/result?approvalId=…` / `approval_ask` | 仅允许请求桌面的一次性业务决定，不授予命令、文件或设备控制 |
| A（保留） | `doc:read`、`doc:write`、`tools:read`、`tools:run`、`recall:read` | 未开放 HTTP/MCP | 声明 Scope 不能获得文件、历史或通用外部工具访问 |
| B（保留） | `agent:run`、`browser:control`、`computer:control` | 未开放 HTTP/MCP | 将来仍要通过原有 Runner、浏览器或原生桌面审批，Scope 本身绝非执行许可 |
| C（禁止） | 无 Scope | 不对应用开放 | Shell/PTY、裸文件访问、敏感路径、提供商/账号密钥、执行配置与管理 API 无外部授权 |

媒体生成产物以 `{mime,data}` 返回，`data` 是标准 Base64。应用 API 禁用 `directory`、`sessionID`、`inputPaths`、`outputPath`；不会读写宿主工作区或拼接桌面会话。应用可自行保存返回的字节。模型及媒体调用将请求数据送到用户配置的服务商，可能计费。Hoxi 不向应用签发上游凭据。

`POST /approval/ask` 接受 `title`、`summary?`、`riskLevel?`（`low|medium|high`，默认 `medium`）、`details?`（字符串键值）、`async?`。

- 默认同步：等待桌面明确「允许一次」或「拒绝」，`200` 返回 `status=approved|denied` 及 `decisionId`。到 600 秒仍无决定返回 `504 APPROVAL_EXPIRED`；连接取消时不继续等待或批准。
- `async:true`：立即 `202` 和 `status=pending`；用原**同一授权**回查 `approvalId` 的结果，到期后 `404 NOT_FOUND`。其他令牌即使 `appId` 相同也不能读取。任何未知状态都视为未批准。

### 2.4 桌面管理

只接受受信任的进程内桌面请求和本次启动的网关凭据。普通回环 HTTP、应用令牌和远程控制器均不能读写审批或授权。桌面设置 → 应用授权可查看来源、勾选 Scope、批准/拒绝及吊销。

| 方法与路径 | 请求与结果 |
| --- | --- |
| `GET /api/hoxi/app-grants` | 授权元数据列表，不含令牌或哈希 |
| `PATCH /api/hoxi/app-grants/{id}` | `scopes` 和/或 `allowedWorkspaces`；只准收窄，扩大需新授权 |
| `DELETE /api/hoxi/app-grants/{id}` | 撤销，成功 `204` |
| `GET /api/hoxi/app-grants/pending` | 待连接请求列表 |
| `POST /api/hoxi/app-grants/pending/{requestId}/decision` | `approve` 必填；可选非空 `scopes` 子集（省略表示全部请求项）、`allowedWorkspaces`；桌面交付回调 |
| `GET /api/hoxi/app-grants/approvals` | 待一次性审批列表 |
| `POST /api/hoxi/app-grants/approvals/{approvalId}/decision` | `{approve: true\|false}`，按对应请求一次性决定 |

`allowedWorkspaces` 仅保留明确、非受保护的绝对路径元数据，不赋予 v1 应用媒体 API 的文件访问权。所有时间戳均为 Unix 毫秒。

## 3. Host MCP

`POST /api/hoxi/mcp` 使用 MCP `2025-03-26` Streamable HTTP、JSON-RPC 2.0 和同样的应用鉴权。客户端发送 `Content-Type: application/json`、`Accept: application/json, text/event-stream`，后续可带 `MCP-Protocol-Version: 2025-03-26`。

| 方法 | 结果 |
| --- | --- |
| `initialize` | `protocolVersion`、`serverInfo` 以及 `tools`、`resources` 能力 |
| `notifications/initialized` | `202` 无正文；通知不能执行工具 |
| `ping` | 空 `result` 对象 |
| `tools/list` | 按当前 Scope 裁剪，含 `name`、`description`、`inputSchema` |
| `tools/call` | `params:{name,arguments?}`；再次校验授权，`content[0].text` 是结果 JSON 文本，失败置 `isError=true` |
| `resources/list`、`resources/read` | 唯一资源 `hoxi://protocol/discovery`，`mimeType:application/json`；不是文件读取接口 |

单次 RPC 可返回 JSON 或一条 SSE `message`，完成后关闭。批量最多 16 项，通过 SSE 逐条返回结果；纯通知返回 `202`。不分配 `Mcp-Session-Id`，不提供服务器主动推送、续传或独立 GET 流（GET 返回 `405`）。MCP Chat 固定非流式；超出单工具响应 8 MiB 时请改用 HTTP 能力 API，不自动重放已执行的操作。JSON-RPC 错误沿用 `-32700`（语法）、`-32600`（请求）、`-32601`（方法）、`-32602`（参数）和 `-32002`（资源不存在）；鉴权或传输失败仍是 HTTP 错误。

## 4. 错误约定

协议拒绝使用 HTTP 状态和 `{"error":{"code":"SCOPE_MISSING","message":"required scope: llm:chat"}}`。消息供展示；程序按 code/状态分流。OpenAI Chat 保留兼容的模型请求错误格式；已有媒体上传的参数错误可携带字符串 `error`，客户端用 HTTP 状态兜底。

| HTTP | `error.code` | 处理 |
| --- | --- | --- |
| 400/413/415 | `INVALID_REQUEST` | 校验方法、JSON、大小、字段或媒体类型 |
| 400 | `INVALID_PROOF` | 授权码、PKCE 或回调 URI 无效；重新授权 |
| 401 | `INVALID_TOKEN` | 缺少凭证、应用身份不匹配、过期或已撤销 |
| 403 | `REMOTE_FORBIDDEN` / `UNTRUSTED_HOST` / `UNTRUSTED_ORIGIN` | 只用桌面本机受信连接 |
| 403 | `SCOPE_MISSING` / `DESKTOP_REQUIRED` / `FILES_FORBIDDEN` / `APPROVAL_CANCELLED` | Scope 不足、非桌面管理通道、尝试访问宿主文件/会话或待审请求已取消 |
| 404 | `NOT_FOUND` | 路由、授权或回查记录不存在/失效 |
| 405 | `METHOD_NOT_ALLOWED` | 按接口要求选择方法 |
| 429 | `RATE_LIMITED` | 按 `Retry-After` 或退避重试，不紧密轮询 |
| 502/503 | `UPSTREAM_ERROR` / `UNAVAILABLE` / `CALLBACK_FAILED` | 上游失败、宿主/配置不可用或系统回调未打开 |
| 504 | `APPROVAL_EXPIRED` | 没有批准，不执行原操作 |

## 5. `work/packages/*` 接入规范

- 在 `work-package.json` 的 `permissions` 逐项声明 `host:<scope>`，例如 `"permissions":["host:media:image"]`。校验器拒绝 `host:*`、`host:auth:admin` 和未列入词表的 Scope。manifest 权限是需求声明，不是授权、更不是自动签发凭据。
- 使用 manifest 的 `id` 作为 `appId`。外部 CLI/本机后端通过 §2 的发现与 PKCE 流程请求声明范围内的最小授权；不要把 Bearer 送入沙箱 iframe、渲染模板、静态包或上传日志。`Origin:null` 的 Work 沙箱页面不能直接访问该协议。
- 声明 `host:<scope>` 的包在本地服务进程中可读 `HOXI_CAP_URL`（已含 `/api/hoxi/cap/v1`）、`HOXI_MCP_URL`、`HOXI_CAP_APP_ID`。HTTP 和 stdio 均**不注入 `HOXI_CAP_TOKEN`**，不因安装、升级或同名应用曾获批而签票。外部自报 `appId` 不是受管包身份，宿主不按它查询或继承授权。
- 用户主动连接时，服务按 §2.2 发起 `poll-init`，桌面设置 → 应用授权可勾选 Scope、同意或拒绝；仅服务端以 verifier 轮询 `poll-token`，**仅在进程内存持票**。向 iframe 仅返回操作结果/连接状态；绝不返回轮询响应、verifier 或 Bearer。进程重启、要求新增 Scope 或撤销后，须重新主动连接。§2.4 的收窄、撤销对下一次能力请求生效，不自动重授权。`host:approval:request` 不替代 PreToolUse、浏览器/电脑或文件访问门控。
- **与 Work 沙箱宿主桥接（`window.hoxiWork`）的分层**：本协议（`hoxi-app/v1`）用于外部应用与本地服务后端向 Hoxi 申请受控宿主能力（`browser:control` 仍为 B 级保留项，未对外开放）；受管 Work 应用包沙箱页面的 `window.hoxiWork.browser`（`state`/`open`/`evaluate`）走桌面内建 RPC 网关，严格限定于当前已安装包的 `browser:store-isolated` 权限、`browserPolicy.allowedHosts` 域白名单与对应店铺隔离窗口，不暴露域外页面、宿主凭据或通用浏览器控制权。
