part of 'hoxi_workspace_adapter.dart';

class _HoxiSessionRef {
  final String hoxiId, parentHoxiId, directory, projectDir, runtime, title, sandboxMode, branch, modelId, reasoningEffort, approvalPolicy;
  final int projectId, diffAdditions, diffDeletions;
  final bool hasLocalTitleOverride, hasLocalModelOverride, hasLocalEffortOverride;
  final DateTime? createdAt, updatedAt;

  const _HoxiSessionRef({
    required this.hoxiId, this.parentHoxiId = '', required this.directory, required this.projectDir,
    required this.projectId, required this.runtime, required this.title,
    required this.sandboxMode, required this.branch, required this.modelId,
    this.reasoningEffort = '', this.approvalPolicy = 'safe_auto',
    this.diffAdditions = 0, this.diffDeletions = 0,
    this.hasLocalTitleOverride = false, this.hasLocalModelOverride = false,
    this.hasLocalEffortOverride = false, this.createdAt, this.updatedAt,
  });

  HoxiSessionTarget toTarget() => HoxiSessionTarget(
    hoxiId: hoxiId, directory: directory, projectDir: projectDir,
    branch: branch, sandboxMode: sandboxMode, runtime: runtime,
  );

  _HoxiSessionRef copyWith({
    String? parentHoxiId, String? runtime, String? title, String? modelId, String? reasoningEffort,
    String? approvalPolicy, int? diffAdditions, int? diffDeletions,
    bool? hasLocalTitleOverride, bool? hasLocalModelOverride, bool? hasLocalEffortOverride, DateTime? updatedAt,
  }) => _HoxiSessionRef(
    hoxiId: hoxiId, parentHoxiId: parentHoxiId ?? this.parentHoxiId,
    directory: directory, projectDir: projectDir, projectId: projectId,
    runtime: runtime ?? this.runtime, title: title ?? this.title,
    sandboxMode: sandboxMode, branch: branch, modelId: modelId ?? this.modelId,
    reasoningEffort: reasoningEffort ?? this.reasoningEffort, approvalPolicy: approvalPolicy ?? this.approvalPolicy,
    diffAdditions: diffAdditions ?? this.diffAdditions, diffDeletions: diffDeletions ?? this.diffDeletions,
    hasLocalTitleOverride: hasLocalTitleOverride ?? this.hasLocalTitleOverride,
    hasLocalModelOverride: hasLocalModelOverride ?? this.hasLocalModelOverride,
    hasLocalEffortOverride: hasLocalEffortOverride ?? this.hasLocalEffortOverride,
    createdAt: createdAt, updatedAt: updatedAt ?? this.updatedAt,
  );
}

class _HoxiParsedSessionTurns {
  final List<ChatMessage> chatMessages;
  final List<CodeHistoryMessage> historyMessages;
  final List<CodeExecutionRun> runs;
  final Map<int, CodeExecutionRunDetail> runDetailsById;
  final Map<int, ({int sessionId, String prompt})> instructionsById;
  final List<AiTimelineEvent> timeline;
  final String recentOutput;
  final AiInstruction? latestInstruction;
  final List<CodeTodoItem> todos;
  final List<HoxiTaskToolCallRef> taskToolCalls;

  const _HoxiParsedSessionTurns({
    required this.chatMessages, required this.historyMessages, required this.runs,
    required this.runDetailsById, required this.instructionsById,
    required this.timeline, required this.recentOutput, required this.latestInstruction,
    this.todos = const [], this.taskToolCalls = const [],
  });
}

class _HoxiRunAccumulator {
  final int runId, instructionId;
  final String prompt;
  final DateTime startedAt;
  final StringBuffer output = StringBuffer(), rawOutput = StringBuffer(), reasoning = StringBuffer();
  String model = '', executorId = '', errorMessage = '', assistantMsgId = '', assistantTurnKey = '';
  DateTime? completedAt, assistantTimestamp;
  int explicitDurationMs = 0, fallbackDurationMs = 0, inputTokens = 0, outputTokens = 0;
  int cachedInputTokens = 0, reasoningTokens = 0, totalTokens = 0;
  bool hasAssistantTurn = false, hadToolAfterOutput = false;
  int outputInsertStepIdx = 0;
  final List<String> pendingToolSteps = <String>[];
  List<CodeTodoItem> todos = const [];

  _HoxiRunAccumulator({
    required this.runId, required this.instructionId, required this.prompt,
    required this.startedAt, required this.model, required this.executorId,
  });

  void archiveCurrentOutputToStep() {
    if (output.isEmpty) return;
    final note = CodeConversationStreamParser.formatStepNote(output.toString());
    if (note.isNotEmpty) {
      final idx = outputInsertStepIdx.clamp(0, pendingToolSteps.length);
      if (idx == 0 || pendingToolSteps[idx - 1] != note) pendingToolSteps.insert(idx, note);
    }
    output.clear();
    hadToolAfterOutput = false;
  }

  void appendAssistantText(String text) {
    final trimmed = text.trim();
    if (trimmed.isEmpty) return;
    if (output.isNotEmpty && hadToolAfterOutput) archiveCurrentOutputToStep();
    if (output.isEmpty) {
      outputInsertStepIdx = pendingToolSteps.length;
    } else {
      output.write('\n\n');
    }
    output.write(trimmed);
  }

  void appendToolStep(String stepLabel, {required String toolName}) {
    if (stepLabel.isEmpty) return;
    if (pendingToolSteps.isEmpty || pendingToolSteps.last != stepLabel) {
      if (pendingToolSteps.isNotEmpty && stepLabel.startsWith('${pendingToolSteps.last} · ')) {
        pendingToolSteps[pendingToolSteps.length - 1] = stepLabel;
      } else if (pendingToolSteps.isEmpty || !pendingToolSteps.last.startsWith('$stepLabel · ')) {
        pendingToolSteps.add(stepLabel);
      }
    }
    if (output.isNotEmpty && toolName.toLowerCase() != 'todowrite') hadToolAfterOutput = true;
  }
}

extension _HoxiSessionTurnParsing on HoxiWorkspaceAdapter {
  static String resolveSourceWorkDir(Map<String, dynamic> map, String workDir, String projectWorkDir) {
    final rawProjectDir = (map['projectDir'] ?? map['sourceWorkDir'] ?? map['projectID'] ?? '').toString().trim();
    if (rawProjectDir.contains('/') || rawProjectDir.contains(r'\')) return rawProjectDir;
    final wtIdx = workDir.replaceAll(r'\', '/').indexOf('/.hoxi/worktrees/');
    if (wtIdx > 0) return workDir.substring(0, wtIdx);
    return projectWorkDir.isNotEmpty ? projectWorkDir : workDir;
  }

  static String composeSessionModelId(String runtime, String rawProviderId, String rawModelId) {
    final model = rawModelId.trim(), provider = rawProviderId.trim();
    if (model.isEmpty) return '';
    if (provider.isEmpty || provider == runtime.trim() || model.startsWith('$provider/') || model.contains('/')) return model;
    return '$provider/$model';
  }

  static Map<String, String>? buildPromptModelPayload(String runtime, String rawModelId) {
    final trimmed = rawModelId.trim();
    if (trimmed.isEmpty) return null;
    final slashIdx = trimmed.indexOf('/');
    if (slashIdx > 0 && slashIdx < trimmed.length - 1) {
      return {'providerID': trimmed.substring(0, slashIdx), 'modelID': trimmed.substring(slashIdx + 1)};
    }
    final cleanRuntime = runtime.trim();
    return {'providerID': cleanRuntime.isEmpty || cleanRuntime == 'opencode' ? '' : cleanRuntime, 'modelID': trimmed};
  }

  void _storeBoundedParsedRuns(_HoxiParsedSessionTurns parsed) {
    _runDetailsById.addAll(parsed.runDetailsById);
    while (_runDetailsById.length > 200) {
      _runDetailsById.remove(_runDetailsById.keys.first);
    }
    _instructionsById.addAll(parsed.instructionsById);
    while (_instructionsById.length > 200) {
      _instructionsById.remove(_instructionsById.keys.first);
    }
  }

  Future<CodeSessionHistory> _loadSessionHistory(int sessionId, {int page = 1, int limit = 20}) async {
    var ref = await _resolveSessionRef(sessionId);
    final statusMap = await _fetchSessionStatuses(ref.directory);
    final rawBusy = isBusySessionStatusValue(
      statusMap == null ? _knownStageBySessionId[sessionId] : statusMap[ref.hoxiId],
    );
    if (rawBusy) _startedUntilBySessionId.remove(sessionId);
    final isBusy = (rawBusy || _isRecentlyStartedBusy(sessionId)) && !_isSuppressedBusy(sessionId);
    final parsed = await _loadParsedSessionTurns(sessionId, ref, isBusy: isBusy);
    _storeBoundedParsedRuns(parsed);
    ref = _sessionsById[sessionId] ?? ref;
    final safePage = page < 1 ? 1 : page, safeLimit = limit < 1 ? 20 : limit;
    final start = (safePage - 1) * safeLimit;
    final pagedRuns = start >= parsed.runs.length ? const <CodeExecutionRun>[] : parsed.runs.skip(start).take(safeLimit).toList();
    return CodeSessionHistory(
      session: _toDevSession(sessionId, ref, stage: isBusy ? 'running' : 'idle'),
      messages: parsed.historyMessages, runs: pagedRuns,
      total: parsed.runs.length, page: safePage, limit: safeLimit,
    );
  }

  Future<AiInstruction> _retryInstructionById(int instructionId) async {
    var entry = _instructionsById[instructionId];
    if (entry == null) {
      if (_sessionsById.isEmpty) await getSessions();
      for (final sid in _sessionsById.keys.toList()) {
        try { await getSessionHistory(sid); } catch (_) {}
        entry = _instructionsById[instructionId];
        if (entry != null) break;
      }
    }
    if (entry == null || entry.prompt.trim().isEmpty) throw Exception('未找到可重试的 Hoxi 指令 ($instructionId)');
    return (await sendAiCommand(sessionId: entry.sessionId, command: entry.prompt)).instruction;
  }

  Future<CodeExecutionRunDetail> _loadExecutionRun(int runId) async {
    final cached = _runDetailsById[runId];
    if (cached != null) return cached;
    if (_sessionsById.isEmpty) await getSessions();
    for (final sessionId in _sessionsById.keys.toList()) {
      try { await getSessionHistory(sessionId); } catch (_) {}
      final loaded = _runDetailsById[runId];
      if (loaded != null) return loaded;
    }
    return _gitAdapter.getExecutionRun(runId);
  }

  Future<_HoxiParsedSessionTurns> _loadParsedSessionTurns(
    int sessionId, _HoxiSessionRef ref, {required bool isBusy}
  ) {
    final existing = _inflightParsedTurns[sessionId];
    if (existing != null) return existing;
    final future = _loadParsedSessionTurnsInternal(sessionId, ref, isBusy: isBusy);
    _inflightParsedTurns[sessionId] = future;
    return future.whenComplete(() {
      if (identical(_inflightParsedTurns[sessionId], future)) _inflightParsedTurns.remove(sessionId);
    });
  }

  Future<_HoxiParsedSessionTurns> _loadParsedSessionTurnsInternal(
    int sessionId, _HoxiSessionRef ref, {required bool isBusy}
  ) async {
    final messages = <ChatMessage>[], historyMessages = <CodeHistoryMessage>[];
    final timeline = <AiTimelineEvent>[], accumulators = <_HoxiRunAccumulator>[];
    final taskToolCalls = <HoxiTaskToolCallRef>[];
    var recentOutput = '';

    void flushAssistantCycle(_HoxiRunAccumulator? acc) {
      if (acc == null || !acc.hasAssistantTurn) return;
      final outText = acc.output.toString().trim(), outReasoning = acc.reasoning.toString().trim();
      final steps = acc.pendingToolSteps.isEmpty ? const <String>[] : List<String>.unmodifiable(acc.pendingToolSteps);
      if (outText.isEmpty && outReasoning.isEmpty && steps.isEmpty) return;
      final msgId = acc.assistantMsgId.isNotEmpty ? acc.assistantMsgId : '${messages.length + 1}';
      final msgTime = acc.assistantTimestamp ?? acc.completedAt ?? acc.startedAt;
      final durMs = acc.explicitDurationMs > 0 ? acc.explicitDurationMs : acc.fallbackDurationMs;
      messages.add(ChatMessage(id: msgId, text: outText, isUser: false, timestamp: msgTime, reasoning: outReasoning, durationMs: durMs, totalTokens: acc.totalTokens, toolSteps: steps));
      historyMessages.add(CodeHistoryMessage(
        id: HoxiWorkspaceAdapter.stablePositiveIntId('msg-${ref.hoxiId}-${acc.assistantTurnKey}-${historyMessages.length + 1}'),
        runId: acc.runId, role: 'assistant', content: outText, createdAt: msgTime, reasoning: outReasoning, durationMs: durMs, totalTokens: acc.totalTokens, toolSteps: steps,
      ));
      if (outText.isNotEmpty) recentOutput = outText;
    }

    try {
      final res = await _apiClient.get<dynamic>(
        '/api/hoxi/sessions/${ref.hoxiId}/message', queryParameters: {'directory': ref.directory},
      );
      final turns = res.data is List ? (res.data as List) : const [];
      if (res.data is List) {
        _diffScopesBySessionId[sessionId] = HoxiSessionDiffScope.fromTurns(turns, projectDir: ref.projectDir, worktreeDir: ref.directory);
        _latestTurnDiffScopesBySessionId[sessionId] = HoxiSessionDiffScope.fromLatestTurn(turns, projectDir: ref.projectDir, worktreeDir: ref.directory);
      }
      var eventIdx = 1, turnIdx = 0;
      var latestTurnModelId = '', latestTurnProviderId = '', latestTurnEffort = '';
      _HoxiRunAccumulator? activeRun;

      for (final rawTurn in turns.whereType<Map>()) {
        turnIdx++;
        final turn = rawTurn.cast<String, dynamic>();
        final info = turn['info'] is Map ? (turn['info'] as Map).cast<String, dynamic>() : turn;
        final modelMap = info['model'] is Map ? (info['model'] as Map).cast<String, dynamic>() : null;
        final tModel = (info['modelID'] ?? modelMap?['modelID'] ?? (info['model'] is String ? info['model'] : '') ?? '').toString().trim();
        final tProv = (info['providerID'] ?? modelMap?['providerID'] ?? '').toString().trim();
        final tEffort = (info['variant'] ?? info['reasoningEffort'] ?? '').toString().trim();
        if (tModel.isNotEmpty) {
          latestTurnModelId = tModel;
          if (tProv.isNotEmpty) latestTurnProviderId = tProv;
        }
        if (tEffort.isNotEmpty) latestTurnEffort = tEffort;
        final isUser = (info['role'] ?? 'assistant').toString().trim().toLowerCase() == 'user';
        final turnKey = (info['id'] ?? 'turn-$turnIdx').toString().trim();
        final timeMap = info['time'] is Map ? (info['time'] as Map).cast<String, dynamic>() : const <String, dynamic>{};
        final createdMs = (timeMap['created'] as num?)?.toInt() ?? 0, completedMs = (timeMap['completed'] as num?)?.toInt() ?? 0;
        final createdAt = createdMs > 0 ? DateTime.fromMillisecondsSinceEpoch(createdMs) : DateTime.now();
        final completedAt = completedMs > 0 ? DateTime.fromMillisecondsSinceEpoch(completedMs) : null;
        final parts = (turn['parts'] as List<dynamic>? ?? const []).whereType<Map>().map((p) => p.cast<String, dynamic>()).toList();
        final textBuffer = StringBuffer(), reasoningBuffer = StringBuffer(), rawTurnBuffer = StringBuffer();
        var turnTodos = const <CodeTodoItem>[], toolError = '';
        String? turnImageDataUrl;
        if (!isUser && activeRun == null) {
          activeRun = _HoxiRunAccumulator(
            runId: HoxiWorkspaceAdapter.stablePositiveIntId('run-${ref.hoxiId}-$turnKey'),
            instructionId: HoxiWorkspaceAdapter.stablePositiveIntId('inst-${ref.hoxiId}-$turnKey'),
            prompt: '', startedAt: createdAt, model: (info['modelID'] ?? ref.modelId).toString(), executorId: (info['providerID'] ?? ref.runtime).toString(),
          );
          accumulators.add(activeRun);
        }

        for (final part in parts) {
          final pType = (part['type'] ?? '').toString();
          if (pType == 'text') {
            final rawText = (part['text'] ?? '').toString();
            final text = isUser ? rawText : stripInlineThinking(rawText);
            if (text.trim().isNotEmpty) {
              if (isUser) {
                if (textBuffer.isNotEmpty) textBuffer.write('\n\n');
                textBuffer.write(text);
              } else {
                activeRun!.appendAssistantText(text);
              }
            }
            if (rawText.trim().isNotEmpty) {
              if (rawTurnBuffer.isNotEmpty) rawTurnBuffer.writeln();
              rawTurnBuffer.write(rawText);
            }
          } else if (pType == 'reasoning') {
            final rawReasoning = normalizeReasoningText(stripInlineThinking((part['text'] ?? '').toString())).trim();
            if (rawReasoning.isNotEmpty) {
              if (reasoningBuffer.isNotEmpty) reasoningBuffer.write('\n\n');
              reasoningBuffer.write(rawReasoning);
            }
          } else if (pType == 'file') {
            final resolvedImg = isUser ? await _catalogAdapter.resolveFilePartImageDataUrl(part) : null;
            if (resolvedImg != null && resolvedImg.isNotEmpty) {
              turnImageDataUrl ??= resolvedImg;
            } else {
              final filename = (part['filename'] ?? part['attachmentId'] ?? 'attachment').toString();
              final mime = (part['mime'] ?? '').toString();
              final tag = mime.isNotEmpty ? '[Attachment: $filename ($mime)]' : '[Attachment: $filename]';
              if (isUser) {
                if (textBuffer.isNotEmpty) textBuffer.writeln();
                textBuffer.write(tag);
              } else {
                activeRun!.appendAssistantText(tag);
              }
            }
          } else if (pType == 'tool') {
            final toolName = (part['tool'] ?? 'tool').toString();
            final stateMap = part['state'] is Map ? (part['state'] as Map).cast<String, dynamic>() : const <String, dynamic>{};
            final status = (stateMap['status'] ?? 'completed').toString(), title = (stateMap['title'] ?? toolName).toString();
            final errText = (stateMap['error'] ?? '').toString().trim();
            if (status == 'error' && errText.isNotEmpty && toolError.isEmpty) toolError = errText;
            if (toolName.toLowerCase() == 'todowrite') {
              final extracted = CodeTodoItem.extractFromToolState(stateMap);
              if (extracted.isNotEmpty) turnTodos = extracted;
            } else if (isTaskLikeToolName(toolName)) {
              final inputMap = stateMap['input'] is Map ? (stateMap['input'] as Map).cast<String, dynamic>() : const <String, dynamic>{};
              final desc = (inputMap['description'] ?? stateMap['title'] ?? '').toString().trim();
              final agentType = (inputMap['subagent_type'] ?? 'subagent').toString().trim();
              var childHoxi = extractSubagentChildHoxiIdFromToolState(stateMap);
              if (childHoxi.isEmpty && desc.isNotEmpty) {
                final matched = _sessionsById.values.where(
                  (s) => s.parentHoxiId == ref.hoxiId && (s.title == desc || s.title.startsWith('$desc (') || s.title.contains(desc)),
                ).firstOrNull;
                if (matched != null) childHoxi = matched.hoxiId;
              }
              if (childHoxi.isNotEmpty) {
                taskToolCalls.add(HoxiTaskToolCallRef(childHoxiId: childHoxi, description: desc, agentType: agentType, status: status));
              }
            }
            final stepLabel = CodeConversationStreamParser.extractToolStepSummary(toolName, stateMap);
            if (!isUser && stepLabel.isNotEmpty) activeRun!.appendToolStep(stepLabel, toolName: toolName);
            if (rawTurnBuffer.isNotEmpty) rawTurnBuffer.writeln();
            rawTurnBuffer.write('[$toolName:$status] $title');
            timeline.add(AiTimelineEvent(
              id: eventIdx++, sessionId: sessionId, taskId: sessionId, instructionId: activeRun?.instructionId ?? 0,
              eventType: 'tool', stage: status == 'running' ? 'executing' : 'completed',
              title: title, content: toolName, status: status == 'error' ? 'error' : 'info', meta: '', createdAt: createdAt, updatedAt: completedAt ?? createdAt,
            ));
          }
        }

        final fullText = textBuffer.toString().trim(), fullReasoning = reasoningBuffer.toString().trim();
        final turnError = _extractTurnError(info['error'], fallback: toolError);

        if (isUser) {
          flushAssistantCycle(activeRun);
          final runId = HoxiWorkspaceAdapter.stablePositiveIntId('run-${ref.hoxiId}-$turnKey');
          final instructionId = HoxiWorkspaceAdapter.stablePositiveIntId('inst-${ref.hoxiId}-$turnKey');
          activeRun = _HoxiRunAccumulator(
            runId: runId, instructionId: instructionId, prompt: fullText,
            startedAt: createdAt, model: (info['modelID'] ?? ref.modelId).toString(), executorId: (info['providerID'] ?? ref.runtime).toString(),
          );
          accumulators.add(activeRun);
          if (fullText.isNotEmpty || fullReasoning.isNotEmpty || (turnImageDataUrl?.isNotEmpty ?? false)) {
            final msgId = (info['id'] ?? '${messages.length + 1}').toString(), msgTime = completedAt ?? createdAt;
            messages.add(ChatMessage(id: msgId, text: fullText, isUser: true, timestamp: msgTime, reasoning: fullReasoning, imageDataUrl: turnImageDataUrl));
            historyMessages.add(CodeHistoryMessage(
              id: HoxiWorkspaceAdapter.stablePositiveIntId('msg-${ref.hoxiId}-$turnKey-${historyMessages.length + 1}'),
              runId: activeRun.runId, role: 'user', content: fullText, createdAt: msgTime, reasoning: fullReasoning, imageDataUrl: turnImageDataUrl,
            ));
          }
        } else {
          final run = activeRun!;
          run.hasAssistantTurn = true;
          if (turnTodos.isNotEmpty) run.todos = turnTodos;
          final rawId = (info['id'] ?? '').toString().trim();
          if (rawId.isNotEmpty) run.assistantMsgId = rawId;
          run.assistantTurnKey = turnKey;
          run.assistantTimestamp = completedAt ?? createdAt;
          final modelId = (info['modelID'] ?? '').toString().trim();
          if (modelId.isNotEmpty) run.model = modelId;
          final providerId = (info['providerID'] ?? '').toString().trim();
          if (providerId.isNotEmpty) run.executorId = providerId;
          if (fullReasoning.isNotEmpty) {
            if (run.reasoning.isNotEmpty) run.reasoning.write('\n\n');
            run.reasoning.write(fullReasoning);
          }
          final rawStr = rawTurnBuffer.toString().trim();
          if (rawStr.isNotEmpty) {
            if (run.rawOutput.isNotEmpty) run.rawOutput.writeln();
            run.rawOutput.write(rawStr);
          }
          if (turnError.isNotEmpty && run.errorMessage.isEmpty) run.errorMessage = turnError;
          if (completedAt != null) run.completedAt = completedAt;
          final explicitDur = (info['durationMs'] as num?)?.toInt() ?? 0;
          if (explicitDur > 0) {
            run.explicitDurationMs += explicitDur;
          } else if (completedAt != null && completedAt.isAfter(createdAt)) {
            run.fallbackDurationMs += completedAt.difference(createdAt).inMilliseconds;
          }
          final tokens = _extractTokens(info['tokens']);
          run.inputTokens += tokens.input;
          run.outputTokens += tokens.output;
          run.cachedInputTokens += tokens.cachedInput;
          run.reasoningTokens += tokens.reasoning;
          run.totalTokens += tokens.total;
        }
      }
      if (latestTurnModelId.isNotEmpty && !ref.hasLocalModelOverride) {
        final isRunner = CodeModelCatalog.isRunnerExecutor(latestTurnProviderId);
        final candidateModel = isRunner ? latestTurnModelId : composeSessionModelId('opencode', latestTurnProviderId, latestTurnModelId);
        final turnRuntime = CodeModelCatalog.resolveRuntimeForModel(
          model: candidateModel, explicitExecutorId: isRunner ? latestTurnProviderId : null,
          executors: _catalogAdapter.cachedExecutors, fallbackRuntime: ref.runtime,
        );
        final turnModel = composeSessionModelId(turnRuntime, latestTurnProviderId, latestTurnModelId);
        ref = ref.copyWith(
          modelId: turnModel, runtime: turnRuntime, hasLocalModelOverride: true,
          reasoningEffort: (!ref.hasLocalEffortOverride && latestTurnEffort.isNotEmpty) ? latestTurnEffort : null,
        );
        _sessionsById[sessionId] = ref;
      }
      if (isBusy && activeRun != null && activeRun.hadToolAfterOutput) activeRun.archiveCurrentOutputToStep();
      flushAssistantCycle(activeRun);
    } catch (_) {}

    final runs = <CodeExecutionRun>[];
    final runDetailsById = <int, CodeExecutionRunDetail>{};
    final instructionsById = <int, ({int sessionId, String prompt})>{};
    AiInstruction? latestInstruction;

    for (var i = 0; i < accumulators.length; i++) {
      final acc = accumulators[i], isLast = i == accumulators.length - 1;
      final status = acc.errorMessage.isNotEmpty ? 'failed' : (isLast && isBusy) ? 'running' : (!acc.hasAssistantTurn ? 'cancelled' : 'completed');
      final durationMs = acc.explicitDurationMs > 0
          ? acc.explicitDurationMs
          : (acc.completedAt != null && acc.completedAt!.isAfter(acc.startedAt)) ? acc.completedAt!.difference(acc.startedAt).inMilliseconds : 0;
      final outputText = acc.output.toString().trim(), rawOutputText = acc.rawOutput.toString().trim();

      runs.add(CodeExecutionRun(
        id: acc.runId, instructionId: acc.instructionId, prompt: acc.prompt,
        output: outputText, status: status, errorMessage: acc.errorMessage, durationMs: durationMs, totalTokens: acc.totalTokens, createdAt: acc.startedAt,
      ));
      runDetailsById[acc.runId] = CodeExecutionRunDetail(
        id: acc.runId, sessionId: sessionId, taskId: sessionId, instructionId: acc.instructionId,
        executorId: acc.executorId.isEmpty ? ref.runtime : acc.executorId,
        model: acc.model, prompt: acc.prompt, output: outputText, rawOutput: rawOutputText.isEmpty ? outputText : rawOutputText,
        status: status, exitCode: status == 'failed' ? 1 : 0, durationMs: durationMs,
        inputTokens: acc.inputTokens, outputTokens: acc.outputTokens, cachedInputTokens: acc.cachedInputTokens, reasoningTokens: acc.reasoningTokens,
        totalTokens: acc.totalTokens, errorMessage: acc.errorMessage, startedAt: acc.startedAt, completedAt: acc.completedAt, createdAt: acc.startedAt,
      );
      if (acc.prompt.isNotEmpty) instructionsById[acc.instructionId] = (sessionId: sessionId, prompt: acc.prompt);
      if (isLast && acc.prompt.isNotEmpty) {
        latestInstruction = AiInstruction(
          id: acc.instructionId, sessionId: sessionId, taskId: sessionId, content: acc.prompt, status: status,
          allowCode: true, autoPreview: false, requireApproval: false, analysisOnly: false, createdAt: acc.startedAt,
        );
      }
    }

    return _HoxiParsedSessionTurns(
      chatMessages: messages, historyMessages: historyMessages, runs: runs.reversed.toList(),
      runDetailsById: runDetailsById, instructionsById: instructionsById, timeline: timeline,
      recentOutput: recentOutput, latestInstruction: latestInstruction,
      todos: accumulators.isNotEmpty ? accumulators.last.todos : const [], taskToolCalls: taskToolCalls,
    );
  }

  static String _extractTurnError(Object? rawError, {String fallback = ''}) {
    if (rawError is String && rawError.trim().isNotEmpty) return rawError.trim();
    if (rawError is Map) {
      final map = rawError.cast<String, dynamic>(), data = map['data'];
      if (data is Map && (data['message'] ?? '').toString().trim().isNotEmpty) return data['message'].toString().trim();
      final msg = (map['message'] ?? map['name'] ?? '').toString().trim();
      if (msg.isNotEmpty) return msg;
    }
    return fallback;
  }

  static ({int input, int output, int cachedInput, int reasoning, int total}) _extractTokens(Object? rawTokens) {
    if (rawTokens is! Map) return (input: 0, output: 0, cachedInput: 0, reasoning: 0, total: 0);
    final map = rawTokens.cast<String, dynamic>();
    int numVal(String k1, [String? k2]) => ((map[k1] ?? (k2 != null ? map[k2] : null)) as num?)?.toInt() ?? 0;
    final input = numVal('input', 'inputTokens'), output = numVal('output', 'outputTokens');
    final reasoning = numVal('reasoning', 'reasoningTokens'), cacheMap = map['cache'];
    final cachedInput = cacheMap is Map ? ((cacheMap['read'] as num?)?.toInt() ?? 0) : numVal('cachedInputTokens');
    final explicitTotal = numVal('total', 'totalTokens');
    final total = explicitTotal > 0 ? explicitTotal : (input + output + reasoning + cachedInput);
    return (input: input, output: output, cachedInput: cachedInput, reasoning: reasoning, total: total);
  }

  Future<Map<String, String>?> _fetchSessionStatuses(String directory) {
    final existing = _inflightSessionStatuses[directory];
    if (existing != null) return existing;
    final future = _fetchSessionStatusesInternal(directory);
    _inflightSessionStatuses[directory] = future;
    return future.whenComplete(() {
      if (identical(_inflightSessionStatuses[directory], future)) _inflightSessionStatuses.remove(directory);
    });
  }

  Future<Map<String, String>?> _fetchSessionStatusesInternal(String directory) async {
    try {
      final res = await _apiClient.get<Map<String, dynamic>>('/api/hoxi/sessions/status', queryParameters: {'directory': directory});
      final data = res.data;
      if (data == null) return null;
      final out = <String, String>{};
      for (final entry in data.entries) {
        final value = entry.value;
        final raw = value is Map
            ? value['type'] ?? value['status'] ?? switch (value['busy']) {
                true => 'busy', false => 'idle', _ => null,
              }
            : value;
        if (raw is! String) return null;
        final status = raw.toLowerCase().trim();
        if (status != 'idle' && !isBusySessionStatusValue(status)) return null;
        out[entry.key] = status;
      }
      return out;
    } catch (_) {
      return null;
    }
  }

  Future<_HoxiSessionRef> _resolveSessionRef(int sessionId) async {
    final existing = _sessionsById[sessionId];
    if (existing != null) return existing;
    await getSessions();
    final reloaded = _sessionsById[sessionId];
    if (reloaded != null) return reloaded;
    throw Exception('未找到对应的 Hoxi 会话 ($sessionId)');
  }

  _HoxiSessionRef _resolveRootSessionRef(_HoxiSessionRef ref) {
    var curr = ref;
    final visited = <String>{curr.hoxiId};
    for (var depth = 0; depth < 8; depth++) {
      final parentHoxi = curr.parentHoxiId.trim();
      if (parentHoxi.isEmpty || !visited.add(parentHoxi)) break;
      final parentRef = _sessionsById[HoxiWorkspaceAdapter.stablePositiveIntId(parentHoxi)];
      if (parentRef == null) break;
      curr = parentRef;
    }
    return curr;
  }

  ({List<CodeConvoyPlan> convoyPlans, List<CodeSubagentItem> subagents, List<AiDevSession> childSessions}) _buildConvoyAndSubagentsForState({
    required _HoxiSessionRef ref, required _HoxiSessionRef rootRef, required _HoxiParsedSessionTurns parsed,
    required List<CodeConvoyPlan> fetchedPlans, required List<AiApproval> approvals, required Map<String, String> statusMap,
  }) {
    final rootIntId = HoxiWorkspaceAdapter.stablePositiveIntId(rootRef.hoxiId);
    final parentByHoxi = <String, String>{for (final s in _sessionsById.values) s.hoxiId: s.parentHoxiId};
    final hoxiByInt = <int, String>{for (final entry in _sessionsById.entries) entry.key: entry.value.hoxiId};
    final childSessions = <AiDevSession>[];
    for (final entry in _sessionsById.entries) {
      final sRef = entry.value;
      if (sRef.hoxiId == rootRef.hoxiId || sRef.parentHoxiId.isEmpty) continue;
      if (HoxiConvoyAdapter.resolveRootHoxiId(sRef.hoxiId, parentByHoxi) != rootRef.hoxiId) continue;
      final sIntId = entry.key, rawBusy = isBusySessionStatusValue(statusMap[sRef.hoxiId]);
      final busy = (rawBusy || _isRecentlyStartedBusy(sIntId)) && !_isSuppressedBusy(sIntId);
      final hasPending = approvals.any((a) => a.sessionId == sIntId);
      final sStage = hasPending ? 'awaiting_approval' : (busy ? 'running' : (_knownStageBySessionId[sIntId] ?? 'idle'));
      childSessions.add(_toDevSession(sIntId, sRef, stage: sStage));
    }
    childSessions.sort((a, b) {
      final aTime = a.createdAt ?? a.updatedAt ?? DateTime.fromMillisecondsSinceEpoch(0);
      final bTime = b.createdAt ?? b.updatedAt ?? DateTime.fromMillisecondsSinceEpoch(0);
      return aTime.compareTo(bTime);
    });
    final built = HoxiConvoyAdapter.buildSessionConvoyAndSubagents(
      rootSessionId: rootIntId, rootHoxiId: rootRef.hoxiId, rootTitle: rootRef.title,
      projectDir: rootRef.projectDir, fetchedPlans: fetchedPlans, childSessions: childSessions,
      hoxiIdByIntId: hoxiByInt, pendingApprovalSessionIds: approvals.map((a) => a.sessionId).toSet(),
      taskToolCalls: parsed.taskToolCalls, chatMessages: parsed.chatMessages, stableId: HoxiWorkspaceAdapter.stablePositiveIntId,
    );
    return (convoyPlans: built.convoyPlans, subagents: built.subagents, childSessions: childSessions);
  }

  AiDevSession _toDevSession(
    int sessionId, _HoxiSessionRef ref, {
    String stage = 'idle', String? approvalPolicy, List<AiDevSession> subagents = const [],
  }) {
    final convoyMeta = extractConvoyMetaFromTitle(ref.title);
    final parentIntId = ref.parentHoxiId.isNotEmpty ? HoxiWorkspaceAdapter.stablePositiveIntId(ref.parentHoxiId) : null;
    return AiDevSession(
      id: sessionId, userId: 1, projectId: ref.projectId, parentSessionId: parentIntId,
      convoyPlanId: convoyMeta?.planId ?? '', convoyTaskId: convoyMeta?.taskId ?? '',
      subagents: subagents, title: ref.title, agentName: ref.runtime, workDir: ref.directory, sourceWorkDir: ref.projectDir,
      worktreeBranch: ref.branch, isolationMode: ref.sandboxMode, status: stage == 'running' ? 'running' : 'active', currentStage: stage,
      approvalPolicy: approvalPolicy ?? ref.approvalPolicy, providerModel: ref.modelId, reasoningEffort: ref.reasoningEffort,
      diffAdditions: ref.diffAdditions, diffDeletions: ref.diffDeletions, lastTaskId: sessionId, currentTaskTitle: ref.title,
      createdAt: ref.createdAt, updatedAt: ref.updatedAt, lastInstructionAt: ref.updatedAt,
    );
  }

  void _patchSessionTitleQuietly(_HoxiSessionRef ref, String title) {
    final trimmed = title.trim();
    if (ref.hoxiId.isEmpty || trimmed.isEmpty) return;
    _apiClient.patch<Map<String, dynamic>>(
      '/api/hoxi/sessions/${Uri.encodeComponent(ref.hoxiId)}',
      queryParameters: {'directory': ref.directory}, data: {'title': trimmed},
    ).then<void>((_) {}).catchError((_) {});
  }

  _HoxiSessionRef _ensureSessionTitleFromTurns(int sessionId, _HoxiSessionRef ref, _HoxiParsedSessionTurns parsed) {
    if (!isPlaceholderSessionTitle(ref.title)) return ref;
    var derived = '';
    for (final msg in parsed.chatMessages) {
      if (!msg.isUser) continue;
      derived = extractSessionTitleFromPrompt(msg.text, hasImage: msg.hasImage);
      if (derived.isNotEmpty && !isPlaceholderSessionTitle(derived)) break;
    }
    if (derived.isEmpty && parsed.runs.isNotEmpty) derived = extractSessionTitleFromPrompt(parsed.runs.last.prompt);
    if (derived.isEmpty || isPlaceholderSessionTitle(derived)) return ref;
    final updated = ref.copyWith(title: derived, updatedAt: DateTime.now());
    _sessionsById[sessionId] = updated;
    _patchSessionTitleQuietly(updated, derived);
    return updated;
  }

  Future<List<AiDevSession>> _fetchSessionsInternal({
    int? projectId, bool recentOnly = false, Duration recentWindow = HoxiWorkspaceAdapter.defaultRecentSessionWindow,
  }) async {
    if (_projectsById.isEmpty) await getProjects();
    final targetProjects = projectId != null && _projectsById.containsKey(projectId) ? [_projectsById[projectId]!] : _projectsById.values.toList();
    final sessions = <AiDevSession>[];
    final seenIds = <int>{};
    final now = DateTime.now();
    final statusByDir = <String, Map<String, String>>{};
    final projectResults = await Future.wait(
      targetProjects.map((proj) async {
        try {
          final results = await Future.wait<dynamic>([
            _apiClient.get<dynamic>('/api/hoxi/sessions', queryParameters: {'directory': proj.workDir}),
            _fetchSessionStatuses(proj.workDir),
          ]);
          return (proj: proj, res: results[0] as dynamic, statusMap: results[1] as Map<String, String>?);
        } catch (_) {
          return null;
        }
      }),
    );

    final rawItems = <({CodeProject proj, Map<String, dynamic> map, String hoxiId, String parentHoxiId, int intId, String workDir, DateTime? createdAt, DateTime? updatedAt})>[];
    final hotWorktreeDirs = <String>{};

    for (final item in projectResults.whereType<({CodeProject proj, dynamic res, Map<String, String>? statusMap})>()) {
      if (item.statusMap case final statusMap?) statusByDir[item.proj.workDir] = statusMap;
      final list = item.res.data is List ? (item.res.data as List) : const [];
      var projectWorktreeRank = 0;
      for (final raw in list.whereType<Map>()) {
        final map = raw.cast<String, dynamic>();
        final hoxiId = (map['id'] ?? '').toString().trim();
        if (hoxiId.isEmpty) continue;
        final parentHoxiId = (map['parentID'] ?? map['parentId'] ?? '').toString().trim();
        final timeMap = map['time'] is Map ? (map['time'] as Map).cast<String, dynamic>() : const <String, dynamic>{};
        if (map['archived'] == true || ((timeMap['archived'] as num?)?.toInt() ?? 0) > 0) continue;
        final intId = HoxiWorkspaceAdapter.stablePositiveIntId(hoxiId);
        if (!seenIds.add(intId)) continue;
        final workDir = (map['directory'] ?? map['worktreePath'] ?? item.proj.workDir).toString().trim();
        final createdMs = (timeMap['created'] as num?)?.toInt() ?? 0, updatedMs = (timeMap['updated'] as num?)?.toInt() ?? createdMs;
        final createdAt = createdMs > 0 ? DateTime.fromMillisecondsSinceEpoch(createdMs) : null;
        final updatedAt = updatedMs > 0 ? DateTime.fromMillisecondsSinceEpoch(updatedMs) : null;
        rawItems.add((proj: item.proj, map: map, hoxiId: hoxiId, parentHoxiId: parentHoxiId, intId: intId, workDir: workDir, createdAt: createdAt, updatedAt: updatedAt));
        if (workDir.isNotEmpty && workDir != item.proj.workDir) {
          if (parentHoxiId.isEmpty) projectWorktreeRank++;
          final prev = _sessionsById[intId], knownStage = _knownStageBySessionId[intId] ?? '';
          final isHot = (parentHoxiId.isEmpty && projectWorktreeRank <= 3) ||
              isRecentTimestamp(updatedAt ?? createdAt ?? prev?.updatedAt, now: now, window: recentWindow) ||
              isBusySessionStatusValue(knownStage) || knownStage == 'awaiting_approval' ||
              _isRecentlyStartedBusy(intId) ||
              isBusySessionStatusValue((map['status'] ?? (map['busy'] == true ? 'busy' : '')).toString());
          if (isHot && !statusByDir.containsKey(workDir)) hotWorktreeDirs.add(workDir);
        }
      }
    }

    if (hotWorktreeDirs.isNotEmpty) {
      final extraEntries = await Future.wait(hotWorktreeDirs.map((dir) async => MapEntry(dir, await _fetchSessionStatuses(dir))));
      for (final entry in extraEntries) {
        if (entry.value case final statusMap?) statusByDir[entry.key] = statusMap;
      }
    }

    final parentByHoxiId = <String, String>{for (final e in rawItems) e.hoxiId: e.parentHoxiId};
    final builtByHoxiId = <String, AiDevSession>{};

    for (final entry in rawItems) {
      final proj = entry.proj, map = entry.map, hoxiId = entry.hoxiId, intId = entry.intId;
      final workDir = entry.workDir, createdAt = entry.createdAt, updatedAt = entry.updatedAt;
      final effectiveDir = workDir.isNotEmpty ? workDir : proj.workDir;
      final sourceDir = _HoxiSessionTurnParsing.resolveSourceWorkDir(map, workDir, proj.workDir);
      final runtime = (map['runtime'] ?? 'opencode').toString().trim();
      final sandboxMode = HoxiSessionDiffScope.resolveSandboxMode(map, workDir);
      final branch = (map['branch'] ?? '').toString(), title = (map['title'] ?? 'New Session').toString();
      final modelId = extractSessionModelFromMap(map, runtime: runtime);
      final diffStats = extractSessionDiffStats(map), rawEffort = (map['reasoningEffort'] ?? '').toString().trim();
      final rawStatus = statusByDir[effectiveDir]?[hoxiId] ?? statusByDir[proj.workDir]?[hoxiId] ?? (map['status'] ?? (map['busy'] == true ? 'busy' : '')).toString();
      final rawBusy = isBusySessionStatusValue(rawStatus) ||
          (!statusByDir.containsKey(effectiveDir) &&
              isBusySessionStatusValue(_knownStageBySessionId[intId]));
      if (rawBusy) _startedUntilBySessionId.remove(intId);
      final isBusy = (rawBusy || _isRecentlyStartedBusy(intId)) && !_isSuppressedBusy(intId);
      final prev = _sessionsById[intId];
      final keepTitle = prev != null && prev.title.trim().isNotEmpty &&
          (prev.hasLocalTitleOverride || (!isPlaceholderSessionTitle(prev.title) && isPlaceholderSessionTitle(title)));
      final keepModel = prev != null && prev.modelId.isNotEmpty && (prev.hasLocalModelOverride || modelId.isEmpty);
      final keepEffort = prev != null && prev.reasoningEffort.isNotEmpty && (prev.hasLocalEffortOverride || rawEffort.isEmpty);
      final resolvedTitle = keepTitle ? prev.title : title, resolvedModelId = keepModel ? prev.modelId : modelId;
      final resolvedRuntime = keepModel && prev.runtime.isNotEmpty
          ? prev.runtime
          : CodeModelCatalog.resolveRuntimeForModel(
              model: resolvedModelId, explicitExecutorId: runtime,
              executors: _catalogAdapter.cachedExecutors, fallbackRuntime: runtime.isEmpty ? (prev?.runtime ?? 'opencode') : runtime,
            );
      final reasoningEffort = keepEffort ? prev.reasoningEffort : (rawEffort.isNotEmpty ? rawEffort : (prev?.reasoningEffort ?? ''));
      final approvalPolicy = prev?.approvalPolicy ?? 'safe_auto';
      final stage = isBusy ? 'running' : (_knownStageBySessionId[intId] == 'awaiting_approval' ? 'awaiting_approval' : 'idle');
      _knownStageBySessionId[intId] = stage;
      final hasDiff = diffStats.additions > 0 || diffStats.deletions > 0;
      final nextRef = _HoxiSessionRef(
        hoxiId: hoxiId, parentHoxiId: entry.parentHoxiId,
        directory: effectiveDir, projectDir: sourceDir.isNotEmpty ? sourceDir : proj.workDir,
        projectId: proj.id, runtime: resolvedRuntime, title: resolvedTitle, sandboxMode: sandboxMode, branch: branch,
        modelId: resolvedModelId, reasoningEffort: reasoningEffort, approvalPolicy: approvalPolicy,
        diffAdditions: hasDiff ? diffStats.additions : (prev?.diffAdditions ?? 0),
        diffDeletions: hasDiff ? diffStats.deletions : (prev?.diffDeletions ?? 0),
        hasLocalTitleOverride: (prev?.hasLocalTitleOverride ?? false) && title.trim() != prev!.title.trim(),
        hasLocalModelOverride: prev?.hasLocalModelOverride ?? false,
        hasLocalEffortOverride: keepEffort, createdAt: createdAt, updatedAt: updatedAt ?? prev?.updatedAt,
      );
      _sessionsById[intId] = nextRef;
      builtByHoxiId[hoxiId] = _toDevSession(intId, nextRef, stage: stage, approvalPolicy: approvalPolicy);
    }

    final childrenByRootHoxiId = <String, List<AiDevSession>>{};
    for (final entry in rawItems) {
      if (entry.parentHoxiId.isEmpty) continue;
      final rootHoxi = HoxiConvoyAdapter.resolveRootHoxiId(entry.hoxiId, parentByHoxiId);
      if (rootHoxi.isNotEmpty && rootHoxi != entry.hoxiId && builtByHoxiId.containsKey(rootHoxi)) {
        final childDev = builtByHoxiId[entry.hoxiId];
        if (childDev != null) childrenByRootHoxiId.putIfAbsent(rootHoxi, () => <AiDevSession>[]).add(childDev);
      }
    }

    for (final entry in rawItems) {
      if (entry.parentHoxiId.isNotEmpty) continue;
      final base = builtByHoxiId[entry.hoxiId];
      if (base == null) continue;
      final children = childrenByRootHoxiId[entry.hoxiId] ?? const <AiDevSession>[];
      if (children.length > 1) {
        children.sort((a, b) {
          final aTime = a.createdAt ?? a.updatedAt ?? DateTime.fromMillisecondsSinceEpoch(0);
          final bTime = b.createdAt ?? b.updatedAt ?? DateTime.fromMillisecondsSinceEpoch(0);
          return aTime.compareTo(bTime);
        });
      }
      final devSession = children.isEmpty ? base : base.copyWith(subagents: List<AiDevSession>.unmodifiable(children));
      final anyHotChild = children.any((c) => isHotAiDevSession(c, now: now, window: recentWindow));
      if (!recentOnly || anyHotChild || isHotAiDevSession(devSession, now: now, window: recentWindow)) sessions.add(devSession);
    }

    sessions.sort((a, b) {
      final aBusy = a.status == 'running' || a.currentStage == 'awaiting_approval' || a.runningSubagentsCount > 0;
      final bBusy = b.status == 'running' || b.currentStage == 'awaiting_approval' || b.runningSubagentsCount > 0;
      if (aBusy != bBusy) return aBusy ? -1 : 1;
      final aTime = a.updatedAt ?? a.createdAt ?? DateTime.fromMillisecondsSinceEpoch(0);
      final bTime = b.updatedAt ?? b.createdAt ?? DateTime.fromMillisecondsSinceEpoch(0);
      return bTime.compareTo(aTime);
    });
    return sessions;
  }

  Future<void> _syncSessionRuntime(String hoxiId, String directory, String runtime, String modelId) async {
    if (hoxiId.isEmpty) return;
    final modelPayload = buildPromptModelPayload(runtime, modelId);
    try {
      await _apiClient.post<Map<String, dynamic>>(
        '/api/hoxi/sessions/$hoxiId/runtime',
        queryParameters: {'directory': directory},
        data: {
          'runtime': runtime,
          if (modelPayload != null) ...{
            if ((modelPayload['providerID'] ?? '').isNotEmpty) 'providerID': modelPayload['providerID'],
            if ((modelPayload['modelID'] ?? '').isNotEmpty) 'modelID': modelPayload['modelID'],
          },
        },
      );
    } catch (_) {}
  }
}
