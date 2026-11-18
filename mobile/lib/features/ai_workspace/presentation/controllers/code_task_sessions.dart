import '../../../../shared/models/task_entity.dart';
import '../../../../shared/models/task_status.dart';
import '../../models/ai_dev_session.dart';
import '../../models/chat_message.dart';
import '../../models/code_task.dart';

List<AiDevSession> alignCodeTaskSessions(
  List<AiDevSession> sessions,
  List<CodeTask> tasks, {
  required int projectId,
}) {
  final sessionsById = {for (final session in sessions) session.id: session};
  final terminalSessionIds = tasks
      .where((task) => task.agentName == 'terminal')
      .map((task) => task.sessionId)
      .toSet();
  final result = <AiDevSession>[];
  final added = <int>{};
  for (final task in tasks) {
    if (task.projectId != projectId || task.agentName == 'terminal') continue;
    final session = sessionsById[task.sessionId];
    if (session == null ||
        session.lastTaskId != task.id ||
        !added.add(session.id)) {
      continue;
    }
    result.add(session);
  }
  for (final session in sessions) {
    if (session.projectId != projectId ||
        session.agentName == 'terminal' ||
        session.lastTaskId != 0 ||
        terminalSessionIds.contains(session.id) ||
        !added.add(session.id)) {
      continue;
    }
    result.add(session);
  }
  return result;
}

String resolveCodeTaskStatus({
  required String sessionStatus,
  required String currentStage,
  String? taskStatus,
}) {
  final stage = currentStage.toLowerCase().trim();
  final status = sessionStatus.toLowerCase().trim();
  if (stage == 'running' ||
      stage == 'executing' ||
      stage == 'instruction_queued' ||
      status == 'running' ||
      status == 'busy') {
    return 'running';
  }
  if (stage == 'awaiting_approval') {
    return 'awaiting_approval';
  }
  if (stage == 'failed' ||
      stage == 'error' ||
      stage == 'initialization_failed' ||
      status == 'failed' ||
      status == 'error') {
    return 'failed';
  }
  if (stage == 'idle' || stage == 'completed' || stage == 'preview_ready') {
    return 'completed';
  }
  return (taskStatus != null && taskStatus.isNotEmpty)
      ? taskStatus
      : sessionStatus;
}

List<AiDevSession> upsertWorkspaceSession(
  List<AiDevSession> sessions,
  AiDevSession updated,
) {
  if (updated.agentName == 'terminal') return sessions;
  final index = sessions.indexWhere((item) => item.id == updated.id);
  if (index >= 0) {
    final existing = sessions[index];
    final next = List<AiDevSession>.of(sessions);
    next[index] = updated.subagents.isEmpty && existing.subagents.isNotEmpty
        ? updated.copyWith(subagents: existing.subagents)
        : updated;
    return next;
  }
  if (updated.isChildSession) {
    final parentIdx = sessions.indexWhere(
      (item) =>
          item.id == updated.parentSessionId ||
          item.subagents.any((sub) => sub.id == updated.id),
    );
    if (parentIdx >= 0) {
      final parent = sessions[parentIdx];
      final subIdx = parent.subagents.indexWhere((s) => s.id == updated.id);
      final nextSubagents = List<AiDevSession>.of(parent.subagents);
      if (subIdx >= 0) {
        nextSubagents[subIdx] = updated;
      } else {
        nextSubagents.add(updated);
      }
      final next = List<AiDevSession>.of(sessions);
      next[parentIdx] = parent.copyWith(subagents: nextSubagents);
      return next;
    }
    return sessions;
  }
  return <AiDevSession>[updated, ...sessions];
}

List<CodeTask> syncWorkspaceTasksWithSession(
  List<CodeTask> tasks,
  AiDevSession session, {
  AiTaskSummary? currentTask,
  String? stageOverride,
  bool insertIfNonEmpty = true,
}) {
  if (session.agentName == 'terminal' || session.isChildSession) return tasks;
  final effectiveStage = stageOverride ?? session.currentStage;
  final effectiveStatus = resolveCodeTaskStatus(
    sessionStatus: session.status,
    currentStage: effectiveStage,
    taskStatus: currentTask?.status,
  );
  final preferredTitle = resolveSessionDisplayTitle(
    session: session,
    taskTitle: currentTask?.title,
  );
  final now = session.updatedAt ?? DateTime.now();
  var matched = false;
  final updatedList = tasks.map((task) {
    if (task.sessionId == session.id ||
        (currentTask != null && currentTask.id > 0 && task.id == currentTask.id)) {
      matched = true;
      final resolvedTaskTitle = !isPlaceholderSessionTitle(preferredTitle)
          ? preferredTitle
          : (!isPlaceholderSessionTitle(task.title)
                ? task.title
                : (preferredTitle.isNotEmpty ? preferredTitle : task.title));
      final hasSessionDiff =
          session.diffAdditions > 0 || session.diffDeletions > 0;
      return task.copyWith(
        id: currentTask != null && currentTask.id > 0 ? currentTask.id : task.id,
        title: resolvedTaskTitle,
        agentName: session.agentName.isNotEmpty
            ? session.agentName
            : task.agentName,
        providerModel: session.providerModel.isNotEmpty
            ? session.providerModel
            : task.providerModel,
        diffAdditions: hasSessionDiff
            ? session.diffAdditions
            : task.diffAdditions,
        diffDeletions: hasSessionDiff
            ? session.diffDeletions
            : task.diffDeletions,
        status: effectiveStatus,
        updatedAt: now,
      );
    }
    return task;
  }).toList();
  if (!matched && insertIfNonEmpty && tasks.isNotEmpty) {
    final taskId = (currentTask != null && currentTask.id > 0)
        ? currentTask.id
        : (session.lastTaskId > 0 ? session.lastTaskId : session.id);
    return <CodeTask>[
      CodeTask(
        id: taskId,
        sessionId: session.id,
        projectId: session.projectId,
        title: preferredTitle.isNotEmpty ? preferredTitle : session.title,
        agentName: session.agentName,
        providerModel: session.providerModel,
        diffAdditions: session.diffAdditions,
        diffDeletions: session.diffDeletions,
        workDir: session.workDir,
        status: effectiveStatus,
        createdAt: session.createdAt ?? now,
        updatedAt: now,
      ),
      ...updatedList,
    ];
  }
  return updatedList;
}

AiTaskSummary? syncTaskSummaryTitle(AiTaskSummary? task, AiDevSession session) {
  if (task == null) return null;
  final rawTitle = session.title.trim();
  final nextTitle = rawTitle.isEmpty ||
          (isPlaceholderSessionTitle(rawTitle) &&
              !isPlaceholderSessionTitle(task.title))
      ? task.title
      : rawTitle;
  final nextStatus = resolveCodeTaskStatus(
    sessionStatus: session.status,
    currentStage: session.currentStage,
    taskStatus: task.status,
  );
  if (task.title == nextTitle && task.status == nextStatus) return task;
  return AiTaskSummary(
    id: task.id,
    title: nextTitle,
    status: nextStatus,
    workDir: task.workDir,
    agentName: task.agentName,
  );
}

({List<AiDevSession> sessions, List<CodeTask> tasks, AiDevSession? current})
applyTaskEntitiesToWorkspace({
  required List<AiDevSession> sessions,
  required List<CodeTask> tasks,
  required AiDevSession? currentSession,
  required List<TaskEntity> taskEntities,
  int? activeStreamingSessionId,
}) {
  if (sessions.isEmpty && tasks.isEmpty) {
    return (sessions: sessions, tasks: tasks, current: currentSession);
  }
  final aiBySessionId = <int, TaskEntity>{};
  for (final entity in taskEntities) {
    if (!entity.id.startsWith('aiSession:')) continue;
    final sid = int.tryParse(entity.id.substring('aiSession:'.length));
    if (sid != null && sid > 0) {
      aiBySessionId[sid] = entity;
    }
  }
  if (aiBySessionId.isEmpty) {
    return (sessions: sessions, tasks: tasks, current: currentSession);
  }
  var nextSessions = sessions;
  var nextTasks = tasks;
  var nextCurrent = currentSession;
  for (final entry in aiBySessionId.entries) {
    final sid = entry.key;
    if (sid == activeStreamingSessionId) continue;
    final entity = entry.value;
    final stageFromMeta = (entity.meta['currentStage'] ?? '').trim();
    final nextStage = entity.requiresAttention
        ? 'awaiting_approval'
        : (stageFromMeta.isNotEmpty
              ? stageFromMeta
              : (entity.status == TaskStatus.running
                    ? 'running'
                    : (entity.status == TaskStatus.failed ? 'failed' : 'idle')));
    final nextStatus = entity.status == TaskStatus.running
        ? 'running'
        : (entity.status == TaskStatus.failed ? 'failed' : 'active');
    final sIdx = nextSessions.indexWhere((s) => s.id == sid);
    if (sIdx >= 0) {
      final existing = nextSessions[sIdx];
      final entityTitle = entity.title.trim();
      final nextTitle = (!isPlaceholderSessionTitle(entityTitle) &&
              isPlaceholderSessionTitle(existing.title))
          ? entityTitle
          : existing.title;
      final nextTaskTitle = (!isPlaceholderSessionTitle(entityTitle) &&
              isPlaceholderSessionTitle(existing.currentTaskTitle))
          ? entityTitle
          : (!isPlaceholderSessionTitle(nextTitle) &&
                  isPlaceholderSessionTitle(existing.currentTaskTitle))
          ? nextTitle
          : existing.currentTaskTitle;
      final metaModel = (entity.meta['providerModel'] ?? '').trim();
      final metaAdd =
          int.tryParse((entity.meta['diffAdditions'] ?? '').trim()) ?? 0;
      final metaDel =
          int.tryParse((entity.meta['diffDeletions'] ?? '').trim()) ?? 0;
      final hasMetaDiff = metaAdd > 0 || metaDel > 0;
      final updatedSession = existing.copyWith(
        title: nextTitle,
        currentTaskTitle: nextTaskTitle,
        providerModel: existing.providerModel.isNotEmpty
            ? existing.providerModel
            : metaModel,
        diffAdditions: hasMetaDiff ? metaAdd : existing.diffAdditions,
        diffDeletions: hasMetaDiff ? metaDel : existing.diffDeletions,
        status: nextStatus,
        currentStage: nextStage,
        updatedAt: entity.updatedAt ?? existing.updatedAt,
      );
      nextSessions = List<AiDevSession>.of(nextSessions)..[sIdx] = updatedSession;
      nextTasks = syncWorkspaceTasksWithSession(
        nextTasks,
        updatedSession,
        stageOverride: nextStage,
        insertIfNonEmpty: false,
      );
      if (nextCurrent?.id == sid) {
        nextCurrent = updatedSession;
      }
    }
  }
  return (sessions: nextSessions, tasks: nextTasks, current: nextCurrent);
}

({List<AiDevSession> sessions, List<CodeTask> tasks})
mergeRecentWorkspaceSessionsAndTasks({
  required List<AiDevSession> existingSessions,
  required List<CodeTask> existingTasks,
  required List<AiDevSession> recentSessions,
  required List<CodeTask> recentTasks,
  int? activeStreamingSessionId,
}) {
  final filteredRecentSessions =
      recentSessions.where((s) => s.agentName != 'terminal').toList();
  final filteredRecentTasks =
      recentTasks.where((t) => t.agentName != 'terminal').toList();

  if (existingSessions.isEmpty && existingTasks.isEmpty) {
    return (sessions: filteredRecentSessions, tasks: filteredRecentTasks);
  }

  final recentSessionById = {for (final s in filteredRecentSessions) s.id: s};
  final existingSessionById = {for (final s in existingSessions) s.id: s};

  AiDevSession protectSession(AiDevSession s) {
    if (activeStreamingSessionId != null && s.id == activeStreamingSessionId) {
      return s.copyWith(status: 'running', currentStage: 'running');
    }
    return s;
  }

  final mergedSessionsMap = <int, AiDevSession>{};
  for (final s in filteredRecentSessions) {
    final prev = existingSessionById[s.id];
    final nextTitle = (prev != null &&
            !isPlaceholderSessionTitle(prev.title) &&
            isPlaceholderSessionTitle(s.title))
        ? prev.title
        : s.title;
    final nextModel = s.providerModel.isNotEmpty
        ? s.providerModel
        : (prev?.providerModel ?? '');
    final hasDiff = s.diffAdditions > 0 || s.diffDeletions > 0;
    mergedSessionsMap[s.id] = protectSession(
      s.copyWith(
        title: nextTitle,
        currentTaskTitle: nextTitle,
        providerModel: nextModel,
        subagents: s.subagents.isNotEmpty
            ? s.subagents
            : (prev?.subagents ?? const []),
        diffAdditions: hasDiff ? s.diffAdditions : (prev?.diffAdditions ?? 0),
        diffDeletions: hasDiff ? s.diffDeletions : (prev?.diffDeletions ?? 0),
      ),
    );
  }

  for (final old in existingSessions) {
    if (old.agentName == 'terminal') continue;
    if (mergedSessionsMap.containsKey(old.id)) continue;
    if (activeStreamingSessionId != null && old.id == activeStreamingSessionId) {
      mergedSessionsMap[old.id] = protectSession(old);
      continue;
    }
    final wasRunning = old.status == 'running' ||
        old.status == 'busy' ||
        old.currentStage == 'running' ||
        old.currentStage == 'executing' ||
        old.currentStage == 'instruction_queued';
    mergedSessionsMap[old.id] = wasRunning
        ? old.copyWith(status: 'active', currentStage: 'idle')
        : old;
  }

  final nextSessions = mergedSessionsMap.values.toList()
    ..sort((a, b) {
      final aBusy =
          a.status == 'running' || a.currentStage == 'awaiting_approval';
      final bBusy =
          b.status == 'running' || b.currentStage == 'awaiting_approval';
      if (aBusy != bBusy) return aBusy ? -1 : 1;
      final aTime =
          a.updatedAt ?? a.createdAt ?? DateTime.fromMillisecondsSinceEpoch(0);
      final bTime =
          b.updatedAt ?? b.createdAt ?? DateTime.fromMillisecondsSinceEpoch(0);
      return bTime.compareTo(aTime);
    });

  var nextTasks = List<CodeTask>.of(
    existingTasks.where((t) => t.agentName != 'terminal'),
  );
  for (final t in filteredRecentTasks) {
    final idx = nextTasks.indexWhere(
      (item) => item.id == t.id || (t.sessionId > 0 && item.sessionId == t.sessionId),
    );
    if (idx >= 0) {
      final oldTask = nextTasks[idx];
      final hasTaskDiff = t.diffAdditions > 0 || t.diffDeletions > 0;
      nextTasks[idx] = t.copyWith(
        providerModel: t.providerModel.isNotEmpty
            ? t.providerModel
            : oldTask.providerModel,
        diffAdditions: hasTaskDiff ? t.diffAdditions : oldTask.diffAdditions,
        diffDeletions: hasTaskDiff ? t.diffDeletions : oldTask.diffDeletions,
      );
    } else {
      nextTasks = <CodeTask>[t, ...nextTasks];
    }
  }
  for (final session in nextSessions) {
    if (recentSessionById.containsKey(session.id) ||
        session.id == activeStreamingSessionId ||
        session.status == 'active') {
      nextTasks = syncWorkspaceTasksWithSession(
        nextTasks,
        session,
        stageOverride: session.currentStage,
        insertIfNonEmpty: recentSessionById.containsKey(session.id),
      );
    }
  }

  return (sessions: nextSessions, tasks: nextTasks);
}

AiDevSession applyPromptAutoTitle(
  AiDevSession session,
  String promptText, {
  required bool hasImage,
  DateTime? now,
}) {
  if (!isPlaceholderSessionTitle(session.title)) return session;
  final autoTitle = extractSessionTitleFromPrompt(
    promptText,
    hasImage: hasImage,
  );
  if (autoTitle.isEmpty) return session;
  return session.copyWith(
    title: autoTitle,
    currentTaskTitle: autoTitle,
    updatedAt: now ?? session.updatedAt,
  );
}

({AiDevSession session, AiTaskSummary? currentTask})
reconcileRefreshedSessionTitle({
  required AiDevSession incomingSession,
  required AiTaskSummary? incomingTask,
  required AiDevSession? previousSession,
  required List<ChatMessage> messages,
  required String currentStage,
  String? concurrentRenamedTitle,
}) {
  var resolvedTitle =
      (concurrentRenamedTitle != null && concurrentRenamedTitle.trim().isNotEmpty)
      ? concurrentRenamedTitle.trim()
      : incomingSession.title;
  if (isPlaceholderSessionTitle(resolvedTitle)) {
    if (incomingTask != null && !isPlaceholderSessionTitle(incomingTask.title)) {
      resolvedTitle = incomingTask.title.trim();
    } else if (previousSession != null &&
        previousSession.id == incomingSession.id &&
        !isPlaceholderSessionTitle(previousSession.title)) {
      resolvedTitle = previousSession.title.trim();
    } else {
      for (final msg in messages) {
        if (!msg.isUser) continue;
        final derived = extractSessionTitleFromPrompt(
          msg.text,
          hasImage: msg.hasImage,
        );
        if (derived.isNotEmpty && !isPlaceholderSessionTitle(derived)) {
          resolvedTitle = derived;
          break;
        }
      }
    }
  }
  final resolvedCurrentTaskTitle = !isPlaceholderSessionTitle(resolvedTitle)
      ? resolvedTitle
      : incomingSession.currentTaskTitle;
  final samePrev = previousSession != null && previousSession.id == incomingSession.id;
  final resolvedModel = incomingSession.providerModel.trim().isNotEmpty
      ? incomingSession.providerModel
      : (samePrev ? previousSession.providerModel : incomingSession.providerModel);
  final resolvedAgent = incomingSession.agentName.trim().isNotEmpty
      ? incomingSession.agentName
      : (samePrev ? previousSession.agentName : incomingSession.agentName);
  final resolvedEffort = incomingSession.reasoningEffort.trim().isNotEmpty
      ? incomingSession.reasoningEffort
      : (samePrev ? previousSession.reasoningEffort : incomingSession.reasoningEffort);
  final hasIncomingDiff =
      incomingSession.diffAdditions > 0 || incomingSession.diffDeletions > 0;
  final nextStage = currentStage.isNotEmpty
      ? currentStage
      : incomingSession.currentStage;
  final nextTaskStatus = resolveCodeTaskStatus(
    sessionStatus: incomingSession.status,
    currentStage: nextStage,
  );
  final syncedSession = incomingSession.copyWith(
    title: resolvedTitle,
    currentTaskTitle: resolvedCurrentTaskTitle,
    agentName: resolvedAgent,
    providerModel: resolvedModel,
    reasoningEffort: resolvedEffort,
    subagents: incomingSession.subagents.isNotEmpty
        ? incomingSession.subagents
        : (samePrev ? previousSession.subagents : const []),
    diffAdditions: hasIncomingDiff
        ? incomingSession.diffAdditions
        : (samePrev ? previousSession.diffAdditions : 0),
    diffDeletions: hasIncomingDiff
        ? incomingSession.diffDeletions
        : (samePrev ? previousSession.diffDeletions : 0),
    status: nextTaskStatus == 'running'
        ? 'running'
        : (nextTaskStatus == 'failed' ? 'failed' : incomingSession.status),
    currentStage: nextStage,
  );
  final syncedTask = syncTaskSummaryTitle(incomingTask, syncedSession);
  return (session: syncedSession, currentTask: syncedTask);
}

List<ChatMessage> finalizeStoppedChatHistory(List<ChatMessage> messages) {
  final result = <ChatMessage>[];
  for (final msg in messages) {
    if (!msg.isStreaming && !msg.id.startsWith('stream-')) {
      result.add(msg);
      continue;
    }
    if (msg.text.trim().isEmpty &&
        msg.reasoning.trim().isEmpty &&
        msg.toolSteps.isEmpty &&
        !msg.hasImage) {
      continue;
    }
    result.add(msg.copyWith(isStreaming: false, activity: ''));
  }
  return result;
}

List<ChatMessage> mergeWorkspaceHistoryMessages(
  List<ChatMessage> history,
  List<ChatMessage> current, {
  bool isSessionRunning = false,
}) {
  if (history.isEmpty) return current;
  final result = [...history];
  final persistedUserCountInCurrent = current
      .where((m) => m.isUser && !m.id.startsWith('local-'))
      .length;
  final persistedUserCountInHistory = history.where((m) => m.isUser).length;
  final canMatchLocalUserToHistory =
      persistedUserCountInCurrent == 0 ||
      persistedUserCountInHistory > persistedUserCountInCurrent;
  for (final message in current) {
    if (!message.id.startsWith('local-') && !message.id.startsWith('stream-')) {
      continue;
    }
    var matchIdx = -1;
    if (message.isUser) {
      if (canMatchLocalUserToHistory) {
        matchIdx = result.lastIndexWhere(
          (item) =>
              item.isUser &&
              (item.text.trim() == message.text.trim() ||
                  (message.hasImage &&
                      message.text.trim().isEmpty &&
                      item.text.trim().startsWith('[Attachment:'))),
        );
      }
    } else {
      final lastUserIdx = result.lastIndexWhere((item) => item.isUser);
      final lastAssistantIdx = result.lastIndexWhere(
        (item) => !item.isUser && item.id != 'welcome',
      );
      if (lastAssistantIdx > lastUserIdx) matchIdx = lastAssistantIdx;
    }
    if (matchIdx >= 0) {
      if (message.isUser) {
        final existing = result[matchIdx];
        if (!existing.hasImage && message.hasImage) {
          result[matchIdx] = existing.copyWith(
            imageDataUrl: message.imageDataUrl,
          );
        }
      } else {
        final existing = result[matchIdx];
        final keepStreaming =
            message.isStreaming || existing.isStreaming || isSessionRunning;
        final archivedPreTool =
            message.text.isEmpty &&
            message.toolSteps.length > existing.toolSteps.length;
        final mergedText = !keepStreaming
            ? (existing.text.isNotEmpty ? existing.text : message.text)
            : (archivedPreTool
                  ? ''
                  : (message.text.length >= existing.text.length
                        ? message.text
                        : existing.text));
        final mergedReasoning =
            message.reasoning.length > existing.reasoning.length
            ? message.reasoning
            : existing.reasoning;
        final mergedTools = !keepStreaming
            ? (existing.toolSteps.isNotEmpty
                  ? existing.toolSteps
                  : message.toolSteps)
            : (message.toolSteps.length >= existing.toolSteps.length
                  ? message.toolSteps
                  : existing.toolSteps);
        result[matchIdx] = existing.copyWith(
          id: keepStreaming && message.id.startsWith('stream-')
              ? message.id
              : existing.id,
          text: mergedText,
          reasoning: mergedReasoning,
          activity: keepStreaming && message.activity.isNotEmpty
              ? message.activity
              : existing.activity,
          isStreaming: keepStreaming,
          agentLabel: existing.agentLabel.isEmpty
              ? message.agentLabel
              : existing.agentLabel,
          durationMs: existing.durationMs > 0
              ? existing.durationMs
              : message.durationMs,
          totalTokens: existing.totalTokens > 0
              ? existing.totalTokens
              : message.totalTokens,
          toolSteps: mergedTools,
        );
      }
      continue;
    }
    final hasVisibleContent =
        message.text.trim().isNotEmpty ||
        message.hasImage ||
        message.reasoning.trim().isNotEmpty ||
        message.toolSteps.isNotEmpty ||
        message.isStreaming;
    if (hasVisibleContent) result.add(message);
  }
  if (isSessionRunning &&
      result.isNotEmpty &&
      !result.last.isUser &&
      result.last.id != 'welcome' &&
      result.last.id != 'stream-current') {
    result[result.length - 1] = result.last.copyWith(
      id: 'stream-current',
      isStreaming: true,
    );
  }
  return result;
}
