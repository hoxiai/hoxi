---
name: skill-authoring
description: Create, modify, validate, or troubleshoot Agent Skills through conversation. 通过对话创建、修改、校验或排查 Agent Skill。当用户要求新增技能、编写或重构 SKILL.md、调整技能触发描述，或反馈技能不被识别、不触发时使用。Use when the user asks to create a skill, write or refactor SKILL.md, tune skill trigger descriptions, or reports a skill that is not discovered or not triggering.
compatibility: 需要读写目标技能目录与当前 Git 工作区
metadata:
  author: "Hoxi"
  version: "1.0.0"
---

# 技能创造与维护

## 适用场景

- 从零创建一个技能。
- 修改已有技能的 description、正文、脚本或引用文件。
- 排查技能不显示、不触发或内容不生效。

## 工作方式

本技能由对话驱动：先补齐最少信息，再直接落盘，最后校验并向用户汇报。不要只给模板让用户自己复制，除非用户明确只要模板。

## 语言策略

- 技能只有一个 SKILL.md，正文不按语言分叉；输出语言跟随用户本轮提问语言。
- 用户用中文提问就用中文汇报，用英文提问就用英文汇报；代码、路径、字段名、命令保持原文。
- 用户明确指定输出语言时以用户指定为准。
- `description` 必须同时包含中英文触发词，因为它是唯一进入模型索引的字段；不要为不同语言复制同名技能。
- 若确需两套完整正文，放入 `references/zh-CN.md` 与 `references/en-US.md`，由本文件做路由，禁止在同一文件中整段重复。

## 第 1 步：确认三件事

1. 目标：新建 / 修改 / 排查。
2. 技能名：kebab-case，匹配 `^[a-z0-9]+(-[a-z0-9]+)*$`，且与目录名完全一致。
3. 触发场景：用户会说什么话时应该使用它，收集 2–5 个真实说法。

只询问缺失项，尽量一轮问完。用户已提供足够信息时直接开始，不要反复确认。

## 第 2 步：选择位置

项目级（随仓库共享，优先）：

- `.opencode/skills/<name>/SKILL.md`
- `.agents/skills/<name>/SKILL.md`
- `.claude/skills/<name>/SKILL.md`

全局级（跨项目）：

- `~/.config/opencode/skills/<name>/SKILL.md`
- `~/.agents/skills/<name>/SKILL.md`
- `~/.claude/skills/<name>/SKILL.md`

决策规则：

- 只服务当前仓库时放项目级；跨项目通用时放全局级。
- 多会话、多 worktree 使用时放项目级并提交 Git。
- 目录名必须使用复数 `skills/`。OpenCode 1.19.3 的 `.opencode` 同时扫描单数 `skill/`，但 `.claude`、`.agents` 只扫描复数；Hoxi 统一使用 `skills/`。
- 禁止放进 `~/.hoxi/`，引擎不扫描该目录。
- 名称在全部位置中必须唯一；创建前先检索同名目录。
- 引擎从当前目录向上扫描到 Git worktree 根。Hoxi 会把主仓库 `.opencode` 符号链接进 worktree，因此未提交文件也可能被发现；提交是为了跨机器共享，不是发现的前提。

## 第 3 步：编写 frontmatter

必填字段：

| 字段 | 约束 |
| --- | --- |
| `name` | 1–64 字符，小写字母/数字/单连字符，与目录名一致 |
| `description` | 1–1024 字符，写清「做什么 + 何时使用 + 触发关键词」 |

可选字段：

- `license`：许可证名称或随包文件引用。
- `compatibility`：1–500 字符的环境要求。
- `metadata`：字符串到字符串的映射，可写 `author`、`version`。
- `metadata["opencode/slash"]: "true"`：在 TUI 中暴露为 `/name` 命令。
- `metadata["opencode/autoinvoke"]: "false"`：不进入模型自动选择列表，只能手动加载。

description 公式：`[能力] + [触发场景与关键词] + [产出]`。避免「帮助处理 X」这类空泛描述，把用户真实会说的词写进去。

## 第 4 步：编写正文

推荐结构：输入、步骤、输出、边界。要求：

- 正文控制在 500 行内，长资料移入 `references/`。
- 可执行代码放 `scripts/`，模板与素材放 `assets/`。
- 所有引用使用技能根目录下的相对路径。
- 不写个人机器的绝对路径、密钥或内部凭据。
- 需要仓库事实时引用 `docs/skills-development.md`，不要另立规则。

模板：

```markdown
---
name: example-skill
description: 一句话说明能力；当用户提到“某关键词”“某场景”时使用；产出是什么。
metadata:
  author: "Hoxi"
  version: "1.0.0"
---

# 技能标题

## 输入

- 用户需要提供的参数与缺省行为。

## 步骤

1. 第一个可执行步骤。
2. 需要读取的引用文件：[检查表](references/checklist.md)。

## 输出

- 交付物格式与验收标准。

## 边界

- 不做什么；缺少依赖时如何报告。
```

## 第 5 步：校验

依次确认：

1. 文件是 `skills/<name>/SKILL.md`，不是裸 `.md`。
2. 目录名与 `name` 完全一致。
3. frontmatter 以首行 `---` 开始，并有闭合 `---`。
4. `description` 存在且不超过 1024 字符。
5. 正文引用的相对文件全部存在。
6. 没有跨目录同名技能。

可用以下命令做结构性自检（把路径替换为目标文件）：

```bash
f=.opencode/skills/skill-authoring/SKILL.md
name=$(awk 'BEGIN{f=0} /^---[[:space:]]*$/{f++; next} f==1 && /^name:/{sub(/^name:[[:space:]]*/,""); print; exit}' "$f")
desc=$(awk 'BEGIN{f=0} /^---[[:space:]]*$/{f++; next} f==1 && /^description:/{sub(/^description:[[:space:]]*/,""); print; exit}' "$f")
dir=$(basename "$(dirname "$f")")
test "$name" = "$dir" && echo "name ok" || echo "name mismatch: $name vs $dir"
test -n "$desc" && test ${#desc} -le 1024 && echo "description ok" || echo "description missing or too long"
awk 'BEGIN{n=0} /^---[[:space:]]*$/{n++; if(n==2){ok=1; exit}} END{exit !ok}' "$f" && echo "frontmatter ok"
```

## 第 6 步：生效与验证

1. 提交：`git add <技能目录>` 并提交，以便跨机器和协作者共享。Hoxi worktree 通过符号链接读取主仓库 `.opencode`，未提交文件也可能被发现。
2. 重启：完全退出并重启 Hoxi，或 dispose 引擎实例。OpenCode 1.19.3 按目录缓存技能目录，新增或修改后不会热更新，重复 GET 也不会刷新。
3. 验证目录接口：
   - Hoxi：`GET /api/hoxi/skills/list?directory=<绝对路径>`
   - OpenCode：`GET /skill?directory=<绝对路径>`
   确认返回中包含目标 `name`、`description`、`location`。
4. 会话内可用性：技能目录在会话创建时快照，已有会话需新开会话才能看到新技能。

## 第 7 步：修改已有技能

1. 先读原文件，确认改动范围。
2. 只改必要部分，保持 `name` 与目录不变；确需改名时同时移动目录并搜索全部引用。
3. 重新执行第 5、6 步。
4. 汇报改动点与验证结果，不覆盖用户未同意的内容。

## 第 8 步：排查不生效

按顺序检查：

1. 是否是裸 `.md`，而不是 `<name>/SKILL.md`。
2. 目录名与 `name` 是否不一致，或名称含大写、下划线、连续连字符。
3. frontmatter 是否未闭合，或缺少 `description`。
4. 是否放在 `~/.hoxi/` 等非扫描目录。
5. 当前 worktree 是否真的能看到该文件。Hoxi 会链接主仓库 `.opencode`；其他目录下的未提交文件不会自动进入 worktree。
6. 引擎是否在技能创建后重启过。
7. 是否存在跨目录同名技能被覆盖。
8. 是否被 `permission.skill` 设为 `deny`，或 `metadata["opencode/autoinvoke"]` 为 `"false"`。

## 交付格式

- 技能路径
- `name` 与 `description`
- 校验结果
- 生效所需操作（提交、重启、新开会话）
- 修改场景下附变更点

## 边界

- 不覆盖用户未同意修改的现有技能。
- 不自动安装依赖。
- 不写入密钥、令牌或个人绝对路径。
- 只有 Markdown 变更时说明未运行构建类校验的原因；涉及脚本时按仓库要求执行对应检查。
