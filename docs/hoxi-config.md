# Hoxi 项目配置：`hoxi.json`

默认 Go 原生引擎支持在**会话实际执行目录根部**放置 `hoxi.json`，与代码一起提交。它不是 OpenCode 的配置，也不会传给外部 CLI。文件只读加载，不会自动生成、迁移、改名或写回到 `~/.hoxi/models.json`。

## 示例

```json
{
  "version": 1,
  "permission": {
    "edit": "allow",
    "bash": "allow",
    "webfetch": "ask"
  },
  "skills": { "paths": [".hoxi/skills"] },
  "mcp": {
    "project-tools": {
      "type": "local",
      "command": ["node", "./tools/launch_mcp.mjs", "example"],
      "enabled": true
    },
    "documentation": {
      "type": "remote",
      "url": "https://example.com/mcp",
      "enabled": true
    }
  }
}
```

示例脚本由项目自行提供，Hoxi 不安装 Node、依赖或 MCP Server，也不会因打开项目就启动 MCP。`$schema` 是可选的编辑器提示元数据，不下载或执行远程内容。本仓库提供 [`schemas/hoxi.schema.json`](../schemas/hoxi.schema.json)，可复制到项目并用相对 `$schema` 引用；尚未承诺公网 schema URL 已部署。

## 格式与读取时机

- 文件名为 `hoxi.json`，支持 JSON、`//` / `/* */` 注释和尾逗号；不扫描 `hoxi.jsonc`、`.hoxi/hoxi.json` 或祖先目录。
- 文件最大 1 MiB，必须是普通文件，不接受文件符号链接。缺失表示使用既有默认配置；语法、类型、未知字段、非法路径错误会阻止原生轮次，Skills 接口也会报告错误。不会把损坏的共享文件移走。
- 原生轮次在开始时读取；工具共享本轮配置快照，下一轮重新加载。子任务继承宿主门控并重新验证项目配置。Skills 列表与详情重新检查文件，防止缓存保留已禁用技能。
- 隔离 worktree 使用 worktree 自己的 `hoxi.json` 和相对路径；不会偷偷执行主工作区未提交的脚本。应先提交配置和脚本，让新 worktree 获得它们。

原生 Build 模式默认按需加载宿主工具。模型用 `load_host_tool` 取得参数和操作约束，再用固定入口 `call_host_tool(name, args)` 调用。加载说明只追加到历史消息末尾，审批与审计使用实际工具名。项目可设置 `"hostToolLoading": "eager"`，从第一步起提供独立工具名和完整参数。

### 服务商缓存与自动摘要

个人模型配置 `~/.hoxi/models.json` 的 `provider.<提供商>.options` 支持以下选项，同名的 `models.<模型>.options` 优先。默认生成请求继续使用各服务商现有的缓存策略。

| 选项 | 适用范围与效果 |
| --- | --- |
| `"promptCacheRetention": "24h"` | 官方 OpenAI Responses 支持长保留期的模型会收到 `prompt_cache_retention` |
| `"openaiCompatCacheControl": true` | 通过 OpenAI Chat Completions 转发 Claude 且支持顶层 `cache_control` 的兼容网关。OpenRouter 转发 Claude 已默认携带该字段 |
| `"geminiExplicitCache": false` | 关闭 Gemini 显式缓存。默认情况下，官方 Gemini 2.5、3 系列生成接口的稳定前缀估算达到 32K token 时创建缓存，保留 12 分钟，活跃时续期。Google 对缓存存储按时长收费 |

自动溢出摘要优先复用主模型的热缓存。需要另起摘要请求时，优先使用已配置的 `agent.compaction.model` 或 `small_model`，缺省时尝试当前提供商的经济模型，必要时回到本轮主模型。

## 配置分工与优先级

| 来源 | 用途 |
| --- | --- |
| `~/.hoxi/models.json` | 既有个人模型、Provider、MCP 等本机设置 |
| `<execution-directory>/.hoxi/models.json` | 兼容已有项目本地设置 |
| `<execution-directory>/.mcp.json` | MCP 兼容声明（`mcpServers` 或 `mcp`） |
| `<execution-directory>/hoxi.json` | 项目共享 Skills、MCP 和权限约束 |
| `~/.hoxi/approval-policy.json` | 本机最终审批策略，不受项目放宽 |

原生 MCP 同名服务按上述顺序覆盖，名字大小写不敏感。旧配置对象递归合并，数组/标量替换；`hoxi.json` 中的完整服务声明替换旧声明，用 `{"enabled": false}` 禁用继承服务。远程服务仅当本机已配置的同名服务具有完全相同 URL 时，才继承本机 headers/OAuth；替换 URL 不得把旧凭据发给新地址。项目声明不支持 `null` 删除语义。MCP 非敏感 `environment` 可随项目共享，密钥应由本机环境或凭据存储提供。`command` 是 argv 数组，不经过 Shell 字符串执行，相对脚本按会话执行目录解析。

`GET /api/hoxi/provider/config?directory=...` 可在本机查看有效配置；必须携带本机网关令牌，保持在远程隧道白名单之外。现有设置页面的 PATCH 仍写本机旧配置，不修改 `hoxi.json`。有效项目视图不能作为全局保存输入，否则会污染其他项目。

## 权限语义

- `allow`：项目没有额外限制；**仍完整经过本机 Hoxi 审批**，不表示免确认，不覆盖手动模式、密钥、破坏性命令、电脑授权、无人值守硬拒绝或沙箱。
- `ask`：本轮工具调用必须进入显式审批（可复用用户授予的精确会话授权），无人值守时拒绝。
- `deny`：项目拒绝该工具调用，不会通过“允许一次”放行。
- `edit` 同时约束 `write`、`edit`、`multiedit`、`apply_patch`；可以按具体工具进一步配置。`*` 为默认项，具体项覆盖默认项。
- `skill` / `task` 支持标量或名称规则对象，供现有技能曝光/加载及子任务策略处理；其余权限仅支持标量，暂不支持 Shell 命令或 URL 通配规则对象。
- 项目声明的本地 MCP 首次列举/调用可能启动代码，因此额外进入显式审批；即便工具列举被标为只读，也不能无授权执行仓库程序。精确请求可用已有会话授权复用，关闭会话后不保留。
- `hoxi.json` 本身是控制执行的受保护文件：编辑须确认，不提供永久或会话文件授权；命令沙箱禁止写入它。

## Skills 路径

`skills.paths` 是附加目录列表，每个目录下放 `<skill-name>/SKILL.md`。相对路径以执行目录为基准，只允许项目内部字面量路径；禁止 `..` 越界、glob、`~`、指向项目外部的符号链接及技能文件逃逸。默认 `.hoxi/skills` 等兼容扫描仍保留，自定义目录优先去重。同名技能以首次发现为准。

## 当前范围

版本 1 仅支持 `$schema`、`version`、`permission`、`skills.paths`、`mcp`。不支持项目 `provider` / `apiKey`、插件、审批模式、默认模型或其他未接入的行为字段，未知项明确报错；模型和 Agent 等继续使用已有本机设置与 Markdown 目录。不会自动删除任何 OpenCode 配置。
