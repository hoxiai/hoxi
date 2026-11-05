# 近期提交审查：待修复任务清单（2026-11-05）

审查范围：`4b8bbc6d..06b837cc`（2026-11-05 的 8 个提交）。本文给接手修复的 AI 使用，每个任务都能单独完成。

## 开工前必读

- 先读仓库根目录的 [`AGENTS.md`](../../AGENTS.md)。改审批、持久化、进程控制前还要读 [`docs/ai-coding-guardrails.md`](../ai-coding-guardrails.md)
- 行号以提交 `06b837cc` 为准，动手前请重新定位
- 工作区里有用户**未提交**的改动（`internal/runner/turn_retry.go`、`stall_watchdog.go`、`internal/llm/retry.go` 等）。不要覆盖、还原或擅自提交这些文件；只有任务 0 涉及它们
- 回归测试请写进对应包已有的 `*_test.go`，禁止留下 `zz_scratch_*` 之类的临时文件
- 每个任务完成后都要跑：

```bash
npm --prefix web run lint && npm --prefix web test && npm --prefix web run build
```

```bash
go vet ./... && go build ./... && go test -race ./...
```

## 任务总览与并行分配

| 任务 | 类型 | 优先级 | 主要文件 | 能否并行 |
| --- | --- | --- | --- | --- |
| 0 | 编译失败 | P0 | `internal/runner/opencode_turn_recovery.go` | 需要用户确认，见任务说明 |
| 1 | Bug（已复现） | P1 | `internal/gateway/session_prune.go` | 和任务 8 改同一个文件，交给同一个 AI |
| 2 | Bug（已复现） | P1 | `web/src/store/session-lifecycle.ts`、`workspace-sessions.ts` | 可以 |
| 3 | Bug（读代码确认） | P1 | `internal/runner/store.go`、`store_cache.go` | 可以 |
| 4 | Bug（读代码确认，未实测） | P2 | `internal/handoff/grok_bridge.go` | 可以 |
| 5 | 职责不清 | 需人工决策 | `internal/handoff/codex.go`、`opencode_provider.go`、`service.go` | **AI 不要动手**，等用户决定 |
| 6 | 重复逻辑 | P2 | `web/src/store/subagent-sessions.ts` | 可以 |
| 7 | 职责归属 | P3 | `internal/gateway/memory_release.go`、`engine.go` | 可以 |
| 8 | 健壮性 | P3 | `internal/gateway/session_prune.go`、`internal/worktree/manager.go` | 和任务 1 一起做 |

---

## 任务 0（P0）：`9c367338` 起 4 个提交单独检出时编译失败

**位置**：`internal/runner/opencode_turn_recovery.go:533`

**现象**：提交 `9c367338` 加了一行 `sanitizeAbortedTurnErrorInfo(&turn.Info, ap)`，但这个函数只在**未提交**的 `internal/runner/turn_retry.go` 里，它依赖的 `ap.stall.abortError()` 也在未提交的 `stall_watchdog.go` 里。用户本地能编译，是因为工作区刚好有这些未提交改动。干净检出 `9c367338`、`505ea0d4`、`df253290`、`06b837cc` 都会报：

```text
internal/runner/opencode_turn_recovery.go:533:3: undefined: sanitizeAbortedTurnErrorInfo
```

`5f6af3a9` 及更早的提交可以正常编译。

**复现**：

```bash
git worktree add --detach /tmp/hoxi-head 06b837cc && (cd /tmp/hoxi-head && go build ./internal/...); git worktree remove --force /tmp/hoxi-head
```

**修复方向**（二选一，**必须先问用户**）：

- A（推荐）：用户确认"重试截断 / 中止错误"这组未提交改动已经完成后，把它们作为一个完整提交提交上去
- B：如果这组改动还没完成，先单独提交一次，删掉第 533 行这处调用（在 HEAD 上它本来就不起作用），等那组改动完成后再加回来

**验收**：用上面的命令干净检出新的 HEAD，`go build ./...` 能通过（根包会嵌入 `web/dist`，需要先跑一次 `npm --prefix web run build`）。

---

## 任务 1（P1）：清理旧会话后，非子 Agent 的子会话变成永远清不掉的孤儿

**位置**：`internal/gateway/session_prune.go`
- `oldSessionCandidates` 第 183 行：`if !ok || rec.ParentID != "" || rec.Subagent { continue }`
- `deletedWithRoot` 第 226 行：只保留根会话和 `Subagent == true` 的直接子会话

**现象**：带 `ParentID` 但 `Subagent == false` 的会话（例如经 `session_create.go:128` / `conversations.go:359` 传 `body.ParentID` 创建出来的子会话）：
1. 预览时会参与根会话的活跃时间和忙碌判断，但不计入 `Children`，也不会随根会话删除
2. 根会话删掉后它留在 store 里，`ParentID` 指向一个已经不存在的会话
3. 下一次预览时，它因为 `ParentID != ""` 被跳过，所以**永远不会出现在清理列表里**

**复现用的回归测试**（写进 `internal/gateway/session_prune_test.go`，复用现有的 `newPruneFixture` / `put` / `preview` / `delete` / `daysAgo`）。按修复后的预期写，修复前会失败：

```go
func TestOldSessionsListsOrphanedNonSubagentChildren(t *testing.T) {
	f := newPruneFixture(t)
	f.put(t, runner.SessionRecord{ID: "ses_cld_root", Title: "root", CreatedAt: daysAgo(60), UpdatedAt: daysAgo(60)})
	f.put(t, runner.SessionRecord{ID: "ses_cld_fork", Title: "fork", ParentID: "ses_cld_root", CreatedAt: daysAgo(59), UpdatedAt: daysAgo(59)})

	if code, res := f.delete(t, `{"olderThanDays":30,"sessionIDs":["ses_cld_root"]}`); code != http.StatusOK || len(res.Deleted) != 1 {
		t.Fatalf("root delete: code=%d res=%+v", code, res)
	}
	if _, ok := f.svc.Store().Meta("ses_cld_fork"); !ok {
		t.Fatal("a non-subagent child must not be deleted silently with its root")
	}
	_, preview := f.preview(t, "30")
	if len(preview.Sessions) != 1 || preview.Sessions[0].ID != "ses_cld_fork" {
		t.Fatalf("an old orphan whose parent is gone must be listed for cleanup: %+v", preview.Sessions)
	}
}
```

**修复要求**：
- 父会话已经不在 store 里的会话，按根会话处理（可以进入候选列表，删除时同样要重新校验）
- **不要**把非子 Agent 的子会话悄悄并进根会话一起删：它们是用户自己的对话，预览里没列出就不能删
- 删除接口仍然只删用户确认过的 ID（这是 AGENTS.md 2.2 第 11 条"禁启发式推断破坏性操作"的要求）
- 顺带检查前端侧边栏：父会话被删以后，这类孤儿还能不能看到和打开（看 `web/src/store/subagent-sessions.ts` 的分组逻辑）。如果看不到，要在 store 层修，不要在 components 里另写一套

**验收**：上面的测试通过；`TestOldSessions*` 现有测试全部保持通过。

---

## 任务 2（P1）：删除会话后"下一个选中谁"有两套不一致的逻辑

**位置**：
- `web/src/store/session-lifecycle.ts:181-186`，`removeSessionsFromStore` 里内联的候选筛选和排序
- `web/src/store/workspace-sessions.ts:78`，原有的 `nextActiveSessionCandidate`

**现象**：`06b837cc` 把 `nextActiveSessionCandidate` 抄进了 `removeSessionsFromStore`，但抄的时候改了语义：
- 原逻辑通过 `sortSessionsByActivity` → `sessionActivityTime` 排序，取 `max(sessionFinishedAt[id], updated, created)`
- 新逻辑用的是 `updated || created`，没有考虑本地的 `sessionFinishedAt`
- 归档判断也从 `isSessionArchived` 改成了手写条件

已经复现：会话 `ses_a`（updated=200）、`ses_b`（updated=100，`sessionFinishedAt=999`），删除 `ses_del` 时，`nextActiveSessionCandidate` 选中 `ses_b`，`removeSessionFromStore` 选中 `ses_a`。

**复现用的回归测试**（加进 `web/src/__tests__/old-sessions.test.ts`，或者新建测试文件并在 `web/scripts/run-tests.ts` 里注册）：

```ts
it("removeSessionFromStore picks the same next session as nextActiveSessionCandidate", () => {
  const saved = structuredClone(unwrap(workspace));
  try {
    const s = (id: string, updated: number) =>
      ({ id, title: id, directory: "/tmp/p", time: { created: 1, updated } }) as SessionInfo;
    setWorkspace("sessions", reconcile([s("ses_del", 50), s("ses_a", 200), s("ses_b", 100)]));
    setWorkspace("sessionFinishedAt", { ses_b: 999 });
    const expected = nextActiveSessionCandidate("ses_del")?.id;
    assertEqual(removeSessionFromStore("ses_del")?.id, expected);
    assertEqual(expected, "ses_b");
  } finally {
    setWorkspace(reconcile(saved));
  }
});
```

**修复要求**：
- 只保留一份实现：让 `nextActiveSessionCandidate` 接受一组要排除的 ID（例如 `exclude?: string | ReadonlySet<string>`），`removeSessionsFromStore` 直接调用它
- 保持现有调用方（`session-lifecycle.ts:120` 等）行为不变
- 遵守分层规则：只改 `store/`，不改 `components/`

**验收**：上面的测试通过；`npm --prefix web test` 全部通过。

---

## 任务 3（P1）：冷会话摘要缓存 `summaryTurns` 没有上限

**位置**：
- `internal/runner/store_cache.go:164`：`trimCache` 淘汰转录时执行 `ss.summaryTurns = compactSummaryTurns(ss.rec.Turns)`
- `internal/runner/store.go:297-313`：`VisitTurnsSummary` 懒加载并缓存摘要
- 引入它的提交：`5f6af3a9`

**现象**：每个转录被淘汰的会话都会留下一份摘要（保留每个 part 的 ID、类型、工具名、状态、标题和时间），这份数据：
- 不计入 `bodyBytes`，缓存淘汰逻辑看不到它
- 只有重新加载转录或提交时才会清掉（`store.go:140`、`store.go:173`）

会话数量多、进程运行时间长时，内存会一直增长。这违反了 AGENTS.md 2.2 第 4 条"局部状态按稳定 ID 有界存储"，也和 `df253290`（空闲时释放 Go 堆）的目标相反。

说明：所有写入路径都会先调用 `ensureTurnsLocked`，它会清空 `summaryTurns`，所以**不存在数据过期的问题**，只是内存没有上限。

**修复要求**（选一种就行）：
- 摘要单独用一个有上限的 LRU（按会话数，或按估算字节），超出就丢掉，下次需要时再从磁盘读
- 或者 `trimCache` 不再预先生成摘要，只在 `VisitTurnsSummary` 时生成，并且同样受上限约束
- 会话删除时要一并清掉对应的摘要（成对清理）

**验收**：新增测试：连续淘汰超过上限数量的会话后，内存里的摘要条目数不超过上限；`VisitTurnsSummary` 的返回结果和修复前一致。`go test -race ./internal/runner/...` 通过。

---

## 任务 4（P2）：Grok 令牌刷新条件永远为真，状态检测带有阻塞的副作用

**位置**：`internal/handoff/grok_bridge.go`
- 第 233 行：`if strings.TrimSpace(entry.RefreshToken) != "" || hasExpiry { canRefresh = true }`。执行到这里时 `hasExpiry` 已经必然为 true（第 229 行没有过期时间就直接 return 了），所以 `canRefresh` 对任何带过期时间的条目都是 true，`RefreshToken` 的检查不起作用
- 第 445 行：`detectGrokCLIStatus` 调用 `loadGrokLocalCredentialsWithRefresh(context.Background())`
- 第 472、540、556 行：导入和桥接请求也会同步刷新
- `refreshGrokAuthViaCLI`（第 346 行）：持有全局锁，同步运行 `grok models`，超时 15 秒

**现象**（读代码得出，本机没有 grok 二进制，没有实测）：
- 没有 refresh_token 的过期凭证，每过 15 秒的冷却期也会拉起一次 `grok models`
- 只读的"CLI 状态检测"接口会触发子进程刷新，最长阻塞 15 秒；并发请求在全局锁上排队
- 如果 `grok models` 在凭证失效时会进入交互登录流程，后台调用可能挂住，直到超时

**修复要求**：
- `canRefresh` 只在 `RefreshToken` 不为空时才是 true
- `detectGrokCLIStatus` 改成只读：只报告"已过期 / 即将过期"，不触发刷新。刷新只放在 60 秒一次的后台 `credentialSyncLoop` 和真正的桥接请求里
- 子进程测试使用 `internal/testutil/execstub`，不要依赖真实的 grok

**验收**：新增测试：
- 有 `expires_at`、没有 `refresh_token` 且已过期 → `needsRefresh()` 为 false，不启动子进程
- 调用 `detectGrokCLIStatus` 不会启动子进程
- 现有的 `cli_credential_sync_test.go` 里 Grok 相关测试全部通过

---

## 任务 5（需人工决策，AI 不要动手）：Codex refresh token 现在归谁不清楚

**位置**：`internal/handoff/codex.go:661`（`readCodexAuthBody`）、`:695`（`parseJWTExpiryMs`）；`internal/handoff/opencode_provider.go:482`（`isEngineOAuthNewer`）；`internal/handoff/service.go:188`

**背景**：`505ea0d4` 之前的设计是"只有 Codex CLI 负责刷新令牌，Hoxi 把 `expires` 设为 0，从不刷新"。现在的逻辑是：
- 把 JWT 里的真实 `exp` 传给 OpenCode 引擎，引擎会在令牌过期时**自己去刷新**
- 再用 `isEngineOAuthNewer` 防止旧文件把引擎刷新过的新令牌覆盖回去

结果是 Codex CLI 和 OpenCode 引擎都可能用同一个 refresh_token 刷新。如果 OpenAI 每次刷新都会轮换 refresh_token，先刷新的一方会让另一方失效，Codex CLI 可能因此被登出。`HANDOFF_AND_CLI_BRIDGE.md` 已经改了描述，但没写明以后由谁负责刷新。

**需要用户决定**：
- 方案 a：引擎拥有刷新权，Hoxi 刷新后需要把新令牌同步给 Codex CLI（但现有约束是"绝不回写 `~/.codex/auth.json`"）
- 方案 b：只有 Codex CLI 拥有刷新权，引擎侧要禁止或拦截刷新（需要确认 OpenCode 有没有对应的开关，且不能修改上游）
- 方案 c：维持现状，接受"两边可能互相登出"，并把这一点写进文档

用户决定后，再单独开任务实现，并补上"刷新后另一方仍然可用"的测试。

---

## 任务 6（P2）：识别子 Agent 的逻辑写了两份

**位置**：`web/src/store/subagent-sessions.ts`
- 第 117 行：`collectChildSubagentSessionIDs` 里识别 task / agent / subagent 工具的循环
- 第 237-251 行：`groupSubagentsByRoot` 里内联的同一段循环（`06b837cc` 为了性能抄过来的）

**风险**：以后改了工具名判断规则（例如新运行时的工具名），很容易只改一处，导致侧边栏分组和删除时的子会话收集结果不一致。

**修复要求**：抽出一个纯函数，例如 `childSubagentIDsFromTurns(turns): string[]`，两处都调用它；不要改变 `groupSubagentsByRoot` 现有的输出。

**验收**：现有 subagent 分组相关测试全部通过；为抽出来的函数补一个单测。

---

## 任务 7（P3）：进程级内存调优放在 gateway 里，挂在"引擎预热"函数名下

**位置**：
- `internal/gateway/memory_release.go`：`debug.SetMemoryLimit`（默认 768 MiB）和空闲时的 `debug.FreeOSMemory`
- `internal/gateway/engine.go:78-83`：`StartEnginePrewarm` 里同时启动了 `StartStorageMaintenance` 和 `StartIdleMemoryRelease`
- 调用方：`internal/app/app.go:156`、`internal/app/headless.go:95`

**问题**：
- 设置 `GOMEMLIMIT` 会影响整个进程，属于进程生命周期，不是网关的职责
- `StartEnginePrewarm` 这个名字只说了"引擎预热"，实际还负责存储维护和内存回收，看名字推不出它做了什么

**修复要求**（选一种）：
- 把 `applyDefaultMemoryLimit` 移到 `internal/app`，在进程启动时调用；空闲回收继续用 gateway 提供的"是否空闲"判断
- 或者至少把 `StartEnginePrewarm` 拆分 / 改名（例如 `StartBackgroundServices`），让每项后台任务有自己的入口

桌面端和 headless 两个入口都要覆盖到。`memory_release_test.go` 要跟着移动或调整。

---

## 任务 8（P3）：旧会话清理中的两个健壮性问题（和任务 1 交给同一个 AI）

**位置**：`internal/gateway/session_prune.go:368-373`、`:336`；`internal/worktree/manager.go:573`

1. **靠匹配错误文案判断错误类型**：`strings.Contains(err.Error(), "uncommitted changes")`。只要 worktree 包改了错误信息，"工作树有未提交改动"就会被误报成 `worktreeFailed`
   - 修复：在 `internal/worktree` 导出一个哨兵错误（例如 `ErrUncommittedChanges`），`manager.go:573` 用 `fmt.Errorf("%w ...")` 包装返回，gateway 里改用 `errors.Is`
2. **先删工作树再删会话，后一步失败时状态不一致**：如果 `DeleteConversation` 失败（例如"session is still stopping"），工作树已经被删了，会话却还在（分支还保留着，提交过的内容能找回）
   - 修复：至少要在跳过原因里说明"工作树已删除，分支保留"；或者调整顺序，先确认会话可以删除再删工作树。具体方案由修复者评估，但不能放宽"有未提交改动就不删"这条规则

**验收**：补充拒绝路径测试：工作树有未提交改动时跳过原因是 `worktreeDirty`；`DeleteConversation` 失败时返回结果能反映工作树的状态。

---

## 审查时的测试基线（`06b837cc`，干净检出）

| 项目 | 结果 |
| --- | --- |
| Web lint（tsc、i18n、800 行上限、循环依赖） | 通过 |
| Web 单测 | 616 个全部通过 |
| Web 构建 | 通过 |
| Go，原样 | 编译失败（任务 0） |
| Go，临时去掉第 533 行后跑 `go vet`、`go build`、`go test -race ./...` | 43 个包全部通过 |
| Go 分层检查（业务包反向 import gateway/app/cli、业务包直接 import provider） | 没有发现 |
