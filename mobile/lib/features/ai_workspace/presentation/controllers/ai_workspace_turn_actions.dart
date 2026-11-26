part of 'ai_workspace_controller.dart';

/// Instruction sending, retry, approval decision, and stop lifecycle for active sessions.
mixin _AiWorkspaceTurnActions
    on Notifier<AiWorkspaceState>, _AiWorkspaceStream, _AiWorkspaceConvoyAndQueue {
  int? _activeSendSessionId;
  int _activeSendTerminalSeq = -1;

  void _resetRefreshTracking();

  @override
  Future<bool> sendMessage(
    String text, {
    CodeInstructionOptions options = const CodeInstructionOptions(),
    String? imageDataUrl,
    List<String> attachedFiles = const [],
    List<LocalPickedDocument> localDocuments = const [],
    NodeSkillSelection? skillSelection,
    bool bypassQueue = false,
    bool useComposerSkill = true,
  }) async {
    final nodeRequest = NodeRequestGuard(_repo);
    final rawText = text.trim();
    final hasImage = imageDataUrl != null && imageDataUrl.trim().isNotEmpty;
    final hasDocuments = localDocuments.isNotEmpty;
    final effectiveSkill = skillSelection ?? (useComposerSkill ? state.selectedSkill : null);
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
      text: bubbleText.isNotEmpty
          ? bubbleText
          : (hasImage
                ? ''
                : (effectiveSkill != null
                      ? '[@Skill: ${effectiveSkill.name}]'
                      : '')),
      isUser: true,
      timestamp: now,
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
    final previousSession = session;
    final previousStage = state.currentStage;
    final wasStreamingBeforeSend = state.isStreaming;
    final sendTerminalSeq = _streamTerminalSeq;
    _activeSendSessionId = session.id;
    _activeSendTerminalSeq = sendTerminalSeq;
    _clearSessionStopped(session.id);
    _clearRecentTerminal(session.id);
    _clearLiveSseRunning(session.id);
    final runningSession = applyPromptAutoTitle(session, content, hasImage: hasImage, now: now)
        .copyWith(status: 'running', currentStage: 'running', updatedAt: now);
    state = state.copyWith(
      isSending: true, isStreaming: true, currentStage: 'running',
      currentSession: runningSession,
      sessions: upsertWorkspaceSession(state.sessions, runningSession),
      tasks: syncWorkspaceTasksWithSession(state.tasks, runningSession, stageOverride: 'running'),
      turnChangedFiles: const [], turnChangedFileStats: const [],
      turnDiffAdditions: 0, turnDiffDeletions: 0, todos: const [],
      clearError: true, clearSelectedSkill: useComposerSkill,
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
      final completedDuringSend = _streamTerminalSeq != sendTerminalSeq;
      if (_activeSendSessionId == session.id) {
        _activeSendSessionId = null;
      }
      if (completedDuringSend) {
        nodeRequest.repository.clearSessionBusyGrace(session.id);
      }
      final resolvedStage = completedDuringSend
          ? (state.currentStage.isNotEmpty &&
                    state.currentStage != 'running' &&
                    state.currentStage != 'executing' &&
                    state.currentStage != 'instruction_queued'
                ? state.currentStage
                : 'idle')
          : (result.approval == null ? 'instruction_queued' : 'awaiting_approval');
      final resolvedStatus = completedDuringSend
          ? (resolvedStage == 'failed' ? 'failed' : 'active')
          : result.session.status;
      final postSession = applyPromptAutoTitle(result.session, content, hasImage: hasImage, now: now)
          .copyWith(status: resolvedStatus, currentStage: resolvedStage);
      final postTask = syncTaskSummaryTitle(result.task, postSession);
      if (isPlaceholderSessionTitle(result.session.title) &&
          !isPlaceholderSessionTitle(postSession.title)) {
        unawaited(
          nodeRequest.repository
              .updateSessionTitle(session.id, postSession.title)
              .catchError((_) => postSession),
        );
      }
      if (state.currentSession?.id == session.id) {
        state = state.copyWith(
          isSending: false,
          isStreaming: !completedDuringSend && result.approval == null,
          currentSession: postSession,
          sessions: upsertWorkspaceSession(state.sessions, postSession),
          tasks: syncWorkspaceTasksWithSession(
            state.tasks, postSession, currentTask: postTask, stageOverride: resolvedStage,
          ),
          currentTask: postTask,
          latestInstruction: result.instruction,
          pendingApproval: completedDuringSend ? null : result.approval,
          currentStage: resolvedStage,
          clearApproval: completedDuringSend || result.approval == null,
        );
        _syncStreamQuietWatchdog();
      } else {
        state = state.copyWith(isSending: false);
      }
      _notifyTaskCenterSession(postSession, currentStage: resolvedStage);
      if (!completedDuringSend && result.approval != null) {
        unawaited(ref.read(codeAttentionControllerProvider.notifier).reloadSilently());
      }
      if (state.currentSession?.id == session.id) {
        await refreshCurrentSession(trustServerState: completedDuringSend);
      }
      return true;
    } catch (error) {
      if (_activeSendSessionId == session.id) {
        _activeSendSessionId = null;
      }
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      if (state.currentSession?.id == session.id) {
        final revertedStage =
            (previousStage.isEmpty ||
                previousStage == 'running' ||
                previousStage == 'executing' ||
                previousStage == 'instruction_queued')
            ? 'idle'
            : previousStage;
        final revertedStatus =
            (previousSession.status == 'running' ||
                previousSession.status == 'busy' ||
                previousSession.status.isEmpty)
            ? 'active'
            : previousSession.status;
        final revertedSession = state.currentSession!.copyWith(
          status: revertedStatus,
          currentStage: revertedStage,
        );
        nodeRequest.repository.markSessionStage(session.id, revertedStage);
        _cancelStreamQuietWatchdog();
        state = state.copyWith(
          isSending: false,
          isStreaming: false,
          currentStage: revertedStage,
          currentSession: revertedSession,
          sessions: upsertWorkspaceSession(state.sessions, revertedSession),
          tasks: syncWorkspaceTasksWithSession(
            state.tasks,
            revertedSession,
            currentTask: state.currentTask,
            stageOverride: revertedStage,
          ),
          chatHistory: state.chatHistory
              .where(
                (m) =>
                    m.id != userMessage.id &&
                    (m.id != 'stream-current' || m.text.trim().isNotEmpty),
              )
              .toList(),
          selectedSkill:
              useComposerSkill && skillSelection == null && state.selectedSkill == null
              ? effectiveSkill
              : state.selectedSkill,
          errorMessage: '指令发送失败：$error',
        );
        _notifyTaskCenterSession(revertedSession, currentStage: revertedStage);
        if (wasStreamingBeforeSend) {
          unawaited(refreshCurrentSession(trustServerState: true));
        }
      } else {
        state = state.copyWith(
          isSending: false,
          errorMessage: '指令发送失败：$error',
        );
      }
      return false;
    } finally {
      if (_activeSendSessionId == session.id) {
        _activeSendSessionId = null;
      }
      if (ref.mounted && nodeRequest.isCurrent(_repo) && state.isSending) {
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
      final previousSession = session;
      final previousStage = state.currentStage;
      final retryTerminalSeq = _streamTerminalSeq;
      _clearSessionStopped(session.id);
      _clearRecentTerminal(session.id);
      _clearLiveSseRunning(session.id);
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
        final completedDuringRetry = _streamTerminalSeq != retryTerminalSeq;
        if (completedDuringRetry) {
          nodeRequest.repository.clearSessionBusyGrace(session.id);
        }
        final resolvedStage = completedDuringRetry
            ? (state.currentStage.isNotEmpty &&
                      state.currentStage != 'running' &&
                      state.currentStage != 'executing' &&
                      state.currentStage != 'instruction_queued'
                  ? state.currentStage
                  : 'idle')
            : 'instruction_queued';
        if (state.currentSession?.id == session.id) {
          state = state.copyWith(
            isActionLoading: false,
            isStreaming: !completedDuringRetry,
            latestInstruction: retried,
            currentStage: resolvedStage,
          );
          _syncStreamQuietWatchdog();
          await refreshCurrentSession(trustServerState: completedDuringRetry);
        } else {
          state = state.copyWith(isActionLoading: false);
        }
        return true;
      } catch (error) {
        if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
        final revertedStage =
            (previousStage.isEmpty ||
                previousStage == 'running' ||
                previousStage == 'executing' ||
                previousStage == 'instruction_queued')
            ? 'idle'
            : previousStage;
        final revertedSession = state.currentSession?.id == session.id
            ? state.currentSession!.copyWith(
                status: previousSession.status.isEmpty
                    ? 'active'
                    : previousSession.status,
                currentStage: revertedStage,
              )
            : state.currentSession;
        nodeRequest.repository.markSessionStage(session.id, revertedStage);
        _cancelStreamQuietWatchdog();
        state = state.copyWith(
          isActionLoading: false,
          isStreaming: false,
          currentStage: state.currentSession?.id == session.id
              ? revertedStage
              : state.currentStage,
          currentSession: revertedSession,
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
      _recordTerminalTransition(sessionId);
      _resetRefreshTracking();
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
      await refreshCurrentSession(trustServerState: true);
      if (!_streamSuspended) _connectConversationStream(sessionId);
      unawaited(ref.read(aiWorkspaceTaskSyncBusProvider).refreshSilently());
      return true;
    } catch (error) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      state = state.copyWith(isActionLoading: false, errorMessage: error.toString());
      return false;
    }
  }
}
