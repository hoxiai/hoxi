part of 'ai_workspace_controller.dart';

/// Delivery state and polling remain in the workspace state lifecycle.
mixin _AiWorkspaceDelivery on Notifier<AiWorkspaceState> {
  static const _deliveryPollInterval = Duration(seconds: 3);
  Timer? _deliveryPollTimer;
  AiWorkspaceRepository get _repo;
  Future<void> refreshCurrentSession({bool showLoading = false});

  Future<CodeDeliveryReview?> loadDeliveryReview() async {
    final nodeRequest = NodeRequestGuard(_repo);
    final sessionId = state.currentSession?.id;
    if (sessionId == null || state.isDeliveryLoading) return null;
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) {
      return null;
    }
    state = state.copyWith(isDeliveryLoading: true, clearDeliveryError: true);
    try {
      final review = await nodeRequest.repository.getDeliveryReview(sessionId);
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) {
        return null;
      }
      state = state.copyWith(isDeliveryLoading: false);
      return review;
    } catch (error) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) {
        return null;
      }
      state = state.copyWith(
        isDeliveryLoading: false,
        deliveryError: error.toString(),
      );
      return null;
    }
  }

  Future<bool> startDelivery({required String reviewRevision}) async {
    final nodeRequest = NodeRequestGuard(_repo);
    final sessionId = state.currentSession?.id;
    if (sessionId == null || state.isDeliveryLoading) return false;
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) {
      return false;
    }
    state = state.copyWith(
      isDeliveryLoading: true,
      clearDeliveryError: true,
      clearError: true,
    );
    try {
      final delivery = await nodeRequest.repository.startDelivery(
        sessionId,
        reviewRevision: reviewRevision,
      );
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) {
        return false;
      }
      state = state.copyWith(delivery: delivery, isDeliveryLoading: false);
      await refreshCurrentSession();
      return true;
    } catch (error) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) {
        return false;
      }
      state = state.copyWith(
        isDeliveryLoading: false,
        deliveryError: error.toString(),
      );
      return false;
    }
  }

  /// 交付进行中时定时只拉交付进度；此前这个定时器从未启动，开始交付后进度一直停在排队/进行中，
  /// 交付预览卡片也等不到交付完成。交付一结束就整体刷新一次会话，让会话状态、时间线和预览跟上。
  void _syncDeliveryPolling() {
    final sessionId = state.currentSession?.id;
    final inFlight =
        state.delivery?.isActive == true ||
        state.currentSession?.status == 'delivering';
    if (sessionId == null || !inFlight) {
      _stopDeliveryPolling();
      return;
    }
    if (_deliveryPollTimer?.isActive == true) return;
    _deliveryPollTimer = Timer(_deliveryPollInterval, () {
      _deliveryPollTimer = null;
      unawaited(_pollDelivery(sessionId));
    });
  }

  void _stopDeliveryPolling() {
    _deliveryPollTimer?.cancel();
    _deliveryPollTimer = null;
  }

  Future<void> _pollDelivery(int sessionId) async {
    final nodeRequest = NodeRequestGuard(_repo);
    if (state.currentSession?.id != sessionId) return;
    final (delivery, error) = await _loadDelivery(sessionId);
    if (state.currentSession?.id != sessionId) return;
    // 拉取失败（网络抖动）保留当前进度，下一轮再试
    if (error == null) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) {
        return;
      }
      state = state.copyWith(
        delivery: delivery,
        clearDelivery: delivery == null,
        clearDeliveryError: true,
      );
      if (delivery?.isActive != true) await refreshCurrentSession();
    }
    _syncDeliveryPolling();
  }

  Future<(CodeDeliveryJob?, String?)> _loadDelivery(int sessionId) async {
    final nodeRequest = NodeRequestGuard(_repo);
    try {
      return (await nodeRequest.repository.getDelivery(sessionId), null);
    } catch (error) {
      return (null, error.toString());
    }
  }
}
