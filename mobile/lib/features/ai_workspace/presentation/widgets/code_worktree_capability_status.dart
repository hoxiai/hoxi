import 'package:flutter/material.dart';

import '../../../../core/theme/app_theme.dart';
import '../../models/ai_dev_session.dart';
import '../../../../core/l10n/l10n.dart';

class CodeWorktreeCapabilityStatus extends StatelessWidget {
  const CodeWorktreeCapabilityStatus({
    super.key,
    required this.capability,
    required this.loading,
    required this.error,
    required this.onRetry,
  });

  final CodeWorktreeCapability? capability;
  final bool loading;
  final String? error;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    if (loading) {
      return _StatusPanel(
        icon: Icons.sync_rounded,
        color: AppTheme.primaryBlue,
        title: context.l10n.codeWsSessionWorktreeChecking,
        child: const LinearProgressIndicator(minHeight: 2),
      );
    }
    if (error != null) {
      return _StatusPanel(
        icon: Icons.error_outline_rounded,
        color: AppTheme.error,
        title: context.l10n.codeWsSessionWorktreeCheckFailed,
        child: Align(
          alignment: Alignment.centerLeft,
          child: TextButton.icon(
            onPressed: onRetry,
            icon: const Icon(Icons.refresh_rounded, size: 17),
            label: Text(context.l10n.codeWsSessionWorktreeRetry),
          ),
        ),
      );
    }
    final value = capability;
    if (value == null) return const SizedBox.shrink();
    if (value.dirtyRepositories.isNotEmpty) {
      return _StatusPanel(
        icon: Icons.warning_amber_rounded,
        color: AppTheme.warning,
        title: context.l10n.codeWsSessionWorktreeDirty,
        child: Text(
          context.l10n.codeWsSessionWorktreeDirtyHint(value.dirtyRepositories.join(', ')),
        ),
      );
    }
    if (!value.available) {
      final reason = switch (value.reason) {
        'not_git' => context.l10n.codeWsSessionWorktreeReasonNotGit,
        'source_unavailable' =>
          context.l10n.codeWsSessionWorktreeReasonSourceUnavailable,
        _ => context.l10n.codeWsSessionWorktreeReasonUnknown,
      };
      return _StatusPanel(
        icon: Icons.block_rounded,
        color: AppTheme.error,
        title: context.l10n.codeWsSessionWorktreeUnavailable,
        child: Text(reason),
      );
    }
    return _StatusPanel(
      icon: Icons.account_tree_outlined,
      color: AppTheme.success,
      title: context.l10n.codeWsSessionWorktreeReady,
      child: Text(context.l10n.codeWsSessionWorktreeReadyHint),
    );
  }
}

class _StatusPanel extends StatelessWidget {
  const _StatusPanel({
    required this.icon,
    required this.color,
    required this.title,
    required this.child,
  });

  final IconData icon;
  final Color color;
  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(16),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(icon, size: 18, color: color),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  title,
                  style: TextStyle(color: color, fontWeight: FontWeight.w700),
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          DefaultTextStyle.merge(
            style:  TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 12,
              height: 1.45,
            ),
            child: child,
          ),
        ],
      ),
    );
  }
}
