import 'package:flutter/material.dart';

import '../../../../core/theme/app_theme.dart';
import '../../../../shared/widgets/panel/panel_card.dart';
import '../../models/ai_dev_session.dart';
import '../../models/code_task.dart';
import '../../../../core/l10n/l10n.dart';
import 'ai_chat_state_widgets.dart';
import 'ai_convoy_widgets.dart';

class CodeSessionListCard extends StatelessWidget {
  const CodeSessionListCard({
    super.key,
    required this.session,
    this.task,
    this.projectName = '',
    required this.onTap,
    this.onOpenSubagent,
    this.onOpenTaskDetail,
  });

  final AiDevSession session;
  final CodeTask? task;
  final String projectName;
  final VoidCallback onTap;
  final ValueChanged<AiDevSession>? onOpenSubagent;
  final VoidCallback? onOpenTaskDetail;

  @override
  Widget build(BuildContext context) {
    final stage = task?.status.isNotEmpty == true
        ? task!.status
        : session.currentStage.isEmpty
        ? session.status
        : session.currentStage;
    final title = resolveSessionDisplayTitle(
      session: session,
      taskTitle: task?.title,
      fallback: '开发 #${session.id}',
    );
    final updatedAt =
        task?.updatedAt ??
        task?.createdAt ??
        session.lastInstructionAt ??
        session.updatedAt ??
        session.createdAt;
    final hasBranch = session.worktreeBranch.isNotEmpty;
    final trimmedProjectName = projectName.trim();
    final hasProjectName = trimmedProjectName.isNotEmpty;
    final rawModel = session.providerModel.trim().isNotEmpty
        ? session.providerModel
        : (task?.providerModel ?? '');
    final rawAgent = session.agentName.trim().isNotEmpty
        ? session.agentName
        : (task?.agentName ?? '');
    final modelLabel = formatSessionModelDisplayLabel(
      providerModel: rawModel,
      agentName: rawAgent,
    );
    final hasSessionDiff =
        session.diffAdditions > 0 || session.diffDeletions > 0;
    final diffAdditions = hasSessionDiff
        ? session.diffAdditions
        : (task?.diffAdditions ?? 0);
    final diffDeletions = hasSessionDiff
        ? session.diffDeletions
        : (task?.diffDeletions ?? 0);
    final hasDiffStats = diffAdditions > 0 || diffDeletions > 0;
    final timeLabel = formatCodeHubTaskTime(
      updatedAt,
      stage: stage,
      status: session.status,
    );

    return PanelCard(
      onTap: onTap,
      onLongPress: onOpenTaskDetail,
      padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 14),
      title: Text(title, maxLines: 1, overflow: TextOverflow.ellipsis),
      trailing: _StageBadge(stage: stage, onTap: onOpenTaskDetail),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Row(
                  children: [
                    if (hasProjectName) ...[
                      Flexible(
                        child: ConstrainedBox(
                          constraints: const BoxConstraints(maxWidth: 96),
                          child: Text(
                            trimmedProjectName,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(
                              color: AppTheme.textSecondary,
                              fontSize: 12,
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                        ),
                      ),
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 6),
                        child: Text(
                          '·',
                          style: TextStyle(
                            color: AppTheme.textSecondary,
                            fontSize: 12,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                      ),
                    ],
                    if (hasBranch) ...[
                      Icon(
                        Icons.fork_right_rounded,
                        size: 15,
                        color: AppTheme.textSecondary,
                      ),
                      const SizedBox(width: 4),
                      Flexible(
                        child: ConstrainedBox(
                          constraints: const BoxConstraints(maxWidth: 110),
                          child: Text(
                            session.worktreeBranch,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(
                              color: AppTheme.textSecondary,
                              fontSize: 12,
                              fontWeight: FontWeight.w500,
                            ),
                          ),
                        ),
                      ),
                      const SizedBox(width: 8),
                    ],
                    Icon(
                      Icons.smart_toy_outlined,
                      size: 15,
                      color: AppTheme.textSecondary,
                    ),
                    const SizedBox(width: 4),
                    Flexible(
                      child: ConstrainedBox(
                        constraints: const BoxConstraints(maxWidth: 116),
                        child: Text(
                          modelLabel,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            color: AppTheme.textSecondary,
                            fontSize: 12,
                          ),
                        ),
                      ),
                    ),
                  ],
                ),
              ),
              if (hasDiffStats) ...[
                const SizedBox(width: 6),
                AiDiffStatBadges(
                  additions: diffAdditions,
                  deletions: diffDeletions,
                  fontSize: 11.5,
                ),
              ],
              if (timeLabel.isNotEmpty) ...[
                const SizedBox(width: 8),
                Text(
                  timeLabel,
                  style: TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 12,
                  ),
                ),
              ],
              const SizedBox(width: 2),
              Icon(
                Icons.chevron_right_rounded,
                size: 18,
                color: AppTheme.textSecondary,
              ),
            ],
          ),
          if (session.hasSubagents)
            CodeSessionSubagentsSection(
              session: session,
              onOpenSubagent: onOpenSubagent,
            ),
        ],
      ),
    );
  }
}

/// 当某个项目仅有 0 或 1 个会话任务时，直接展示项目卡片（不套外层折叠壳）
class CodeSingleProjectCard extends StatelessWidget {
  const CodeSingleProjectCard({
    super.key,
    required this.projectName,
    this.session,
    this.task,
    this.isActive = false,
    this.onTap,
    this.onOpenSubagent,
    this.onOpenTaskDetail,
  });

  final String projectName;
  final AiDevSession? session;
  final CodeTask? task;
  final bool isActive;
  final VoidCallback? onTap;
  final ValueChanged<AiDevSession>? onOpenSubagent;
  final VoidCallback? onOpenTaskDetail;

  @override
  Widget build(BuildContext context) {
    final activeSession = session;
    final stage = task?.status.isNotEmpty == true
        ? task!.status
        : activeSession == null
        ? ''
        : (activeSession.currentStage.isEmpty
              ? activeSession.status
              : activeSession.currentStage);
    final taskTitle = resolveSessionDisplayTitle(
      session: activeSession,
      taskTitle: task?.title,
    );
    final showTaskSubtitle =
        taskTitle.trim().isNotEmpty && taskTitle.trim() != projectName.trim();
    final updatedAt =
        task?.updatedAt ??
        task?.createdAt ??
        activeSession?.lastInstructionAt ??
        activeSession?.updatedAt ??
        activeSession?.createdAt;
    final hasBranch = activeSession?.worktreeBranch.isNotEmpty == true;
    final rawModel = activeSession == null
        ? (task?.providerModel ?? '')
        : (activeSession.providerModel.trim().isNotEmpty
              ? activeSession.providerModel
              : (task?.providerModel ?? ''));
    final rawAgent = activeSession == null
        ? (task?.agentName ?? '')
        : (activeSession.agentName.trim().isNotEmpty
              ? activeSession.agentName
              : (task?.agentName ?? ''));
    final modelLabel = activeSession == null
        ? ''
        : formatSessionModelDisplayLabel(
            providerModel: rawModel,
            agentName: rawAgent,
          );
    final hasSessionDiff = activeSession != null &&
        (activeSession.diffAdditions > 0 || activeSession.diffDeletions > 0);
    final diffAdditions = activeSession == null
        ? 0
        : (hasSessionDiff
              ? activeSession.diffAdditions
              : (task?.diffAdditions ?? 0));
    final diffDeletions = activeSession == null
        ? 0
        : (hasSessionDiff
              ? activeSession.diffDeletions
              : (task?.diffDeletions ?? 0));
    final hasDiffStats = diffAdditions > 0 || diffDeletions > 0;
    final timeLabel = formatCodeHubTaskTime(
      updatedAt,
      stage: stage,
      status: activeSession?.status ?? '',
    );

    return Card(
      elevation: 0,
      margin: EdgeInsets.zero,
      clipBehavior: Clip.antiAlias,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(16),
      ),
      child: InkWell(
        onTap: onTap,
        onLongPress: onOpenTaskDetail,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 13),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Container(
                    width: 28,
                    height: 28,
                    decoration: BoxDecoration(
                      color: AppTheme.primaryBlueLight,
                      borderRadius: BorderRadius.circular(8),
                    ),
                    child: const Icon(
                      Icons.folder_outlined,
                      size: 16,
                      color: AppTheme.primaryBlue,
                    ),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Text(
                      projectName,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: Theme.of(context).textTheme.titleSmall?.copyWith(
                        fontWeight: FontWeight.w800,
                      ),
                    ),
                  ),
                  if (activeSession != null)
                    _StageBadge(stage: stage, onTap: onOpenTaskDetail)
                  else
                    Text(
                      context.l10n.codeWsHubNoProjectTasks,
                      style: Theme.of(context).textTheme.bodySmall?.copyWith(
                        color: AppTheme.textSecondary,
                      ),
                    ),
                ],
              ),
              if (activeSession != null) ...[
                const SizedBox(height: 8),
                Row(
                  children: [
                    const SizedBox(width: 38),
                    Expanded(
                      child: Row(
                        children: [
                          if (showTaskSubtitle) ...[
                            Flexible(
                              child: Text(
                                taskTitle,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: TextStyle(
                                  color: AppTheme.textPrimary,
                                  fontSize: 12.5,
                                  fontWeight: FontWeight.w600,
                                ),
                              ),
                            ),
                            const SizedBox(width: 8),
                          ],
                          if (hasBranch) ...[
                            Icon(
                              Icons.fork_right_rounded,
                              size: 14,
                              color: AppTheme.textSecondary,
                            ),
                            const SizedBox(width: 3),
                            Flexible(
                              child: ConstrainedBox(
                                constraints: const BoxConstraints(maxWidth: 96),
                                child: Text(
                                  activeSession.worktreeBranch,
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                  style: TextStyle(
                                    color: AppTheme.textSecondary,
                                    fontSize: 12,
                                  ),
                                ),
                              ),
                            ),
                            const SizedBox(width: 8),
                          ],
                          Icon(
                            Icons.smart_toy_outlined,
                            size: 14,
                            color: AppTheme.textSecondary,
                          ),
                          const SizedBox(width: 3),
                          Flexible(
                            child: ConstrainedBox(
                              constraints: const BoxConstraints(maxWidth: 108),
                              child: Text(
                                modelLabel,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: TextStyle(
                                  color: AppTheme.textSecondary,
                                  fontSize: 12,
                                ),
                              ),
                            ),
                          ),
                        ],
                      ),
                    ),
                    if (hasDiffStats) ...[
                      const SizedBox(width: 6),
                      AiDiffStatBadges(
                        additions: diffAdditions,
                        deletions: diffDeletions,
                        fontSize: 11.5,
                      ),
                    ],
                    if (timeLabel.isNotEmpty) ...[
                      const SizedBox(width: 8),
                      Text(
                        timeLabel,
                        style: TextStyle(
                          color: AppTheme.textSecondary,
                          fontSize: 12,
                        ),
                      ),
                    ],
                    const SizedBox(width: 2),
                    Icon(
                      Icons.chevron_right_rounded,
                      size: 18,
                      color: AppTheme.textSecondary,
                    ),
                  ],
                ),
                if (activeSession.hasSubagents)
                  Padding(
                    padding: const EdgeInsets.only(left: 38),
                    child: CodeSessionSubagentsSection(
                      session: activeSession,
                      onOpenSubagent: onOpenSubagent,
                    ),
                  ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

class CodeHubErrorCard extends StatelessWidget {
  const CodeHubErrorCard({
    super.key,
    required this.message,
    required this.onRetry,
  });

  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return CodeHubEmptyCard(
      icon: Icons.error_outline_rounded,
      title: '加载失败',
      description: message,
      actionLabel: '重试',
      onAction: onRetry,
    );
  }
}

class CodeHubEmptyCard extends StatelessWidget {
  const CodeHubEmptyCard({
    super.key,
    required this.icon,
    required this.title,
    required this.description,
    this.actionLabel,
    this.onAction,
  });

  final IconData icon;
  final String title;
  final String description;
  final String? actionLabel;
  final VoidCallback? onAction;

  @override
  Widget build(BuildContext context) {
    return PanelCard(
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 36),
        child: Center(
          child: Column(
            children: [
              Icon(icon, size: 46, color: AppTheme.textSecondary),
              const SizedBox(height: 16),
              Text(title, style: Theme.of(context).textTheme.titleMedium),
              const SizedBox(height: 8),
              Text(
                description,
                textAlign: TextAlign.center,
                style: TextStyle(
                  color: AppTheme.textSecondary,
                  height: 1.5,
                ),
              ),
              if (actionLabel != null && onAction != null) ...[
                const SizedBox(height: 22),
                FilledButton(onPressed: onAction, child: Text(actionLabel!)),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

class _StageBadge extends StatelessWidget {
  const _StageBadge({required this.stage, this.onTap});

  final String stage;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final normalized = stage.toLowerCase();
    final failed = normalized == 'failed';
    final completed =
        normalized == 'completed' || normalized == 'preview_ready';
    final foreground = failed
        ? AppTheme.error
        : completed
        ? AppTheme.success
        : AppTheme.primaryBlue;
    final background = failed
        ? AppTheme.errorSoft
        : completed
        ? AppTheme.successSoft
        : AppTheme.primaryBlueLight;
    final badge = Container(
      padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 5),
      decoration: BoxDecoration(
        color: background,
        borderRadius: BorderRadius.circular(99),
      ),
      child: Text(
        _stageLabel(stage),
        style: TextStyle(
          color: foreground,
          fontSize: 11,
          fontWeight: FontWeight.w800,
        ),
      ),
    );
    if (onTap == null) return badge;
    return InkWell(
      borderRadius: BorderRadius.circular(99),
      onTap: onTap,
      child: badge,
    );
  }
}

String _stageLabel(String stage) => switch (stage) {
  'instruction_queued' => '排队中',
  'awaiting_approval' => '待审批',
  'executing' => '执行中',
  'running' => '运行中',
  'completed' => '已完成',
  'preview_ready' => '预览就绪',
  'failed' => '失败',
  'idle' || 'active' => '空闲',
  _ => stage.isEmpty ? '会话中' : stage,
};

bool _isActiveStage(String stage, [String status = '']) {
  final s = stage.toLowerCase().trim();
  final st = status.toLowerCase().trim();
  return s == 'running' ||
      s == 'executing' ||
      s == 'instruction_queued' ||
      s == 'awaiting_approval' ||
      s == 'busy' ||
      st == 'running' ||
      st == 'executing' ||
      st == 'instruction_queued' ||
      st == 'awaiting_approval' ||
      st == 'busy';
}

String formatCodeHubTaskTime(
  DateTime? value, {
  String stage = '',
  String status = '',
  DateTime? now,
}) {
  if (_isActiveStage(stage, status) || value == null) return '';
  final difference = (now ?? DateTime.now()).difference(value);
  if (difference.inMinutes < 1) return '';
  if (difference.inHours < 1) return '${difference.inMinutes} 分钟前';
  if (difference.inDays < 1) return '${difference.inHours} 小时前';
  if (difference.inDays < 7) return '${difference.inDays} 天前';
  return '${value.month}/${value.day}';
}
