import 'package:flutter/material.dart';

import '../../../../core/theme/app_theme.dart';
import '../../../../shared/widgets/panel/panel_card.dart';
import '../../../../shared/models/task_attention.dart';
import '../../../../core/l10n/l10n.dart';

class CodeAttentionCard extends StatelessWidget {
  const CodeAttentionCard({
    super.key,
    required this.isLoading,
    required this.errorMessage,
    required this.items,
    required this.onOpen,
    required this.onRetry,
    this.actionKey,
    this.onExecuteAction,
    this.onOpenSession,
  });

  final bool isLoading;
  final String? errorMessage;
  final List<TaskAttention> items;
  final VoidCallback onOpen;
  final VoidCallback onRetry;
  final String? actionKey;
  final Future<bool> Function(
    TaskAttention attention,
    TaskAttentionAction action,
  )?
  onExecuteAction;
  final Future<void> Function(int sessionId)? onOpenSession;

  @override
  Widget build(BuildContext context) {
    if (!isLoading && errorMessage == null && items.isEmpty) {
      return const SizedBox.shrink();
    }
    return PanelCard(
      title: Row(
        children: [
          const Icon(
            Icons.notification_important_outlined,
            size: 21,
            color: AppTheme.error,
          ),
          const SizedBox(width: 8),
          Text(context.l10n.codeWsAttentionTitle),
          if (items.isNotEmpty) ...[
            const SizedBox(width: 8),
            Badge.count(count: items.length, backgroundColor: AppTheme.error),
          ],
        ],
      ),
      trailing: TextButton(
        onPressed: isLoading ? null : onOpen,
        child: Text(context.l10n.codeWsAttentionViewAll),
      ),
      child: _buildBody(context),
    );
  }

  Widget _buildBody(BuildContext context) {
    if (isLoading && items.isEmpty) {
      return const Center(child: LinearProgressIndicator(minHeight: 2));
    }
    if (errorMessage != null && items.isEmpty) {
      return Row(
        children: [
           Icon(Icons.cloud_off_outlined, color: AppTheme.textLight),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              context.l10n.codeWsAttentionLoadFailed,
              style:  TextStyle(color: AppTheme.textSecondary),
            ),
          ),
          TextButton(
            onPressed: onRetry,
            child: Text(context.l10n.codeWsActionRetry),
          ),
        ],
      );
    }
    if (items.isEmpty) {
      return Row(
        children: [
          const Icon(Icons.check_circle_outline, color: AppTheme.success),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              context.l10n.codeWsAttentionEmpty,
              style:  TextStyle(color: AppTheme.textSecondary),
            ),
          ),
        ],
      );
    }

    final displayedItems = items.take(3).toList();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        for (var index = 0; index < displayedItems.length; index++) ...[
          _buildItemRow(context, displayedItems[index]),
          if (index < displayedItems.length - 1)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 12),
              child: Divider(
                height: 1,
                color: AppTheme.border.withValues(alpha: 0.6),
              ),
            ),
        ],
      ],
    );
  }

  Widget _buildItemRow(BuildContext context, TaskAttention item) {
    final visibleActions =
        (item.actions.length > 1 && onOpenSession != null)
        ? item.actions.where((a) => a.type != 'open_session').toList()
        : item.actions;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        InkWell(
          key: ValueKey('code_attention_card_item_${item.id}'),
          borderRadius: BorderRadius.circular(10),
          onTap: item.sessionId > 0 && onOpenSession != null
              ? () => onOpenSession!(item.sessionId)
              : onOpen,
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 2),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: Text(
                        item.title,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(fontWeight: FontWeight.w800),
                      ),
                    ),
                    if (item.sessionId > 0) ...[
                      const SizedBox(width: 8),
                      Text(
                        '#${item.sessionId}',
                        style: TextStyle(
                          color: AppTheme.textLight,
                          fontSize: 12,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                      const SizedBox(width: 2),
                      Icon(
                        Icons.chevron_right_rounded,
                        size: 16,
                        color: AppTheme.textLight,
                      ),
                    ],
                  ],
                ),
                if (item.summary.isNotEmpty) ...[
                  const SizedBox(height: 4),
                  Text(
                    item.summary,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      color: AppTheme.textSecondary,
                      height: 1.4,
                    ),
                  ),
                ],
              ],
            ),
          ),
        ),
        if (visibleActions.isNotEmpty &&
            (onExecuteAction != null || onOpenSession != null)) ...[
          const SizedBox(height: 10),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: visibleActions.map((action) {
              final busy = actionKey == '${item.id}:${action.type}';
              final isApprove = action.type == 'approve';
              final isReject = action.type == 'reject';
              final btnKey = ValueKey(
                'code_attention_card_action_${item.id}_${action.type}',
              );

              if (isApprove) {
                return SizedBox(
                  height: 32,
                  child: FilledButton.icon(
                    key: btnKey,
                    style: FilledButton.styleFrom(
                      visualDensity: VisualDensity.compact,
                      backgroundColor: AppTheme.primaryBlue,
                      padding: const EdgeInsets.symmetric(horizontal: 12),
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(9),
                      ),
                      textStyle: const TextStyle(
                        fontSize: 12.5,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    onPressed: actionKey == null
                        ? () => _handleAction(context, item, action)
                        : null,
                    icon: busy
                        ? const SizedBox.square(
                            dimension: 13,
                            child: CircularProgressIndicator(
                              strokeWidth: 2,
                              color: Colors.white,
                            ),
                          )
                        : const Icon(Icons.check_rounded, size: 16),
                    label: Text(action.label),
                  ),
                );
              }

              return SizedBox(
                height: 32,
                child: OutlinedButton.icon(
                  key: btnKey,
                  style: OutlinedButton.styleFrom(
                    visualDensity: VisualDensity.compact,
                    foregroundColor: isReject
                        ? AppTheme.error
                        : AppTheme.textPrimary,
                    side: BorderSide(
                      color: isReject
                          ? AppTheme.error.withValues(alpha: 0.45)
                          : AppTheme.border,
                    ),
                    padding: const EdgeInsets.symmetric(horizontal: 12),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(9),
                    ),
                    textStyle: const TextStyle(
                      fontSize: 12.5,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                  onPressed: actionKey == null
                      ? () => _handleAction(context, item, action)
                      : null,
                  icon: busy
                      ? SizedBox.square(
                          dimension: 13,
                          child: CircularProgressIndicator(
                            strokeWidth: 2,
                            color: isReject ? AppTheme.error : null,
                          ),
                        )
                      : Icon(_actionIcon(action.type), size: 16),
                  label: Text(action.label),
                ),
              );
            }).toList(),
          ),
        ],
      ],
    );
  }

  Future<void> _handleAction(
    BuildContext context,
    TaskAttention attention,
    TaskAttentionAction action,
  ) async {
    if (action.type == 'open_session') {
      if (onOpenSession != null && attention.sessionId > 0) {
        await onOpenSession!(attention.sessionId);
      } else {
        onOpen();
      }
      return;
    }
    if (onExecuteAction == null) {
      onOpen();
      return;
    }
    if (action.requiresConfirmation) {
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (dialogContext) => AlertDialog(
          title: Text(action.label),
          content: Text(dialogContext.l10n.codeWsAttentionConfirmHint),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(dialogContext).pop(false),
              child: Text(dialogContext.l10n.codeWsAttentionCancel),
            ),
            FilledButton(
              onPressed: () => Navigator.of(dialogContext).pop(true),
              child: Text(dialogContext.l10n.codeWsAttentionConfirm),
            ),
          ],
        ),
      );
      if (confirmed != true || !context.mounted) return;
    }
    final success = await onExecuteAction!(attention, action);
    if (!context.mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(
          success
              ? context.l10n.codeWsAttentionActionSuccess
              : context.l10n.codeWsAttentionActionFailed,
        ),
      ),
    );
  }

  IconData _actionIcon(String type) => switch (type) {
    'approve' => Icons.check_rounded,
    'reject' => Icons.close_rounded,
    'retry_initialization' => Icons.refresh_rounded,
    _ => Icons.open_in_new_rounded,
  };
}
