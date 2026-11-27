part of 'hoxi_workspace_adapter.dart';

extension HoxiSessionListAdapter on HoxiWorkspaceAdapter {
  /// Converts an externally fetched session list and registers its session references.
  List<AiDevSession> registerSessionList(
    Map<dynamic, dynamic> body, {
    required String fallbackProjectKey,
  }) {
    final rawList = body['items'] as List;
    final statusMap = <String, String>{};
    if (body['statuses'] is Map) {
      for (final entry in (body['statuses'] as Map).entries) {
        final k = entry.key.toString().trim();
        final v = entry.value is Map
            ? ((entry.value as Map)['type'] ?? '').toString().trim()
            : (entry.value ?? '').toString().trim();
        if (k.isNotEmpty && v.isNotEmpty) {
          statusMap[k] = v;
        }
      }
    }
    final hasAuthoritativePending = body['pendingApprovals'] is Map;
    final pendingApprovalCounts = <String, int>{};
    if (hasAuthoritativePending) {
      for (final entry in (body['pendingApprovals'] as Map).entries) {
        final k = entry.key.toString().trim();
        final count = (entry.value as num?)?.toInt() ?? 0;
        if (k.isNotEmpty && count > 0) {
          pendingApprovalCounts[k] = count;
        }
      }
    }

    final rawItems =
        <
          ({
            Map<String, dynamic> map,
            String hoxiId,
            String parentHoxiId,
            int intId,
            String workDir,
            DateTime? createdAt,
            DateTime? updatedAt,
          })
        >[];
    final seenIds = <int>{};
    for (final raw in rawList.whereType<Map>()) {
      final map = raw.cast<String, dynamic>();
      final hoxiId = (map['id'] ?? '').toString().trim();
      if (hoxiId.isEmpty) continue;
      final timeMap = map['time'] is Map
          ? (map['time'] as Map).cast<String, dynamic>()
          : const <String, dynamic>{};
      if (map['archived'] == true ||
          ((timeMap['archived'] as num?)?.toInt() ?? 0) > 0) {
        continue;
      }
      final intId = HoxiWorkspaceAdapter.stablePositiveIntId(hoxiId);
      if (!seenIds.add(intId)) continue;
      final parentHoxiId =
          (map['parentID'] ?? map['parentId'] ?? '').toString().trim();
      final workDir =
          (map['directory'] ?? map['worktreePath'] ?? '').toString().trim();
      final createdMs = (timeMap['created'] as num?)?.toInt() ?? 0;
      final updatedMs = (timeMap['updated'] as num?)?.toInt() ?? createdMs;
      rawItems.add((
        map: map,
        hoxiId: hoxiId,
        parentHoxiId: parentHoxiId,
        intId: intId,
        workDir: workDir,
        createdAt: createdMs > 0
            ? DateTime.fromMillisecondsSinceEpoch(createdMs)
            : null,
        updatedAt: updatedMs > 0
            ? DateTime.fromMillisecondsSinceEpoch(updatedMs)
            : null,
      ));
    }

    final parentByHoxiId = <String, String>{
      for (final e in rawItems) e.hoxiId: e.parentHoxiId,
    };
    final builtByHoxiId = <String, AiDevSession>{};

    for (final entry in rawItems) {
      final map = entry.map;
      final hoxiId = entry.hoxiId;
      final intId = entry.intId;
      final workDir = entry.workDir;
      final sourceDir = _HoxiSessionTurnParsing.resolveSourceWorkDir(
        map,
        workDir,
        workDir,
      );
      final rawRuntime = (map['runtime'] ?? '').toString().trim();
      final runtime = rawRuntime.isNotEmpty
          ? rawRuntime
          : (hoxiId.startsWith('ses_nat_') ? 'native' : '');
      final sandboxMode = HoxiSessionDiffScope.resolveSandboxMode(
        map,
        workDir,
      );
      final branch = (map['branch'] ?? '').toString();
      final title = (map['title'] ?? 'New Session').toString();
      final modelId = extractSessionModelFromMap(map, runtime: runtime);
      final diffStats = extractSessionDiffStats(map);
      final rawEffort = (map['reasoningEffort'] ?? '').toString().trim();
      final rawStatus =
          statusMap[hoxiId] ??
          (map['status'] ?? (map['busy'] == true ? 'busy' : '')).toString();
      final rawBusy = isBusySessionStatusValue(rawStatus);
      if (rawBusy) _startedUntilBySessionId.remove(intId);
      final isBusy =
          (rawBusy || _isRecentlyStartedBusy(intId)) &&
          !_isSuppressedBusy(intId);

      final prev = _sessionsById[intId];
      final projectId = prev?.projectId ??
          (sourceDir.isNotEmpty
              ? HoxiWorkspaceAdapter.stablePositiveIntId(sourceDir)
              : HoxiWorkspaceAdapter.stablePositiveIntId(fallbackProjectKey));
      final resolvedTitle = (prev != null &&
              prev.title.trim().isNotEmpty &&
              (prev.hasLocalTitleOverride ||
                  (!isPlaceholderSessionTitle(prev.title) &&
                      isPlaceholderSessionTitle(title))))
          ? prev.title
          : title;
      final resolvedModelId = (prev != null &&
              prev.modelId.isNotEmpty &&
              (prev.hasLocalModelOverride || modelId.isEmpty))
          ? prev.modelId
          : modelId;
      final resolvedRuntime = runtime.isNotEmpty
          ? runtime
          : (prev?.runtime ?? _catalogAdapter.defaultCoreRuntime);
      final approvalPolicy = prev?.approvalPolicy ?? 'safe_auto';
      final hasPendingApproval = (pendingApprovalCounts[hoxiId] ?? 0) > 0;
      final stage = hasPendingApproval
          ? 'awaiting_approval'
          : isBusy
          ? 'running'
          : (!hasAuthoritativePending &&
                    _knownStageBySessionId[intId] == 'awaiting_approval'
                ? 'awaiting_approval'
                : 'idle');
      _knownStageBySessionId[intId] = stage;
      final hasDiff = diffStats.additions > 0 || diffStats.deletions > 0;

      final nextRef = _HoxiSessionRef(
        hoxiId: hoxiId,
        parentHoxiId: entry.parentHoxiId,
        directory: workDir,
        projectDir: sourceDir.isNotEmpty ? sourceDir : workDir,
        projectId: projectId,
        runtime: resolvedRuntime,
        title: resolvedTitle,
        sandboxMode: sandboxMode,
        branch: branch,
        modelId: resolvedModelId,
        reasoningEffort: rawEffort.isNotEmpty
            ? rawEffort
            : (prev?.reasoningEffort ?? ''),
        approvalPolicy: approvalPolicy,
        diffAdditions: hasDiff
            ? diffStats.additions
            : (prev?.diffAdditions ?? 0),
        diffDeletions: hasDiff
            ? diffStats.deletions
            : (prev?.diffDeletions ?? 0),
        hasLocalTitleOverride: prev?.hasLocalTitleOverride ?? false,
        hasLocalModelOverride: prev?.hasLocalModelOverride ?? false,
        hasLocalEffortOverride: prev?.hasLocalEffortOverride ?? false,
        createdAt: entry.createdAt,
        updatedAt: entry.updatedAt ?? prev?.updatedAt,
      );
      _sessionsById[intId] = nextRef;
      builtByHoxiId[hoxiId] = _toDevSession(
        intId,
        nextRef,
        stage: stage,
        approvalPolicy: approvalPolicy,
      );
    }

    final childrenByRootHoxiId = <String, List<AiDevSession>>{};
    for (final entry in rawItems) {
      if (entry.parentHoxiId.isEmpty) continue;
      final rootHoxi = HoxiConvoyAdapter.resolveRootHoxiId(
        entry.hoxiId,
        parentByHoxiId,
      );
      if (rootHoxi.isNotEmpty &&
          rootHoxi != entry.hoxiId &&
          builtByHoxiId.containsKey(rootHoxi)) {
        final childDev = builtByHoxiId[entry.hoxiId];
        if (childDev != null) {
          childrenByRootHoxiId
              .putIfAbsent(rootHoxi, () => <AiDevSession>[])
              .add(childDev);
        }
      }
    }

    final sessions = <AiDevSession>[];
    for (final entry in rawItems) {
      if (entry.parentHoxiId.isNotEmpty &&
          builtByHoxiId.containsKey(
            HoxiConvoyAdapter.resolveRootHoxiId(entry.hoxiId, parentByHoxiId),
          )) {
        continue;
      }
      final base = builtByHoxiId[entry.hoxiId];
      if (base == null) continue;
      final children =
          childrenByRootHoxiId[entry.hoxiId] ?? const <AiDevSession>[];
      if (children.length > 1) {
        children.sort((a, b) {
          final aTime =
              a.createdAt ??
              a.updatedAt ??
              DateTime.fromMillisecondsSinceEpoch(0);
          final bTime =
              b.createdAt ??
              b.updatedAt ??
              DateTime.fromMillisecondsSinceEpoch(0);
          return aTime.compareTo(bTime);
        });
      }
      sessions.add(
        children.isEmpty
            ? base
            : base.copyWith(subagents: List<AiDevSession>.unmodifiable(children)),
      );
    }

    sessions.sort((a, b) {
      final aBusy =
          a.status == 'running' ||
          a.currentStage == 'awaiting_approval' ||
          a.runningSubagentsCount > 0 ||
          a.subagents.any((sub) => sub.currentStage == 'awaiting_approval');
      final bBusy =
          b.status == 'running' ||
          b.currentStage == 'awaiting_approval' ||
          b.runningSubagentsCount > 0 ||
          b.subagents.any((sub) => sub.currentStage == 'awaiting_approval');
      if (aBusy != bBusy) return aBusy ? -1 : 1;
      final aTime =
          a.updatedAt ?? a.createdAt ?? DateTime.fromMillisecondsSinceEpoch(0);
      final bTime =
          b.updatedAt ?? b.createdAt ?? DateTime.fromMillisecondsSinceEpoch(0);
      return bTime.compareTo(aTime);
    });

    return List<AiDevSession>.unmodifiable(sessions);
  }

  /// Registers a single externally created Hoxi session (e.g. Work App session)
  /// into this adapter so subsequent selection, streaming, and prompting target
  /// its exact Hoxi ID and workspace directory.
  AiDevSession? registerSingleSession(
    Map<dynamic, dynamic> raw, {
    required String fallbackProjectKey,
  }) {
    final sessions = registerSessionList(
      <String, dynamic>{
        'items': <dynamic>[raw],
        'statuses': const <String, dynamic>{},
      },
      fallbackProjectKey: fallbackProjectKey,
    );
    return sessions.firstOrNull;
  }
}
