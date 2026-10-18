# Hoxi 跨软件会话接管与本地 CLI 桥接落地方案 (`HANDOFF_AND_CLI_BRIDGE.md`)

> **核心定位**：不仅支持一键发现并桥接本地已安装的 AI CLI 工具（**Grok CLI**、**Claude Code**、**OpenAI Codex**）的模型与凭证，更将**跨软件会话接管与任务无缝续做（Cross-Tool Session Handoff & Task Continuation）**作为一等公民能力落盘——自动扫描本机外部 CLI 的历史会话，按工程目录（`cwd`）精准匹配，将外部会话的完整对话轮次（User Prompt、Reasoning 思考链、Tool Call/Result 工具执行记录、Assistant 回复）、任务摘要（Summary）与执行计划（Plan）无损物化到 OpenCode 会话存储与 Hoxi 8 维结构化记忆库中，实现跨工具一键接力续做任务。

---

## 1. 总体架构与四层解耦流水线

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ Layer 1: External Session & CLI Scanners (只读无侵入探测层)                   │
│  ├─ GrokAdapter   (~/.grok/sessions/session_search.sqlite + summary.json     │
│  │                 + chat_history.jsonl / updates.jsonl + plan.json          │
│  │                 + ~/.grok/auth.json & models_cache.json)                  │
│  ├─ ClaudeAdapter (~/.claude/projects/<dash-cwd>/*.jsonl                     │
│  │                 + ~/.claude/settings.json & Keychain/credentials)         │
│  └─ CodexAdapter  (~/.codex/state_5.sqlite threads + rollout *.jsonl[.zst]   │
│                    + ~/.codex/auth.json & config.toml*)                      │
├──────────────────────────────────────────────────────────────────────────────┤
│ Layer 2: Canonical Session & Tool IR (统一会话中间表示层)                     │
│  ├─ ExternalSessionSummary (ID, Source, CWD, Title, Summary, Branch, Time)   │
│  ├─ ExternalSessionDetail  (Turns[], PlanSteps[], ChangedFiles[], GitContext)│
│  └─ Tool Call Normalization:                                                 │
│     • Read/View -> `read`       • Edit/Patch -> `edit`                       │
│     • Write/Create -> `write`   • Bash/Exec  -> `bash`                       │
│     • Grep/Search -> `grep`     • Glob/List  -> `glob`                       │
├──────────────────────────────────────────────────────────────────────────────┤
│ Layer 3: OpenCode Session Materializer & 8-Dim Handoff Memory (物化与续做层)  │
│  ├─ 1. 创建目标会话: POST /session?directory=<target-cwd>                     │
│  ├─ 2. 注入历史消息与 Part: 写入 OpenCode SQLite (`opencode.db` message/part) │
│  │     生成单调递增 msg_* / prt_* ID，保留完整时间线、ThoughtBox 与 ToolCard  │
│  ├─ 3. 生成 8 维交接记忆: `.opencode/memory/sessions/<new-session-id>.md`     │
│  │     并通过 `.opencode/plugins/hoxi-memory.js` 自动注入 System Prompt      │
│  └─ 4. Git 分支与工作树对齐: 检测源会话 `git_branch` / `head_commit` 状态     │
├──────────────────────────────────────────────────────────────────────────────┤
│ Layer 4: In-Process Local CLI Protocol Bridge (本地网关协议桥接层)            │
│  └─ `/api/hoxi/bridge/{toolID}/v1/*`                                         │
│     • 动态读取本地 CLI 最新 Token，零明文拷贝落盘                             │
│     • 注入专有握手头 (`X-XAI-Token-Auth`, `x-grok-client-version`,            │
│       `x-grok-model-override`)                                               │
│     • Stream-to-JSON 双向自适应转换（解决上游强制 `stream: true` 限制）        │
└──────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 本机三大 CLI 会话与凭证格式逆向规范

### 2.1 Grok CLI (`~/.grok`)
1. **会话索引与目录结构**：
   - 全局全文检索库：`~/.grok/sessions/session_search.sqlite`（表 `session_docs(session_id, cwd, updated_at, title, content)`），毫秒级按 `cwd` 或关键词检索。
   - 会话目录：`~/.grok/sessions/<url-encoded-cwd>/<session-id>/`（其中 `<url-encoded-cwd>` 将 `/` 编码为 `%2F`）。
2. **单会话核心文件**：
   - `summary.json`：
     - `info.id`、`info.cwd`、`generated_title`、`session_summary`、`created_at`、`updated_at`、`current_model_id`
     - `git_root_dir`、`head_commit`、`head_branch`
     - `last_turn_summary`（最后轮次摘要）、`last_recap`（阶段性回顾）
   - `plan.json`：结构化任务计划列表（含 step title / status）。
   - `chat_history.jsonl`：按行存储的对话记录，包含 `type: "user" | "reasoning" | "assistant" | "tool_call" | "tool_result"`（或 OpenAI 兼容 message 结构）。
   - `updates.jsonl`：ACP 协议 `session/update` 增量流（当 `chat_history.jsonl` 缺失或仅含部分记录时，可聚合 `user_message_chunk`、`agent_thought_chunk`、`agent_message_chunk`、`tool_call` 重建完整对话）。
3. **凭证与模型缓存**：
   - `~/.grok/auth.json`：`{"https://cli-chat-proxy.grok.com": {"type": "api", "key": "xai-..."}}`
   - `~/.grok/models_cache.json`：`models: ["grok-4.7", "grok-4.7-build-fast", "grok-4.6", "grok-4.5"]`，`default_model: "grok-4.7"`
   - `~/.grok/version.json`：`{"latest_version": "1.0.52"}`
   - **上游专有协议要求**：
     - Base URL：`https://cli-chat-proxy.grok.com/v1`
     - 必需请求头：
       - `Authorization: Bearer <key>`
       - `X-XAI-Token-Auth: xai-grok-cli`
       - `x-grok-client-version: <latest_version>`
       - `x-grok-model-override: <request.model>`（每次请求动态匹配 body 中的 `model`）
     - 流式约束：`POST /v1/chat/completions` 必须传 `"stream": true`，非流式请求会返回 `400 Streaming is required`。

### 2.2 Claude Code (`~/.claude`)
1. **会话存储结构**：
   - 路径：`~/.claude/projects/<dash-encoded-cwd>/<session-id>.jsonl`
   - 每行 JSON 事件包含：
     - `type`: `"user"` | `"assistant"` | `"custom-title"` | `"ai-title"` | `"summary"`
     - `cwd`: 精确的原始工作目录绝对路径（无需反解目录名中的 `-`）
     - `gitBranch`: 会话当时的 Git 分支名
     - `sessionId`: 会话 UUID
     - `timestamp`: ISO-8601 时间戳
     - `message`:
       - `role`: `"user"` | `"assistant"`
       - `content`: 字符串，或数组 `[ { "type": "text", "text": "..." }, { "type": "thinking", "thinking": "..." }, { "type": "tool_use", "id": "...", "name": "...", "input": {...} }, { "type": "tool_result", "tool_use_id": "...", "content": "..." } ]`
2. **凭证与环境配置**：
   - `~/.claude/settings.json`：可能包含 `env.ANTHROPIC_BASE_URL`、`env.ANTHROPIC_AUTH_TOKEN`、`env.ANTHROPIC_API_KEY`、`model`。
   - `~/.claude/.credentials.json` 或 macOS Keychain（`security find-generic-password -s "Claude Code-credentials" -w`）：包含 `claudeAiOauth`。

### 2.3 OpenAI Codex (`~/.codex`)
1. **会话存储结构**：
   - SQLite 索引库：`~/.codex/state_5.sqlite`（或 `~/.codex/state_*.sqlite` 最新版本）中的 `threads` 表：
     - 字段：`id`, `rollout_path`, `created_at`, `updated_at`, `source`, `model_provider`, `cwd`, `title`, `git_sha`, `git_branch`, `model`, `reasoning_effort`, `first_user_message`, `preview`。
   - 对话轨迹文件：`rollout_path` 指向 `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`（或 `.jsonl.zst`，可通过 `zstd -dc` 解压读取）：
     - `type: "event_msg"` (`payload.type`: `"user_message"`, `"agent_message"`, `"agent_reasoning"`)
     - `type: "response_item"` (`payload.type`: `"message"`, `"reasoning"`, `"function_call"`, `"function_call_output"`)
2. **凭证与提供商配置**：
   - `~/.codex/auth.json`：
     - `OPENAI_API_KEY`: API Key 模式
     - `tokens`: ChatGPT OAuth 模式（`access_token`, `refresh_token`, `id_token`, `account_id`），其 OAuth `client_id` (`app_EMoamEEZ73f0CkXaXp7hrann`) 与 OpenCode 内置 `openai` OAuth 插件完全同源，可直接通过 `PUT /auth/openai` 注入 OpenCode。
   - `~/.codex/config.toml`：包含 `model`, `model_provider` 及自定义 `[model_providers.<id>]` 的 `base_url`。

---

## 3. Canonical Session IR 与 OpenCode 无损物化机制

### 3.1 统一会话中间表示 (`NormalizedExternalSession`)
```go
type ExternalToolID string

const (
    ExternalToolGrok   ExternalToolID = "grok-cli"
    ExternalToolClaude ExternalToolID = "claude-code"
    ExternalToolCodex  ExternalToolID = "codex-cli"
)

type ExternalSessionSummary struct {
    ID           string         `json:"id"`
    Source       ExternalToolID `json:"source"`
    SourceLabel  string         `json:"sourceLabel"`
    CWD          string         `json:"cwd"`
    Title        string         `json:"title"`
    Summary      string         `json:"summary,omitempty"`
    Model        string         `json:"model,omitempty"`
    GitBranch    string         `json:"gitBranch,omitempty"`
    GitCommit    string         `json:"gitCommit,omitempty"`
    CreatedAt    int64          `json:"createdAt"` // Unix ms
    UpdatedAt    int64          `json:"updatedAt"` // Unix ms
    TurnCount    int            `json:"turnCount,omitempty"`
    MatchesCWD   bool           `json:"matchesCwd"`
}

type NormalizedToolCall struct {
    CallID string                 `json:"callID"`
    Tool   string                 `json:"tool"` // read | edit | write | bash | grep | glob
    Title  string                 `json:"title"`
    Input  map[string]interface{} `json:"input"`
    Output string                 `json:"output"`
    Status string                 `json:"status"` // completed | error
}

type NormalizedTurn struct {
    Role      string               `json:"role"` // "user" | "assistant"
    Timestamp int64                `json:"timestamp"`
    Text      string               `json:"text"`
    Reasoning string               `json:"reasoning,omitempty"`
    Tools     []NormalizedToolCall `json:"tools,omitempty"`
    ModelID   string               `json:"modelID,omitempty"`
}
```

### 3.2 工具调用语义映射表 (`normalizeExternalToolName`)
将不同 CLI 的内置工具名统一映射为 Hoxi `ToolCard.tsx` 原生支持渲染的 OpenCode 标准工具名：
- 文件读取：`read`, `Read`, `view_file`, `read_file`, `cat` $\rightarrow$ `"read"`
- 文件编辑：`edit`, `Edit`, `MultiEdit`, `replace_file_content`, `apply_patch`, `patch` $\rightarrow$ `"edit"`
- 文件写入：`write`, `Write`, `write_file`, `create_file` $\rightarrow$ `"write"`
- 终端命令：`bash`, `Bash`, `exec_command`, `shell`, `run_command`, `terminal` $\rightarrow$ `"bash"`
- 内容搜索：`grep`, `Grep`, `search`, `ripgrep`, `rg` $\rightarrow$ `"grep"`
- 文件查找：`glob`, `Glob`, `list_dir`, `ls`, `find` $\rightarrow$ `"glob"`

### 3.3 物化到 OpenCode 数据库与 8 维记忆库 (`MaterializeExternalSession`)
当用户点击「接管会话并继续任务 (Take Over & Continue)」时，Go 后端执行以下原子步骤：
1. **确定目标工作目录 (`targetDir`)**：
   - 默认使用外部会话记录的 `cwd`（若该目录存在），或当前 Hoxi 激活的工程目录。
   - 若用户开启了 Worktree 沙箱模式，自动为新会话创建对应的 `.hoxi/worktrees/<sessionID>`。
2. **创建原生 OpenCode 会话**：
   - 调用 `POST http://127.0.0.1:<openCodePort>/session?directory=<targetDir>`，标题设为 `[Grok/Claude/Codex] <原会话标题>`，获取合法的 OpenCode `SessionInfo`（含 `ses_...` ID）。
3. **写入对话历史 (`message` 与 `part` 表)**：
   - 定位 OpenCode SQLite 数据库 `~/.local/share/opencode/opencode.db`（支持 `$XDG_DATA_HOME/opencode/opencode.db`）。
   - 将 `NormalizedTurn[]` 转换为 OpenCode 的 `message` 行与 `part` 行（使用单调递增的 `msg_` 与 `prt_` 标识符，确保时间轴严格有序）：
     - `user` 轮次生成 `role: "user"` 的 `message` + `type: "text"` 的 `part`。
     - `assistant` 轮次生成 `role: "assistant"` 的 `message` +：
       - 若含思考过程：生成 `type: "reasoning"` 的 `part`（前端自动渲染为可折叠 `<ThoughtBox />`）。
       - 若含工具调用：为每个工具生成 `type: "tool"` 的 `part`（`state.status = "completed"`，前端自动渲染为带输入输出详情的 `<ToolCard />`）。
       - 若含回复正文：生成 `type: "text"` 的 `part`。
4. **生成并注入 8 维结构化交接记忆 (`.opencode/memory/sessions/<sessionID>.md`)**：
   - 提取源会话的：
     1. `## 1. Goals`（首轮用户需求 + 源会话标题与核心目标）
     2. `## 2. Constraints`（工作目录、源工具、Git 分支与 Commit 约束）
     3. `## 3. Key Facts`（源会话摘要 `session_summary` / `last_recap`）
     4. `## 4. Changed Files`（从历史 `edit` / `write` / `apply_patch` 工具调用中提取的已修改文件列表）
     5. `## 5. Errors & Fixes`（历史工具调用中的错误与修复上下文）
     6. `## 6. Todo`（从 `plan.json` 或最近任务计划提取的待办项）
     7. `## 7. Progress`（最后轮次进展 `last_turn_summary` 与已完成轮次统计）
     8. `## 8. Next Steps`（无缝续做指引：基于上述已落盘的对话历史与代码现状继续执行下一步）
   - 写入 `<targetDir>/.opencode/memory/sessions/<sessionID>.md`。
   - 升级 `.opencode/plugins/hoxi-memory.js`：在 `experimental.chat.system.transform` 钩子中，除了读取 `project.md`，还根据 `input.sessionID` 自动读取 `.opencode/memory/sessions/${input.sessionID}.md` 并作为 `<session_handoff_memory>` 注入 System Prompt！

---

## 4. 后端 HTTP API 与协议桥接契约

### 4.1 跨软件会话发现与接管接口
- `GET /api/hoxi/handoff/sessions?directory=<cwd>&query=<q>&source=<all|grok-cli|claude-code|codex-cli>&limit=50`
  - 返回本机外部 CLI 会话列表（优先置顶与当前 `directory` 匹配的会话，同时支持跨工程全局搜索）：
    ```json
    {
      "sessions": [
        {
          "id": "b1bc6d85-1695-4df4-a7ae-9de75aa9df70",
          "source": "grok-cli",
          "sourceLabel": "Grok CLI",
          "cwd": "/path/to/project",
          "title": "Analyze local CLI session handoff",
          "summary": "...",
          "model": "grok-4.7",
          "gitBranch": "main",
          "gitCommit": "a1b2c3d",
          "createdAt": 1775573432177,
          "updatedAt": 1775574102000,
          "turnCount": 14,
          "matchesCwd": true
        }
      ],
      "sources": {
        "grok-cli": { "installed": true, "sessionCount": 27 },
        "claude-code": { "installed": true, "sessionCount": 639 },
        "codex-cli": { "installed": true, "sessionCount": 1260 }
      }
    }
    ```
- `GET /api/hoxi/handoff/preview?source=<source>&id=<sessionID>&cwd=<encodedOrRawCwd>`
  - 返回外部会话的完整预览详情（`NormalizedTurn[]`、`PlanSteps`、`ChangedFiles`、`Summary`），供用户在接管前在弹窗中实时预览对话内容与任务进度。
- `POST /api/hoxi/handoff/import`
  - 请求体：
    ```json
    {
      "source": "grok-cli",
      "sessionID": "b1bc6d85-1695-4df4-a7ae-9de75aa9df70",
      "sourceCwd": "/path/to/project",
      "targetDirectory": "/path/to/project",
      "switchProject": true
    }
    ```
  - 响应体：
    ```json
    {
      "ok": true,
      "session": { "id": "ses_...", "title": "...", "directory": "..." },
      "importedTurns": 14,
      "memoryPath": ".opencode/memory/sessions/ses_....md",
      "targetDirectory": "/path/to/project"
    }
    ```

### 4.2 本地 CLI 模型与凭证探测/导入接口
- `GET /api/hoxi/local-cli/status`
  - 自动探测本机已安装的 `grok-cli`、`codex-cli`、`claude-code` 的二进制版本、登录状态、可用模型列表以及是否已接入 Hoxi。
- `POST /api/hoxi/local-cli/import`
  - 请求体：`{ "toolID": "grok-cli" | "codex-cli" | "claude-code" }`
  - 行为：
    - `grok-cli`：在 `.hoxi/models.json` 中注册 `grok-cli` 提供商，其 `baseURL` 指向 Hoxi 内部协议桥 `http://127.0.0.1:<port>/api/hoxi/bridge/grok-cli/v1`，并自动导入 `grok-4.7`、`grok-4.7-build-fast`、`grok-4.6`、`grok-4.5` 四个模型。
    - `codex-cli`：将 `~/.codex/auth.json` 的 OAuth 凭证或 API Key 同步至 OpenCode `/auth/openai`（或注册自定义 provider），无需二次登录。
    - `claude-code`：将 `~/.claude/settings.json` 中的 `ANTHROPIC_BASE_URL` + Token 或凭证导入 `.hoxi/models.json`。

### 4.3 本地 CLI 协议转换网关 (`/api/hoxi/bridge/grok-cli/v1/*`)
- `GET /api/hoxi/bridge/grok-cli/v1/models`：动态读取 `~/.grok/models_cache.json` 并返回标准 OpenAI `{ "object": "list", "data": [...] }`。
- `POST /api/hoxi/bridge/grok-cli/v1/chat/completions`：
  - 实时从 `~/.grok/auth.json` 读取最新 Bearer Key，从 `~/.grok/version.json` 读取 `latest_version`。
  - 解析请求 JSON 中的 `model` 与 `stream` 字段，注入 `X-XAI-Token-Auth: xai-grok-cli`、`x-grok-client-version`、`x-grok-model-override: <model>`。
  - 若调用方请求 `"stream": false`（例如模型连通性探测、代码审计或标题总结），网关在转发上游时自动改写为 `"stream": true`，并在内存中聚合 SSE `delta.content` / `delta.reasoning_content` / `delta.tool_calls` 后返回标准非流式 OpenAI JSON 响应；若调用方请求 `"stream": true`，则零缓冲直通转发 SSE 流。

---

## 5. 前端交互与模块化拆分设计（严格遵守单文件 < 500 行红线）

### 5.1 后端新增模块（按职责拆分）
1. `desktop_handoff_types.go`：跨软件会话接管与本地 CLI 桥接的数据结构定义、工具名归一化映射、HTTP 路由分发入口。
2. `desktop_handoff_grok.go`：Grok CLI 会话扫描器（SQLite + JSONL/Updates 解析）、Grok CLI 凭证/模型探测与 `/api/hoxi/bridge/grok-cli/v1/*` 协议桥。
3. `desktop_handoff_claude.go`：Claude Code 会话扫描器（`~/.claude/projects` JSONL 流式解析、Thinking/ToolUse/ToolResult 提取）与环境凭证探测。
4. `desktop_handoff_codex.go`：OpenAI Codex 会话扫描器（`~/.codex/state_*.sqlite` 检索 + `.jsonl`/`.jsonl.zst` 解压解析）与 OAuth/API 凭证桥接。
5. `desktop_handoff_materialize.go`：OpenCode 会话与 SQLite `message`/`part` 写入器 + 8 维交接记忆 `.opencode/memory/sessions/<id>.md` 生成器。

### 5.2 前端新增模块（按职责拆分）
1. `web/src/components/layout/ExternalHandoffModal.tsx`：跨软件会话接管中心弹窗：
   - 支持按来源筛选（All / Grok CLI / Claude Code / Codex）与按当前项目 (`Current Project`) 或全局所有项目 (`All Projects`) 切换。
   - 左侧展示外部会话列表（带来源徽章、Git 分支胶囊、修改时间、轮次计数与匹配当前工程标识）。
   - 右侧实时预览选中会话的完整上下文（任务摘要、8 维提炼预览、历史对话流 User / Reasoning / Tool / Assistant），底部提供一键「接管会话并继续任务 (Take Over & Continue)」按钮。
2. `web/src/components/models/LocalCliBridgeBanner.tsx`：在模型管理页（`ProviderCatalogTab.tsx`）顶部展示本机已安装的 CLI 工具（Grok CLI / Claude Code / Codex），支持一键接入模型与凭证，并可直接跳转打开会话接管中心。
3. `web/src/components/inspector/MemoryBankView.tsx`：升级右侧 `Memory` 面板，支持切换查看「当前会话交接记忆 (`sessions/<id>.md`)」与「项目长期记忆 (`project.md`)」。
4. `web/src/i18n/`：在 `types.ts`、`domain-types.ts`、`en-US` 与 `zh-CN` 中完整新增 `handoff` 领域词条，确保 100% 双语对齐并通过 `npm --prefix web run check:i18n`。
