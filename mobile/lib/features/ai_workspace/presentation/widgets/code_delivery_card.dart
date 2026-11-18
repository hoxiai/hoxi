import 'package:flutter/material.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/theme/app_theme.dart';
import '../../models/ai_dev_session.dart';
import '../../models/code_delivery_job.dart';

class CodeDeliveryCard extends StatelessWidget {
  const CodeDeliveryCard({
    super.key,
    required this.session,
    required this.delivery,
    required this.loading,
    required this.errorMessage,
    required this.onStart,
  });

  final AiDevSession session;
  final CodeDeliveryJob? delivery;
  final bool loading;
  final String? errorMessage;
  final VoidCallback onStart;

  bool get _managed =>
      session.isolationMode == 'multi_worktree' ||
      session.isolationMode == 'worktree' ||
      (session.sourceWorkDir.isNotEmpty && session.worktreeBranch.isNotEmpty);

  @override
  Widget build(BuildContext context) {
    final job = delivery;
    final accent = _accent(job);
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.fromLTRB(16, 0, 16, 8),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(14),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(Icons.rocket_launch_outlined, color: accent, size: 17),
              const SizedBox(width: 6),
              Flexible(
                child: Text(
                  context.l10n.codeWsDeliveryTitle,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 13,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
              if (job != null) ...[
                const SizedBox(width: 8),
                _StatusBadge(job: job, color: accent),
              ],
              const Spacer(),
              if (_showAction(job))
                FilledButton.icon(
                  style: FilledButton.styleFrom(
                    visualDensity: VisualDensity.compact,
                    padding: const EdgeInsets.symmetric(
                      horizontal: 10,
                      vertical: 6,
                    ),
                    textStyle: const TextStyle(
                      fontSize: 12,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                  onPressed: loading ? null : onStart,
                  icon: loading
                      ? const SizedBox.square(
                          dimension: 14,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.rocket_launch_outlined, size: 15),
                  label: Text(
                    job?.canDeliverPending == true
                        ? context.l10n.codeWsDeliveryDeliverLatest
                        : job?.canRetry == true
                        ? context.l10n.codeWsDeliveryRetry
                        : context.l10n.codeWsDeliveryStart,
                  ),
                ),
            ],
          ),
          const SizedBox(height: 4),
          Text(
            _description(context, job),
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
            style: TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 12,
              height: 1.35,
            ),
          ),
          if (job?.isActive == true) ...[
            const SizedBox(height: 8),
            LinearProgressIndicator(
              value: job!.progress.clamp(0, 100) / 100,
              minHeight: 4,
              borderRadius: BorderRadius.circular(99),
              color: accent,
              backgroundColor: AppTheme.surfaceMuted,
            ),
            const SizedBox(height: 6),
            Text(
              _progressMeta(context, job),
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 11.5),
            ),
          ],
          if (job?.conflictFiles.isNotEmpty == true) ...[
            const SizedBox(height: 8),
            for (final file in job!.conflictFiles.take(8))
              Padding(
                padding: const EdgeInsets.only(top: 3),
                child: Text(
                  '• $file',
                  style: const TextStyle(
                    color: AppTheme.warning,
                    fontFamily: 'monospace',
                    fontSize: 11.5,
                  ),
                ),
              ),
          ],
          if (errorMessage?.isNotEmpty == true) ...[
            const SizedBox(height: 6),
            Text(
              errorMessage!,
              style: const TextStyle(color: AppTheme.error, fontSize: 11.5),
            ),
          ],
        ],
      ),
    );
  }

  bool _showAction(CodeDeliveryJob? job) {
    if (!_managed || session.status == 'delivered') return false;
    return job == null || job.canRetry || job.canDeliverPending;
  }

  String _description(BuildContext context, CodeDeliveryJob? job) {
    if (!_managed) return context.l10n.codeWsDeliveryUnavailable;
    if (job == null) return context.l10n.codeWsDeliveryEmpty;
    if (job.status == 'conflict' || job.status == 'partial') {
      return context.l10n.codeWsDeliveryConflictHint;
    }
    if (job.status == 'failed') {
      return job.errorMessage.isEmpty
          ? context.l10n.codeWsDeliveryFailed
          : job.errorMessage;
    }
    if (job.isCompleted) {
      final commit = job.resultCommit.length > 10
          ? job.resultCommit.substring(0, 10)
          : job.resultCommit;
      final suffix = commit.isEmpty ? '' : ' · $commit';
      final label = job.hasUncommittedChanges
          ? context.l10n.codeWsDeliveryCompletedWithUnsaved
          : job.hasPendingCommits
          ? context.l10n.codeWsDeliveryCompletedWithPending
          : context.l10n.codeWsDeliveryCompleted;
      return '$label$suffix';
    }
    return _stageLabel(context, job.stage);
  }

  String _progressMeta(BuildContext context, CodeDeliveryJob job) {
    final parts = <String>['${job.progress}%'];
    if (job.queuePosition > 0) {
      parts.add(
        context.l10n.codeWsDeliveryQueuePosition(job.queuePosition),
      );
    }
    if (job.attempt > 0) {
      parts.add(
        context.l10n.codeWsDeliveryAttempt(job.attempt),
      );
    }
    return parts.join(' · ');
  }

  Color _accent(CodeDeliveryJob? job) {
    if (_hasPendingWork(job)) return Colors.amberAccent;
    if (job?.isCompleted == true) return Colors.greenAccent;
    if (job?.status == 'failed') return Colors.redAccent;
    if (job?.status == 'conflict' || job?.status == 'partial') {
      return Colors.orangeAccent;
    }
    return Colors.lightBlueAccent;
  }
}

class _StatusBadge extends StatelessWidget {
  const _StatusBadge({required this.job, required this.color});

  final CodeDeliveryJob job;
  final Color color;

  @override
  Widget build(BuildContext context) {
    final text = _hasPendingWork(job)
        ? context.l10n.codeWsDeliveryPending
        : switch (job.status) {
            'completed' => context.l10n.codeWsDeliveryCompleted,
            'failed' => context.l10n.codeWsDeliveryFailed,
            'conflict' || 'partial' => context.l10n.codeWsDeliveryConflict,
            'queued' => context.l10n.codeWsDeliveryQueued,
            _ => context.l10n.codeWsDeliveryRunning,
          };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 5),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.14),
        borderRadius: BorderRadius.circular(99),
      ),
      child: Text(
        text,
        style: TextStyle(color: color, fontSize: 11),
      ),
    );
  }
}

bool _hasPendingWork(CodeDeliveryJob? job) {
  if (job == null || !job.isCompleted) return false;
  return job.hasPendingChanges ||
      job.hasPendingCommits ||
      job.hasUncommittedChanges;
}

String _stageLabel(BuildContext context, String stage) {
  final l10n = context.l10n;
  return switch (stage) {
    'queued' => l10n.codeWsDeliveryQueued,
    'stopping_terminal' => l10n.codeWsDeliveryStageStoppingTerminal,
    'syncing' => l10n.codeWsDeliveryStageSyncing,
    'merging' => l10n.codeWsDeliveryStageMerging,
    'quality_check' => l10n.codeWsDeliveryStageQualityCheck,
    'pushing' => l10n.codeWsDeliveryStagePushing,
    'verifying' => l10n.codeWsDeliveryStageVerifying,
    'cleaning' => l10n.codeWsDeliveryStageCleaning,
    _ => l10n.codeWsDeliveryRunning,
  };
}
