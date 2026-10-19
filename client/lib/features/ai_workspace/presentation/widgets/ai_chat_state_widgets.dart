import 'package:flutter/material.dart';

import '../code_workspace_text.dart';

class AiChatErrorBanner extends StatelessWidget {
  const AiChatErrorBanner({
    super.key,
    required this.message,
    required this.onRetry,
    required this.onDismiss,
  });

  final String message;
  final VoidCallback onRetry;
  final VoidCallback onDismiss;

  @override
  Widget build(BuildContext context) {
    return MaterialBanner(
      backgroundColor: const Color(0xFF451A1A),
      content: Text(message, style: const TextStyle(color: Colors.white)),
      leading: const Icon(Icons.error_outline_rounded, color: Colors.redAccent),
      actions: [
        TextButton(
          onPressed: onDismiss,
          child: Text(CodeWorkspaceText.t(context, 'action.dismiss')),
        ),
        TextButton(
          onPressed: onRetry,
          child: Text(CodeWorkspaceText.t(context, 'action.retry')),
        ),
      ],
    );
  }
}

/// 实时对话流断开时的提示：数据仍会在重连后补齐，用户知道此刻看到的不是实时输出。
class AiChatReconnectingBanner extends StatelessWidget {
  const AiChatReconnectingBanner({super.key});

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      color: const Color(0xFF3F2D0C),
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
      child: Row(
        children: [
          const Icon(Icons.wifi_off_rounded, size: 16, color: Colors.amber),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              CodeWorkspaceText.t(context, 'chat.streamReconnecting'),
              style: const TextStyle(color: Colors.amber, fontSize: 12),
            ),
          ),
        ],
      ),
    );
  }
}

class AiChatEmptyWorkspace extends StatelessWidget {
  const AiChatEmptyWorkspace({super.key, required this.onOpenSessions});

  final VoidCallback onOpenSessions;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.terminal_rounded, color: Colors.white54, size: 52),
            const SizedBox(height: 16),
            Text(
              CodeWorkspaceText.t(context, 'chat.emptyTitle'),
              style: const TextStyle(
                color: Colors.white,
                fontSize: 18,
                fontWeight: FontWeight.w700,
              ),
            ),
            const SizedBox(height: 8),
            Text(
              CodeWorkspaceText.t(context, 'chat.emptyHint'),
              textAlign: TextAlign.center,
              style: const TextStyle(color: Colors.white60, height: 1.5),
            ),
            const SizedBox(height: 20),
            FilledButton.icon(
              onPressed: onOpenSessions,
              icon: const Icon(Icons.add_rounded),
              label: Text(CodeWorkspaceText.t(context, 'chat.chooseSession')),
            ),
          ],
        ),
      ),
    );
  }
}

class AiChatExecutionIndicator extends StatelessWidget {
  const AiChatExecutionIndicator({
    super.key,
    required this.isStopping,
    required this.onStop,
    this.isStreaming = false,
    this.activityKind = '',
    this.activity = '',
  });

  final bool isStopping;
  final VoidCallback onStop;
  final bool isStreaming;
  final String activityKind;
  final String activity;

  // 桌面端显示「正在执行 · go test ./...」，移动端原先只显示裸命令——
  // 没有动词，用户分不清那串字是在跑、在改还是在等审批。
  static const _knownActivityKinds = {
    'command',
    'file',
    'read',
    'tool',
    'search',
    'thinking',
    'approval_waiting',
    'approval_approved',
    'approval_declined',
  };

  String _statusText(BuildContext context) {
    final label = _knownActivityKinds.contains(activityKind)
        ? CodeWorkspaceText.t(context, 'chat.activity_$activityKind')
        : '';
    if (label.isNotEmpty && activity.isNotEmpty) return '$label · $activity';
    if (label.isNotEmpty) return label;
    if (activity.isNotEmpty) return activity;
    return CodeWorkspaceText.t(
      context,
      isStreaming ? 'chat.streaming' : 'chat.executing',
    );
  }

  @override
  Widget build(BuildContext context) {
    final waitingApproval = activityKind == 'approval_waiting';
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 8, 8),
      child: Row(
        children: [
          waitingApproval
              ? const Icon(
                  Icons.gpp_maybe_outlined,
                  color: Colors.orangeAccent,
                  size: 18,
                )
              : const SizedBox.square(
                  dimension: 16,
                  child: CircularProgressIndicator(
                    color: Colors.greenAccent,
                    strokeWidth: 2,
                  ),
                ),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              _statusText(context),
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                color: waitingApproval
                    ? Colors.orangeAccent
                    : Colors.greenAccent,
              ),
            ),
          ),
          TextButton.icon(
            onPressed: isStopping ? null : onStop,
            icon: isStopping
                ? const SizedBox.square(
                    dimension: 14,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Icon(Icons.stop_circle_outlined, size: 18),
            label: Text(CodeWorkspaceText.t(context, 'action.stop')),
          ),
        ],
      ),
    );
  }
}
