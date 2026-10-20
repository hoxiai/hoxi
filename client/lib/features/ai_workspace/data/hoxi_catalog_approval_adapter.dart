import 'dart:convert';

import '../../../core/network/api_client.dart';
import '../models/ai_dev_session.dart';

class HoxiApprovalRef {
  final String permissionId;
  final String sessionId;
  final String directory;
  final AiApproval approval;

  const HoxiApprovalRef({
    required this.permissionId,
    required this.sessionId,
    required this.directory,
    required this.approval,
  });
}

/// Hoxi 节点的工作区目录、多运行时执行器发现与安全审批门控适配器
class HoxiCatalogApprovalAdapter {
  final ApiClient _apiClient;
  final int Function(String raw) _stableId;

  const HoxiCatalogApprovalAdapter(this._apiClient, this._stableId);

  static const List<CodeExecutorOption> defaultReasoningEfforts = [
    CodeExecutorOption(value: 'low', label: 'Low'),
    CodeExecutorOption(value: 'medium', label: 'Medium'),
    CodeExecutorOption(value: 'high', label: 'High'),
    CodeExecutorOption(value: 'xhigh', label: 'XHigh'),
    CodeExecutorOption(value: 'max', label: 'Max'),
  ];

  static List<CodeExecutorOption> extractRunnerReasoningEfforts(
    List<dynamic> rawModels, {
    required List<CodeExecutorOption> fallback,
  }) {
    final seen = <String>{};
    for (final raw in rawModels.whereType<Map>()) {
      final efforts = raw['efforts'];
      if (efforts is List) {
        for (final item in efforts) {
          final v = item.toString().trim().toLowerCase();
          if (v.isNotEmpty) seen.add(v);
        }
      }
    }
    if (seen.isEmpty) return fallback;
    const labels = {
      'low': 'Low',
      'medium': 'Medium',
      'high': 'High',
      'xhigh': 'XHigh',
      'max': 'Max',
    };
    final ordered = <CodeExecutorOption>[];
    for (final key in const ['low', 'medium', 'high', 'xhigh', 'max']) {
      if (seen.contains(key)) {
        ordered.add(CodeExecutorOption(value: key, label: labels[key] ?? key));
      }
    }
    return ordered.isEmpty ? fallback : ordered;
  }

  Future<List<CodeProject>> getProjects(
    Map<int, CodeProject> projectsById,
  ) async {
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
        final id = _stableId(primaryPath);
        final project = CodeProject(
          id: id,
          name: name.isNotEmpty ? name : _basename(primaryPath),
          description: 'Hoxi 工作区 · $primaryPath',
          workDir: primaryPath,
          sourceDirs: dirs.isEmpty ? [primaryPath] : dirs,
        );
        projectsById[id] = project;
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
          final id = _stableId(rootDir);
          final project = CodeProject(
            id: id,
            name: _basename(rootDir),
            description: 'Hoxi 默认工作区 · $rootDir',
            workDir: rootDir,
            sourceDirs: [rootDir],
          );
          projectsById[id] = project;
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
        reasoningEfforts: defaultReasoningEfforts,
      ),
    ];

    const runnerSpecs = [
      (
        id: 'claude-code',
        name: 'Claude Code',
        description: 'Anthropic Claude Code CLI 引擎',
        fallbackEfforts: defaultReasoningEfforts,
      ),
      (
        id: 'antigravity',
        name: 'Antigravity',
        description: 'Google Antigravity CLI 引擎',
        fallbackEfforts: [
          CodeExecutorOption(value: 'low', label: 'Low'),
          CodeExecutorOption(value: 'medium', label: 'Medium'),
          CodeExecutorOption(value: 'high', label: 'High'),
          CodeExecutorOption(value: 'max', label: 'Max'),
        ],
      ),
      (
        id: 'qoder',
        name: 'Qoder',
        description: 'Qoder CLI 引擎',
        fallbackEfforts: defaultReasoningEfforts,
      ),
      (
        id: 'codebuddy',
        name: 'CodeBuddy',
        description: 'CodeBuddy CLI 引擎',
        fallbackEfforts: defaultReasoningEfforts,
      ),
    ];

    for (final spec in runnerSpecs) {
      try {
        final res = await _apiClient.get<Map<String, dynamic>>(
          '/api/hoxi/runner/${spec.id}/models',
        );
        if (res.data?['available'] == true) {
          final rawModels = res.data?['models'] as List<dynamic>? ?? const [];
          final models = rawModels
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
              id: spec.id,
              name: spec.name,
              description: spec.description,
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
              reasoningEfforts: extractRunnerReasoningEfforts(
                rawModels,
                fallback: spec.fallbackEfforts,
              ),
            ),
          );
        }
      } catch (_) {}
    }

    return executors;
  }

  Future<List<AiApproval>> getApprovals({
    required Map<int, HoxiApprovalRef> approvalsById,
    required String Function(int sessionIntId) resolveSessionDir,
  }) async {
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
            ? _stableId(hoxiSessionId)
            : 0;
        final approvalIntId = _stableId(permId);
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
        final sessionDir = resolveSessionDir(sessionIntId);
        approvalsById[approvalIntId] = HoxiApprovalRef(
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
    required Map<int, HoxiApprovalRef> approvalsById,
    required Future<List<AiApproval>> Function() reloadApprovals,
  }) async {
    var ref = approvalsById[approvalId];
    if (ref == null) {
      await reloadApprovals();
      ref = approvalsById[approvalId];
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
    approvalsById.remove(approvalId);
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

  static String _basename(String path) {
    final parts = path.split(RegExp(r'[/\\]')).where((s) => s.isNotEmpty);
    return parts.isEmpty ? path : parts.last;
  }
}
