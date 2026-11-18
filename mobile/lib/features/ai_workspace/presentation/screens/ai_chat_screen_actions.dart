part of 'ai_chat_screen.dart';

extension _AiChatScreenActions on _AiChatScreenState {
  List<int> _computeSearchMatches(List<ChatMessage> messages, String query) {
    final q = query.trim().toLowerCase();
    if (q.isEmpty) return const [];
    final matches = <int>[];
    for (var i = 0; i < messages.length; i++) {
      final msg = messages[i];
      if (msg.id == 'welcome') continue;
      if (msg.text.toLowerCase().contains(q) ||
          msg.reasoning.toLowerCase().contains(q) ||
          msg.toolSteps.any((s) => s.toLowerCase().contains(q))) {
        matches.add(i);
      }
    }
    return matches;
  }

  void _toggleSearch() {
    _updateScreenState(() {
      _searchOpen = !_searchOpen;
      if (!_searchOpen) {
        _searchController.clear();
        _searchQuery = '';
        _activeMatchCursor = 0;
      } else {
        _visibleMessageLimit = math.max(
          _visibleMessageLimit,
          ref.read(aiWorkspaceControllerProvider).chatHistory.length,
        );
      }
    });
  }

  void _editQueuedPrompt(String id) {
    final popped = _workspaceController.popQueuedPromptForEdit(id);
    if (popped == null) return;
    _setInputText(popped.text);
    _updateScreenState(() {
      _pendingImageDataUrl = popped.imageDataUrl;
      _attachedFiles = List<String>.of(popped.attachedFiles);
      _instructionOptions = popped.options;
    });
    _focusNode.requestFocus();
  }

  void _editUserMessage(String text) {
    _setInputText(text);
    _focusNode.requestFocus();
  }

  void _openSessionSheet() {
    dismissChatKeyboard(context);
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      showDragHandle: false,
      builder: (_) => const CodeSessionSheet(),
    );
  }

  void _openGitReview() {
    final session = ref.read(aiWorkspaceControllerProvider).currentSession;
    if (session == null) return;
    Navigator.of(context).push(
      MaterialPageRoute(builder: (_) => CodeGitReviewScreen(session: session)),
    );
  }

  void _openFileDiff(String path) {
    final session = ref.read(aiWorkspaceControllerProvider).currentSession;
    if (session == null) return;
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => CodeGitDiffScreen(
          sessionId: session.id,
          repositoryId: 'primary',
          path: path,
          kind: 'working',
        ),
      ),
    );
  }

  void _openWorkspaceFile(String? path) {
    final session = ref.read(aiWorkspaceControllerProvider).currentSession;
    if (session == null) return;
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => CodeWorkspaceFilesScreen(
          sessionId: session.id,
          sessionTitle: _sessionTitle(session),
          initialPath: path,
        ),
      ),
    );
  }

  String _sessionTitle(AiDevSession session) => session.title.isEmpty
      ? context.l10n.codeWsChatSessionFallback(session.id)
      : session.title;

  void _openRecovery() {
    final session = ref.read(aiWorkspaceControllerProvider).currentSession;
    if (session == null) return;
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => CodeSessionRecoveryScreen(
          sessionId: session.id,
          sessionTitle: _sessionTitle(session),
        ),
      ),
    );
  }

  void _openTaskDetail() {
    final state = ref.read(aiWorkspaceControllerProvider);
    final session = state.currentSession;
    if (session == null) return;
    final task = state.tasks.where((t) => t.sessionId == session.id).firstOrNull;
    final resolvedProjectId = task?.projectId ?? session.projectId;
    final projectName = resolveWorkspaceProjectName(
      projectId: resolvedProjectId,
      projects: state.projects,
      sourceWorkDir: session.sourceWorkDir,
      workDir: session.workDir,
    );
    final entity = buildAiTaskEntity(
      taskId: task?.id ?? (session.lastTaskId > 0 ? session.lastTaskId : session.id),
      sessionId: session.id,
      projectId: resolvedProjectId,
      agentName: session.agentName.isNotEmpty ? session.agentName : (task?.agentName ?? 'opencode'),
      providerModel: session.providerModel.isNotEmpty ? session.providerModel : (task?.providerModel ?? ''),
      projectName: projectName,
      sourceWorkDir: session.sourceWorkDir,
      diffAdditions: session.diffAdditions > 0 ? session.diffAdditions : state.diffAdditions,
      diffDeletions: session.diffDeletions > 0 ? session.diffDeletions : state.diffDeletions,
      title: _sessionTitle(session),
      workDir: session.workDir,
      status: session.status,
      currentStage: state.currentStage.isNotEmpty ? state.currentStage : session.currentStage,
      createdAt: session.createdAt,
      updatedAt: session.updatedAt,
    );
    unawaited(FeatureRouteRegistry.openTaskDetail(context, entity));
  }

  void _openSessionSettings() {
    if (ref.read(aiWorkspaceControllerProvider).currentSession == null) return;
    dismissChatKeyboard(context);
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      showDragHandle: true,
      builder: (_) => const CodeSessionSettingsSheet(),
    );
  }

  Future<void> _openModelSelector() async {
    final session = ref.read(aiWorkspaceControllerProvider).currentSession;
    if (session == null) return;
    dismissChatKeyboard(context);
    final executors = await ref
        .read(aiWorkspaceControllerProvider.notifier)
        .ensureExecutorsLoaded();
    if (!mounted) return;
    final executor = executors
        .where((item) => item.id == session.agentName)
        .firstOrNull;
    final selection = await CodeModelSelectorSheet.show(
      context,
      executors: executors,
      selectedExecutor: executor,
      selectedModel: session.providerModel,
      selectedEffort: session.reasoningEffort,
      executorIdHint: session.agentName,
      allowCrossExecutorSelection: true,
    );
    if (selection == null || !mounted) return;
    final updated = await ref
        .read(aiWorkspaceControllerProvider.notifier)
        .updateSessionModel(
          model: selection.model,
          executorId: selection.executorId,
          reasoningEffort: selection.reasoningEffort,
        );
    if (updated && selection.model.trim().isNotEmpty) {
      unawaited(
        CodeModelCatalog.saveLastSelection(
          model: selection.model,
          executorId: selection.executorId ?? session.agentName,
        ),
      );
    }
  }

  Future<void> _pickImage(ImageSource source) async {
    dismissChatKeyboard(context);
    try {
      final dataUrl = await const ChatCameraCapture().pickImageDataUrl(
        source: source,
      );
      if (!mounted || dataUrl == null || dataUrl.isEmpty) return;
      _updateScreenState(() => _pendingImageDataUrl = dataUrl);
    } on ChatCameraException catch (e) {
      if (!mounted || e.failure == ChatCameraFailure.cancelled) return;
      final msg = e.failure == ChatCameraFailure.denied
          ? context.l10n.chatCameraPermissionDenied
          : context.l10n.chatCameraUnavailable;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
    }
  }

  Future<void> _pickWorkspaceFile() async {
    final session = ref.read(aiWorkspaceControllerProvider).currentSession;
    if (session == null) return;
    final path = await CodeWorkspaceFilePickerSheet.show(
      context,
      sessionId: session.id,
    );
    if (!mounted || path == null || path.trim().isEmpty) return;
    if (!_attachedFiles.contains(path)) {
      _updateScreenState(() => _attachedFiles = [..._attachedFiles, path]);
    }
  }

  Future<void> _pickWorkspaceSkill() async {
    final session = ref.read(aiWorkspaceControllerProvider).currentSession;
    if (session == null) return;
    final dir = session.sourceWorkDir.isNotEmpty
        ? session.sourceWorkDir
        : session.workDir;
    final selected = await CodeSkillPickerSheet.show(context, directory: dir);
    if (!mounted || selected == null) return;
    _workspaceController.selectComposerSkill(selected);
  }

  void _openTerminal() {
    final state = ref.read(aiWorkspaceControllerProvider);
    final session = state.currentSession;
    if (session == null) return;
    final executor = state.executors
        .where((item) => item.id == session.agentName)
        .firstOrNull;
    final projectName =
        state.projects
            .where((item) => item.id == session.projectId)
            .firstOrNull
            ?.name ??
        '开发项目';
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => CodeTerminalScreen(
          session: session,
          terminalId: session.id,
          task: state.currentTask,
          nativeProtocol:
              executor?.nativeTerminal ?? session.agentName != 'terminal',
          projectName: projectName,
        ),
      ),
    );
  }

  Future<void> _confirmStop() async {
    final confirmed = await showAiChatStopConfirmDialog(context);
    if (!confirmed || !mounted) return;
    final stopped = await ref
        .read(aiWorkspaceControllerProvider.notifier)
        .stopCurrentSession();
    if (!mounted || !stopped) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text(context.l10n.codeWsStopSuccess)));
  }

  Future<void> _confirmDelivery() async {
    final controller = ref.read(aiWorkspaceControllerProvider.notifier);
    final review = await controller.loadDeliveryReview();
    if (!mounted || review == null) return;
    if (!review.ready || review.revision.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(context.l10n.codeWsDeliveryReviewNotReady)),
      );
      return;
    }
    final confirmed = await showAiChatDeliveryConfirmDialog(
      context,
      files: review.files,
      additions: review.additions,
      deletions: review.deletions,
    );
    if (!confirmed || !mounted) return;
    final started = await controller.startDelivery(
      reviewRevision: review.revision,
    );
    if (!mounted || !started) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text(context.l10n.codeWsDeliveryStarted)));
  }
}
