import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../models/ai_dev_session.dart';
import '../../models/ai_session_state_info.dart';

typedef AiWorkspaceSessionSyncHandler =
    void Function(
      AiDevSession session, {
      String? currentStage,
      AiSessionStateInfo? stateInfo,
    });

typedef AiWorkspaceTaskRefreshHandler =
    Future<void> Function({bool recentOnly});

final aiWorkspaceTaskSyncBusProvider = Provider<AiWorkspaceTaskSyncBus>((ref) {
  final bus = AiWorkspaceTaskSyncBus();
  ref.onDispose(bus.dispose);
  return bus;
});

/// `ai_workspace` 向外部任务聚合层（如 `task_center`）单向广播会话实时状态与刷新信号的总线。
///
/// 按 `ProviderContainer` 隔离实例，`ai_workspace` 只触发广播而不反向依赖 `task_center`，
/// `task_center` 在 `build()` 时注册监听并在销毁时解绑，从而保持 `task_center -> ai_workspace` 严格单向。
class AiWorkspaceTaskSyncBus {
  AiWorkspaceSessionSyncHandler? _onSyncSession;
  AiWorkspaceTaskRefreshHandler? _onRefreshSilently;

  void bind({
    required AiWorkspaceSessionSyncHandler onSyncSession,
    required AiWorkspaceTaskRefreshHandler onRefreshSilently,
  }) {
    _onSyncSession = onSyncSession;
    _onRefreshSilently = onRefreshSilently;
  }

  void unbind({
    required AiWorkspaceSessionSyncHandler onSyncSession,
    required AiWorkspaceTaskRefreshHandler onRefreshSilently,
  }) {
    if (_onSyncSession == onSyncSession) {
      _onSyncSession = null;
    }
    if (_onRefreshSilently == onRefreshSilently) {
      _onRefreshSilently = null;
    }
  }

  void notifySession(
    AiDevSession session, {
    String? currentStage,
    AiSessionStateInfo? stateInfo,
  }) {
    _onSyncSession?.call(
      session,
      currentStage: currentStage,
      stateInfo: stateInfo,
    );
  }

  Future<void> refreshSilently({bool recentOnly = true}) async {
    final fn = _onRefreshSilently;
    if (fn != null) {
      await fn(recentOnly: recentOnly);
    }
  }

  Future<void> refreshRecentSilently() => refreshSilently(recentOnly: true);

  void dispose() {
    _onSyncSession = null;
    _onRefreshSilently = null;
  }
}
