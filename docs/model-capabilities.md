# 统一模型能力词表与业务槽位规范（`ModelCapability` / `ModelSlot`）

本文定义 Hoxi 对接 OpenAI 兼容网关（`GET {baseUrl}/v1/models`）与本机 `.hoxi/models.json` 时的统一模型能力枚举、业务槽位（`ModelSlot`）过滤规则、历史 `modality` 兼容推导以及多模态图像编辑 / 图片翻译本地网关接口。

---

## 一、标准能力词表（`ModelCapability`）

一个模型可声明多项能力（`capabilities: string[]`），底座定义位于 [`internal/llm/capabilities.go`](../internal/llm/capabilities.go)，前端同构镜像位于 [`web/src/store/model-capabilities.ts`](../web/src/store/model-capabilities.ts)：

| 能力枚举值 | 语义 |
| :--- | :--- |
| `chat` | 通用对话 / 编码 / 工具调用 |
| `vision` | 图像理解与视觉输入（纯文本模型严禁拥有此能力） |
| `text_translate` | 结构化文本 / 文案翻译 |
| `image_generate` | 文生图 |
| `image_edit` | 图生图改图、蒙版局部重绘（Inpaint） |
| `image_translate` | 图片内文字翻译，配套子能力 `image_translate.output_modes` |
| `video` | 视频生成 |
| `tts` | 语音合成 |
| `asr` | 语音转写 |
| `embedding` | 向量嵌入 |
| `rerank` | 检索结果重排 |
| `moderation` | 内容审核 |

### `image_translate.output_modes` 子能力

- `raster`：直接输出翻译后的成品栅格图（如 `qwen-mt-image`）。
- `editable_scene`：返回带背景底图与可编辑文字图层的结构化场景（如 `alimt-image-translate`、`image-translate-editor`）。

---

## 二、业务槽位与严格过滤（`ModelSlot`）

所有设置项下拉与运行时选模均按槽位严格过滤；当本地无满足该槽位能力的模型时，返回空候选并提示缺少匹配能力，**绝不静默塞入不兼容模型**。

| 业务槽位 (`ModelSlot`) | 角色配置键 (`~/.hoxi/media-roles.json`) | 必须满足的能力条件 |
| :--- | :--- | :--- |
| `chat` | 全局主对话 / 轻量模型 | `chat` |
| `vision` | `visionModel` | `vision`（严禁混入纯文本模型） |
| `text_translate` | `textTranslateModel` | `text_translate` |
| `image_generate` | `imageModel` | `image_generate` |
| `image_edit` | `imageEditModel` | `image_edit`（未显式配置时，若存量 `imageModel` 具备 `image_edit` 能力则平滑继承，否则择优匹配 `image_edit` 候选） |
| `image_translate_raster` | `imageTranslateRasterModel` | `image_translate` + `raster` |
| `image_translate_editor` | `imageTranslateEditorModel` | `image_translate` + `editable_scene` |
| `video` | `videoModel` | `video` |
| `tts` | `speechModel` | `tts` |
| `asr` | `transcribeModel` | `asr` |
| `embedding` | `embeddingModel` | `embedding`（已接入本地 `EmbedTexts` 检索链路） |
| `rerank` | `rerankModel` | `rerank`（配置与槽位解析就绪，供扩展/后续检索重排管线读取） |

> 注：`visionModel`、`textTranslateModel` 与 `rerankModel` 已完成设置层严格能力过滤、持久化与 `ResolveModelForSlot` 解析；主对话图文理解仍默认由当前会话模型承担。

---

## 三、向后兼容推导与排除规则

当上游网关 `/v1/models` 尚未返回标准 `capabilities` 数组时，按以下规则无损推导：

1. **工具/内部排除项**：`modality` 为 `crawl`/`crawler`/`utility`/`none`，或模型 ID 命中 `collector-*`、`codex-auto-review`、`crawl-*` → 归一化为 `[]`（不进入任何业务槽位）。
2. **`translate_image`**（或模型名含 `alimt-image-translate` / `image-translate-editor`）→ `["image_translate"]` + `output_modes: ["raster", "editable_scene"]`。
3. **`translate_image_task`**（或模型名含 `mt-image` / `image-translate`）→ `["image_translate"]` + `output_modes: ["raster"]`。
4. **`image`** → `["image_generate"]`；若模型名命中 `gpt-image`、`dall-e-2`、`imagen`（排除仅生图的 `imagen-*-generate*`）、`qwen-image`、`kontext`、`inpaint`、`flux-fill`、`seedream`、`-edit`、对话式 Gemini 图像模型或声明支持图像输入，同时具备 `image_edit`。
5. **`vision`** → `["chat", "vision", "text_translate"]`。
6. **`text`** → `["chat", "text_translate"]`（明确不具备 `vision`）。

### `.hoxi/models.json` 持久化兼容

为保持与 OpenCode 配置中对象形态 `"capabilities": { toolcall, reasoning, attachment, input }` 的双向兼容，Hoxi 将归一化后的标准能力字符串数组持久化在 `"capabilityTags"` 字段、子能力持久化在 `"imageTranslate": { "outputModes": [...] }` 与 `"modality"` 字段；读取端同时兼容 `"capabilityTags"` 与原始字符串数组形态的 `"capabilities"`。

---

## 四、本机多模态网关接口（桌面端专属，远程隧道一律 403）

| 路由 | 方法 | 说明 |
| :--- | :--- | :--- |
| `/api/hoxi/media/capabilities` | `GET` | 返回各媒体能力布尔值（含 `hasImageEditModel`、`hasImageTranslateRasterModel`、`hasImageTranslateEditorModel`）、分槽位候选 `slots` 与当前角色偏好 `roles` |
| `/api/hoxi/media/roles` | `GET` / `PUT` | 读写 `~/.hoxi/media-roles.json`（严格白名单校验字段名，未知字段返回 `400`） |
| `/api/hoxi/media/image` | `POST` | 支持 `slot: "image_edit"`、`maskDataUrl`、`idempotencyKey`、`promptKind`（如 `image_inpaint`）。当携带蒙版或指定 `image_edit` 时，路由至 `image_edit` 槽位并优先调用上游异步任务端点 `POST {v1Base}/images/edits/tasks`（附 `Idempotency-Key` 与 `X-Prompt-Kind`）轮询 `GET {v1Base}/tasks/{id}`；遇 `404`/`405`/`501` 自动平滑回退至 `/v1/images/edits` 与 `/v1/chat/completions` |
| `/api/hoxi/media/image-translate` | `POST` | 解析 `image_translate_editor` / `image_translate_raster` 槽位模型，优先调用上游 `POST {v1Base}/images/translations`（遇 `404`/`405`/`501` 回退至 `POST {rootBase}/ai/translate/image`），兼容解析 `data.scene` 与 `JSON.parse(data.template_json)`，并在 Go 后端通过 SSRF 防护客户端将 `scene` 中所有 `backgroundImg` 与 `type === "image"` 的远端 `http(s)://` 图层拉取内联为 `data:image/...` 后返回 |

---

## 五、关键文件索引

- **Go 底座与领域层**：[`internal/llm/capabilities.go`](../internal/llm/capabilities.go)、[`internal/llm/image_translate.go`](../internal/llm/image_translate.go)、[`internal/provider/model_slots.go`](../internal/provider/model_slots.go)、[`internal/provider/image_edit_tasks.go`](../internal/provider/image_edit_tasks.go)、[`internal/provider/image_translate.go`](../internal/provider/image_translate.go)
- **Go 网关层**：[`internal/gateway/media_routes.go`](../internal/gateway/media_routes.go)、[`internal/gateway/media_roles_route.go`](../internal/gateway/media_roles_route.go)、[`internal/gateway/media_image_translate_route.go`](../internal/gateway/media_image_translate_route.go)
- **前端与设置 UI**：[`web/src/store/model-capabilities.ts`](../web/src/store/model-capabilities.ts)、[`web/src/store/media-options.ts`](../web/src/store/media-options.ts)、[`web/src/components/models/ModelsRoleTab.tsx`](../web/src/components/models/ModelsRoleTab.tsx)、[`web/src/components/models/FetchedModelsModal.tsx`](../web/src/components/models/FetchedModelsModal.tsx)
