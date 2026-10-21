# Hoxi ⇄ GoPanel 远程通信与反向加密隧道架构规范（端口 `5417`）

> **文档状态**：已定稿（与 `gopanel_v2/docs/hoxi-remote-tunnel.md` 双仓同步）  
> **核心目标**：在 **100% 保留 GoPanel 原有全部服务器运营能力** 的前提下，复用 GoPanel 成熟的二进制多路复用隧道底座（`app/service/tunnel_*.go`），让手机端（`client/`）无论在局域网还是外网 4G/5G 下，都能通过语音或聊天实时了解业务信息并远程控制家里/办公室的 **Hoxi 本地电脑（默认受控端口 `5417`）**。

---

## 1. 架构拓扑与设计铁律

```text
┌──────────────────────────────────────────────────────────────────────────────────┐
│                     Hoxi 手机 App (`client/` — 沟通 · 工程 · 专栏 · 我的)          │
│   • 语音/聊天查 6 大业务情报 (ChatInformationEngine)                              │
│   • 语音/聊天直控 GoPanel 服务器 & Hoxi 本地电脑 (ChatActionExecutor)              │
└───────────────────┬──────────────────────────────────────────┬───────────────────┘
                    │                                          │
   [路径 A：同 Wi-Fi / Tailscale 局域网直连]      [路径 B：外网 4G/5G 经 GoPanel 公网中继]
   HTTP/SSE/WS -> http://<Mac-LAN-IP>:5417       HTTPS/WSS -> https://<gopanel-domain>
   Header: X-Hoxi-Token: <PairingToken>          Header: Authorization: Bearer <GoPanel-JWT>
                    │                                          │
                    │                                          ▼
                    │               ┌──────────────────────────────────────────────┐
                    │               │       GoPanel 公网云服务器 (`gopanel_v2`)      │
                    │               │  1. 原有服务器运营能力 100% 直达 (/api/*)：    │
                    │               │     主机监控 / 容器 / 网站 / SSL / 数据库 /   │
                    │               │     应用商店 / 文件 / 防火墙 / 任务 / 远端Code│
                    │               │  2. Hoxi 隧道透明代理网关：                   │
                    │               │     • GET /api/tunnel/hoxi-nodes             │
                    │               │     • ALL /api/tunnel/hoxi-nodes/:id/proxy/* │
                    │               └──────────────────────┬───────────────────────┘
                    │                                      │
                    │                        [WSS /api/tunnel/connect]
                    │                   (9-Byte 二进制多路复用帧 · 穿透家庭 NAT)
                    │                   Header: X-GoPanel-Tunnel-Client / Token
                    │                                      │
                    ▼                                      ▼
┌──────────────────────────────────────────────────────────────────────────────────┐
│                  家里 / 办公室的 Hoxi 本地电脑 (`hoxi` — macOS)                    │
│   • 远程受控桥接端口：127.0.0.1:5417（开启局域网配对时兼听 0.0.0.0:5417）          │
│   • 内置隧道客户端 (`internal/tunnel`)：主动外连 GoPanel 并同步 127.0.0.1:5417     │
│   • 统一网关平面：/api/hoxi/sessions · /api/hoxi/event (SSE) · /api/hoxi/permission│
└──────────────────────────────────────────────────────────────────────────────────┘
```

### 三大设计铁律
1. **GoPanel 原有运营能力零损保留**：
   - GoPanel 作为线上 Linux 服务器管理面板的全部核心能力（系统监控、Docker/Podman 容器、Nginx 网站与流水线、SSL 证书、MySQL/PG/Redis 数据库、应用商店、文件/进程/防火墙资源管理、任务中心、以及服务器本机 `/api/code/*` 编码工作区）保持 **100% 原生可用**。
   - Hoxi 远程通信作为增量能力挂载在 GoPanel 现有的 `/api/tunnel/*` 命名空间下，不侵入 GoPanel 任何既有业务模块。
2. **端口统一约定为 `5417`**：
   - Hoxi 端口谱系：
     - `5410`：Hoxi Desktop Release 本地回环端口（`DefaultReleaseBridgeAddr = "127.0.0.1:5410"`）
     - **`5417`**：**Hoxi 远程控制与 GoPanel 隧道桥接专用端口（`DefaultRemoteBridgePort = 5417`）**
     - `5420`：Hoxi Desktop Dev 本地回环端口（`DefaultDevBridgeAddr = "127.0.0.1:5420"`）
   - Hoxi 本地电脑向 GoPanel 隧道注册本机服务时，固定声明 `TargetHost: "127.0.0.1", TargetPort: 5417`。
3. **100% 复用 GoPanel 现有二进制隧道帧协议**：
   - 严禁发明第二套不兼容的隧道协议。Hoxi 桌面端直接实现与 `gopanel_v2/app/service/tunnel_protocol.go` 完全一致的二进制帧协议，天然支持 **HTTP REST、SSE (`text/event-stream` 零缓冲 Flush) 与 WebSocket (`/pty` 终端双向流)**。

---

## 2. 线缆协议契约（与 `gopanel_v2` 100% 对齐）

### 2.1 配对码签发与长期凭据交换
1. **签发一次性配对码（GoPanel 端已有）**：
   - 路由：`POST /api/tunnel/pairing`（需 GoPanel Admin JWT）
   - 响应：`{"code": "<32-char-hex>", "expiresAt": "2026-10-21T10:35:00Z"}`（10 分钟有效，一次性消费）。
2. **Hoxi 本地电脑交换长期隧道凭据（GoPanel 端已有）**：
   - 路由：`POST /api/tunnel/pair/exchange`
   - 请求体：
     ```json
     {
       "code": "<32-char-hex>",
       "name": "Hugh's Mac Studio (Hoxi)",
       "hostname": "Mac-Studio.local",
       "version": "hoxi-0.2.9"
     }
     ```
   - 响应体：
     ```json
     {
       "nodeId": 3,
       "clientId": "550e8400-e29b-41d4-a716-446655440000",
       "token": "<64-char-hex-secret>"
     }
     ```
   - Hoxi 桌面端将 `{controllerUrl, nodeId, clientId, token}` 原子加密落盘至 `~/.hoxi/tunnel.json`。

### 2.2 出站 WebSocket 隧道连接与 `5417` 服务注册
1. **建立长连接（GoPanel 端已有）**：
   - 路由：`WSS /api/tunnel/connect`
   - 认证请求头：
     - `X-GoPanel-Tunnel-Client: <clientId>`
     - `X-GoPanel-Tunnel-Token: <token>`
2. **二进制帧格式（9 字节定长头 + 载荷）**：
   ```text
   +----------+------------------------+-------------------+
   | Type(1B) | StreamID (8B, uint64)  | Payload (0..4MB)  |
   +----------+------------------------+-------------------+
   ```
   - `0x01` `tunnelFrameOpen`：GoPanel 打开一条新请求流（JSON：`serviceId`, `method`, `requestUri`, `headers`, `hasBody`, `upgrade`）
   - `0x02` `tunnelFrameResponse`：Hoxi 返回 HTTP 状态码与响应头（JSON：`status`, `headers`）
   - `0x03` `tunnelFrameData`：流式数据块（首字节 `0x00`=HTTP/SSE 字节流，`0x01`=WS Text，`0x02`=WS Binary）
   - `0x04` `tunnelFrameEnd`：流正常结束
   - `0x05` `tunnelFrameReset`：流异常重置
   - `0x06` `tunnelFramePing` / `0x07` `tunnelFramePong`：20 秒心跳保活（65 秒读超时，断线 1s~30s 指数退避自动重连）
   - `0x08` `tunnelFrameServiceSync`：连接成功后 Hoxi 立即上报本机 `5417` 受控网关服务：
     ```json
     {
       "hostname": "Mac-Studio.local",
       "version": "hoxi-0.2.9",
       "services": [
         {
           "publicId": "hoxi-gateway-5417",
           "name": "Hoxi Desktop Gateway",
           "scheme": "http",
           "targetHost": "127.0.0.1",
           "targetPort": 5417,
           "enabled": true
         }
       ]
     }
     ```

---

## 3. GoPanel 侧增量接口规划（`gopanel_v2`）

GoPanel 现有的 `serveTunnelIngress` 仅面向本机 Caddy 网站反代。为了让 **Hoxi 手机 App (`client/`)** 直接通过 GoPanel 远程控制已连入的 **Hoxi 本地电脑（端口 `5417`）**，在 `gopanel_v2/app/router/tunnel.go` 与 `app/api/tunnel.go` 中增量暴露以下 2 个受 JWT 保护的接口：

### 3.1 查询在线的 Hoxi 本地电脑节点列表
- **路由**：`GET /api/tunnel/hoxi-nodes`（需登录 JWT）
- **响应示例**：
  ```json
  {
    "code": 200,
    "msg": "success",
    "data": {
      "items": [
        {
          "nodeId": 3,
          "name": "Hugh's Mac Studio (Hoxi)",
          "hostname": "Mac-Studio.local",
          "version": "hoxi-0.2.9",
          "status": "online",
          "serviceId": "hoxi-gateway-5417",
          "targetPort": 5417,
          "lastSeenAt": "2026-10-21T10:25:00Z"
        }
      ]
    }
  }
  ```

### 3.2 透明代理转发至 Hoxi 本地电脑 `5417` 端口
- **路由**：`ALL /api/tunnel/hoxi-nodes/:nodeId/proxy/*`（支持 `GET`, `POST`, `PUT`, `DELETE`、SSE 流及 WebSocket Upgrade）
- **转发逻辑**：
  1. 校验当前请求的 GoPanel 登录凭证（JWT 或 API Token）。
  2. 查找 `nodeId` 下 `targetPort == 5417`（或 `publicId == "hoxi-gateway-5417"`）的 `TunnelService` 与在线 `tunnelGatewayPeer`。
  3. 剥离前缀 `/api/tunnel/hoxi-nodes/:nodeId/proxy`，将剩余路径与 Query（例如 `/api/hoxi/sessions?directory=...` 或 `/api/hoxi/event?directory=...`）直接交由 `serveTunnelHTTP` / `serveTunnelWebSocket` 通过隧道转发至 Hoxi 本地电脑的 `127.0.0.1:5417`！
  4. 对于 `Content-Type: text/event-stream`（如 `/api/hoxi/event`），每收到一帧 `tunnelFrameData` 立即调用 `flusher.Flush()`，确保手机端 120Hz SSE 实时流与语音播报零缓冲延迟。

---

## 4. Hoxi 桌面端侧增量规划（`hoxi`）

1. **监听 `5417` 受控桥接端口（`internal/hoxipaths` & `internal/app`）**：
   - 在 `internal/hoxipaths/hoxipaths.go` 定义 `DefaultRemoteBridgePort = 5417` 与 `DefaultRemoteBridgeAddr = "127.0.0.1:5417"`。
   - 当用户在桌面端开启「允许局域网手机直连」时，额外在 `0.0.0.0:5417` 启动受 `X-Hoxi-Token` 严格保护的监听器；仅开启「GoPanel 隧道远程控制」时，仅需监听 `127.0.0.1:5417`，零本地端口暴露。
2. **移动端令牌准入守卫（`internal/gateway/security.go`）**：
   - 未携带有效 `X-Hoxi-Token` 的请求继续严格执行 `trustedHost(r.Host)`（仅限 `127.0.0.1` / `localhost` / `wails`）。
   - 携带有效 `X-Hoxi-Token`（ConstantTimeCompare 校验通过）的请求，放行局域网 Host（`192.168.x.x:5417` / `100.x.x.x:5417`）与隧道转发请求。
3. **内置 GoPanel 隧道客户端（`internal/tunnel/`）**：
   - 提供桌面端 REST 接口：
     - `GET /api/hoxi/tunnel/status`：查看当前与 GoPanel 的隧道连接状态、延时与局域网 `:5417` 配对二维码信息。
     - `POST /api/hoxi/tunnel/pair`：传入 `{controllerUrl, code, name}`，一键完成与 GoPanel 的配对并自动连接 `WSS /api/tunnel/connect`，同步 `127.0.0.1:5417` 服务。
     - `POST /api/hoxi/tunnel/disable`：断开并停用远程隧道。

---

## 5. 手机端 (`client/`) 双模路由与全能力协同规划

1. **GoPanel 原有运营能力 100% 保持直通**：
   - 在 Tab 1 `工程 -> 主机与容器` 及「沟通」页语音指令中，所有面向 GoPanel 的请求继续直连 `https://<gopanel-url>/api/...`（主机监控、Docker 容器、网站、SSL、数据库、应用商店、文件/进程/防火墙、任务中心）。
2. **Hoxi 本地电脑「局域网 `:5417` + GoPanel 隧道」智能双路由**：
   - `ServerConnection`（`nodeType == 'hoxi'`）支持同时保存：
     - `lanUrl`: `http://<mac-lan-ip>:5417`
     - `relayUrl`: `https://<gopanel-url>/api/tunnel/hoxi-nodes/<nodeId>/proxy`
   - 手机端访问 Hoxi 本地电脑时：
     - 若处于同一 Wi-Fi 且 `lanUrl`（`:5417`）健康探测在 `180ms` 内响应，优先走局域网 `:5417` 极速直连；
     - 若人在外网（4G/5G）或局域网不可达，**自动无感切换至 `relayUrl`（经 GoPanel 隧道穿透至家里 Mac 的 `127.0.0.1:5417`）**。
   - 无论走局域网 `:5417` 还是走 GoPanel 隧道，手机端「沟通」页的 **1:1 灵光实时语音通话**、**6 大业务情报查询 (`ChatInformationEngine`)** 与 **12 类声控直达动作 (`ChatActionExecutor`)** 均完全透明运行。
