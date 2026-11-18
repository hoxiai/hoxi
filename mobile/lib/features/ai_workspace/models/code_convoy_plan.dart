import 'dart:convert';

import '../../../core/models/node_skill.dart';
import 'ai_dev_session.dart';
import 'code_instruction_options.dart';

bool isConvoyPlanTerminal(String status) {
  final normalized = status.trim().toLowerCase();
  return normalized == 'succeeded' ||
      normalized == 'completed' ||
      normalized == 'failed' ||
      normalized == 'cancelled';
}

final RegExp _convoyTitlePrefixRe = RegExp(
  r'^Convoy\s*·\s*(plan_[a-z0-9_-]+)\s*·\s*(.+)$',
  caseSensitive: false,
);

final RegExp _subagentTitleSuffixRe = RegExp(
  r'\s*\(@[\w.-]+ subagent\)\s*$',
  caseSensitive: false,
);

({String planId, String taskId})? extractConvoyMetaFromTitle(String? title) {
  final raw = (title ?? '').trim();
  if (raw.isEmpty) return null;
  final match = _convoyTitlePrefixRe.firstMatch(raw);
  if (match == null) return null;
  final planId = (match.group(1) ?? '').trim();
  final taskId = (match.group(2) ?? '').trim();
  if (planId.isEmpty || taskId.isEmpty) return null;
  return (planId: planId, taskId: taskId);
}

String stripSubagentTitleSuffix(String? title) {
  var raw = (title ?? '').trim();
  if (raw.isEmpty) return '';
  final convoyMatch = _convoyTitlePrefixRe.firstMatch(raw);
  if (convoyMatch != null) {
    raw = (convoyMatch.group(2) ?? '').trim();
  }
  return raw.replaceFirst(_subagentTitleSuffixRe, '').trim();
}

String _normalizeTitleForCompare(String title) {
  return title
      .toLowerCase()
      .replaceAll(RegExp(r'[\s\p{P}\p{S}]+', unicode: true), '');
}

bool isRedundantSubagentTitle(String? parentTitle, String? childTitle) {
  if (isPlaceholderSessionTitle(parentTitle)) return false;
  final parent = _normalizeTitleForCompare(parentTitle!);
  final child = _normalizeTitleForCompare(stripSubagentTitleSuffix(childTitle));
  if (parent.isEmpty || child.isEmpty) return false;
  if (parent == child) return true;
  final parentLen = parent.runes.length;
  final childLen = child.runes.length;
  if ((parentLen < 4 || childLen < 4)) return false;
  if (parent.contains(child)) return true;
  return child.contains(parent) && childLen <= (parentLen * 1.5).round();
}

({String label, String full, bool redundant}) resolveSubagentDisplayTitle({
  String? parentTitle,
  String? childTitle,
  int ordinal = 0,
}) {
  final full = stripSubagentTitleSuffix(childTitle);
  final redundant = full.isEmpty || isRedundantSubagentTitle(parentTitle, full);
  if (!redundant) {
    return (label: full, full: full, redundant: false);
  }
  final label = ordinal > 0 ? '子任务 $ordinal' : '子任务';
  return (label: label, full: full.isNotEmpty ? full : label, redundant: true);
}

bool isTaskLikeToolName(String? tool) {
  final lower = (tool ?? '').trim().toLowerCase();
  return lower == 'task' || lower == 'agent' || lower.contains('subagent');
}

String extractSubagentChildHoxiIdFromToolState(Map<String, dynamic> state) {
  final meta = state['metadata'] is Map
      ? (state['metadata'] as Map).cast<String, dynamic>()
      : const <String, dynamic>{};
  final input = state['input'] is Map
      ? (state['input'] as Map).cast<String, dynamic>()
      : const <String, dynamic>{};
  final direct =
      (meta['sessionId'] ?? meta['sessionID'] ?? input['task_id'] ?? '')
          .toString()
          .trim();
  if (direct.isNotEmpty) return direct;
  final output = (state['output'] ?? '').toString();
  if (output.isNotEmpty) {
    final m = RegExp(r'session_id:\s*([a-zA-Z0-9_-]+)').firstMatch(output);
    if (m != null) return (m.group(1) ?? '').trim();
  }
  return '';
}

class CodeConvoyTaskItem {
  final String id;
  final String prompt;
  final List<String> dependsOn;
  final String status;
  final String hoxiSessionId;
  final int? sessionId;
  final String summary;
  final String error;
  final int diffAdditions;
  final int diffDeletions;
  final DateTime? startedAt;
  final DateTime? endedAt;

  const CodeConvoyTaskItem({
    required this.id,
    required this.prompt,
    this.dependsOn = const [],
    this.status = 'queued',
    this.hoxiSessionId = '',
    this.sessionId,
    this.summary = '',
    this.error = '',
    this.diffAdditions = 0,
    this.diffDeletions = 0,
    this.startedAt,
    this.endedAt,
  });

  bool get isRunning => status == 'running';
  bool get isSucceeded => status == 'succeeded' || status == 'completed';
  bool get isFailed => status == 'failed' || status == 'blocked';

  String get displayPromptFirstLine {
    for (final line in prompt.split(RegExp(r'\r?\n'))) {
      final trimmed = line.trim();
      if (trimmed.isNotEmpty) {
        return trimmed.length > 120
            ? '${trimmed.substring(0, 120)}…'
            : trimmed;
      }
    }
    final fallback = prompt.trim();
    return fallback.length > 120
        ? '${fallback.substring(0, 120)}…'
        : fallback;
  }

  CodeConvoyTaskItem copyWith({
    String? id,
    String? prompt,
    List<String>? dependsOn,
    String? status,
    String? hoxiSessionId,
    int? sessionId,
    String? summary,
    String? error,
    int? diffAdditions,
    int? diffDeletions,
    DateTime? startedAt,
    DateTime? endedAt,
  }) {
    return CodeConvoyTaskItem(
      id: id ?? this.id,
      prompt: prompt ?? this.prompt,
      dependsOn: dependsOn ?? this.dependsOn,
      status: status ?? this.status,
      hoxiSessionId: hoxiSessionId ?? this.hoxiSessionId,
      sessionId: sessionId ?? this.sessionId,
      summary: summary ?? this.summary,
      error: error ?? this.error,
      diffAdditions: diffAdditions ?? this.diffAdditions,
      diffDeletions: diffDeletions ?? this.diffDeletions,
      startedAt: startedAt ?? this.startedAt,
      endedAt: endedAt ?? this.endedAt,
    );
  }

  factory CodeConvoyTaskItem.fromJson(
    Map<String, dynamic> json, {
    int Function(String)? stableId,
  }) {
    final rawHoxiSid = (json['sessionID'] ?? json['sessionId'] ?? '')
        .toString()
        .trim();
    final rawIntSid = (json['sessionIntId'] as num?)?.toInt();
    final resolvedSid =
        rawIntSid ??
        (rawHoxiSid.isNotEmpty && stableId != null
            ? stableId(rawHoxiSid)
            : null);
    final rawDeps = json['dependsOn'];
    final deps = rawDeps is List
        ? rawDeps
              .map((e) => e.toString().trim())
              .where((s) => s.isNotEmpty)
              .toList()
        : const <String>[];
    final startedMs = (json['startedAt'] as num?)?.toInt() ?? 0;
    final endedMs = (json['endedAt'] as num?)?.toInt() ?? 0;
    return CodeConvoyTaskItem(
      id: (json['id'] ?? '').toString().trim(),
      prompt: (json['prompt'] ?? '').toString().trim(),
      dependsOn: deps,
      status: _normalizeConvoyStatus((json['status'] ?? 'queued').toString()),
      hoxiSessionId: rawHoxiSid,
      sessionId: resolvedSid,
      summary: (json['summary'] ?? '').toString().trim(),
      error: (json['error'] ?? '').toString().trim(),
      diffAdditions: (json['diffAdditions'] as num?)?.toInt() ?? 0,
      diffDeletions: (json['diffDeletions'] as num?)?.toInt() ?? 0,
      startedAt: startedMs > 0
          ? DateTime.fromMillisecondsSinceEpoch(startedMs)
          : null,
      endedAt: endedMs > 0
          ? DateTime.fromMillisecondsSinceEpoch(endedMs)
          : null,
    );
  }
}

class CodeConvoyPlan {
  final String id;
  final String parentHoxiSessionId;
  final int? parentSessionId;
  final String title;
  final String directory;
  final String mode;
  final String provider;
  final String model;
  final int concurrency;
  final bool isolated;
  final String status;
  final List<CodeConvoyTaskItem> tasks;
  final DateTime? createdAt;
  final DateTime? updatedAt;
  final DateTime? mergedAt;
  final String error;
  final bool autoStart;

  const CodeConvoyPlan({
    required this.id,
    this.parentHoxiSessionId = '',
    this.parentSessionId,
    required this.title,
    this.directory = '',
    this.mode = 'build',
    this.provider = '',
    this.model = '',
    this.concurrency = 3,
    this.isolated = true,
    this.status = 'queued',
    this.tasks = const [],
    this.createdAt,
    this.updatedAt,
    this.mergedAt,
    this.error = '',
    this.autoStart = false,
  });

  int get totalCount => tasks.length;
  int get completedCount => tasks.where((t) => t.isSucceeded).length;
  int get runningCount => tasks.where((t) => t.isRunning).length;
  bool get isTerminal => isConvoyPlanTerminal(status);
  bool get isRunning =>
      status == 'running' || tasks.any((t) => t.status == 'running');
  bool get canRetry =>
      isTerminal &&
      tasks.every((t) => !t.isRunning) &&
      tasks.any((t) => !t.isSucceeded);
  bool get canMerge =>
      (status == 'succeeded' || status == 'completed') &&
      isolated &&
      mergedAt == null;

  int get totalDiffAdditions =>
      tasks.fold<int>(0, (sum, item) => sum + item.diffAdditions);
  int get totalDiffDeletions =>
      tasks.fold<int>(0, (sum, item) => sum + item.diffDeletions);

  CodeConvoyPlan copyWith({
    String? id,
    String? parentHoxiSessionId,
    int? parentSessionId,
    String? title,
    String? directory,
    String? mode,
    String? provider,
    String? model,
    int? concurrency,
    bool? isolated,
    String? status,
    List<CodeConvoyTaskItem>? tasks,
    DateTime? createdAt,
    DateTime? updatedAt,
    DateTime? mergedAt,
    String? error,
    bool? autoStart,
  }) {
    return CodeConvoyPlan(
      id: id ?? this.id,
      parentHoxiSessionId: parentHoxiSessionId ?? this.parentHoxiSessionId,
      parentSessionId: parentSessionId ?? this.parentSessionId,
      title: title ?? this.title,
      directory: directory ?? this.directory,
      mode: mode ?? this.mode,
      provider: provider ?? this.provider,
      model: model ?? this.model,
      concurrency: concurrency ?? this.concurrency,
      isolated: isolated ?? this.isolated,
      status: status ?? this.status,
      tasks: tasks ?? this.tasks,
      createdAt: createdAt ?? this.createdAt,
      updatedAt: updatedAt ?? this.updatedAt,
      mergedAt: mergedAt ?? this.mergedAt,
      error: error ?? this.error,
      autoStart: autoStart ?? this.autoStart,
    );
  }

  factory CodeConvoyPlan.fromJson(
    Map<String, dynamic> json, {
    int Function(String)? stableId,
  }) {
    final parentHoxi =
        (json['parentSessionID'] ?? json['parentSessionId'] ?? '')
            .toString()
            .trim();
    final rawParentInt = (json['parentSessionIntId'] as num?)?.toInt();
    final parentSid =
        rawParentInt ??
        (parentHoxi.isNotEmpty && stableId != null
            ? stableId(parentHoxi)
            : null);
    final rawTasks = json['tasks'] as List<dynamic>? ?? const [];
    final tasks = rawTasks
        .whereType<Map>()
        .map(
          (t) => CodeConvoyTaskItem.fromJson(
            t.cast<String, dynamic>(),
            stableId: stableId,
          ),
        )
        .where((t) => t.id.isNotEmpty)
        .toList();
    final createdMs = (json['createdAt'] as num?)?.toInt() ?? 0;
    final updatedMs = (json['updatedAt'] as num?)?.toInt() ?? createdMs;
    final mergedMs = (json['mergedAt'] as num?)?.toInt() ?? 0;
    final rawConcurrency = (json['concurrency'] as num?)?.toInt() ?? 3;
    return CodeConvoyPlan(
      id: (json['id'] ?? '').toString().trim(),
      parentHoxiSessionId: parentHoxi,
      parentSessionId: parentSid,
      title: (json['title'] ?? '').toString().trim(),
      directory: (json['directory'] ?? '').toString().trim(),
      mode: (json['mode'] ?? 'build').toString().trim(),
      provider: (json['provider'] ?? json['providerID'] ?? '')
          .toString()
          .trim(),
      model: (json['model'] ?? '').toString().trim(),
      concurrency: rawConcurrency.clamp(1, 8),
      isolated: json['isolated'] != false,
      status: _normalizeConvoyStatus((json['status'] ?? 'queued').toString()),
      tasks: tasks,
      createdAt: createdMs > 0
          ? DateTime.fromMillisecondsSinceEpoch(createdMs)
          : null,
      updatedAt: updatedMs > 0
          ? DateTime.fromMillisecondsSinceEpoch(updatedMs)
          : null,
      mergedAt: mergedMs > 0
          ? DateTime.fromMillisecondsSinceEpoch(mergedMs)
          : null,
      error: (json['error'] ?? '').toString().trim(),
      autoStart: json['autoStart'] == true,
    );
  }
}

/// 生成编队完成后的汇总提示词（对齐 PC 端 buildConvoyCompletionSummaryPrompt）。
String buildConvoyCompletionSummaryPrompt(CodeConvoyPlan plan) {
  final header =
      '并行子任务编队「${plan.title.trim().isNotEmpty ? plan.title.trim() : plan.id}」已执行完毕（状态：${plan.status}）。以下是各子任务的执行结果，请汇总整体改动并给出后续验证建议：';
  final lines = <String>[header, ''];
  for (final task in plan.tasks) {
    final detail = task.error.trim().isNotEmpty
        ? '错误：${task.error.trim()}'
        : (task.summary.trim().isNotEmpty
              ? task.summary.trim()
              : task.prompt.trim());
    lines.add('- **${task.id}** (`${task.status}`)：$detail');
  }
  return lines.join('\n');
}

/// 运行中待发指令队列项（对齐 PC 端 QueuedPromptItem）。
class CodeQueuedPromptItem {
  final String id;
  final int sessionId;
  final String text;
  final String? imageDataUrl;
  final List<String> attachedFiles;
  final NodeSkillSelection? skillSelection;
  final CodeInstructionOptions options;
  final DateTime createdAt;

  const CodeQueuedPromptItem({
    required this.id,
    required this.sessionId,
    required this.text,
    this.imageDataUrl,
    this.attachedFiles = const [],
    this.skillSelection,
    this.options = const CodeInstructionOptions(),
    required this.createdAt,
  });
}

class CodeSubagentItem {
  final int sessionId;
  final String hoxiSessionId;
  final int parentSessionId;
  final String title;
  final String fullTitle;
  final String agentType;
  final String status;
  final bool needsAttention;
  final int stepCount;
  final String currentStepLabel;
  final int additions;
  final int deletions;
  final String convoyPlanId;
  final String convoyTaskId;

  const CodeSubagentItem({
    required this.sessionId,
    this.hoxiSessionId = '',
    required this.parentSessionId,
    required this.title,
    required this.fullTitle,
    this.agentType = 'subagent',
    this.status = 'completed',
    this.needsAttention = false,
    this.stepCount = 0,
    this.currentStepLabel = '',
    this.additions = 0,
    this.deletions = 0,
    this.convoyPlanId = '',
    this.convoyTaskId = '',
  });

  String get key =>
      hoxiSessionId.isNotEmpty ? hoxiSessionId : 'subagent-$sessionId';
  bool get isConvoyTask => convoyPlanId.isNotEmpty || agentType == 'convoy';
  bool get isRunning => status == 'running';
  bool get isFailed => status == 'error' || status == 'failed';
  bool get isInterrupted => status == 'interrupted';
  bool get isCompleted => status == 'completed' || status == 'succeeded';
  bool get awaitingApproval =>
      needsAttention || status == 'awaiting_approval';
  int get diffAdditions => additions;
  int get diffDeletions => deletions;

  factory CodeSubagentItem.fromJson(Map<String, dynamic> json) {
    final title = (json['title'] ?? '').toString().trim();
    final full = (json['fullTitle'] ?? title).toString().trim();
    return CodeSubagentItem(
      sessionId: (json['sessionId'] as num?)?.toInt() ?? 0,
      hoxiSessionId: (json['hoxiSessionId'] ?? json['sessionID'] ?? '')
          .toString()
          .trim(),
      parentSessionId: (json['parentSessionId'] as num?)?.toInt() ?? 0,
      title: title,
      fullTitle: full,
      agentType: (json['agentType'] ?? 'subagent').toString().trim(),
      status: (json['status'] ?? 'completed').toString().trim(),
      needsAttention: json['needsAttention'] == true,
      stepCount: (json['stepCount'] as num?)?.toInt() ?? 0,
      currentStepLabel: (json['currentStepLabel'] ?? '').toString().trim(),
      additions: (json['additions'] as num?)?.toInt() ?? 0,
      deletions: (json['deletions'] as num?)?.toInt() ?? 0,
      convoyPlanId: (json['convoyPlanId'] ?? '').toString().trim(),
      convoyTaskId: (json['convoyTaskId'] ?? '').toString().trim(),
    );
  }
}

String _normalizeConvoyStatus(String raw) {
  final lower = raw.trim().toLowerCase();
  return switch (lower) {
    'queued' ||
    'running' ||
    'succeeded' ||
    'failed' ||
    'blocked' ||
    'cancelled' => lower,
    'completed' || 'success' => 'succeeded',
    'error' => 'failed',
    'pending' => 'queued',
    _ => 'queued',
  };
}

bool isSubtaskPlanCodeBlockLang(String? lang) {
  final clean = (lang ?? '').trim().toLowerCase();
  return clean == 'hoxi-subtasks' ||
      clean == 'hoxi-convoy' ||
      clean == 'subtasks-plan';
}

String _sanitizeSubtaskId(String value) {
  return value
      .trim()
      .toLowerCase()
      .replaceAll(RegExp(r'[^a-z0-9_-]+'), '-')
      .replaceAll(RegExp(r'-{2,}'), '-')
      .replaceAll(RegExp(r'^-+|-+$'), '');
}

bool _hasDependencyCycle(List<CodeConvoyTaskItem> tasks) {
  final indegree = <String, int>{};
  final edges = <String, List<String>>{};
  for (final t in tasks) {
    indegree[t.id] = t.dependsOn.length;
    for (final dep in t.dependsOn) {
      edges.putIfAbsent(dep, () => <String>[]).add(t.id);
    }
  }
  final queue = <String>[
    for (final entry in indegree.entries)
      if (entry.value == 0) entry.key,
  ];
  var visited = 0;
  while (queue.isNotEmpty) {
    final id = queue.removeAt(0);
    visited++;
    for (final next in edges[id] ?? const <String>[]) {
      final nextDeg = (indegree[next] ?? 0) - 1;
      indegree[next] = nextDeg;
      if (nextDeg == 0) queue.add(next);
    }
  }
  return visited != tasks.length;
}

CodeConvoyPlan? parseSubtaskPlanBlock(String raw) {
  final trimmed = raw.trim();
  if (trimmed.isEmpty) return null;
  dynamic parsed;
  try {
    parsed = jsonDecode(trimmed);
  } catch (_) {
    final start = trimmed.indexOf('{');
    final end = trimmed.lastIndexOf('}');
    if (start < 0 || end <= start) return null;
    try {
      parsed = jsonDecode(trimmed.substring(start, end + 1));
    } catch (_) {
      return null;
    }
  }
  final Map<String, dynamic> obj;
  if (parsed is List) {
    obj = <String, dynamic>{'title': '', 'concurrency': 3, 'tasks': parsed};
  } else if (parsed is Map) {
    obj = parsed.cast<String, dynamic>();
  } else {
    return null;
  }
  final rawTasks = obj['tasks'];
  if (rawTasks is! List || rawTasks.isEmpty || rawTasks.length > 20) {
    return null;
  }
  final idMap = <String, String>{};
  final usedIds = <String>{};
  final normalized = <({String id, String prompt, List<String> rawDeps})>[];

  for (var i = 0; i < rawTasks.length; i++) {
    final item = rawTasks[i];
    if (item is! Map) continue;
    final map = item.cast<String, dynamic>();
    final prompt = (map['prompt'] ?? '').toString().trim();
    if (prompt.isEmpty) continue;
    final rawId = (map['id'] ?? '').toString().trim();
    var id = _sanitizeSubtaskId(rawId);
    if (id.isEmpty) id = 'task-${i + 1}';
    final base = id;
    for (var n = 2; usedIds.contains(id); n++) {
      id = '$base-$n';
    }
    usedIds.add(id);
    if (rawId.isNotEmpty) idMap[rawId] = id;
    idMap[id] = id;
    final rawDeps = map['dependsOn'] is List
        ? (map['dependsOn'] as List)
              .map((d) => d.toString().trim())
              .where((s) => s.isNotEmpty)
              .toList()
        : const <String>[];
    normalized.add((id: id, prompt: prompt, rawDeps: rawDeps));
  }
  if (normalized.isEmpty) return null;

  final tasks = normalized.map((t) {
    final seen = <String>{};
    final dependsOn = <String>[];
    for (final rawDep in t.rawDeps) {
      final mapped = idMap[rawDep];
      if (mapped == null || mapped == t.id || !seen.add(mapped)) continue;
      dependsOn.add(mapped);
    }
    return CodeConvoyTaskItem(
      id: t.id,
      prompt: t.prompt,
      dependsOn: dependsOn,
      status: 'queued',
    );
  }).toList();

  if (_hasDependencyCycle(tasks)) return null;

  final rawConcurrency = (obj['concurrency'] as num?)?.toInt() ?? 3;
  final concurrency = rawConcurrency.clamp(1, 8);
  final title = (obj['title'] ?? '').toString().trim();

  return CodeConvoyPlan(
    id: 'inline_${title.hashCode}_${tasks.length}',
    title: title,
    concurrency: concurrency,
    status: 'queued',
    tasks: tasks,
    autoStart: obj['autoStart'] == true,
  );
}

class AiMarkdownSegment {
  final String? markdown;
  final CodeConvoyPlan? convoyPlan;

  const AiMarkdownSegment.markdown(this.markdown) : convoyPlan = null;
  const AiMarkdownSegment.convoy(this.convoyPlan) : markdown = null;
}

final RegExp _fencedConvoyBlockRe = RegExp(
  r'```([a-zA-Z0-9_-]+)[ \t]*\r?\n([\s\S]*?)```',
  multiLine: true,
);

List<AiMarkdownSegment> splitMarkdownWithConvoyBlocks(String text) {
  if (text.isEmpty || !text.contains('```')) {
    return <AiMarkdownSegment>[AiMarkdownSegment.markdown(text)];
  }
  final segments = <AiMarkdownSegment>[];
  var cursor = 0;
  for (final match in _fencedConvoyBlockRe.allMatches(text)) {
    final lang = (match.group(1) ?? '').trim();
    if (!isSubtaskPlanCodeBlockLang(lang)) continue;
    final code = match.group(2) ?? '';
    final parsed = parseSubtaskPlanBlock(code);
    if (parsed == null) continue;
    if (match.start > cursor) {
      final before = text.substring(cursor, match.start).trim();
      if (before.isNotEmpty) {
        segments.add(AiMarkdownSegment.markdown(before));
      }
    }
    segments.add(AiMarkdownSegment.convoy(parsed));
    cursor = match.end;
  }
  if (segments.isEmpty) {
    return <AiMarkdownSegment>[AiMarkdownSegment.markdown(text)];
  }
  if (cursor < text.length) {
    final after = text.substring(cursor).trim();
    if (after.isNotEmpty) {
      segments.add(AiMarkdownSegment.markdown(after));
    }
  }
  return segments;
}
