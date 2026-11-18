import '../models/ai_dev_session.dart';
import '../models/ai_session_state_info.dart';
import '../models/code_task.dart';
import '../models/session_context_label.dart';
import '../../../shared/models/task_entity.dart';
import '../../../shared/models/task_log.dart';
import '../../../shared/models/task_status.dart';
import '../../../shared/models/task_type.dart';
import '../../../shared/models/task_attention.dart';

/// 近期热点任务的默认时间窗口（10 分钟内有更新 + 正在运行 / 待审批）
const Duration defaultRecentTaskWindow = Duration(minutes: 10);

bool isBusySessionStatusValue(String? raw) {
  if (raw == null) return false;
  switch (raw.toLowerCase().trim()) {
    case 'busy':
    case 'running':
    case 'executing':
    case 'retry':
    case 'pending':
    case 'in_progress':
    case 'instruction_queued':
      return true;
    default:
      return false;
  }
}

bool isRecentTimestamp(
  DateTime? timestamp, {
  DateTime? now,
  Duration window = defaultRecentTaskWindow,
  bool treatNullAsRecent = false,
}) {
  if (timestamp == null) return treatNullAsRecent;
  final current = now ?? DateTime.now();
  return current.difference(timestamp) <= window;
}

bool isHotAiDevSession(
  AiDevSession session, {
  DateTime? now,
  Duration window = defaultRecentTaskWindow,
}) {
  if (isBusySessionStatusValue(session.status) ||
      isBusySessionStatusValue(session.currentStage) ||
      session.currentStage == 'awaiting_approval' ||
      session.currentStage == 'failed' ||
      session.status == 'failed') {
    return true;
  }
  return isRecentTimestamp(
    session.updatedAt ?? session.createdAt,
    now: now,
    window: window,
  );
}

bool isHotCodeTask(
  CodeTask task, {
  DateTime? now,
  Duration window = defaultRecentTaskWindow,
}) {
  if (isBusySessionStatusValue(task.status) ||
      task.status == 'awaiting_approval' ||
      task.status == 'failed') {
    return true;
  }
  return isRecentTimestamp(
    task.updatedAt ?? task.createdAt,
    now: now,
    window: window,
  );
}

List<TaskEntity> filterRecentActiveTaskEntities(
  List<TaskEntity> items, {
  DateTime? now,
  Duration window = defaultRecentTaskWindow,
  bool treatNullTimestampAsRecent = true,
}) {
  final current = now ?? DateTime.now();
  return items.where((task) {
    if (task.status == TaskStatus.running ||
        task.requiresAttention ||
        task.status == TaskStatus.failed) {
      return true;
    }
    return isRecentTimestamp(
      task.updatedAt ?? task.startedAt,
      now: current,
      window: window,
      treatNullAsRecent: treatNullTimestampAsRecent,
    );
  }).toList();
}

List<TaskEntity> mergeRecentTaskEntities({
  required List<TaskEntity> existingTasks,
  required List<TaskEntity> recentTasks,
  int? activeStreamingSessionId,
}) {
  final activeTaskKey =
      activeStreamingSessionId != null && activeStreamingSessionId > 0
          ? 'aiSession:$activeStreamingSessionId'
          : null;

  TaskEntity protectStreaming(TaskEntity task, {TaskEntity? previous}) {
    if (activeTaskKey == null || task.id != activeTaskKey) return task;
    if (task.status == TaskStatus.running) return task;
    return TaskEntity(
      id: task.id,
      title: task.title,
      type: task.type,
      status: TaskStatus.running,
      progress: previous?.progress ?? 0.65,
      startedAt: task.startedAt ?? previous?.startedAt,
      updatedAt: DateTime.now(),
      summary: previous?.summary ?? task.summary,
      error: null,
      attention: task.attention ?? previous?.attention,
      meta: {
        ...task.meta,
        'currentStage': 'running',
        'currentStageLabel': aiStageLabel('running'),
      },
      attentionOnly: task.attentionOnly,
    );
  }

  if (existingTasks.isEmpty) {
    return recentTasks.map((t) => protectStreaming(t)).toList();
  }

  final existingById = {for (final t in existingTasks) t.id: t};
  final recentById = <String, TaskEntity>{};
  for (final t in recentTasks) {
    final prev = existingById[t.id];
    var candidate = t;
    if (prev != null) {
      final mergedMeta = Map<String, String>.of(t.meta);
      for (final k in const <String>[
        'providerModel',
        'modelLabel',
        'diffAdditions',
        'diffDeletions',
        'projectName',
        'sourceWorkDir',
      ]) {
        if ((mergedMeta[k] ?? '').isEmpty && (prev.meta[k] ?? '').isNotEmpty) {
          mergedMeta[k] = prev.meta[k]!;
        }
      }
      candidate = TaskEntity(
        id: t.id,
        title: t.title,
        type: t.type,
        status: t.status,
        progress: t.progress,
        startedAt: t.startedAt,
        updatedAt: t.updatedAt,
        summary: t.summary,
        error: t.error,
        attention: t.attention,
        meta: mergedMeta,
        attentionOnly: t.attentionOnly,
      );
    }
    recentById[t.id] = protectStreaming(candidate, previous: prev);
  }

  final merged = <TaskEntity>[...recentById.values];
  for (final old in existingTasks) {
    if (recentById.containsKey(old.id)) continue;
    if (old.id == activeTaskKey) {
      merged.add(protectStreaming(old, previous: old));
      continue;
    }
    if (old.attentionOnly) {
      continue;
    }
    if (old.status == TaskStatus.running || old.requiresAttention) {
      merged.add(
        TaskEntity(
          id: old.id,
          title: old.title,
          type: old.type,
          status: TaskStatus.success,
          progress: 1.0,
          startedAt: old.startedAt,
          updatedAt: old.updatedAt,
          summary: old.summary,
          error: old.error,
          meta: {
            ...old.meta,
            'currentStage': 'idle',
            'currentStageLabel': aiStageLabel('idle'),
          },
        ),
      );
    } else {
      merged.add(old);
    }
  }

  merged.sort((a, b) {
    final aAct = a.status == TaskStatus.running || a.requiresAttention;
    final bAct = b.status == TaskStatus.running || b.requiresAttention;
    if (aAct != bAct) return aAct ? -1 : 1;
    final at =
        a.updatedAt ?? a.startedAt ?? DateTime.fromMillisecondsSinceEpoch(0);
    final bt =
        b.updatedAt ?? b.startedAt ?? DateTime.fromMillisecondsSinceEpoch(0);
    return bt.compareTo(at);
  });
  return merged;
}

TaskEntity buildAiTaskEntity({
  required int taskId,
  required int sessionId,
  required int projectId,
  required String agentName,
  String providerModel = '',
  String projectName = '',
  String sourceWorkDir = '',
  int diffAdditions = 0,
  int diffDeletions = 0,
  required String title,
  required String workDir,
  required String status,
  required String currentStage,
  required DateTime? createdAt,
  required DateTime? updatedAt,
  AiSessionStateInfo? state,
  TaskAttention? attention,
}) {
  final resolvedStage = currentStage.isNotEmpty
      ? currentStage
      : (state?.currentStage ?? '');
  final resolvedStatus = isBusySessionStatusValue(resolvedStage)
      ? 'running'
      : (status.isNotEmpty ? status : (state?.session.status ?? ''));
  final previewCount = state?.previews.length ?? 0;
  final latestInstruction = state?.latestInstruction?.content.trim() ?? '';
  final latestEventTitle = state?.timelineEvents.isNotEmpty == true
      ? state!.timelineEvents.first.title.trim()
      : '';
  final errorSummary = state?.errorSummary.trim() ?? '';
  final changedFiles = state?.changedFiles ?? const <String>[];
  final effectiveModel = providerModel.trim().isNotEmpty
      ? providerModel.trim()
      : (state?.session.providerModel.trim() ?? '');
  final effectiveSourceDir = sourceWorkDir.trim().isNotEmpty
      ? sourceWorkDir.trim()
      : (state?.session.sourceWorkDir.trim() ?? '');
  final resolvedProjectName = resolveWorkspaceProjectName(
    explicitProjectName: projectName,
    sourceWorkDir: effectiveSourceDir,
    workDir: workDir,
  );
  final hasStateDiff =
      state != null && (state.diffAdditions > 0 || state.diffDeletions > 0);
  final effectiveAdd = hasStateDiff
      ? state.diffAdditions
      : (state != null &&
                (state.session.diffAdditions > 0 ||
                    state.session.diffDeletions > 0)
            ? state.session.diffAdditions
            : diffAdditions);
  final effectiveDel = hasStateDiff
      ? state.diffDeletions
      : (state != null &&
                (state.session.diffAdditions > 0 ||
                    state.session.diffDeletions > 0)
            ? state.session.diffDeletions
            : diffDeletions);
  final modelLabel = formatSessionModelDisplayLabel(
    providerModel: effectiveModel,
    agentName: agentName,
    fallback: '',
  );

  final summary = () {
    if (errorSummary.isNotEmpty) return errorSummary;
    if (latestInstruction.isNotEmpty) return latestInstruction;
    if (latestEventTitle.isNotEmpty) return latestEventTitle;
    final parts = <String>[
      aiStageLabel(resolvedStage),
      if (previewCount > 0) '$previewCount 个预览',
      if (workDir.isNotEmpty) workDir,
    ];
    return parts.join(' · ');
  }();

  return TaskEntity(
    id: 'aiSession:$sessionId',
    title: title.isEmpty ? '开发会话 #$sessionId' : title,
    type: TaskType.ai,
    status: mapAiSessionStatus(resolvedStatus, resolvedStage),
    progress: aiSessionProgress(resolvedStage),
    startedAt: createdAt,
    updatedAt: updatedAt,
    summary: summary,
    error: errorSummary.isNotEmpty
        ? errorSummary
        : (resolvedStage == 'failed' ? '开发会话执行失败' : null),
    attention: attention,
    meta: {
      'sessionId': sessionId.toString(),
      'taskId': taskId.toString(),
      'projectId': projectId.toString(),
      'agentName': agentName,
      if (resolvedProjectName.isNotEmpty) 'projectName': resolvedProjectName,
      if (effectiveSourceDir.isNotEmpty) 'sourceWorkDir': effectiveSourceDir,
      if (effectiveModel.isNotEmpty) 'providerModel': effectiveModel,
      if (modelLabel.isNotEmpty) 'modelLabel': modelLabel,
      if (effectiveAdd > 0 || effectiveDel > 0) ...{
        'diffAdditions': effectiveAdd.toString(),
        'diffDeletions': effectiveDel.toString(),
      },
      'currentStage': resolvedStage,
      'currentStageLabel': aiStageLabel(resolvedStage),
      'previewCount': previewCount.toString(),
      'workDir': workDir,
      if (latestInstruction.isNotEmpty) 'latestInstruction': latestInstruction,
      if (latestEventTitle.isNotEmpty) 'latestEventTitle': latestEventTitle,
      if (errorSummary.isNotEmpty) 'errorSummary': errorSummary,
      if (changedFiles.isNotEmpty) 'changedFiles': changedFiles.join('\n'),
    },
  );
}

TaskLog buildAiTaskLog({
  required String taskId,
  required AiSessionStateInfo state,
}) {
  final lines = <String>[
    '会话 #${state.session.id}',
    '阶段: ${state.currentStage.isEmpty ? '-' : aiStageLabel(state.currentStage)}',
  ];

  if (state.currentTask != null) {
    lines.add('任务 #${state.currentTask!.id}');
  }
  if (state.errorSummary.trim().isNotEmpty) {
    lines
      ..add('')
      ..add('错误摘要:')
      ..add(state.errorSummary.trim());
  }
  if (state.changedFiles.isNotEmpty) {
    lines
      ..add('')
      ..add('涉及文件:');
    for (final file in state.changedFiles) {
      lines.add('- $file');
    }
  }
  if (state.timelineEvents.isNotEmpty) {
    final recentTimeline = state.timelineEvents.take(6).toList().reversed;
    lines
      ..add('')
      ..add('时间线:');
    for (final event in recentTimeline) {
      final title = event.title.trim().isEmpty
          ? event.eventType
          : event.title.trim();
      lines.add('- [$title] ${event.content.trim()}'.trim());
    }
  }
  if (state.recentOutput.trim().isNotEmpty) {
    lines
      ..add('')
      ..add('最近输出:')
      ..add(state.recentOutput.trim());
  }

  return TaskLog(
    taskId: taskId,
    lines: lines,
    status: mapAiSessionStatus(state.session.status, state.currentStage),
    meta: {
      'sessionId': state.session.id.toString(),
      'currentStage': state.currentStage,
      'currentStageLabel': aiStageLabel(state.currentStage),
      'previewCount': state.previews.length.toString(),
      if (state.errorSummary.trim().isNotEmpty)
        'errorSummary': state.errorSummary.trim(),
      if (state.changedFiles.isNotEmpty)
        'changedFiles': state.changedFiles.join('\n'),
      if (state.timelineEvents.isNotEmpty)
        'latestEventTitle': state.timelineEvents.first.title.trim(),
    },
  );
}

TaskStatus mapAiSessionStatus(String status, String currentStage) {
  final stage = currentStage.toLowerCase().trim();
  final normalized = status.toLowerCase().trim();
  if (stage == 'failed' ||
      stage == 'initialization_failed' ||
      stage == 'error' ||
      normalized == 'failed' ||
      normalized == 'error' ||
      normalized == 'cancelled') {
    return TaskStatus.failed;
  }
  if (stage == 'running' ||
      stage == 'executing' ||
      stage == 'instruction_queued' ||
      normalized == 'busy' ||
      normalized == 'running' ||
      normalized == 'executing' ||
      normalized == 'instruction_queued') {
    return TaskStatus.running;
  }
  return TaskStatus.success;
}

double? aiSessionProgress(String currentStage) {
  switch (currentStage) {
    case 'idle':
    case 'ready':
      return null;
    case 'instruction_queued':
      return 0.15;
    case 'awaiting_approval':
      return 0.5;
    case 'executing':
    case 'running':
      return 0.65;
    case 'completed':
    case 'preview_ready':
      return 1.0;
    case 'failed':
      return null;
    default:
      return null;
  }
}

String aiStageLabel(String currentStage) {
  switch (currentStage) {
    case 'instruction_queued':
      return '指令排队中';
    case 'awaiting_approval':
      return '等待人工审批';
    case 'executing':
    case 'running':
      return '执行中';
    case 'preview_ready':
      return '预览已生成';
    case 'completed':
      return '已完成';
    case 'failed':
      return '执行失败';
    case 'idle':
      return '空闲';
    default:
      return currentStage.isEmpty ? '会话中' : currentStage;
  }
}
