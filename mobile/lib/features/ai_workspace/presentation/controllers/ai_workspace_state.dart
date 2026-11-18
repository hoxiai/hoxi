import '../../../../core/models/node_skill.dart';
import '../../models/ai_dev_session.dart';
import '../../models/ai_session_state_info.dart';
import '../../models/chat_message.dart';
import '../../models/code_convoy_plan.dart';
import '../../models/code_delivery_job.dart';
import '../../models/code_git_review.dart';
import '../../models/code_task.dart';

// awaiting_approval 也算进行中：此前等审批时指示条消失，看上去像指令已经结束
const activeWorkspaceStages = {
  'interactive',
  'awaiting_approval',
  'task_ready',
  'instruction_queued',
  'executing',
  'running',
};

/// 对话流重连间隔：2、4、8、16 秒递增，封顶 30 秒。此前固定 2 秒无上限，面板重启或断网期间
/// 每 2 秒就连带三个刷新请求重试一次。
Duration codeStreamReconnectDelay(int attempt) {
  final seconds = 2 << attempt.clamp(0, 4);
  return Duration(seconds: seconds > 30 ? 30 : seconds);
}

class AiWorkspaceState {
  final bool isLoading;
  final bool isSending;
  final bool isActionLoading;
  final String? errorMessage;
  final List<CodeProject> projects;
  final List<CodeExecutor> executors;
  final List<AiDevSession> sessions;
  final List<CodeTask> tasks;
  final int? selectedProjectId;
  final List<ChatMessage> chatHistory;
  final AiDevSession? currentSession;
  final AiTaskSummary? currentTask;
  final AiInstruction? latestInstruction;
  final AiApproval? pendingApproval;
  final String currentStage;
  final String recentOutput;
  final List<AiPreview> previews;
  final List<AiTimelineEvent> timelineEvents;
  final String errorSummary;
  final List<String> changedFiles;
  final List<CodeGitFile> changedFileStats;
  final int diffAdditions;
  final int diffDeletions;
  final List<String>? turnChangedFiles;
  final List<CodeGitFile>? turnChangedFileStats;
  final int? turnDiffAdditions;
  final int? turnDiffDeletions;
  final List<CodeTodoItem> todos;
  final List<CodeConvoyPlan> convoyPlans;
  final List<CodeSubagentItem> subagents;
  final List<CodeQueuedPromptItem> queuedPrompts;
  final NodeSkillSelection? selectedSkill;
  final CodeDeliveryJob? delivery;
  final bool isDeliveryLoading;
  final String? deliveryError;
  final bool isStreaming;

  /// 实时对话流断开、正在按退避间隔重连
  final bool isStreamReconnecting;
  final String streamActivityKind;
  final String streamActivity;

  const AiWorkspaceState({
    this.isLoading = false,
    this.isSending = false,
    this.isActionLoading = false,
    this.errorMessage,
    this.projects = const [],
    this.executors = const [],
    this.sessions = const [],
    this.tasks = const [],
    this.selectedProjectId,
    this.chatHistory = const [],
    this.currentSession,
    this.currentTask,
    this.latestInstruction,
    this.pendingApproval,
    this.currentStage = 'idle',
    this.recentOutput = '',
    this.previews = const [],
    this.timelineEvents = const [],
    this.errorSummary = '',
    this.changedFiles = const [],
    this.changedFileStats = const [],
    this.diffAdditions = 0,
    this.diffDeletions = 0,
    this.turnChangedFiles,
    this.turnChangedFileStats,
    this.turnDiffAdditions,
    this.turnDiffDeletions,
    this.todos = const [],
    this.convoyPlans = const [],
    this.subagents = const [],
    this.queuedPrompts = const [],
    this.selectedSkill,
    this.delivery,
    this.isDeliveryLoading = false,
    this.deliveryError,
    this.isStreaming = false,
    this.isStreamReconnecting = false,
    this.streamActivityKind = '',
    this.streamActivity = '',
  });

  String? get selectedWorkspacePath => currentSession?.workDir;
  bool get isAiThinking =>
      isSending || isStreaming || activeWorkspaceStages.contains(currentStage);

  bool get hasActiveConvoyOrSubagent =>
      convoyPlans.any((p) => !p.isTerminal) ||
      subagents.any((s) => s.isRunning);

  List<CodeQueuedPromptItem> get currentSessionQueuedPrompts {
    final sid = currentSession?.id;
    if (sid == null) return const [];
    return queuedPrompts.where((item) => item.sessionId == sid).toList();
  }

  List<String> get activeTurnChangedFiles =>
      isAiThinking ? (turnChangedFiles ?? changedFiles) : const [];
  List<CodeGitFile> get activeTurnChangedFileStats =>
      isAiThinking ? (turnChangedFileStats ?? changedFileStats) : const [];
  int get activeTurnDiffAdditions =>
      isAiThinking ? (turnDiffAdditions ?? diffAdditions) : 0;
  int get activeTurnDiffDeletions =>
      isAiThinking ? (turnDiffDeletions ?? diffDeletions) : 0;
  List<CodeTodoItem> get activeTurnTodos => isAiThinking ? todos : const [];

  List<String> get completedTurnChangedFiles {
    if (isAiThinking) return const [];
    final turn = turnChangedFiles;
    return (turn != null && turn.isNotEmpty) ? turn : changedFiles;
  }

  List<CodeGitFile> get completedTurnChangedFileStats {
    if (isAiThinking) return const [];
    final turn = turnChangedFileStats;
    return (turn != null && turn.isNotEmpty) ? turn : changedFileStats;
  }

  int get completedTurnDiffAdditions {
    if (isAiThinking) return 0;
    final turn = turnDiffAdditions;
    return (turn != null && (turnChangedFiles?.isNotEmpty ?? false))
        ? turn
        : diffAdditions;
  }

  int get completedTurnDiffDeletions {
    if (isAiThinking) return 0;
    final turn = turnDiffDeletions;
    return (turn != null && (turnChangedFiles?.isNotEmpty ?? false))
        ? turn
        : diffDeletions;
  }

  /// 指令在等人工审批（指令门）时流里没有活动，指示条仍要说明是在等审批而不是在执行
  String get indicatorActivityKind =>
      streamActivityKind.isEmpty && currentStage == 'awaiting_approval'
      ? 'approval_waiting'
      : streamActivityKind;
  bool get isDevelopmentClosed =>
      currentSession?.status == 'delivering' ||
      currentSession?.status == 'delivered' ||
      delivery?.isActive == true;

  AiWorkspaceState copyWith({
    bool? isLoading,
    bool? isSending,
    bool? isActionLoading,
    String? errorMessage,
    List<CodeProject>? projects,
    List<CodeExecutor>? executors,
    List<AiDevSession>? sessions,
    List<CodeTask>? tasks,
    int? selectedProjectId,
    List<ChatMessage>? chatHistory,
    AiDevSession? currentSession,
    AiTaskSummary? currentTask,
    AiInstruction? latestInstruction,
    AiApproval? pendingApproval,
    String? currentStage,
    String? recentOutput,
    List<AiPreview>? previews,
    List<AiTimelineEvent>? timelineEvents,
    String? errorSummary,
    List<String>? changedFiles,
    List<CodeGitFile>? changedFileStats,
    int? diffAdditions,
    int? diffDeletions,
    List<String>? turnChangedFiles,
    List<CodeGitFile>? turnChangedFileStats,
    int? turnDiffAdditions,
    int? turnDiffDeletions,
    List<CodeTodoItem>? todos,
    List<CodeConvoyPlan>? convoyPlans,
    List<CodeSubagentItem>? subagents,
    List<CodeQueuedPromptItem>? queuedPrompts,
    NodeSkillSelection? selectedSkill,
    CodeDeliveryJob? delivery,
    bool? isDeliveryLoading,
    String? deliveryError,
    bool? isStreaming,
    bool? isStreamReconnecting,
    String? streamActivityKind,
    String? streamActivity,
    bool clearError = false,
    bool clearSession = false,
    bool clearTask = false,
    bool clearInstruction = false,
    bool clearApproval = false,
    bool clearDelivery = false,
    bool clearDeliveryError = false,
    bool clearSelectedSkill = false,
  }) {
    return AiWorkspaceState(
      isLoading: isLoading ?? this.isLoading,
      isSending: isSending ?? this.isSending,
      isActionLoading: isActionLoading ?? this.isActionLoading,
      errorMessage: clearError ? null : (errorMessage ?? this.errorMessage),
      projects: projects ?? this.projects,
      executors: executors ?? this.executors,
      sessions: sessions ?? this.sessions,
      tasks: tasks ?? this.tasks,
      selectedProjectId: selectedProjectId ?? this.selectedProjectId,
      chatHistory: chatHistory ?? this.chatHistory,
      currentSession: clearSession
          ? null
          : (currentSession ?? this.currentSession),
      currentTask: clearTask ? null : (currentTask ?? this.currentTask),
      latestInstruction: clearInstruction
          ? null
          : (latestInstruction ?? this.latestInstruction),
      pendingApproval: clearApproval
          ? null
          : (pendingApproval ?? this.pendingApproval),
      currentStage: currentStage ?? this.currentStage,
      recentOutput: recentOutput ?? this.recentOutput,
      previews: previews ?? this.previews,
      timelineEvents: timelineEvents ?? this.timelineEvents,
      errorSummary: errorSummary ?? this.errorSummary,
      changedFiles: changedFiles ?? this.changedFiles,
      changedFileStats: changedFileStats ?? this.changedFileStats,
      diffAdditions: diffAdditions ?? this.diffAdditions,
      diffDeletions: diffDeletions ?? this.diffDeletions,
      turnChangedFiles: turnChangedFiles ?? this.turnChangedFiles,
      turnChangedFileStats: turnChangedFileStats ?? this.turnChangedFileStats,
      turnDiffAdditions: turnDiffAdditions ?? this.turnDiffAdditions,
      turnDiffDeletions: turnDiffDeletions ?? this.turnDiffDeletions,
      todos: todos ?? this.todos,
      convoyPlans: convoyPlans ?? this.convoyPlans,
      subagents: subagents ?? this.subagents,
      queuedPrompts: queuedPrompts ?? this.queuedPrompts,
      selectedSkill: clearSelectedSkill
          ? null
          : (selectedSkill ?? this.selectedSkill),
      delivery: clearDelivery ? null : (delivery ?? this.delivery),
      isDeliveryLoading: isDeliveryLoading ?? this.isDeliveryLoading,
      deliveryError: clearDeliveryError
          ? null
          : (deliveryError ?? this.deliveryError),
      isStreaming: isStreaming ?? this.isStreaming,
      isStreamReconnecting: isStreamReconnecting ?? this.isStreamReconnecting,
      streamActivityKind: streamActivityKind ?? this.streamActivityKind,
      streamActivity: streamActivity ?? this.streamActivity,
    );
  }
}
