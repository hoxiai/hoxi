import 'package:flutter/material.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/theme/app_theme.dart';
import '../../../../shared/widgets/chat_image_preview.dart';
import '../../../../shared/widgets/panel/panel_card.dart';
import '../../models/code_session_recovery.dart';
import 'ai_chat_markdown.dart';

class CodeInitializationCard extends StatelessWidget {
  const CodeInitializationCard({
    super.key,
    required this.initialization,
    required this.isRetrying,
    required this.onRetry,
    required this.isAborting,
    required this.onAbort,
  });

  final CodeSessionInitialization initialization;
  final bool isRetrying;
  final VoidCallback onRetry;
  final bool isAborting;
  final VoidCallback onAbort;

  @override
  Widget build(BuildContext context) {
    final failed = initialization.isFailed;
    final initializing = initialization.isInitializing;
    final color = failed
        ? AppTheme.error
        : initializing
        ? Colors.orange
        : AppTheme.success;
    return PanelCard(
      title: Text(context.l10n.codeWsRecoveryInitialization),
      trailing: _StatusBadge(
        label: failed
            ? context.l10n.codeWsRecoveryInitializationFailed
            : initializing
            ? context.l10n.codeWsRecoveryInitializing
            : context.l10n.codeWsRecoveryInitialized,
        color: color,
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            initialization.errorMessage.isNotEmpty
                ? initialization.errorMessage
                : failed
                ? context.l10n.codeWsRecoveryInitializationFailedHint
                : initializing
                ? context.l10n.codeWsRecoveryInitializingHint
                : context.l10n.codeWsRecoveryInitializedHint,
            style:  TextStyle(color: AppTheme.textSecondary, height: 1.45),
          ),
          if (initialization.canRetry) ...[
            const SizedBox(height: 14),
            FilledButton.icon(
              onPressed: isRetrying ? null : onRetry,
              icon: isRetrying
                  ? const SizedBox.square(
                      dimension: 16,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.refresh_rounded),
              label: Text(
                context.l10n.codeWsRecoveryRetryInitialization,
              ),
            ),
          ],
          if (initializing) ...[
            const SizedBox(height: 10),
            OutlinedButton.icon(
              onPressed: isAborting ? null : onAbort,
              icon: isAborting
                  ? const SizedBox.square(
                      dimension: 16,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.cancel_outlined),
              label: Text(
                context.l10n.codeWsRecoveryAbortInitialization,
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class CodeHistoryMessagesCard extends StatelessWidget {
  const CodeHistoryMessagesCard({super.key, required this.messages});

  final List<CodeHistoryMessage> messages;

  @override
  Widget build(BuildContext context) {
    return PanelCard(
      title: Text(context.l10n.codeWsRecoveryConversation),
      trailing: Text('${messages.length}'),
      child: messages.isEmpty
          ? Text(
              context.l10n.codeWsRecoveryNoMessages,
              style: TextStyle(color: AppTheme.textSecondary),
            )
          : Column(
              children: [
                for (var index = 0; index < messages.length; index++) ...[
                  _HistoryMessage(message: messages[index]),
                  if (index != messages.length - 1) const SizedBox(height: 14),
                ],
              ],
            ),
    );
  }
}

class CodeExecutionRunsCard extends StatelessWidget {
  const CodeExecutionRunsCard({
    super.key,
    required this.runs,
    required this.total,
    required this.isLoadingMore,
    required this.retryingInstructionId,
    required this.retriedInstructionIds,
    required this.onRetry,
    required this.onOpenDetail,
    required this.onLoadMore,
  });

  final List<CodeExecutionRun> runs;
  final int total;
  final bool isLoadingMore;
  final int? retryingInstructionId;
  final Set<int> retriedInstructionIds;
  final ValueChanged<CodeExecutionRun> onRetry;
  final ValueChanged<CodeExecutionRun> onOpenDetail;
  final VoidCallback onLoadMore;

  @override
  Widget build(BuildContext context) {
    return PanelCard(
      title: Text(context.l10n.codeWsRecoveryRuns),
      trailing: Text('${runs.length}/$total'),
      child: runs.isEmpty
          ? Text(
              context.l10n.codeWsRecoveryNoRuns,
              style:  TextStyle(color: AppTheme.textSecondary),
            )
          : Column(
              children: [
                for (var index = 0; index < runs.length; index++) ...[
                  _ExecutionRunItem(
                    run: runs[index],
                    isRetrying:
                        retryingInstructionId == runs[index].instructionId,
                    wasRetried: retriedInstructionIds.contains(
                      runs[index].instructionId,
                    ),
                    onRetry: () => onRetry(runs[index]),
                    onOpenDetail: () => onOpenDetail(runs[index]),
                  ),
                  if (index != runs.length - 1) const SizedBox(height: 24),
                ],
                if (runs.length < total) ...[
                  const SizedBox(height: 14),
                  OutlinedButton.icon(
                    onPressed: isLoadingMore ? null : onLoadMore,
                    icon: isLoadingMore
                        ? const SizedBox.square(
                            dimension: 16,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(Icons.expand_more_rounded),
                    label: Text(
                      context.l10n.codeWsRecoveryLoadMore,
                    ),
                  ),
                ],
              ],
            ),
    );
  }
}

class _HistoryMessage extends StatelessWidget {
  const _HistoryMessage({required this.message});

  final CodeHistoryMessage message;

  @override
  Widget build(BuildContext context) {
    final user = message.role.toLowerCase() == 'user';
    return Column(
      crossAxisAlignment:
          user ? CrossAxisAlignment.end : CrossAxisAlignment.start,
      children: [
        Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              user ? Icons.person_outline : Icons.smart_toy_outlined,
              size: 15,
              color: user ? AppTheme.primaryBlue : AppTheme.success,
            ),
            const SizedBox(width: 5),
            Text(
              user
                  ? context.l10n.codeWsRecoveryUser
                  : context.l10n.codeWsRecoveryAgent,
              style: TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 12,
                fontWeight: FontWeight.w700,
              ),
            ),
            if (message.createdAt != null) ...[
              const SizedBox(width: 8),
              Text(
                _formatTime(message.createdAt),
                style: TextStyle(color: AppTheme.textLight, fontSize: 11),
              ),
            ],
          ],
        ),
        const SizedBox(height: 6),
        Align(
          alignment: user ? Alignment.centerRight : Alignment.centerLeft,
          child: Container(
            constraints: user ? const BoxConstraints(maxWidth: 300) : null,
            width: user ? null : double.infinity,
            padding: EdgeInsets.symmetric(
              horizontal: 14,
              vertical: user ? 10 : 12,
            ),
            decoration: BoxDecoration(
              color: user ? AppTheme.primaryBlue : AppTheme.surfaceMuted,
              borderRadius: BorderRadius.only(
                topLeft: const Radius.circular(16),
                topRight: const Radius.circular(16),
                bottomLeft: Radius.circular(user ? 16 : 6),
                bottomRight: Radius.circular(user ? 6 : 16),
              ),
            ),
            child: user
                ? Column(
                    crossAxisAlignment: CrossAxisAlignment.end,
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      if (message.imageDataUrl != null &&
                          message.imageDataUrl!.isNotEmpty)
                        ChatTappableImage(
                          imageDataUrl: message.imageDataUrl!,
                          width: 220,
                        ),
                      if (message.imageDataUrl != null &&
                          message.imageDataUrl!.isNotEmpty &&
                          message.content.trim().isNotEmpty)
                        const SizedBox(height: 8),
                      if (message.content.trim().isNotEmpty)
                        SelectableText(
                          message.content,
                          style: const TextStyle(
                            color: Colors.white,
                            fontSize: 14,
                            height: 1.5,
                          ),
                        ),
                    ],
                  )
                : AiChatMarkdown(text: message.content),
          ),
        ),
      ],
    );
  }
}

class _ExecutionRunItem extends StatelessWidget {
  const _ExecutionRunItem({
    required this.run,
    required this.isRetrying,
    required this.wasRetried,
    required this.onRetry,
    required this.onOpenDetail,
  });

  final CodeExecutionRun run;
  final bool isRetrying;
  final bool wasRetried;
  final VoidCallback onRetry;
  final VoidCallback onOpenDetail;

  @override
  Widget build(BuildContext context) {
    final color = _runColor(run.status);
    final summary = run.errorMessage.isNotEmpty
        ? run.errorMessage
        : run.output.isNotEmpty
        ? run.output
        : run.prompt;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            _StatusBadge(label: _runLabel(context, run.status), color: color),
            const Spacer(),
            Text(
              _formatTime(run.createdAt),
              style:  TextStyle(color: AppTheme.textLight, fontSize: 11),
            ),
          ],
        ),
        if (run.prompt.isNotEmpty) ...[
          const SizedBox(height: 8),
          Text(
            run.prompt,
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(fontWeight: FontWeight.w700),
          ),
        ],
        if (summary.isNotEmpty && summary != run.prompt) ...[
          const SizedBox(height: 6),
          Text(
            summary,
            maxLines: 4,
            overflow: TextOverflow.ellipsis,
            style: TextStyle(
              color: run.status == 'failed'
                  ? AppTheme.error
                  : AppTheme.textSecondary,
              height: 1.4,
            ),
          ),
        ],
        const SizedBox(height: 8),
        Wrap(
          spacing: 10,
          runSpacing: 6,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            Text(
              context.l10n.codeWsRecoveryDuration(run.durationMs),
              style:  TextStyle(color: AppTheme.textLight, fontSize: 11),
            ),
            if (run.totalTokens > 0)
              Text(
                context.l10n.codeWsRecoveryTokens(run.totalTokens),
                style:  TextStyle(color: AppTheme.textLight, fontSize: 11),
              ),
            TextButton.icon(
              onPressed: onOpenDetail,
              icon: const Icon(Icons.article_outlined, size: 17),
              label: Text(
                context.l10n.codeWsRecoveryViewRunDetail,
              ),
            ),
            if (run.canRetry && !wasRetried)
              TextButton.icon(
                onPressed: isRetrying ? null : onRetry,
                icon: isRetrying
                    ? const SizedBox.square(
                        dimension: 14,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.replay_rounded, size: 17),
                label: Text(
                  context.l10n.codeWsRecoveryRetryInstruction,
                ),
              ),
            if (wasRetried)
              Text(
                context.l10n.codeWsRecoveryRetryQueued,
                style: const TextStyle(
                  color: AppTheme.success,
                  fontSize: 11,
                  fontWeight: FontWeight.w700,
                ),
              ),
          ],
        ),
      ],
    );
  }
}

class _StatusBadge extends StatelessWidget {
  const _StatusBadge({required this.label, required this.color});

  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 11,
          fontWeight: FontWeight.w800,
        ),
      ),
    );
  }
}

Color _runColor(String status) => switch (status) {
  'completed' => AppTheme.success,
  'failed' => AppTheme.error,
  'cancelled' || 'stopped' => Colors.orange,
  'running' => AppTheme.primaryBlue,
  'awaiting_approval' || 'pending_approval' => Colors.amber,
  _ => AppTheme.textSecondary,
};

String _runLabel(BuildContext context, String status) {
  final l10n = context.l10n;
  return switch (status) {
    'completed' => l10n.codeWsRecoveryStatusCompleted,
    'failed' => l10n.codeWsRecoveryStatusFailed,
    'cancelled' || 'stopped' => l10n.codeWsRecoveryStatusStopped,
    'running' => l10n.codeWsRecoveryStatusRunning,
    'awaiting_approval' ||
    'pending_approval' => l10n.codeWsRecoveryStatusAwaitingApproval,
    'queued' => l10n.codeWsRecoveryStatusQueued,
    _ => l10n.codeWsRecoveryStatusUnknown,
  };
}

String _formatTime(DateTime? value) {
  if (value == null) return '-';
  final local = value.toLocal();
  String two(int number) => number.toString().padLeft(2, '0');
  return '${local.month}/${local.day} ${two(local.hour)}:${two(local.minute)}';
}
