import 'ai_dev_session.dart';
import 'chat_message.dart';
import 'code_convoy_plan.dart';
import 'code_git_review.dart';

class CodeTodoItem {
  final String id;
  final String content;
  final String status;

  const CodeTodoItem({
    required this.id,
    required this.content,
    required this.status,
  });

  bool get isCompleted => status == 'completed';
  bool get isInProgress => status == 'in_progress';
  bool get isCancelled => status == 'cancelled';

  factory CodeTodoItem.fromJson(Map<String, dynamic> json) {
    final rawStatus = (json['status'] ?? 'pending').toString().trim();
    final normalizedStatus = switch (rawStatus) {
      'completed' || 'in_progress' || 'cancelled' => rawStatus,
      _ => 'pending',
    };
    return CodeTodoItem(
      id: (json['id'] ?? '').toString().trim(),
      content: (json['content'] ?? json['text'] ?? '').toString().trim(),
      status: normalizedStatus,
    );
  }

  static List<CodeTodoItem> extractFromToolState(Map<String, dynamic> state) {
    final input = state['input'];
    final metadata = state['metadata'];
    final rawList = (input is Map && input['todos'] is List)
        ? input['todos'] as List
        : (metadata is Map && metadata['todos'] is List)
        ? metadata['todos'] as List
        : const <dynamic>[];
    final items = <CodeTodoItem>[];
    for (var i = 0; i < rawList.length; i++) {
      final raw = rawList[i];
      if (raw is! Map) continue;
      final item = CodeTodoItem.fromJson(raw.cast<String, dynamic>());
      if (item.content.isEmpty) continue;
      items.add(
        item.id.isNotEmpty
            ? item
            : CodeTodoItem(
                id: 'todo-$i',
                content: item.content,
                status: item.status,
              ),
      );
    }
    return items;
  }
}

class AiSessionStateInfo {
  final AiDevSession session;
  final AiTaskSummary? currentTask;
  final AiInstruction? latestInstruction;
  final String currentStage;
  final String recentOutput;
  final List<ChatMessage> recentMessages;
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
  final AiApproval? pendingApproval;

  String get status => session.status;
  List<String> get effectiveTurnChangedFiles =>
      turnChangedFiles ?? changedFiles;
  List<CodeGitFile> get effectiveTurnChangedFileStats =>
      turnChangedFileStats ?? changedFileStats;
  int get effectiveTurnDiffAdditions => turnDiffAdditions ?? diffAdditions;
  int get effectiveTurnDiffDeletions => turnDiffDeletions ?? diffDeletions;

  const AiSessionStateInfo({
    required this.session,
    required this.currentTask,
    required this.latestInstruction,
    required this.currentStage,
    required this.recentOutput,
    required this.recentMessages,
    required this.previews,
    required this.timelineEvents,
    required this.errorSummary,
    required this.changedFiles,
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
    required this.pendingApproval,
  });

  factory AiSessionStateInfo.fromJson(Map<String, dynamic> json) {
    final recentMessagesRaw =
        json['recentMessages'] as List<dynamic>? ?? const [];
    final previewsRaw = json['previews'] as List<dynamic>? ?? const [];
    final timelineRaw = json['timelineEvents'] as List<dynamic>? ?? const [];
    final changedFileStatsRaw =
        json['changedFileStats'] as List<dynamic>? ?? const [];
    final todosRaw = json['todos'] as List<dynamic>? ?? const [];
    final convoyPlansRaw = json['convoyPlans'] as List<dynamic>? ?? const [];
    final subagentsRaw = json['subagents'] as List<dynamic>? ?? const [];

    return AiSessionStateInfo(
      session: AiDevSession.fromJson(
        (json['session'] as Map? ?? const {}).cast<String, dynamic>(),
      ),
      currentTask: json['currentTask'] is Map<String, dynamic>
          ? AiTaskSummary.fromJson(json['currentTask'] as Map<String, dynamic>)
          : json['currentTask'] is Map
          ? AiTaskSummary.fromJson(
              (json['currentTask'] as Map).cast<String, dynamic>(),
            )
          : null,
      latestInstruction: json['latestInstruction'] is Map<String, dynamic>
          ? AiInstruction.fromJson(
              json['latestInstruction'] as Map<String, dynamic>,
            )
          : json['latestInstruction'] is Map
          ? AiInstruction.fromJson(
              (json['latestInstruction'] as Map).cast<String, dynamic>(),
            )
          : null,
      currentStage: (json['currentStage'] ?? '').toString(),
      recentOutput: (json['recentOutput'] ?? '').toString(),
      recentMessages: recentMessagesRaw
          .whereType<Map>()
          .map(
            (item) =>
                ChatMessage.fromAiMessageJson(item.cast<String, dynamic>()),
          )
          .toList(),
      previews: previewsRaw
          .whereType<Map>()
          .map((item) => AiPreview.fromJson(item.cast<String, dynamic>()))
          .toList(),
      timelineEvents: timelineRaw
          .whereType<Map>()
          .map((item) => AiTimelineEvent.fromJson(item.cast<String, dynamic>()))
          .toList(),
      errorSummary: (json['errorSummary'] ?? '').toString(),
      changedFiles: (json['changedFiles'] as List<dynamic>? ?? const [])
          .map((item) => item.toString())
          .where((item) => item.trim().isNotEmpty)
          .toList(),
      changedFileStats: changedFileStatsRaw
          .whereType<Map>()
          .map((item) => CodeGitFile.fromJson(item.cast<String, dynamic>()))
          .toList(),
      diffAdditions:
          ((json['diffAdditions'] ?? json['additions']) as num?)?.toInt() ?? 0,
      diffDeletions:
          ((json['diffDeletions'] ?? json['deletions']) as num?)?.toInt() ?? 0,
      todos: todosRaw
          .whereType<Map>()
          .map((item) => CodeTodoItem.fromJson(item.cast<String, dynamic>()))
          .where((item) => item.content.isNotEmpty)
          .toList(),
      convoyPlans: convoyPlansRaw
          .whereType<Map>()
          .map((item) => CodeConvoyPlan.fromJson(item.cast<String, dynamic>()))
          .toList(),
      subagents: subagentsRaw
          .whereType<Map>()
          .map((item) => CodeSubagentItem.fromJson(item.cast<String, dynamic>()))
          .toList(),
      pendingApproval: json['pendingApproval'] is Map<String, dynamic>
          ? AiApproval.fromJson(json['pendingApproval'] as Map<String, dynamic>)
          : json['pendingApproval'] is Map
          ? AiApproval.fromJson(
              (json['pendingApproval'] as Map).cast<String, dynamic>(),
            )
          : null,
    );
  }
}

class AiInstructionSendResult {
  final AiDevSession session;
  final AiInstruction instruction;
  final AiTaskSummary? task;
  final AiApproval? approval;

  const AiInstructionSendResult({
    required this.session,
    required this.instruction,
    required this.task,
    required this.approval,
  });

  factory AiInstructionSendResult.fromJson(Map<String, dynamic> json) {
    return AiInstructionSendResult(
      session: AiDevSession.fromJson(
        (json['session'] as Map? ?? const {}).cast<String, dynamic>(),
      ),
      instruction: AiInstruction.fromJson(
        (json['instruction'] as Map? ?? const {}).cast<String, dynamic>(),
      ),
      task: json['task'] is Map<String, dynamic>
          ? AiTaskSummary.fromJson(json['task'] as Map<String, dynamic>)
          : json['task'] is Map
          ? AiTaskSummary.fromJson(
              (json['task'] as Map).cast<String, dynamic>(),
            )
          : null,
      approval: json['approval'] is Map<String, dynamic>
          ? AiApproval.fromJson(json['approval'] as Map<String, dynamic>)
          : json['approval'] is Map
          ? AiApproval.fromJson(
              (json['approval'] as Map).cast<String, dynamic>(),
            )
          : null,
    );
  }
}
