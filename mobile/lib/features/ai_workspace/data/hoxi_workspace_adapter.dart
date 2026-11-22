import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';

import '../../../core/network/api_client.dart';
import '../models/ai_dev_session.dart';
import '../models/ai_session_state_info.dart';
import '../models/chat_message.dart';
import '../models/code_convoy_plan.dart';
import '../models/code_conversation_stream.dart';
import '../models/code_delivery_job.dart';
import '../models/code_execution_run_detail.dart';
import '../models/code_git_review.dart';
import '../models/code_memory_entry.dart';
import '../models/code_model_catalog.dart';
import '../models/code_project_terminal_session.dart';
import '../models/code_quality_check.dart';
import '../models/code_session_flow.dart';
import '../models/code_session_recovery.dart';
import '../models/code_task.dart';
import '../models/code_workspace_file.dart';
import '../../../core/models/node_skill.dart';
import 'task_ai_utils.dart';
import 'hoxi_catalog_approval_adapter.dart';
import 'hoxi_convoy_adapter.dart';
import 'hoxi_git_artifact_adapter.dart';

part 'hoxi_session_turn_adapter.dart';
part 'hoxi_session_list_adapter.dart';

/// Hoxi 节点协议适配器
/// 将 Hoxi Gateway (/api/hoxi/* 及 OpenCode 接口) 转换为移动端现有的领域模型
class HoxiWorkspaceAdapter {
  static const Duration defaultRecentSessionWindow = defaultRecentTaskWindow;

  final ApiClient _apiClient;
  late final HoxiGitArtifactAdapter _gitAdapter;
  late final HoxiCatalogApprovalAdapter _catalogAdapter;

  final Map<int, CodeProject> _projectsById = {};
  final Map<int, _HoxiSessionRef> _sessionsById = {};
  final Map<int, HoxiApprovalRef> _approvalsById = {};
  final Map<int, CodeExecutionRunDetail> _runDetailsById = {};
  final Map<int, ({int sessionId, String prompt})> _instructionsById = {};
  final Map<int, CodeDeliveryJob> _deliveriesBySessionId = {};
  final Map<int, HoxiSessionDiffScope> _diffScopesBySessionId = {};
  final Map<int, HoxiSessionDiffScope> _latestTurnDiffScopesBySessionId = {};
  final Map<(int?, bool), Future<List<AiDevSession>>> _inflightSessions = {};
  final Map<int, Future<_HoxiParsedSessionTurns>> _inflightParsedTurns = {};
  final Map<String, Future<Map<String, String>>> _inflightSessionStatuses = {};
  final Map<int, DateTime> _stoppedUntilBySessionId = {};
  final Map<int, DateTime> _startedUntilBySessionId = {};
  final Map<int, String> _knownStageBySessionId = {};

  HoxiWorkspaceAdapter(this._apiClient) {
    _gitAdapter = HoxiGitArtifactAdapter(_apiClient);
    _catalogAdapter = HoxiCatalogApprovalAdapter(_apiClient, stablePositiveIntId);
  }

  bool _isSuppressedBusy(int sessionId) {
    final until = _stoppedUntilBySessionId[sessionId];
    if (until == null) return false;
    if (DateTime.now().isBefore(until)) return true;
    _stoppedUntilBySessionId.remove(sessionId);
    return false;
  }

  bool _isRecentlyStartedBusy(int sessionId) {
    final until = _startedUntilBySessionId[sessionId];
    if (until == null) return false;
    if (DateTime.now().isBefore(until)) return true;
    _startedUntilBySessionId.remove(sessionId);
    return false;
  }

  void markSessionStage(int sessionId, String stage) {
    if (sessionId <= 0) return;
    final normalized = stage.trim();
    if (normalized.isEmpty) return;
    _knownStageBySessionId[sessionId] = normalized;
    if (isBusySessionStatusValue(normalized)) {
      _startedUntilBySessionId[sessionId] = DateTime.now().add(const Duration(seconds: 15));
    } else if (normalized == 'idle' ||
        normalized == 'completed' ||
        normalized == 'failed' ||
        normalized == 'awaiting_approval') {
      _startedUntilBySessionId.remove(sessionId);
    }
  }

  static int stablePositiveIntId(String raw) {
    var hash = 0x811c9dc5;
    for (final unit in utf8.encode(raw)) {
      hash ^= unit;
      hash = (hash * 0x01000193) & 0x7fffffff;
    }
    return hash == 0 ? 1 : hash;
  }

  Future<List<CodeProject>> getProjects() => _catalogAdapter.getProjects(_projectsById);

  Future<List<CodeExecutor>> getExecutors() => _catalogAdapter.getExecutors();

  Future<CodeWorktreeCapability> getWorktreeCapability(int projectId) async {
    final dir = _projectsById[projectId]?.workDir ?? '';
    return CodeWorktreeCapability(
      available: true, reason: '', sourceDirs: dir.isEmpty ? const [] : [dir],
      repositoryCount: 1, dirtyRepositories: const [], snapshotSupported: true,
    );
  }

  Future<CodeProjectTerminalSession> openProjectTerminal(int projectId) async {
    final dir = _projectsById[projectId]?.workDir ?? '';
    return CodeProjectTerminalSession(
      id: projectId, status: 'running', shell: '/bin/zsh', workDir: dir, errorMessage: '',
    );
  }

  Future<List<AiDevSession>> getSessions({int? projectId}) => _loadSessions(projectId: projectId);

  Future<List<AiDevSession>> getRecentActiveSessions({
    int? projectId,
    Duration window = defaultRecentSessionWindow,
  }) => runtimeType != HoxiWorkspaceAdapter
      ? getSessions(projectId: projectId)
      : _loadSessions(projectId: projectId, recentOnly: true, recentWindow: window);

  Future<List<AiDevSession>> _loadSessions({
    int? projectId,
    bool recentOnly = false,
    Duration recentWindow = defaultRecentSessionWindow,
  }) {
    final key = (projectId, recentOnly);
    final existing = _inflightSessions[key];
    if (existing != null) return existing;
    final future = _fetchSessionsInternal(
      projectId: projectId, recentOnly: recentOnly, recentWindow: recentWindow,
    );
    _inflightSessions[key] = future;
    return future.whenComplete(() {
      if (identical(_inflightSessions[key], future)) _inflightSessions.remove(key);
    });
  }

  Future<List<CodeTask>> getTasks({int? projectId}) async =>
      _sessionsToTasks(await getSessions(projectId: projectId));

  Future<List<CodeTask>> getRecentActiveTasks({
    int? projectId,
    Duration window = defaultRecentSessionWindow,
  }) async => runtimeType != HoxiWorkspaceAdapter
      ? getTasks(projectId: projectId)
      : _sessionsToTasks(await getRecentActiveSessions(projectId: projectId, window: window));

  List<CodeTask> _sessionsToTasks(List<AiDevSession> sessions) => sessions
      .map(
        (s) => CodeTask(
          id: s.id, projectId: s.projectId, sessionId: s.id, title: s.title,
          agentName: s.agentName, providerModel: s.providerModel,
          diffAdditions: s.diffAdditions, diffDeletions: s.diffDeletions,
          workDir: s.workDir,
          status: s.status == 'running' ? 'running' : 'completed',
          createdAt: s.createdAt, updatedAt: s.updatedAt,
        ),
      )
      .toList();

  Future<AiDevSession> createSession({
    required int projectId,
    required String executorId,
    required String approvalPolicy,
    String title = '',
    bool isolated = true,
    String model = '',
    String? clientRequestId,
  }) async {
    if (_projectsById.isEmpty) await getProjects();
    final proj = _projectsById[projectId] ??
        (_projectsById.values.isNotEmpty ? _projectsById.values.first : null);
    final workDir = proj?.workDir ?? '';
    var trimmedModel = model.trim();
    var runtime = executorId.trim().isEmpty ? 'opencode' : executorId.trim();
    final execs = _catalogAdapter.cachedExecutors.isNotEmpty
        ? _catalogAdapter.cachedExecutors
        : await getExecutors().catchError((_) => const <CodeExecutor>[]);
    if (trimmedModel.isEmpty && execs.isNotEmpty) {
      final defModel = CodeModelCatalog.resolvePreferredModelForExecutors(
        execs, executorIdHint: runtime,
      );
      if (defModel != null) {
        trimmedModel = defModel.value;
        runtime = defModel.executorId;
      }
    } else if (trimmedModel.isNotEmpty) {
      runtime = CodeModelCatalog.resolveRuntimeForModel(
        model: trimmedModel, explicitExecutorId: executorId,
        executors: execs, fallbackRuntime: runtime,
      );
      unawaited(
        CodeModelCatalog.saveLastSelection(
          model: trimmedModel,
          executorId: runtime,
        ),
      );
    }
    final reqId = clientRequestId?.trim() ?? '';
    final modelPayload = _buildPromptModelPayload(runtime, trimmedModel);
    final res = await _apiClient.post<Map<String, dynamic>>(
      '/api/hoxi/sessions',
      options: reqId.isEmpty ? null : Options(headers: {'X-Idempotency-Key': reqId}),
      data: {
        'title': title.trim(),
        'directory': workDir,
        'runtime': runtime,
        'sandboxMode': isolated ? 'worktree' : 'local',
        if (modelPayload != null) ...{
          if ((modelPayload['providerID'] ?? '').isNotEmpty) 'providerID': modelPayload['providerID'],
          if ((modelPayload['modelID'] ?? '').isNotEmpty) 'modelID': modelPayload['modelID'],
        },
        if (reqId.isNotEmpty) 'clientRequestId': reqId,
      },
    );
    final data = res.data ?? const <String, dynamic>{};
    final hoxiId = (data['id'] ?? '').toString().trim();
    final intId = stablePositiveIntId(hoxiId.isEmpty ? DateTime.now().toIso8601String() : hoxiId);
    final sessionDir = (data['directory'] ?? data['worktreePath'] ?? workDir).toString();
    final branch = (data['branch'] ?? '').toString();
    final resolvedTitle = (data['title'] ?? title).toString().trim();
    final responseModelId = _HoxiSessionTurnParsing.composeSessionModelId(
      runtime, (data['providerID'] ?? '').toString(), (data['modelID'] ?? '').toString(),
    );
    final resolvedModelId = trimmedModel.isNotEmpty ? trimmedModel : responseModelId;
    final now = DateTime.now();
    final effectiveTitle = resolvedTitle.isEmpty ? 'New Session' : resolvedTitle;

    _sessionsById[intId] = _HoxiSessionRef(
      hoxiId: hoxiId, directory: sessionDir, projectDir: workDir,
      projectId: proj?.id ?? projectId, runtime: runtime, title: effectiveTitle,
      sandboxMode: isolated ? 'worktree' : 'local', branch: branch,
      modelId: resolvedModelId, hasLocalModelOverride: resolvedModelId.isNotEmpty,
      approvalPolicy: approvalPolicy, createdAt: now, updatedAt: now,
    );

    return AiDevSession(
      id: intId, userId: 1, projectId: proj?.id ?? projectId, title: effectiveTitle,
      agentName: runtime, workDir: sessionDir, sourceWorkDir: workDir,
      worktreeBranch: branch, isolationMode: isolated ? 'worktree' : 'local',
      status: 'active', currentStage: 'idle', approvalPolicy: approvalPolicy,
      providerModel: resolvedModelId, lastTaskId: intId, currentTaskTitle: effectiveTitle,
      createdAt: now, updatedAt: now, lastInstructionAt: now,
    );
  }

  Future<AiSessionStateInfo> getSessionState(int sessionId) async {
    var ref = await _resolveSessionRef(sessionId);
    final rootRef = _resolveRootSessionRef(ref);
    final target = ref.toTarget();
    final parallelResults = await Future.wait<dynamic>([
      getApprovals(),
      _fetchSessionStatuses(ref.directory),
      _gitAdapter
          .fetchRawSessionDiffs(target)
          .then<HoxiRawSessionDiffs?>((v) => v)
          .catchError((_) => null),
      HoxiConvoyAdapter.fetchOrchestrationPlans(
        _apiClient,
        sessionHoxiIds: <String>[
          ref.hoxiId,
          if (rootRef.hoxiId != ref.hoxiId) rootRef.hoxiId,
        ],
        directory: ref.projectDir,
        stableId: stablePositiveIntId,
      ),
    ]);
    final approvals = parallelResults[0] as List<AiApproval>;
    final pendingApproval = approvals.where((a) => a.sessionId == sessionId).firstOrNull;
    final statusMap = parallelResults[1] as Map<String, String>;
    final rawBusy = isBusySessionStatusValue(statusMap[ref.hoxiId]);
    if (rawBusy) _startedUntilBySessionId.remove(sessionId);
    final isBusy = (rawBusy || _isRecentlyStartedBusy(sessionId)) && !_isSuppressedBusy(sessionId);
    final parsed = await _loadParsedSessionTurns(sessionId, ref, isBusy: isBusy);
    _storeBoundedParsedRuns(parsed);
    ref = _ensureSessionTitleFromTurns(sessionId, _sessionsById[sessionId] ?? ref, parsed);
    final stage = pendingApproval != null ? 'awaiting_approval' : (isBusy ? 'running' : 'idle');
    _knownStageBySessionId[sessionId] = stage;

    final changedFiles = <String>[];
    final changedFileStats = <CodeGitFile>[];
    var diffAdditions = 0, diffDeletions = 0;
    final rawDiffs = parallelResults[2] as HoxiRawSessionDiffs?;
    final gitStatus = rawDiffs == null
        ? null
        : _gitAdapter.buildGitStatus(target, rawDiffs, scope: _diffScopesBySessionId[sessionId]);
    if (gitStatus != null) {
      diffAdditions = gitStatus.totalAdditions;
      diffDeletions = gitStatus.totalDeletions;
      ref = ref.copyWith(diffAdditions: diffAdditions, diffDeletions: diffDeletions);
      _sessionsById[sessionId] = ref;
      for (final repo in gitStatus.repositories) {
        for (final f in repo.files) {
          changedFiles.add(f.path);
          changedFileStats.add(f);
        }
      }
    }
    final latestTurnScope = _latestTurnDiffScopesBySessionId[sessionId];
    final turnChangedFileStats = latestTurnScope == null
        ? const <CodeGitFile>[]
        : latestTurnScope.filterTurnFiles(
            changedFileStats, projectDir: ref.projectDir, worktreeDir: ref.directory,
          );
    final turnChangedFiles = turnChangedFileStats.map((f) => f.path).toList();
    final turnDiffAdditions = turnChangedFileStats.fold<int>(0, (sum, f) => sum + f.additions);
    final turnDiffDeletions = turnChangedFileStats.fold<int>(0, (sum, f) => sum + f.deletions);

    final fetchedPlans = parallelResults[3] as List<CodeConvoyPlan>;
    final convoyAndSubs = _buildConvoyAndSubagentsForState(
      ref: ref, rootRef: rootRef, parsed: parsed,
      fetchedPlans: fetchedPlans, approvals: approvals, statusMap: statusMap,
    );
    final latestError =
        parsed.runs.isNotEmpty && parsed.runs.first.status == 'failed'
            ? parsed.runs.first.errorMessage
            : '';
    final session = _toDevSession(sessionId, ref, stage: stage, subagents: convoyAndSubs.childSessions);
    return AiSessionStateInfo(
      session: session,
      currentTask: AiTaskSummary(
        id: sessionId, title: ref.title, status: isBusy ? 'running' : 'completed',
        workDir: ref.directory, agentName: ref.runtime,
      ),
      latestInstruction: parsed.latestInstruction,
      currentStage: stage,
      recentOutput: parsed.recentOutput,
      recentMessages: parsed.chatMessages,
      previews: const [],
      timelineEvents: parsed.timeline,
      errorSummary: latestError,
      changedFiles: changedFiles,
      changedFileStats: changedFileStats,
      diffAdditions: diffAdditions,
      diffDeletions: diffDeletions,
      turnChangedFiles: turnChangedFiles,
      turnChangedFileStats: turnChangedFileStats,
      turnDiffAdditions: turnDiffAdditions,
      turnDiffDeletions: turnDiffDeletions,
      todos: parsed.todos,
      convoyPlans: convoyAndSubs.convoyPlans,
      subagents: convoyAndSubs.subagents,
      pendingApproval: pendingApproval,
    );
  }

  Future<ResponseBody> openConversationStream(
    int sessionId, {int? afterSeq, CancelToken? cancelToken}
  ) async {
    final ref = await _resolveSessionRef(sessionId);
    final validSeq = afterSeq != null && afterSeq > 0 ? afterSeq : null;
    return _apiClient.openSseStream(
      '/api/hoxi/event',
      queryParameters: {
        'directory': ref.directory,
        'after_seq': ?validSeq,
      },
      headers: validSeq != null ? {'Last-Event-ID': '$validSeq'} : null,
      cancelToken: cancelToken,
    );
  }

  Future<AiDevSession> updateSessionTitle(int sessionId, String title) async {
    final ref = await _resolveSessionRef(sessionId);
    final trimmed = title.trim();
    try {
      await _apiClient.patch<Map<String, dynamic>>(
        '/api/hoxi/sessions/${Uri.encodeComponent(ref.hoxiId)}',
        queryParameters: {'directory': ref.directory},
        data: {'title': trimmed},
      );
    } catch (_) {}
    final updated = ref.copyWith(
      title: trimmed, hasLocalTitleOverride: true, updatedAt: DateTime.now(),
    );
    _sessionsById[sessionId] = updated;
    return _toDevSession(sessionId, updated);
  }

  Future<AiDevSession> updateApprovalPolicy(int sessionId, String approvalPolicy) async {
    final ref = await _resolveSessionRef(sessionId);
    final updated = ref.copyWith(approvalPolicy: approvalPolicy, updatedAt: DateTime.now());
    _sessionsById[sessionId] = updated;
    return _toDevSession(sessionId, updated);
  }

  Future<AiDevSession> updateSessionModel(
    int sessionId, {String? model, String? executorId, String? reasoningEffort}
  ) async {
    final ref = await _resolveSessionRef(sessionId);
    final nextModel = model != null ? model.trim() : ref.modelId;
    final hasExecArg = executorId != null && executorId.trim().isNotEmpty;
    final nextRuntime = (model != null || hasExecArg)
        ? CodeModelCatalog.resolveRuntimeForModel(
            model: nextModel, explicitExecutorId: executorId,
            executors: _catalogAdapter.cachedExecutors, fallbackRuntime: ref.runtime,
          )
        : ref.runtime;
    final updated = ref.copyWith(
      runtime: nextRuntime, modelId: nextModel, reasoningEffort: reasoningEffort?.trim(),
      hasLocalModelOverride: (model != null || hasExecArg) ? true : ref.hasLocalModelOverride,
      hasLocalEffortOverride: reasoningEffort != null ? true : ref.hasLocalEffortOverride,
      updatedAt: DateTime.now(),
    );
    _sessionsById[sessionId] = updated;
    if ((model != null || hasExecArg) && nextModel.isNotEmpty) {
      unawaited(
        CodeModelCatalog.saveLastSelection(
          model: nextModel,
          executorId: nextRuntime,
        ),
      );
    }
    if (nextRuntime != ref.runtime || (model != null && nextModel != ref.modelId)) {
      await _syncSessionRuntime(ref.hoxiId, ref.directory, nextRuntime, nextModel);
    }
    return _toDevSession(sessionId, updated);
  }

  Future<CodeSessionInitialization> getSessionInitialization(int sessionId) async {
    return CodeSessionInitialization.fromJson({
      'id': sessionId, 'status': 'ready', 'currentStage': 'idle',
    });
  }

  Future<CodeSessionHistory> getSessionHistory(
    int sessionId, {int page = 1, int limit = 20}
  ) => _loadSessionHistory(sessionId, page: page, limit: limit);

  Future<AiInstruction> retryInstruction(int instructionId) => _retryInstructionById(instructionId);

  Future<CodeConvoyPlan> startConvoyPlan(int sessionId, CodeConvoyPlan draft) async {
    final ref = await _resolveSessionRef(sessionId);
    final rootRef = _resolveRootSessionRef(ref);
    final modelPayload = _buildPromptModelPayload(ref.runtime, ref.modelId);
    final plan = await HoxiConvoyAdapter.createOrchestrationPlan(
      _apiClient,
      directory: rootRef.projectDir.isNotEmpty ? rootRef.projectDir : ref.directory,
      parentHoxiSessionId: rootRef.hoxiId,
      title: draft.title,
      concurrency: draft.concurrency,
      tasks: draft.tasks,
      providerId: modelPayload?['providerID'] ?? '',
      modelId: modelPayload?['modelID'] ?? '',
      stableId: stablePositiveIntId,
    );
    _inflightSessions.clear();
    return plan;
  }

  Future<CodeConvoyPlan> cancelConvoyPlan(int sessionId, String planId) async {
    final plan = await HoxiConvoyAdapter.cancelOrchestrationPlan(
      _apiClient, planId: planId, stableId: stablePositiveIntId,
    );
    _inflightSessions.clear();
    return plan;
  }

  Future<CodeConvoyPlan> retryConvoyPlan(int sessionId, String planId) async {
    final plan = await HoxiConvoyAdapter.retryOrchestrationPlan(
      _apiClient, planId: planId, stableId: stablePositiveIntId,
    );
    _inflightSessions.clear();
    return plan;
  }

  Future<CodeConvoyPlan> mergeConvoyPlan(int sessionId, String planId) async {
    final ref = await _resolveSessionRef(sessionId);
    final rootRef = _resolveRootSessionRef(ref);
    final plan = await HoxiConvoyAdapter.mergeOrchestrationPlan(
      _apiClient, planId: planId, parentHoxiSessionId: rootRef.hoxiId, stableId: stablePositiveIntId,
    );
    _inflightSessions.clear();
    return plan;
  }

  static Map<String, String>? _buildPromptModelPayload(String runtime, String rawModelId) =>
      _HoxiSessionTurnParsing.buildPromptModelPayload(runtime, rawModelId);

  Future<Map<String, dynamic>> uploadAttachment({
    required int sessionId, required String filename,
    required String mime, required String dataUrl,
  }) async {
    final ref = await _resolveSessionRef(sessionId);
    return _catalogAdapter.uploadAttachment(
      sessionId: ref.hoxiId, directory: ref.directory,
      filename: filename, mime: mime, dataUrl: dataUrl,
    );
  }

  static int _requestSeq = 0;

  static String _nextClientRequestId(String prefix, int sessionId) {
    _requestSeq = (_requestSeq + 1) & 0x7fffffff;
    final micros = DateTime.now().microsecondsSinceEpoch;
    return '$prefix-$sessionId-$micros-$_requestSeq';
  }

  Future<AiInstructionSendResult> sendAiCommand({
    required int sessionId,
    required String command,
    List<Map<String, dynamic>>? attachments,
    NodeSkillSelection? skillSelection,
    String? clientRequestId,
  }) async {
    _stoppedUntilBySessionId.remove(sessionId);
    var ref = await _resolveSessionRef(sessionId);
    if (ref.runtime == 'opencode' && ref.modelId.trim().isEmpty) {
      final execs = _catalogAdapter.cachedExecutors.isNotEmpty
          ? _catalogAdapter.cachedExecutors
          : await getExecutors().catchError((_) => const <CodeExecutor>[]);
      final defModel = CodeModelCatalog.defaultModelForExecutors(execs);
      if (defModel != null) {
        await _syncSessionRuntime(ref.hoxiId, ref.directory, defModel.executorId, defModel.value);
        ref = ref.copyWith(
          runtime: defModel.executorId, modelId: defModel.value,
          hasLocalModelOverride: true, updatedAt: DateTime.now(),
        );
        _sessionsById[sessionId] = ref;
      }
    } else if (ref.modelId.trim().isNotEmpty) {
      final targetRuntime = CodeModelCatalog.resolveRuntimeForModel(
        model: ref.modelId, explicitExecutorId: ref.runtime,
        executors: _catalogAdapter.cachedExecutors, fallbackRuntime: ref.runtime,
      );
      if (targetRuntime != ref.runtime) {
        await _syncSessionRuntime(ref.hoxiId, ref.directory, targetRuntime, ref.modelId);
      }
      ref = ref.copyWith(
        runtime: targetRuntime, hasLocalModelOverride: true, updatedAt: DateTime.now(),
      );
      _sessionsById[sessionId] = ref;
    }
    final reqId = (clientRequestId != null && clientRequestId.trim().isNotEmpty)
        ? clientRequestId.trim()
        : _nextClientRequestId('prompt', sessionId);
    final modelPayload = _buildPromptModelPayload(ref.runtime, ref.modelId);
    final effort = ref.reasoningEffort.trim();
    final parts = <Map<String, dynamic>>[];
    if (command.trim().isNotEmpty || (attachments == null || attachments.isEmpty)) {
      parts.add({'type': 'text', 'text': command});
    }
    if (attachments != null) {
      for (final att in attachments) {
        final attId = att['attachmentId'] ?? att['id'];
        parts.add({
          'type': 'file',
          'attachmentId': ?attId,
          if (att['mime'] != null) 'mime': att['mime'],
          if (att['filename'] != null) 'filename': att['filename'],
          if (att['url'] != null) 'url': att['url'],
          if (att['size'] != null) 'size': att['size'],
          if (att['kind'] != null) 'kind': att['kind'],
        });
      }
    }
    final autoTitle = isPlaceholderSessionTitle(ref.title)
        ? extractSessionTitleFromPrompt(
            command, hasImage: attachments != null && attachments.isNotEmpty,
          )
        : '';
    if (autoTitle.isNotEmpty && !isPlaceholderSessionTitle(autoTitle)) {
      ref = ref.copyWith(title: autoTitle, updatedAt: DateTime.now());
      _sessionsById[sessionId] = ref;
      _patchSessionTitleQuietly(ref, autoTitle);
    }
    final res = await _apiClient.post<Map<String, dynamic>>(
      '/api/hoxi/sessions/${ref.hoxiId}/prompt_async',
      queryParameters: {'directory': ref.directory},
      options: Options(headers: {'X-Idempotency-Key': reqId}),
      data: {
        'text': command,
        'parts': parts,
        'clientRequestId': reqId,
        'runtime': ref.runtime,
        'model': ?modelPayload,
        if (ref.runtime == 'qoder' || ref.runtime == 'codebuddy') 'agent': 'plan',
        if (effort.isNotEmpty) 'variant': effort,
        if (skillSelection != null) 'skillSelection': skillSelection.toJson(),
      },
    );
    final rawInstructionId = (res.data?['instructionId'] as num?)?.toInt() ?? 0;
    final instructionId = rawInstructionId > 0 ? rawInstructionId : 0;
    final now = DateTime.now();
    _startedUntilBySessionId[sessionId] = now.add(const Duration(seconds: 15));
    _knownStageBySessionId[sessionId] = 'running';
    ref = ref.copyWith(updatedAt: now);
    _sessionsById[sessionId] = ref;
    if (instructionId > 0 && command.trim().isNotEmpty) {
      _instructionsById[instructionId] = (sessionId: sessionId, prompt: command.trim());
    }
    return AiInstructionSendResult(
      session: _toDevSession(sessionId, ref, stage: 'running'),
      instruction: AiInstruction(
        id: instructionId, sessionId: sessionId, taskId: sessionId,
        content: command, status: 'running', allowCode: true,
        autoPreview: false, requireApproval: false, analysisOnly: false,
        createdAt: now,
      ),
      task: AiTaskSummary(
        id: sessionId, title: ref.title, status: 'running',
        workDir: ref.directory, agentName: ref.runtime,
      ),
      approval: null,
    );
  }

  Future<void> stopSession(int sessionId, {String? clientRequestId}) async {
    final ref = await _resolveSessionRef(sessionId);
    final reqId = clientRequestId?.trim() ?? '';
    await _apiClient.post<dynamic>(
      '/api/hoxi/sessions/${ref.hoxiId}/abort',
      queryParameters: {'directory': ref.directory},
      options: reqId.isEmpty ? null : Options(headers: {'X-Idempotency-Key': reqId}),
      data: {if (reqId.isNotEmpty) 'clientRequestId': reqId},
    );
    _startedUntilBySessionId.remove(sessionId);
    _knownStageBySessionId[sessionId] = 'idle';
    _stoppedUntilBySessionId[sessionId] = DateTime.now().add(const Duration(seconds: 4));
    _inflightSessionStatuses.clear();
    _inflightParsedTurns.remove(sessionId);
    _inflightSessions.clear();
  }

  Future<List<AiApproval>> getApprovals() => _catalogAdapter.getApprovals(
        approvalsById: _approvalsById,
        resolveSessionDir: (sessionIntId) => _sessionsById[sessionIntId]?.directory ?? '',
      );

  Future<AiApproval> replyApproval(
    int approvalId, {required bool approve, String? reason, String? clientRequestId}
  ) => _catalogAdapter.replyApproval(
        approvalId, approve: approve, reason: reason, clientRequestId: clientRequestId,
        approvalsById: _approvalsById, reloadApprovals: getApprovals,
      );

  Future<CodeGitStatus> getGitStatus(int sessionId) async {
    final ref = await _resolveSessionRef(sessionId);
    final target = ref.toTarget();
    if (!target.isIsolatedWorktree && !_diffScopesBySessionId.containsKey(sessionId)) {
      try {
        await _loadParsedSessionTurns(sessionId, ref, isBusy: false);
      } catch (_) {}
    }
    return _gitAdapter.getGitStatus(target, scope: _diffScopesBySessionId[sessionId]);
  }

  Future<CodeGitDiff> getGitDiff({required int sessionId, required String path}) async {
    final ref = await _resolveSessionRef(sessionId);
    return _gitAdapter.getGitDiff(ref: ref.toTarget(), path: path);
  }

  Future<CodeGitSaveResult> saveGitChanges(
    int sessionId, String message, {String? clientRequestId}
  ) async {
    final ref = await _resolveSessionRef(sessionId);
    final res = await _gitAdapter.saveGitChanges(
      ref.toTarget(), message, clientRequestId: clientRequestId,
    );
    final existing = _deliveriesBySessionId[sessionId];
    if (existing != null) {
      _deliveriesBySessionId[sessionId] = CodeDeliveryJob.fromJson({
        'id': existing.id, 'sessionId': existing.sessionId,
        'status': existing.status, 'stage': existing.stage,
        'progress': existing.progress, 'resultCommit': existing.resultCommit,
        'hasPendingChanges': true, 'hasPendingCommits': true, 'hasUncommittedChanges': false,
      });
    }
    return res;
  }

  Future<CodeStructureResult> getSessionStructure(int sessionId, {String path = ''}) async {
    final ref = await _resolveSessionRef(sessionId);
    return _gitAdapter.getSessionStructure(ref.toTarget(), path: path);
  }

  Future<CodeSessionFile> getSessionFile(int sessionId, String path) async {
    final ref = await _resolveSessionRef(sessionId);
    return _gitAdapter.getSessionFile(ref.toTarget(), path);
  }

  Future<CodeSessionFile> saveSessionFile({
    required int sessionId, required CodeSessionFile file, required String content,
  }) async {
    final ref = await _resolveSessionRef(sessionId);
    return _gitAdapter.saveSessionFile(ref.toTarget(), file: file, content: content);
  }

  Future<CodeDeliveryJob?> getDelivery(int sessionId) async {
    final existing = _deliveriesBySessionId[sessionId];
    if (existing == null) return null;
    try {
      final status = await getGitStatus(sessionId);
      final hasPendingCommits = existing.hasPendingCommits && status.hasChanges;
      final hasUncommitted = status.hasChanges && !hasPendingCommits;
      final updated = CodeDeliveryJob.fromJson({
        'id': existing.id, 'sessionId': existing.sessionId,
        'status': existing.status, 'stage': existing.stage,
        'progress': existing.progress, 'resultCommit': existing.resultCommit,
        'hasPendingChanges': status.hasChanges,
        'hasPendingCommits': hasPendingCommits,
        'hasUncommittedChanges': hasUncommitted,
      });
      _deliveriesBySessionId[sessionId] = updated;
      return updated;
    } catch (_) {
      return existing;
    }
  }

  Future<CodeDeliveryReview> getDeliveryReview(int sessionId) async {
    final status = await getGitStatus(sessionId);
    return CodeDeliveryReview.fromJson({
      'reviewReady': status.hasChanges,
      'reviewRevision': DateTime.now().millisecondsSinceEpoch.toString(),
      'files': status.files,
      'additions': status.totalAdditions,
      'deletions': status.totalDeletions,
    });
  }

  Future<CodeDeliveryJob> startDelivery(int sessionId) async {
    final ref = await _resolveSessionRef(sessionId);
    final job = await _gitAdapter.startDelivery(sessionId, ref.toTarget());
    _deliveriesBySessionId[sessionId] = job;
    return job;
  }

  Future<CodeSessionFlow> getSessionFlow(int sessionId) async => _gitAdapter.getSessionFlow();

  Future<List<CodeQualityCheck>> getQualityChecks(int sessionId) async {
    final ref = await _resolveSessionRef(sessionId);
    return _gitAdapter.getQualityChecks(ref.toTarget());
  }

  Future<CodeQualityCheckResult> runQualityCheck(
    int sessionId, String checkId, {Duration? receiveTimeout}
  ) async {
    final ref = await _resolveSessionRef(sessionId);
    return _gitAdapter.runQualityCheck(ref.toTarget(), checkId, receiveTimeout: receiveTimeout);
  }

  Future<CodeMemoryExtractionStatus> getSessionMemoryStatus(int sessionId) async =>
      _gitAdapter.getSessionMemoryStatus();

  Future<CodeMemoryExtractionStatus> extractSessionMemory(int sessionId) async {
    final ref = await _resolveSessionRef(sessionId);
    return _gitAdapter.extractSessionMemory(ref.toTarget());
  }

  Future<List<CodeMemoryEntry>> getMemories({int? projectId}) async => _gitAdapter.getMemories();

  Future<CodeExecutionRunDetail> getExecutionRun(int runId) => _loadExecutionRun(runId);
}
