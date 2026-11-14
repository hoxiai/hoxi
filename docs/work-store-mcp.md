# Work 店铺商品 MCP

## 实现与范围

`internal/workcatalog` 提供基于官方 `github.com/modelcontextprotocol/go-sdk` 的 MCP 服务，使用 SDK 的初始化与版本协商、JSON-RPC 2.0、工具输入/输出 JSON Schema、`structuredContent` 与兼容文本结果、工具风险 annotations、资源模板及 prompts。

闭环：**授权店铺分页同步 → 本地快照 → 不可变草稿与字段差异 → 桌面审批 → 增量提交 → 线上回读收据**。

当前接入 1688 卖家开放平台：

- 同步 `alibaba.product.list.get` 返回的完整商品对象，包括商品 ID、标题、详情、图片、类目、属性、SKU、价格、库存、起订量等实际返回的字段；数值 ID 不转浮点数
- 首次完整遍历，后续可按修改时间增量同步；每次取 1–10 页，每页 20 条，游标落盘可续传；完整遍历使用官方要求的 ID 排序
- 标题 `subject`、详情 `description`、主图 `images` 通过 `alibaba.product.incrementModify` 回写；只提交修改字段，图片参数保留已有水印设置
- SKU、价格、库存、类目、属性等字段当前用于读取与分析，不在可写 schema 中
- 商品列表 API 官方注明无法查询“简易商品”；实际可读范围和可写权限以商家授权及应用订购的解决方案为准
- Work 商品管理主视图可配置本机接口授权、浏览/搜索/筛选/分页商品、查看详情、创建修改草稿、发布并检查收据，支持从选品库或 JSON 商品包导入本地上新草稿
- 当前没有接入订单、客户、经营报表、图片上传、SKU/价格/库存修改或远端新建商品接口；新建与修改草稿通过 `kind: create | update` 明确区分

已用 SDK 客户端、协议桥接及模拟平台验证协议与业务流程；真实店铺同步/发布需要商家授权后验收。

## 图形界面入口

商品管理采用前后端双端工厂注册模式（后端 `workcatalog.Service.RegisterAdapter` + 前端 `hoxi-work/web/catalog-factory.tsx` + 应用声明 `catalogPolicy`）。左侧「任务会话 / 商品管理」切换栏**仅在当前应用注册了商品目录适配器时展示**（当前内置 `1688`；其他未注册商品目录适配器的应用不展示该选项）。

启动含 `hoxi-work` 扩展的桌面端，选择已接入商品管理的应用（如 1688）和店铺，点击左侧「商品管理」：

1. 点击「接口授权」，填写 1688 卖家登录名、AppKey、AppSecret 和卖家 AccessToken，保存后可验证读取权限
2. 点击「同步商品」，由桌面后台连续拉取页面；关闭商品页面不会中止同步，可显式停止或在中断后续传
3. 在「店铺商品」中按标题、商品 ID、SKU 或平台状态搜索筛选；列表显示缩略图、价格、库存、SKU 数量及未发布修改标记，未知库存不会显示为零
4. 查看详情后创建修改草稿，审阅字段差异；发布时选择或创建绑定当前店铺的任务，直接在商品界面使用统一桌面审批卡「允许一次」，随后检查回读收据
5. 「上新商品」及「选品库」导入会创建本地上新草稿；当前适配器的 `publishNew=false`，不会将其当作已有商品的增量修改提交

界面使用 `/api/hoxi/work/catalog/*` 的受信任桌面 HTTP 门面，与 MCP 工具调用同一领域服务。浏览和本地草稿属于店铺作用域，不要求事先创建聊天；线上发布仍需当前店铺的受管会话和精确目录。界面接口要求本机网关 Bearer Token、`X-Hoxi-Work-UI: true` 及已认可的桌面 Origin/同源上下文；不进入任何远程白名单。

本机授权文件按店铺隔离，使用 OS 文件锁、同目录原子写入及 `0600` 权限保存；界面仅收到授权状态、卖家名和公开 AppKey，不返回 AppSecret/AccessToken，也不把它们写入前端持久化或会话内容。更换凭据会清除之前的读取验证状态，读取验证不代表获得远端商品创建或修改权限。

后台同步任务保存任务状态与每页游标，同店铺由跨进程租约防止重复执行，进程退出后显示中断并允许恢复；产品和草稿详情传到 JavaScript 时，超过安全整数范围的数值标识转为字符串，磁盘原始快照保持原值。

## 本地店铺配置

推荐使用上面的界面配置。本节保留已有环境变量接入方式：通过现有 Work 配置接口 `PUT /api/hoxi/work/apps`，在对应 `storesByApp["1688"]` 店铺对象或 `storeConfigs[storeId]` 增加以下非秘密字段：

```json
{
  "alibaba1688SellerLoginId": "你的1688卖家登录名",
  "alibaba1688CredentialEnv": "FACTORY_A"
}
```

`alibaba1688CredentialEnv` 是环境变量前缀，不是访问令牌。在启动 Hoxi Desktop 的进程环境中设置：

- `FACTORY_A_APP_KEY`
- `FACTORY_A_APP_SECRET`
- `FACTORY_A_ACCESS_TOKEN`

每个店铺使用自己的前缀与卖家授权令牌。环境变量必须由桌面进程继承，不能只设置在 MCP 桥接子进程中。访问令牌的获取、续期及开放平台应用权限需由商家配置；本实现不自动申请或刷新 1688 OAuth 授权。

绑定当前会话到该店铺，使用现有 `sessionStoreBindings`。所有平台商品的 `sellerLoginId`（缺失时检查 `supplierLoginId`）必须与配置的卖家登录名一致。相同本地店铺 ID 不允许复用另一卖家的历史快照。

API 目的地固定为 `https://gw.open.1688.com/openapi/`；使用店铺配置的代理，`proxyRequired` 且未配置代理时拒绝联网。禁用重定向，凭据仅在签名 POST 正文中传输，不放 URL、工具输入、草稿、系统提示或错误结果中。

## 标准 MCP 接入

### stdio

启动 Hoxi Desktop 后，将下列服务器添加到任意支持标准 MCP stdio 的客户端配置中。把示例会话 ID、店铺 ID、目录及可执行文件路径替换为实际值：

```json
{
  "mcpServers": {
    "hoxi-work": {
      "command": "/absolute/path/to/hoxi",
      "args": [
        "work-mcp",
        "--session", "ses_example",
        "--store", "store-1688-factory-a",
        "--dir", "/absolute/session/directory",
        "--address", "127.0.0.1:5410"
      ]
    }
  }
}
```

开发版网关通常为 `127.0.0.1:5420`。桥接使用本机 bootstrap 获取当前启动的网关令牌，标准输出仅输出 MCP 帧。会话的目录必须与实际 worktree 相同。桥接不启动另一份业务状态存储，不自动修改任何运行时的 MCP 配置。

### Streamable HTTP

```text
POST http://127.0.0.1:5410/api/hoxi/work/mcp?sessionID=ses_example&storeId=store-1688-factory-a&directory=<URL编码后的会话目录>
Authorization: Bearer <本机当前启动的 gatewayToken>
```

支持 MCP 的标准 Streamable HTTP 请求。使用 stateless JSON response 模式，客户端执行标准 `initialize` / `notifications/initialized` / `tools/list` / `tools/call`；`GET`、`DELETE` 返回 405。每个请求重新验证令牌、会话目录及店铺绑定；客户端不能在工具参数中切换店铺。

该路由只在桌面回环网关开放；GoPanel、LAN 和 Cloudflare 远程平面均拒绝访问。

## 工具、资源与推荐流程

| 工具 | 行为 |
| --- | --- |
| `work_store_status` | 店铺身份、授权配置就绪状态、外发目的地、商品计数、同步状态与可编辑字段 |
| `work_products_sync` | `restart?`、`since?`（RFC3339）、`maxPages?`（默认 1，最大 10）；重复调用到 `completed` |
| `work_products_list` | 本地快照分页，`offset?`、`limit?`（默认 20，最大 100） |
| `work_products_query` | 整店搜索标题/商品 ID/SKU、平台状态和待发布标记；返回轻量摘要与快照令牌 |
| `work_product_get` | 按 `productId` 读取完整本地商品 |
| `work_product_intake_draft` | 创建本地上新草稿；不生成虚构平台商品 ID，不执行远端创建 |
| `work_draft_create` | `productId` + `changes: {subject?, description?, images?}`；保存原始快照、差异和不可变 `revision` |
| `work_drafts_list` | 分页读取草稿及发布结果摘要 |
| `work_draft_get` | 按 `draftId` 查看原始值、改后值、revision 和收据 |
| `work_draft_discard` | 按 `draftId` + `revision` 撤销尚未发布的本地草稿 |
| `work_draft_publish` | 按 `draftId` + `revision` 审批、检查冲突、增量提交与回读 |
| `work_publish_verify` | 按 `draftId` 重新回读上次发布；绝不重新提交写请求 |

资源模板：

```text
hoxi-work://stores/<storeId>/products/{id}
hoxi-work://stores/<storeId>/drafts/{id}
```

Prompt：`optimize_store_products`。

建议先同步并检查 `work_store_status`，再分页分析商品，逐个创建草稿。发布时必须使用草稿返回的精确 revision。批量操作可以逐商品调用相同工具，每个商品拥有独立结果与收据。

## 发布与恢复语义

- 原始快照与修改稿分开；修改草稿内容需要创建新草稿
- 发布前展示精确商品 ID、revision 和字段差异，经统一桌面审批平面“允许一次”；完全信任模式仍需确认，无人值守、远程回复、class grant 与“始终允许”不能授权发布
- 只比较将要修改的字段；无关库存变化不阻止标题优化，也不会被旧快照覆盖
- 平台接口没有条件写入/CAS 参数，因此“发布前比较”不是服务端原子事务；提交后必须回读
- 在发起平台写请求**之前**持久化 `publishing`，确保进程重启不会让同一草稿盲目重复写入
- `published` 代表修改字段均与回读匹配；`conflict` 代表线上目标字段变化；`unverified` 代表超时、回读失败或平台审核/规范化导致暂未匹配
- `publishing` / `unverified` 先调用 `work_publish_verify`；`unverified` 不会被报告为发布成功。确认需要重新修改时，基于最新回读创建新草稿并重新审批
- 已发布同一草稿的重复发布返回既有收据，不产生第二次写入
- 本地撤销草稿不等于恢复线上数据；当前没有自动线上回滚

状态位于 `hoxipaths.Join("work-catalog")`，按店铺 ID 的 SHA-256 文件名隔离；OS 文件锁覆盖多进程访问，状态通过同目录临时文件、fsync 和原子 rename 落盘。损坏文件先备份并保留原件，返回错误，不静默重新初始化。

完整同步期间旧快照仍可读取，只有所有页面成功后替换；增量同步合并记录。每个店铺当前最多 1000 份草稿/收据，单店铺状态文件上限 128 MiB，单次平台响应上限 8 MiB；达到限制返回显式错误。

完整遍历显式查询官方列出的商品生命周期状态，不设置修改时间上界，避免把遍历期间刚修改的商品排除；增量同步固定 `since` 到启动时刻的时间区间。平台不提供历史快照事务，遍历发现重复 ID 或不一致总数会返回错误，需重新同步。

## 官方接口参考

以下可公开读取的官方元数据于实现时核对：

- [卖家商品列表](https://open.1688.com/api/data/getApiDetail.json?namespace=com.alibaba.product&name=alibaba.product.list.get&version=1&_input_charset=UTF-8)
- [获取自有商品](https://open.1688.com/api/data/getApiDetail.json?namespace=com.alibaba.product&name=alibaba.product.get&version=1&_input_charset=UTF-8)
- [增量修改商品](https://open.1688.com/api/data/getApiDetail.json?namespace=com.alibaba.product&name=alibaba.product.incrementModify&version=1&_input_charset=UTF-8)

应用需具备以上接口权限。增量修改接口的当前官方元数据列出了“工业品商品数字化解决方案（定向招募）”；不能把普通网页登录态或仅有商品查询权限当作已获得该接口写权限。
