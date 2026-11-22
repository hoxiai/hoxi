---
name: tools
description: Discover, inspect, and execute 220+ verified Agent Tools and 2,000+ data/API endpoints (SEO, Ahrefs, DataForSEO, Apify scrapers, Amazon/TikTok/social media data, Apollo/Crunchbase lead enrichment, ElevenLabs/AI media) through Hoxi Desktop local browser/Chrome Link and the Hoxi Tools Gateway using Hoxi Account Points. 发现、校验并调用 Hoxi 全网 Agent 工具库（含网页采集、SEO 外链与关键词、跨境电商评论、社媒达人数据、B2B 获客背调、多模态生成等 220+ 服务商接口），优先使用 Hoxi 桌面端本地浏览器免费能力，商业数据接口经 Hoxi 网关按次消耗会员积分。
compatibility: 支持 Hoxi 桌面端本地免 Key 桥接、Hoxi MCP Server 与 REST API 调用
metadata:
  author: "Hoxi"
  version: "1.3.0"
---

# Hoxi Tools — 全栈与 Agent 工具调度技能

`tools` 是 Hoxi 研发工作台的工具与外部数据调度中枢。它把 **Hoxi 桌面端本地能力（无头浏览器 + Chrome Link + 本地工作区）** 与 **Hoxi 云端工具网关（220+ 服务商、按次积分计费接口）** 结合为统一的调用闭环。

---

## 一、双层省钱路由原则（必读）

在写爬虫或请求外部数据前，严格按以下优先级选路：

1. **第 1 层：零成本本地能力优先（0 积分）**
   - **公开单页读取**：目标只是读取普通公开网页正文、文档或截图时，直接使用内置网页抓取或 Hoxi 桌面端浏览器（Desktop Window / 隔离无头 Chrome）。
   - **用户已登录后台读取与回填**：目标是读取或操作用户在 Chrome 已登录的卖家/创作者后台（Amazon Seller Central、小红书创作者中心、TikTok Shop、Shopify Admin、1688 旺铺、广告后台），优先使用 **Hoxi Chrome Link 扩展**。Chrome Link 已连接标签页与桌面窗口/隔离无头浏览器统一通过 Hoxi 内置的 `hoxi_browser` 工具或本机 `/api/hoxi/browser/*` 回环接口控制。
   - **本地电商与买量测算**：纯算术与本地文本规则校验（如 ROAS 盈亏平衡计算、Amazon FBA 利润与 250B 字节长度校验、Shopify 阶梯定价 CSV 生成、TikTok 30s 脚本字数与佣金测算、小红书违禁词规则比对、1688 阶梯批发价测算）直接由 Agent 或本地脚本在工作区内计算完成（0 积分），无需调用付费外部 API。
   - **本地 Office / PDF 文档与报表读写**：读取、拆解、生成或修改 `.xlsx`、`.xls`、`.ods`、`.csv`、`.tsv`、`.docx`、`.pptx`、`.pdf` 时，优先使用 Hoxi 内置零依赖命令 `hoxi doc inspect|read|write-xlsx|set-cell|write-docx|write-pptx|write-pdf`（详见 `office-docs` 技能，0 积分、免装第三方库）。

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

## 三、四种调用通道（按当前运行环境自动选择）

### 通道 A：Hoxi 原生内置工具 `hoxi_tools`（当前会话具备 `hoxi_tools` 工具时首选）

当工具列表包含内置的 **`hoxi_tools`**（Hoxi 桌面端默认 Go 原生引擎 `native` 及可选 OpenCode 运行时自动注入）时，无需跑 `curl` 命令、也无需手动配置任何 API Key，直接调用 `hoxi_tools`（`discover` 与 `inspect` 为只读操作自动放行且 0 积分，`run` 会发起工具审批并按次消耗积分）：

1. **Discover（检索可用工具与 Endpoint）**：
   调用 `hoxi_tools`：`{ "action": "discover", "query": "amazon reviews", "limit": 8 }`
2. **Inspect（读取入参 Schema 与积分消耗）**：
   调用 `hoxi_tools`：`{ "action": "inspect", "provider": "dataforseo", "endpoint": "/v3/merchant/amazon/products/live" }`
3. **Run（按 Schema 执行接口）**：
   调用 `hoxi_tools`：`{ "action": "run", "provider": "dataforseo", "endpoint": "/v3/merchant/amazon/products/live", "body": { "keyword": "wireless charger", "location_code": 2840 }, "wait": true }`

> 若返回 `401 Unauthorized`（未登录 Hoxi 账号），提示用户在 Hoxi 桌面端左下角点击头像一键登录 Hoxi 账号（每日签到即可免费领取积分）。

### 通道 B：Hoxi 桌面端原生 CLI 本机免 Key 网桥（Claude Code / Antigravity / Gemini CLI 等）

在 Hoxi 桌面端启动的原生 CLI 运行时中，进程环境变量已自动注入本机网关地址 `HOXI_GATEWAY_URL`（未设置时默认回环地址为 `http://127.0.0.1:5410`）。直接请求本机网桥即可复用桌面端已登录的 Hoxi 账号（无需手动传 Token；在原生 CLI 运行时中通过 `bash` + `curl` 调用受当前会话的 Shell 审批策略管控，请使用单行命令，不要写 `${HOXI_GATEWAY_URL:-...}` 参数展开或 `\` 续行以免触发复杂命令门控）：

```bash
# 1. Discover（0 积分）
curl -sS -X POST "$HOXI_GATEWAY_URL/api/hoxi/tools/discover" -H "Content-Type: application/json" -d '{"query":"amazon reviews","limit":8}'

# 2. Inspect（0 积分）
curl -sS -X POST "$HOXI_GATEWAY_URL/api/hoxi/tools/inspect" -H "Content-Type: application/json" -d '{"provider":"dataforseo","endpoint":"/v3/merchant/amazon/products/live"}'

# 3. Run（按次消耗 Hoxi 账号积分）
curl -sS -X POST "$HOXI_GATEWAY_URL/api/hoxi/tools/run" -H "Content-Type: application/json" -d '{"provider":"dataforseo","endpoint":"/v3/merchant/amazon/products/live","body":{"keyword":"wireless charger","location_code":2840},"wait":true}'
```

### 通道 C：MCP 工具直调（外部客户端挂载 Hoxi MCP Server 时首选）

若当前客户端已连接 Hoxi MCP Server（`/api/mcp`），直接调用以下 3 个 MCP 工具：

- `hoxi_tools_discover`：传入 `{ "query": "<需求关键词>", "category": "<可选分类>", "limit": 10 }`
- `hoxi_tools_inspect`：传入 `{ "provider": "<provider>", "endpoint": "<endpoint>" }`
- `hoxi_tools_run`：传入 `{ "provider": "<provider>", "endpoint": "<endpoint>", "body": { ... }, "wait": true }`

### 通道 D：云端 HTTP REST 直调（脱离 Hoxi 桌面端的独立环境）

在未启动 Hoxi 桌面端的独立服务器或外部 CLI 环境中，直接请求云端网关（`https://hoxi.ai`）：

- **Discover（公开免鉴权）**：
  ```bash
  curl -sS -X POST "https://hoxi.ai/api/hoxi/tools/discover" -H "Content-Type: application/json" -d '{"query":"ahrefs backlinks"}'
  ```
- **Inspect（公开免鉴权）**：
  ```bash
  curl -sS -X POST "https://hoxi.ai/api/hoxi/tools/inspect" -H "Content-Type: application/json" -d '{"provider":"ahrefs","endpoint":"/v3/site-explorer/backlinks"}'
  ```
- **Run（需携带 Hoxi 令牌 `hox_dt_...` 或 `hox_mcp_...`）**：
  ```bash
  curl -sS -X POST "https://hoxi.ai/api/hoxi/tools/run" -H "Authorization: Bearer $HOXI_TOKEN" -H "Content-Type: application/json" -d '{"provider":"ahrefs","endpoint":"/v3/site-explorer/backlinks","body":{"target":"example.com"}}'
  ```

---

## 四、在行业应用中使用工具数据

进行跨境电商、社媒运营或广告分析时，先切换到已安装的行业应用。用 `tools` 获取任务所需的数据，再用该应用的技能或内置工具分析结果。

1. **前置取数（`tools`）**：先用 `dataforseo` / `apify` / `ahrefs` / `apollo` 拉取竞品评论、关键词、外链、社媒趋势或潜客数据并落盘至工作区。
2. **本地精算与合规体检（应用内置 `tools/bin/`）**：由对应应用包自带的确定性脚本完成利润、广告报表诊断、Listing 合规校验或 CSV 导出（0 积分）。
3. **浏览器隔离协同（`hoxi_browser`）**：通过当前店铺隔离的 Chrome Link / 专属浏览器回填草稿，并在高危发布前经桌面审批确认。
