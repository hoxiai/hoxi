# Hoxi 对话富交互视图规范（`hoxi.ui/v1`）

本文档定义 Hoxi 对话时间线的通用结构化展示原语与反向引用交互规范，覆盖**多图自动画廊与 A/B 对比**、**工具结构化视图（`uiView` 与可扩展适配器注册表）**以及**克制的声明式 Markdown 卡片块**。

---

## 1. 设计原则

1. **通用原语，零业务硬编码**：Core 层只提供领域无关的基础视觉原语（实体卡片组、KPI 指标条、多图画廊/对比、链接与扫码回执），不耦合单一垂直行业字段；具体行业/应用通过注册表按需映射。
2. **工具确定性数据优先于模型复述**：工具或 MCP 返回的真实数据直接驱动卡片渲染，避免模型二次转抄导致价格、编号或图片地址失真，同时节省输出 Token 并加快首屏呈现。
3. **与 `hoxi-context` v1 双向闭环**：用户从外部/面板勾选条目带入对话时生成 `hoxi-context` 胶囊；在对话流中浏览卡片、多图或指标时，也可一键勾选或点选，反向封装为标准 `hoxi-context` v1 胶囊送回输入框，实现连续多轮筛选与精修。
4. **窄屏优先与只读快照语义**：网格统一使用容器宽度自适应列（`auto-fill`），在完整对话区呈多列网格，在窄侧栏/浮窗中自动折为紧凑单列/双列；对话流中的卡片明确为「执行时刻只读快照」，重编辑仍交由工作区文件或应用主视图完成。
5. **安全与导出降级**：所有链接严格限制 `http(s)://`，图片地址校验协议与相对路径防穿越；卡片按钮仅触发只读预览、跳转或带入输入框草稿，绝不绕过桌面端审批门控。会话导出与分享长图自动将声明式卡片块转为可读 Markdown 列表，避免暴露原始 JSON。

---

## 2. 多图自动画廊与 A/B 对比（零协议负担）

无需模型学习任何新语法，以下两种场景自动升级为 `MediaGalleryView`（[MediaGalleryView.tsx](../web/src/components/timeline/MediaGalleryView.tsx)）：

1. **连续图片附件**：助手单轮输出（含 `hoxi_image` 等媒体工具透出的结果）中连续出现 `>= 2` 张图片 `file` part 时，`groupAssistantParts` 自动合并为 `media-gallery` 渲染项（单张图片保持原有单图大图样式）。
2. **连续 Markdown 图片段落**：同一段落仅包含 `>= 2` 张 `![alt](src)` 图片，或连续多个纯单图段落相邻时，`MarkdownBlocks` 自动收拢为统一响应式缩略图网格。

**内置交互能力**：
- 点击缩略图打开放大预览（本地文件支持一键在检查器定位原文件）；
- 勾选 1~N 张图后点击「带入对话」，以 `hoxi-context` v1（`entityType: "image"`）胶囊送入输入框；
- 恰好 2 张图或勾选任意 2 张图时，可一键切换 **A/B 滑块对比模式**，左右拖动滑块直观对比前后效果。

---

## 3. 结构化展示契约（`hoxi.ui/v1`）

类型定义位于 [rich-view-registry.ts](../web/src/store/rich-view-registry.ts)，解析与校验位于 [rich-view-spec.ts](../web/src/components/timeline/rich-view-spec.ts)，统一由 [StructuredRichView.tsx](../web/src/components/timeline/StructuredRichView.tsx) 渲染：

```json
{
  "schemaVersion": "hoxi.ui/v1",
  "title": "Candidate Comparison",
  "subtitle": "Top 3 verified options",
  "badge": "3 items",
  "badgeTone": "accent",
  "promote": true,
  "layout": "grid",
  "note": "Read-only snapshot at tool run",
  "metrics": [
    { "label": "Average Margin", "value": "38.5%", "delta": "+4.2%", "tone": "ok" }
  ],
  "entities": [
    {
      "id": "item-101",
      "title": "Sample Item A",
      "subtitle": "Category · Key summary",
      "badge": "Ready",
      "badgeTone": "ok",
      "thumbnailUrl": "https://example.com/thumb.jpg",
      "entityType": "item",
      "metrics": [
        { "label": "Price", "value": "$29.90", "tone": "accent" },
        { "label": "Cost", "value": "$8.40" }
      ],
      "tags": ["Featured"],
      "url": "https://example.com/item-101",
      "ref": { "id": "item-101", "uri": "hoxi://app/item/101" }
    }
  ],
  "gallery": [
    { "src": "https://example.com/preview-1.png", "alt": "Variant A", "badge": "v1" }
  ],
  "links": [
    { "label": "Open Detail", "url": "https://example.com/detail", "tone": "accent" }
  ]
}
```

**有界容量与安全校验**：
- 单视图上限：最多 12 个指标、24 张实体卡（默认首屏折叠展示前 6 张，可展开全部）、24 张画廊图、6 个动作链接；
- 色调枚举：`neutral` | `accent` | `ok` | `warn` | `danger`（兼容常见语义别名如 `success` / `warning` / `error`）；
- `url` 仅接受 `http://` / `https://`；`thumbnailUrl` / `gallery[].src` / `qrImage.src` 仅接受 `http(s)://`、`/api/hoxi/{attachments,file/content,intake/portal/attachments}`、`data:image/*` 或无 `..` 穿越的工作区相对路径。

---

## 4. 两条接入通道

### 4.1 工具结构化输出与扩展注册表（首选）

`resolveToolRichView(part)` 按以下优先级解析已完成工具的富视图：

1. **显式 `uiView` 字段**：工具返回的 `metadata.uiView`、`metadata.structuredContent.uiView`，或 JSON 输出中的 `uiView` / 根对象 `schemaVersion: "hoxi.ui/v1"`。
2. **扩展解析器注册表**：扩展模块可通过 `registerToolRichViewResolver(id, resolver, priority)` 注册面向特定领域的自动适配器（例如将标准业务 MCP 返回自动映射为通用 `StructuredRichViewSpec`），无需向 Core 引入垂类耦合或循环依赖。

**展示位置**：
- **工具展开态**（[ToolOutputView.tsx](../web/src/components/timeline/ToolOutputView.tsx)）：自动提供「卡片 / JSON」切换栏，默认展示可读富卡片，随时切回原始 `JsonTreeView`。
- **主时间线直出**：当 `promote: true` 时，`groupAssistantParts` 在折叠的 `ContextActivityGroupView` 下方直接透出该富视图（单活动组上限 2 张，避免刷屏）。

### 4.2 声明式 Markdown 代码块（多源归纳补充）

当模型综合多份来源（网页、文档、多工具结果）输出最终横向对比时，可使用以下轻量 fenced block，由 [RichBlockCard.tsx](../web/src/components/timeline/RichBlockCard.tsx) 渲染（支持「卡片 / 表格 / 源码」切换、一键复制与解析失败回退源码 + 「让 AI 修复」）：

- ```` ```hoxi-cards ```` / ```` ```cards ````：支持完整 `StructuredRichViewSpec` 对象或实体数组 `[{ title, subtitle, metrics, ... }]`。
- ```` ```hoxi-metrics ```` / ```` ```metrics ````：支持 `{ title, metrics }` 或指标数组 `[{ label, value, delta, tone }]`。

导出（[session-export.ts](../web/src/components/timeline/session-export.ts)）与分享长图（[share-image-model.ts](../web/src/components/share/share-image-model.ts)）统一经 `replaceRichFencesForReadableExport` 将合法块转换为干净的可读列表，不暴露原始 JSON。
