part of 'ai_workspace_controller.dart';

/// 编队生命周期控制（启动/取消/重试/合并/汇总）、会话元数据修改与运行期指令队列（QueuedPrompts）mixin。
mixin _AiWorkspaceConvoyAndQueue on Notifier<AiWorkspaceState> {
  AiWorkspaceRepository get _repo;
  void _notifyTaskCenterSession(
    AiDevSession session, {
    String? currentStage,
    AiSessionStateInfo? stateInfo,
  });
  Future<void> selectSession(AiDevSession session);
  Future<void> refreshCurrentSession({
    bool showLoading = false,
    bool trustServerState = false,
  });
  Future<bool> sendMessage(
    String text, {
    CodeInstructionOptions options,
    String? imageDataUrl,
    List<String> attachedFiles,
    List<LocalPickedDocument> localDocuments,
    NodeSkillSelection? skillSelection,
    bool bypassQueue,
    bool useComposerSkill,
  });

  String? _sendingQueuedPromptId;

  void selectComposerSkill(NodeSkillSelection? skill) {
    if (skill == null) {
      state = state.copyWith(clearSelectedSkill: true);
    } else {
      state = state.copyWith(selectedSkill: skill);
    }
  }

  Future<bool> updateSessionTitle(String title) async {
    final nodeRequest = NodeRequestGuard(_repo);
    final session = state.currentSession;
    if (session == null || title.trim().isEmpty || state.isActionLoading) {
      return false;
    }
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
    state = state.copyWith(isActionLoading: true, clearError: true);
    try {
      final updated = await nodeRequest.repository.updateSessionTitle(
        session.id,
        title,
      );
      _replaceSession(updated);
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      state = state.copyWith(isActionLoading: false);
      return true;
    } catch (error) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      state = state.copyWith(
        isActionLoading: false,
        errorMessage: '会话名称更新失败：$error',
      );
      return false;
    }
  }

  Future<bool> updateApprovalPolicy(String policy) async {
    final nodeRequest = NodeRequestGuard(_repo);
    final session = state.currentSession;
    if (session == null || policy.trim().isEmpty || state.isActionLoading) {
      return false;
    }
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
    state = state.copyWith(isActionLoading: true, clearError: true);
    try {
      final updated = await nodeRequest.repository.updateApprovalPolicy(
        session.id,
        policy,
      );
      _replaceSession(updated);
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      state = state.copyWith(isActionLoading: false);
      return true;
    } catch (error) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      state = state.copyWith(
        isActionLoading: false,
        errorMessage: '审批策略更新失败：$error',
      );
      return false;
    }
  }

  Future<bool> updateSessionModel({
    String? model,
    String? executorId,
    String? reasoningEffort,
  }) async {
    final nodeRequest = NodeRequestGuard(_repo);
    final session = state.currentSession;
    if (session == null || !ref.mounted || !nodeRequest.isCurrent(_repo)) {
      return false;
    }
    try {
      final updated = await nodeRequest.repository.updateSessionModel(
        session.id,
        model: model,
        executorId: executorId,
        reasoningEffort: reasoningEffort,
      );
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      final effectiveModel = updated.providerModel.trim().isNotEmpty
          ? updated.providerModel.trim()
          : (model ?? '').trim();
      if (effectiveModel.isNotEmpty) {
        unawaited(
          CodeModelCatalog.saveLastSelection(
            model: effectiveModel,
            executorId: updated.agentName.trim().isNotEmpty
                ? updated.agentName.trim()
                : (executorId ?? session.agentName).trim(),
          ),
        );
      }
      _replaceSession(updated);
      return true;
    } catch (error) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      state = state.copyWith(errorMessage: '会话模型更新失败：$error');
      return false;
    }
  }

  void _replaceSession(AiDevSession updated) {
    final prev = state.currentSession;
    final merged = (prev != null && prev.id == updated.id && state.isStreaming)
        ? updated.copyWith(status: prev.status, currentStage: prev.currentStage)
        : updated;
    final nextTask = syncTaskSummaryTitle(state.currentTask, merged);
    state = state.copyWith(
      currentSession: merged,
      currentTask: nextTask,
      sessions: upsertWorkspaceSession(state.sessions, merged),
      tasks: syncWorkspaceTasksWithSession(
        state.tasks,
        merged,
        currentTask: nextTask,
      ),
    );
    _notifyTaskCenterSession(merged);
  }

  Future<void> openSessionById(int sessionId) async {
    if (sessionId <= 0 || state.currentSession?.id == sessionId) return;
    AiDevSession? target = state.sessions
        .where((s) => s.id == sessionId)
        .firstOrNull;
    if (target == null) {
      for (final root in state.sessions) {
        final child = root.subagents.where((s) => s.id == sessionId).firstOrNull;
        if (child != null) {
          target = child;
          break;
        }
      }
    }
    target ??= state.currentSession?.subagents
        .where((s) => s.id == sessionId)
        .firstOrNull;
    if (target != null) {
      await selectSession(target);
      return;
    }
    final nodeRequest = NodeRequestGuard(_repo);
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return;
    try {
      final sessionState = await nodeRequest.repository.getSessionState(sessionId);
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return;
      await selectSession(sessionState.session);
    } catch (error) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return;
      state = state.copyWith(errorMessage: '打开子会话失败：$error');
    }
  }

  void enqueueFollowUpPrompt({
    required int sessionId,
    required String text,
    String? imageDataUrl,
    List<String> attachedFiles = const [],
    List<LocalPickedDocument> localDocuments = const [],
    NodeSkillSelection? skillSelection,
    CodeInstructionOptions options = const CodeInstructionOptions(),
  }) {
    final now = DateTime.now();
    final item = CodeQueuedPromptItem(
      id: 'queued-${now.microsecondsSinceEpoch}',
      sessionId: sessionId,
      text: text.trim(),
      imageDataUrl: imageDataUrl,
      attachedFiles: List<String>.unmodifiable(attachedFiles),
      localDocuments: List<LocalPickedDocument>.unmodifiable(localDocuments),
      skillSelection: skillSelection,
      options: options,
      createdAt: now,
    );
    state = state.copyWith(
      queuedPrompts: [...state.queuedPrompts, item],
      clearSelectedSkill: true,
    );
  }

  void removeQueuedPrompt(String id) {
    if (_sendingQueuedPromptId == id) return;
    state = state.copyWith(
      queuedPrompts: state.queuedPrompts.where((q) => q.id != id).toList(),
    );
  }

  CodeQueuedPromptItem? popQueuedPromptForEdit(String id) {
    if (_sendingQueuedPromptId == id) return null;
    final item = state.queuedPrompts.where((q) => q.id == id).firstOrNull;
    if (item == null || item.sessionId != state.currentSession?.id) return null;
    state = state.copyWith(
      queuedPrompts: state.queuedPrompts.where((q) => q.id != id).toList(),
      selectedSkill: item.skillSelection,
      clearSelectedSkill: item.skillSelection == null,
    );
    return item;
  }

  Future<bool> sendQueuedPromptNow(String id) => _sendQueuedPrompt(id);

  Future<bool> _sendQueuedPrompt(String id, {bool automatic = false}) async {
    if (!ref.mounted || _sendingQueuedPromptId != null || state.isSending ||
        (automatic && state.isAiThinking)) {
      return false;
    }
    final item = state.queuedPrompts.where((q) => q.id == id).firstOrNull;
    if (item == null || item.sessionId != state.currentSession?.id ||
        (automatic && item.requiresManualRetry)) {
      return false;
    }
    final nodeRequest = NodeRequestGuard(_repo);
    _sendingQueuedPromptId = id;
    var sent = false;
    try {
      sent = await sendMessage(
        item.text,
        options: item.options,
        imageDataUrl: item.imageDataUrl,
        attachedFiles: item.attachedFiles,
        localDocuments: item.localDocuments,
        skillSelection: item.skillSelection,
        bypassQueue: true,
        useComposerSkill: false,
      );
      return sent;
    } finally {
      _sendingQueuedPromptId = null;
      if (ref.mounted && nodeRequest.isCurrent(_repo)) {
        state = state.copyWith(queuedPrompts: [
          for (final queued in state.queuedPrompts)
            if (queued.id != id) queued else if (!sent) queued.waitForManualRetry(),
        ]);
        if (sent) drainNextQueuedPromptIfIdle();
      }
    }
  }

  void drainNextQueuedPromptIfIdle() {
    if (!ref.mounted || _sendingQueuedPromptId != null || state.isAiThinking) return;
    final next = state.currentSessionQueuedPrompts.firstOrNull;
    if (next == null || next.requiresManualRetry) return;
    final nodeRequest = NodeRequestGuard(_repo);
    unawaited(Future.microtask(() async {
      if (ref.mounted && nodeRequest.isCurrent(_repo) &&
          state.currentSessionQueuedPrompts.firstOrNull?.id == next.id) {
        await _sendQueuedPrompt(next.id, automatic: true);
      }
    }));
  }

  Future<bool> startConvoyPlan(CodeConvoyPlan draft) async {
    final sessionId = state.currentSession?.id;
    if (sessionId == null || state.isActionLoading) return false;
    final nodeRequest = NodeRequestGuard(_repo);
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
    state = state.copyWith(isActionLoading: true, clearError: true);
    try {
      final created = await nodeRequest.repository.startConvoyPlan(
        sessionId,
        draft,
      );
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      final nextPlans = <CodeConvoyPlan>[
        created,
        ...state.convoyPlans.where((p) => p.id != created.id),
      ];
      state = state.copyWith(isActionLoading: false, convoyPlans: nextPlans);
      await refreshCurrentSession();
      return true;
    } catch (_) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      state = state.copyWith(isActionLoading: false);
      final taskLines = draft.tasks
          .map(
            (t) =>
                '- [${t.id}]${t.dependsOn.isNotEmpty ? " (依赖: ${t.dependsOn.join(", ")})" : ""}: ${t.prompt}',
          )
          .join('\n');
      final fallbackPrompt =
          '请使用子任务（task 工具）按以下编排并行执行（并发上限 ${draft.concurrency}）：\n$taskLines';
      return sendMessage(fallbackPrompt, bypassQueue: true);
    }
  }

  Future<bool> cancelConvoyPlan(String planId) async {
    final sessionId = state.currentSession?.id;
    if (sessionId == null || planId.trim().isEmpty) return false;
    final nodeRequest = NodeRequestGuard(_repo);
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
    try {
      final updated = await nodeRequest.repository.cancelConvoyPlan(
        sessionId,
        planId,
      );
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      _upsertConvoyPlanInState(updated);
      await refreshCurrentSession();
      return true;
    } catch (error) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      state = state.copyWith(errorMessage: '取消编队失败：$error');
      return false;
    }
  }

  Future<bool> retryConvoyPlan(String planId) async {
    final sessionId = state.currentSession?.id;
    if (sessionId == null || planId.trim().isEmpty) return false;
    final nodeRequest = NodeRequestGuard(_repo);
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
    try {
      final updated = await nodeRequest.repository.retryConvoyPlan(
        sessionId,
        planId,
      );
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      _upsertConvoyPlanInState(updated);
      await refreshCurrentSession();
      return true;
    } catch (error) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      state = state.copyWith(errorMessage: '重试编队失败：$error');
      return false;
    }
  }

  Future<bool> mergeConvoyPlan(String planId) async {
    final sessionId = state.currentSession?.id;
    if (sessionId == null || planId.trim().isEmpty) return false;
    final nodeRequest = NodeRequestGuard(_repo);
    if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
    try {
      final updated = await nodeRequest.repository.mergeConvoyPlan(
        sessionId,
        planId,
      );
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      _upsertConvoyPlanInState(updated);
      await refreshCurrentSession();
      return true;
    } catch (error) {
      if (!ref.mounted || !nodeRequest.isCurrent(_repo)) return false;
      state = state.copyWith(errorMessage: '合并编队分支失败：$error');
      return false;
    }
  }

  Future<bool> summarizeConvoyPlanInChat(CodeConvoyPlan plan) {
    return sendMessage(
      buildConvoyCompletionSummaryPrompt(plan),
      bypassQueue: true,
    );
  }

  void _upsertConvoyPlanInState(CodeConvoyPlan updated) {
    final idx = state.convoyPlans.indexWhere((p) => p.id == updated.id);
    final next = List<CodeConvoyPlan>.of(state.convoyPlans);
    if (idx >= 0) {
      next[idx] = updated;
    } else {
      next.insert(0, updated);
    }
    state = state.copyWith(convoyPlans: next);
  }
}
