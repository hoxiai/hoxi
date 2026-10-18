# Antigravity (`agy`) & Claude Code (`claude`) Runner 实测报告（阶段 0）

本文档记录在独立临时目录（非项目仓库、非用户主目录）中对官方 `agy` CLI 与 `claude` CLI 无头流式协议（`stream-json`）的实测结果，作为 `internal/runner/` 的协议契约与单元测试 Fixture 依据。

> **安全声明**：全程未读取、复制或输出任何本地 OAuth 凭证文件（如 `~/.gemini/jetski-standalone-oauth-token`、`~/.claude/.credentials.json`），所有样例均已脱敏。

---

## 一、`agy` CLI 实测结论

### 1. 命令行参数注意事项（关键坑点）
- **错误写法**：`agy -p --input-format stream-json --output-format stream-json ...`
  - 实测报错（退出码 `2`）：
    ```text
    Error: -p took "--input-format" as its prompt, so the intended prompt was left as an argument and ignored.
    Attach the prompt to the flag (-p='your prompt') and move --input-format elsewhere on the command line.
    ```
- **正确写法**：必须显式绑定空字符串 `-p=""`（或将 `-p=""` 放在末尾）：
  ```bash
  agy --input-format stream-json --output-format stream-json --sandbox --mode accept-edits [--model <slug>] [--effort <level>] [--conversation <id>] -p=""
  ```
- **`cmd.Dir` 绑定**：`agy` 启动后首条 `init` 事件中的 `init.cwd` 严格等于进程的 `cmd.Dir`（即会话专属 `.hoxi/worktrees/<session-id>`）。

---

### 2. 输入消息格式与输出事件格式（`stream-json`）

#### 2.1 Stdin 输入格式（NDJSON，每行一条）
`agy` 的 `--input-format stream-json` 要求顶层必须包含 `"event": "user"` 与 `"message"` 对象（若传 `"type": "user"` 会报错 `stream input message is missing the "event" field`）：

```json
{"event":"user","message":{"role":"user","content":"Read hello.txt and reply with its exact contents."}}
```
- `message.content` 支持字符串或内容块数组（`string | Array<ContentBlock>`）。

#### 2.2 Stdout 输出事件格式（真实脱敏样例）

1. **会话初始化事件（`event: "init"`）**
   - `conversation_id` 位于顶层 `conversation_id` 字段（后续所有 `step_update` 和 `result` 均携带同一 `conversation_id`）：
   ```json
   {"event":"init","conversation_id":"ddd4b54a-7996-4819-91d4-0e47de47c6c9","init":{"model":"gemini-3.8-flash-low","cwd":"/path/to/.hoxi/worktrees/ses_01","tools":["ask_custom_permission","ask_permission","ask_question","find_by_name","grep_search","list_dir","multi_replace_file_content","replace_file_content","run_command","view_file","write_to_file"],"permission_mode":"request-review"}}
   ```

2. **用户输入确认步（`step_type: "user_input"`）**
   ```json
   {"event":"step_update","step_update":{"conversation_id":"ddd4b54a-7996-4819-91d4-0e47de47c6c9","step_index":0,"state":"DONE","step_type":"user_input"}}
   ```

3. **规划/思考步（触发工具调用前的 `agent_response`）**
   - 当模型进行内部思考并决定调用工具时，`step_update` 输出 `usage.thinking_tokens` 与耗时；同时完整 `thinking` 文本实时落盘至 `~/.gemini/antigravity-cli/brain/<conversation_id>/.system_generated/logs/transcript_full.jsonl` 对应 `step_index` 的 `thinking` 字段：
   ```json
   {"event":"step_update","step_update":{"conversation_id":"7edcb69b-073e-476b-ab8e-3e2948cf9770","step_index":1,"state":"DONE","step_type":"agent_response","duration_seconds":2.37029,"usage":{"input_tokens":12709,"output_tokens":272,"thinking_tokens":157,"cache_read_tokens":0,"total_tokens":12981}}}
   ```
   对应 `transcript_full.jsonl` 中的推理记录：
   ```json
   {"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-10-18T07:36:47Z","input_tokens":12709,"cache_read_tokens":0,"output_tokens":272,"thinking":"The user wants to run echo sandbox_test in the shell...","tool_calls":[{"name":"run_command","args":{"BypassSandbox":"false","CommandLine":"\"echo sandbox_test\"","Cwd":"\"/path/to/.hoxi/worktrees/ses_01\"","WaitMsBeforeAsync":"2000","toolAction":"\"Echoing sandbox test\"","toolSummary":"\"Echo sandbox test\""}}]}
   ```

4. **工具调用开始与结束（`step_type: "tool"`，`state: "ACTIVE" -> "DONE"`）**
   - 文件读取（`view_file`）：
     ```json
     {"event":"step_update","step_update":{"conversation_id":"ddd4b54a-7996-4819-91d4-0e47de47c6c9","step_index":2,"state":"ACTIVE","step_type":"tool","tool_name":"view_file","tool_info":{"name":"view_file","parameters":{"AbsolutePath":"/path/to/.hoxi/worktrees/ses_01/hello.txt"}}}}
     {"event":"step_update","step_update":{"conversation_id":"ddd4b54a-7996-4819-91d4-0e47de47c6c9","step_index":2,"state":"DONE","step_type":"tool","tool_name":"view_file","duration_seconds":0.008778,"tool_info":{"name":"view_file","parameters":{"AbsolutePath":"/path/to/.hoxi/worktrees/ses_01/hello.txt"},"output":"2 lines, 12 bytes"}}}
     ```
   - 文件写入（`write_to_file`）：
     ```json
     {"event":"step_update","step_update":{"conversation_id":"3067593d-9a8b-4c4d-8256-ea0865411cbd","step_index":2,"state":"ACTIVE","step_type":"tool","tool_name":"write_to_file","tool_info":{"name":"write_to_file","parameters":{"TargetFile":"/path/to/.hoxi/worktrees/ses_01/edit_test.txt"}}}}
     {"event":"step_update","step_update":{"conversation_id":"3067593d-9a8b-4c4d-8256-ea0865411cbd","step_index":2,"state":"DONE","step_type":"tool","tool_name":"write_to_file","duration_seconds":0.160829,"tool_info":{"name":"write_to_file","parameters":{"TargetFile":"/path/to/.hoxi/worktrees/ses_01/edit_test.txt"}}}}
     ```
   - 安全 Shell 执行（`run_command`）：
     ```json
     {"event":"step_update","step_update":{"conversation_id":"7edcb69b-073e-476b-ab8e-3e2948cf9770","step_index":2,"state":"ACTIVE","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","parameters":{"CommandLine":"echo sandbox_test"}}}}
     {"event":"step_update","step_update":{"conversation_id":"7edcb69b-073e-476b-ab8e-3e2948cf9770","step_index":2,"state":"DONE","step_type":"tool","tool_name":"run_command","duration_seconds":0.085779,"tool_info":{"name":"run_command","parameters":{"CommandLine":"echo sandbox_test"},"output":"sandbox_test\r\n"}}}
     ```

5. **助手流式文本增量（`step_type: "agent_response"`，`text_delta`）**
   ```json
   {"event":"step_update","step_update":{"conversation_id":"7edcb69b-073e-476b-ab8e-3e2948cf9770","step_index":3,"state":"ACTIVE","step_type":"agent_response","text_delta":"The output of the command is:\n\n```\ns"}}
   {"event":"step_update","step_update":{"conversation_id":"7edcb69b-073e-476b-ab8e-3e2948cf9770","step_index":3,"state":"ACTIVE","step_type":"agent_response","text_delta":"andbox_test\n```"}}
   {"event":"step_update","step_update":{"conversation_id":"7edcb69b-073e-476b-ab8e-3e2948cf9770","step_index":3,"state":"DONE","step_type":"agent_response","text_delta":"\n","duration_seconds":1.996007,"usage":{"input_tokens":13070,"output_tokens":33,"thinking_tokens":18,"cache_read_tokens":0,"total_tokens":13103}}}
   ```

6. **单轮完成汇总事件（`event: "result"`）**
   ```json
   {"event":"result","result":{"conversation_id":"7edcb69b-073e-476b-ab8e-3e2948cf9770","status":"SUCCESS","response":"The output of the command is:\n\n```\nsandbox_test\n```\n","duration_seconds":4.441009,"num_turns":1,"usage":{"input_tokens":25779,"output_tokens":305,"thinking_tokens":175,"cache_read_tokens":0,"total_tokens":26084}}}
   ```

---

### 3. 单进程多轮 Stdin 对话与运行中途写入行为
- **结论：支持单进程连续多轮对话，运行中途写入 `stdin` 会被顺序排队（Queue），不会打断当前轮次**。
- **实测过程**：
  1. 发送第 1 轮消息 `Say exactly: TURN_1_OK`；
  2. 在收到 `step_index: 0`（第 1 轮还在生成中）时，立即向 `stdin` 写入第 2 轮消息 `Say exactly: TURN_2_OK`；
  3. 实测输出顺序：
     - `step_index: 1` 完成输出 `TURN_1_OK` -> 输出第 1 个 `{"event":"result","result":{...,"num_turns":1}}`；
     - 紧接着自动读取缓冲区的第 2 行，输出 `step_index: 2` (`user_input`) -> `step_index: 3` (`TURN_2_OK`) -> 输出第 2 个 `{"event":"result","result":{...,"num_turns":2}}`；
     - 当 `stdin` 关闭（EOF）后，进程正常退出（退出码 `0`）。

---

### 4. `--mode` + `--sandbox` 下的文件编辑与 Shell 命令权限表现
1. **`--mode auto`**：
   - 在个人账号下会向 `stderr` 输出：`warning: --mode auto is not available for this account; continuing in the default mode.`，因此**不应默认传 `--mode auto`**，应使用 `--mode accept-edits`（对应 Hoxi 的 `Build` 模式）或 `--mode plan`（对应 Hoxi 的 `Plan` 模式）。
2. **`--mode plan --sandbox`**：
   - 不会修改工作区文件，只会将计划写入 `~/.gemini/antigravity-cli/brain/<conversation_id>/implementation_plan.md` 并提示用户审阅。
3. **`--mode accept-edits --sandbox`**：
   - **工作区内文件编辑**（`write_to_file`、`replace_file_content`）及**无害只读/输出命令**（如 `echo`）：在沙箱内直接执行成功。
   - **破坏性命令**（实测 `rm -f edit_test.txt`）：
     - 无头模式（`-p`）**不支持交互式等待审批输入**，而是**立即自动拒绝（Auto-Deny）**，并输出 `state: "ERROR"` 的工具步与包含 `denied_actions` 的 `result` 事件：
     ```json
     {"event":"step_update","step_update":{"conversation_id":"94c2c36b-0484-4104-9d3e-2cda0147146b","step_index":2,"state":"ERROR","step_type":"tool","tool_name":"run_command","duration_seconds":0.060877,"tool_info":{"name":"run_command","parameters":{"CommandLine":"rm -f edit_test.txt"},"error":{"type":"TOOL_ERROR","message":"permission check failed for command \"rm -f edit_test.txt\": user denied permission to run command:\nrm -f edit_test.txt\nDo not attempt to circumvent this denial by rephrasing the command, using alternative tools/scripts (e.g. python, sh, curl), or accessing the same target resource. Proceed without performing this action."}}}}
     {"event":"result","result":{"conversation_id":"94c2c36b-0484-4104-9d3e-2cda0147146b","status":"SUCCESS","response":"","duration_seconds":1.915125,"num_turns":1,"usage":{"input_tokens":12715,"output_tokens":114,"thinking_tokens":0,"cache_read_tokens":0,"total_tokens":12829},"denied_actions":[{"action":"command","display_name":"RunCommand"}]}}
     ```
     - **架构应对策略**：符合任务书约束 5——`agy` 无头模式不提供挂起等待回写的权限事件，安全边界由强制 `--sandbox` + `--mode accept-edits / plan` + Worktree 隔离保障；当检测到 `denied_actions` 或 `TOOL_ERROR` 时，在工具卡片中清晰展示拒绝原因，并在 UI 中说明该运行时的沙箱安全机制。

---

### 5. `--conversation <id>` 跨进程恢复对话
- **结论：完全支持**。
- 新进程传入 `--conversation ddd4b54a-7996-4819-91d4-0e47de47c6c9` 后：
  - 首条 `init` 事件保持相同的 `conversation_id`；
  - `step_index` 从上一轮结束后的 `4` 继续递增（`step_index: 4` 为 `user_input`，`step_index: 5` 为 `system_message`，`step_index: 6` 为 `agent_response`），且完整保留上下文记忆（准确回答出上一轮读取的文件名 `hello.txt`）。

---

### 6. `SIGINT` 中止行为与退出码
- 在生成中途向 `agy` 进程发送 `SIGINT`：
  - `stdout` 立即输出中断结果事件：
    ```json
    {"event":"result","result":{"conversation_id":"b586c946-a0af-4526-bb5f-47acc875c21b","status":"ERROR","response":"","error":"interrupted","duration_seconds":0,"num_turns":1,"usage":{"input_tokens":0,"output_tokens":0,"thinking_tokens":0,"cache_read_tokens":0,"total_tokens":0}}}
    ```
  - `stderr` 输出：`error: interrupted`
  - **进程退出码**：`1`
  - 下次发消息可通过 `--conversation <id>` 无缝续接。

---

### 7. `agy models` 输出格式
实测 `agy models` 输出（含终端 Spinner 控制符 `\r`，现有 `parseAgyModelsOutput` 已正确剥离）：
```text
gemini-4-argon-medium     Gemini 4 Argon (Medium)
gemini-4-argon-high       Gemini 4 Argon (High)
gemini-4-argon-low        Gemini 4 Argon (Low)
gemini-3.8-flash-high     Gemini 3.8 Flash (High)
gemini-3.8-flash-medium   Gemini 3.8 Flash (Medium)
gemini-3.8-flash-low      Gemini 3.8 Flash (Low)
claude-opus-5-5-low       Claude Opus 5.5 (Low)
claude-opus-5-5-medium    Claude Opus 5.5 (Medium)
claude-opus-5-5-high      Claude Opus 5.5 (High)
claude-sonnet-5-5-low     Claude Sonnet 5.5 (Low)
claude-sonnet-5-5-medium  Claude Sonnet 5.5 (Medium)
claude-sonnet-5-5-high    Claude Sonnet 5.5 (High)
gpt-oss-120b-medium       GPT-OSS 120B (Medium)
```

---

## 二、`claude` CLI 实测结论（共用 `internal/runner/`）

1. **启动参数**：
   ```bash
   claude -p --input-format stream-json --output-format stream-json --verbose --include-partial-messages --permission-mode <acceptEdits|plan> --permission-prompts none [--model <slug>] [--resume <session_id>]
   ```
   - `cmd.Dir` 同样强制设为会话的 `.hoxi/worktrees/<session-id>`。
2. **Stdin 输入格式**：
   ```json
   {"type":"user","message":{"role":"user","content":"Read hello.txt"}}
   ```
3. **Stdout 输出事件样例**：
   - 初始化（`type: "system", subtype: "init"`，会话 ID 在 `session_id` 字段）：
     ```json
     {"type":"system","subtype":"init","cwd":"/path/to/.hoxi/worktrees/ses_02","session_id":"de2ab4e7-82d7-4961-adf9-35d8d4c77962","tools":["Task","Bash","Edit","Read","Write","Grep","Glob"],"model":"claude-opus-5-5[1m]","permissionMode":"acceptEdits"}
     ```
   - 流式增量与完整消息（`type: "stream_event"` / `type: "assistant"` / `type: "user"` 工具结果）
   - 结束与错误汇总（`type: "result"`，含 `is_error`、`result`、`permission_denials`、`session_id`）：
     ```json
     {"type":"result","subtype":"success","is_error":false,"num_turns":1,"result":"...","session_id":"de2ab4e7-82d7-4961-adf9-35d8d4c77962","permission_denials":[]}
     ```

---

## 三、Runner 事件到 Hoxi OpenCode 事件（`sse-types.ts`）映射表

| Runner 事件来源 | 匹配条件 | 转换后的 Hoxi SSE 事件 (`OpenCodeEvent`) | Hoxi `MessagePart` / 状态映射细节 |
| :--- | :--- | :--- | :--- |
| **进程启动 / 发送 Prompt** | Turn 开始 | `session.status` (`{ type: "busy" }`) + `message.updated` (user & assistant `MessageInfo`) | 触发状态栏 `Running` 计数 `+1`，创建时间线助手消息容器 |
| **`agy` `init`** / **`claude` `system:init`** | 首次收到 `conversation_id` / `session_id` | 写入 `runner/store.go` 绑定 `externalID` | 记录 `modelID`、`cwd`，供重启后 `--conversation` / `--resume` 恢复 |
| **`agy` `step_update` (`agent_response`)** | 存在 `thinking` / `thinking_delta` 或同 `step_index` 下 `transcript_full.jsonl` 含 `thinking` | `message.part.updated` / `message.part.delta` (`field: "text"`) | **`ReasoningPart`** (`type: "reasoning"`, `id: "prt_reason_<step>"`, `time: { start, end }`) -> 渲染至 `<ThoughtBox />` |
| **`agy` `step_update` (`agent_response`)** | 存在 `text_delta` (`state: "ACTIVE"` / `"DONE"`) | `message.part.updated` (首次) + `message.part.delta` (`field: "text"`) | **`TextPart`** (`type: "text"`, `id: "prt_text_<step>"`) -> 经前端 `rAF` 批处理流式渲染正文 |
| **`agy` `step_update` (`tool`, `ACTIVE`)** | `step_type == "tool" && state == "ACTIVE"` | `message.part.updated` | **`ToolPart`** (`type: "tool"`, `tool: normalizeToolName(tool_name)`, `state: { status: "running", input, title, time: { start } }`) -> `<ToolCard />` 动态计时 |
| **`agy` `step_update` (`tool`, `DONE`)** | `step_type == "tool" && state == "DONE"` | `message.part.updated` | **`ToolPart`** (`state: { status: "completed", input, output, title, time: { start, end } }`)；若为文件修改工具则触发 Worktree Diff 刷新 |
| **`agy` `step_update` (`tool`, `ERROR`)** | `step_type == "tool" && state == "ERROR"` | `message.part.updated` | **`ToolPart`** (`state: { status: "error", input, error: tool_info.error.message, time: { start, end } }`) |
| **`agy` `result` (`SUCCESS`)** / **`claude` `result`** | `status == "SUCCESS"` 且 `!is_error` | `message.updated` (`time.completed`, `tokens`) + `session.status` (`{ type: "idle" }`) + `session.idle` | 闭合所有未完成 Part 时间戳；若 `response` 非空且此前无 `text_delta` 则补齐 `TextPart`；状态栏 `Running` `-1` |
| **`agy` `result` (`ERROR`)** / **进程异常退出** | `status == "ERROR"`（非主动 `interrupted`）或 `is_error == true` | `session.error` + `session.status` (`{ type: "idle" }`) | 在时间线直接展示错误卡片，**不自动循环重试** |
| **用户点击 Stop (`abort`)** | 发送 `SIGINT` 收到 `error: "interrupted"` | `message.updated` (`finish: "aborted"`) + `session.status` (`{ type: "idle" }`) | 标记当前轮次中止，保持 `externalID` 供下一轮续接 |
