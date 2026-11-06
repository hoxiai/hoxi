# hoxi 动态交接班日志 (STATE.md)

- **Status**: `READY`
- **Last Updated**: 2026-11-06
- **Active Branch / Worktree**: `main`

## Last Completed
- 修复 macOS 上「已做过任务的会话里发消息页面闪一下」：根因是时间线滚动容器的父级设了 `container-type: inline-size`，WebKit 分两段做样式/布局，中间那次布局缺少新插入节点，贴底的 `scrollTop` 被夹低一截再被拉回；Chrome/WebView2 不会，新会话不滚动所以也不会闪。
- 改动：`web/src/components/timeline/MessageStream.tsx`（去掉外层 container-type）、`web/src/components/timeline/task-timeline.css`（容器声明移到 `.task-transcript-navigation` 浮层，`100cqw` 结果不变）、`web/src/__tests__/session-send-layout.browser.tsx`（断言滚动容器祖先不是尺寸容器）。

## Verification
- `./verify.sh` 退出码 0；Chrome 浏览器测试 session-send-layout / send-scroll-anchor / session-send / session-page / activity-group 全过。
- Playwright WebKit 探针：修复前发送时与首段输出时各 1 次错误夹取（554→470、945→888），修复后连续 3 次均为 0；上述前 4 组浏览器测试在 WebKit 下也全过。
- 未在打包后的 Hoxi 桌面应用里人工复核。
- 同期另一会话完成审批修复：`b31f63d9`（Monitor 走命令策略 + 3 个 trusted 误判）与随后的重复弹窗修复（「始终允许」前后端同规则、无人值守硬拒绝先于会话授权）；待核实 OpenCode「始终允许」是否按前缀通配。

## Next Actions
- 可选 UX 改进：发送后让新消息停在视口顶部（下方留跑道），替代全程贴底跟随，需产品确认。
- 已知遗留：`tail-hold.ts` 的 `measure()` effect 在 spacer DOM 写入前读布局，会先算出约 40px 过冲再由 ResizeObserver 同帧修正（两种内核都有，触发 RO loop 警告）；`activity-group.browser.tsx` 的折叠条文字计时用例在 WebKit 下本来就失败。

## [BLOCKED]
- None
