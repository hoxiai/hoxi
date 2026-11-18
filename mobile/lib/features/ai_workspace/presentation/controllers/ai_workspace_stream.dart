part of 'ai_workspace_controller.dart';

/// Conversation SSE stream lifecycle, reconnection backoff, and event application.
mixin _AiWorkspaceStream on Notifier<AiWorkspaceState> {
  Timer? _streamReconnectTimer;
  StreamSubscription<String>? _streamSubscription;
  CancelToken? _streamCancelToken;
  int? _streamSessionId;
  bool _streamConnecting = false;
  bool _streamSuspended = false;
  int _streamReconnectAttempts = 0;
  final Map<int, int> _lastSseSeqBySessionId = <int, int>{};
  int? _stoppedSessionId;
  DateTime? _stoppedUntil;

  AiWorkspaceRepository get _repo;
  Future<void> refreshCurrentSession({bool showLoading = false});

  void _markSessionStopped(int sessionId) {
    _stoppedSessionId = sessionId;
    _stoppedUntil = DateTime.now().add(const Duration(seconds: 4));
  }

  void _clearSessionStopped(int sessionId) {
    if (_stoppedSessionId == sessionId) {
      _stoppedSessionId = null;
      _stoppedUntil = null;
    }
  }

  bool _isStopSuppressed(int sessionId) {
    if (_stoppedSessionId != sessionId || _stoppedUntil == null) return false;
    if (DateTime.now().isBefore(_stoppedUntil!)) return true;
    _clearSessionStopped(sessionId);
    return false;
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
    final resumeAfterSeq = _lastSseSeqBySessionId[sessionId];
    _repo
        .openConversationStream(
          sessionId,
          afterSeq: resumeAfterSeq,
          cancelToken: _streamCancelToken,
        )
        .then((body) {
          if (!ref.mounted ||
              _streamSuspended ||
              _streamSessionId != sessionId) {
            _streamConnecting = false;
            unawaited(body.stream.listen(null).cancel());
            return;
          }
          _streamConnecting = false;
          final parser = CodeConversationStreamParser(
            targetSessionIntId: sessionId,
            lastSeq: resumeAfterSeq ?? 0,
          );
          _streamSubscription = body.stream
              .cast<List<int>>()
              .transform(utf8.decoder)
              .listen(
                (chunk) {
                  if (!ref.mounted) return;
                  // 服务端连上就先推快照，收到数据说明流已恢复
                  if (_streamReconnectAttempts != 0 ||
                      state.isStreamReconnecting) {
                    _streamReconnectAttempts = 0;
                    state = state.copyWith(isStreamReconnecting: false);
                  }
                  final events = parser.add(chunk);
                  if (parser.lastSeq > 0) {
                    _lastSseSeqBySessionId[sessionId] = parser.lastSeq;
                  }
                  for (final event in events) {
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
          if (!ref.mounted) return;
          _scheduleStreamReconnect(sessionId);
        });
  }

  void _scheduleStreamReconnect(int sessionId) {
    if (!ref.mounted ||
        _streamSuspended ||
        _streamSessionId != sessionId ||
        _streamReconnectTimer != null) {
      return;
    }
    _streamSubscription = null;
    final delay = codeStreamReconnectDelay(_streamReconnectAttempts++);
    state = state.copyWith(isStreamReconnecting: true);
    _streamReconnectTimer = Timer(delay, () {
      _streamReconnectTimer = null;
      if (!ref.mounted || _streamSessionId != sessionId) return;
      refreshCurrentSession().whenComplete(() {
        if (!ref.mounted) return;
        _connectConversationStream(sessionId);
      });
    });
  }

  Future<void> _stopConversationStream() async {
    final nodeRequest = NodeRequestGuard(_repo);
    _streamSessionId = null;
    _streamReconnectTimer?.cancel();
    _streamReconnectTimer = null;
    _streamCancelToken?.cancel();
    _streamCancelToken = null;
    await _streamSubscription?.cancel();
    _streamSubscription = null;
    _streamConnecting = false;
    _streamReconnectAttempts = 0;
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) {
      return;
    }
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

  Future<void> onChatScreenExited() async {
    final onEngineeringTab =
        ref.read(mainScaffoldIndexProvider) ==
        MainScaffoldIndexController.engineeringIndex;
    final sessionActive =
        state.isStreaming ||
        state.currentStage == 'running' ||
        state.currentStage == 'executing' ||
        state.currentStage == 'instruction_queued' ||
        state.currentStage == 'awaiting_approval';
    if (!onEngineeringTab && !sessionActive) {
      await suspendConversationStream();
    }
  }

  void _notifyTaskCenterSession(
    AiDevSession session, {
    String? currentStage,
    AiSessionStateInfo? stateInfo,
  }) {
    if (!ref.mounted) return;
    ref
        .read(aiWorkspaceTaskSyncBusProvider)
        .notifySession(
          session,
          currentStage: currentStage,
          stateInfo: stateInfo,
        );
  }

  void syncFromTaskEntities(List<TaskEntity> taskEntities) {
    if (!ref.mounted) return;
    final activeId = (state.isStreaming || state.isSending)
        ? state.currentSession?.id
        : null;
    final synced = applyTaskEntitiesToWorkspace(
      sessions: state.sessions,
      tasks: state.tasks,
      currentSession: state.currentSession,
      taskEntities: taskEntities,
      activeStreamingSessionId: activeId,
    );
    if (identical(synced.sessions, state.sessions) &&
        identical(synced.tasks, state.tasks) &&
        identical(synced.current, state.currentSession)) {
      return;
    }
    state = state.copyWith(
      sessions: synced.sessions,
      tasks: synced.tasks,
      currentSession: synced.current,
      currentStage:
          synced.current != null && synced.current!.id == state.currentSession?.id
          ? synced.current!.currentStage
          : state.currentStage,
    );
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
    if (_isStopSuppressed(sessionId)) {
      if (event.type == 'done') {
        _clearSessionStopped(sessionId);
      } else if (event.type != 'session_updated' && event.type != 'error') {
        return;
      }
    }
    if (event.type == 'resync') {
      unawaited(refreshCurrentSession());
      return;
    }
    if (event.type == 'session_updated') {
      final nextTitle = event.content.trim();
      final current = state.currentSession;
      if (current != null &&
          nextTitle.isNotEmpty &&
          !isPlaceholderSessionTitle(nextTitle) &&
          nextTitle != current.title) {
        final updated = current.copyWith(
          title: nextTitle,
          currentTaskTitle: nextTitle,
          updatedAt: DateTime.now(),
        );
        final nextTask = syncTaskSummaryTitle(state.currentTask, updated);
        state = state.copyWith(
          currentSession: updated,
          currentTask: nextTask,
          sessions: upsertWorkspaceSession(state.sessions, updated),
          tasks: syncWorkspaceTasksWithSession(
            state.tasks,
            updated,
            currentTask: nextTask,
          ),
        );
        _notifyTaskCenterSession(updated);
      }
      return;
    }
    final startedExternalRun =
        !state.isSending && !state.isStreaming && event.status == 'running';
    final messages = [...state.chatHistory];
    final streamId = event.runId > 0
        ? 'stream-${event.runId}'
        : 'stream-current';
    var index = messages.indexWhere((message) => message.id == streamId);
    if (index < 0 && streamId != 'stream-current') {
      index = messages.indexWhere((message) => message.id == 'stream-current');
    }
    final previous = index >= 0 ? messages[index] : null;
    final session = state.currentSession;
    final defaultAgentLabel = [
      if (session != null && session.agentName.trim().isNotEmpty)
        session.agentName.trim(),
      if (session != null && session.providerModel.trim().isNotEmpty)
        session.providerModel.trim(),
    ].join(' · ');
    final agentLabel =
        (previous != null && previous.agentLabel.isNotEmpty)
            ? previous.agentLabel
            : defaultAgentLabel;
    final isDone = event.type == 'done';
    final isError = event.type == 'error';
    final isRunning =
        !isDone &&
        !isError &&
        event.status != 'completed' &&
        event.status != 'failed';
    final isToolOrSkillEvent =
        (event.activityKind == 'tool' || event.activityKind == 'skill') &&
        event.activity.trim().isNotEmpty;
    var archivedPreToolText = false;
    final nextToolSteps = () {
      var base = previous?.toolSteps ?? const <String>[];
      if (isToolOrSkillEvent) {
        final raw = event.activity.trim();
        final stepLabel =
            event.activityKind == 'tool' && event.content.trim().isNotEmpty
            ? event.content.trim()
            : (raw.startsWith('正在执行 ') ? raw.substring(5).trim() : raw);
        if (stepLabel.isNotEmpty) {
          final isTodo = stepLabel.toLowerCase().startsWith('todowrite');
          if (!isTodo && previous != null && previous.text.trim().isNotEmpty) {
            final note = CodeConversationStreamParser.formatStepNote(
              previous.text,
            );
            if (note.isNotEmpty && (base.isEmpty || base.last != note)) {
              base = <String>[...base, note];
            }
            archivedPreToolText = true;
          }
          if (base.isEmpty) return <String>[stepLabel];
          if (base.last == stepLabel) return base;
          if (stepLabel.startsWith('${base.last} · ')) {
            return <String>[...base.sublist(0, base.length - 1), stepLabel];
          }
          if (base.last.startsWith('$stepLabel · ')) {
            return base;
          }
          return <String>[...base, stepLabel];
        }
      }
      return base;
    }();

    void upsertMessage(ChatMessage msg) {
      if (index >= 0) {
        messages[index] = msg;
      } else {
        messages.add(msg);
        index = messages.length - 1;
      }
    }

    if (event.type == 'strip_user_echo') {
      if (previous != null &&
          event.content.isNotEmpty &&
          previous.text.startsWith(event.content)) {
        upsertMessage(
          previous.copyWith(
            text: previous.text.substring(event.content.length),
          ),
        );
      }
    } else if (event.type == 'delta') {
      upsertMessage(
        ChatMessage(
          id: streamId,
          text: '${previous?.text ?? ''}${event.content}',
          reasoning: previous?.reasoning ?? '',
          activity: event.activity,
          isStreaming: true,
          agentLabel: agentLabel,
          isUser: false,
          timestamp: previous?.timestamp ?? DateTime.now(),
          toolSteps: nextToolSteps,
        ),
      );
    } else if (event.type == 'activity' && event.activityKind == 'thinking') {
      upsertMessage(
        ChatMessage(
          id: streamId,
          text: previous?.text ?? '',
          reasoning: '${previous?.reasoning ?? ''}${event.content}',
          activity: event.activity.isNotEmpty ? event.activity : '正在深度思考...',
          isStreaming: true,
          agentLabel: agentLabel,
          isUser: false,
          timestamp: previous?.timestamp ?? DateTime.now(),
          toolSteps: nextToolSteps,
        ),
      );
    } else if ((event.type == 'snapshot' || isDone) &&
        event.content.isNotEmpty) {
      final startedAt = previous?.timestamp ?? DateTime.now();
      final elapsedMs = isDone
          ? DateTime.now().difference(startedAt).inMilliseconds
          : (previous?.durationMs ?? 0);
      upsertMessage(
        ChatMessage(
          id: streamId,
          text: event.content,
          reasoning: previous?.reasoning ?? '',
          activity: isDone ? '' : event.activity,
          isStreaming: !isDone,
          agentLabel: agentLabel,
          isUser: false,
          timestamp: isDone ? DateTime.now() : startedAt,
          durationMs: elapsedMs > 0 ? elapsedMs : 0,
          toolSteps: nextToolSteps,
        ),
      );
    } else if (isDone || isError) {
      if (previous != null) {
        if (previous.text.trim().isEmpty &&
            previous.reasoning.trim().isEmpty &&
            previous.toolSteps.isEmpty) {
          messages.removeAt(index);
        } else {
          final elapsedMs = DateTime.now()
              .difference(previous.timestamp)
              .inMilliseconds;
          messages[index] = previous.copyWith(
            isStreaming: false,
            activity: '',
            timestamp: DateTime.now(),
            durationMs: previous.durationMs > 0
                ? previous.durationMs
                : (elapsedMs > 0 ? elapsedMs : 0),
            toolSteps: nextToolSteps,
          );
        }
      }
    } else if (event.activity.isNotEmpty) {
      if (previous != null) {
        messages[index] = previous.copyWith(
          text: archivedPreToolText ? '' : previous.text,
          activity: event.activity,
          isStreaming: isRunning,
          toolSteps: nextToolSteps,
        );
      } else if (event.status == 'running') {
        upsertMessage(
          ChatMessage(
            id: streamId,
            text: '',
            activity: event.activity,
            isStreaming: true,
            agentLabel: agentLabel,
            isUser: false,
            timestamp: DateTime.now(),
            toolSteps: nextToolSteps,
          ),
        );
      }
    }
    // 审批或提问状态变化时必须立刻刷会话：pendingApproval 只有 refresh 才会拉到，
    // 原先要等用户手动刷新，审批/提问卡片才会冒出来——AI 卡住、界面也不解释。
    final approvalChanged =
        (event.activityKind.startsWith('approval_') ||
            event.activityKind.startsWith('question_')) &&
        event.activityKind != state.streamActivityKind;
    final nextStage = isDone
        ? 'idle'
        : (isError || event.status == 'failed'
              ? 'failed'
              : (event.status == 'awaiting_approval' ||
                        event.activityKind == 'approval_waiting' ||
                        event.activityKind == 'question_waiting'
                    ? 'awaiting_approval'
                    : (event.status == 'running'
                          ? 'running'
                          : state.currentStage)));
    final nextSessionStatus = nextStage == 'running'
        ? 'running'
        : (nextStage == 'failed' ? 'failed' : 'active');
    _repo.markSessionStage(sessionId, nextStage);
    final updatedSession = session?.copyWith(
      status: nextSessionStatus,
      currentStage: nextStage,
      updatedAt: DateTime.now(),
    );
    final stageChanged = nextStage != state.currentStage;
    state = state.copyWith(
      chatHistory: messages,
      isStreaming: !isDone && !isError && event.status == 'running',
      currentStage: nextStage,
      currentSession: updatedSession,
      sessions: updatedSession == null
          ? null
          : upsertWorkspaceSession(state.sessions, updatedSession),
      tasks: updatedSession == null
          ? null
          : syncWorkspaceTasksWithSession(
              state.tasks,
              updatedSession,
              currentTask: state.currentTask,
              stageOverride: nextStage,
            ),
      todos: isDone || isError ? const [] : null,
      recentOutput:
          event.type == 'delta' && event.content.isNotEmpty
              ? event.content
              : state.recentOutput,
      streamActivityKind: event.activityKind,
      streamActivity: event.activity,
      errorMessage: event.type == 'error' && event.message.isNotEmpty
          ? event.message
          : state.errorMessage,
    );
    if (updatedSession != null &&
        (stageChanged ||
            isDone ||
            isError ||
            approvalChanged ||
            startedExternalRun)) {
      _notifyTaskCenterSession(updatedSession, currentStage: nextStage);
    }
    final toolCompleted =
        event.activityKind == 'tool' && event.status == 'completed';
    if (isDone ||
        isError ||
        approvalChanged ||
        startedExternalRun ||
        toolCompleted) {
      unawaited(refreshCurrentSession());
    }
    if (approvalChanged || isDone || isError) {
      unawaited(
        ref.read(codeAttentionControllerProvider.notifier).reloadSilently(),
      );
    }
    // AI session rows are pushed via _notifyTaskCenterSession above.
    // On terminal / approval changes, only refresh the hot (recent 10m + active)
    // TaskCenter slice so other active sessions and history are not wiped.
    if (isDone || isError || approvalChanged) {
      unawaited(
        ref.read(aiWorkspaceTaskSyncBusProvider).refreshRecentSilently(),
      );
    }
  }
}
