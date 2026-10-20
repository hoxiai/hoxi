import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/network/api_client.dart';
import '../../data/ai_workspace_repository.dart';
import '../../models/ai_dev_session.dart';
import '../../models/ai_session_state_info.dart';
import '../../models/chat_message.dart';
import '../../models/code_instruction_options.dart';
import '../../models/code_delivery_job.dart';
import '../../models/code_conversation_stream.dart';
import '../../models/code_session_recovery.dart';
import '../../models/code_task.dart';
import 'ai_workspace_state.dart';
import 'code_task_sessions.dart';

export 'ai_workspace_state.dart';

final aiWorkspaceRepositoryProvider = Provider<AiWorkspaceRepository>((ref) {
  return AiWorkspaceRepository(ApiClient());
});

final aiWorkspaceControllerProvider =
    NotifierProvider<AiWorkspaceController, AiWorkspaceState>(
      AiWorkspaceController.new,
    );

class AiWorkspaceController extends Notifier<AiWorkspaceState> {
  static const _deliveryPollInterval = Duration(seconds: 3);
  late AiWorkspaceRepository _repo;
  Timer? _deliveryPollTimer;
  Timer? _streamReconnectTimer;
  StreamSubscription<String>? _streamSubscription;
  CancelToken? _streamCancelToken;
  int? _streamSessionId;
  bool _streamConnecting = false;
  bool _streamSuspended = false;
  int _streamReconnectAttempts = 0;
  int? _refreshingSessionId;
  int _projectRequest = 0;
  @override
  AiWorkspaceState build() {
    _repo = ref.watch(aiWorkspaceRepositoryProvider);
    ref.onDispose(() {
      _stopDeliveryPolling();
      _streamReconnectTimer?.cancel();
      _streamCancelToken?.cancel();
      _streamSubscription?.cancel();
    });
    Future.microtask(loadWorkspace);
    return AiWorkspaceState(chatHistory: [_welcomeMessage()]);
  }

  Future<void> loadWorkspace() async {
    final request = ++_projectRequest;
    state = state.copyWith(isLoading: true, clearError: true);
    try {
      final results = await Future.wait([
        _repo.getProjects(),
        _repo.getExecutors(),
      ]);
      final projects = results[0] as List<CodeProject>;
      final executors = results[1] as List<CodeExecutor>;
      final projectId = _resolveProjectId(projects);
      final projectData = await _loadAllSessions();
      final sessions = projectData.$1;
      final tasks = projectData.$2;
      if (request != _projectRequest) return;
      final clearSelection = state.currentSession?.projectId != projectId;
      state = state.copyWith(
        isLoading: false,
        projects: projects,
        executors: executors,
        sessions: sessions,
        tasks: tasks,
        selectedProjectId: projectId,
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
      if (request != _projectRequest) return;
      state = state.copyWith(
        isLoading: false,
        errorMessage: '开发工作区加载失败：$error',
      );
    }
  }

  Future<void> selectProject(int projectId) async {
    if (projectId <= 0 || projectId == state.selectedProjectId) return;
    final request = ++_projectRequest;
    _stopDeliveryPolling();
    state = state.copyWith(
      isLoading: true,
      selectedProjectId: projectId,
      sessions: const [],
      tasks: const [],
      chatHistory: [_welcomeMessage()],
      currentStage: 'idle',
      recentOutput: '',
      previews: const [],
      timelineEvents: const [],
      errorSummary: '',
      changedFiles: const [],
      clearError: true,
      clearSession: true,
      clearTask: true,
      clearInstruction: true,
      clearApproval: true,
      clearDelivery: true,
      clearDeliveryError: true,
    );
    try {
      final projectData = await _loadProjectSessions(projectId);
      final sessions = projectData.$1;
      if (request != _projectRequest) return;
      state = state.copyWith(
        isLoading: false,
        sessions: sessions,
        tasks: projectData.$2,
      );
      if (sessions.isNotEmpty) await selectSession(sessions.first);
    } catch (error) {
      if (request != _projectRequest) return;
      state = state.copyWith(isLoading: false, errorMessage: '开发任务加载失败：$error');
    }
  }

  Future<AiDevSession> createSession({
    required int projectId,
    required String executorId,
    required String approvalPolicy,
    String title = '',
  }) async {
    state = state.copyWith(isActionLoading: true, clearError: true);
    try {
      final session = await _repo.createSession(
        projectId: projectId,
        executorId: executorId,
        approvalPolicy: approvalPolicy,
        title: title.trim(),
      );
      state = state.copyWith(
        isActionLoading: false,
        selectedProjectId: projectId,
        sessions: state.selectedProjectId == projectId
            ? state.sessions
            : const [],
      );
      await selectSession(session);
      return session;
    } catch (error) {
      state = state.copyWith(
        isActionLoading: false,
        errorMessage: '创建开发会话失败：$error',
      );
      rethrow;
    }
  }

  Future<void> selectSession(AiDevSession session) async {
    _stopDeliveryPolling();
    await _stopConversationStream();
    state = state.copyWith(
      currentSession: session,
      currentStage: session.currentStage,
      chatHistory: [_welcomeMessage()],
      previews: const [],
      timelineEvents: const [],
      errorSummary: '',
      changedFiles: const [],
      clearTask: true,
      clearInstruction: true,
      clearApproval: true,
      clearDelivery: true,
      clearDeliveryError: true,
    );
    await refreshCurrentSession(showLoading: true);
    _connectConversationStream(session.id);
  }

  Future<void> refreshCurrentSession({bool showLoading = false}) async {
    final sessionId = state.currentSession?.id;
    if (sessionId == null || _refreshingSessionId == sessionId) return;
    _refreshingSessionId = sessionId;
    if (showLoading) {
      state = state.copyWith(isLoading: true, clearError: true);
    }
    try {
      final results = await Future.wait<Object>([
        _repo.getSessionState(sessionId),
        _repo.getSessionHistory(sessionId, page: 1, limit: 1),
        _loadDelivery(sessionId),
      ]);
      if (state.currentSession?.id != sessionId) return;
      final sessionState = results[0] as AiSessionStateInfo;
      final history = results[1] as CodeSessionHistory;
      final (delivery, deliveryError) =
          results[2] as (CodeDeliveryJob?, String?);
      final historyMessages = codeHistoryChatMessages(history.messages);
      final messages = _mergeHistoryMessages(
        historyMessages,
        state.chatHistory,
      );
      final sessions = state.sessions
          .map(
            (item) => item.id == sessionState.session.id
                ? sessionState.session
                : item,
          )
          .toList();
      state = state.copyWith(
        isLoading: false,
        clearError: true,
        sessions: sessions,
        chatHistory: messages,
        currentSession: sessionState.session,
        currentTask: sessionState.currentTask,
        latestInstruction: sessionState.latestInstruction,
        pendingApproval: sessionState.pendingApproval,
        currentStage: sessionState.currentStage,
        recentOutput: sessionState.recentOutput,
        previews: sessionState.previews,
        timelineEvents: sessionState.timelineEvents,
        errorSummary: sessionState.errorSummary,
        changedFiles: sessionState.changedFiles,
        delivery: delivery,
        clearDelivery: delivery == null,
        deliveryError: deliveryError,
        clearDeliveryError: deliveryError == null,
        clearTask: sessionState.currentTask == null,
        clearInstruction: sessionState.latestInstruction == null,
        clearApproval: sessionState.pendingApproval == null,
      );
      _syncDeliveryPolling();
    } catch (error) {
      if (state.currentSession?.id != sessionId) return;
      state = state.copyWith(isLoading: false, errorMessage: '会话状态刷新失败：$error');
    } finally {
      if (_refreshingSessionId == sessionId) {
        _refreshingSessionId = null;
      }
    }
  }

  Future<bool> sendMessage(
    String text, {
    CodeInstructionOptions options = const CodeInstructionOptions(),
  }) async {
    final content = text.trim();
    if (content.isEmpty || state.isSending) return false;
    final session = state.currentSession;
    if (session == null) {
      state = state.copyWith(errorMessage: '请先创建或选择一个开发会话');
      return false;
    }
    final userMessage = ChatMessage(
      id: 'local-${DateTime.now().microsecondsSinceEpoch}',
      text: content,
      isUser: true,
      timestamp: DateTime.now(),
    );
    state = state.copyWith(
      isSending: true,
      clearError: true,
      chatHistory: [...state.chatHistory, userMessage],
    );
    try {
      final result = await _repo.sendAiCommand(
        sessionId: session.id,
        command: content,
        autoPreview: options.autoPreview,
        requireApproval: options.requireApproval,
      );
      state = state.copyWith(
        currentSession: result.session,
        currentTask: result.task,
        latestInstruction: result.instruction,
        pendingApproval: result.approval,
        currentStage: result.approval == null
            ? 'instruction_queued'
            : 'awaiting_approval',
        clearApproval: result.approval == null,
      );
      await refreshCurrentSession();
      return true;
    } catch (error) {
      state = state.copyWith(errorMessage: '指令发送失败：$error');
      return false;
    } finally {
      state = state.copyWith(isSending: false);
    }
  }

  Future<bool> updateSessionTitle(String title) async {
    final session = state.currentSession;
    if (session == null || title.trim().isEmpty || state.isActionLoading) {
      return false;
    }
    state = state.copyWith(isActionLoading: true, clearError: true);
    try {
      final updated = await _repo.updateSessionTitle(session.id, title);
      _replaceSession(updated);
      state = state.copyWith(isActionLoading: false);
      return true;
    } catch (error) {
      state = state.copyWith(
        isActionLoading: false,
        errorMessage: '会话名称更新失败：$error',
      );
      return false;
    }
  }

  Future<bool> updateApprovalPolicy(String policy) async {
    final session = state.currentSession;
    if (session == null || policy.trim().isEmpty || state.isActionLoading) {
      return false;
    }
    state = state.copyWith(isActionLoading: true, clearError: true);
    try {
      final updated = await _repo.updateApprovalPolicy(session.id, policy);
      _replaceSession(updated);
      state = state.copyWith(isActionLoading: false);
      return true;
    } catch (error) {
      state = state.copyWith(
        isActionLoading: false,
        errorMessage: '审批策略更新失败：$error',
      );
      return false;
    }
  }

  Future<bool> updateSessionModel({
    String? model,
    String? reasoningEffort,
  }) async {
    final session = state.currentSession;
    if (session == null || state.isActionLoading) return false;
    state = state.copyWith(isActionLoading: true, clearError: true);
    try {
      final updated = await _repo.updateSessionModel(
        session.id,
        model: model,
        reasoningEffort: reasoningEffort,
      );
      _replaceSession(updated);
      state = state.copyWith(isActionLoading: false);
      return true;
    } catch (error) {
      state = state.copyWith(
        isActionLoading: false,
        errorMessage: '会话模型更新失败：$error',
      );
      return false;
    }
  }

  void _replaceSession(AiDevSession updated) {
    state = state.copyWith(
      currentSession: updated,
      sessions: state.sessions
          .map((item) => item.id == updated.id ? updated : item)
          .toList(),
    );
  }

  List<ChatMessage> _mergeHistoryMessages(
    List<ChatMessage> history,
    List<ChatMessage> current,
  ) {
    if (history.isEmpty) return current;
    final result = [...history];
    for (final message in current) {
      if (!message.id.startsWith('local-') &&
          !message.id.startsWith('stream-')) {
        continue;
      }
      final duplicate = result.any(
        (item) => item.isUser == message.isUser && item.text == message.text,
      );
      if (!duplicate && message.text.trim().isNotEmpty) result.add(message);
    }
    return result;
  }

  void _connectConversationStream(int sessionId) {
    if (_streamSuspended ||
        _streamConnecting ||
        (_streamSessionId == sessionId && _streamSubscription != null)) {
      return;
    }
    _streamSessionId = sessionId;
    _streamConnecting = true;
    _streamCancelToken?.cancel();
    _streamCancelToken = CancelToken();
    _streamSubscription?.cancel();
    _streamSubscription = null;
    _repo
        .openConversationStream(sessionId, cancelToken: _streamCancelToken)
        .then((body) {
          if (_streamSessionId != sessionId) return;
          _streamConnecting = false;
          final parser = CodeConversationStreamParser();
          _streamSubscription = body.stream
              .cast<List<int>>()
              .transform(utf8.decoder)
              .listen(
                (chunk) {
                  // 服务端连上就先推快照，收到数据说明流已恢复
                  if (_streamReconnectAttempts != 0 ||
                      state.isStreamReconnecting) {
                    _streamReconnectAttempts = 0;
                    state = state.copyWith(isStreamReconnecting: false);
                  }
                  for (final event in parser.add(chunk)) {
                    _applyConversationStreamEvent(sessionId, event);
                  }
                },
                onError: (_) => _scheduleStreamReconnect(sessionId),
                onDone: () => _scheduleStreamReconnect(sessionId),
                cancelOnError: true,
              );
        })
        .catchError((_) {
          _streamConnecting = false;
          _scheduleStreamReconnect(sessionId);
        });
  }

  void _scheduleStreamReconnect(int sessionId) {
    if (_streamSuspended ||
        _streamSessionId != sessionId ||
        _streamReconnectTimer != null) {
      return;
    }
    _streamSubscription = null;
    final delay = codeStreamReconnectDelay(_streamReconnectAttempts++);
    state = state.copyWith(isStreamReconnecting: true);
    _streamReconnectTimer = Timer(delay, () {
      _streamReconnectTimer = null;
      if (_streamSessionId != sessionId) return;
      refreshCurrentSession().whenComplete(
        () => _connectConversationStream(sessionId),
      );
    });
  }

  Future<void> _stopConversationStream() async {
    _streamSessionId = null;
    _streamReconnectTimer?.cancel();
    _streamReconnectTimer = null;
    _streamCancelToken?.cancel();
    _streamCancelToken = null;
    await _streamSubscription?.cancel();
    _streamSubscription = null;
    _streamConnecting = false;
    _streamReconnectAttempts = 0;
    state = state.copyWith(
      isStreaming: false,
      isStreamReconnecting: false,
      streamActivityKind: '',
      streamActivity: '',
    );
  }

  Future<void> suspendConversationStream() async {
    _streamSuspended = true;
    await _stopConversationStream();
  }

  Future<void> resumeConversationStream() async {
    _streamSuspended = false;
    final sessionId = state.currentSession?.id;
    if (sessionId == null) return;
    await refreshCurrentSession();
    if (!_streamSuspended && state.currentSession?.id == sessionId) {
      _connectConversationStream(sessionId);
    }
  }

  void _applyConversationStreamEvent(
    int sessionId,
    CodeConversationStreamEvent event,
  ) {
    if (state.currentSession?.id != sessionId) return;
    final messages = [...state.chatHistory];
    final streamId = event.runId > 0
        ? 'stream-${event.runId}'
        : 'stream-current';
    final index = messages.indexWhere((message) => message.id == streamId);
    final isDone = event.type == 'done';
    if (event.type == 'delta') {
      final previous = index >= 0 ? messages[index].text : '';
      final message = ChatMessage(
        id: streamId,
        text: '$previous${event.content}',
        isUser: false,
        timestamp: DateTime.now(),
      );
      if (index >= 0) {
        messages[index] = message;
      } else {
        messages.add(message);
      }
    } else if ((event.type == 'snapshot' || isDone) &&
        event.content.isNotEmpty) {
      final message = ChatMessage(
        id: streamId,
        text: event.content,
        isUser: false,
        timestamp: DateTime.now(),
      );
      if (index >= 0) {
        messages[index] = message;
      } else {
        messages.add(message);
      }
    }
    // 审批状态变化时必须立刻刷会话：pendingApproval 只有 refresh 才会拉到，
    // 原先要等用户手动刷新，审批卡片才会冒出来——AI 卡住、界面也不解释。
    final approvalChanged =
        event.activityKind.startsWith('approval_') &&
        event.activityKind != state.streamActivityKind;
    state = state.copyWith(
      chatHistory: messages,
      isStreaming: !isDone && event.status == 'running',
      currentStage: event.status == 'running' ? 'running' : state.currentStage,
      recentOutput: event.content.isEmpty ? state.recentOutput : event.content,
      streamActivityKind: event.activityKind,
      streamActivity: event.activity,
    );
    if (isDone || approvalChanged) unawaited(refreshCurrentSession());
  }

  Future<void> decideApproval({
    required bool approved,
    String reason = '',
  }) async {
    final approval = state.pendingApproval;
    if (approval == null || state.isActionLoading) return;
    state = state.copyWith(isActionLoading: true, clearError: true);
    try {
      if (approved) {
        await _repo.approveApproval(approval.id, reason: reason);
      } else {
        await _repo.rejectApproval(approval.id, reason: reason);
      }
      state = state.copyWith(isActionLoading: false, clearApproval: true);
      await refreshCurrentSession();
    } catch (error) {
      state = state.copyWith(
        isActionLoading: false,
        errorMessage: '审批处理失败：$error',
      );
    }
  }

  Future<bool> stopCurrentSession() async {
    final sessionId = state.currentSession?.id;
    if (sessionId == null || state.isActionLoading) return false;
    state = state.copyWith(isActionLoading: true, clearError: true);
    try {
      await _repo.stopSession(sessionId);
      state = state.copyWith(isActionLoading: false);
      await refreshCurrentSession();
      return true;
    } catch (error) {
      state = state.copyWith(
        isActionLoading: false,
        errorMessage: error.toString(),
      );
      return false;
    }
  }

  Future<CodeDeliveryReview?> loadDeliveryReview() async {
    final sessionId = state.currentSession?.id;
    if (sessionId == null || state.isDeliveryLoading) return null;
    state = state.copyWith(isDeliveryLoading: true, clearDeliveryError: true);
    try {
      final review = await _repo.getDeliveryReview(sessionId);
      state = state.copyWith(isDeliveryLoading: false);
      return review;
    } catch (error) {
      state = state.copyWith(
        isDeliveryLoading: false,
        deliveryError: error.toString(),
      );
      return null;
    }
  }

  Future<bool> startDelivery({required String reviewRevision}) async {
    final sessionId = state.currentSession?.id;
    if (sessionId == null || state.isDeliveryLoading) return false;
    state = state.copyWith(
      isDeliveryLoading: true,
      clearDeliveryError: true,
      clearError: true,
    );
    try {
      final delivery = await _repo.startDelivery(
        sessionId,
        reviewRevision: reviewRevision,
      );
      state = state.copyWith(delivery: delivery, isDeliveryLoading: false);
      await refreshCurrentSession();
      return true;
    } catch (error) {
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
    if (state.currentSession?.id != sessionId) return;
    final (delivery, error) = await _loadDelivery(sessionId);
    if (state.currentSession?.id != sessionId) return;
    // 拉取失败（网络抖动）保留当前进度，下一轮再试
    if (error == null) {
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
    try {
      return (await _repo.getDelivery(sessionId), null);
    } catch (error) {
      return (null, error.toString());
    }
  }

  void clearError() {
    state = state.copyWith(clearError: true);
  }

  ChatMessage _welcomeMessage() {
    return ChatMessage(
      id: 'welcome',
      text: '选择已有会话，或从开发项目创建会话，然后发送开发指令。',
      isUser: false,
      timestamp: DateTime.now(),
    );
  }

  int? _resolveProjectId(List<CodeProject> projects) {
    final selected = state.selectedProjectId;
    if (projects.any((project) => project.id == selected)) return selected;
    final current = state.currentSession?.projectId;
    if (projects.any((project) => project.id == current)) return current;
    return projects.firstOrNull?.id;
  }

  Future<(List<AiDevSession>, List<CodeTask>)> _loadProjectSessions(
    int projectId,
  ) async {
    final results = await Future.wait([
      _repo.getSessions(projectId: projectId),
      _repo.getTasks(projectId: projectId),
    ]);
    final sessions = results[0] as List<AiDevSession>;
    final tasks = results[1] as List<CodeTask>;
    return (
      alignCodeTaskSessions(sessions, tasks, projectId: projectId),
      tasks.where((task) => task.agentName != 'terminal').toList(),
    );
  }

  Future<(List<AiDevSession>, List<CodeTask>)> _loadAllSessions() async {
    final results = await Future.wait([_repo.getSessions(), _repo.getTasks()]);
    final sessions = results[0] as List<AiDevSession>;
    final tasks = results[1] as List<CodeTask>;
    return (
      sessions.where((session) => session.agentName != 'terminal').toList(),
      tasks.where((task) => task.agentName != 'terminal').toList(),
    );
  }
}
