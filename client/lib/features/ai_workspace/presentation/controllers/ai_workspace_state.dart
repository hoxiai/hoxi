import '../../models/ai_dev_session.dart';
import '../../models/chat_message.dart';
import '../../models/code_delivery_job.dart';
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
