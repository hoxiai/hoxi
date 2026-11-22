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
    with _AiWorkspaceDelivery, _AiWorkspaceStream, _AiWorkspaceConvoyAndQueue {
  AiWorkspaceRepository? _boundRepo;
  @override
  AiWorkspaceRepository get _repo =>
      _boundRepo ?? ref.read(aiWorkspaceRepositoryProvider);
  int? _refreshingSessionId;
  int? _pendingRefreshSessionId;
  int _refreshSeq = 0;
  int _projectRequest = 0;

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
  Future<void> refreshCurrentSession({bool showLoading = false}) async {
    if (_boundRepo == null) return;
    final nodeRequest = NodeRequestGuard(_repo);
    final sessionId = state.currentSession?.id;
    if (sessionId == null) return;
    if (_refreshingSessionId == sessionId) {
      _pendingRefreshSessionId = sessionId;
      return;
    }
    _refreshingSessionId = sessionId;
    final refreshSeq = ++_refreshSeq;
    if (showLoading) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return;
      state = state.copyWith(isLoading: true, clearError: true);
    }
    final titleBeforeRefresh = state.currentSession?.title ?? '';
    final wasStreamingBeforeRefresh = state.isStreaming;
    try {
      final results = await Future.wait<Object>([
        nodeRequest.repository.getSessionState(sessionId),
        nodeRequest.repository.getSessionHistory(sessionId, page: 1, limit: 50),
        _loadDelivery(sessionId),
      ]);
      if (state.currentSession?.id != sessionId || refreshSeq != _refreshSeq) return;
      final streamEndedInFlight = wasStreamingBeforeRefresh && !state.isStreaming;
      final stillActivelyStreaming = state.isStreaming && _pendingRefreshSessionId != sessionId;
      final sessionState = results[0] as AiSessionStateInfo;
      final history = results[1] as CodeSessionHistory;
      final (delivery, deliveryError) = results[2] as (CodeDeliveryJob?, String?);
      final historyMessages = codeHistoryChatMessages(history.messages, runs: history.runs);
      final baseMessages = historyMessages.isNotEmpty ? historyMessages : sessionState.recentMessages;
      final persistedUsers = state.chatHistory.where((m) => m.isUser && !m.id.startsWith('local-')).length;
      final hasUnpersistedLocalTurn = state.chatHistory.any((m) => m.isUser && m.id.startsWith('local-')) &&
          baseMessages.where((m) => m.isUser).length <= persistedUsers;
      final isSessionRunning = !streamEndedInFlight &&
          (state.isSending ||
              stillActivelyStreaming ||
              sessionState.currentStage == 'running' ||
              sessionState.currentStage == 'executing' ||
              sessionState.currentStage == 'instruction_queued' ||
              sessionState.session.status == 'running' ||
              sessionState.session.status == 'busy');
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
      state = state.copyWith(
        isLoading: false,
        isStreaming: isSessionRunning || state.isSending,
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
      _notifyTaskCenterSession(syncedSession, currentStage: effectiveStage, stateInfo: sessionState);
      _syncDeliveryPolling();
      drainNextQueuedPromptIfIdle();
    } catch (error) {
      if (state.currentSession?.id != sessionId || !ref.mounted || !nodeRequest.isCurrent(_repo)) {
        return;
      }
      state = state.copyWith(isLoading: false, errorMessage: '会话状态刷新失败：$error');
    } finally {
      if (refreshSeq == _refreshSeq) {
        if (_refreshingSessionId == sessionId) _refreshingSessionId = null;
        if (_pendingRefreshSessionId == sessionId) {
          _pendingRefreshSessionId = null;
          if (ref.mounted && nodeRequest.isCurrent(_repo) && state.currentSession?.id == sessionId) {
            unawaited(refreshCurrentSession());
          }
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

  @override
  Future<bool> sendMessage(
    String text, {
    CodeInstructionOptions options = const CodeInstructionOptions(),
    String? imageDataUrl,
    List<String> attachedFiles = const [],
    List<LocalPickedDocument> localDocuments = const [],
    NodeSkillSelection? skillSelection,
    bool bypassQueue = false,
  }) async {
    final nodeRequest = NodeRequestGuard(_repo);
    final rawText = text.trim();
    final hasImage = imageDataUrl != null && imageDataUrl.trim().isNotEmpty;
    final hasDocuments = localDocuments.isNotEmpty;
    final effectiveSkill = skillSelection ?? state.selectedSkill;
    final fileRefs = attachedFiles
        .map((f) => f.trim())
        .where((f) => f.isNotEmpty && !rawText.contains('@$f'))
        .map((f) => '@$f')
        .join(' ');
    final content = [if (fileRefs.isNotEmpty) fileRefs, if (rawText.isNotEmpty) rawText].join('\n');
    final session = state.currentSession;
    if (session == null) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      state = state.copyWith(errorMessage: '请先创建或选择一个开发会话');
      return false;
    }
    if (content.isEmpty && !hasImage && !hasDocuments && effectiveSkill == null) return false;
    if (!bypassQueue && (state.isSending || state.isAiThinking)) {
      enqueueFollowUpPrompt(
        sessionId: session.id,
        text: rawText,
        imageDataUrl: hasImage ? imageDataUrl.trim() : null,
        attachedFiles: attachedFiles,
        localDocuments: localDocuments,
        skillSelection: effectiveSkill,
        options: options,
      );
      return true;
    }
    if (state.isSending) return false;
    final now = DateTime.now();
    final docTags = localDocuments.map((d) => '[Attachment: ${d.name}]').join('\n');
    final bubbleText = [
      if (content.isNotEmpty) content,
      if (docTags.isNotEmpty) docTags,
    ].join('\n');
    final userMessage = ChatMessage(
      id: 'local-${now.microsecondsSinceEpoch}',
      text: bubbleText.isNotEmpty ? bubbleText : '[@Skill: ${effectiveSkill?.name ?? ""}]',
      isUser: true, timestamp: now,
      imageDataUrl: hasImage ? imageDataUrl.trim() : null,
    );
    final agentLabel = [
      if (session.agentName.trim().isNotEmpty) session.agentName.trim(),
      if (session.providerModel.trim().isNotEmpty) session.providerModel.trim(),
    ].join(' · ');
    final streamPlaceholder = ChatMessage(
      id: 'stream-current', text: '', isUser: false, timestamp: now,
      activity: '正在下发指令...', isStreaming: true, agentLabel: agentLabel,
    );
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
    _clearSessionStopped(session.id);
    final runningSession = applyPromptAutoTitle(session, content, hasImage: hasImage, now: now)
        .copyWith(status: 'running', currentStage: 'running', updatedAt: now);
    state = state.copyWith(
      isSending: true, isStreaming: true, currentStage: 'running',
      currentSession: runningSession,
      sessions: upsertWorkspaceSession(state.sessions, runningSession),
      tasks: syncWorkspaceTasksWithSession(state.tasks, runningSession, stageOverride: 'running'),
      turnChangedFiles: const [], turnChangedFileStats: const [],
      turnDiffAdditions: 0, turnDiffDeletions: 0, todos: const [],
      clearError: true, clearSelectedSkill: true,
      chatHistory: [
        ...state.chatHistory.where((m) => !m.id.startsWith('stream-')),
        userMessage, streamPlaceholder,
      ],
    );
    _notifyTaskCenterSession(runningSession, currentStage: 'running');
    _connectConversationStream(session.id);
    try {
      final attachments = <Map<String, dynamic>>[];
      if (hasImage) {
        final commaIdx = imageDataUrl.indexOf(',');
        final header = commaIdx > 5 ? imageDataUrl.substring(5, commaIdx) : 'image/jpeg;base64';
        final mime = header.split(';').first;
        final ext = mime.endsWith('png') ? 'png' : (mime.endsWith('webp') ? 'webp' : 'jpg');
        final uploaded = await nodeRequest.repository.uploadAttachment(
          sessionId: session.id,
          filename: 'image_${now.millisecondsSinceEpoch}.$ext',
          mime: mime, dataUrl: imageDataUrl,
        );
        attachments.add(uploaded);
      }
      final useJsonEnvelope = (nodeRequest.node.e2eeKey ?? '').trim().isNotEmpty;
      for (final doc in localDocuments) {
        final uploaded = await nodeRequest.repository.uploadDocumentAttachment(
          sessionId: session.id,
          filename: doc.name,
          mime: doc.mime,
          bytes: doc.bytes,
          useJsonEnvelope: useJsonEnvelope,
        );
        attachments.add(uploaded);
      }
      final result = await nodeRequest.repository.sendAiCommand(
        sessionId: session.id, command: content,
        autoPreview: options.autoPreview, requireApproval: options.requireApproval,
        attachments: attachments.isEmpty ? null : attachments,
        skillSelection: effectiveSkill,
      );
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      final postStage = result.approval == null ? 'instruction_queued' : 'awaiting_approval';
      final postSession = applyPromptAutoTitle(result.session, content, hasImage: hasImage, now: now)
          .copyWith(currentStage: postStage);
      final postTask = syncTaskSummaryTitle(result.task, postSession);
      if (isPlaceholderSessionTitle(result.session.title) &&
          !isPlaceholderSessionTitle(postSession.title)) {
        unawaited(
          nodeRequest.repository
              .updateSessionTitle(session.id, postSession.title)
              .catchError((_) => postSession),
        );
      }
      state = state.copyWith(
        currentSession: postSession,
        sessions: upsertWorkspaceSession(state.sessions, postSession),
        tasks: syncWorkspaceTasksWithSession(
          state.tasks, postSession, currentTask: postTask, stageOverride: postStage,
        ),
        currentTask: postTask,
        latestInstruction: result.instruction,
        pendingApproval: result.approval,
        currentStage: postStage,
        clearApproval: result.approval == null,
      );
      _notifyTaskCenterSession(postSession, currentStage: postStage);
      if (result.approval != null) {
        unawaited(ref.read(codeAttentionControllerProvider.notifier).reloadSilently());
      }
      await refreshCurrentSession();
      return true;
    } catch (error) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      state = state.copyWith(
        isStreaming: false,
        chatHistory: state.chatHistory
            .where((m) => m.id != 'stream-current' || m.text.trim().isNotEmpty)
            .toList(),
        errorMessage: '指令发送失败：$error',
      );
      return false;
    } finally {
      if (ref.mounted && nodeRequest.isCurrent(_repo)) {
        state = state.copyWith(isSending: false);
      }
    }
  }

  Future<bool> retryLatestInstruction() async {
    final nodeRequest = NodeRequestGuard(_repo);
    final session = state.currentSession;
    if (session == null || state.isSending || state.isActionLoading) return false;
    final instruction = state.latestInstruction;
    if (instruction != null && instruction.id > 0) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      _clearSessionStopped(session.id);
      state = state.copyWith(
        isActionLoading: true, isStreaming: true, currentStage: 'running',
        turnChangedFiles: const [], turnChangedFileStats: const [],
        turnDiffAdditions: 0, turnDiffDeletions: 0, todos: const [],
        clearError: true,
      );
      _connectConversationStream(session.id);
      try {
        final retried = await nodeRequest.repository.retryInstruction(instruction.id);
        if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
        state = state.copyWith(
          isActionLoading: false, latestInstruction: retried,
          currentStage: 'instruction_queued',
        );
        await refreshCurrentSession();
        return true;
      } catch (error) {
        if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
        state = state.copyWith(
          isActionLoading: false, isStreaming: false,
          errorMessage: '重试指令失败：$error',
        );
        return false;
      }
    }
    final lastUser = state.chatHistory.lastWhere(
      (m) => m.isUser && m.text.trim().isNotEmpty,
      orElse: () => ChatMessage(
        id: '', text: '', isUser: true,
        timestamp: DateTime.fromMillisecondsSinceEpoch(0),
      ),
    );
    if (lastUser.text.trim().isEmpty) return false;
    return sendMessage(lastUser.text, options: const CodeInstructionOptions(), bypassQueue: true);
  }

  Future<void> decideApproval({required bool approved, String reason = ''}) async {
    final nodeRequest = NodeRequestGuard(_repo);
    final approval = state.pendingApproval;
    if (approval == null || state.isActionLoading) return;
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return;
    state = state.copyWith(isActionLoading: true, clearError: true);
    try {
      if (approved) {
        await nodeRequest.repository.approveApproval(approval.id, reason: reason);
      } else {
        await nodeRequest.repository.rejectApproval(approval.id, reason: reason);
      }
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return;
      state = state.copyWith(isActionLoading: false, clearApproval: true);
      await refreshCurrentSession();
      unawaited(ref.read(codeAttentionControllerProvider.notifier).reloadSilently());
      unawaited(ref.read(aiWorkspaceTaskSyncBusProvider).refreshSilently());
    } catch (error) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return;
      state = state.copyWith(isActionLoading: false, errorMessage: '审批处理失败：$error');
    }
  }

  Future<bool> stopCurrentSession() async {
    final nodeRequest = NodeRequestGuard(_repo);
    final sessionId = state.currentSession?.id;
    if (sessionId == null || state.isActionLoading) return false;
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
    state = state.copyWith(isActionLoading: true, clearError: true);
    try {
      await nodeRequest.repository.stopSession(sessionId);
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      _markSessionStopped(sessionId);
      _refreshSeq++;
      _refreshingSessionId = null;
      _pendingRefreshSessionId = null;
      final stoppedSession = state.currentSession?.copyWith(
        status: 'active', currentStage: 'idle', updatedAt: DateTime.now(),
      );
      state = state.copyWith(
        isActionLoading: false, isStreaming: false, currentStage: 'idle',
        streamActivityKind: '', streamActivity: '',
        currentSession: stoppedSession,
        sessions: stoppedSession == null ? null : upsertWorkspaceSession(state.sessions, stoppedSession),
        tasks: stoppedSession == null
            ? null
            : syncWorkspaceTasksWithSession(state.tasks, stoppedSession, currentTask: state.currentTask, stageOverride: 'idle'),
        chatHistory: finalizeStoppedChatHistory(state.chatHistory),
        turnChangedFiles: const [], turnChangedFileStats: const [],
        turnDiffAdditions: 0, turnDiffDeletions: 0, todos: const [],
      );
      await refreshCurrentSession();
      if (!_streamSuspended) _connectConversationStream(sessionId);
      unawaited(ref.read(aiWorkspaceTaskSyncBusProvider).refreshSilently());
      return true;
    } catch (error) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      state = state.copyWith(isActionLoading: false, errorMessage: error.toString());
      return false;
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
