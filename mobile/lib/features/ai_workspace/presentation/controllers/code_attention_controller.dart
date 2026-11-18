import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/navigation/main_scaffold_controller.dart';
import '../../../../core/network/network_providers.dart';
import '../../../../core/storage/node_request_guard.dart';
import 'ai_workspace_task_sync_bus.dart';
import '../../data/task_attention_repository.dart';
import '../../../../shared/models/task_attention.dart';
import 'ai_workspace_controller.dart';

final codeAttentionRepositoryProvider = Provider<TaskAttentionRepository>((
  ref,
) {
  return TaskAttentionRepository(ref.watch(nodeApiClientProvider));
});

class CodeAttentionState {
  final bool isLoading;
  final String? errorMessage;
  final List<TaskAttention> items;
  final String? actionKey;

  const CodeAttentionState({
    this.isLoading = true,
    this.errorMessage,
    this.items = const [],
    this.actionKey,
  });

  CodeAttentionState copyWith({
    bool? isLoading,
    String? errorMessage,
    List<TaskAttention>? items,
    String? actionKey,
    bool clearError = false,
    bool clearAction = false,
  }) {
    return CodeAttentionState(
      isLoading: isLoading ?? this.isLoading,
      errorMessage: clearError ? null : (errorMessage ?? this.errorMessage),
      items: items ?? this.items,
      actionKey: clearAction ? null : (actionKey ?? this.actionKey),
    );
  }
}

final codeAttentionControllerProvider =
    NotifierProvider<CodeAttentionController, CodeAttentionState>(
      CodeAttentionController.new,
    );

class CodeAttentionController extends Notifier<CodeAttentionState> {
  TaskAttentionRepository? _boundRepo;
  TaskAttentionRepository get _repository =>
      _boundRepo ?? ref.read(codeAttentionRepositoryProvider);
  bool _syncedBeforeInitialLoad = false;

  @override
  CodeAttentionState build() {
    _boundRepo = ref.watch(codeAttentionRepositoryProvider);
    _syncedBeforeInitialLoad = false;
    ref.listen(mainScaffoldIndexProvider, (previous, next) {
      if (next == MainScaffoldIndexController.engineeringIndex) {
        unawaited(reloadSilently());
      }
    });
    Future.microtask(() {
      if (!_syncedBeforeInitialLoad) {
        unawaited(load());
      }
    });
    return const CodeAttentionState();
  }

  Future<void> load() => _load(silent: false);

  Future<void> reloadSilently() => _load(silent: true);

  void syncItems(List<TaskAttention> items) {
    if (!ref.mounted || _boundRepo == null) return;
    _syncedBeforeInitialLoad = true;
    state = state.copyWith(isLoading: false, items: items, clearError: true);
  }

  Future<void> _load({required bool silent}) async {
    if (_boundRepo == null) return;
    final nodeRequest = NodeRequestGuard(_repository);
    if (!ref.mounted || !nodeRequest.isCurrent(_repository)) {
      return;
    }

    if (!silent || state.items.isEmpty) {
      state = state.copyWith(isLoading: true, clearError: true);
    } else {
      state = state.copyWith(clearError: true);
    }
    try {
      final items = await nodeRequest.repository.list();
      if (!ref.mounted || !nodeRequest.isCurrent(_repository)) {
        return;
      }
      state = state.copyWith(isLoading: false, items: items);
    } catch (error) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repository)) {
        return;
      }
      state = state.copyWith(isLoading: false, errorMessage: error.toString());
    }
  }

  Future<bool> execute(
    TaskAttention attention,
    TaskAttentionAction action,
  ) async {
    if (_boundRepo == null) return false;
    final nodeRequest = NodeRequestGuard(_repository);
    if (state.actionKey != null) return false;
    final key = '${attention.id}:${action.type}';
    if (!ref.mounted || !nodeRequest.isCurrent(_repository)) {
      return false;
    }
    state = state.copyWith(actionKey: key, clearError: true);
    try {
      await nodeRequest.repository.execute(action);
      if (!ref.mounted || !nodeRequest.isCurrent(_repository)) {
        return false;
      }
      state = state.copyWith(
        items: state.items.where((item) => item.id != attention.id).toList(),
        clearAction: true,
      );
      final wsState = ref.read(aiWorkspaceControllerProvider);
      final wsNotifier = ref.read(aiWorkspaceControllerProvider.notifier);
      if (wsState.currentSession?.id == attention.sessionId) {
        await wsNotifier.refreshCurrentSession();
      }
      if (wsState.sessions.isNotEmpty || wsState.tasks.isNotEmpty) {
        unawaited(wsNotifier.refreshSessionsAndWorkspace());
      }
      unawaited(
        ref.read(aiWorkspaceTaskSyncBusProvider).refreshSilently(),
      );
      await load();
      return true;
    } catch (error) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repository)) {
        return false;
      }
      state = state.copyWith(errorMessage: error.toString(), clearAction: true);
      return false;
    }
  }
}
