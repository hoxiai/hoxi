import 'package:flutter/material.dart';

import '../../../../core/theme/app_theme.dart';
import '../../models/ai_dev_session.dart';
import '../../models/code_convoy_plan.dart';
import 'ai_chat_state_widgets.dart';
import 'code_session_subagents_section.dart';

export 'code_session_subagents_section.dart';

/// 子会话顶部导航栏（对齐 PC 端 SubagentSessionBanner）：
/// 当进入编队子任务或 Subagent 子会话时，提供「返回主会话」、状态、Diff 统计及同级子任务切换。
class AiSubagentSessionBanner extends StatelessWidget {
  const AiSubagentSessionBanner({
    super.key,
    required this.session,
    this.siblings = const [],
    required this.onOpenSession,
  });

  final AiDevSession session;
  final List<AiDevSession> siblings;
  final ValueChanged<int> onOpenSession;

  @override
  Widget build(BuildContext context) {
    final parentId = session.parentSessionId;
    if (parentId == null || parentId <= 0) return const SizedBox.shrink();

    final isRunning = isAiSessionRunning(session);
    final isFailed =
        session.status.toLowerCase() == 'failed' ||
        session.currentStage.toLowerCase() == 'failed';
    final isApproval = session.currentStage.toLowerCase() == 'awaiting_approval';
    final cleanTitle = stripSubagentTitleSuffix(
      resolveSessionDisplayTitle(
        session: session,
        fallback: session.convoyTaskId.isNotEmpty
            ? session.convoyTaskId
            : '子任务 #${session.id}',
      ),
    );
    final orderedSiblings = siblings.isNotEmpty ? siblings : <AiDevSession>[session];
    final currentIdx = orderedSiblings.indexWhere((s) => s.id == session.id);
    final prevSibling = currentIdx > 0 ? orderedSiblings[currentIdx - 1] : null;
    final nextSibling =
        currentIdx >= 0 && currentIdx < orderedSiblings.length - 1
        ? orderedSiblings[currentIdx + 1]
        : null;

    return Container(
      key: const ValueKey('ai_subagent_session_banner'),
      width: double.infinity,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
      decoration: BoxDecoration(
        color: AppTheme.surfaceMuted.withValues(alpha: 0.75),
        border: Border(bottom: BorderSide(color: AppTheme.border)),
      ),
      child: Row(
        children: [
          InkWell(
            key: const ValueKey('ai_subagent_back_to_parent'),
            borderRadius: BorderRadius.circular(8),
            onTap: () => onOpenSession(parentId),
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 3),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Icon(
                    Icons.arrow_back_rounded,
                    size: 14,
                    color: AppTheme.primaryBlueText,
                  ),
                  const SizedBox(width: 4),
                  Text(
                    '返回主会话',
                    style: TextStyle(
                      color: AppTheme.primaryBlueText,
                      fontSize: 12,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                ],
              ),
            ),
          ),
          Container(
            width: 1,
            height: 14,
            margin: const EdgeInsets.symmetric(horizontal: 8),
            color: AppTheme.border,
          ),
          buildConvoyStatusIcon(
            isRunning: isRunning,
            isFailed: isFailed,
            isApproval: isApproval,
            isCompleted: !isRunning && !isFailed && !isApproval,
          ),
          const SizedBox(width: 6),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1.5),
            decoration: BoxDecoration(
              color: AppTheme.primaryBlueLight,
              borderRadius: BorderRadius.circular(6),
            ),
            child: Text(
              session.isConvoyTask ? '编队' : '子任务',
              style: const TextStyle(
                color: AppTheme.primaryBlue,
                fontSize: 10.5,
                fontWeight: FontWeight.w700,
              ),
            ),
          ),
          const SizedBox(width: 6),
          Expanded(
            child: Text(
              cleanTitle,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          if (session.diffAdditions > 0 || session.diffDeletions > 0) ...[
            const SizedBox(width: 6),
            AiDiffStatBadges(
              additions: session.diffAdditions,
              deletions: session.diffDeletions,
              fontSize: 11,
            ),
          ],
          if (orderedSiblings.length > 1 && currentIdx >= 0) ...[
            const SizedBox(width: 6),
            InkWell(
              borderRadius: BorderRadius.circular(6),
              onTap: prevSibling == null
                  ? null
                  : () => onOpenSession(prevSibling.id),
              child: Padding(
                padding: const EdgeInsets.all(2),
                child: Icon(
                  Icons.chevron_left_rounded,
                  size: 16,
                  color: prevSibling == null
                      ? AppTheme.textLight
                      : AppTheme.textSecondary,
                ),
              ),
            ),
            Text(
              '${currentIdx + 1}/${orderedSiblings.length}',
              style: TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 11,
                fontFamily: 'monospace',
              ),
            ),
            InkWell(
              borderRadius: BorderRadius.circular(6),
              onTap: nextSibling == null
                  ? null
                  : () => onOpenSession(nextSibling.id),
              child: Padding(
                padding: const EdgeInsets.all(2),
                child: Icon(
                  Icons.chevron_right_rounded,
                  size: 16,
                  color: nextSibling == null
                      ? AppTheme.textLight
                      : AppTheme.textSecondary,
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

/// 会话内编队任务（Convoy Plan）与子会话（Subagents）总览卡片（对齐 PC 端 TaskOverviewView / ComposerShelf）。
class AiConvoyOverviewCard extends StatefulWidget {
  const AiConvoyOverviewCard({
    super.key,
    required this.convoyPlans,
    required this.subagents,
    this.sessionSubagents = const [],
    required this.onOpenSession,
    this.onCancelConvoyPlan,
    this.onRetryConvoyPlan,
    this.onMergeConvoyPlan,
    this.onSummarizeConvoyPlan,
  });

  final List<CodeConvoyPlan> convoyPlans;
  final List<CodeSubagentItem> subagents;
  final List<AiDevSession> sessionSubagents;
  final ValueChanged<int> onOpenSession;
  final ValueChanged<String>? onCancelConvoyPlan;
  final ValueChanged<String>? onRetryConvoyPlan;
  final ValueChanged<String>? onMergeConvoyPlan;
  final ValueChanged<CodeConvoyPlan>? onSummarizeConvoyPlan;

  @override
  State<AiConvoyOverviewCard> createState() => _AiConvoyOverviewCardState();
}

class _AiConvoyOverviewCardState extends State<AiConvoyOverviewCard> {
  bool _expanded = true;

  @override
  Widget build(BuildContext context) {
    final plans = widget.convoyPlans;
    final planSessionIds = <int>{
      for (final plan in plans)
        for (final task in plan.tasks)
          if (task.sessionId != null && task.sessionId! > 0) task.sessionId!,
    };
    final sessionDiffById = <int, ({int add, int del})>{
      for (final s in widget.sessionSubagents)
        s.id: (add: s.diffAdditions, del: s.diffDeletions),
    };

    final standaloneSubagents = <CodeSubagentItem>[];
    final seenSubagentSessionIds = <int>{...planSessionIds};
    for (final item in widget.subagents) {
      if (item.sessionId > 0 && !seenSubagentSessionIds.add(item.sessionId)) {
        continue;
      }
      standaloneSubagents.add(item);
    }
    for (final child in widget.sessionSubagents) {
      if (!seenSubagentSessionIds.add(child.id)) continue;
      final isRun = isAiSessionRunning(child);
      final isFail =
          child.status.toLowerCase() == 'failed' ||
          child.currentStage.toLowerCase() == 'failed';
      final cleanTitle = stripSubagentTitleSuffix(
        resolveSessionDisplayTitle(
          session: child,
          fallback: '子会话 #${child.id}',
        ),
      );
      standaloneSubagents.add(
        CodeSubagentItem(
          sessionId: child.id,
          parentSessionId: child.parentSessionId ?? 0,
          title: cleanTitle,
          fullTitle: cleanTitle,
          agentType: child.isConvoyTask ? 'convoy' : 'subagent',
          status: isRun ? 'running' : (isFail ? 'failed' : 'completed'),
          needsAttention:
              child.currentStage.toLowerCase() == 'awaiting_approval',
          additions: child.diffAdditions,
          deletions: child.diffDeletions,
          convoyPlanId: child.convoyPlanId,
          convoyTaskId: child.convoyTaskId,
        ),
      );
    }

    if (plans.isEmpty && standaloneSubagents.isEmpty) {
      return const SizedBox.shrink();
    }

    final totalConvoyTasks = plans.fold<int>(
      0,
      (sum, plan) => sum + plan.tasks.length,
    );
    final completedConvoyTasks = plans.fold<int>(
      0,
      (sum, plan) => sum + plan.completedCount,
    );
    final runningCount =
        plans.fold<int>(0, (sum, p) => sum + p.runningCount) +
        standaloneSubagents.where((s) => s.isRunning).length;

    return Container(
      key: const ValueKey('ai_convoy_overview_card'),
      margin: const EdgeInsets.fromLTRB(16, 8, 16, 4),
      decoration: BoxDecoration(
        color: AppTheme.surfaceMuted.withValues(alpha: 0.65),
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: AppTheme.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          InkWell(
            key: const ValueKey('ai_convoy_overview_header'),
            borderRadius: BorderRadius.circular(14),
            onTap: () => setState(() => _expanded = !_expanded),
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
              child: Row(
                children: [
                  if (runningCount > 0)
                    const SizedBox.square(
                      dimension: 15,
                      child: CircularProgressIndicator(
                        strokeWidth: 2,
                        color: AppTheme.primaryBlue,
                      ),
                    )
                  else
                    const Icon(
                      Icons.account_tree_rounded,
                      size: 16,
                      color: AppTheme.primaryBlue,
                    ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      plans.isNotEmpty ? '多 Agent 编队' : '子会话编排',
                      style: TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 13,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ),
                  if (runningCount > 0) ...[
                    ConvoyPillBadge(
                      label: '$runningCount 执行中',
                      foreground: AppTheme.primaryBlue,
                      background: AppTheme.primaryBlueLight,
                    ),
                    const SizedBox(width: 6),
                  ],
                  if (totalConvoyTasks > 0)
                    ConvoyPillBadge(
                      label: '已完成 $completedConvoyTasks/$totalConvoyTasks',
                      foreground: completedConvoyTasks == totalConvoyTasks
                          ? AppTheme.success
                          : AppTheme.textSecondary,
                      background: completedConvoyTasks == totalConvoyTasks
                          ? AppTheme.successSoft
                          : AppTheme.surface,
                    ),
                  if (standaloneSubagents.isNotEmpty) ...[
                    if (totalConvoyTasks > 0) const SizedBox(width: 6),
                    ConvoyPillBadge(
                      label: '子会话 ${standaloneSubagents.length}',
                      foreground: AppTheme.textSecondary,
                      background: AppTheme.surface,
                    ),
                  ],
                  const SizedBox(width: 4),
                  Icon(
                    _expanded
                        ? Icons.keyboard_arrow_up_rounded
                        : Icons.keyboard_arrow_down_rounded,
                    size: 18,
                    color: AppTheme.textSecondary,
                  ),
                ],
              ),
            ),
          ),
          if (_expanded) ...[
            Divider(height: 1, color: AppTheme.border),
            Padding(
              padding: const EdgeInsets.fromLTRB(10, 8, 10, 10),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  for (var i = 0; i < plans.length; i++) ...[
                    if (i > 0) const SizedBox(height: 8),
                    _ConvoyPlanSection(
                      plan: plans[i],
                      sessionDiffById: sessionDiffById,
                      onOpenSession: widget.onOpenSession,
                      onCancelPlan: widget.onCancelConvoyPlan,
                      onRetryPlan: widget.onRetryConvoyPlan,
                      onMergePlan: widget.onMergeConvoyPlan,
                      onSummarizePlan: widget.onSummarizeConvoyPlan,
                    ),
                  ],
                  if (standaloneSubagents.isNotEmpty) ...[
                    if (plans.isNotEmpty) ...[
                      const SizedBox(height: 8),
                      Padding(
                        padding: const EdgeInsets.only(left: 4, bottom: 4, top: 2),
                        child: Text(
                          '子会话 (${standaloneSubagents.length})',
                          style: TextStyle(
                            color: AppTheme.textSecondary,
                            fontSize: 11,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                      ),
                    ],
                    for (final sub in standaloneSubagents)
                      CodeSubagentRowItem(
                        item: sub,
                        onOpenSession: widget.onOpenSession,
                      ),
                  ],
                ],
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _ConvoyPlanSection extends StatelessWidget {
  const _ConvoyPlanSection({
    required this.plan,
    required this.sessionDiffById,
    required this.onOpenSession,
    this.onCancelPlan,
    this.onRetryPlan,
    this.onMergePlan,
    this.onSummarizePlan,
  });

  final CodeConvoyPlan plan;
  final Map<int, ({int add, int del})> sessionDiffById;
  final ValueChanged<int> onOpenSession;
  final ValueChanged<String>? onCancelPlan;
  final ValueChanged<String>? onRetryPlan;
  final ValueChanged<String>? onMergePlan;
  final ValueChanged<CodeConvoyPlan>? onSummarizePlan;

  @override
  Widget build(BuildContext context) {
    final (statusLabel, fg, bg) = convoyPlanStatusStyle(plan.status);
    return Container(
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppTheme.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  plan.title.trim().isNotEmpty ? plan.title.trim() : '编队任务',
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 12.5,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
              const SizedBox(width: 8),
              Text(
                '已完成 ${plan.completedCount}/${plan.tasks.length}',
                style: TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 11,
                  fontFamily: 'monospace',
                ),
              ),
              const SizedBox(width: 6),
              ConvoyPillBadge(label: statusLabel, foreground: fg, background: bg),
            ],
          ),
          if (plan.error.trim().isNotEmpty) ...[
            const SizedBox(height: 6),
            Text(
              plan.error.trim(),
              style: const TextStyle(color: AppTheme.error, fontSize: 11.5),
            ),
          ],
          const SizedBox(height: 8),
          for (var i = 0; i < plan.tasks.length; i++) ...[
            if (i > 0) const SizedBox(height: 6),
            _ConvoyTaskRow(
              task: plan.tasks[i],
              diff: plan.tasks[i].sessionId != null
                  ? sessionDiffById[plan.tasks[i].sessionId!]
                  : null,
              onOpenSession: onOpenSession,
            ),
          ],
          ConvoyPlanActionFooter(
            plan: plan,
            isLive: true,
            onCancelPlan: onCancelPlan,
            onRetryPlan: onRetryPlan,
            onMergePlan: onMergePlan,
            onSummarizePlan: onSummarizePlan,
          ),
        ],
      ),
    );
  }
}

class _ConvoyTaskRow extends StatelessWidget {
  const _ConvoyTaskRow({
    required this.task,
    this.diff,
    required this.onOpenSession,
  });

  final CodeConvoyTaskItem task;
  final ({int add, int del})? diff;
  final ValueChanged<int> onOpenSession;

  @override
  Widget build(BuildContext context) {
    final canOpen = task.sessionId != null && task.sessionId! > 0;
    final detail = task.error.trim().isNotEmpty
        ? task.error.trim()
        : (task.summary.trim().isNotEmpty
              ? task.summary.trim()
              : task.prompt.trim());
    final depLabel = task.dependsOn.isNotEmpty
        ? '依赖: ${task.dependsOn.join(', ')}'
        : '可并行';
    final addCount = diff?.add ?? task.diffAdditions;
    final delCount = diff?.del ?? task.diffDeletions;

    return InkWell(
      key: ValueKey('convoy_task_row_${task.id}'),
      borderRadius: BorderRadius.circular(8),
      onTap: canOpen ? () => onOpenSession(task.sessionId!) : null,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 7),
        decoration: BoxDecoration(
          color: AppTheme.surfaceMuted.withValues(alpha: 0.45),
          borderRadius: BorderRadius.circular(8),
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.only(top: 2),
              child: buildConvoyTaskStatusIcon(task.status),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Flexible(
                        child: Text(
                          task.id,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            color: AppTheme.textPrimary,
                            fontSize: 12,
                            fontWeight: FontWeight.w700,
                            fontFamily: 'monospace',
                          ),
                        ),
                      ),
                      const SizedBox(width: 6),
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
                        decoration: BoxDecoration(
                          color: AppTheme.surface,
                          borderRadius: BorderRadius.circular(4),
                          border: Border.all(color: AppTheme.border),
                        ),
                        child: Text(
                          depLabel,
                          style: TextStyle(
                            color: AppTheme.textSecondary,
                            fontSize: 10,
                          ),
                        ),
                      ),
                      if (addCount > 0 || delCount > 0) ...[
                        const SizedBox(width: 6),
                        AiDiffStatBadges(
                          additions: addCount,
                          deletions: delCount,
                          fontSize: 10.5,
                        ),
                      ],
                    ],
                  ),
                  if (detail.isNotEmpty) ...[
                    const SizedBox(height: 3),
                    Text(
                      detail,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: task.error.trim().isNotEmpty
                            ? AppTheme.error
                            : AppTheme.textSecondary,
                        fontSize: 11.5,
                        height: 1.35,
                      ),
                    ),
                  ],
                ],
              ),
            ),
            if (canOpen) ...[
              const SizedBox(width: 6),
              Padding(
                padding: const EdgeInsets.only(top: 1),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(
                      '打开会话',
                      style: TextStyle(
                        color: AppTheme.primaryBlueText,
                        fontSize: 11,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    Icon(
                      Icons.chevron_right_rounded,
                      size: 15,
                      color: AppTheme.primaryBlueText,
                    ),
                  ],
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

/// Markdown 内联编队代码块卡片（对齐 PC 端 InlineSubtaskPlanCard）：
/// 识别 ```hoxi-subtasks / ```hoxi-convoy / ```subtasks-plan 并支持一键启动、取消、重试、合并与汇总。
class AiInlineConvoyPlanCard extends StatelessWidget {
  const AiInlineConvoyPlanCard({
    super.key,
    required this.plan,
    this.livePlans = const [],
    this.subagents = const [],
    this.onOpenSession,
    this.onStartConvoyPlan,
    this.onCancelConvoyPlan,
    this.onRetryConvoyPlan,
    this.onMergeConvoyPlan,
    this.onSummarizeConvoyPlan,
  });

  final CodeConvoyPlan plan;
  final List<CodeConvoyPlan> livePlans;
  final List<CodeSubagentItem> subagents;
  final ValueChanged<int>? onOpenSession;
  final ValueChanged<CodeConvoyPlan>? onStartConvoyPlan;
  final ValueChanged<String>? onCancelConvoyPlan;
  final ValueChanged<String>? onRetryConvoyPlan;
  final ValueChanged<String>? onMergeConvoyPlan;
  final ValueChanged<CodeConvoyPlan>? onSummarizeConvoyPlan;

  @override
  Widget build(BuildContext context) {
    final matchedLive = livePlans
        .where(
          (p) =>
              p.id == plan.id ||
              (p.title.trim().isNotEmpty &&
                  p.title.trim() == plan.title.trim()) ||
              p.tasks.any((lt) => plan.tasks.any((pt) => pt.id == lt.id)),
        )
        .firstOrNull;
    final effectivePlan = matchedLive ?? plan;
    final (statusLabel, fg, bg) = convoyPlanStatusStyle(effectivePlan.status);

    return Container(
      key: ValueKey('inline_convoy_plan_${effectivePlan.title}'),
      margin: const EdgeInsets.symmetric(vertical: 6),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppTheme.surfaceMuted.withValues(alpha: 0.65),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppTheme.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Icon(
                Icons.account_tree_rounded,
                size: 15,
                color: AppTheme.primaryBlue,
              ),
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  effectivePlan.title.trim().isNotEmpty
                      ? effectivePlan.title.trim()
                      : '并行子任务编排',
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 12.5,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
              const SizedBox(width: 6),
              Text(
                '${effectivePlan.tasks.length} 个子任务',
                style: TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 11,
                ),
              ),
              if (matchedLive != null) ...[
                const SizedBox(width: 6),
                ConvoyPillBadge(label: statusLabel, foreground: fg, background: bg),
              ],
            ],
          ),
          const SizedBox(height: 8),
          for (var i = 0; i < effectivePlan.tasks.length; i++) ...[
            if (i > 0) const SizedBox(height: 6),
            _ConvoyTaskRow(
              task: effectivePlan.tasks[i],
              onOpenSession: (sessionId) => onOpenSession?.call(sessionId),
            ),
          ],
          ConvoyPlanActionFooter(
            plan: effectivePlan,
            isLive: matchedLive != null,
            onStartPlan: onStartConvoyPlan,
            onCancelPlan: onCancelConvoyPlan,
            onRetryPlan: onRetryConvoyPlan,
            onMergePlan: onMergeConvoyPlan,
            onSummarizePlan: onSummarizeConvoyPlan,
          ),
        ],
      ),
    );
  }
}
