import 'ai_dev_session.dart';
import 'chat_message.dart';

class CodeHistoryMessage {
  final int id;
  final int runId;
  final String role;
  final String content;
  final DateTime? createdAt;
  final String reasoning;
  final int durationMs;
  final int totalTokens;
  final List<String> toolSteps;
  final String? imageDataUrl;

  const CodeHistoryMessage({
    required this.id,
    required this.runId,
    required this.role,
    required this.content,
    required this.createdAt,
    this.reasoning = '',
    this.durationMs = 0,
    this.totalTokens = 0,
    this.toolSteps = const [],
    this.imageDataUrl,
  });

  factory CodeHistoryMessage.fromJson(Map<String, dynamic> json) {
    final rawSteps = json['toolSteps'];
    return CodeHistoryMessage(
      id: (json['id'] as num?)?.toInt() ?? 0,
      runId: (json['runId'] as num?)?.toInt() ?? 0,
      role: (json['role'] ?? '').toString(),
      content: (json['content'] ?? '').toString(),
      createdAt: DateTime.tryParse((json['createdAt'] ?? '').toString()),
      reasoning: (json['reasoning'] ?? '').toString(),
      durationMs: (json['durationMs'] as num?)?.toInt() ?? 0,
      totalTokens: (json['totalTokens'] as num?)?.toInt() ?? 0,
      toolSteps: rawSteps is List
          ? rawSteps
                .map((e) => e.toString().trim())
                .where((e) => e.isNotEmpty)
                .toList(growable: false)
          : const [],
      imageDataUrl: json['imageDataUrl']?.toString(),
    );
  }

  ChatMessage toChatMessage({CodeExecutionRun? run}) {
    final isUserMsg = role.trim().toLowerCase() == 'user';
    return ChatMessage(
      id: 'history-$id',
      text: content,
      isUser: isUserMsg,
      timestamp: createdAt ?? DateTime.fromMillisecondsSinceEpoch(0),
      reasoning: reasoning,
      durationMs: isUserMsg
          ? 0
          : (durationMs > 0 ? durationMs : (run?.durationMs ?? 0)),
      totalTokens: isUserMsg
          ? 0
          : (totalTokens > 0 ? totalTokens : (run?.totalTokens ?? 0)),
      toolSteps: isUserMsg ? const [] : toolSteps,
      imageDataUrl: isUserMsg ? imageDataUrl : null,
    );
  }
}

List<ChatMessage> codeHistoryChatMessages(
  Iterable<CodeHistoryMessage> messages, {
  Iterable<CodeExecutionRun> runs = const [],
}) {
  final runMap = <int, CodeExecutionRun>{
    for (final run in runs)
      if (run.id > 0) run.id: run,
  };
  return messages
      .map((message) => message.toChatMessage(run: runMap[message.runId]))
      .toList();
}

class CodeExecutionRun {
  final int id;
  final int instructionId;
  final String prompt;
  final String output;
  final String status;
  final String errorMessage;
  final int durationMs;
  final int totalTokens;
  final DateTime? createdAt;

  const CodeExecutionRun({
    required this.id,
    required this.instructionId,
    required this.prompt,
    required this.output,
    required this.status,
    required this.errorMessage,
    required this.durationMs,
    required this.totalTokens,
    required this.createdAt,
  });

  bool get canRetry =>
      instructionId > 0 && (status == 'failed' || status == 'cancelled');

  factory CodeExecutionRun.fromJson(Map<String, dynamic> json) {
    return CodeExecutionRun(
      id: (json['id'] as num?)?.toInt() ?? 0,
      instructionId: (json['instructionId'] as num?)?.toInt() ?? 0,
      prompt: (json['prompt'] ?? '').toString(),
      output: (json['output'] ?? '').toString(),
      status: (json['status'] ?? '').toString(),
      errorMessage: (json['errorMessage'] ?? '').toString(),
      durationMs: (json['durationMs'] as num?)?.toInt() ?? 0,
      totalTokens: (json['totalTokens'] as num?)?.toInt() ?? 0,
      createdAt: DateTime.tryParse((json['createdAt'] ?? '').toString()),
    );
  }
}

class CodeSessionHistory {
  final AiDevSession session;
  final List<CodeHistoryMessage> messages;
  final List<CodeExecutionRun> runs;
  final int total;
  final int page;
  final int limit;

  const CodeSessionHistory({
    required this.session,
    required this.messages,
    required this.runs,
    required this.total,
    required this.page,
    required this.limit,
  });

  factory CodeSessionHistory.fromJson(Map<String, dynamic> json) {
    return CodeSessionHistory(
      session: AiDevSession.fromJson(
        (json['session'] as Map? ?? const {}).cast<String, dynamic>(),
      ),
      messages: (json['messages'] as List<dynamic>? ?? const [])
          .whereType<Map>()
          .map(
            (item) => CodeHistoryMessage.fromJson(item.cast<String, dynamic>()),
          )
          .toList(),
      runs: (json['runs'] as List<dynamic>? ?? const [])
          .whereType<Map>()
          .map(
            (item) => CodeExecutionRun.fromJson(item.cast<String, dynamic>()),
          )
          .toList(),
      total: (json['total'] as num?)?.toInt() ?? 0,
      page: (json['page'] as num?)?.toInt() ?? 1,
      limit: (json['limit'] as num?)?.toInt() ?? 20,
    );
  }
}

class CodeSessionInitialization {
  final int id;
  final String status;
  final String currentStage;
  final String errorMessage;

  const CodeSessionInitialization({
    required this.id,
    required this.status,
    required this.currentStage,
    required this.errorMessage,
  });

  bool get isInitializing => status == 'initializing';
  bool get isFailed => status == 'failed';
  bool get canAbort => isInitializing;
  bool get canRetry =>
      status == 'failed' && currentStage == 'initialization_failed';

  factory CodeSessionInitialization.fromJson(Map<String, dynamic> json) {
    return CodeSessionInitialization(
      id: (json['id'] as num?)?.toInt() ?? 0,
      status: (json['status'] ?? '').toString(),
      currentStage: (json['currentStage'] ?? '').toString(),
      errorMessage: (json['initializationError'] ?? '').toString(),
    );
  }
}
