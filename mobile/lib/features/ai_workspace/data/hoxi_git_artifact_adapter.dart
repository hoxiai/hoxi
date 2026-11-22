import 'dart:convert';

import 'package:dio/dio.dart';

import '../../../core/network/api_client.dart';
import '../models/code_delivery_job.dart';
import '../models/code_execution_run_detail.dart';
import '../models/code_git_review.dart';
import '../models/code_memory_entry.dart';
import '../models/code_quality_check.dart';
import '../models/code_session_flow.dart';
import '../models/code_workspace_file.dart';
import 'hoxi_request_ledger_helper.dart';

class HoxiSessionTarget {
  final String hoxiId, directory, projectDir, branch, sandboxMode, runtime;

  const HoxiSessionTarget({
    required this.hoxiId,
    required this.directory,
    required this.projectDir,
    required this.branch,
    this.sandboxMode = 'local',
    this.runtime = 'opencode',
  });

  bool get isIsolatedWorktree =>
      sandboxMode == 'worktree' ||
      directory.replaceAll(r'\', '/').contains('/.hoxi/worktrees/');

  bool get isRunnerSession =>
      HoxiSessionDiffScope.isRunnerSessionId(hoxiId, runtime: runtime);
}

class HoxiRawSessionDiffs {
  final List<dynamic> diffs;
  final bool fromSessionEndpoint;

  const HoxiRawSessionDiffs({
    required this.diffs,
    required this.fromSessionEndpoint,
  });
}

/// 从会话消息轮次中提取当前会话实际编辑过的文件集合（对齐 PC 端 `session-diffs.ts` 与服务端 `session_diff.go`）
class HoxiSessionDiffScope {
  final Set<String> touchedFiles;
  final bool hasSubagentCalls;
  final bool turnsLoaded;

  const HoxiSessionDiffScope({
    required this.touchedFiles,
    this.hasSubagentCalls = false,
    this.turnsLoaded = true,
  });

  static final RegExp _applyPatchHeaderRe = RegExp(
    r'^\*\*\*\s+(?:Update|Add|Delete)\s+File:\s*(.+)$',
    multiLine: true,
  );
  static final RegExp _systemTempPathRe = RegExp(
    r'^(?:\/(?:private\/)?tmp(?:\/|$)|\/(?:private\/)?var\/folders\/[^\/]+\/[^\/]+\/T(?:\/|$)|[a-z]:[\\\/](?:users[\\\/][^\\\/]+[\\\/]appdata[\\\/]local[\\\/]temp|windows[\\\/]temp)(?:[\\\/]|$))',
    caseSensitive: false,
  );
  static const Set<String> _editingTools = <String>{
    'edit', 'write', 'patch', 'multiedit', 'apply_patch',
    'str_replace_based_edit_tool', 'create_file', 'write_to_file',
    'replace_file_content', 'multi_replace_file_content', 'file_edit', 'notebookedit',
  };
  static const List<String> _inputPathKeys = <String>[
    'filePath', 'file_path', 'Path', 'AbsolutePath', 'path', 'file',
    'TargetFile', 'target_file', 'targetFile', 'filename', 'notebook_path',
  ];

  static bool isRunnerSessionId(String sessionId, {String runtime = ''}) {
    final id = sessionId.trim();
    if (id.startsWith('ses_nat_') || id.startsWith('ses_cdx_') ||
        id.startsWith('ses_hoxi_') || id.startsWith('ses_agy_') ||
        id.startsWith('ses_gem_') || id.startsWith('ses_cld_') ||
        id.startsWith('ses_cbd_') || id.startsWith('ses_qdr_')) {
      return true;
    }
    final rt = runtime.trim().toLowerCase();
    return rt == 'native' || rt == 'codex' ||
        rt == 'antigravity' || rt == 'gemini-cli' || rt == 'claude-code' ||
        rt == 'codebuddy' || rt == 'qoder';
  }

  static String resolveSandboxMode(Map<String, dynamic> map, String workDir) {
    final raw = (map['sandboxMode'] ?? '').toString().trim();
    if (raw.isNotEmpty) return raw;
    final norm = workDir.replaceAll(r'\', '/');
    return norm.contains('/.hoxi/worktrees/') || norm.contains('/.worktree')
        ? 'worktree'
        : 'local';
  }

  static bool isEditingToolName(String tool) {
    final t = tool.trim().toLowerCase();
    if (t.isEmpty || t == 'todowrite' || t == 'todoread' || t == 'glob' ||
        t == 'grep' || t == 'read' || t.endsWith('_read')) {
      return false;
    }
    return _editingTools.contains(t) || t.contains('edit') ||
        t.contains('write') || t.contains('patch') || t.contains('replace');
  }

  static bool isSubagentToolName(String tool) {
    final t = tool.trim().toLowerCase();
    return t == 'task' || t == 'agent' || t.contains('subagent');
  }

  static bool _isUnderDir(String file, String dir) {
    var f = file.trim().replaceAll(r'\', '/');
    var base = dir.trim().replaceAll(r'\', '/').replaceAll(RegExp(r'/+$'), '');
    if (f.isEmpty || base.isEmpty) return false;
    if (RegExp(r'^[A-Za-z]:').hasMatch(f) &&
        RegExp(r'^[A-Za-z]:').hasMatch(base)) {
      f = f.toLowerCase();
      base = base.toLowerCase();
    }
    return f == base || f.startsWith('$base/');
  }

  static bool isScratchFile(
    String rawPath, {
    required String projectDir,
    required String worktreeDir,
  }) {
    final trimmed = rawPath.trim();
    if (trimmed.isEmpty || !_systemTempPathRe.hasMatch(trimmed)) return false;
    for (final root in <String>[worktreeDir, projectDir]) {
      if (root.trim().isNotEmpty && _isUnderDir(trimmed, root)) return false;
    }
    return true;
  }

  static String normalizeSessionRelPath(
    String raw, {
    required String projectDir,
    required String worktreeDir,
  }) {
    var trimmed = raw.trim().replaceAll(r'\', '/');
    if (trimmed.isEmpty) return '';
    for (final root in <String>[worktreeDir, projectDir]) {
      final r = root.trim().replaceAll(r'\', '/').replaceAll(RegExp(r'/+$'), '');
      if (r.isNotEmpty && (trimmed == r || trimmed.startsWith('$r/'))) {
        trimmed = trimmed.substring(r.length).replaceFirst(RegExp(r'^/+'), '');
        break;
      }
    }
    return trimmed
        .replaceFirst(RegExp(r'^\./+'), '')
        .replaceFirst(RegExp(r'^\.hoxi/worktrees/[^/]+/+'), '');
  }

  static bool pathsMatch(
    String candidate,
    String target, {
    required String projectDir,
    required String worktreeDir,
  }) {
    final normA = normalizeSessionRelPath(
      candidate, projectDir: projectDir, worktreeDir: worktreeDir,
    );
    final normB = normalizeSessionRelPath(
      target, projectDir: projectDir, worktreeDir: worktreeDir,
    );
    if (normA.isEmpty || normB.isEmpty) return false;
    return normA == normB || normA.endsWith('/$normB') || normB.endsWith('/$normA');
  }

  bool matchesFile(
    String candidate, {
    required String projectDir,
    required String worktreeDir,
  }) {
    final norm = normalizeSessionRelPath(
      candidate, projectDir: projectDir, worktreeDir: worktreeDir,
    );
    if (norm.isEmpty) return false;
    if (touchedFiles.contains(norm)) return true;
    for (final edited in touchedFiles) {
      if (norm.endsWith('/$edited') || edited.endsWith('/$norm')) return true;
    }
    return false;
  }

  factory HoxiSessionDiffScope.fromTurns(
    List<dynamic> turns, {
    required String projectDir,
    required String worktreeDir,
  }) {
    final touched = <String>{};
    var hasSubagent = false;

    void addPath(Object? raw) {
      if (raw is! String) return;
      final trimmed = raw.trim();
      if (trimmed.isEmpty ||
          isScratchFile(trimmed, projectDir: projectDir, worktreeDir: worktreeDir)) {
        return;
      }
      final norm = normalizeSessionRelPath(
        trimmed, projectDir: projectDir, worktreeDir: worktreeDir,
      );
      if (norm.isNotEmpty) touched.add(norm);
    }

    for (final rawTurn in turns.whereType<Map>()) {
      final parts = rawTurn['parts'];
      if (parts is! List) continue;
      for (final rawPart in parts.whereType<Map>()) {
        final pType = (rawPart['type'] ?? '').toString().trim();
        if (pType != 'tool') continue;
        final toolName = (rawPart['tool'] ?? '').toString().trim();
        if (isSubagentToolName(toolName)) {
          hasSubagent = true;
          continue;
        }
        final state = rawPart['state'];
        if (state is! Map) continue;
        final status =
            (state['status'] ?? 'completed').toString().trim().toLowerCase();
        if (status != 'completed' || !isEditingToolName(toolName)) continue;

        final beforeCount = touched.length;
        final input = state['input'];
        if (input is Map) {
          for (final key in _inputPathKeys) {
            addPath(input[key]);
          }
          final patchText = input['patchText'];
          if (patchText is String && patchText.isNotEmpty) {
            for (final m in _applyPatchHeaderRe.allMatches(patchText)) {
              addPath(m.group(1));
            }
          }
        }
        final meta = state['metadata'];
        if (meta is Map) {
          addPath(meta['filePath']);
          addPath(meta['filepath']);
          final filediff = meta['filediff'];
          if (filediff is Map) {
            addPath(filediff['file']);
            addPath(filediff['path']);
          }
          final files = meta['files'];
          if (files is List) {
            for (final item in files) {
              if (item is String) {
                addPath(item);
              } else if (item is Map) {
                final rel = (item['relativePath'] ?? '').toString().trim();
                final abs = (item['filePath'] ?? '').toString().trim();
                if (rel.isNotEmpty && !rel.startsWith('..')) {
                  addPath(rel);
                } else if (abs.isNotEmpty) {
                  addPath(abs);
                } else {
                  addPath(item['file']);
                  addPath(item['path']);
                }
              }
            }
          }
        }
        if (touched.length == beforeCount) {
          final title = (state['title'] ?? '').toString().trim();
          if (title.isNotEmpty &&
              title.toLowerCase() != toolName.toLowerCase() &&
              !title.contains(' ')) {
            addPath(title);
          }
        }
      }
    }

    return HoxiSessionDiffScope(
      touchedFiles: touched,
      hasSubagentCalls: hasSubagent,
      turnsLoaded: true,
    );
  }

  factory HoxiSessionDiffScope.fromLatestTurn(
    List<dynamic> turns, {
    required String projectDir,
    required String worktreeDir,
  }) {
    var lastUserIdx = -1;
    for (var i = turns.length - 1; i >= 0; i--) {
      final raw = turns[i];
      if (raw is! Map) continue;
      final info = raw['info'] is Map ? raw['info'] as Map : raw;
      final role = (info['role'] ?? '').toString().trim().toLowerCase();
      if (role == 'user') {
        lastUserIdx = i;
        break;
      }
    }
    final cycleTurns =
        lastUserIdx >= 0 ? turns.sublist(lastUserIdx + 1) : turns;
    return HoxiSessionDiffScope.fromTurns(
      cycleTurns,
      projectDir: projectDir,
      worktreeDir: worktreeDir,
    );
  }

  List<CodeGitFile> filterTurnFiles(
    List<CodeGitFile> files, {
    required String projectDir,
    required String worktreeDir,
  }) {
    if (touchedFiles.isEmpty && !hasSubagentCalls) return const [];
    if (touchedFiles.isEmpty && hasSubagentCalls) return files;
    return files
        .where(
          (f) => matchesFile(
            f.path,
            projectDir: projectDir,
            worktreeDir: worktreeDir,
          ),
        )
        .toList();
  }
}

/// Hoxi 节点的工作树 Git、文件浏览、合并交付与质量记忆适配器
class HoxiGitArtifactAdapter {
  final ApiClient _apiClient;

  const HoxiGitArtifactAdapter(this._apiClient);

  Future<HoxiRawSessionDiffs> fetchRawSessionDiffs(
    HoxiSessionTarget ref,
  ) async {
    if (ref.hoxiId.isNotEmpty) {
      try {
        final res = await _apiClient.get<dynamic>(
          '/api/hoxi/sessions/${Uri.encodeComponent(ref.hoxiId)}/diff',
          queryParameters: {'directory': ref.directory},
        );
        if (res.data is List) {
          final list = res.data as List;
          if (list.isNotEmpty || !ref.isIsolatedWorktree) {
            return HoxiRawSessionDiffs(
              diffs: list,
              fromSessionEndpoint: true,
            );
          }
        }
      } catch (_) {
        // 兼容旧版节点或未实现 /sessions/{id}/diff 的环境，回退到 worktree/diff
      }
    }
    final res = await _apiClient.get<dynamic>(
      '/api/hoxi/worktree/diff',
      queryParameters: {'sessionID': ref.hoxiId, 'directory': ref.directory},
    );
    return HoxiRawSessionDiffs(
      diffs: res.data is List ? (res.data as List) : const [],
      fromSessionEndpoint: false,
    );
  }

  CodeGitStatus buildGitStatus(
    HoxiSessionTarget ref,
    HoxiRawSessionDiffs raw, {
    HoxiSessionDiffScope? scope,
  }) {
    final shouldFilterByTurns = !ref.isIsolatedWorktree &&
        scope != null &&
        scope.turnsLoaded &&
        (!raw.fromSessionEndpoint ||
            !ref.isRunnerSession ||
            !scope.hasSubagentCalls);
    final files = <Map<String, dynamic>>[];
    var totalAdditions = 0;
    var totalDeletions = 0;
    for (final d in raw.diffs.whereType<Map>()) {
      final map = d.cast<String, dynamic>();
      final file = (map['file'] ?? map['path'] ?? '').toString();
      if (file.isEmpty) continue;
      if (shouldFilterByTurns &&
          !scope.matchesFile(
            file,
            projectDir: ref.projectDir,
            worktreeDir: ref.directory,
          )) {
        continue;
      }
      final additions = (map['additions'] as num?)?.toInt() ?? 0;
      final deletions = (map['deletions'] as num?)?.toInt() ?? 0;
      final rawStatus = (map['status'] ?? '').toString().toLowerCase();
      final worktreeStatus = rawStatus == 'added'
          ? 'A'
          : (rawStatus == 'deleted' ? 'D' : 'M');
      totalAdditions += additions;
      totalDeletions += deletions;
      files.add({
        'path': file,
        'oldPath': '',
        'workspacePath': file,
        'indexStatus': ' ',
        'worktreeStatus': worktreeStatus,
        'staged': false,
        'changed': true,
        'untracked': rawStatus == 'added',
        'additions': additions,
        'deletions': deletions,
      });
    }
    return CodeGitStatus.fromJson({
      'available': true,
      'reason': '',
      'files': files.length,
      'staged': 0,
      'changed': files.length,
      'untracked': 0,
      'additions': totalAdditions,
      'deletions': totalDeletions,
      'stagedAdditions': 0,
      'stagedDeletions': 0,
      'repositories': [
        {
          'id': 'primary',
          'name': _basename(ref.projectDir),
          'branch': ref.branch.isEmpty ? 'HEAD' : ref.branch,
          'files': files,
          'stagedCount': 0,
          'changedCount': files.length,
          'untrackedCount': 0,
          'additions': totalAdditions,
          'deletions': totalDeletions,
          'stagedAdditions': 0,
          'stagedDeletions': 0,
          'truncated': false,
          'isolated': ref.isIsolatedWorktree,
          'savedCommits': 0,
          'headCommit': '',
        },
      ],
    });
  }

  Future<CodeGitStatus> getGitStatus(
    HoxiSessionTarget ref, {
    HoxiSessionDiffScope? scope,
  }) async {
    final raw = await fetchRawSessionDiffs(ref);
    return buildGitStatus(ref, raw, scope: scope);
  }

  Future<CodeGitDiff> getGitDiff({
    required HoxiSessionTarget ref,
    required String path,
  }) async {
    if (ref.hoxiId.isNotEmpty) {
      try {
        final sessionRes = await _apiClient.get<dynamic>(
          '/api/hoxi/sessions/${Uri.encodeComponent(ref.hoxiId)}/diff',
          queryParameters: {'directory': ref.directory},
        );
        if (sessionRes.data is List) {
          final matched = _findMatchingDiffEntry(
            sessionRes.data as List,
            path,
            projectDir: ref.projectDir,
            worktreeDir: ref.directory,
          );
          if (matched != null) {
            final patch =
                (matched['patch'] ?? matched['diff'] ?? '').toString();
            final after = (matched['after'] ?? '').toString();
            if (patch.isNotEmpty || after.isNotEmpty) {
              return _buildCodeGitDiff(path, matched);
            }
          }
        }
      } catch (_) {}
    }
    final res = await _apiClient.get<dynamic>(
      '/api/hoxi/worktree/diff',
      queryParameters: {
        'sessionID': ref.hoxiId,
        'directory': ref.directory,
        'file': path,
      },
    );
    if (res.data is Map) {
      final map = (res.data as Map).cast<String, dynamic>();
      final file = (map['file'] ?? map['path'] ?? '').toString();
      final patch = (map['patch'] ?? map['diff'] ?? '').toString();
      final after = (map['after'] ?? '').toString();
      if (file.isNotEmpty || patch.isNotEmpty || after.isNotEmpty) {
        return _buildCodeGitDiff(path, map);
      }
    }
    final diffs = res.data is List ? (res.data as List) : const [];
    final matched = _findMatchingDiffEntry(
      diffs,
      path,
      projectDir: ref.projectDir,
      worktreeDir: ref.directory,
    );
    if (matched != null) {
      return _buildCodeGitDiff(path, matched);
    }
    return CodeGitDiff.fromJson({'path': path, 'content': ''});
  }

  static Map<String, dynamic>? _findMatchingDiffEntry(
    List<dynamic> diffs,
    String path, {
    required String projectDir,
    required String worktreeDir,
  }) {
    for (final d in diffs.whereType<Map>()) {
      final map = d.cast<String, dynamic>();
      final file = (map['file'] ?? map['path'] ?? '').toString();
      if (file == path) return map;
    }
    for (final d in diffs.whereType<Map>()) {
      final map = d.cast<String, dynamic>();
      final file = (map['file'] ?? map['path'] ?? '').toString();
      if (HoxiSessionDiffScope.pathsMatch(
        file,
        path,
        projectDir: projectDir,
        worktreeDir: worktreeDir,
      )) {
        return map;
      }
    }
    return null;
  }

  static CodeGitDiff _buildCodeGitDiff(
    String path,
    Map<String, dynamic> map,
  ) {
    final after = (map['after'] ?? '').toString();
    final patch = (map['patch'] ?? map['diff'] ?? '').toString();
    final rawStatus = (map['status'] ?? '').toString().toLowerCase();
    final kind = rawStatus == 'added' || rawStatus == 'deleted'
        ? rawStatus
        : 'modified';
    return CodeGitDiff.fromJson({
      'repositoryId': 'primary',
      'path': path,
      'kind': kind,
      'content': patch.isNotEmpty
          ? patch
          : '--- a/$path\n+++ b/$path\n$after',
      'truncated': map['large'] == true,
    });
  }

  Future<CodeGitSaveResult> saveGitChanges(
    HoxiSessionTarget ref,
    String message, {
    String? clientRequestId,
  }) {
    return HoxiRequestLedgerHelper.commitWorktreeWithRecovery(
      _apiClient,
      ref: ref,
      message: message,
      clientRequestId: clientRequestId,
    );
  }

  Future<CodeStructureResult> getSessionStructure(
    HoxiSessionTarget ref, {
    String path = '',
  }) async {
    final res = await _apiClient.get<dynamic>(
      '/api/hoxi/file/list',
      queryParameters: {
        'path': path,
        'directory': ref.directory,
        if (ref.hoxiId.isNotEmpty) 'sessionID': ref.hoxiId,
      },
    );
    final list = res.data is List ? (res.data as List) : const [];
    final entries = <Map<String, dynamic>>[];
    for (final raw in list.whereType<Map>()) {
      final map = raw.cast<String, dynamic>();
      final name = (map['name'] ?? '').toString();
      final itemPath = (map['path'] ?? name).toString();
      final isDir = map['type'] == 'directory' || map['isDir'] == true;
      entries.add({
        'name': name.isNotEmpty ? name : _basename(itemPath),
        'path': itemPath,
        'isDir': isDir,
        'extension': isDir || !name.contains('.') ? '' : name.split('.').last,
      });
    }
    return CodeStructureResult.fromJson({
      'path': path,
      'truncated': false,
      'entries': entries,
    });
  }

  Future<CodeSessionFile> getSessionFile(HoxiSessionTarget ref, String path) async {
    final norm = HoxiSessionDiffScope.normalizeSessionRelPath(
      path, projectDir: ref.projectDir, worktreeDir: ref.directory,
    );
    final targetPath = norm.isNotEmpty ? norm : path;
    final res = await _apiClient.get<Map<String, dynamic>>(
      '/api/hoxi/file/content',
      queryParameters: {
        'path': targetPath,
        'directory': ref.directory,
        if (ref.hoxiId.isNotEmpty) 'sessionID': ref.hoxiId,
      },
    );
    return CodeSessionFile.fromHoxiContentJson(targetPath, res.data);
  }

  Future<({List<int> bytes, String filename})> downloadSessionFileBytes(
    HoxiSessionTarget ref,
    String path,
  ) async {
    final norm = HoxiSessionDiffScope.normalizeSessionRelPath(
      path, projectDir: ref.projectDir, worktreeDir: ref.directory,
    );
    final targetPath = norm.isNotEmpty ? norm : path;
    final res = await _apiClient.get<dynamic>(
      '/api/hoxi/file/content',
      queryParameters: {
        'path': targetPath, 'directory': ref.directory, 'raw': '1',
        if (ref.hoxiId.isNotEmpty) 'sessionID': ref.hoxiId,
      },
      options: Options(responseType: ResponseType.bytes),
    );
    final raw = res.data;
    final bytes = raw is List<int> ? raw : (raw is String ? utf8.encode(raw) : const <int>[]);
    return (bytes: bytes, filename: _basename(targetPath));
  }

  Future<CodeSessionFile> saveSessionFile(
    HoxiSessionTarget ref, {
    required CodeSessionFile file,
    required String content,
  }) async {
    final payload = {
      'path': file.path,
      'directory': ref.directory,
      'content': content,
      if (file.version.isNotEmpty) 'baseVersion': file.version,
    };
    Map<String, dynamic>? data;
    try {
      final res = await _apiClient.put<Map<String, dynamic>>(
        '/file/content',
        queryParameters: {'path': file.path, 'directory': ref.directory},
        data: payload,
      );
      data = res.data;
    } on DioException catch (e) {
      final code = e.response?.statusCode ?? 0;
      if (code != 404 && code != 405) rethrow;
      final res = await _apiClient.post<Map<String, dynamic>>(
        '/file/content',
        queryParameters: {'path': file.path, 'directory': ref.directory},
        data: payload,
      );
      data = res.data;
    }
    final nextVersion = (data?['version'] ?? '').toString().trim();
    return CodeSessionFile(
      path: (data?['path'] ?? file.path).toString(),
      content: content,
      extension: file.extension,
      size: (data?['size'] as num?)?.toInt() ?? utf8.encode(content).length,
      version: nextVersion.isNotEmpty
          ? nextVersion
          : 'hoxi-${DateTime.now().microsecondsSinceEpoch}',
    );
  }

  Future<CodeDeliveryReview> getDeliveryReview(HoxiSessionTarget ref) async {
    final status = await getGitStatus(ref);
    return CodeDeliveryReview.fromJson({
      'reviewReady': status.hasChanges,
      'reviewRevision': DateTime.now().millisecondsSinceEpoch.toString(),
      'files': status.files,
      'additions': status.totalAdditions,
      'deletions': status.totalDeletions,
    });
  }

  Future<CodeDeliveryJob> startDelivery(
    int sessionId,
    HoxiSessionTarget ref,
  ) async {
    final res = await _apiClient.post<Map<String, dynamic>>(
      '/api/hoxi/worktree/apply',
      queryParameters: {'directory': ref.directory},
      data: {'sessionID': ref.hoxiId, 'mode': 'merge'},
    );
    final resultCommit =
        (res.data?['commitHash'] ?? res.data?['commit'] ?? '').toString();
    return CodeDeliveryJob.fromJson({
      'id': sessionId,
      'sessionId': sessionId,
      'status': 'completed',
      'stage': 'completed',
      'progress': 100,
      'resultCommit': resultCommit,
    });
  }

  Future<CodeSessionFlow> getSessionFlow() async =>
      CodeSessionFlow.fromJson(const {});

  Future<List<CodeQualityCheck>> getQualityChecks(HoxiSessionTarget ref) async {
    try {
      final res = await _apiClient.get<dynamic>(
        '/api/hoxi/quality-checks',
        queryParameters: {'sessionID': ref.hoxiId, 'directory': ref.directory},
      );
      final data = res.data;
      final list = data is Map
          ? (data['items'] as List<dynamic>? ?? const [])
          : (data is List ? data : const []);
      return list
          .whereType<Map>()
          .map(
            (item) => CodeQualityCheck.fromJson(item.cast<String, dynamic>()),
          )
          .toList();
    } catch (_) {
      return const [];
    }
  }

  Future<CodeQualityCheckResult> runQualityCheck(
    HoxiSessionTarget ref,
    String checkId, {
    Duration? receiveTimeout,
  }) async {
    final res = await _apiClient.post<Map<String, dynamic>>(
      '/api/hoxi/quality-checks/run',
      queryParameters: {'sessionID': ref.hoxiId, 'directory': ref.directory},
      data: {
        'sessionID': ref.hoxiId,
        'directory': ref.directory,
        'checkId': checkId,
      },
      options: receiveTimeout == null
          ? null
          : Options(receiveTimeout: receiveTimeout),
    );
    return CodeQualityCheckResult.fromJson(
      res.data ?? const <String, dynamic>{},
    );
  }

  Future<CodeMemoryExtractionStatus> getSessionMemoryStatus() async =>
      CodeMemoryExtractionStatus.fromJson(const {'status': 'idle'});

  Future<CodeMemoryExtractionStatus> extractSessionMemory(
    HoxiSessionTarget ref,
  ) async {
    try {
      final res = await _apiClient.post<Map<String, dynamic>>(
        '/api/hoxi/sessions/${ref.hoxiId}/memory/extract',
        queryParameters: {'directory': ref.directory},
        data: {'sessionID': ref.hoxiId, 'directory': ref.directory},
      );
      final statusData = res.data?['status'] is Map<String, dynamic>
          ? res.data!['status'] as Map<String, dynamic>
          : res.data ?? const <String, dynamic>{'status': 'completed'};
      return CodeMemoryExtractionStatus.fromJson(statusData);
    } catch (_) {
      return getSessionMemoryStatus();
    }
  }

  Future<List<CodeMemoryEntry>> getMemories() async => const [];

  Future<CodeExecutionRunDetail> getExecutionRun(int runId) async =>
      CodeExecutionRunDetail.fromJson({'id': runId, 'status': 'completed'});

  static String _basename(String path) {
    final parts = path.split(RegExp(r'[/\\]')).where((s) => s.isNotEmpty);
    return parts.isEmpty ? path : parts.last;
  }
}
