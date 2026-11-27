import 'dart:convert';

import 'package:dio/dio.dart';

import '../../../core/network/api_client.dart';
import '../models/ai_dev_session.dart';
import '../models/code_model_catalog.dart';
import 'hoxi_approval_gate_helper.dart';

export 'hoxi_approval_gate_helper.dart' show HoxiApprovalRef;

/// Hoxi 节点的工作区目录、多运行时执行器发现与会话附件适配器。
///
/// 审批与交互提问门控由 `HoxiApprovalGateHelper` 独立承载，本类仅保留轻量转发接口以兼容调用方。
class HoxiCatalogApprovalAdapter {
  static const int _maxCachedAttachmentKeys = 48;

  final ApiClient _apiClient;
  final int Function(String raw) _stableId;
  List<CodeExecutor> _cachedExecutors = const [];
  String _defaultCoreRuntime = 'native';
  bool _openCodeDaemonReady = false;
  final Map<String, String> _attachmentDataUrls = {};

  HoxiCatalogApprovalAdapter(this._apiClient, this._stableId);

  List<CodeExecutor> get cachedExecutors => _cachedExecutors;
  String get defaultCoreRuntime => _defaultCoreRuntime;
  bool get openCodeDaemonReady => _openCodeDaemonReady;

  static final Options _uploadOptions = Options(
    sendTimeout: const Duration(seconds: 60),
    receiveTimeout: const Duration(seconds: 60),
  );

  void _cacheAttachmentDataUrl(String key, String dataUrl) {
    if (key.isEmpty || dataUrl.isEmpty) return;
    _attachmentDataUrls.remove(key);
    while (_attachmentDataUrls.length >= _maxCachedAttachmentKeys) {
      _attachmentDataUrls.remove(_attachmentDataUrls.keys.first);
    }
    _attachmentDataUrls[key] = dataUrl;
  }

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
      options: _uploadOptions,
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
        _cacheAttachmentDataUrl(id, trimmed);
        _cacheAttachmentDataUrl('/api/hoxi/attachments/$id/content', trimmed);
      }
      if (url.isNotEmpty) _cacheAttachmentDataUrl(url, trimmed);
    }
    return data;
  }

  Future<Map<String, dynamic>> uploadDocumentAttachment({
    required String sessionId,
    required String directory,
    required String filename,
    required String mime,
    required List<int> bytes,
    bool useJsonEnvelope = false,
  }) async {
    final safeMime = mime.trim().isNotEmpty
        ? mime.trim()
        : 'application/octet-stream';
    if (useJsonEnvelope) {
      return uploadAttachment(
        sessionId: sessionId,
        directory: directory,
        filename: filename,
        mime: safeMime,
        dataUrl: 'data:$safeMime;base64,${base64Encode(bytes)}',
      );
    }
    final formData = FormData.fromMap({
      'sessionID': sessionId,
      'directory': directory,
      'file': MultipartFile.fromBytes(
        bytes,
        filename: filename,
        contentType: DioMediaType.parse(safeMime),
      ),
    });
    final res = await _apiClient.post<Map<String, dynamic>>(
      '/api/hoxi/attachments',
      queryParameters: {'directory': directory, 'sessionID': sessionId},
      options: _uploadOptions,
      data: formData,
    );
    return res.data ?? const <String, dynamic>{};
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
      _cacheAttachmentDataUrl(contentPath, dataUrl);
      if (rawUrl.isNotEmpty) _cacheAttachmentDataUrl(rawUrl, dataUrl);
      if (attId.isNotEmpty) _cacheAttachmentDataUrl(attId, dataUrl);
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
        .then((sys) => sys.data)
        .catchError((_) => null);

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

    final sysMap = results[0] as Map<String, dynamic>?;
    final openCodeVer = (sysMap?['openCodeVersion'] ?? '').toString().trim();
    final appVersion = (sysMap?['appVersion'] ?? '').toString().trim();
    final nativeEngineReady = sysMap == null
        ? _defaultCoreRuntime != 'opencode'
        : sysMap['nativeEngineReady'] == true;
    final openCodeReady = sysMap == null
        ? _openCodeDaemonReady
        : sysMap['openCodeReady'] == true;
    final coreRuntimeId = nativeEngineReady ? 'native' : 'opencode';
    _defaultCoreRuntime = coreRuntimeId;
    _openCodeDaemonReady = nativeEngineReady ? openCodeReady : true;
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

    final coreExecutor = CodeExecutor(
      id: coreRuntimeId,
      name: nativeEngineReady ? 'Hoxi Native' : 'OpenCode',
      description: nativeEngineReady ? 'Hoxi 原生 Agent 引擎' : 'Hoxi 核心 Agent 引擎',
      available: openCodeModels.isNotEmpty || runnerExecutors.isEmpty,
      version: nativeEngineReady ? appVersion : openCodeVer,
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
      if (hasConfiguredOpenCodeProvider) coreExecutor,
      ...runnerExecutors,
      if (!hasConfiguredOpenCodeProvider) coreExecutor,
    ];
    _cachedExecutors = executors;
    return executors;
  }

  Future<List<AiApproval>> getApprovals({
    required Map<int, HoxiApprovalRef> approvalsById,
    required String Function(int sessionIntId) resolveSessionDir,
  }) => HoxiApprovalGateHelper.fetchApprovals(
    _apiClient,
    stableId: _stableId,
    approvalsById: approvalsById,
    resolveSessionDir: resolveSessionDir,
  );

  static List<AiQuestionItem> parseQuestionItems(Map<String, dynamic> map) =>
      HoxiApprovalGateHelper.parseQuestionItems(map);

  static String formatQuestionSummary(List<AiQuestionItem> questions) =>
      HoxiApprovalGateHelper.formatQuestionSummary(questions);

  static Future<void> postQuestionReply(
    ApiClient apiClient, {
    required String questionId,
    required bool approve,
    String directory = '',
    String? reason,
    int questionCount = 1,
  }) => HoxiApprovalGateHelper.postQuestionReply(
    apiClient,
    questionId: questionId,
    approve: approve,
    directory: directory,
    reason: reason,
    questionCount: questionCount,
  );

  static Future<void> postPermissionReply(
    ApiClient apiClient, {
    required String permissionId,
    required bool approve,
    String directory = '',
    String? reason,
    String? clientRequestId,
  }) => HoxiApprovalGateHelper.postPermissionReply(
    apiClient,
    permissionId: permissionId,
    approve: approve,
    directory: directory,
    reason: reason,
    clientRequestId: clientRequestId,
  );

  Future<AiApproval> replyApproval(
    int approvalId, {
    required bool approve,
    String? reason,
    String? clientRequestId,
    required Map<int, HoxiApprovalRef> approvalsById,
    required Future<List<AiApproval>> Function() reloadApprovals,
  }) => HoxiApprovalGateHelper.replyApproval(
    _apiClient,
    approvalId,
    approve: approve,
    reason: reason,
    clientRequestId: clientRequestId,
    approvalsById: approvalsById,
    reloadApprovals: reloadApprovals,
  );

  static ({String title, String content, String riskLevel})
  formatApprovalDetails(Map<String, dynamic> map) =>
      HoxiApprovalGateHelper.formatApprovalDetails(map);

  static String _basename(String path) {
    final parts = path.split(RegExp(r'[/\\]')).where((s) => s.isNotEmpty);
    return parts.isEmpty ? path : parts.last;
  }
}
