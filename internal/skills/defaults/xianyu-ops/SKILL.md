---
name: xianyu-ops
description: Operate Xianyu (闲鱼网页版) sourcing, continuous new-listing monitoring, AI item defect/scam screening, price-arbitrage calculation, and fast hybrid order locking via the local `xianyu-mcp` Chrome extension bridge and `hoxi ecom`. 当用户需要在闲鱼搜索淘货、配置关键词持续盯盘监控、深度查验二手商品正文排雷（识别贩子引流/暗病硬伤/邮费刺客）、测算二手捡漏或 1688 价差套利、以及执行极速双模锁单时使用。
compatibility: 适用于 Hoxi Work / Hoxi Code，需在 Chrome 加载 `xianyu-picker` 扩展并连接本机 `xianyu-mcp` 桥接服务
metadata:
  author: "Hoxi"
  version: "1.1.0"
---

# 闲鱼智能选品、AI 验品排雷与双模极速锁单技能 (`xianyu-ops`)

`xianyu-ops` 将 **闲鱼网页版 Chrome 扩展（`xianyu-picker`）的双模极速锁单引擎** 与 **Hoxi Work 多会话 AI 决策能力** 深度打通，形成「**智能盯盘规则拆解 → 实时搜品/命中聚合 → 单品详情深度排雷 → 直跳确认页原生核价锁单**」闭环。

## 一、双通道工具调用机制

### 通道 A：原生 `xianyu-mcp` 工具直调（首选）

当当前会话已挂载 `xianyu-mcp` 时，直接按序调用以下工具：

| 类别 | 工具名 | 核心用途 |
| :--- | :--- | :--- |
| **环境与风控** | `xianyu_status` | 检查扩展连接、监控引擎开关、极速锁单与运费熔断配置、风控状态（`running` / `backoff` / `paused_manual_verify`）、未读命中数与锁单进度 |
| **环境与风控** | `xianyu_open_monitor_tab` | 打开或聚焦 Chrome 中的闲鱼常驻监控标签页（供用户扫码登录或手动过滑块） |
| **环境与风控** | `xianyu_resume_guard` | 用户在浏览器手动完成滑块/登录验证后，立即重置熔断器恢复运行 |
| **实时淘货验品** | `xianyu_search_items` | 按「最新发布」实时搜索商品（自动走限流与风控守卫），支持 `priceMin`/`priceMax`/`includeWords`/`excludeWords`/`freeShippingOnly` 过滤 |
| **实时淘货验品** | `xianyu_get_item_detail` | 拉取单品完整详情（正文 `desc`、在售状态 `online`、网页可拍 `pcSupportTrade`、多规格 `hasMultiSku`、验货宝 `yhbSupport`、运费 `transportFee`、芝麻信用与 `riskSignals`） |
| **持续盯盘监控** | `xianyu_list_tasks` | 查看全部监控任务、过滤规则与基线状态 |
| **持续盯盘监控** | `xianyu_upsert_task` | 新建或更新关键词监控任务（支持必含规格词 `includeWords`、防坑排除词 `excludeWords` 与目标价备注 `note`） |
| **持续盯盘监控** | `xianyu_delete_task` | 删除指定监控任务并清理基线缓存 |
| **持续盯盘监控** | `xianyu_control_monitor` | `start` 启动持续轮询、`stop` 停止轮询、`poll_now` 立即巡检一轮（`matchExisting=true` 可将当前首页现存好物直接记入命中） |
| **命中流与锁单** | `xianyu_list_hits` | 查询监控命中的商品列表（支持按 `new`/`opened`/`locked`/`ignored`/`all` 筛选） |
| **命中流与锁单** | `xianyu_update_hits` | 批量标记已审阅（`opened`）或归档剔除引流/暗病废品（`ignored`） |
| **命中流与锁单** | `xianyu_lock_item` | 双模极速锁单（非多规格商品跳过详情页直达 `/create-order`，通过 `order.render` 原生核验默认地址 `deliverId` 与真实含邮实付款，支持 `maxTotalPrice` 含邮总价熔断与多规格无缝回退详情页） |
| **命中流与锁单** | `xianyu_abort_lock` | 立即中止进行中的锁单任务 |
| **审计与设置** | `xianyu_list_orders_and_events` | 查询历史锁单订单号、确认页实付款 `realPayAmount`、直达模式标记与扩展动态日志 |
| **审计与设置** | `xianyu_update_settings` | 调整轮询间隔（`>=15s`）、随机抖动、活跃时段、极速直达开关（`fastLock`）、运费熔断上限（`maxShippingFee`）与每日自动锁单上限 |

### 通道 B：本机 CLI 免配置直调兜底

若当前会话尚未加载 `xianyu-mcp` 工具，可直接通过本地构建产物执行等价调用，或引导用户在项目目录运行一次 `npm run setup:hoxi` 永久注册到 Hoxi 模型配置中：

```bash
# 查看桥接与扩展状态
node /Users/hugh/code/hoxi/xianyu/dist-mcp/index.mjs status

# 直接调用任意工具（传入 JSON 参数）
node /Users/hugh/code/hoxi/xianyu/dist-mcp/index.mjs call xianyu_search_items '{"keyword":"Mac mini M4","priceMin":2600,"priceMax":3400,"excludeWords":["求购","回收","出租"]}'
```

## 二、标准四步实战工作流（SOP）

### SOP 1：实时淘货与 AI 深度验品排雷（最常用）

1. **先查状态**：调用 `xianyu_status`。若 `extensionConnected=false`，提示用户在 Chrome 打开扩展；若 `guardState.kind === "paused_manual_verify"`，调用 `xianyu_open_monitor_tab(focus=true)` 请用户完成滑块验证后再调 `xianyu_resume_guard`。
2. **精准初筛**：调用 `xianyu_search_items`，结合品类常识自动补齐合理下限价（过滤 ¥1/¥9.9 引流贴）、必含规格词（如 `["256G", "国行"]`）与高频防坑排除词（`["求购", "回收", "出租", "租赁", "扩容", "有锁", "卡贴", "尸体", "模型", "仅自提"]`）。
3. **逐单深验**：对初筛性价比最高的 3–5 件候选商品，逐一调用 `xianyu_get_item_detail` 重点核查：
   - **可买性与形态**：`online === true` 且 `pcSupportTrade !== false`，同时留意 `hasMultiSku`（多规格）与 `yhbSupport`（验货宝）；
   - **真实到手价**：`price + (transportFee || 0)`，警惕低标价 + 高额运费的「邮费刺客」；
   - **卖家与正文排雷**：结合 `desc`、`sellerCredit` 与 `riskSignals` 甄别个人一手自用闲置 vs 职业商家批量引流话术（如“看主页”、“详聊”、“型号齐全”）、暗病硬伤（漏液/断触/无面容/绕 ID/维修史）及“售出不退换”霸王声明。
4. **结构化交付**：按「含邮到手总价 + 个人/商家属性 + 成色亮点 + 风险提示 + 商品直达链接」输出推荐对比表，询问用户需要对哪一件发起极速锁单。

### SOP 2：7×24 持续防漏监控任务搭建

1. 将用户的捡漏目标拆解为 1–3 个互补搜索词（覆盖官方全称与玩家常用简称），分别调用 `xianyu_upsert_task` 写入精确的 `priceMin`、`priceMax`、`includeWords`、`excludeWords` 与 `note`（写入目标心理价，便于后续审阅命中时对标）。
2. 调用 `xianyu_control_monitor(action="poll_now", matchExisting=true)` 立即扫描当前首页现存好物并完成首轮排雷汇报。
3. 调用 `xianyu_control_monitor(action="start")` 开启后台低频串行轮询盯盘。

### SOP 3：未读命中池批量清洗（Hit Triage）

1. 调用 `xianyu_list_hits(status="new")` 读取所有新命中的未读商品。
2. 对候选单品调用 `xianyu_get_item_detail` 验明正身：
   - 已下架售出、明显引流/求购贴、存在严重硬伤的商品 → 批量调用 `xianyu_update_hits(itemIds=[...], status="ignored")` 自动清理降噪；
   - 正常有效商品 → 标记为 `opened` 并整理出「今日最值得出手的漏价精选清单」。

### SOP 4：双模混合极速锁单与含邮总价熔断保护

1. 如涉及二手回收转售或与 1688 新品比价，可联动运行 `hoxi ecom profit` 核算平台手续费、运费与净利空间。
2. 发起锁单时，**建议始终附带 `maxTotalPrice`（含邮最高心理总价）并默认保持 `autoSubmit: false`**：
   - 调用 `xianyu_lock_item(itemId="...", price=..., maxTotalPrice=..., precheck=true, autoSubmit=false)`；
   - **极速直达架构**：扩展会自动跳过笨重的商品详情页，直接打开 `/create-order?itemId=...`（省去 2.5–4 秒），由闲鱼官方前端携带完整人机指纹与环境令牌请求 `order.render`；扩展在 0ms 内比对缓存的默认收货地址 `deliverId` 并核验真实含邮实付款（若超出 `maxTotalPrice` 或 `maxShippingFee` 自动熔断拦截；若遇多规格未选自动回退 `/item` 详情页），最后停在确认订单页高亮「确认购买」等用户核对点击。

## 三、安全与风控铁律（不可逾越）

1. **绝不自动付款、绝不静默提交订单**：`xianyu_lock_item` 默认一律使用 `autoSubmit: false`（半自动停在确认订单页）。仅当用户在对话中明确指令“帮我自动提交锁单”且已在扩展侧边栏手动开启「允许 AI 自动提交订单」开关时，才允许传 `autoSubmit: true`，且必须同时传入合理的 `maxTotalPrice` 保护上限。提醒用户闲鱼拍下不付款会扣减账号信用。
2. **绝不绕过验证码与滑块**：遇到 `RGV587_ERROR`、`FAIL_SYS_USER_VALIDATE` 或页面滑块导致风控暂停时，严禁尝试任何脚本绕过或密集重试，必须交由用户在监控标签页手动完成验证后再恢复。
3. **隐私零外泄**：所有闲鱼接口均在用户本机 Chrome 的 `goofish.com` 页面上下文内由官方 `lib.mtop` 发出，严禁读取或导出用户 Cookie / Token。
