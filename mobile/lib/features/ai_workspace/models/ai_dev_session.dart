class AiDevSession {
  final int id;
  final int userId;
  final int projectId;
  final int? parentSessionId;
  final String convoyPlanId;
  final String convoyTaskId;
  final List<AiDevSession> subagents;
  final String title;
  final String agentName;
  final String workDir;
  final String sourceWorkDir;
  final String worktreeBranch;
  final String isolationMode;
  final String status;
  final String currentStage;
  final String approvalPolicy;
  final String providerModel;
  final String reasoningEffort;
  final int diffAdditions;
  final int diffDeletions;
  final int lastTaskId;
  final String currentTaskTitle;
  final DateTime? createdAt;
  final DateTime? updatedAt;
  final DateTime? lastInstructionAt;

  bool get isChildSession => parentSessionId != null && parentSessionId! > 0;
  bool get isConvoyTask => convoyPlanId.isNotEmpty;
  bool get hasSubagents => subagents.isNotEmpty;

  int get runningSubagentsCount => subagents.where((s) {
    final st = s.status.toLowerCase();
    final stage = s.currentStage.toLowerCase();
    return st == 'running' ||
        st == 'busy' ||
        stage == 'running' ||
        stage == 'executing' ||
        stage == 'instruction_queued' ||
        stage == 'awaiting_approval';
  }).length;

  const AiDevSession({
    required this.id,
    required this.userId,
    required this.projectId,
    this.parentSessionId,
    this.convoyPlanId = '',
    this.convoyTaskId = '',
    this.subagents = const [],
    required this.title,
    required this.agentName,
    required this.workDir,
    required this.sourceWorkDir,
    required this.worktreeBranch,
    required this.isolationMode,
    required this.status,
    required this.currentStage,
    required this.approvalPolicy,
    this.providerModel = '',
    this.reasoningEffort = '',
    this.diffAdditions = 0,
    this.diffDeletions = 0,
    required this.lastTaskId,
    required this.currentTaskTitle,
    required this.createdAt,
    required this.updatedAt,
    required this.lastInstructionAt,
  });

  AiDevSession copyWith({
    int? id, int? userId, int? projectId, int? parentSessionId,
    String? convoyPlanId, String? convoyTaskId, List<AiDevSession>? subagents,
    String? title, String? agentName, String? workDir, String? sourceWorkDir,
    String? worktreeBranch, String? isolationMode, String? status,
    String? currentStage, String? approvalPolicy, String? providerModel,
    String? reasoningEffort, int? diffAdditions, int? diffDeletions,
    int? lastTaskId, String? currentTaskTitle,
    DateTime? createdAt, DateTime? updatedAt, DateTime? lastInstructionAt,
  }) {
    return AiDevSession(
      id: id ?? this.id, userId: userId ?? this.userId,
      projectId: projectId ?? this.projectId,
      parentSessionId: parentSessionId ?? this.parentSessionId,
      convoyPlanId: convoyPlanId ?? this.convoyPlanId,
      convoyTaskId: convoyTaskId ?? this.convoyTaskId,
      subagents: subagents ?? this.subagents, title: title ?? this.title,
      agentName: agentName ?? this.agentName, workDir: workDir ?? this.workDir,
      sourceWorkDir: sourceWorkDir ?? this.sourceWorkDir,
      worktreeBranch: worktreeBranch ?? this.worktreeBranch,
      isolationMode: isolationMode ?? this.isolationMode,
      status: status ?? this.status, currentStage: currentStage ?? this.currentStage,
      approvalPolicy: approvalPolicy ?? this.approvalPolicy,
      providerModel: providerModel ?? this.providerModel,
      reasoningEffort: reasoningEffort ?? this.reasoningEffort,
      diffAdditions: diffAdditions ?? this.diffAdditions,
      diffDeletions: diffDeletions ?? this.diffDeletions,
      lastTaskId: lastTaskId ?? this.lastTaskId,
      currentTaskTitle: currentTaskTitle ?? this.currentTaskTitle,
      createdAt: createdAt ?? this.createdAt, updatedAt: updatedAt ?? this.updatedAt,
      lastInstructionAt: lastInstructionAt ?? this.lastInstructionAt,
    );
  }

  factory AiDevSession.fromJson(Map<String, dynamic> json) {
    final diffStats = extractSessionDiffStats(json);
    final agentName = (json['agentName'] ?? '').toString();
    final rawParentId = (json['parentSessionId'] as num?)?.toInt();
    final rawSubagents = json['subagents'];
    final subagents = rawSubagents is List
        ? rawSubagents
              .whereType<Map>()
              .map((e) => AiDevSession.fromJson(e.cast<String, dynamic>()))
              .toList()
        : const <AiDevSession>[];
    return AiDevSession(
      id: (json['id'] as num?)?.toInt() ?? 0,
      userId: (json['userId'] as num?)?.toInt() ?? 0,
      projectId: (json['projectId'] as num?)?.toInt() ?? 0,
      parentSessionId: rawParentId != null && rawParentId > 0 ? rawParentId : null,
      convoyPlanId: (json['convoyPlanId'] ?? '').toString(),
      convoyTaskId: (json['convoyTaskId'] ?? '').toString(),
      subagents: subagents,
      title: (json['title'] ?? '').toString(),
      agentName: agentName,
      workDir: (json['workDir'] ?? '').toString(),
      sourceWorkDir: (json['sourceWorkDir'] ?? '').toString(),
      worktreeBranch: (json['worktreeBranch'] ?? '').toString(),
      isolationMode: (json['isolationMode'] ?? '').toString(),
      status: (json['status'] ?? '').toString(),
      currentStage: (json['currentStage'] ?? '').toString(),
      approvalPolicy: (json['approvalPolicy'] ?? 'safe_auto').toString(),
      providerModel: extractSessionModelFromMap(json, runtime: agentName),
      reasoningEffort: (json['reasoningEffort'] ?? '').toString(),
      diffAdditions: diffStats.additions,
      diffDeletions: diffStats.deletions,
      lastTaskId: (json['lastTaskId'] as num?)?.toInt() ?? 0,
      currentTaskTitle: (json['currentTaskTitle'] ?? '').toString(),
      createdAt: DateTime.tryParse((json['createdAt'] ?? '').toString()),
      updatedAt: DateTime.tryParse((json['updatedAt'] ?? '').toString()),
      lastInstructionAt: DateTime.tryParse(
        (json['lastInstructionAt'] ?? '').toString(),
      ),
    );
  }
}

class CodeProject {
  final int id;
  final String name;
  final String description;
  final String workDir;
  final List<String> sourceDirs;
  final bool collapsed;

  const CodeProject({
    required this.id,
    required this.name,
    this.description = '',
    required this.workDir,
    this.sourceDirs = const [],
    this.collapsed = false,
  });

  factory CodeProject.fromJson(Map<String, dynamic> json) {
    final isCollapsed =
        json['collapsed'] == true ||
        json['isCollapsed'] == true ||
        json['expanded'] == false;
    return CodeProject(
      id: (json['id'] as num?)?.toInt() ?? 0,
      name: (json['name'] ?? '').toString(),
      description: (json['description'] ?? '').toString(),
      workDir: (json['workDir'] ?? '').toString(),
      sourceDirs: (json['sourceDirs'] as List<dynamic>? ?? const [])
          .map((item) => item.toString())
          .where((item) => item.isNotEmpty)
          .toList(),
      collapsed: isCollapsed,
    );
  }
}

class CodeWorktreeCapability {
  final bool available;
  final String reason;
  final List<String> sourceDirs;
  final int repositoryCount;
  final List<String> dirtyRepositories;
  final bool snapshotSupported;

  const CodeWorktreeCapability({
    required this.available,
    required this.reason,
    required this.sourceDirs,
    required this.repositoryCount,
    required this.dirtyRepositories,
    required this.snapshotSupported,
  });

  bool get canCreate => available && dirtyRepositories.isEmpty;

  factory CodeWorktreeCapability.fromJson(Map<String, dynamic> json) {
    return CodeWorktreeCapability(
      available: json['available'] == true,
      reason: (json['reason'] ?? '').toString(),
      sourceDirs: (json['sourceDirs'] as List<dynamic>? ?? const [])
          .map((item) => item.toString())
          .where((item) => item.isNotEmpty)
          .toList(),
      repositoryCount: (json['repositoryCount'] as num?)?.toInt() ?? 0,
      dirtyRepositories:
          (json['dirtyRepositories'] as List<dynamic>? ?? const [])
              .map((item) => item.toString())
              .where((item) => item.isNotEmpty)
              .toList(),
      snapshotSupported: json['snapshotSupported'] == true,
    );
  }
}

class CodeExecutor {
  final String id;
  final String name;
  final String description;
  final bool available;
  final String version;
  final String reason;
  final bool nativeTerminal;
  final List<String> capabilities;
  final List<String> approvalPolicies;
  final List<CodeExecutorOption> modelOptions;
  final List<CodeExecutorOption> reasoningEfforts;

  const CodeExecutor({
    required this.id,
    required this.name,
    required this.description,
    required this.available,
    required this.version,
    required this.reason,
    required this.nativeTerminal,
    required this.capabilities,
    required this.approvalPolicies,
    this.modelOptions = const [],
    this.reasoningEfforts = const [],
  });

  bool get supportsAutomation => capabilities.contains('automation');

  bool rejectsApprovalInConversation(String? policy) =>
      policy != null &&
      policy != 'full_auto' &&
      approvalPolicies.length > 1 &&
      capabilities.contains('structured_turn') &&
      !capabilities.contains('conversation_approval');

  factory CodeExecutor.fromJson(Map<String, dynamic> json) {
    return CodeExecutor(
      id: (json['id'] ?? '').toString(),
      name: (json['name'] ?? '').toString(),
      description: (json['description'] ?? '').toString(),
      available: json['available'] == true,
      version: (json['version'] ?? '').toString(),
      reason: (json['reason'] ?? '').toString(),
      nativeTerminal: json['nativeTerminal'] == true,
      capabilities: (json['capabilities'] as List<dynamic>? ?? const [])
          .map((item) => item.toString())
          .toList(),
      approvalPolicies:
          (json['approvalPolicies'] as List<dynamic>? ?? const ['full_auto'])
              .map((item) => item.toString())
              .toList(),
      modelOptions: _executorOptions(json['modelOptions']),
      reasoningEfforts: _executorOptions(json['reasoningEfforts']),
    );
  }
}

class CodeExecutorOption {
  final String value;
  final String label;

  const CodeExecutorOption({required this.value, required this.label});

  factory CodeExecutorOption.fromJson(Map<String, dynamic> json) {
    return CodeExecutorOption(
      value: (json['value'] ?? '').toString(),
      label: (json['label'] ?? json['value'] ?? '').toString(),
    );
  }
}

List<CodeExecutorOption> _executorOptions(dynamic value) {
  return (value as List<dynamic>? ?? const [])
      .whereType<Map>()
      .map((item) => CodeExecutorOption.fromJson(item.cast<String, dynamic>()))
      .where((item) => item.value.isNotEmpty)
      .toList();
}

class AiInstruction {
  final int id, sessionId, taskId;
  final String content, status;
  final bool allowCode, autoPreview, requireApproval, analysisOnly;
  final DateTime? createdAt;

  const AiInstruction({
    required this.id, required this.sessionId, required this.taskId,
    required this.content, required this.status,
    required this.allowCode, required this.autoPreview,
    required this.requireApproval, required this.analysisOnly,
    required this.createdAt,
  });

  factory AiInstruction.fromJson(Map<String, dynamic> json) {
    return AiInstruction(
      id: (json['id'] as num?)?.toInt() ?? 0,
      sessionId: (json['sessionId'] as num?)?.toInt() ?? 0,
      taskId: (json['taskId'] as num?)?.toInt() ?? 0,
      content: (json['content'] ?? '').toString(),
      status: (json['status'] ?? '').toString(),
      allowCode: json['allowCode'] == true,
      autoPreview: json['autoPreview'] == true,
      requireApproval: json['requireApproval'] == true,
      analysisOnly: json['analysisOnly'] == true,
      createdAt: DateTime.tryParse((json['createdAt'] ?? '').toString()),
    );
  }
}

class AiTaskSummary {
  final int id;
  final String title, status, workDir, agentName;

  const AiTaskSummary({
    required this.id, required this.title, required this.status,
    required this.workDir, required this.agentName,
  });

  factory AiTaskSummary.fromJson(Map<String, dynamic> json) {
    return AiTaskSummary(
      id: (json['id'] as num?)?.toInt() ?? 0,
      title: (json['title'] ?? '').toString(),
      status: (json['status'] ?? '').toString(),
      workDir: (json['workDir'] ?? '').toString(),
      agentName: (json['agentName'] ?? '').toString(),
    );
  }
}

class AiPreview {
  final int id, sessionId, taskId, instructionId;
  final String previewType, source, title, url, status;
  final DateTime? createdAt, updatedAt, lastCheckedAt;

  const AiPreview({
    required this.id, required this.sessionId, required this.taskId,
    required this.instructionId, required this.previewType,
    required this.source, required this.title, required this.url,
    required this.status, required this.createdAt,
    required this.updatedAt, required this.lastCheckedAt,
  });

  factory AiPreview.fromJson(Map<String, dynamic> json) {
    return AiPreview(
      id: (json['id'] as num?)?.toInt() ?? 0,
      sessionId: (json['sessionId'] as num?)?.toInt() ?? 0,
      taskId: (json['taskId'] as num?)?.toInt() ?? 0,
      instructionId: (json['instructionId'] as num?)?.toInt() ?? 0,
      previewType: (json['previewType'] ?? '').toString(),
      source: (json['source'] ?? '').toString(),
      title: (json['title'] ?? '').toString(),
      url: (json['url'] ?? '').toString(),
      status: (json['status'] ?? '').toString(),
      createdAt: DateTime.tryParse((json['createdAt'] ?? '').toString()),
      updatedAt: DateTime.tryParse((json['updatedAt'] ?? '').toString()),
      lastCheckedAt: DateTime.tryParse((json['lastCheckedAt'] ?? '').toString()),
    );
  }
}

class AiTimelineEvent {
  final int id, sessionId, taskId, instructionId;
  final String eventType, stage, title, content, status, meta;
  final DateTime? createdAt, updatedAt;

  const AiTimelineEvent({
    required this.id, required this.sessionId, required this.taskId,
    required this.instructionId, required this.eventType,
    required this.stage, required this.title, required this.content,
    required this.status, required this.meta,
    required this.createdAt, required this.updatedAt,
  });

  factory AiTimelineEvent.fromJson(Map<String, dynamic> json) {
    return AiTimelineEvent(
      id: (json['id'] as num?)?.toInt() ?? 0,
      sessionId: (json['sessionId'] as num?)?.toInt() ?? 0,
      taskId: (json['taskId'] as num?)?.toInt() ?? 0,
      instructionId: (json['instructionId'] as num?)?.toInt() ?? 0,
      eventType: (json['eventType'] ?? '').toString(),
      stage: (json['stage'] ?? '').toString(),
      title: (json['title'] ?? '').toString(),
      content: (json['content'] ?? '').toString(),
      status: (json['status'] ?? '').toString(),
      meta: (json['meta'] ?? '').toString(),
      createdAt: DateTime.tryParse((json['createdAt'] ?? '').toString()),
      updatedAt: DateTime.tryParse((json['updatedAt'] ?? '').toString()),
    );
  }
}

class AiQuestionOption {
  final String label, description;

  const AiQuestionOption({required this.label, this.description = ''});

  factory AiQuestionOption.fromDynamic(dynamic raw) {
    if (raw is String) return AiQuestionOption(label: raw.trim());
    if (raw is Map) {
      final map = raw.cast<String, dynamic>();
      final label = (map['label'] ?? map['value'] ?? map['title'] ?? map['text'] ?? map['name'] ?? '').toString().trim();
      final desc = (map['description'] ?? map['detail'] ?? map['hint'] ?? '').toString().trim();
      return AiQuestionOption(label: label, description: desc);
    }
    return const AiQuestionOption(label: '');
  }
}

class AiQuestionItem {
  final String question, header;
  final List<AiQuestionOption> options;
  final bool multiple, custom;

  const AiQuestionItem({
    required this.question, this.header = '',
    this.options = const <AiQuestionOption>[],
    this.multiple = false, this.custom = true,
  });

  factory AiQuestionItem.fromMap(Map<String, dynamic> map) {
    final question = (map['question'] ?? map['prompt'] ?? map['text'] ?? map['title'] ?? '').toString().trim();
    final header = (map['header'] ?? '').toString().trim();
    final rawOpts = map['options'];
    final options = <AiQuestionOption>[];
    if (rawOpts is List) {
      for (final item in rawOpts) {
        final opt = AiQuestionOption.fromDynamic(item);
        if (opt.label.isNotEmpty) options.add(opt);
      }
    }
    final multiple = map['multiple'] == true || map['multiSelect'] == true || map['is_multi_select'] == true;
    final custom = map['custom'] != false;
    return AiQuestionItem(
      question: question.isNotEmpty ? question : header,
      header: header, options: options, multiple: multiple, custom: custom,
    );
  }
}

class AiApproval {
  final int id, sessionId, taskId, instructionId, requestUserId, approveUserId;
  final String title, content, riskLevel, status, decision, decisionReason;
  final DateTime? createdAt, updatedAt, decisionAt;
  final List<AiQuestionItem> questions;

  bool get isQuestion => questions.isNotEmpty;

  const AiApproval({
    required this.id, required this.sessionId, required this.taskId,
    required this.instructionId, required this.requestUserId,
    required this.approveUserId, required this.title, required this.content,
    required this.riskLevel, required this.status, required this.decision,
    required this.decisionReason, required this.createdAt,
    required this.updatedAt, required this.decisionAt,
    this.questions = const <AiQuestionItem>[],
  });

  factory AiApproval.fromJson(Map<String, dynamic> json) {
    final rawQuestions = json['questions'];
    final questions = <AiQuestionItem>[];
    if (rawQuestions is List) {
      for (final item in rawQuestions.whereType<Map>()) {
        final q = AiQuestionItem.fromMap(item.cast<String, dynamic>());
        if (q.question.isNotEmpty || q.options.isNotEmpty) questions.add(q);
      }
    }
    return AiApproval(
      id: (json['id'] as num?)?.toInt() ?? 0,
      sessionId: (json['sessionId'] as num?)?.toInt() ?? 0,
      taskId: (json['taskId'] as num?)?.toInt() ?? 0,
      instructionId: (json['instructionId'] as num?)?.toInt() ?? 0,
      requestUserId: (json['requestUserId'] as num?)?.toInt() ?? 0,
      approveUserId: (json['approveUserId'] as num?)?.toInt() ?? 0,
      title: (json['title'] ?? '').toString(),
      content: (json['content'] ?? '').toString(),
      riskLevel: (json['riskLevel'] ?? '').toString(),
      status: (json['status'] ?? '').toString(),
      decision: (json['decision'] ?? '').toString(),
      decisionReason: (json['decisionReason'] ?? '').toString(),
      createdAt: DateTime.tryParse((json['createdAt'] ?? '').toString()),
      updatedAt: DateTime.tryParse((json['updatedAt'] ?? '').toString()),
      decisionAt: DateTime.tryParse((json['decisionAt'] ?? '').toString()),
      questions: questions,
    );
  }
}

const Set<String> _placeholderSessionTitlesLower = <String>{
  'new session', 'new thread', 'untitled', 'new dialogue', 'image prompt',
  'claude code session', 'antigravity session', 'gemini cli session',
  'codebuddy session', 'qoder session', 'opencode session', 'codex session',
  '新会话', '新建会话', '新对话', '未命名', '未命名会话',
};

final RegExp _genericDevSessionTitleRe = RegExp(
  r'^开发(?:会话)?\s*#\d+$',
  caseSensitive: false,
);

bool isPlaceholderSessionTitle(String? title) {
  final normalized = (title ?? '').trim();
  if (normalized.isEmpty) return true;
  final lower = normalized.toLowerCase();
  if (_placeholderSessionTitlesLower.contains(lower)) return true;
  if (lower.startsWith('new session -') || lower.startsWith('child session -')) {
    return true;
  }
  return _genericDevSessionTitleRe.hasMatch(normalized);
}

String extractSessionTitleFromPrompt(
  String promptText, {
  bool hasImage = false,
}) {
  final strippedCode = promptText
      .replaceAll(RegExp(r'```[\s\S]*?```'), ' ')
      .replaceAll(RegExp(r'\[Attachment:[^\]]*\]', caseSensitive: false), ' ');
  final lines = strippedCode
      .split(RegExp(r'\r?\n'))
      .map((line) => line.trim())
      .where((line) => line.isNotEmpty)
      .toList();
  var firstLine = '';
  for (final line in lines) {
    final withoutFileRefs = line.replaceAll(RegExp(r'(?:^|\s)@\S+'), ' ').trim();
    if (withoutFileRefs.isNotEmpty) {
      firstLine = withoutFileRefs;
      break;
    }
  }
  if (firstLine.isEmpty && lines.isNotEmpty) {
    firstLine = lines.first.replaceAll(RegExp(r'^@+'), '').trim();
  }
  firstLine = firstLine
      .replaceFirst(RegExp(r'^(?:[#*\->\s]+|\d+[.)]\s+)+'), '')
      .replaceAll('`', '')
      .replaceAll(RegExp(r'\s+'), ' ')
      .trim();
  if (firstLine.isEmpty) return hasImage ? '图片指令' : '';

  var clause = firstLine;
  var sentenceBreak = -1;
  for (final sep in const <String>['。', '；', '！', '？', '. ', '? ', '! ']) {
    final idx = clause.indexOf(sep);
    if (idx > 6 && (sentenceBreak == -1 || idx < sentenceBreak)) {
      sentenceBreak = idx;
    }
  }
  if (sentenceBreak > 0) clause = clause.substring(0, sentenceBreak);
  clause = clause
      .replaceFirst(RegExp('^["\'“”‘’《》（）【】\\[\\]()]+'), '')
      .replaceFirst(RegExp('["\'“”‘’《》（）【】\\[\\]()，,、：:；;。.！!？?…]+\$'), '')
      .trim();
  if (clause.isEmpty) return hasImage ? '图片指令' : '';

  final hasCjk = RegExp(r'[\u4e00-\u9fff]').hasMatch(clause);
  final maxLen = hasCjk ? 18 : 42;
  final runes = clause.runes.toList();
  final sliced = runes.length > maxLen
      ? String.fromCharCodes(runes.take(maxLen)).trim()
      : clause;
  final cleaned = sliced
      .replaceFirst(RegExp('["\'“”‘’《》（）【】\\[\\]()，,、：:；;。.！!？?…]+\$'), '')
      .trim();
  if (cleaned.isNotEmpty) return cleaned;
  return hasImage ? '图片指令' : '';
}

final RegExp _convoyTitlePrefixInDisplayRe = RegExp(
  r'^Convoy\s*·\s*plan_[a-z0-9_-]+\s*·\s*',
  caseSensitive: false,
);
final RegExp _subagentTitleSuffixInDisplayRe = RegExp(
  r'\s+\(@[^\s)]+\s+subagent\)$',
  caseSensitive: false,
);

String _cleanSessionDisplayTitle(String value) {
  final stripped = value
      .trim()
      .replaceFirst(_convoyTitlePrefixInDisplayRe, '')
      .replaceFirst(_subagentTitleSuffixInDisplayRe, '')
      .trim();
  return stripped.isNotEmpty ? stripped : value.trim();
}

String resolveSessionDisplayTitle({
  AiDevSession? session,
  String? taskTitle,
  String fallback = '',
}) {
  final candidates = <String>[
    if (session != null) _cleanSessionDisplayTitle(session.title),
    if (taskTitle != null) _cleanSessionDisplayTitle(taskTitle),
    if (session != null) _cleanSessionDisplayTitle(session.currentTaskTitle),
  ];
  for (final item in candidates) {
    if (item.isNotEmpty && !isPlaceholderSessionTitle(item)) return item;
  }
  for (final item in <String>[
    if (taskTitle != null) _cleanSessionDisplayTitle(taskTitle),
    if (session != null) _cleanSessionDisplayTitle(session.currentTaskTitle),
    if (session != null) _cleanSessionDisplayTitle(session.title),
  ]) {
    if (item.isNotEmpty) return item;
  }
  return fallback;
}

const int kMaxSessionModelDisplayChars = 18;

String formatSessionModelDisplayLabel({
  String? providerModel,
  String? agentName,
  String fallback = '开发',
  int maxChars = kMaxSessionModelDisplayChars,
}) {
  final rawModel = (providerModel ?? '').trim();
  final stripped = rawModel.contains('/') ? rawModel.split('/').last.trim() : rawModel;
  final rawAgent = (agentName ?? '').trim();
  final chosen = stripped.isNotEmpty
      ? stripped
      : (rawAgent.isNotEmpty ? rawAgent : fallback.trim());
  if (chosen.isEmpty || maxChars <= 0) return chosen;
  final runes = chosen.runes.toList();
  if (runes.length <= maxChars) return chosen;
  return maxChars <= 1 ? '…' : '${String.fromCharCodes(runes.take(maxChars - 1))}…';
}

({int additions, int deletions}) extractSessionDiffStats(Map<String, dynamic> json) {
  int readInt(Map<String, dynamic> m, String k1, [String? k2]) {
    final raw = m[k1] ?? (k2 != null ? m[k2] : null);
    return raw is num ? raw.toInt() : (raw is String ? int.tryParse(raw.trim()) ?? 0 : 0);
  }
  var add = readInt(json, 'diffAdditions', 'additions');
  var del = readInt(json, 'diffDeletions', 'deletions');
  for (final k in const <String>['summary', 'git']) {
    if (add > 0 || del > 0) break;
    final nested = json[k];
    if (nested is Map) {
      final m = nested.cast<String, dynamic>();
      add = readInt(m, 'diffAdditions', 'additions');
      del = readInt(m, 'diffDeletions', 'deletions');
    }
  }
  return (additions: add < 0 ? 0 : add, deletions: del < 0 ? 0 : del);
}

String extractSessionModelFromMap(Map<String, dynamic> json, {String runtime = ''}) {
  final direct = (json['providerModel'] ?? '').toString().trim();
  if (direct.isNotEmpty) return direct;
  var provider = (json['providerID'] ?? json['providerId'] ?? '').toString().trim();
  var model = (json['modelID'] ?? json['modelId'] ?? '').toString().trim();
  final rawModelObj = json['model'];
  if (model.isEmpty && rawModelObj is Map) {
    final m = rawModelObj.cast<String, dynamic>();
    if (provider.isEmpty) provider = (m['providerID'] ?? m['providerId'] ?? '').toString().trim();
    model = (m['modelID'] ?? m['modelId'] ?? m['id'] ?? '').toString().trim();
  } else if (model.isEmpty && rawModelObj is String) {
    model = rawModelObj.trim();
  }
  if (model.isEmpty) return '';
  if (provider.isEmpty || provider == runtime.trim() || model.startsWith('$provider/') || model.contains('/')) {
    return model;
  }
  return '$provider/$model';
}
