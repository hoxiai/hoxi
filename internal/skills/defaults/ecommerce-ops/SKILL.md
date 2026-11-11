---
name: ecommerce-ops
description: Cross-border e-commerce operations workflow for Amazon, TikTok Shop, Shopify, and 1688 sourcing using deterministic `hoxi ecom` calculators, Search Term ad report auditing, listing compliance checks, Shopify CSV generation, and anti-association store browser isolation. 跨境电商全链路运营工作流（Amazon / TikTok Shop / Shopify 独立站 / 1688 采源），内置确定性利润精算、广告搜索词报表诊断、Listing 字节与违禁词合规体检、Shopify 多变体 CSV 构建及多店铺防关联浏览器隔离规范。
compatibility: Built-in Hoxi CLI (`hoxi ecom` + `hoxi doc` + `hoxi image`) + Store-Isolated `hoxi_browser`
metadata:
  author: "Hoxi"
  version: "1.1.0"
---

# 跨境电商全链路运营技能 (`ecommerce-ops`)

`ecommerce-ops` 为 **Hoxi Work** 与 **Hoxi Code** 提供面向 **Amazon、TikTok Shop、Shopify 独立站、1688 采源** 的确定性财务核算、广告报表诊断、Listing 合规校验与多店铺防关联操作规范。

---

## 一、三大铁律（严禁估算与越界）

1. **严禁心算财务与字节数**：
   - 凡涉及 **FBA 尺寸分段配送费、1688 抛重（`长×宽×高/6000` vs 实重）、头程运费折算、达人佣金+寄样摊销、Shopify 支付通道费、保本 ACOS/ROAS、目标净利率反推售价**，一律调用内置确定性引擎 `hoxi ecom profit`。
   - 凡涉及 **Amazon 后台 Search Terms 249 UTF-8 字节上限、EPA 杀虫剂/抗菌词、FDA 医疗宣称、TikTok Shop 站外导流词与前 3 秒黄金完播口播时长、1688 极限词**，一律调用 `hoxi ecom listing-check`。
   - 凡涉及 **Amazon / TikTok / Google 搜索词广告报表（STR）否词与调价诊断**，一律调用 `hoxi ecom ads-audit`。
2. **优先继承绑定店铺档案（`<hoxi_work_store_profile>`）**：
   - 当会话已绑定店铺时，系统提示词会自动注入 `<hoxi_work_store_profile>`（含店铺 ID、站点、币种、佣金率、头程单价、汇率、退货率、目标净利率、品牌名与违禁词）。
   - 执行 CLI 时带上 `--store <storeId>` 可自动继承该店铺参数，无需用户每轮重复输入。
3. **多店防关联与店铺后台写操作人工确认红线**：
   - 绑定了独立代理（`Proxy=isolated`）的店铺会话，其 `hoxi_browser` 会自动经本机隔离转发桥接至该店铺专属住宅/静态 ISP 代理并禁用非代理 WebRTC UDP 泄露；若代理不可达或 `proxyRequired=true` 未配置代理，浏览器会 **Fail-Closed 阻断启动**，严禁绕过浏览器改用本机直连请求访问卖家后台。
   - 在 Seller Central / TikTok Seller Center / Shopify Admin 执行 **改价、发布上架、调整广告预算/竞价、创建促销优惠券（Coupon/Deal）或发货确认** 前，必须先向用户展示变更前后对比表并由用户确认提交，严禁静默点击最终发布/保存按钮。

---

## 二、`hoxi ecom` 确定性命令速查

### 1. 选品与单品利润精算（`hoxi ecom profit`）

```bash
# Amazon FBA 精算（自动对比实重 vs 抛重、自动估算美区 FBA 尺寸分段配送费、退货损耗与目标净利率反推售价）
hoxi ecom profit \
  --store "<storeId>" \
  --platform amazon \
  --price 39.99 \
  --cost-cny 36 \
  --dims-cm 30x20x10 \
  --weight-kg 0.45 \
  --ship-rate-kg 14.4 \
  --fx 7.2 \
  --return-pct 5 \
  --acos-pct 20 \
  --target-margin-pct 18

# TikTok Shop 达人带货精算（含平台佣金 + 达人佣金 + 寄样成本摊销 + 1.5x~4.0x ROAS 敏感度矩阵与最高允许 CPA）
hoxi ecom profit \
  --platform tiktok-shop \
  --price 29.99 \
  --cost 5.50 \
  --fulfillment-fee 4.20 \
  --referral-pct 6 \
  --affiliate-pct 15 \
  --sample-cost 5.50 \
  --sample-count 20 \
  --expected-units 200 \
  --roas 3.0 \
  --target-margin-pct 15

# Shopify 独立站定价与支付通道费（默认含 2.9% + $0.30 通道费与 ROAS 盈亏矩阵）
hoxi ecom profit \
  --platform shopify \
  --price 49.99 \
  --cost 11.00 \
  --fulfillment-fee 6.50 \
  --roas 2.5

# 1688 阶梯起订量（MOQ）到岸成本对比与建议零售价测算
hoxi ecom profit \
  --platform 1688 \
  --currency USD \
  --fx 7.2 \
  --qty 500 \
  --brackets "100:22.0,500:19.5,1000:18.0" \
  --dims-cm 28x18x8 \
  --weight-kg 0.55 \
  --ship-rate-kg 12.5 \
  --tariff-pct 10
```

### 2. 搜索词广告报表诊断与否词导出（`hoxi ecom ads-audit`）

支持直接解析卖家后台导出的中英文 `.xlsx`、`.xls`、`.ods`、`.csv`、`.tsv` 搜索词报表（自动识别 `Customer Search Term` / `客户搜索词`、`Spend` / `花费`、`7 Day Total Sales` / `销售额`、`Orders` / `订单数`）：

```bash
# 只读诊断（自动分桶：0单高耗否词候选 Bleeders、高 ACOS 精确降价建议、出单王拓词 Winners、高曝光低点击预警）
hoxi ecom ads-audit --file ".hoxi/attachments/SearchTermReport.xlsx" --target-acos 25

# 诊断并导出带原生 Excel 公式汇总页的 4-Sheet 运营执行表（Summary / Negative_Keywords / Bid_Adjustments / Scale_Winners）
hoxi ecom ads-audit \
  --file ".hoxi/attachments/SearchTermReport.xlsx" \
  --target-acos 25 \
  --waste-spend 15 \
  --waste-clicks 12 \
  --out "output/ads-optimization-plan.xlsx"
```

### 3. Listing 字节数、合规词库与短视频口播体检（`hoxi ecom listing-check`）

```bash
# Amazon Listing 合规与后台 Search Terms 249 UTF-8 字节精准去重截断
hoxi ecom listing-check \
  --store "<storeId>" \
  --platform amazon \
  --brand "YourBrand" \
  --title "YourBrand Ergonomic Memory Foam Seat Cushion for Office Chair..." \
  --bullet "TAILBONE PRESSURE RELIEF - High-density memory foam contours..." \
  --search-terms "office chair cushion desk seat pad tailbone pillow..."

# TikTok Shop 短视频脚本前 3 秒黄金钩子时长测算 + 站外导流/夸大功效词拦截
hoxi ecom listing-check \
  --platform tiktok-shop \
  --title "Portable Cordless Mini Blender Cup for Smoothies & Shakes" \
  --script "Stop overpaying ten dollars for smoothies! Watch this crush frozen strawberries in ten seconds. Tap the orange cart below while the flash deal is live!"

# 1688 跨境选品/批发标题 60 字节利用率与《广告法》极限词扫描
hoxi ecom listing-check --platform 1688 --title "源头厂家跨境专供硅胶防滑厨房隔热垫一件代发定制批发"

# 1688 店铺商品 7 星质量全量体检（支持直接读取抓取落盘的 source-1688.json，校验第 5 张白底图、主图数、属性完整度、阶梯价与跨境体积重）
hoxi ecom listing-check \
  --platform 1688 \
  --seller-7star \
  --from-json "products/881234567890/source-1688.json"
```

### 4. Shopify 多变体导入表构建与校验（`hoxi ecom shopify-csv`）

```bash
# 生成带 .99 心理定价与划线价（Compare At Price）的标准 26 列 Shopify 商品导入 CSV
hoxi ecom shopify-csv \
  --title "Merino Wool Travel Hoodie" \
  --vendor "NorthPeak" \
  --option1 "Size" \
  --variants "S:NP-HD-S:68:40,M:NP-HD-M:68:80,L:NP-HD-L:68:60" \
  --psych-99 \
  --compare-discount-pct 20 \
  --out "output/shopify-import.csv"

# 校验已有 Shopify 导入 CSV 的必填列、Handle 规范、划线价逻辑与重复 SKU
hoxi ecom shopify-csv --in "output/shopify-import.csv"
```

---

## 三、标准运营 SOP 组合范式

1. **1688 选品核价 → Amazon / TikTok Shop 定价立项**：
   - 用隔离浏览器（`hoxi_browser`）或 `hoxi_tools` 采集 1688 阶梯价、克重与外箱体积 + 目标站点竞品售价与差评痛点 → 运行 `hoxi ecom profit` 输出 100/500/1000 件阶梯到岸成本、保本 ACOS/ROAS 与目标净利率建议售价 → 用 `hoxi doc write-xlsx` 生成带公式的立项测算表。
2. **高转化 Listing / 带货脚本撰写 → 零违规交付**：
   - 提炼竞品差评痛点撰写五点描述 / TikTok 脚本 → 必须运行 `hoxi ecom listing-check` 验证 `passed: true` 且采用返回的 `trimmedSearchTerms`（确保严格 `<= 249` UTF-8 字节、无重复词、无 EPA/FDA/导流违禁词） → 需要主图/场景图时调用 `hoxi image generate`。
3. **周度广告优化复盘**：
   - 读取用户上传的广告搜索词报表 → 运行 `hoxi ecom ads-audit --out output/weekly-ads-plan.xlsx` → 输出「本周止血否词清单 + 降竞价清单（附精确目标出价） + 自动转精准拓词清单」。
4. **1688 店铺抓品体检 → 轻铺 AI MCP（`qingpu-mcp`）修图生图 → 多渠道上品**：
   - 通过绑定独立环境的 `hoxi_browser` 抓取 1688 店铺商品（标题、`alicdn.com` 原图、属性、SKU、阶梯价与包装尺寸克重）落盘至 `products/<offer-id>/source-1688.json` → 运行 `hoxi ecom listing-check --platform 1688 --seller-7star --from-json ...` 诊断 7 星质量短板 → 调用已授权 `qingpu-mcp` 的 `sync_source_product`、`edit_product_image`（`image-bg-remove` 抠纯白底 / `image-enhancer` 超清 / `image-to-image` 改图）与 `generate_ecommerce_image`（覆盖 `1688`、`ozon`、`wb`、`amazon`、`tiktok`、`shopee`、`aliexpress`、`temu`、`taobao`）并通过 `get_task_result` 拉回 COS 成图 → 目标为 `ozon` / `wb` 时直接走 `enqueue_pipeline` → `resolve_elicitation` → `refresh_and_submit_draft` 提交渠道草稿，其他渠道结合 `hoxi ecom` 与 `hoxi_browser` 预填后台草稿并经人工确认后发布。
