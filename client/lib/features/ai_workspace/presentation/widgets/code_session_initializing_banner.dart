import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../code_workspace_text.dart';
import '../controllers/ai_workspace_controller.dart';

/// 隔离会话创建后要先同步远端并创建 Worktree，期间发指令会被服务端拒绝。
/// 初始化中显示提示并定时刷新会话，完成后自动消失。
class CodeSessionInitializingBanner extends ConsumerStatefulWidget {
  const CodeSessionInitializingBanner({super.key, required this.status});

  final String status;

  @override
  ConsumerState<CodeSessionInitializingBanner> createState() =>
      _CodeSessionInitializingBannerState();
}

class _CodeSessionInitializingBannerState
    extends ConsumerState<CodeSessionInitializingBanner> {
  Timer? _timer;

  bool get _initializing => widget.status == 'initializing';

  @override
  void initState() {
    super.initState();
    _sync();
  }

  @override
  void didUpdateWidget(covariant CodeSessionInitializingBanner oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.status != widget.status) _sync();
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  void _sync() {
    _timer?.cancel();
    _timer = null;
    if (!_initializing) return;
    _timer = Timer.periodic(const Duration(seconds: 3), (_) {
      ref.read(aiWorkspaceControllerProvider.notifier).refreshCurrentSession();
    });
  }

  @override
  Widget build(BuildContext context) {
    if (!_initializing) return const SizedBox.shrink();
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.fromLTRB(16, 0, 16, 12),
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: const Color(0xFF111827),
        borderRadius: BorderRadius.circular(14),
        border: Border.all(
          color: Colors.lightBlueAccent.withValues(alpha: 0.24),
        ),
      ),
      child: Row(
        children: [
          const SizedBox.square(
            dimension: 18,
            child: CircularProgressIndicator(strokeWidth: 2),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  CodeWorkspaceText.t(context, 'recovery.initializing'),
                  style: const TextStyle(
                    color: Colors.white,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                const SizedBox(height: 4),
                Text(
                  CodeWorkspaceText.t(context, 'recovery.initializingHint'),
                  style: const TextStyle(color: Colors.white70, height: 1.45),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
