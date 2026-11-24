import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/models/node_skill.dart';
import '../../../../core/navigation/main_scaffold_controller.dart';
import '../../../../core/network/network_providers.dart';
import '../../../../core/storage/active_node_provider.dart';
import '../../../../core/storage/node_request_guard.dart';
import '../../../../shared/models/task_entity.dart';
import '../../../../shared/utils/local_file_bridge.dart';
import 'ai_workspace_task_sync_bus.dart';
import '../../data/ai_workspace_repository.dart';
import '../../data/hoxi_workspace_adapter.dart';
import '../../models/ai_dev_session.dart';
import '../../models/ai_session_state_info.dart';
import '../../models/chat_message.dart';
import '../../models/code_convoy_plan.dart';
import '../../models/code_instruction_options.dart';
import '../../models/code_delivery_job.dart';
import '../../models/code_conversation_stream.dart';
import '../../models/code_model_catalog.dart';
import '../../models/code_session_recovery.dart';
import '../../models/code_task.dart';
import 'ai_workspace_state.dart';
import 'code_attention_controller.dart';
import 'code_task_sessions.dart';

export 'ai_workspace_state.dart';

part 'ai_workspace_convoy_queue.dart';
part 'ai_workspace_delivery.dart';
part 'ai_workspace_stream.dart';
part 'ai_workspace_turn_actions.dart';

final hoxiWorkspaceAdapterProvider = Provider<HoxiWorkspaceAdapter>((ref) {
  return HoxiWorkspaceAdapter(ref.watch(nodeApiClientProvider));
});

final aiWorkspaceRepositoryProvider = Provider<AiWorkspaceRepository>((ref) {
  return AiWorkspaceRepository(
    ref.watch(nodeApiClientProvider),
    hoxiAdapter: ref.watch(hoxiWorkspaceAdapterProvider),
  );
});

final aiWorkspaceControllerProvider =
    NotifierProvider<AiWorkspaceController, AiWorkspaceState>(
      AiWorkspaceController.new,
    );

class AiWorkspaceController extends Notifier<AiWorkspaceState>
    with
        _AiWorkspaceDelivery,
        _AiWorkspaceStream,
        _AiWorkspaceConvoyAndQueue,
        _AiWorkspaceTurnActions {
  AiWorkspaceRepository? _boundRepo;
  @override
  AiWorkspaceRepository get _repo =>
      _boundRepo ?? ref.read(aiWorkspaceRepositoryProvider);
  int? _refreshingSessionId;
  int? _pendingRefreshSessionId;
  bool _pendingRefreshTrustServerState = false;
  int _refreshSeq = 0;
  int _projectRequest = 0;

  @override
  void _resetRefreshTracking() {
    _refreshSeq++;
    _refreshingSessionId = null;
    _pendingRefreshSessionId = null;
    _pendingRefreshTrustServerState = false;
  }

  @override
  AiWorkspaceState build() {
    _boundRepo = ref.watch(aiWorkspaceRepositoryProvider);
    ref.listen(activeNodeGenerationProvider, (previous, next) {
      if (previous != next) unawaited(loadWorkspace());
    });
    ref.listen(mainScaffoldIndexProvider, (previous, next) {
      unawaited(_syncWorkspaceVisibility());
    });
    ref.onDispose(() {
      _projectRequest++;
      _stopDeliveryPolling();
      _cancelStreamQuietWatchdog();
      _streamReconnectTimer?.cancel();
      _streamCancelToken?.cancel();
      _streamSubscription?.cancel();
    });
    Future.microtask(loadWorkspace);
    unawaited(CodeModelCatalog.loadLastSelection());
    return AiWorkspaceState(chatHistory: [_welcomeMessage()]);
  }

  Future<void> _syncWorkspaceVisibility() async {
    final visible =
        ref.read(mainScaffoldIndexProvider) ==
        MainScaffoldIndexController.engineeringIndex;
    if (!visible) {
      _stopDeliveryPolling();
      await suspendConversationStream();
      return;
    }
    if (_streamSuspended) {
      await resumeConversationStream();
    }
    unawaited(refreshSessionsAndWorkspace());
    _syncDeliveryPolling();
  }

  Future<void> loadWorkspace() async {
    if (_boundRepo == null) return;
    final nodeRequest = NodeRequestGuard(_repo);
    final request = ++_projectRequest;
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return;
    final isDefaultRepo = _repo.runtimeType == AiWorkspaceRepository;
    if ((isDefaultRepo && !nodeRequest.node.isConnected) ||
        nodeRequest.node.isAuxService) {
      _stopDeliveryPolling();
      await _stopConversationStream();
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return;
      state = _clearedWorkspaceState(clearProjects: true);
      return;
    }
    state = state.copyWith(isLoading: true, clearError: true);
    try {
      final results = await Future.wait([
        nodeRequest.repository.getProjects(),
        nodeRequest.repository.getExecutors(),
      ]);
      final projects = results[0] as List<CodeProject>;
      final executors = results[1] as List<CodeExecutor>;
      final projectId = _resolveProjectId(projects);
      final projectData = await _loadAllSessions();
      final sessions = projectData.$1;
      final tasks = projectData.$2;
      if (request != _projectRequest ||
          !ref.mounted ||
          !nodeRequest.isCurrent(_repo)) {
        return;
      }
      final currentId = state.currentSession?.id;
      final hasCurrentInSessions =
          currentId != null &&
          sessions.any(
            (s) =>
                s.id == currentId ||
                s.subagents.any((sub) => sub.id == currentId),
          );
      final clearSelection = !hasCurrentInSessions && state.currentSession != null;
      final effectiveProjectId = hasCurrentInSessions &&
              (state.currentSession?.projectId ?? 0) > 0
          ? state.currentSession!.projectId
          : projectId;
      state = state.copyWith(
        isLoading: false,
        projects: projects, executors: executors,
        sessions: sessions, tasks: tasks,
        selectedProjectId: effectiveProjectId,
        clearSession: clearSelection,
        clearTask: clearSelection,
        clearInstruction: clearSelection,
        clearApproval: clearSelection,
        clearDelivery: clearSelection,
      );
      if ((clearSelection || state.currentSession == null) &&
          sessions.isNotEmpty) {
        await selectSession(sessions.first);
      }
    } catch (error) {
      if (request != _projectRequest ||
          !ref.mounted ||
          !nodeRequest.isCurrent(_repo)) {
        return;
      }
      state = state.copyWith(isLoading: false, errorMessage: '开发工作区加载失败：$error');
    }
  }

  AiWorkspaceState _clearedWorkspaceState({
    bool clearProjects = false, bool isLoading = false, int? selectedProjectId,
  }) => state.copyWith(
    isLoading: isLoading,
    projects: clearProjects ? const [] : null,
    executors: clearProjects ? const [] : null,
    selectedProjectId: selectedProjectId,
    sessions: const [], tasks: const [],
    chatHistory: [_welcomeMessage()],
    currentStage: 'idle', recentOutput: '',
    previews: const [], timelineEvents: const [], errorSummary: '',
    changedFiles: const [], changedFileStats: const [],
    diffAdditions: 0, diffDeletions: 0,
    turnChangedFiles: const [], turnChangedFileStats: const [],
    turnDiffAdditions: 0, turnDiffDeletions: 0, todos: const [],
    convoyPlans: const [], subagents: const [],
    queuedPrompts: const [],
    clearError: true, clearSession: true, clearTask: true,
    clearInstruction: true, clearApproval: true,
    clearDelivery: true, clearDeliveryError: true,
    clearSelectedSkill: true,
  );

  Future<void> selectProject(int projectId) async {
    final nodeRequest = NodeRequestGuard(_repo);
    if (projectId <= 0 || projectId == state.selectedProjectId) return;
    final request = ++_projectRequest;
    _stopDeliveryPolling();
    _cancelStreamQuietWatchdog();
    _clearLiveSseRunning();
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return;
    state = _clearedWorkspaceState(isLoading: true, selectedProjectId: projectId);
    try {
      final projectData = await _loadProjectSessions(projectId);
      final sessions = projectData.$1;
      if (request != _projectRequest || !ref.mounted || !nodeRequest.isCurrent(_repo)) {
        return;
      }
      state = state.copyWith(isLoading: false, sessions: sessions, tasks: projectData.$2);
      if (sessions.isNotEmpty) await selectSession(sessions.first);
    } catch (error) {
      if (request != _projectRequest || !ref.mounted || !nodeRequest.isCurrent(_repo)) {
        return;
      }
      state = state.copyWith(isLoading: false, errorMessage: '开发任务加载失败：$error');
    }
  }

  Future<AiDevSession> createSession({
    required int projectId,
    required String executorId,
    required String approvalPolicy,
    String title = '',
    bool isolated = false,
    String model = '',
  }) async {
    final nodeRequest = NodeRequestGuard(_repo);
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) throw StateError('节点已切换');

    state = state.copyWith(isActionLoading: true, clearError: true);
    try {
      final session = await nodeRequest.repository.createSession(
        projectId: projectId, executorId: executorId,
        approvalPolicy: approvalPolicy, title: title.trim(),
        isolated: isolated, model: model.trim(),
      );
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) throw StateError('节点已切换');
      final effectiveModel = session.providerModel.trim().isNotEmpty
          ? session.providerModel.trim()
          : model.trim();
      if (effectiveModel.isNotEmpty) {
        unawaited(
          CodeModelCatalog.saveLastSelection(
            model: effectiveModel,
            executorId: session.agentName.trim().isNotEmpty
                ? session.agentName.trim()
                : executorId.trim(),
          ),
        );
      }
      state = state.copyWith(
        isActionLoading: false,
        selectedProjectId: projectId,
        sessions: upsertWorkspaceSession(state.sessions, session),
        tasks: syncWorkspaceTasksWithSession(state.tasks, session),
      );
      _notifyTaskCenterSession(session);
      await selectSession(session);
      return session;
    } catch (error) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) throw StateError('节点已切换');
      state = state.copyWith(isActionLoading: false, errorMessage: '创建开发会话失败：$error');
      rethrow;
    }
  }

  @override
  Future<void> selectSession(AiDevSession session) async {
    final nodeRequest = NodeRequestGuard(_repo);
    _projectRequest++;
    _stopDeliveryPolling();
    if (_pendingRefreshSessionId != session.id) {
      _pendingRefreshSessionId = null;
      _pendingRefreshTrustServerState = false;
    }
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return;
    state = state.copyWith(
      isLoading: true,
      selectedProjectId:
          session.projectId > 0 ? session.projectId : state.selectedProjectId,
      currentSession: session,
      currentStage: session.currentStage,
      chatHistory: [_welcomeMessage()],
      previews: const [],
      timelineEvents: const [],
      errorSummary: '',
      changedFiles: const [],
      changedFileStats: const [],
      diffAdditions: 0,
      diffDeletions: 0,
      todos: const [],
      convoyPlans: const [],
      subagents: const [],
      clearError: true,
      clearTask: true,
      clearInstruction: true,
      clearApproval: true,
      clearDelivery: true,
      clearDeliveryError: true,
      clearSelectedSkill: true,
    );
    await _stopConversationStream();
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return;
    if (state.currentSession?.id != session.id) return;
    await refreshCurrentSession(showLoading: true);
    if (!ref.mounted || state.currentSession?.id != session.id) return;
    _connectConversationStream(session.id);
  }

  @override
  Future<void> refreshCurrentSession({
    bool showLoading = false,
    bool trustServerState = false,
  }) async {
    if (_boundRepo == null) return;
    final nodeRequest = NodeRequestGuard(_repo);
    final sessionId = state.currentSession?.id;
    if (sessionId == null) return;
    final effectiveTrustServer = trustServerState || showLoading;
    if (_refreshingSessionId == sessionId) {
      _pendingRefreshSessionId = sessionId;
      _pendingRefreshTrustServerState =
          _pendingRefreshTrustServerState || effectiveTrustServer;
      return;
    }
    _refreshingSessionId = sessionId;
    final refreshSeq = ++_refreshSeq;
    if (effectiveTrustServer) {
      nodeRequest.repository.clearSessionBusyGrace(sessionId);
    }
    if (showLoading) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return;
      state = state.copyWith(isLoading: true, clearError: true);
    }
    final titleBeforeRefresh = state.currentSession?.title ?? '';
    final wasStreamingBeforeRefresh = state.isStreaming;
    final refreshTerminalSeq = _streamTerminalSeq;
    try {
      final results = await Future.wait<Object>([
        nodeRequest.repository.getSessionState(sessionId),
        nodeRequest.repository.getSessionHistory(sessionId, page: 1, limit: 50),
        _loadDelivery(sessionId),
      ]);
      if (state.currentSession?.id != sessionId || refreshSeq != _refreshSeq) return;
      final streamEndedInFlight =
          _streamTerminalSeq != refreshTerminalSeq ||
          (wasStreamingBeforeRefresh && !state.isStreaming);
      final activeSendStillPending =
          state.isSending &&
          _activeSendSessionId == sessionId &&
          _streamTerminalSeq == _activeSendTerminalSeq;
      final stillActivelyStreaming =
          !effectiveTrustServer &&
          state.isStreaming &&
          _hasRecentLiveSseRunning(sessionId) &&
          _pendingRefreshSessionId != sessionId;
      final sessionState = results[0] as AiSessionStateInfo;
      final history = results[1] as CodeSessionHistory;
      final (delivery, deliveryError) = results[2] as (CodeDeliveryJob?, String?);
      final historyMessages = codeHistoryChatMessages(history.messages, runs: history.runs);
      final baseMessages = historyMessages.isNotEmpty ? historyMessages : sessionState.recentMessages;
      final persistedUsers = state.chatHistory.where((m) => m.isUser && !m.id.startsWith('local-')).length;
      final hasUnpersistedLocalTurn = state.chatHistory.any((m) => m.isUser && m.id.startsWith('local-')) &&
          baseMessages.where((m) => m.isUser).length <= persistedUsers;
      final serverRunning =
          sessionState.currentStage == 'running' ||
          sessionState.currentStage == 'executing' ||
          sessionState.currentStage == 'instruction_queued' ||
          sessionState.session.status == 'running' ||
          sessionState.session.status == 'busy';
      final isSessionRunning = !streamEndedInFlight &&
          (activeSendStillPending || stillActivelyStreaming || serverRunning);
      final isTurnActive = isSessionRunning || sessionState.currentStage == 'awaiting_approval';
      final keepTurnShelf = isTurnActive && !hasUnpersistedLocalTurn;
      final rawStage = streamEndedInFlight ? state.currentStage : sessionState.currentStage;
      final effectiveStage = isSessionRunning && (rawStage.isEmpty || rawStage == 'idle' || rawStage == 'ready')
          ? (state.currentStage.isNotEmpty && state.currentStage != 'idle' ? state.currentStage : 'running')
          : rawStage;
      final messages = mergeWorkspaceHistoryMessages(
        baseMessages, state.chatHistory, isSessionRunning: isSessionRunning,
      );
      final currentTitle = state.currentSession?.title ?? '';
      final keepRenamed = currentTitle.trim().isNotEmpty && currentTitle != titleBeforeRefresh;
      final incomingWithStats = sessionState.session.copyWith(
        providerModel: sessionState.session.providerModel.isNotEmpty
            ? sessionState.session.providerModel
            : history.session.providerModel,
        diffAdditions: sessionState.diffAdditions,
        diffDeletions: sessionState.diffDeletions,
      );
      final reconciled = reconcileRefreshedSessionTitle(
        incomingSession: incomingWithStats, incomingTask: sessionState.currentTask,
        previousSession: state.currentSession, messages: messages,
        currentStage: effectiveStage, concurrentRenamedTitle: keepRenamed ? currentTitle : null,
      );
      final syncedSession = reconciled.session;
      final syncedTask = reconciled.currentTask;
      final sessions = upsertWorkspaceSession(state.sessions, syncedSession);
      final tasks = syncWorkspaceTasksWithSession(
        state.tasks, syncedSession, currentTask: syncedTask, stageOverride: effectiveStage,
      );
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return;
      if (!isSessionRunning &&
          (effectiveStage == 'idle' ||
              effectiveStage == 'completed' ||
              effectiveStage == 'failed')) {
        _clearLiveSseRunning(sessionId);
        if (effectiveTrustServer && !serverRunning) {
          if (wasStreamingBeforeRefresh ||
              (activeWorkspaceStages.contains(state.currentStage) &&
                  state.currentStage != 'awaiting_approval')) {
            _markRecentlyTerminal(sessionId);
          }
          nodeRequest.repository.markSessionStage(sessionId, effectiveStage);
        }
      }
      state = state.copyWith(
        isLoading: false,
        isStreaming: isSessionRunning,
        clearError: true,
        sessions: sessions, tasks: tasks, chatHistory: messages,
        currentSession: syncedSession, currentTask: syncedTask,
        latestInstruction: sessionState.latestInstruction,
        pendingApproval: sessionState.pendingApproval,
        currentStage: effectiveStage,
        recentOutput: sessionState.recentOutput,
        previews: sessionState.previews,
        timelineEvents: sessionState.timelineEvents,
        errorSummary: sessionState.errorSummary,
        changedFiles: sessionState.changedFiles,
        changedFileStats: sessionState.changedFileStats,
        diffAdditions: sessionState.diffAdditions,
        diffDeletions: sessionState.diffDeletions,
        turnChangedFiles: hasUnpersistedLocalTurn ? const [] : sessionState.effectiveTurnChangedFiles,
        turnChangedFileStats: hasUnpersistedLocalTurn ? const [] : sessionState.effectiveTurnChangedFileStats,
        turnDiffAdditions: hasUnpersistedLocalTurn ? 0 : sessionState.effectiveTurnDiffAdditions,
        turnDiffDeletions: hasUnpersistedLocalTurn ? 0 : sessionState.effectiveTurnDiffDeletions,
        todos: keepTurnShelf ? sessionState.todos : const [],
        convoyPlans: sessionState.convoyPlans,
        subagents: sessionState.subagents,
        delivery: delivery, clearDelivery: delivery == null,
        deliveryError: deliveryError, clearDeliveryError: deliveryError == null,
        clearTask: sessionState.currentTask == null,
        clearInstruction: sessionState.latestInstruction == null,
        clearApproval: sessionState.pendingApproval == null,
      );
      _syncStreamQuietWatchdog();
      _notifyTaskCenterSession(syncedSession, currentStage: effectiveStage, stateInfo: sessionState);
      _syncDeliveryPolling();
    } catch (error) {
      if (state.currentSession?.id != sessionId || !ref.mounted || !nodeRequest.isCurrent(_repo)) {
        return;
      }
      state = state.copyWith(isLoading: false, errorMessage: '会话状态刷新失败：$error');
      _syncStreamQuietWatchdog();
    } finally {
      if (refreshSeq == _refreshSeq) {
        if (_refreshingSessionId == sessionId) _refreshingSessionId = null;
        if (_pendingRefreshSessionId == sessionId) {
          final nextTrustServer = _pendingRefreshTrustServerState;
          _pendingRefreshSessionId = null;
          _pendingRefreshTrustServerState = false;
          if (ref.mounted && nodeRequest.isCurrent(_repo) && state.currentSession?.id == sessionId) {
            unawaited(
              refreshCurrentSession(trustServerState: nextTrustServer),
            );
          }
        } else if (ref.mounted &&
            nodeRequest.isCurrent(_repo) &&
            state.currentSession?.id == sessionId) {
          drainNextQueuedPromptIfIdle();
        }
      }
    }
  }

  /// 供沟通页（ChatHub）或子页返回后同步刷新工作台近期活跃会话列表与当前会话状态。
  Future<void> refreshSessionsAndWorkspace({bool recentOnly = true}) async {
    if (_boundRepo == null) return;
    final nodeRequest = NodeRequestGuard(_repo);
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return;
    final isDefaultRepo = _repo.runtimeType == AiWorkspaceRepository;
    if ((isDefaultRepo && !nodeRequest.node.isConnected) ||
        nodeRequest.node.isAuxService) {
      return;
    }
    try {
      final needCatalog = state.executors.isEmpty || state.projects.isEmpty;
      final projectsFuture = needCatalog ? nodeRequest.repository.getProjects() : null;
      final executorsFuture = needCatalog ? nodeRequest.repository.getExecutors() : null;
      final useRecent = recentOnly && state.sessions.isNotEmpty;
      final rawData = useRecent ? await _loadRecentAllSessions() : await _loadAllSessions();
      final projects = projectsFuture != null ? await projectsFuture : state.projects;
      final executors = executorsFuture != null ? await executorsFuture : state.executors;
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return;
      final activeStreamingId =
          (state.isSending || state.isStreaming) ? state.currentSession?.id : null;
      final merged = mergeRecentWorkspaceSessionsAndTasks(
        existingSessions: useRecent ? state.sessions : const [],
        existingTasks: useRecent ? state.tasks : const [],
        recentSessions: rawData.$1,
        recentTasks: rawData.$2,
        activeStreamingSessionId: activeStreamingId,
      );
      final sessions = merged.sessions;
      final currentId = state.currentSession?.id;
      final updatedCurrent = currentId == null
          ? sessions.firstOrNull
          : (sessions.where((s) => s.id == currentId).firstOrNull ?? state.currentSession);
      state = state.copyWith(
        projects: projects, executors: executors,
        selectedProjectId: state.selectedProjectId ?? _resolveProjectId(projects),
        sessions: sessions, tasks: merged.tasks, currentSession: updatedCurrent,
      );
      if (state.currentSession != null) await refreshCurrentSession();
    } catch (_) {}
  }

  Future<List<CodeExecutor>> ensureExecutorsLoaded({bool forceRefresh = false}) async {
    if (!forceRefresh && state.executors.isNotEmpty) return state.executors;
    final nodeRequest = NodeRequestGuard(_repo);
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return state.executors;
    try {
      final executors = await nodeRequest.repository.getExecutors();
      if (ref.mounted && nodeRequest.isCurrent(_repo) && executors.isNotEmpty) {
        state = state.copyWith(executors: executors);
      }
      return state.executors;
    } catch (_) {
      return state.executors;
    }
  }

  void clearError() => state = state.copyWith(clearError: true);

  ChatMessage _welcomeMessage() => ChatMessage(
    id: 'welcome', text: '选择已有会话，或从开发项目创建会话，然后发送开发指令。',
    isUser: false, timestamp: DateTime.now(),
  );

  int? _resolveProjectId(List<CodeProject> projects) {
    final selected = state.selectedProjectId;
    if (projects.any((project) => project.id == selected)) return selected;
    final current = state.currentSession?.projectId;
    if (projects.any((project) => project.id == current)) return current;
    return projects.firstOrNull?.id;
  }

  Future<(List<AiDevSession>, List<CodeTask>)> _loadProjectSessions(int projectId) async {
    final results = await Future.wait([_repo.getSessions(projectId: projectId), _repo.getTasks(projectId: projectId)]);
    final sessions = results[0] as List<AiDevSession>;
    final tasks = results[1] as List<CodeTask>;
    return (alignCodeTaskSessions(sessions, tasks, projectId: projectId), tasks.where((task) => task.agentName != 'terminal').toList());
  }

  Future<(List<AiDevSession>, List<CodeTask>)> _loadAllSessions() async {
    final results = await Future.wait([_repo.getSessions(), _repo.getTasks()]);
    return ((results[0] as List<AiDevSession>).where((s) => s.agentName != 'terminal').toList(), (results[1] as List<CodeTask>).where((t) => t.agentName != 'terminal').toList());
  }

  Future<(List<AiDevSession>, List<CodeTask>)> _loadRecentAllSessions() async {
    final results = await Future.wait([_repo.getRecentActiveSessions(), _repo.getRecentActiveTasks()]);
    return ((results[0] as List<AiDevSession>).where((s) => s.agentName != 'terminal').toList(), (results[1] as List<CodeTask>).where((t) => t.agentName != 'terminal').toList());
  }
}
