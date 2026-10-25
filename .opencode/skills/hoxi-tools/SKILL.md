---
name: hoxi-tools
description: Discover, inspect, and execute 220+ verified Agent Tools and 2,000+ data/API endpoints (SEO, Ahrefs, DataForSEO, Apify scrapers, Amazon/TikTok/social media data, Apollo/Crunchbase lead enrichment, ElevenLabs/AI media) through Hoxi Desktop local browser/Chrome Link and the Hoxi Tools Gateway using Hoxi Account Points. 发现、校验并调用 Hoxi 全网 Agent 工具库（含网页采集、SEO 外链与关键词、跨境电商评论、社媒达人数据、B2B 获客背调、多模态生成等 220+ 服务商接口），优先使用 Hoxi 桌面端本地浏览器免费能力，商业数据接口经 Hoxi 网关按次消耗会员积分。
compatibility: 支持 Hoxi 桌面端本地免 Key 桥接、Hoxi MCP Server 与 REST API 调用
metadata:
  author: "Hoxi"
  version: "1.0.0"
---

# Hoxi Tools — 全栈与 Agent 工具调度技能

`hoxi-tools` 是 Hoxi 研发工作台的工具与外部数据调度中枢。它把 **Hoxi 桌面端本地能力（无头浏览器 + Chrome Link + 本地工作区）** 与 **Hoxi 云端工具网关（220+ 服务商、按次积分计费接口）** 结合为统一的调用闭环。

---

## 一、双层省钱路由原则（必读）

在写爬虫或请求外部数据前，严格按以下优先级选路：

1. **第 1 层：零成本本地能力优先（0 积分）**
   - **公开单页读取**：目标只是读取普通公开网页正文、文档或截图时，直接使用内置网页抓取或 Hoxi 桌面端无头浏览器（Headless Chrome）。
   - **用户已登录后台读取与回填**：目标是读取或操作用户在 Chrome 已登录的卖家/创作者后台（Amazon Seller Central、小红书创作者中心、TikTok Shop、Shopify Admin、1688 旺铺、广告后台），优先使用 **Hoxi Chrome Link 扩展**。
   - **本地电商与买量测算**：Shopify 阶梯定价 CSV、TikTok 30s 脚本与佣金测算、小红书违禁词体检、Amazon FBA 利润与 250B 字节校验、ROAS 盈亏平衡计算、1688 阶梯批发价测算，直接调用内置 `hoxi_work_*` MCP 工具（0 积分）。

2. **第 2 层：Hoxi Tools 云端数据与 AI 接口（按次消耗 Hoxi 积分）**
   - 当任务涉及 **商业闭源数据库、反爬社媒平台批量采集、专业 SEO 索引或生成式多模态 API** 时，切勿手写脆弱爬虫，直接走 **Hoxi Tools 网关**：
     - **搜索与 SEO**：`ahrefs`（外链、Domain Rating、关键词难度、AI 引用率）、`dataforseo`（Google/Bing/YouTube/Amazon 实时 SERP 与搜索量）、`exa`、`cloro`（ChatGPT/Gemini/Perplexity 真实回答监控）
     - **社媒与电商采集**：`apify`、`anyapi`、`firecrawl`、`context-dev`（TikTok、Instagram、X/Twitter、YouTube、Reddit、Amazon 商品与评论结构化提取）
     - **B2B 获客与创投背调**：`apollo`、`clay`、`contactout`、`crunchbase`、`fundable`、`akta`、`g2`、`capterra`（企业决策人验证邮箱、融资轮次、竞品评价）
     - **金融、车况与公共数据**：`finviz`、`defillama`、`carfax`、`edmunds`、`accuweather`、`censusdata-xyz`
     - **多模态与语音生成**：`elevenlabs`、`fishaudio`、`alibaba`（Wan/Qwen）、`bytedance`（Seedance）

---

## 二、标准三步工作流（Discover → Inspect → Run）

调用任何 Hoxi Tools 接口前，**严禁猜测参数名**，必须严格执行三步法：

1. **Discover（检索接口）**：按自然语言关键词或分类检索匹配的 `provider` 与 `endpoint`，查看预估积分消耗（`estimatedPoints`）。
2. **Inspect（读取入参 Schema）**：指定 `provider` 与 `endpoint`，获取精确的 `inputSchema`（区分 `body`、`queryParams`、`pathParams`）。
3. **Run（执行并落盘）**：按 Schema 传入参数执行接口，将返回的 JSON 结果保存到当前工作区（如 `./.hoxi/data/` 或项目数据目录）供后续分析或由业务 Skill 消费。

---

## 三、三种调用通道（按当前运行环境自动选择）

### 通道 A：MCP 工具直调（已挂载 Hoxi MCP Server 时首选）

若当前会话已连接 Hoxi MCP Server（`/api/mcp`），直接调用以下 3 个原生工具：

- `hoxi_tools_discover`：传入 `{ "query": "<需求关键词>", "category": "<可选分类>", "limit": 10 }`
- `hoxi_tools_inspect`：传入 `{ "provider": "<provider>", "endpoint": "<endpoint>" }`
- `hoxi_tools_run`：传入 `{ "provider": "<provider>", "endpoint": "<endpoint>", "body": { ... }, "wait": true }`

### 通道 B：Hoxi 桌面端本机免 Key 桥接（在 Hoxi 桌面端内运行命令时首选）

在 Hoxi 桌面端会话中，本地桥接网关（Release 默认 `http://127.0.0.1:5410`，Dev 默认 `http://127.0.0.1:5420`）会自动附带当前已登录的 Hoxi 账号凭据，**无需用户手动配置 API Key**：

```bash
# 探测本地桥接端口（优先 5410，回落 5420）
HOXI_BRIDGE="${HOXI_BRIDGE_URL:-http://127.0.0.1:5410}"

# 1. Discover：检索可用工具与 Endpoint
curl -sS -X POST "${HOXI_BRIDGE}/api/hoxi/auth/tools/discover" \
  -H "Content-Type: application/json" \
  -d '{"query": "amazon reviews", "limit": 8}'

# 2. Inspect：查看指定 provider + endpoint 的入参结构与积分消耗
curl -sS -X POST "${HOXI_BRIDGE}/api/hoxi/auth/tools/inspect" \
  -H "Content-Type: application/json" \
  -d '{"provider": "dataforseo", "endpoint": "/v3/merchant/amazon/products/live"}'

# 3. Run：使用当前登录的 Hoxi 账号与积分执行接口
curl -sS -X POST "${HOXI_BRIDGE}/api/hoxi/auth/tools/run" \
  -H "Content-Type: application/json" \
  -d '{
    "provider": "dataforseo",
    "endpoint": "/v3/merchant/amazon/products/live",
    "body": {"keyword": "wireless charger", "location_code": 2840},
    "wait": true
  }'
```

> 若返回 `401 Unauthorized`（未登录 Hoxi 账号），提示用户在 Hoxi 桌面端左下角点击头像一键登录 Hoxi 账号（每日签到即可免费领取积分）。

### 通道 C：云端 API 直调（外部 CLI / 独立环境）

在非 Hoxi 桌面端的外部 Agent 环境中，直接请求 `https://hoxi.ai`（开发联调为 `http://127.0.0.1:5400`）：

- **Discover（公开免鉴权）**：
  ```bash
  curl -sS -X POST "https://hoxi.ai/api/hoxi/tools/discover" \
    -H "Content-Type: application/json" \
    -d '{"query": "ahrefs backlinks"}'
  ```
- **Inspect（公开免鉴权）**：
  ```bash
  curl -sS -X POST "https://hoxi.ai/api/hoxi/tools/inspect" \
    -H "Content-Type: application/json" \
    -d '{"provider": "ahrefs", "endpoint": "/v3/site-explorer/backlinks"}'
  ```
- **Run（需携带 Hoxi 令牌 `hox_dt_...` 或 `apay_mcp_...`）**：
  ```bash
  curl -sS -X POST "https://hoxi.ai/api/hoxi/tools/run" \
    -H "Authorization: Bearer ${HOXI_TOKEN}" \
    -H "Content-Type: application/json" \
    -d '{"provider": "ahrefs", "endpoint": "/v3/site-explorer/backlinks", "body": {"target": "example.com"}}'
  ```

---

## 四、与一人公司垂直运营 Skill 的闭环联动

当用户执行跨境电商、独立站、社媒种草或买量投放任务时，主动将 `hoxi-tools` 作为 **前置真实数据源** 与以下官方技能组合：

1. **配合 `/amazon-ops`（亚马逊运营）**：
   先用 `hoxi-tools` 调用 `dataforseo` / `apify` 拉取竞品 ASIN 差评（VOC）与搜索词数据 → 交由 `amazon-ops` 生成 COSMO 五点描述、A+ 脚本与 250B Search Terms → 经 Chrome Link 填入卖家后台。
2. **配合 `/tiktok-shop-ops`（TikTok 小店与带货）**：
   先用 `hoxi-tools` 调用 `apify` / `anyapi` 拉取同类目热门带货视频数据与达人信息 → 交由 `tiktok-shop-ops` 拆解黄金前 3 秒钩子、生成挂车脚本与达人建联 DM。
3. **配合 `/shopify-ops` & `/ads-growth-ops`（独立站与出海买量）**：
   先用 `hoxi-tools` 调用 `ahrefs` / `dataforseo` / `apollo` 获取高转化长尾词、竞品外链与潜客决策人邮箱 → 生成高转化着陆页与 Meta/Google 广告测试矩阵。
4. **配合 `/xiaohongshu-ops` & `/1688-ops`（国内种草与源头货盘）**：
   结合 `hoxi-tools` 行业趋势数据与本地合规审计工具，完成从 1688 源头拿样定价到小红书 KFS 矩阵种草的全链路闭环。
