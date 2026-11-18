import 'package:flutter/material.dart';

import '../../../../core/theme/app_theme.dart';
import '../../models/ai_dev_session.dart';
import '../../models/code_convoy_plan.dart';
import 'ai_chat_state_widgets.dart';

/// 会话卡片底部的可展开编队/子任务抽屉（对齐 PC 端 SessionNavItem + SubagentNavRow）。
class CodeSessionSubagentsSection extends StatefulWidget {
  const CodeSessionSubagentsSection({
    super.key,
    required this.session,
    this.onOpenSubagent,
  });

  final AiDevSession session;
  final ValueChanged<AiDevSession>? onOpenSubagent;

  @override
  State<CodeSessionSubagentsSection> createState() =>
      _CodeSessionSubagentsSectionState();
}

class _CodeSessionSubagentsSectionState
    extends State<CodeSessionSubagentsSection> {
  late bool _expanded = widget.session.runningSubagentsCount > 0;

  @override
  void didUpdateWidget(covariant CodeSessionSubagentsSection oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.session.runningSubagentsCount == 0 &&
        widget.session.runningSubagentsCount > 0) {
      _expanded = true;
    }
  }

  @override
  Widget build(BuildContext context) {
    final subagents = widget.session.subagents;
    if (subagents.isEmpty) return const SizedBox.shrink();

    final hasConvoy = subagents.any((s) => s.isConvoyTask);
    final runningCount = widget.session.runningSubagentsCount;
    final completedCount = subagents.where((s) {
      final st = s.status.toLowerCase();
      final stage = s.currentStage.toLowerCase();
      return !isAiSessionRunning(s) && st != 'failed' && stage != 'failed';
    }).length;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const SizedBox(height: 10),
        InkWell(
          key: ValueKey('session_subagents_toggle_${widget.session.id}'),
          borderRadius: BorderRadius.circular(8),
          onTap: () => setState(() => _expanded = !_expanded),
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
            decoration: BoxDecoration(
              color: AppTheme.surfaceMuted.withValues(alpha: 0.7),
              borderRadius: BorderRadius.circular(8),
            ),
            child: Row(
              children: [
                Icon(
                  hasConvoy
                      ? Icons.account_tree_rounded
                      : Icons.smart_toy_outlined,
                  size: 14,
                  color: AppTheme.primaryBlue,
                ),
                const SizedBox(width: 6),
                Text(
                  hasConvoy
                      ? '编队 · ${subagents.length} 个子任务'
                      : '${subagents.length} 个子会话',
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 11.5,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                const SizedBox(width: 8),
                if (runningCount > 0)
                  Text(
                    '$runningCount 运行中',
                    style: const TextStyle(
                      color: AppTheme.primaryBlue,
                      fontSize: 11,
                      fontWeight: FontWeight.w600,
                    ),
                  )
                else
                  Text(
                    '已完成 $completedCount/${subagents.length}',
                    style: TextStyle(
                      color: AppTheme.textSecondary,
                      fontSize: 11,
                    ),
                  ),
                const Spacer(),
                Icon(
                  _expanded
                      ? Icons.keyboard_arrow_up_rounded
                      : Icons.keyboard_arrow_down_rounded,
                  size: 16,
                  color: AppTheme.textSecondary,
                ),
              ],
            ),
          ),
        ),
        if (_expanded) ...[
          const SizedBox(height: 6),
          Container(
            margin: const EdgeInsets.only(left: 10),
            padding: const EdgeInsets.only(left: 10),
            decoration: BoxDecoration(
              border: Border(
                left: BorderSide(color: AppTheme.border, width: 1.5),
              ),
            ),
            child: Column(
              children: [
                for (final child in subagents)
                  InkWell(
                    key: ValueKey('session_subagent_item_${child.id}'),
                    borderRadius: BorderRadius.circular(8),
                    onTap: widget.onOpenSubagent == null
                        ? null
                        : () => widget.onOpenSubagent!(child),
                    child: Padding(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 6,
                        vertical: 5,
                      ),
                      child: Row(
                        children: [
                          buildConvoyStatusIcon(
                            isRunning: isAiSessionRunning(child),
                            isFailed:
                                child.status.toLowerCase() == 'failed' ||
                                child.currentStage.toLowerCase() == 'failed',
                            isApproval:
                                child.currentStage.toLowerCase() ==
                                'awaiting_approval',
                            isCompleted:
                                !isAiSessionRunning(child) &&
                                child.status.toLowerCase() != 'failed' &&
                                child.currentStage.toLowerCase() != 'failed',
                          ),
                          const SizedBox(width: 7),
                          Expanded(
                            child: Text(
                              stripSubagentTitleSuffix(
                                resolveSessionDisplayTitle(
                                  session: child,
                                  fallback: child.convoyTaskId.isNotEmpty
                                      ? child.convoyTaskId
                                      : '子任务 #${child.id}',
                                ),
                              ),
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                              style: TextStyle(
                                color: AppTheme.textPrimary,
                                fontSize: 12,
                                fontWeight: FontWeight.w500,
                              ),
                            ),
                          ),
                          if (child.diffAdditions > 0 ||
                              child.diffDeletions > 0) ...[
                            const SizedBox(width: 6),
                            AiDiffStatBadges(
                              additions: child.diffAdditions,
                              deletions: child.diffDeletions,
                              fontSize: 10.5,
                            ),
                          ],
                          const SizedBox(width: 4),
                          Icon(
                            Icons.chevron_right_rounded,
                            size: 15,
                            color: AppTheme.textSecondary,
                          ),
                        ],
                      ),
                    ),
                  ),
              ],
            ),
          ),
        ],
      ],
    );
  }
}

class CodeSubagentRowItem extends StatelessWidget {
  const CodeSubagentRowItem({
    super.key,
    required this.item,
    required this.onOpenSession,
  });

  final CodeSubagentItem item;
  final ValueChanged<int> onOpenSession;

  @override
  Widget build(BuildContext context) {
    final canOpen = item.sessionId > 0;
    return InkWell(
      key: ValueKey('subagent_row_${item.sessionId}_${item.title}'),
      borderRadius: BorderRadius.circular(8),
      onTap: canOpen ? () => onOpenSession(item.sessionId) : null,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 6),
        child: Row(
          children: [
            buildConvoyStatusIcon(
              isRunning: item.isRunning,
              isFailed: item.isFailed,
              isApproval: item.needsAttention,
              isCompleted: !item.isRunning && !item.isFailed,
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                stripSubagentTitleSuffix(item.title),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            if (item.additions > 0 || item.deletions > 0) ...[
              const SizedBox(width: 6),
              AiDiffStatBadges(
                additions: item.additions,
                deletions: item.deletions,
                fontSize: 10.5,
              ),
            ],
            const SizedBox(width: 6),
            Text(
              item.needsAttention
                  ? '待审批'
                  : (item.isRunning ? '运行中' : (item.isFailed ? '失败' : '已完成')),
              style: TextStyle(
                color: item.needsAttention
                    ? AppTheme.warning
                    : (item.isRunning
                          ? AppTheme.primaryBlue
                          : (item.isFailed
                                ? AppTheme.error
                                : AppTheme.textSecondary)),
                fontSize: 11,
                fontWeight: FontWeight.w600,
              ),
            ),
            if (canOpen) ...[
              const SizedBox(width: 2),
              Icon(
                Icons.chevron_right_rounded,
                size: 15,
                color: AppTheme.textSecondary,
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class ConvoyPlanActionFooter extends StatelessWidget {
  const ConvoyPlanActionFooter({
    super.key,
    required this.plan,
    required this.isLive,
    this.onStartPlan,
    this.onCancelPlan,
    this.onRetryPlan,
    this.onMergePlan,
    this.onSummarizePlan,
  });

  final CodeConvoyPlan plan;
  final bool isLive;
  final ValueChanged<CodeConvoyPlan>? onStartPlan;
  final ValueChanged<String>? onCancelPlan;
  final ValueChanged<String>? onRetryPlan;
  final ValueChanged<String>? onMergePlan;
  final ValueChanged<CodeConvoyPlan>? onSummarizePlan;

  @override
  Widget build(BuildContext context) {
    final showStart = !isLive && onStartPlan != null && plan.tasks.isNotEmpty;
    final showCancel = isLive && !plan.isTerminal && onCancelPlan != null;
    final showRetry = isLive && plan.canRetry && onRetryPlan != null;
    final showMerge = isLive && plan.canMerge && onMergePlan != null;
    final showMergedBadge = isLive && plan.mergedAt != null;
    final showSummarize = isLive && plan.isTerminal && onSummarizePlan != null;

    if (!showStart &&
        !showCancel &&
        !showRetry &&
        !showMerge &&
        !showMergedBadge &&
        !showSummarize) {
      return const SizedBox.shrink();
    }

    return Padding(
      padding: const EdgeInsets.only(top: 8),
      child: Wrap(
        spacing: 8,
        runSpacing: 6,
        alignment: WrapAlignment.end,
        crossAxisAlignment: WrapCrossAlignment.center,
        children: [
          if (showStart)
            FilledButton.tonalIcon(
              key: ValueKey('convoy_start_btn_${plan.id}'),
              onPressed: () => onStartPlan!(plan),
              style: FilledButton.styleFrom(
                visualDensity: VisualDensity.compact,
                padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
              ),
              icon: const Icon(Icons.play_arrow_rounded, size: 15),
              label: Text(
                '启动编队 (${plan.tasks.length})',
                style: const TextStyle(fontSize: 11.5, fontWeight: FontWeight.w700),
              ),
            ),
          if (showCancel)
            OutlinedButton.icon(
              key: ValueKey('convoy_cancel_btn_${plan.id}'),
              onPressed: () => onCancelPlan!(plan.id),
              style: OutlinedButton.styleFrom(
                visualDensity: VisualDensity.compact,
                padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
              ),
              icon: const Icon(Icons.stop_rounded, size: 14),
              label: const Text('取消编队', style: TextStyle(fontSize: 11.5)),
            ),
          if (showRetry)
            OutlinedButton.icon(
              key: ValueKey('convoy_retry_btn_${plan.id}'),
              onPressed: () => onRetryPlan!(plan.id),
              style: OutlinedButton.styleFrom(
                visualDensity: VisualDensity.compact,
                padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
              ),
              icon: const Icon(Icons.refresh_rounded, size: 14),
              label: const Text('重试失败项', style: TextStyle(fontSize: 11.5)),
            ),
          if (showMergedBadge)
            ConvoyPillBadge(
              label: '已合并至主分支',
              foreground: AppTheme.success,
              background: AppTheme.successSoft,
            ),
          if (showMerge)
            FilledButton.tonalIcon(
              key: ValueKey('convoy_merge_btn_${plan.id}'),
              onPressed: () => onMergePlan!(plan.id),
              style: FilledButton.styleFrom(
                visualDensity: VisualDensity.compact,
                padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
              ),
              icon: const Icon(Icons.merge_type_rounded, size: 15),
              label: const Text(
                '合并回主分支',
                style: TextStyle(fontSize: 11.5, fontWeight: FontWeight.w700),
              ),
            ),
          if (showSummarize)
            FilledButton.icon(
              key: ValueKey('convoy_summarize_btn_${plan.id}'),
              onPressed: () => onSummarizePlan!(plan),
              style: FilledButton.styleFrom(
                backgroundColor: AppTheme.primaryBlue,
                foregroundColor: Colors.white,
                visualDensity: VisualDensity.compact,
                padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
              ),
              icon: const Icon(Icons.summarize_outlined, size: 14),
              label: const Text(
                '汇总到对话',
                style: TextStyle(fontSize: 11.5, fontWeight: FontWeight.w700),
              ),
            ),
        ],
      ),
    );
  }
}

class ConvoyPillBadge extends StatelessWidget {
  const ConvoyPillBadge({
    super.key,
    required this.label,
    required this.foreground,
    required this.background,
  });

  final String label;
  final Color foreground;
  final Color background;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 2),
      decoration: BoxDecoration(
        color: background,
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: foreground,
          fontSize: 10.5,
          fontWeight: FontWeight.w700,
        ),
      ),
    );
  }
}

bool isAiSessionRunning(AiDevSession session) {
  final st = session.status.toLowerCase();
  final stage = session.currentStage.toLowerCase();
  return st == 'running' ||
      st == 'busy' ||
      stage == 'running' ||
      stage == 'executing' ||
      stage == 'instruction_queued';
}

Widget buildConvoyStatusIcon({
  required bool isRunning,
  required bool isFailed,
  required bool isApproval,
  required bool isCompleted,
}) {
  if (isApproval) {
    return const Icon(
      Icons.error_outline_rounded,
      size: 13.5,
      color: AppTheme.warning,
    );
  }
  if (isRunning) {
    return const SizedBox.square(
      dimension: 12,
      child: CircularProgressIndicator(
        strokeWidth: 1.8,
        color: AppTheme.primaryBlue,
      ),
    );
  }
  if (isFailed) {
    return const Icon(
      Icons.cancel_outlined,
      size: 13.5,
      color: AppTheme.error,
    );
  }
  if (isCompleted) {
    return const Icon(
      Icons.check_circle_outline_rounded,
      size: 13.5,
      color: AppTheme.success,
    );
  }
  return Icon(
    Icons.radio_button_unchecked_rounded,
    size: 13.5,
    color: AppTheme.textSecondary,
  );
}

Widget buildConvoyTaskStatusIcon(String status) {
  final lower = status.toLowerCase().trim();
  return switch (lower) {
    'running' => const SizedBox.square(
      dimension: 12,
      child: CircularProgressIndicator(
        strokeWidth: 1.8,
        color: AppTheme.primaryBlue,
      ),
    ),
    'completed' || 'succeeded' => const Icon(
      Icons.check_circle_outline_rounded,
      size: 14,
      color: AppTheme.success,
    ),
    'failed' => const Icon(
      Icons.cancel_outlined,
      size: 14,
      color: AppTheme.error,
    ),
    'blocked' => const Icon(
      Icons.warning_amber_rounded,
      size: 14,
      color: AppTheme.warning,
    ),
    _ => Icon(
      Icons.radio_button_unchecked_rounded,
      size: 13.5,
      color: AppTheme.textSecondary,
    ),
  };
}

(String, Color, Color) convoyPlanStatusStyle(String status) {
  final lower = status.toLowerCase().trim();
  return switch (lower) {
    'running' => ('执行中', AppTheme.primaryBlue, AppTheme.primaryBlueLight),
    'completed' || 'succeeded' => ('已完成', AppTheme.success, AppTheme.successSoft),
    'failed' => ('失败', AppTheme.error, AppTheme.errorSoft),
    'blocked' => ('已阻塞', AppTheme.warning, AppTheme.warningSoft),
    'canceled' || 'cancelled' => (
      '已取消',
      AppTheme.textSecondary,
      AppTheme.surfaceMuted,
    ),
    _ => ('排队中', AppTheme.textSecondary, AppTheme.surfaceMuted),
  };
}
