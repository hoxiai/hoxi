# Hoxi 远程可达性与端到端加密契约（草案）

> 状态：**草案 / 规划**。除「现状」一节外，本文描述的都是**尚未实现**的目标形态，不能当作现有能力引用或宣传。
> 参照 `sailry-harness`（AGPL-3.0-only）的架构思路（配对票据服务与数据面分离、直连优先、盲目中继回落）写成；**只借鉴契约与架构，不复制其代码**。

## 1. 目标

让官方打包的 Hoxi 在**零配置**下做到：

1. 同一局域网：手机扫码直连桌面，不经任何云端。
2. 跨网络：直连优先，打不通才回落中继；**中继读不到明文**，厂商看不到会话、代码、Diff 或命令。
3. 与「用户自建 GoPanel」并存，不把某一家写死为唯一官方中继。

对齐现有红线：开源本地工具、不托管 AI、外发不带提示词/代码/会话内容（见 [`AGENTS.md`](../AGENTS.md) §2.2 网络外发边界）。

## 2. 设计原则

- **配对与数据面分离**：配对服务只做「一次性邀请票据」的交换，是一个短命的信任边界，不是业务中继、也不是账号系统。数据面走独立的端到端加密传输。
- **直连优先**：同网 / 可打洞时走直连；只有 NAT 打不通才用中继，且中继只转发密文。
- **最小信任**：中继与配对服务都不得看到会话内容；配对服务只见短期票据。
- **可替换**：中继抽象为 provider，自建 GoPanel / 自建 TURN / Cloudflare 都是一等实现。

## 3. 现状（已实现）

| 能力 | 状态 | 位置 |
| --- | --- | --- |
| 桌面生成局域网配对二维码（含局域网 IPv4、端口 `5417`、配对令牌；配置 Cloudflare Tunnel 时另带 `relay`），并支持重新生成令牌（`POST /api/hoxi/pairing/rotate`，旧码立即失效） | 已实现 | [`internal/pairing`](../internal/pairing/pairing.go)、`GET /api/hoxi/pairing/qr` |
| 桌面监听局域网 `:5417`，用配对令牌鉴权，复用远程路由白名单；仅接受局域网来源（loopback / RFC1918 / ULA / 链路本地 / CGNAT），公网来源一律 403 | 已实现 | [`internal/gateway/lan.go`](../internal/gateway/lan.go)、[`internal/app/app.go`](../internal/app/app.go) |
| 手机扫码直连（Hoxi 节点二维码，`type=hoxi`） | 已实现（手机端既有扫码流程） | `client/lib/features/auth/...` |
| 反向代理入口（loopback `127.0.0.1:5418`，供 cloudflared 等本地转发器指向；配对令牌鉴权 + 远程路由白名单；**不做来源地址过滤**，由转发器自身的前置鉴权把关） | 已实现 | [`internal/gateway/ingress.go`](../internal/gateway/ingress.go)、[`internal/app/app.go`](../internal/app/app.go) |
| 受监管的 Cloudflare Tunnel 连接器（设置 → 远程：粘贴 connector token、启停、运行状态；token 存 `~/.hoxi/cloudflared.json`，0600；二进制仅在 PATH / `~/.hoxi/bin` 解析，不自动下载） | 已实现 | [`internal/cloudflared`](../internal/cloudflared/manager.go) |
| 手机端传输选路（身份 ⟂ 传输：节点保存局域网 + 中继多个传输，选路 LAN 优先、失败回落中继，带缓存与重探；`relay` 由二维码携带） | 已实现 | `client/lib/core/network/hoxi_transport_resolver.dart`、`client/lib/shared/models/node_transport.dart` |
| GoPanel 反向隧道（**明文**，GoPanel 可见内容） | 已实现 | [`docs/gopanel-hoxi-remote-tunnel.md`](gopanel-hoxi-remote-tunnel.md) |
| 端到端加密传输、配对票据服务、盲目中继回落 | **未实现** | 本文 |

当前 `5417` 为**明文 HTTP + 令牌**：局域网内被动监听者可嗅探到令牌并冒充手机。适用于可信家庭/办公网络；跨网场景由后续 E2E 阶段解决。监听器**只接受局域网来源地址**（loopback / RFC1918 / ULA / 链路本地 / `100.64.0.0/10`），公网来源在鉴权前即被拒绝，因此即便某台机器直接暴露在公网也不会被外部接入。

## 4. 目标架构（三层）

```text
手机 ──┬─ 局域网直连 http://<lan-ip>:5417（令牌）        [已实现]
       └─ 跨网：E2E 加密传输
                │  直连优先（QUIC + NAT 打洞）
                └─ 打不通 → 盲目中继（只转发密文）        [未实现]
                          ▲
配对票据服务（边缘函数，只交换一次性票据）             [未实现]
```

### 4.1 配对票据服务

- 职责：把「桌面发布的一次性邀请」交给「持有票据的手机」。**不参与业务流量**。
- 部署：边缘函数，一套逻辑多provider：Cloudflare Workers（Durable Object + SQLite）与 EdgeOne（Blob），或直接挂在 `hoxi.ai`。
- 票据内容：**端点的公钥（endpoint ID）+ 直连候选地址 + 中继 URL + 一次性邀请凭据 + 过期时间**。服务原样透传，不得替换成自己的中继。
- 生命周期：一次性、短过期（≤60s）、单次兑付、可取消；`no-store`；不写请求体日志；票据不进 URL。
- 手机扫码路径：二维码**本身就是高熵票据**，无需 6 位短码。短码仅在**无头 Host**（无法扫码）时启用。

### 4.2 E2E 加密传输

- 传输：QUIC（`quic-go`），直连优先、NAT 打洞；打不通回落盲目中继。
- 中继候选（Go 原生，避免引入 Rust）：
  - **Tailscale DERP**（Go、盲目，只转发加密包）；或
  - 自建 **TURN**（`pion/turn`），DTLS 天然盲目。
- 复用现有协议：**HTTP/SSE 跑在 QUIC 单流之上**，手机端适配层几乎不动（现在经隧道访问 `/api/hoxi/...`，改的是底层传输而不改业务路由）。
- 身份与信任：`TOFU`——手机 pin 二维码里的桌面公钥；已配对的重连不再需要票据。

### 4.3 为什么不用 iroh（Sailry 的选择）

iroh 是 **Rust**；Hoxi 桌面是 **Go**、手机是 **Flutter**。没有官方 Go iroh，Go 端只能 cgo 包 FFI。为避免重写传输栈，Hoxi 采用 **`quic-go` + DERP/TURN** 复刻同一套思想；若未来生态成熟可再评估 iroh。

## 5. QR 载荷（已实现：v1 + `relay`）

现载荷：`{"url":"http://<lan-ip>:5417","token":"<hex>","type":"hoxi","entrance":"","relay":"https://<host>"}`。

- `url` 是局域网直连地址；配置了 Cloudflare Tunnel 时附带 `relay`（公开主机名），手机据两候选做 **LAN 优先、失败回落中继** 的选路（`client/lib/core/network/hoxi_transport_resolver.dart`，带缓存与重探）。
- 目标 v2 在此基础上再加**端点公钥**（`endpointId`）与直连地址列表，配合 TOFU 与 E2E 传输（见第 6 节）。
- 与旧版兼容：手机先看 `relay`，缺失时按单地址（局域网）处理。

## 6. 握手与帧格式（草案）

- 握手：手机（发起方）用桌面公钥 + 票据证明自己；`X25519` 临时密钥协商出会话密钥；票据防重放、一次性。
- 帧：每条 HTTP 请求/响应体与每个 SSE `data` 帧各封装为一个 AEAD 封包（`XChaCha20-Poly1305`），按方向 + 单调计数做 nonce，AAD 绑定方向与计数。
- 重放/乱序：计数单调，超窗丢弃；定期重协商。
- 元数据：路径、时序、体积仍可能被中继观察。若要连路径也隐藏，可把所有调用收进单一不透明端点，把真实路径放进密文信封（作为可选第二阶段）。

## 7. 配对票据服务契约（草案）

仿 `sailry-harness` 的 `services/pairing-relay`，但 Hoxi 以二维码承载主票据：

| 方法 + 路径 | 请求体 | 成功 |
| --- | --- | --- |
| `POST /v1/tickets` | `requestId`、`ticket`、可选 `expiresAtMs` | 201：`ticketId`、`cancelToken`、`expiresAtMs` |
| `POST /v1/tickets/redeem` | `requestId`、`ticketId` | 200：`ticket`、`expiresAtMs` |
| `POST /v1/tickets/cancel` | `ticketId`、`cancelToken` | 204 |

- `requestId` 为 64 位小写 hex；单次兑付、过期即失效；限流按源与全局固定窗口。
- 服务只见过期票据，不得记录请求体；响应 `no-store`。

## 8. 中继 provider 配置面

- **自建 GoPanel**（现有）：作为可达性通道，配合应用层 E2E 后即变成盲中继。
- **Cloudflare Tunnel**：仅做可达性（`cloudflared` 主动外连、不开公网端口）；边缘终结 TLS，故**必须叠加应用层 E2E** 才盲。桌面侧的落点是反向代理入口 `http://127.0.0.1:5418`（`internal/gateway/ingress.go`，配对令牌鉴权 + 远程白名单），`cloudflared` 的 `service` 指到这里；连接器由 Hoxi 监管（`internal/cloudflared`，设置 → 远程 粘贴 connector token 并启停），域名用 `<opaque-id>.node.hoxi.work`（CNAME → `<tunnel-id>.cfargotunnel.com`，Proxied），由后端用 Cloudflare API 自动开通，或用户自带。
- **Cloudflare TURN / Realtime**：语音等实时通道打不通时回落，DTLS 本身即盲。
- **EdgeOne**：与 Cloudflare 同构，作配对票据服务的备选 provider。

> 反向代理入口与 LAN 监听是两条独立的本地面：LAN 监听绑所有接口但只收私网来源；入口绑 loopback、不收来源限制，因为它只应被本地转发器访问，公网可达性由转发器前置鉴权（如 Cloudflare Access）负责。

## 9. 分期与验收

1. **LAN 直连**（已完成）：出码 + `5417` 令牌监听 + 手机既有扫码流程。
2. **反向代理入口 + Cloudflare 连接器**（已完成）：loopback `:5418` 入口 + 受监管的 `cloudflared`（设置 → 远程 粘贴 connector token 并启停）。
3. **手机端传输选路**（已完成）：节点保存多传输，LAN 优先、失败回落中继，带缓存与重探。
4. **E2E 契约评审**：本文 + 向量化测试设计评审通过后再动连接层。
5. **E2E 传输**：QUIC 直连 + 打洞 + DERP/TURN 回落；HTTP/SSE over QUIC；QR v2 + TOFU。
6. **配对票据服务 + Cloudflare 开通控制面**：边缘函数 + 限流 + 单次兑付；后端用 Cloudflare API 建 tunnel/DNS 并下发 connector token。
7. **语音实时**：Cloudflare TURN 回落（可选）。

验收要点：直连优先命中率、打洞失败回落可读密文、中继侧抓包无明文、票据一次性与过期、拒绝路径测试（无票据/错公钥/重放均拒）。

## 10. 安全与隐私边界

- 当前 `5417` 为明文 + 令牌；跨网 E2E 落地前，远程仅应经 GoPanel（用户自建，明文但属用户自己的服务器）。
- 中继与配对服务不得记录内容；配对服务只见过期票据。
- 元数据（路径、时序、体积）在 E2E 下仍可能泄露。
- 密钥轮换、`抹除全部节点与本地数据` 必须覆盖配对公钥、票据缓存与会话密钥。

## 11. 未决问题

- 中继自建 vs 依赖公共 DERP/TURN 网络。
- 路径是否也要藏进单一不透明端点。
- 无头 Host 的短码配对是否纳入首版。
- QUIC UDP 在部分网络被封锁时的回落策略。
