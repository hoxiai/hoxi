import 'dart:convert';

import 'package:dio/dio.dart';

import '../../../core/network/api_client.dart';
import '../models/ai_dev_session.dart';
import '../models/ai_session_state_info.dart';
import '../models/chat_message.dart';
import '../models/code_delivery_job.dart';
import '../models/code_execution_run_detail.dart';
import '../models/code_git_review.dart';
import '../models/code_memory_entry.dart';
import '../models/code_project_terminal_session.dart';
import '../models/code_quality_check.dart';
import '../models/code_session_flow.dart';
import '../models/code_session_recovery.dart';
import '../models/code_task.dart';
import '../models/code_workspace_file.dart';
import 'hoxi_git_artifact_adapter.dart';

class _HoxiSessionRef {
  final String hoxiId;
  final String directory;
  final String projectDir;
  final int projectId;
  final String runtime;
  final String title;
  final String sandboxMode;
  final String branch;
  final String modelId;
  final DateTime? createdAt;
  final DateTime? updatedAt;

  const _HoxiSessionRef({
    required this.hoxiId,
    required this.directory,
    required this.projectDir,
    required this.projectId,
    required this.runtime,
    required this.title,
    required this.sandboxMode,
    required this.branch,
    required this.modelId,
    this.createdAt,
    this.updatedAt,
  });

  HoxiSessionTarget toTarget() => HoxiSessionTarget(
        hoxiId: hoxiId,
        directory: directory,
        projectDir: projectDir,
        branch: branch,
      );
}

class _HoxiApprovalRef {
  final String permissionId;
  final String sessionId;
  final String directory;
  final AiApproval approval;

  const _HoxiApprovalRef({
    required this.permissionId,
    required this.sessionId,
    required this.directory,
    required this.approval,
  });
}

/// Hoxi 节点协议适配器
/// 将 Hoxi Gateway (/api/hoxi/* 及 OpenCode 接口) 转换为移动端现有的领域模型
class HoxiWorkspaceAdapter {
  final ApiClient _apiClient;
  late final HoxiGitArtifactAdapter _gitAdapter;

  final Map<int, CodeProject> _projectsById = {};
  final Map<int, _HoxiSessionRef> _sessionsById = {};
  final Map<int, _HoxiApprovalRef> _approvalsById = {};

  HoxiWorkspaceAdapter(this._apiClient) {
    _gitAdapter = HoxiGitArtifactAdapter(_apiClient);
  }

  static int stablePositiveIntId(String raw) {
    var hash = 0x811c9dc5;
    for (final unit in utf8.encode(raw)) {
      hash ^= unit;
      hash = (hash * 0x01000193) & 0x7fffffff;
    }
    return hash == 0 ? 1 : hash;
  }

  Future<List<CodeProject>> getProjects() async {
    final projects = <CodeProject>[];
    try {
      final res = await _apiClient.get<dynamic>('/api/hoxi/workspaces');
      final rawData = res.data;
      final list = rawData is Map
          ? (rawData['workspaces'] as List<dynamic>? ?? const [])
          : (rawData is List ? rawData : const []);
      for (final item in list.whereType<Map>()) {
        final map = item.cast<String, dynamic>();
        final name = (map['name'] ?? '').toString().trim();
        final folders = (map['folders'] as List<dynamic>? ?? const [])
            .whereType<Map>()
            .map((f) => f.cast<String, dynamic>())
            .toList();
        String primaryPath = '';
        final dirs = <String>[];
        for (final f in folders) {
          final p = (f['path'] ?? '').toString().trim();
          if (p.isEmpty) continue;
          dirs.add(p);
          if (f['isPrimary'] == true || primaryPath.isEmpty) {
            primaryPath = p;
          }
        }
        if (primaryPath.isEmpty) {
          primaryPath = (map['path'] ?? map['rootDir'] ?? '').toString().trim();
          if (primaryPath.isNotEmpty) dirs.add(primaryPath);
        }
        if (primaryPath.isEmpty) continue;
        final id = stablePositiveIntId(primaryPath);
        final project = CodeProject(
          id: id,
          name: name.isNotEmpty ? name : _basename(primaryPath),
          description: 'Hoxi 工作区 · $primaryPath',
          workDir: primaryPath,
          sourceDirs: dirs.isEmpty ? [primaryPath] : dirs,
        );
        _projectsById[id] = project;
        projects.add(project);
      }
    } catch (_) {
      // Fallback to bootstrap rootDir below
    }

    if (projects.isEmpty) {
      try {
        final boot = await _apiClient.get<Map<String, dynamic>>(
          '/api/hoxi/bootstrap',
        );
        final rootDir = (boot.data?['rootDir'] ?? '').toString().trim();
        if (rootDir.isNotEmpty) {
          final id = stablePositiveIntId(rootDir);
          final project = CodeProject(
            id: id,
            name: _basename(rootDir),
            description: 'Hoxi 默认工作区 · $rootDir',
            workDir: rootDir,
            sourceDirs: [rootDir],
          );
          _projectsById[id] = project;
          projects.add(project);
        }
      } catch (_) {}
    }
    return projects;
  }

  Future<List<CodeExecutor>> getExecutors() async {
    var openCodeVer = '';
    try {
      final sys = await _apiClient.get<Map<String, dynamic>>(
        '/api/hoxi/system-info',
      );
      openCodeVer = (sys.data?['openCodeVersion'] ?? '').toString();
    } catch (_) {}

    final executors = <CodeExecutor>[
      CodeExecutor(
        id: 'opencode',
        name: 'OpenCode',
        description: 'Hoxi 核心 Agent 引擎',
        available: true,
        version: openCodeVer,
        reason: '',
        nativeTerminal: false,
        capabilities: const [
          'code',
          'automation',
          'structured_turn',
          'conversation_approval',
          'resume',
        ],
        approvalPolicies: const ['safe_auto', 'manual', 'full_auto'],
      ),
    ];

    try {
      final claude = await _apiClient.get<Map<String, dynamic>>(
        '/api/hoxi/runner/claude-code/models',
      );
      if (claude.data?['available'] == true) {
        final models = (claude.data?['models'] as List<dynamic>? ?? const [])
            .whereType<Map>()
            .map(
              (m) => CodeExecutorOption(
                value: (m['id'] ?? '').toString(),
                label: (m['name'] ?? m['id'] ?? '').toString(),
              ),
            )
            .where((o) => o.value.isNotEmpty)
            .toList();
        executors.add(
          CodeExecutor(
            id: 'claude-code',
            name: 'Claude Code',
            description: 'Anthropic Claude Code CLI 引擎',
            available: true,
            version: '',
            reason: '',
            nativeTerminal: false,
            capabilities: const [
              'code',
              'automation',
              'structured_turn',
              'conversation_approval',
              'resume',
            ],
            approvalPolicies: const ['safe_auto', 'manual', 'full_auto'],
            modelOptions: models,
          ),
        );
      }
    } catch (_) {}

    try {
      final agy = await _apiClient.get<Map<String, dynamic>>(
        '/api/hoxi/runner/antigravity/models',
      );
      if (agy.data?['available'] == true) {
        final models = (agy.data?['models'] as List<dynamic>? ?? const [])
            .whereType<Map>()
            .map(
              (m) => CodeExecutorOption(
                value: (m['id'] ?? '').toString(),
                label: (m['name'] ?? m['id'] ?? '').toString(),
              ),
            )
            .where((o) => o.value.isNotEmpty)
            .toList();
        executors.add(
          CodeExecutor(
            id: 'antigravity',
            name: 'Antigravity',
            description: 'Google Antigravity CLI 引擎',
            available: true,
            version: '',
            reason: '',
            nativeTerminal: false,
            capabilities: const [
              'code',
              'automation',
              'structured_turn',
              'conversation_approval',
              'resume',
            ],
            approvalPolicies: const ['safe_auto', 'manual', 'full_auto'],
            modelOptions: models,
          ),
        );
      }
    } catch (_) {}

    return executors;
  }

  Future<CodeWorktreeCapability> getWorktreeCapability(int projectId) async {
    final project = _projectsById[projectId];
    final dir = project?.workDir ?? '';
    return CodeWorktreeCapability(
      available: true,
      reason: '',
      sourceDirs: dir.isEmpty ? const [] : [dir],
      repositoryCount: 1,
      dirtyRepositories: const [],
      snapshotSupported: true,
    );
  }

  Future<CodeProjectTerminalSession> openProjectTerminal(int projectId) async {
    final project = _projectsById[projectId];
    return CodeProjectTerminalSession(
      id: projectId,
      status: 'running',
      shell: '/bin/zsh',
      workDir: project?.workDir ?? '',
      errorMessage: '',
    );
  }

  Future<List<AiDevSession>> getSessions({int? projectId}) async {
    if (_projectsById.isEmpty) {
      await getProjects();
    }
    final targetProjects =
        projectId != null && _projectsById.containsKey(projectId)
            ? [_projectsById[projectId]!]
            : _projectsById.values.toList();

    final sessions = <AiDevSession>[];
    final seenIds = <int>{};

    for (final proj in targetProjects) {
      try {
        final res = await _apiClient.get<dynamic>(
          '/api/hoxi/sessions',
          queryParameters: {'directory': proj.workDir},
        );
        final statusMap = await _fetchSessionStatuses(proj.workDir);
        final list = res.data is List ? (res.data as List) : const [];
        for (final raw in list.whereType<Map>()) {
          final map = raw.cast<String, dynamic>();
          final hoxiId = (map['id'] ?? '').toString().trim();
          if (hoxiId.isEmpty) continue;
          final intId = stablePositiveIntId(hoxiId);
          if (!seenIds.add(intId)) continue;

          final workDir =
              (map['directory'] ?? map['worktreePath'] ?? proj.workDir)
                  .toString()
                  .trim();
          final sourceDir = (map['projectID'] ?? proj.workDir).toString().trim();
          final runtime = (map['runtime'] ?? 'opencode').toString().trim();
          final sandboxMode = (map['sandboxMode'] ?? 'worktree').toString();
          final branch = (map['branch'] ?? '').toString();
          final title = (map['title'] ?? 'New Session').toString();
          final modelId = (map['modelID'] ?? '').toString();
          final timeMap = map['time'] is Map
              ? (map['time'] as Map).cast<String, dynamic>()
              : const <String, dynamic>{};
          final createdMs = (timeMap['created'] as num?)?.toInt() ?? 0;
          final updatedMs = (timeMap['updated'] as num?)?.toInt() ?? createdMs;
          final createdAt = createdMs > 0
              ? DateTime.fromMillisecondsSinceEpoch(createdMs)
              : null;
          final updatedAt = updatedMs > 0
              ? DateTime.fromMillisecondsSinceEpoch(updatedMs)
              : null;

          final isBusy = statusMap[hoxiId] == 'busy';
          _sessionsById[intId] = _HoxiSessionRef(
            hoxiId: hoxiId,
            directory: workDir.isNotEmpty ? workDir : proj.workDir,
            projectDir: sourceDir.isNotEmpty ? sourceDir : proj.workDir,
            projectId: proj.id,
            runtime: runtime.isEmpty ? 'opencode' : runtime,
            title: title,
            sandboxMode: sandboxMode,
            branch: branch,
            modelId: modelId,
            createdAt: createdAt,
            updatedAt: updatedAt,
          );

          sessions.add(
            AiDevSession(
              id: intId,
              userId: 1,
              projectId: proj.id,
              title: title,
              agentName: runtime.isEmpty ? 'opencode' : runtime,
              workDir: workDir.isNotEmpty ? workDir : proj.workDir,
              sourceWorkDir: sourceDir.isNotEmpty ? sourceDir : proj.workDir,
              worktreeBranch: branch,
              isolationMode: sandboxMode,
              status: isBusy ? 'running' : 'active',
              currentStage: isBusy ? 'running' : 'idle',
              approvalPolicy: 'safe_auto',
              providerModel: modelId,
              lastTaskId: intId,
              currentTaskTitle: title,
              createdAt: createdAt,
              updatedAt: updatedAt,
              lastInstructionAt: updatedAt,
            ),
          );
        }
      } catch (_) {}
    }
    return sessions;
  }

  Future<List<CodeTask>> getTasks({int? projectId}) async {
    final sessions = await getSessions(projectId: projectId);
    return sessions
        .map(
          (s) => CodeTask(
            id: s.id,
            projectId: s.projectId,
            sessionId: s.id,
            title: s.title,
            agentName: s.agentName,
            workDir: s.workDir,
            status: s.status == 'running' ? 'running' : 'completed',
            createdAt: s.createdAt,
            updatedAt: s.updatedAt,
          ),
        )
        .toList();
  }

  Future<AiDevSession> createSession({
    required int projectId,
    required String executorId,
    required String approvalPolicy,
    String title = '',
    bool isolated = true,
  }) async {
    if (_projectsById.isEmpty) {
      await getProjects();
    }
    final proj = _projectsById[projectId] ??
        (_projectsById.values.isNotEmpty ? _projectsById.values.first : null);
    final workDir = proj?.workDir ?? '';
    final runtime = executorId.trim().isEmpty ? 'opencode' : executorId.trim();
    final res = await _apiClient.post<Map<String, dynamic>>(
      '/api/hoxi/sessions',
      data: {
        'title': title.trim(),
        'directory': workDir,
        'runtime': runtime,
        'sandboxMode': isolated ? 'worktree' : 'local',
      },
    );
    final data = res.data ?? const <String, dynamic>{};
    final hoxiId = (data['id'] ?? '').toString().trim();
    final intId = stablePositiveIntId(
      hoxiId.isEmpty ? DateTime.now().toIso8601String() : hoxiId,
    );
    final sessionDir = (data['directory'] ?? data['worktreePath'] ?? workDir)
        .toString();
    final branch = (data['branch'] ?? '').toString();
    final resolvedTitle = (data['title'] ?? title).toString().trim();
    final now = DateTime.now();

    _sessionsById[intId] = _HoxiSessionRef(
      hoxiId: hoxiId,
      directory: sessionDir,
      projectDir: workDir,
      projectId: proj?.id ?? projectId,
      runtime: runtime,
      title: resolvedTitle.isEmpty ? 'New Session' : resolvedTitle,
      sandboxMode: isolated ? 'worktree' : 'local',
      branch: branch,
      modelId: '',
      createdAt: now,
      updatedAt: now,
    );

    return AiDevSession(
      id: intId,
      userId: 1,
      projectId: proj?.id ?? projectId,
      title: resolvedTitle.isEmpty ? 'New Session' : resolvedTitle,
      agentName: runtime,
      workDir: sessionDir,
      sourceWorkDir: workDir,
      worktreeBranch: branch,
      isolationMode: isolated ? 'worktree' : 'local',
      status: 'active',
      currentStage: 'idle',
      approvalPolicy: approvalPolicy,
      lastTaskId: intId,
      currentTaskTitle: resolvedTitle.isEmpty ? 'New Session' : resolvedTitle,
      createdAt: now,
      updatedAt: now,
      lastInstructionAt: now,
    );
  }

  Future<AiSessionStateInfo> getSessionState(int sessionId) async {
    final ref = await _resolveSessionRef(sessionId);
    final messages = <ChatMessage>[];
    final timeline = <AiTimelineEvent>[];
    var recentOutput = '';

    try {
      final res = await _apiClient.get<dynamic>(
        '/api/hoxi/sessions/${ref.hoxiId}/message',
        queryParameters: {'directory': ref.directory},
      );
      final turns = res.data is List ? (res.data as List) : const [];
      var eventIdx = 1;
      for (final rawTurn in turns.whereType<Map>()) {
        final turn = rawTurn.cast<String, dynamic>();
        final info = turn['info'] is Map
            ? (turn['info'] as Map).cast<String, dynamic>()
            : turn;
        final role = (info['role'] ?? 'assistant').toString();
        final timeMap = info['time'] is Map
            ? (info['time'] as Map).cast<String, dynamic>()
            : const <String, dynamic>{};
        final createdMs = (timeMap['created'] as num?)?.toInt() ?? 0;
        final createdAt = createdMs > 0
            ? DateTime.fromMillisecondsSinceEpoch(createdMs)
            : DateTime.now();

        final parts = (turn['parts'] as List<dynamic>? ?? const [])
            .whereType<Map>()
            .map((p) => p.cast<String, dynamic>())
            .toList();
        final textBuffer = StringBuffer();
        for (final part in parts) {
          final pType = (part['type'] ?? '').toString();
          if (pType == 'text') {
            final text = (part['text'] ?? '').toString();
            if (text.trim().isNotEmpty) {
              if (textBuffer.isNotEmpty) textBuffer.writeln();
              textBuffer.write(text);
            }
          } else if (pType == 'tool') {
            final toolName = (part['tool'] ?? 'tool').toString();
            final stateMap = part['state'] is Map
                ? (part['state'] as Map).cast<String, dynamic>()
                : const <String, dynamic>{};
            final status = (stateMap['status'] ?? 'completed').toString();
            final title = (stateMap['title'] ?? toolName).toString();
            timeline.add(
              AiTimelineEvent(
                id: eventIdx++,
                sessionId: sessionId,
                taskId: sessionId,
                instructionId: 0,
                eventType: 'tool',
                stage: status == 'running' ? 'executing' : 'completed',
                title: title,
                content: toolName,
                status: status == 'error' ? 'error' : 'info',
                meta: '',
                createdAt: createdAt,
                updatedAt: createdAt,
              ),
            );
          }
        }
        final fullText = textBuffer.toString().trim();
        if (fullText.isNotEmpty) {
          messages.add(
            ChatMessage(
              id: (info['id'] ?? '${messages.length + 1}').toString(),
              text: fullText,
              isUser: role == 'user',
              timestamp: createdAt,
            ),
          );
          if (role != 'user') {
            recentOutput = fullText;
          }
        }
      }
    } catch (_) {}

    final approvals = await getApprovals();
    final pendingApproval = approvals
        .where((a) => a.sessionId == sessionId)
        .firstOrNull;
    final statusMap = await _fetchSessionStatuses(ref.directory);
    final isBusy = statusMap[ref.hoxiId] == 'busy';
    final stage = pendingApproval != null
        ? 'awaiting_approval'
        : (isBusy ? 'running' : 'idle');

    final changedFiles = <String>[];
    try {
      final gitStatus = await getGitStatus(sessionId);
      for (final repo in gitStatus.repositories) {
        for (final f in repo.files) {
          changedFiles.add(f.path);
        }
      }
    } catch (_) {}

    final session = _toDevSession(sessionId, ref, stage: stage);
    return AiSessionStateInfo(
      session: session,
      currentTask: AiTaskSummary(
        id: sessionId,
        title: ref.title,
        status: isBusy ? 'running' : 'completed',
        workDir: ref.directory,
        agentName: ref.runtime,
      ),
      latestInstruction: null,
      currentStage: stage,
      recentOutput: recentOutput,
      recentMessages: messages,
      previews: const [],
      timelineEvents: timeline,
      errorSummary: '',
      changedFiles: changedFiles,
      pendingApproval: pendingApproval,
    );
  }

  Future<ResponseBody> openConversationStream(
    int sessionId, {
    CancelToken? cancelToken,
  }) async {
    final ref = await _resolveSessionRef(sessionId);
    return _apiClient.openSseStream(
      '/api/hoxi/event',
      queryParameters: {'directory': ref.directory},
      cancelToken: cancelToken,
    );
  }

  Future<AiDevSession> updateSessionTitle(int sessionId, String title) async {
    final ref = await _resolveSessionRef(sessionId);
    await _apiClient.patch<Map<String, dynamic>>(
      '/api/hoxi/sessions/${ref.hoxiId}',
      queryParameters: {'directory': ref.directory},
      data: {'title': title.trim()},
    );
    final updated = _HoxiSessionRef(
      hoxiId: ref.hoxiId,
      directory: ref.directory,
      projectDir: ref.projectDir,
      projectId: ref.projectId,
      runtime: ref.runtime,
      title: title.trim(),
      sandboxMode: ref.sandboxMode,
      branch: ref.branch,
      modelId: ref.modelId,
      createdAt: ref.createdAt,
      updatedAt: DateTime.now(),
    );
    _sessionsById[sessionId] = updated;
    return _toDevSession(sessionId, updated);
  }

  Future<AiDevSession> updateApprovalPolicy(
    int sessionId,
    String approvalPolicy,
  ) async {
    final ref = await _resolveSessionRef(sessionId);
    return _toDevSession(sessionId, ref, approvalPolicy: approvalPolicy);
  }

  Future<AiDevSession> updateSessionModel(
    int sessionId, {
    String? model,
    String? reasoningEffort,
  }) async {
    final ref = await _resolveSessionRef(sessionId);
    final updated = _HoxiSessionRef(
      hoxiId: ref.hoxiId,
      directory: ref.directory,
      projectDir: ref.projectDir,
      projectId: ref.projectId,
      runtime: ref.runtime,
      title: ref.title,
      sandboxMode: ref.sandboxMode,
      branch: ref.branch,
      modelId: model?.trim() ?? ref.modelId,
      createdAt: ref.createdAt,
      updatedAt: DateTime.now(),
    );
    _sessionsById[sessionId] = updated;
    return _toDevSession(sessionId, updated);
  }

  Future<CodeSessionInitialization> getSessionInitialization(
    int sessionId,
  ) async {
    return CodeSessionInitialization.fromJson(const {'status': 'ready'});
  }

  Future<CodeSessionHistory> getSessionHistory(
    int sessionId, {
    int page = 1,
    int limit = 20,
  }) async {
    return CodeSessionHistory.fromJson(const {
      'instructions': [],
      'runs': [],
      'instructionTotal': 0,
      'runTotal': 0,
    });
  }

  Future<AiInstructionSendResult> sendAiCommand({
    required int sessionId,
    required String command,
  }) async {
    final ref = await _resolveSessionRef(sessionId);
    await _apiClient.post<Map<String, dynamic>>(
      '/api/hoxi/sessions/${ref.hoxiId}/prompt_async',
      queryParameters: {'directory': ref.directory},
      data: {
        'text': command,
        'parts': [
          {'type': 'text', 'text': command},
        ],
      },
    );
    final now = DateTime.now();
    return AiInstructionSendResult(
      session: _toDevSession(sessionId, ref, stage: 'running'),
      instruction: AiInstruction(
        id: now.millisecondsSinceEpoch,
        sessionId: sessionId,
        taskId: sessionId,
        content: command,
        status: 'running',
        allowCode: true,
        autoPreview: false,
        requireApproval: false,
        analysisOnly: false,
        createdAt: now,
      ),
      task: AiTaskSummary(
        id: sessionId,
        title: ref.title,
        status: 'running',
        workDir: ref.directory,
        agentName: ref.runtime,
      ),
      approval: null,
    );
  }

  Future<void> stopSession(int sessionId) async {
    final ref = await _resolveSessionRef(sessionId);
    await _apiClient.post<dynamic>(
      '/api/hoxi/sessions/${ref.hoxiId}/abort',
      queryParameters: {'directory': ref.directory},
      data: const <String, dynamic>{},
    );
  }

  Future<List<AiApproval>> getApprovals() async {
    final results = <AiApproval>[];
    try {
      final res = await _apiClient.get<dynamic>('/api/hoxi/permission');
      final list = res.data is List ? (res.data as List) : const [];
      for (final raw in list.whereType<Map>()) {
        final map = raw.cast<String, dynamic>();
        final permId = (map['id'] ?? '').toString().trim();
        if (permId.isEmpty) continue;
        final hoxiSessionId =
            (map['sessionID'] ?? map['sessionId'] ?? '').toString().trim();
        final sessionIntId = hoxiSessionId.isNotEmpty
            ? stablePositiveIntId(hoxiSessionId)
            : 0;
        final approvalIntId = stablePositiveIntId(permId);
        final title = (map['title'] ?? map['permission'] ?? '高风险操作待确认')
            .toString();
        final metadata = map['metadata'] is Map
            ? jsonEncode(map['metadata'])
            : (map['pattern'] ?? map['command'] ?? title).toString();
        final approval = AiApproval(
          id: approvalIntId,
          sessionId: sessionIntId,
          taskId: sessionIntId,
          instructionId: 0,
          requestUserId: 1,
          approveUserId: 0,
          title: title,
          content: metadata,
          riskLevel: 'high',
          status: 'pending',
          decision: '',
          decisionReason: '',
          createdAt: DateTime.now(),
          updatedAt: DateTime.now(),
          decisionAt: null,
        );
        final sessionDir = _sessionsById[sessionIntId]?.directory ?? '';
        _approvalsById[approvalIntId] = _HoxiApprovalRef(
          permissionId: permId,
          sessionId: hoxiSessionId,
          directory: sessionDir,
          approval: approval,
        );
        results.add(approval);
      }
    } catch (_) {}
    return results;
  }

  Future<AiApproval> replyApproval(
    int approvalId, {
    required bool approve,
    String? reason,
  }) async {
    var ref = _approvalsById[approvalId];
    if (ref == null) {
      await getApprovals();
      ref = _approvalsById[approvalId];
    }
    if (ref == null) {
      throw Exception('未找到对应的 Hoxi 审批项');
    }
    await _apiClient.post<dynamic>(
      '/api/hoxi/permission/${ref.permissionId}/reply',
      queryParameters: {
        if (ref.directory.isNotEmpty) 'directory': ref.directory,
      },
      data: {
        'reply': approve ? 'once' : 'reject',
        if (reason != null && reason.isNotEmpty) 'message': reason,
      },
    );
    _approvalsById.remove(approvalId);
    return AiApproval(
      id: ref.approval.id,
      sessionId: ref.approval.sessionId,
      taskId: ref.approval.taskId,
      instructionId: ref.approval.instructionId,
      requestUserId: ref.approval.requestUserId,
      approveUserId: 1,
      title: ref.approval.title,
      content: ref.approval.content,
      riskLevel: ref.approval.riskLevel,
      status: approve ? 'approved' : 'rejected',
      decision: approve ? 'approve' : 'reject',
      decisionReason: reason ?? '',
      createdAt: ref.approval.createdAt,
      updatedAt: DateTime.now(),
      decisionAt: DateTime.now(),
    );
  }

  Future<CodeGitStatus> getGitStatus(int sessionId) async {
    final ref = await _resolveSessionRef(sessionId);
    return _gitAdapter.getGitStatus(ref.toTarget());
  }

  Future<CodeGitDiff> getGitDiff({
    required int sessionId,
    required String path,
  }) async {
    final ref = await _resolveSessionRef(sessionId);
    return _gitAdapter.getGitDiff(ref: ref.toTarget(), path: path);
  }

  Future<CodeGitSaveResult> saveGitChanges(
    int sessionId,
    String message,
  ) async {
    final ref = await _resolveSessionRef(sessionId);
    return _gitAdapter.saveGitChanges(ref.toTarget(), message);
  }

  Future<CodeStructureResult> getSessionStructure(
    int sessionId, {
    String path = '',
  }) async {
    final ref = await _resolveSessionRef(sessionId);
    return _gitAdapter.getSessionStructure(ref.toTarget(), path: path);
  }

  Future<CodeSessionFile> getSessionFile(int sessionId, String path) async {
    final ref = await _resolveSessionRef(sessionId);
    return _gitAdapter.getSessionFile(ref.toTarget(), path);
  }

  Future<CodeDeliveryJob?> getDelivery(int sessionId) async => null;

  Future<CodeDeliveryReview> getDeliveryReview(int sessionId) async {
    final ref = await _resolveSessionRef(sessionId);
    return _gitAdapter.getDeliveryReview(ref.toTarget());
  }

  Future<CodeDeliveryJob> startDelivery(int sessionId) async {
    final ref = await _resolveSessionRef(sessionId);
    return _gitAdapter.startDelivery(sessionId, ref.toTarget());
  }

  Future<CodeSessionFlow> getSessionFlow(int sessionId) async =>
      _gitAdapter.getSessionFlow();

  Future<List<CodeQualityCheck>> getQualityChecks(int sessionId) async =>
      _gitAdapter.getQualityChecks();

  Future<CodeMemoryExtractionStatus> getSessionMemoryStatus(
    int sessionId,
  ) async => _gitAdapter.getSessionMemoryStatus();

  Future<List<CodeMemoryEntry>> getMemories({int? projectId}) async =>
      _gitAdapter.getMemories();

  Future<CodeExecutionRunDetail> getExecutionRun(int runId) async =>
      _gitAdapter.getExecutionRun(runId);

  Future<Map<String, String>> _fetchSessionStatuses(String directory) async {
    try {
      final res = await _apiClient.get<Map<String, dynamic>>(
        '/api/hoxi/sessions/status',
        queryParameters: {'directory': directory},
      );
      final out = <String, String>{};
      res.data?.forEach((key, value) {
        if (value is Map) {
          out[key] = (value['type'] ?? 'idle').toString();
        }
      });
      return out;
    } catch (_) {
      return const {};
    }
  }

  Future<_HoxiSessionRef> _resolveSessionRef(int sessionId) async {
    final existing = _sessionsById[sessionId];
    if (existing != null) return existing;
    await getSessions();
    final reloaded = _sessionsById[sessionId];
    if (reloaded != null) return reloaded;
    throw Exception('未找到对应的 Hoxi 会话 ($sessionId)');
  }

  AiDevSession _toDevSession(
    int sessionId,
    _HoxiSessionRef ref, {
    String stage = 'idle',
    String approvalPolicy = 'safe_auto',
  }) {
    return AiDevSession(
      id: sessionId,
      userId: 1,
      projectId: ref.projectId,
      title: ref.title,
      agentName: ref.runtime,
      workDir: ref.directory,
      sourceWorkDir: ref.projectDir,
      worktreeBranch: ref.branch,
      isolationMode: ref.sandboxMode,
      status: stage == 'running' ? 'running' : 'active',
      currentStage: stage,
      approvalPolicy: approvalPolicy,
      providerModel: ref.modelId,
      lastTaskId: sessionId,
      currentTaskTitle: ref.title,
      createdAt: ref.createdAt,
      updatedAt: ref.updatedAt,
      lastInstructionAt: ref.updatedAt,
    );
  }

  static String _basename(String path) {
    final parts = path.split(RegExp(r'[/\\]')).where((s) => s.isNotEmpty);
    return parts.isEmpty ? path : parts.last;
  }
}
