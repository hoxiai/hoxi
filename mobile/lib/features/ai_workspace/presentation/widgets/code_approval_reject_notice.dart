import 'package:flutter/material.dart';

import '../../../../core/theme/app_theme.dart';
import '../../models/ai_dev_session.dart';
import '../../../../core/l10n/l10n.dart';

/// 选了手动确认/安全自动、但执行器在对话模式下无法转人工确认时，如实说明操作会被直接拒绝。
class CodeApprovalRejectNotice extends StatelessWidget {
  const CodeApprovalRejectNotice({
    super.key,
    required this.executor,
    required this.policy,
  });

  final CodeExecutor? executor;
  final String? policy;

  @override
  Widget build(BuildContext context) {
    final current = executor;
    if (current == null || !current.rejectsApprovalInConversation(policy)) {
      return const SizedBox.shrink();
    }
    return Padding(
      padding: const EdgeInsets.only(top: 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(
            Icons.info_outline_rounded,
            size: 16,
            color: AppTheme.warning,
          ),
          const SizedBox(width: 6),
          Expanded(
            child: Text(
              context.l10n.codeWsSessionSettingsPolicyRejectsInConversation(current.name),
              style: Theme.of(
                context,
              ).textTheme.bodySmall?.copyWith(color: AppTheme.warning),
            ),
          ),
        ],
      ),
    );
  }
}
