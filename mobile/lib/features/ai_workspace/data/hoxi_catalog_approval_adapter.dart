import 'dart:convert';

import 'package:dio/dio.dart';

import '../../../core/network/api_client.dart';
import '../models/ai_dev_session.dart';
import '../models/code_model_catalog.dart';
import 'hoxi_request_ledger_helper.dart';

class HoxiApprovalRef {
  final String permissionId;
  final String sessionId;
  final String directory;
  final AiApproval approval;
  final bool isQuestion;

  const HoxiApprovalRef({
    required this.permissionId,
    required this.sessionId,
    required this.directory,
    required this.approval,
    this.isQuestion = false,
  });
}

/// Hoxi 节点的工作区目录、多运行时执行器发现与安全审批门控适配器
class HoxiCatalogApprovalAdapter {
  final ApiClient _apiClient;
  final int Function(String raw) _stableId;
  List<CodeExecutor> _cachedExecutors = const [];
  final Map<String, String> _attachmentDataUrls = {};

  HoxiCatalogApprovalAdapter(this._apiClient, this._stableId);

  List<CodeExecutor> get cachedExecutors => _cachedExecutors;

  Future<Map<String, dynamic>> uploadAttachment({
    required String sessionId,
    required String directory,
    required String filename,
    required String mime,
    required String dataUrl,
  }) async {
    final res = await _apiClient.post<Map<String, dynamic>>(
      '/api/hoxi/attachments',
      queryParameters: {'directory': directory, 'sessionID': sessionId},
      data: {
        'sessionID': sessionId,
        'directory': directory,
        'filename': filename,
        'mime': mime,
        'dataUrl': dataUrl,
      },
    );
    final data = res.data ?? const <String, dynamic>{};
    final trimmed = dataUrl.trim();
    if (trimmed.isNotEmpty) {
      final id = (data['id'] ?? data['attachmentId'] ?? '').toString().trim();
      final url = (data['url'] ?? '').toString().trim();
      if (id.isNotEmpty) {
        _attachmentDataUrls[id] = trimmed;
        _attachmentDataUrls['/api/hoxi/attachments/$id/content'] = trimmed;
      }
      if (url.isNotEmpty) _attachmentDataUrls[url] = trimmed;
    }
    return data;
  }

  Future<String?> resolveFilePartImageDataUrl(Map<String, dynamic> part) async {
    final mime = (part['mime'] ?? '').toString().trim().toLowerCase();
    final filename =
        (part['filename'] ?? part['attachmentId'] ?? part['id'] ?? '')
            .toString()
            .trim();
    final rawUrl = (part['url'] ?? '').toString().trim();
    var attId = (part['attachmentId'] ?? part['id'] ?? '').toString().trim();
    if (attId.isEmpty && rawUrl.isNotEmpty) {
      final m = RegExp(
        r'/api/hoxi/attachments/(att_[A-Za-z0-9_]+)',
      ).firstMatch(rawUrl);
      if (m != null) attId = m.group(1)!;
    }
    final isImage =
        mime.startsWith('image/') ||
        rawUrl.startsWith('data:image/') ||
        (part['kind'] ?? '').toString().trim().toLowerCase() == 'image' ||
        RegExp(
          r'\.(png|jpe?g|webp|gif|bmp|heic|heif|avif)$',
          caseSensitive: false,
        ).hasMatch(filename);
    if (!isImage) return null;
    if (rawUrl.startsWith('data:image/')) return rawUrl;
    if (rawUrl.isNotEmpty && _attachmentDataUrls[rawUrl] != null) {
      return _attachmentDataUrls[rawUrl];
    }
    if (attId.isNotEmpty && _attachmentDataUrls[attId] != null) {
      return _attachmentDataUrls[attId];
    }
    final contentPath = attId.startsWith('att_')
        ? '/api/hoxi/attachments/$attId/content'
        : (rawUrl.startsWith('/api/hoxi/attachments/')
              ? (rawUrl.endsWith('/content') ? rawUrl : '$rawUrl/content')
              : '');
    if (contentPath.isEmpty) return null;
    if (_attachmentDataUrls[contentPath] != null) {
      return _attachmentDataUrls[contentPath];
    }
    try {
      final res = await _apiClient.get<dynamic>(
        contentPath,
        options: Options(responseType: ResponseType.bytes),
      );
      final raw = res.data;
      final effectiveMime = mime.startsWith('image/') ? mime : 'image/png';
      String? dataUrl;
      if (raw is List<int> && raw.isNotEmpty) {
        dataUrl = 'data:$effectiveMime;base64,${base64Encode(raw)}';
      } else if (raw is String && raw.trim().startsWith('data:image/')) {
        dataUrl = raw.trim();
      }
      if (dataUrl == null || dataUrl.isEmpty) return null;
      _attachmentDataUrls[contentPath] = dataUrl;
      if (rawUrl.isNotEmpty) _attachmentDataUrls[rawUrl] = dataUrl;
      if (attId.isNotEmpty) _attachmentDataUrls[attId] = dataUrl;
      return dataUrl;
    } catch (_) {
      return null;
    }
  }

  static const List<CodeExecutorOption> defaultReasoningEfforts =
      CodeModelCatalog.defaultReasoningEfforts;

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
          primaryPath =
              (map['path'] ??
                      map['rootDir'] ??
                      map['directory'] ??
                      map['workDir'] ??
                      '')
                  .toString()
                  .trim();
          if (primaryPath.isNotEmpty) dirs.add(primaryPath);
        }
        if (primaryPath.isEmpty) continue;
        final id = _stableId(primaryPath);
        final isCollapsed =
            map['collapsed'] == true ||
            map['isCollapsed'] == true ||
            map['expanded'] == false;
        final project = CodeProject(
          id: id,
          name: name.isNotEmpty ? name : _basename(primaryPath),
          description: 'Hoxi 工作区 · $primaryPath',
          workDir: primaryPath,
          sourceDirs: dirs.isEmpty ? [primaryPath] : dirs,
          collapsed: isCollapsed,
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
        final rootDir =
            (boot.data?['rootDir'] ??
                    boot.data?['directory'] ??
                    boot.data?['workDir'] ??
                    boot.data?['cwd'] ??
                    '')
                .toString()
                .trim();
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
    final openCodeModels = <CodeExecutorOption>[];

    const runnerSpecs = [
      (
        id: 'claude-code',
        name: 'Claude Code',
        description: 'Anthropic Claude Code CLI 引擎',
        supportsApproval: true,
        fallbackEfforts: defaultReasoningEfforts,
      ),
      (
        id: 'antigravity',
        name: 'Antigravity',
        description: 'Google Antigravity CLI 引擎',
        supportsApproval: true,
        fallbackEfforts: [
          CodeExecutorOption(value: 'low', label: 'Low'),
          CodeExecutorOption(value: 'medium', label: 'Medium'),
          CodeExecutorOption(value: 'high', label: 'High'),
          CodeExecutorOption(value: 'max', label: 'Max'),
        ],
      ),
      (
        id: 'gemini-cli',
        name: 'Gemini CLI',
        description: 'Google Gemini CLI 引擎',
        supportsApproval: true,
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
        supportsApproval: false,
        fallbackEfforts: defaultReasoningEfforts,
      ),
      (
        id: 'codebuddy',
        name: 'CodeBuddy',
        description: 'CodeBuddy CLI 引擎',
        supportsApproval: false,
        fallbackEfforts: defaultReasoningEfforts,
      ),
    ];

    final sysFuture = _apiClient
        .get<Map<String, dynamic>>('/api/hoxi/system-info')
        .timeout(const Duration(milliseconds: 5000))
        .then((sys) => (sys.data?['openCodeVersion'] ?? '').toString())
        .catchError((_) => '');

    final provFuture = _apiClient
        .get<dynamic>('/api/hoxi/provider/models')
        .timeout(const Duration(milliseconds: 6000))
        .then((res) => res.data)
        .catchError((_) => null);

    final runnerFutures = runnerSpecs.map((spec) async {
      try {
        final res = await _apiClient
            .get<Map<String, dynamic>>('/api/hoxi/runner/${spec.id}/models')
            .timeout(const Duration(milliseconds: 5000));
        if (res.data?['available'] == true || res.data?['installed'] == true) {
          final rawModels = res.data?['models'] as List<dynamic>? ?? const [];
          final defaultId = (res.data?['default'] ?? '').toString().trim();
          final parsedModels = rawModels
              .whereType<Map>()
              .map(
                (m) => CodeExecutorOption(
                  value: (m['id'] ?? '').toString(),
                  label: (m['name'] ?? m['id'] ?? '').toString(),
                ),
              )
              .where((o) => o.value.isNotEmpty)
              .toList();
          if (defaultId.isNotEmpty) {
            final defIdx = parsedModels.indexWhere((o) => o.value == defaultId);
            if (defIdx > 0) {
              final defItem = parsedModels.removeAt(defIdx);
              parsedModels.insert(0, defItem);
            }
          }
          final models = parsedModels.isNotEmpty
              ? parsedModels
              : CodeModelCatalog.fallbackOptionsForExecutor(spec.id);
          return CodeExecutor(
            id: spec.id,
            name: spec.name,
            description: spec.description,
            available: true,
            version: '',
            reason: '',
            nativeTerminal: false,
            capabilities: [
              'code',
              'automation',
              'structured_turn',
              if (spec.supportsApproval) 'conversation_approval',
              'resume',
            ],
            approvalPolicies: spec.supportsApproval
                ? const ['safe_auto', 'manual', 'full_auto']
                : const ['safe_auto', 'full_auto'],
            modelOptions: models,
            reasoningEfforts: extractRunnerReasoningEfforts(
              rawModels,
              fallback: spec.fallbackEfforts,
            ),
          );
        }
      } catch (_) {}
      return null;
    });

    final results = await Future.wait<dynamic>([
      sysFuture,
      provFuture,
      ...runnerFutures,
    ]);

    final openCodeVer = results[0] as String;
    final provData = results[1];
    final builtInOpenCodeModels = <CodeExecutorOption>[];
    if (provData is Map) {
      final rawProviders = provData['providers'];
      final providerEntries = <Map<String, dynamic>>[];
      if (rawProviders is List) {
        for (final item in rawProviders.whereType<Map>()) {
          providerEntries.add(item.cast<String, dynamic>());
        }
      } else if (rawProviders is Map) {
        for (final entry in rawProviders.entries) {
          if (entry.value is Map) {
            final map = (entry.value as Map).cast<String, dynamic>();
            providerEntries.add({'id': entry.key.toString(), ...map});
          }
        }
      }
      for (final prov in providerEntries) {
        final provId = (prov['id'] ?? '').toString().trim();
        if (provId.isEmpty) continue;
        final provName = (prov['name'] ?? provId).toString().trim();
        final rawModels = prov['models'];
        final modelEntries = <Map<String, dynamic>>[];
        if (rawModels is List) {
          for (final item in rawModels.whereType<Map>()) {
            modelEntries.add(item.cast<String, dynamic>());
          }
        } else if (rawModels is Map) {
          for (final entry in rawModels.entries) {
            if (entry.value is Map) {
              final map = (entry.value as Map).cast<String, dynamic>();
              modelEntries.add({'id': entry.key.toString(), ...map});
            }
          }
        }
        for (final model in modelEntries) {
          final modelId = (model['id'] ?? '').toString().trim();
          if (modelId.isEmpty) continue;
          final modelName = (model['name'] ?? modelId).toString().trim();
          final value = modelId.contains('/') ? modelId : '$provId/$modelId';
          final option = CodeExecutorOption(
            value: value,
            label: '$provName · $modelName',
          );
          if (provId.toLowerCase() == 'opencode') {
            builtInOpenCodeModels.add(option);
          } else {
            openCodeModels.add(option);
          }
        }
      }
    }

    final hasConfiguredOpenCodeProvider = openCodeModels.isNotEmpty;
    openCodeModels.addAll(builtInOpenCodeModels);

    final runnerExecutors = <CodeExecutor>[
      for (final r in results.skip(2))
        if (r is CodeExecutor) r,
    ];

    final openCodeExecutor = CodeExecutor(
      id: 'opencode',
      name: 'OpenCode',
      description: 'Hoxi 核心 Agent 引擎',
      available: openCodeModels.isNotEmpty || runnerExecutors.isEmpty,
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
      modelOptions: openCodeModels,
      reasoningEfforts: defaultReasoningEfforts,
    );

    final executors = <CodeExecutor>[
      if (hasConfiguredOpenCodeProvider) openCodeExecutor,
      ...runnerExecutors,
      if (!hasConfiguredOpenCodeProvider) openCodeExecutor,
    ];
    _cachedExecutors = executors;
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
        final parsed = formatApprovalDetails(map);
        final approval = AiApproval(
          id: approvalIntId,
          sessionId: sessionIntId,
          taskId: sessionIntId,
          instructionId: 0,
          requestUserId: 1,
          approveUserId: 0,
          title: parsed.title,
          content: parsed.content,
          riskLevel: parsed.riskLevel,
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
    try {
      final qRes = await _apiClient.get<dynamic>('/api/hoxi/question');
      final qList = qRes.data is List ? (qRes.data as List) : const [];
      for (final raw in qList.whereType<Map>()) {
        final map = raw.cast<String, dynamic>();
        final qId = (map['id'] ?? '').toString().trim();
        final questions = parseQuestionItems(map);
        if (qId.isEmpty || questions.isEmpty) continue;
        final hoxiSessionId =
            (map['sessionID'] ?? map['sessionId'] ?? '').toString().trim();
        final sessionIntId =
            hoxiSessionId.isNotEmpty ? _stableId(hoxiSessionId) : 0;
        final approvalIntId = _stableId(qId);
        final now = DateTime.now();
        final approval = AiApproval(
          id: approvalIntId,
          sessionId: sessionIntId,
          taskId: sessionIntId,
          instructionId: 0,
          requestUserId: 1,
          approveUserId: 0,
          title: questions.length > 1
              ? '待回答问题 (${questions.length})'
              : '待回答问题',
          content: formatQuestionSummary(questions),
          riskLevel: 'normal',
          status: 'pending',
          decision: '',
          decisionReason: '',
          createdAt: now,
          updatedAt: now,
          decisionAt: null,
          questions: questions,
        );
        final explicitDir = (map['directory'] ?? '').toString().trim();
        approvalsById[approvalIntId] = HoxiApprovalRef(
          permissionId: qId,
          sessionId: hoxiSessionId,
          directory: explicitDir.isNotEmpty
              ? explicitDir
              : resolveSessionDir(sessionIntId),
          approval: approval,
          isQuestion: true,
        );
        results.add(approval);
      }
    } catch (_) {}
    return results;
  }

  static List<AiQuestionItem> parseQuestionItems(Map<String, dynamic> map) {
    final raw = map['questions'];
    if (raw is! List) return const <AiQuestionItem>[];
    return raw
        .whereType<Map>()
        .map((m) => AiQuestionItem.fromMap(m.cast<String, dynamic>()))
        .where((q) => q.question.isNotEmpty || q.options.isNotEmpty)
        .toList();
  }

  static String formatQuestionSummary(List<AiQuestionItem> questions) {
    final lines = <String>[];
    for (final q in questions) {
      final title = q.question.isNotEmpty ? q.question : q.header;
      final opts = q.options.map((o) => o.label).where((s) => s.isNotEmpty).join(' / ');
      if (title.isNotEmpty) {
        lines.add(opts.isNotEmpty ? '$title ($opts)' : title);
      }
    }
    return lines.isNotEmpty ? lines.join('\n') : '请选择一个选项继续执行';
  }

  static Future<void> postQuestionReply(
    ApiClient apiClient, {
    required String questionId,
    required bool approve,
    String directory = '',
    String? reason,
    int questionCount = 1,
  }) async {
    final query = <String, dynamic>{
      if (directory.trim().isNotEmpty) 'directory': directory.trim(),
    };
    final action = approve ? 'reply' : 'reject';
    final data = approve
        ? <String, dynamic>{'answers': decodeQuestionAnswers(reason, questionCount)}
        : const <String, dynamic>{};
    await apiClient.post<dynamic>(
      '/api/hoxi/question/$questionId/$action',
      queryParameters: query,
      data: data,
    );
  }

  static List<List<String>> decodeQuestionAnswers(String? rawReason, int questionCount) {
    final count = questionCount > 0 ? questionCount : 1;
    final trimmed = (rawReason ?? '').trim();
    if (trimmed.startsWith('[')) {
      try {
        final decoded = jsonDecode(trimmed);
        if (decoded is List) {
          final parsed = decoded.map<List<String>>((entry) {
            if (entry is List) {
              return entry.map((e) => e.toString().trim()).where((s) => s.isNotEmpty).toList();
            }
            final s = entry?.toString().trim() ?? '';
            return s.isEmpty ? const <String>[] : <String>[s];
          }).toList();
          while (parsed.length < count) {
            parsed.add(const <String>[]);
          }
          return parsed;
        }
      } catch (_) {}
    }
    final fallback = trimmed.isEmpty ? const <String>[] : <String>[trimmed];
    return List<List<String>>.generate(count, (idx) => idx == 0 ? fallback : const <String>[]);
  }

  static Future<void> postPermissionReply(
    ApiClient apiClient, {
    required String permissionId,
    required bool approve,
    String directory = '',
    String? reason,
    String? clientRequestId,
  }) {
    return HoxiRequestLedgerHelper.replyPermissionWithRecovery(
      apiClient,
      permissionId: permissionId,
      approve: approve,
      directory: directory,
      reason: reason,
      clientRequestId: clientRequestId,
    );
  }

  Future<AiApproval> replyApproval(
    int approvalId, {
    required bool approve,
    String? reason,
    String? clientRequestId,
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
    if (ref.isQuestion) {
      await postQuestionReply(
        _apiClient,
        questionId: ref.permissionId,
        approve: approve,
        directory: ref.directory,
        reason: reason,
        questionCount: ref.approval.questions.length,
      );
    } else {
      await postPermissionReply(
        _apiClient,
        permissionId: ref.permissionId,
        approve: approve,
        directory: ref.directory,
        reason: reason,
        clientRequestId: clientRequestId,
      );
    }
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

  static final RegExp _destructiveCmdRe = RegExp(
    r'(\b(rm|sudo|su|chmod|chown|chgrp|dd|mkfs|fdisk|diskutil|shutdown|reboot|kill|killall|pkill|kubectl\s+delete|terraform\s+(destroy|apply)|docker\s+(rm|rmi|system\s+prune)|(npm|pnpm|yarn|cargo)\s+publish)\b|git\s+(push\b.*(-f|--force)|reset\s+--hard|clean\b.*-f|checkout\s+--\s+\.|restore\b.*\s+\.|branch\b.*(-D|-d|--delete)|worktree\s+(remove|prune)))',
    caseSensitive: false,
  );

  static ({String title, String content, String riskLevel})
  formatApprovalDetails(Map<String, dynamic> map) {
    final perm = (map['permission'] ?? '').toString().trim();
    final meta = map['metadata'] is Map
        ? (map['metadata'] as Map).cast<String, dynamic>()
        : const <String, dynamic>{};
    final patterns = (map['patterns'] is List ? map['patterns'] as List : const [])
        .map((e) => e.toString().trim())
        .where((s) => s.isNotEmpty)
        .toList();
    final cmd = (meta['command'] ?? map['command'] ?? '').toString().trim();
    final desc = (meta['description'] ?? '').toString().trim();
    final toolName = (meta['tool'] ?? (patterns.isNotEmpty ? patterns.first : perm))
        .toString()
        .trim();

    String body = cmd;
    if (body.isEmpty && perm == 'tool' && meta['args'] != null) {
      try {
        body = const JsonEncoder.withIndent('  ').convert(meta['args']);
      } catch (_) {
        body = meta['args'].toString();
      }
    }
    if (body.isEmpty && patterns.isNotEmpty) {
      body = patterns.join('\n');
    }
    if (body.isEmpty && meta.isNotEmpty) {
      body = jsonEncode(meta);
    }
    if (desc.isNotEmpty && body.isNotEmpty && body != desc) {
      body = '$desc\n$body';
    }
    final isDestructive = _destructiveCmdRe.hasMatch(cmd.isNotEmpty ? cmd : body);
    final rawTitle = (map['title'] ?? '').toString().trim();
    final title = rawTitle.isNotEmpty
        ? rawTitle
        : (perm == 'tool' && toolName.isNotEmpty
              ? '工具调用确认 · $toolName'
              : (perm.isNotEmpty ? '安全确认 · $perm' : '高风险操作待确认'));
    return (
      title: title,
      content: body.isNotEmpty ? body : title,
      riskLevel: isDestructive ? 'critical' : 'high',
    );
  }

  static String _basename(String path) {
    final parts = path.split(RegExp(r'[/\\]')).where((s) => s.isNotEmpty);
    return parts.isEmpty ? path : parts.last;
  }
}
