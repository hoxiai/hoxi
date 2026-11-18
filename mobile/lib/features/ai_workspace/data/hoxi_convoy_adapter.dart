import 'package:dio/dio.dart';

import '../../../core/network/api_client.dart';
import '../models/ai_dev_session.dart';
import '../models/chat_message.dart';
import '../models/code_convoy_plan.dart';

class HoxiTaskToolCallRef {
  final String childHoxiId;
  final String description;
  final String agentType;
  final String status;

  const HoxiTaskToolCallRef({
    required this.childHoxiId,
    required this.description,
    this.agentType = 'general',
    this.status = 'completed',
  });
}

class HoxiConvoyAdapter {
  const HoxiConvoyAdapter._();

  /// Walks up parent links (max depth 8, cycle-safe) to find the root Hoxi session ID.
  static String resolveRootHoxiId(
    String hoxiId,
    Map<String, String> parentByHoxiId,
  ) {
    final clean = hoxiId.trim();
    if (clean.isEmpty) return '';
    var curr = clean;
    final visited = <String>{curr};
    for (var depth = 0; depth < 8; depth++) {
      final parent = (parentByHoxiId[curr] ?? '').trim();
      if (parent.isEmpty ||
          !parentByHoxiId.containsKey(parent) ||
          !visited.add(parent)) {
        break;
      }
      curr = parent;
    }
    return curr;
  }

  /// Creates and starts a new Convoy orchestration plan on Hoxi.
  static Future<CodeConvoyPlan> createOrchestrationPlan(
    ApiClient apiClient, {
    required String directory,
    required String parentHoxiSessionId,
    required String title,
    required int concurrency,
    required List<CodeConvoyTaskItem> tasks,
    String providerId = '',
    String modelId = '',
    required int Function(String) stableId,
  }) async {
    final res = await apiClient.post<Map<String, dynamic>>(
      '/api/hoxi/orchestration/plans',
      data: <String, dynamic>{
        'directory': directory,
        if (parentHoxiSessionId.trim().isNotEmpty)
          'parentSessionID': parentHoxiSessionId.trim(),
        if (title.trim().isNotEmpty) 'title': title.trim(),
        'mode': 'build',
        if (providerId.trim().isNotEmpty) 'providerID': providerId.trim(),
        if (modelId.trim().isNotEmpty) 'model': modelId.trim(),
        'concurrency': concurrency.clamp(1, 8),
        'tasks': tasks
            .map(
              (t) => <String, dynamic>{
                'id': t.id,
                'prompt': t.prompt,
                if (t.dependsOn.isNotEmpty) 'dependsOn': t.dependsOn,
              },
            )
            .toList(),
      },
    );
    return CodeConvoyPlan.fromJson(
      res.data ?? const <String, dynamic>{},
      stableId: stableId,
    );
  }

  /// Cancels an active Convoy orchestration plan.
  static Future<CodeConvoyPlan> cancelOrchestrationPlan(
    ApiClient apiClient, {
    required String planId,
    required int Function(String) stableId,
  }) async {
    final encoded = Uri.encodeComponent(planId.trim());
    final res = await apiClient.post<Map<String, dynamic>>(
      '/api/hoxi/orchestration/plans/$encoded/cancel',
      data: const <String, dynamic>{},
    );
    return CodeConvoyPlan.fromJson(
      res.data ?? const <String, dynamic>{},
      stableId: stableId,
    );
  }

  /// Retries failed/blocked tasks in a Convoy orchestration plan.
  static Future<CodeConvoyPlan> retryOrchestrationPlan(
    ApiClient apiClient, {
    required String planId,
    required int Function(String) stableId,
  }) async {
    final encoded = Uri.encodeComponent(planId.trim());
    final res = await apiClient.post<Map<String, dynamic>>(
      '/api/hoxi/orchestration/plans/$encoded/retry',
      data: const <String, dynamic>{},
    );
    return CodeConvoyPlan.fromJson(
      res.data ?? const <String, dynamic>{},
      stableId: stableId,
    );
  }

  /// Merges completed isolated worktree tasks of a Convoy plan back into the parent branch.
  static Future<CodeConvoyPlan> mergeOrchestrationPlan(
    ApiClient apiClient, {
    required String planId,
    required String parentHoxiSessionId,
    required int Function(String) stableId,
  }) async {
    final encoded = Uri.encodeComponent(planId.trim());
    final res = await apiClient.post<Map<String, dynamic>>(
      '/api/hoxi/orchestration/plans/$encoded/merge',
      data: <String, dynamic>{
        if (parentHoxiSessionId.trim().isNotEmpty)
          'sessionID': parentHoxiSessionId.trim(),
      },
    );
    return CodeConvoyPlan.fromJson(
      res.data ?? const <String, dynamic>{},
      stableId: stableId,
    );
  }

  /// Fetches orchestration plans for a session and/or directory when reachable.
  /// Gracefully returns empty list if the endpoint is unavailable (e.g. 403 over tunnel).
  static Future<List<CodeConvoyPlan>> fetchOrchestrationPlans(
    ApiClient apiClient, {
    required List<String> sessionHoxiIds,
    required String directory,
    required int Function(String) stableId,
  }) async {
    final cleanIds = sessionHoxiIds
        .map((id) => id.trim())
        .where((id) => id.isNotEmpty)
        .toSet()
        .toList();
    final cleanDir = directory.trim();
    if (cleanIds.isEmpty && cleanDir.isEmpty) return const [];

    Future<List<CodeConvoyPlan>> getList(Map<String, dynamic> query) async {
      try {
        final res = await apiClient.get<dynamic>(
          '/api/hoxi/orchestration/plans',
          queryParameters: query,
          options: Options(
            sendTimeout: const Duration(milliseconds: 2500),
            receiveTimeout: const Duration(milliseconds: 2500),
          ),
        );
        final raw = res.data;
        final list = raw is List
            ? raw
            : (raw is Map && raw['items'] is List
                  ? raw['items'] as List
                  : const []);
        return list
            .whereType<Map>()
            .map(
              (item) => CodeConvoyPlan.fromJson(
                item.cast<String, dynamic>(),
                stableId: stableId,
              ),
            )
            .where((p) => p.id.isNotEmpty && p.tasks.isNotEmpty)
            .toList();
      } catch (_) {
        return const [];
      }
    }

    final primaryQuery = <String, dynamic>{
      if (cleanIds.isNotEmpty) 'session': cleanIds.last,
      if (cleanDir.isNotEmpty) 'directory': cleanDir,
    };
    final list = await getList(primaryQuery);
    final mergedById = <String, CodeConvoyPlan>{
      for (final plan in list) plan.id: plan,
    };
    if (mergedById.isEmpty) return const [];

    final relatedSet = cleanIds.toSet();
    final filtered = mergedById.values.where((plan) {
      if (relatedSet.isEmpty) return true;
      if (relatedSet.contains(plan.parentHoxiSessionId)) return true;
      if (plan.tasks.any((t) => relatedSet.contains(t.hoxiSessionId))) {
        return true;
      }
      return !plan.isTerminal;
    }).toList();

    filtered.sort((a, b) {
      final aTime =
          a.updatedAt ?? a.createdAt ?? DateTime.fromMillisecondsSinceEpoch(0);
      final bTime =
          b.updatedAt ?? b.createdAt ?? DateTime.fromMillisecondsSinceEpoch(0);
      return bTime.compareTo(aTime);
    });
    return filtered.take(8).toList();
  }

  /// Combines fetched orchestration plans, child sessions (`Convoy · plan_...` and subagents),
  /// and `task` tool calls into unified `convoyPlans` and `subagents` for a session.
  static ({List<CodeConvoyPlan> convoyPlans, List<CodeSubagentItem> subagents})
  buildSessionConvoyAndSubagents({
    required int rootSessionId,
    required String rootHoxiId,
    required String rootTitle,
    required String projectDir,
    required List<CodeConvoyPlan> fetchedPlans,
    required List<AiDevSession> childSessions,
    required Map<int, String> hoxiIdByIntId,
    required Set<int> pendingApprovalSessionIds,
    required List<HoxiTaskToolCallRef> taskToolCalls,
    required List<ChatMessage> chatMessages,
    required int Function(String) stableId,
  }) {
    final childByIntId = <int, AiDevSession>{
      for (final s in childSessions) s.id: s,
    };
    final childByHoxiId = <String, AiDevSession>{
      for (final s in childSessions)
        if ((hoxiIdByIntId[s.id] ?? '').isNotEmpty) hoxiIdByIntId[s.id]!: s,
    };

    // Extract any inline hoxi-subtasks / hoxi-convoy blocks from assistant messages
    final inlinePlans = <CodeConvoyPlan>[];
    for (final msg in chatMessages) {
      if (msg.isUser || msg.text.isEmpty) continue;
      for (final seg in splitMarkdownWithConvoyBlocks(msg.text)) {
        if (seg.convoyPlan != null) {
          inlinePlans.add(seg.convoyPlan!);
        }
      }
    }

    // 1. Enrich fetched orchestration plans with live child session stats
    final plansById = <String, CodeConvoyPlan>{};
    for (final plan in fetchedPlans) {
      final enrichedTasks = plan.tasks.map((task) {
        final child =
            (task.hoxiSessionId.isNotEmpty
                ? childByHoxiId[task.hoxiSessionId]
                : null) ??
            (task.sessionId != null ? childByIntId[task.sessionId!] : null) ??
            childSessions
                .where(
                  (s) => s.convoyPlanId == plan.id && s.convoyTaskId == task.id,
                )
                .firstOrNull;
        if (child == null) return task;
        final childHoxi = hoxiIdByIntId[child.id] ?? task.hoxiSessionId;
        final childRunning =
            child.status == 'running' ||
            child.currentStage == 'running' ||
            child.currentStage == 'awaiting_approval';
        final nextStatus = childRunning
            ? 'running'
            : (child.status == 'failed' || child.currentStage == 'failed'
                  ? 'failed'
                  : task.status);
        return task.copyWith(
          sessionId: child.id,
          hoxiSessionId: childHoxi,
          status: nextStatus,
          diffAdditions: child.diffAdditions > 0
              ? child.diffAdditions
              : task.diffAdditions,
          diffDeletions: child.diffDeletions > 0
              ? child.diffDeletions
              : task.diffDeletions,
        );
      }).toList();
      final anyRunning = enrichedTasks.any((t) => t.isRunning);
      final allDone =
          enrichedTasks.isNotEmpty && enrichedTasks.every((t) => t.isSucceeded);
      final anyFailed = enrichedTasks.any((t) => t.isFailed);
      final effectiveStatus = anyRunning
          ? 'running'
          : (plan.status == 'running' && allDone
                ? 'succeeded'
                : (plan.status == 'running' && anyFailed
                      ? 'failed'
                      : plan.status));
      plansById[plan.id] = plan.copyWith(
        parentSessionId: plan.parentSessionId ?? rootSessionId,
        parentHoxiSessionId: plan.parentHoxiSessionId.isNotEmpty
            ? plan.parentHoxiSessionId
            : rootHoxiId,
        status: effectiveStatus,
        tasks: enrichedTasks,
      );
    }

    // 2. Reconstruct Convoy plans from `Convoy · <planID> · <taskID>` child sessions
    //    when `/api/hoxi/orchestration/plans` was unreachable (e.g. over GoPanel tunnel).
    final convoyChildrenByPlanId = <String, List<AiDevSession>>{};
    for (final child in childSessions) {
      final meta = child.convoyPlanId.isNotEmpty
          ? (planId: child.convoyPlanId, taskId: child.convoyTaskId)
          : extractConvoyMetaFromTitle(child.title);
      if (meta == null || meta.planId.isEmpty) continue;
      convoyChildrenByPlanId
          .putIfAbsent(meta.planId, () => <AiDevSession>[])
          .add(child);
    }

    for (final entry in convoyChildrenByPlanId.entries) {
      final planId = entry.key;
      if (plansById.containsKey(planId)) continue;
      final group = entry.value;
      final matchedInline = inlinePlans.lastOrNull;
      final inlineTaskById = <String, CodeConvoyTaskItem>{
        if (matchedInline != null)
          for (final t in matchedInline.tasks) t.id: t,
      };
      final tasks = <CodeConvoyTaskItem>[];
      final seenTaskIds = <String>{};
      for (final child in group) {
        final meta = extractConvoyMetaFromTitle(child.title);
        final taskId = child.convoyTaskId.isNotEmpty
            ? child.convoyTaskId
            : (meta?.taskId ?? 'task-${tasks.length + 1}');
        if (!seenTaskIds.add(taskId)) continue;
        final inlineSpec = inlineTaskById[taskId];
        final isRunning =
            child.status == 'running' ||
            child.currentStage == 'running' ||
            child.currentStage == 'awaiting_approval';
        final isFailed =
            child.status == 'failed' || child.currentStage == 'failed';
        final status = isRunning
            ? 'running'
            : (isFailed ? 'failed' : 'succeeded');
        tasks.add(
          CodeConvoyTaskItem(
            id: taskId,
            prompt: inlineSpec?.prompt ?? stripSubagentTitleSuffix(child.title),
            dependsOn: inlineSpec?.dependsOn ?? const [],
            status: status,
            hoxiSessionId: hoxiIdByIntId[child.id] ?? '',
            sessionId: child.id,
            diffAdditions: child.diffAdditions,
            diffDeletions: child.diffDeletions,
            startedAt: child.createdAt,
            endedAt: isRunning ? null : child.updatedAt,
          ),
        );
      }
      if (matchedInline != null) {
        for (final spec in matchedInline.tasks) {
          if (seenTaskIds.add(spec.id)) {
            tasks.add(spec);
          }
        }
      }
      final anyRunning = tasks.any((t) => t.isRunning);
      final anyFailed = tasks.any((t) => t.isFailed);
      final allDone = tasks.isNotEmpty && tasks.every((t) => t.isSucceeded);
      final planStatus = anyRunning
          ? 'running'
          : (anyFailed ? 'failed' : (allDone ? 'succeeded' : 'queued'));
      final createdAt = group
          .map((s) => s.createdAt)
          .whereType<DateTime>()
          .fold<DateTime?>(
            null,
            (prev, dt) => prev == null || dt.isBefore(prev) ? dt : prev,
          );
      final updatedAt = group
          .map((s) => s.updatedAt ?? s.createdAt)
          .whereType<DateTime>()
          .fold<DateTime?>(
            null,
            (prev, dt) => prev == null || dt.isAfter(prev) ? dt : prev,
          );
      plansById[planId] = CodeConvoyPlan(
        id: planId,
        parentHoxiSessionId: rootHoxiId,
        parentSessionId: rootSessionId,
        title: (matchedInline != null && matchedInline.title.isNotEmpty)
            ? matchedInline.title
            : '多 Agent 编队',
        directory: projectDir,
        concurrency: matchedInline?.concurrency ?? 3,
        isolated: group.any((s) => s.isolationMode == 'worktree'),
        status: planStatus,
        tasks: tasks,
        createdAt: createdAt,
        updatedAt: updatedAt,
      );
    }

    // 3. Build unified subagent items list (task tool subagents + child sessions)
    final subagentBySessionId = <int, CodeSubagentItem>{};
    var ordinal = 0;

    for (final call in taskToolCalls) {
      final cleanHoxi = call.childHoxiId.trim();
      if (cleanHoxi.isEmpty) continue;
      final sid = stableId(cleanHoxi);
      final child = childByIntId[sid] ?? childByHoxiId[cleanHoxi];
      ordinal++;
      final rawTitle = call.description.trim().isNotEmpty
          ? call.description.trim()
          : (child?.title ?? cleanHoxi);
      final resolved = resolveSubagentDisplayTitle(
        parentTitle: rootTitle,
        childTitle: rawTitle,
        ordinal: ordinal,
      );
      final needsAttention =
          pendingApprovalSessionIds.contains(sid) ||
          child?.currentStage == 'awaiting_approval';
      final isRunning =
          child?.status == 'running' ||
          child?.currentStage == 'running' ||
          call.status == 'running' ||
          call.status == 'pending';
      final isError =
          child?.status == 'failed' ||
          child?.currentStage == 'failed' ||
          call.status == 'error';
      final status = isRunning
          ? 'running'
          : (isError ? 'error' : 'completed');
      final convoyMeta = extractConvoyMetaFromTitle(child?.title);
      subagentBySessionId[sid] = CodeSubagentItem(
        sessionId: sid,
        hoxiSessionId: cleanHoxi,
        parentSessionId: rootSessionId,
        title: resolved.label,
        fullTitle: resolved.full,
        agentType: convoyMeta != null
            ? 'convoy'
            : (call.agentType.isNotEmpty ? call.agentType : 'subagent'),
        status: status,
        needsAttention: needsAttention,
        additions: child?.diffAdditions ?? 0,
        deletions: child?.diffDeletions ?? 0,
        convoyPlanId: child?.convoyPlanId.isNotEmpty == true
            ? child!.convoyPlanId
            : (convoyMeta?.planId ?? ''),
        convoyTaskId: child?.convoyTaskId.isNotEmpty == true
            ? child!.convoyTaskId
            : (convoyMeta?.taskId ?? ''),
      );
    }

    for (final child in childSessions) {
      if (subagentBySessionId.containsKey(child.id)) continue;
      ordinal++;
      final convoyMeta = extractConvoyMetaFromTitle(child.title);
      final resolved = resolveSubagentDisplayTitle(
        parentTitle: rootTitle,
        childTitle: child.title,
        ordinal: ordinal,
      );
      final needsAttention =
          pendingApprovalSessionIds.contains(child.id) ||
          child.currentStage == 'awaiting_approval';
      final isRunning =
          child.status == 'running' ||
          child.currentStage == 'running' ||
          child.currentStage == 'awaiting_approval';
      final isError =
          child.status == 'failed' || child.currentStage == 'failed';
      subagentBySessionId[child.id] = CodeSubagentItem(
        sessionId: child.id,
        hoxiSessionId: hoxiIdByIntId[child.id] ?? '',
        parentSessionId: rootSessionId,
        title: resolved.label,
        fullTitle: resolved.full,
        agentType: (child.isConvoyTask || convoyMeta != null)
            ? 'convoy'
            : 'subagent',
        status: isRunning ? 'running' : (isError ? 'error' : 'completed'),
        needsAttention: needsAttention,
        additions: child.diffAdditions,
        deletions: child.diffDeletions,
        convoyPlanId: child.convoyPlanId.isNotEmpty
            ? child.convoyPlanId
            : (convoyMeta?.planId ?? ''),
        convoyTaskId: child.convoyTaskId.isNotEmpty
            ? child.convoyTaskId
            : (convoyMeta?.taskId ?? ''),
      );
    }

    final plansList = plansById.values.toList()
      ..sort((a, b) {
        if (a.isRunning != b.isRunning) return a.isRunning ? -1 : 1;
        final aTime =
            a.updatedAt ??
            a.createdAt ??
            DateTime.fromMillisecondsSinceEpoch(0);
        final bTime =
            b.updatedAt ??
            b.createdAt ??
            DateTime.fromMillisecondsSinceEpoch(0);
        return bTime.compareTo(aTime);
      });

    final subagentsList = subagentBySessionId.values.toList();
    return (convoyPlans: plansList, subagents: subagentsList);
  }
}
