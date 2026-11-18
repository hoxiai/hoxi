import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';

import '../../../../core/storage/active_node_provider.dart';
import '../../../../core/storage/storage_service.dart';
import '../../../../shared/widgets/chat_keyboard_dismiss.dart';
import '../../../../shared/widgets/stream_text_pacer.dart';
import '../../../../shared/utils/chat_camera_capture.dart';
import '../../data/task_ai_utils.dart';
import '../../../../shared/navigation/feature_route_registry.dart';
import '../../models/ai_dev_session.dart';
import '../../models/chat_message.dart';
import '../../models/code_instruction_options.dart';
import '../../models/code_model_catalog.dart';
import '../../models/session_context_label.dart';
import '../controllers/ai_workspace_controller.dart';
import '../widgets/ai_approval_prompt.dart';
import '../widgets/ai_chat_message_item.dart';
import '../widgets/ai_chat_state_widgets.dart';
import '../widgets/ai_chat_timeline_extras.dart';
import '../widgets/ai_convoy_widgets.dart';
import '../widgets/ai_preview_strip.dart';
import '../widgets/ai_timeline_panel.dart';
import '../widgets/code_delivery_card.dart';
import '../widgets/code_flow_card.dart';
import '../widgets/code_model_selector_sheet.dart';
import '../widgets/code_session_initializing_banner.dart';
import '../widgets/code_instruction_composer.dart';
import '../widgets/hoxi_session_skeleton.dart';
import '../widgets/task_context_app_bar_widgets.dart';
import 'ai_preview_detail_screen.dart';
import 'ai_preview_list_screen.dart';
import 'code_git_diff_screen.dart';
import 'code_git_review_screen.dart';
import 'code_session_sheet.dart';
import '../widgets/code_session_settings_sheet.dart';
import 'code_session_recovery_screen.dart';
import 'code_workspace_files_screen.dart';
import 'code_terminal_screen.dart';
import '../../../../core/l10n/l10n.dart';
import '../../../../core/theme/app_theme.dart';

part 'ai_chat_screen_actions.dart';

class AiChatScreen extends ConsumerStatefulWidget {
  const AiChatScreen({super.key});

  @override
  ConsumerState<AiChatScreen> createState() => _AiChatScreenState();
}

class _AiChatScreenState extends ConsumerState<AiChatScreen>
    with WidgetsBindingObserver {
  final _inputController = TextEditingController();
  final _searchController = TextEditingController();
  final _scrollController = ScrollController();
  final _focusNode = FocusNode();
  Timer? _draftTimer, _convoyPollTimer;
  int? _draftSessionId;
  bool _restoringDraft = false, _stickToBottom = true, _showScrollToBottomFab = false;
  bool _searchOpen = false, _scrollToBottomScheduled = false, _pendingJumpToBottom = false;
  String _searchQuery = '';
  int _activeMatchCursor = 0, _autoScrollGen = 0, _visibleMessageLimit = kInitialVisibleMessages;
  bool _prependingEarlier = false;
  CodeInstructionOptions _instructionOptions = const CodeInstructionOptions();
  String? _pendingImageDataUrl;
  List<String> _attachedFiles = const [];
  late final AiWorkspaceController _workspaceController;

  void _updateScreenState(VoidCallback fn) {
    if (mounted) setState(fn);
  }

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _workspaceController = ref.read(aiWorkspaceControllerProvider.notifier);
    _inputController.addListener(_scheduleDraftSave);
    _scrollController.addListener(_onScroll);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      final initialState = ref.read(aiWorkspaceControllerProvider);
      _activateDraftSession(initialState.currentSession?.id);
      _syncConvoyPollTimer(initialState.hasActiveConvoyOrSubagent);
      _scrollToBottom();
      unawaited(_workspaceController.resumeConversationStream());
    });
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    unawaited(_workspaceController.onChatScreenExited());
    _draftTimer?.cancel();
    _convoyPollTimer?.cancel();
    final sessionId = _draftSessionId;
    if (sessionId != null) {
      unawaited(StorageService.saveCodeInstructionDraft(sessionId, _inputController.text));
    }
    _inputController.removeListener(_scheduleDraftSave);
    _scrollController.removeListener(_onScroll);
    _inputController.dispose();
    _searchController.dispose();
    _scrollController.dispose();
    _focusNode.dispose();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState lifecycleState) {
    if (lifecycleState == AppLifecycleState.resumed) {
      unawaited(_workspaceController.resumeConversationStream());
    } else if (lifecycleState == AppLifecycleState.inactive ||
        lifecycleState == AppLifecycleState.paused ||
        lifecycleState == AppLifecycleState.detached) {
      unawaited(_workspaceController.suspendConversationStream());
    }
  }

  void _syncConvoyPollTimer(bool shouldPoll) {
    if (!shouldPoll) {
      _convoyPollTimer?.cancel();
      _convoyPollTimer = null;
      return;
    }
    if (_convoyPollTimer != null) return;
    _convoyPollTimer = Timer.periodic(const Duration(milliseconds: 2500), (_) {
      if (!mounted) return;
      unawaited(_workspaceController.refreshCurrentSession());
    });
  }

  Future<void> _sendMessage() async {
    final text = _inputController.text.trim();
    final hasImage = _pendingImageDataUrl?.trim().isNotEmpty ?? false;
    final hasSkill = ref.read(aiWorkspaceControllerProvider).selectedSkill != null;
    if (text.isEmpty && !hasImage && _attachedFiles.isEmpty && !hasSkill) return;
    final sessionId = _draftSessionId;
    final sent = await ref.read(aiWorkspaceControllerProvider.notifier).sendMessage(
      text, options: _instructionOptions, imageDataUrl: _pendingImageDataUrl, attachedFiles: _attachedFiles,
    );
    if (!mounted || !sent || _draftSessionId != sessionId) return;
    dismissChatKeyboard(context);
    _setInputText('');
    setState(() {
      _pendingImageDataUrl = null;
      _attachedFiles = const [];
    });
    if (sessionId != null) await StorageService.saveCodeInstructionDraft(sessionId, '');
    _scrollToBottom(animate: true);
  }

  void _scheduleDraftSave() {
    if (_restoringDraft || _draftSessionId == null) return;
    _draftTimer?.cancel();
    final sessionId = _draftSessionId!;
    _draftTimer = Timer(const Duration(milliseconds: 400), () {
      if (_draftSessionId != sessionId) return;
      unawaited(StorageService.saveCodeInstructionDraft(sessionId, _inputController.text));
    });
  }

  void _activateDraftSession(int? sessionId) {
    if (_draftSessionId == sessionId) return;
    _draftTimer?.cancel();
    final previousSessionId = _draftSessionId;
    if (previousSessionId != null) {
      unawaited(StorageService.saveCodeInstructionDraft(previousSessionId, _inputController.text));
    }
    _draftSessionId = sessionId;
    _pendingImageDataUrl = null;
    _attachedFiles = const [];
    _setInputText(sessionId == null ? '' : StorageService.getCodeInstructionDraft(sessionId));
  }

  void _setInputText(String text) {
    _restoringDraft = true;
    _inputController.value = TextEditingValue(text: text, selection: TextSelection.collapsed(offset: text.length));
    _restoringDraft = false;
  }

  void _onScroll() {
    if (!_scrollController.hasClients || _prependingEarlier) return;
    final position = _scrollController.position;
    final distance = position.maxScrollExtent - position.pixels;
    if (distance <= 24 && position.userScrollDirection != ScrollDirection.forward) {
      _stickToBottom = true;
    }
    final showFab = distance > 140;
    if (showFab != _showScrollToBottomFab) {
      setState(() => _showScrollToBottomFab = showFab);
    }
  }

  bool _onScrollNotification(ScrollNotification notification) {
    if (notification.depth != 0 || _prependingEarlier) return false;
    if (notification is UserScrollNotification && notification.direction == ScrollDirection.forward) {
      _stickToBottom = false;
      _autoScrollGen++;
    } else if (notification is ScrollUpdateNotification &&
        notification.dragDetails != null &&
        (notification.scrollDelta ?? 0) < 0) {
      _stickToBottom = false;
      _autoScrollGen++;
    } else if (notification is ScrollEndNotification) {
      final m = notification.metrics;
      if ((m.maxScrollExtent - m.pixels) <= 24) _stickToBottom = true;
    }
    return false;
  }

  void _loadEarlierMessages(int totalCount, {bool all = false}) {
    if (_prependingEarlier || _visibleMessageLimit >= totalCount) return;
    _prependingEarlier = true;
    _stickToBottom = false;
    _autoScrollGen++;
    final hasClients = _scrollController.hasClients;
    final prevMaxExtent = hasClients ? _scrollController.position.maxScrollExtent : 0.0;
    final prevPixels = hasClients ? _scrollController.position.pixels : 0.0;
    setState(() {
      _visibleMessageLimit = all ? totalCount : math.min(totalCount, _visibleMessageLimit + kMessageBatchSize);
    });
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted && _scrollController.hasClients) {
        final newMaxExtent = _scrollController.position.maxScrollExtent;
        final delta = newMaxExtent - prevMaxExtent;
        if (delta > 0) _scrollController.jumpTo((prevPixels + delta).clamp(0.0, newMaxExtent));
      }
      _prependingEarlier = false;
    });
  }

  void _scrollToBottom({bool animate = false, bool force = true}) {
    if (force) {
      _stickToBottom = true;
    } else if (!_stickToBottom) {
      return;
    }
    if (_scrollToBottomScheduled) {
      if (!animate) _pendingJumpToBottom = true;
      return;
    }
    final gen = ++_autoScrollGen;
    final passes = force ? 4 : 1;
    _scrollToBottomScheduled = true;
    _pendingJumpToBottom = !animate;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _scrollToBottomScheduled = false;
      final jump = _pendingJumpToBottom;
      _pendingJumpToBottom = false;
      if (!_stickToBottom || gen != _autoScrollGen) return;
      _performScrollToBottom(jump: jump, remainingPasses: passes, gen: gen);
    });
  }

  void _performScrollToBottom({required bool jump, required int remainingPasses, required int gen}) {
    if (!mounted || !_scrollController.hasClients || !_stickToBottom || gen != _autoScrollGen) return;
    final position = _scrollController.position;
    final target = position.maxScrollExtent;
    final distance = target - position.pixels;
    if (distance > 0.5) {
      if (jump || distance > 1200) {
        _scrollController.jumpTo(target);
      } else {
        _scrollController.animateTo(target, duration: const Duration(milliseconds: 220), curve: Curves.easeOutCubic);
      }
    }
    if (remainingPasses > 1 && _stickToBottom) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted || !_scrollController.hasClients || !_stickToBottom || gen != _autoScrollGen) return;
        final nextTarget = _scrollController.position.maxScrollExtent;
        if ((nextTarget - _scrollController.position.pixels).abs() > 1.0) {
          _performScrollToBottom(jump: true, remainingPasses: remainingPasses - 1, gen: gen);
        }
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(aiWorkspaceControllerProvider);
    final controller = ref.read(aiWorkspaceControllerProvider.notifier);
    final session = state.currentSession;

    ref.listen(aiWorkspaceControllerProvider, (previous, next) {
      _syncConvoyPollTimer(next.hasActiveConvoyOrSubagent);
      if (previous?.currentSession?.id != next.currentSession?.id) {
        _visibleMessageLimit = kInitialVisibleMessages;
        _activateDraftSession(next.currentSession?.id);
        _scrollToBottom();
        return;
      }
      final prevLen = previous?.chatHistory.length ?? 0;
      final nextLen = next.chatHistory.length;
      if (prevLen != nextLen) {
        if (prevLen > 1 && nextLen > prevLen && _visibleMessageLimit < kMaxAutoExpandedMessages) {
          _visibleMessageLimit = math.min(kMaxAutoExpandedMessages, _visibleMessageLimit + (nextLen - prevLen));
        }
        if (prevLen <= 1 && nextLen > 1) {
          _scrollToBottom(force: true);
        } else if (next.chatHistory.lastOrNull?.isUser == true) {
          _scrollToBottom(animate: true, force: true);
        } else if (_stickToBottom) {
          _scrollToBottom(animate: false, force: false);
        }
        return;
      }
      final prevLast = previous?.chatHistory.lastOrNull;
      final nextLast = next.chatHistory.lastOrNull;
      final lastChanged =
          prevLast?.id != nextLast?.id ||
          prevLast?.text != nextLast?.text ||
          prevLast?.reasoning != nextLast?.reasoning ||
          prevLast?.activity != nextLast?.activity ||
          prevLast?.isStreaming != nextLast?.isStreaming;
      final approvalAppeared =
          previous?.pendingApproval?.id != next.pendingApproval?.id && next.pendingApproval != null;
      final finishedLoading = previous?.isLoading == true && !next.isLoading;
      if (_stickToBottom && (lastChanged || approvalAppeared || finishedLoading)) {
        _scrollToBottom(force: false);
      }
    });

    final totalMessages = state.chatHistory.length;
    final windowStart = computeMessageWindowStart(state.chatHistory, _visibleMessageLimit, (m) => m.isUser);
    final lastAssistantIdx = state.chatHistory.lastIndexWhere((m) => !m.isUser && m.id != 'welcome');
    final searchMatches = _searchOpen ? _computeSearchMatches(state.chatHistory, _searchQuery) : const <int>[];
    final safeMatchCursor = searchMatches.isEmpty ? 0 : _activeMatchCursor.clamp(0, searchMatches.length - 1);
    final highlightedMsgIdx = searchMatches.isEmpty ? -1 : searchMatches[safeMatchCursor];
    final queuedPrompts = state.currentSessionQueuedPrompts;
    final canRetryLatest =
        !state.isAiThinking &&
        !state.isSending &&
        (state.currentStage == 'failed' ||
            state.errorSummary.trim().isNotEmpty ||
            state.latestInstruction?.status == 'failed' ||
            state.latestInstruction?.status == 'cancelled');
    final modelText = session?.providerModel.trim() ?? '';
    final composerModelLabel = session == null
        ? ''
        : (modelText.isNotEmpty ? modelText.split('/').last : session.agentName.trim());
    final isManagedSession =
        session != null &&
        (session.isolationMode == 'multi_worktree' ||
            session.isolationMode == 'worktree' ||
            (session.sourceWorkDir.isNotEmpty && session.worktreeBranch.isNotEmpty));
    final showDeliveryCard =
        (isManagedSession && state.changedFiles.isNotEmpty) ||
        state.delivery != null ||
        state.isDeliveryLoading ||
        (state.deliveryError?.trim().isNotEmpty ?? false);
    final parentSession = session?.isChildSession == true
        ? state.sessions
              .where((s) => s.id == session!.parentSessionId || s.subagents.any((sub) => sub.id == session.id))
              .firstOrNull
        : null;
    final hasConvoyOrSubagents =
        session != null &&
        (state.convoyPlans.isNotEmpty || state.subagents.isNotEmpty || session.subagents.isNotEmpty);

    ref.watch(activeNodeGenerationProvider);
    final projectName = session == null
        ? ''
        : resolveWorkspaceProjectName(
            projectId: session.projectId,
            projects: state.projects,
            sourceWorkDir: session.sourceWorkDir,
            workDir: session.workDir,
          );
    final contextSubtitle = composeTaskContextSubtitle(
      projectName: projectName,
      nodeName: resolveActiveNodeDisplayName(),
    );
    final canPop = Navigator.maybeOf(context)?.canPop() ?? false;
    final hasRealMessages = state.chatHistory.any((m) => m.id != 'welcome');
    final isInitialSessionLoading =
        session != null &&
        state.isLoading &&
        !hasRealMessages &&
        state.pendingApproval == null &&
        state.convoyPlans.isEmpty &&
        state.errorMessage == null;

    return Scaffold(
      backgroundColor: AppTheme.surface,
      appBar: AppBar(
        toolbarHeight: 52,
        leadingWidth: canPop ? 52 : null,
        automaticallyImplyLeading: !canPop,
        leading: canPop ? const TaskHeaderBackButton() : null,
        backgroundColor: AppTheme.surface,
        foregroundColor: AppTheme.textPrimary,
        titleSpacing: canPop ? 4 : 16,
        title: AiChatEditableTitle(
          session: session,
          taskTitle: state.currentTask?.title,
          subtitle: contextSubtitle,
          isSaving: state.isActionLoading,
          onSaveTitle: controller.updateSessionTitle,
        ),
        actions: [
          AiChatHeaderActionCapsule(
            hasSession: session != null,
            searchOpen: _searchOpen,
            onOpenSessionSheet: _openSessionSheet,
            onMenuSelected: (value) {
              switch (value) {
                case 'search':
                  _toggleSearch();
                case 'git_review':
                  _openGitReview();
                case 'terminal':
                  _openTerminal();
                case 'task_detail':
                  _openTaskDetail();
                case 'files':
                  _openWorkspaceFile(null);
                case 'settings':
                  _openSessionSettings();
                case 'recovery':
                  _openRecovery();
                case 'refresh':
                  if (session == null) {
                    controller.loadWorkspace();
                  } else {
                    controller.refreshCurrentSession(showLoading: true);
                  }
              }
            },
          ),
        ],
      ),
      body: Column(
        children: [
          if (state.errorMessage != null)
            AiChatErrorBanner(
              message: state.errorMessage!,
              onRetry: session == null
                  ? controller.loadWorkspace
                  : () => controller.refreshCurrentSession(showLoading: true),
              onDismiss: controller.clearError,
            ),
          if (session != null && state.isStreamReconnecting) const AiChatReconnectingBanner(),
          if (state.isLoading && !isInitialSessionLoading) const LinearProgressIndicator(minHeight: 2),
          if (session != null) CodeSessionInitializingBanner(status: session.status),
          if (session != null && session.isChildSession)
            AiSubagentSessionBanner(
              session: session,
              siblings: parentSession?.subagents ?? const [],
              onOpenSession: controller.openSessionById,
            ),
          if (_searchOpen && session != null)
            AiTranscriptSearchBar(
              controller: _searchController,
              matchCount: searchMatches.length,
              activeMatchIndex: safeMatchCursor,
              onChanged: (val) => setState(() {
                _searchQuery = val;
                _activeMatchCursor = 0;
              }),
              onPrev: () => setState(() {
                if (searchMatches.isEmpty) return;
                _activeMatchCursor = (safeMatchCursor - 1 + searchMatches.length) % searchMatches.length;
              }),
              onNext: () => setState(() {
                if (searchMatches.isEmpty) return;
                _activeMatchCursor = (safeMatchCursor + 1) % searchMatches.length;
              }),
              onClose: _toggleSearch,
            ),
          Expanded(
            child: session == null
                ? AiChatEmptyWorkspace(onOpenSessions: _openSessionSheet)
                : isInitialSessionLoading
                ? const HoxiSessionSkeleton(
                    key: ValueKey('ai_chat_session_skeleton'),
                  )
                : HoxiViewEntrance(
                    identity: session.id,
                    child: Stack(
                    children: [
                      NotificationListener<ScrollNotification>(
                        onNotification: _onScrollNotification,
                        child: NotificationListener<ScrollMetricsNotification>(
                          onNotification: (notification) {
                            if (notification.depth == 0 &&
                                _stickToBottom &&
                                !_prependingEarlier &&
                                notification.metrics.maxScrollExtent - notification.metrics.pixels > 1.0) {
                              _scrollToBottom(force: false);
                            }
                            return false;
                          },
                          child: ChatKeyboardDismissArea(
                            child: CustomScrollView(
                              controller: _scrollController,
                              keyboardDismissBehavior:
                                  ScrollViewKeyboardDismissBehavior.onDrag,
                              physics: const AlwaysScrollableScrollPhysics(),
                              slivers: [
                              if (hasConvoyOrSubagents)
                                SliverToBoxAdapter(
                                  child: AiConvoyOverviewCard(
                                    convoyPlans: state.convoyPlans,
                                    subagents: state.subagents,
                                    sessionSubagents: session.subagents,
                                    onOpenSession: controller.openSessionById,
                                    onCancelConvoyPlan: controller.cancelConvoyPlan,
                                    onRetryConvoyPlan: controller.retryConvoyPlan,
                                    onMergeConvoyPlan: controller.mergeConvoyPlan,
                                    onSummarizeConvoyPlan: controller.summarizeConvoyPlanInChat,
                                  ),
                                ),
                              if (showDeliveryCard)
                                SliverPadding(
                                  padding: const EdgeInsets.only(top: 8),
                                  sliver: SliverToBoxAdapter(
                                    child: CodeDeliveryCard(
                                      session: session,
                                      delivery: state.delivery,
                                      loading: state.isDeliveryLoading,
                                      errorMessage: state.deliveryError,
                                      onStart: _confirmDelivery,
                                    ),
                                  ),
                                ),
                              SliverToBoxAdapter(
                                child: CodeFlowCard(
                                  sessionId: session.id,
                                  refreshKey: '${state.delivery?.id ?? 0}:${state.delivery?.status ?? ''}:${state.delivery?.attempt ?? 0}:${state.delivery?.resultCommit ?? ''}',
                                ),
                              ),
                              if (state.errorSummary.trim().isNotEmpty &&
                                  state.errorSummary.trim() != (state.errorMessage?.trim() ?? ''))
                                SliverToBoxAdapter(
                                  child: AiTimelinePanel(
                                    timelineEvents: const [],
                                    errorSummary: state.errorSummary,
                                    changedFiles: const [],
                                  ),
                                ),
                              SliverToBoxAdapter(
                                child: AiPreviewStrip(
                                  previews: state.previews,
                                  onOpenPreview: (preview) => Navigator.of(context).push(
                                    MaterialPageRoute(builder: (_) => AiPreviewDetailScreen(preview: preview)),
                                  ),
                                  onViewAll: () => Navigator.of(context).push(
                                    MaterialPageRoute(
                                      builder: (_) => AiPreviewListScreen(
                                        sessionId: session.id,
                                        title: context.l10n.codeWsChatPreviewTitle,
                                      ),
                                    ),
                                  ),
                                ),
                              ),
                              if (windowStart > 0)
                                SliverPadding(
                                  padding: const EdgeInsets.fromLTRB(16, 6, 16, 0),
                                  sliver: SliverToBoxAdapter(
                                    child: EarlierMessagesBanner(
                                      hiddenCount: windowStart,
                                      totalCount: totalMessages,
                                      onLoadBatch: () => _loadEarlierMessages(totalMessages),
                                      onLoadAll: () => _loadEarlierMessages(totalMessages, all: true),
                                    ),
                                  ),
                                ),
                              SliverPadding(
                                padding: const EdgeInsets.fromLTRB(16, 6, 16, 10),
                                sliver: SliverList.separated(
                                  itemCount: totalMessages - windowStart,
                                  separatorBuilder: (_, _) => const SizedBox(height: 12),
                                  itemBuilder: (context, index) {
                                    final globalIdx = windowStart + index;
                                    final message = state.chatHistory[globalIdx];
                                    final isLatest = globalIdx == lastAssistantIdx;
                                    return AiChatMessageItem(
                                      key: ValueKey('turn_${globalIdx}_${message.isUser ? "u" : "a"}'),
                                      message: message,
                                      workDir: session.workDir,
                                      isSearchHighlighted: globalIdx == highlightedMsgIdx,
                                      convoyPlans: state.convoyPlans,
                                      subagents: state.subagents,
                                      changedFiles: isLatest ? state.completedTurnChangedFiles : const [],
                                      changedFileStats: isLatest
                                          ? (state.isAiThinking ? state.activeTurnChangedFileStats : state.completedTurnChangedFileStats)
                                          : state.changedFileStats,
                                      diffAdditions: isLatest ? state.completedTurnDiffAdditions : 0,
                                      diffDeletions: isLatest ? state.completedTurnDiffDeletions : 0,
                                      onOpenGitReview: isLatest ? _openGitReview : null,
                                      onOpenFileDiff: isLatest ? _openFileDiff : null,
                                      onOpenFile: _openWorkspaceFile,
                                      onOpenSession: controller.openSessionById,
                                      onStartConvoyPlan: controller.startConvoyPlan,
                                      onCancelConvoyPlan: controller.cancelConvoyPlan,
                                      onRetryConvoyPlan: controller.retryConvoyPlan,
                                      onMergeConvoyPlan: controller.mergeConvoyPlan,
                                      onSummarizeConvoyPlan: controller.summarizeConvoyPlanInChat,
                                      onEditUserMessage: _editUserMessage,
                                      onRetry: isLatest && canRetryLatest ? controller.retryLatestInstruction : null,
                                    );
                                  },
                                ),
                              ),
                              if (queuedPrompts.isNotEmpty)
                                SliverToBoxAdapter(
                                  child: AiQueuedPromptsList(
                                    items: queuedPrompts,
                                    onSendNow: controller.sendQueuedPromptNow,
                                    onEdit: _editQueuedPrompt,
                                    onDelete: controller.removeQueuedPrompt,
                                  ),
                                ),
                              ],
                            ),
                          ),
                        ),
                      ),
                      if (_showScrollToBottomFab)
                        Positioned(
                          bottom: 10,
                          left: 0,
                          right: 0,
                          child: Center(
                            child: AiScrollToBottomFab(
                              isStreaming: state.isAiThinking,
                              onTap: () {
                                setState(() => _showScrollToBottomFab = false);
                                _scrollToBottom(animate: true, force: true);
                              },
                            ),
                          ),
                        ),
                    ],
                  ),
                ),
          ),
          if (session != null && state.pendingApproval != null)
            AiApprovalPrompt(
              approval: state.pendingApproval!,
              loading: state.isActionLoading,
              onDecision: (approved, reason) => controller.decideApproval(approved: approved, reason: reason),
            ),
          if (session != null && state.isAiThinking)
            AiComposerDiffShelf(
              changedFiles: state.activeTurnChangedFiles,
              changedFileStats: state.activeTurnChangedFileStats,
              additions: state.activeTurnDiffAdditions,
              deletions: state.activeTurnDiffDeletions,
              todos: state.activeTurnTodos,
              onOpenGitReview: _openGitReview,
              onOpenFileDiff: _openFileDiff,
            ),
          CodeInstructionComposer(
            controller: _inputController,
            focusNode: _focusNode,
            enabled: session != null && !state.isDevelopmentClosed,
            closed: state.isDevelopmentClosed,
            options: _instructionOptions,
            onOptionsChanged: (options) => setState(() => _instructionOptions = options),
            modelLabel: composerModelLabel,
            reasoningEffort: session?.reasoningEffort ?? '',
            onSelectModel: _openModelSelector,
            pendingImageDataUrl: _pendingImageDataUrl,
            onClearPendingImage: () => setState(() => _pendingImageDataUrl = null),
            attachedFiles: _attachedFiles,
            onRemoveAttachedFile: (file) => setState(
              () => _attachedFiles = _attachedFiles.where((f) => f != file).toList(),
            ),
            selectedSkill: state.selectedSkill,
            onClearSelectedSkill: () => controller.selectComposerSkill(null),
            onPickSkill: _pickWorkspaceSkill,
            onTakePhoto: () => _pickImage(ImageSource.camera),
            onPickFromGallery: () => _pickImage(ImageSource.gallery),
            onPickWorkspaceFile: _pickWorkspaceFile,
            onSend: _sendMessage,
            isRunning: session != null && state.isAiThinking,
            isStopping: state.isActionLoading,
            onStop: _confirmStop,
          ),
        ],
      ),
    );
  }
}
