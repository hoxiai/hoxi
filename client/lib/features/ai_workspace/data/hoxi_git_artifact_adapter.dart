import 'dart:convert';

import '../../../core/network/api_client.dart';
import '../models/code_delivery_job.dart';
import '../models/code_execution_run_detail.dart';
import '../models/code_git_review.dart';
import '../models/code_memory_entry.dart';
import '../models/code_quality_check.dart';
import '../models/code_session_flow.dart';
import '../models/code_workspace_file.dart';

class HoxiSessionTarget {
  final String hoxiId;
  final String directory;
  final String projectDir;
  final String branch;

  const HoxiSessionTarget({
    required this.hoxiId,
    required this.directory,
    required this.projectDir,
    required this.branch,
  });
}

/// Hoxi 节点的工作树 Git、文件浏览、合并交付与质量记忆适配器
class HoxiGitArtifactAdapter {
  final ApiClient _apiClient;

  const HoxiGitArtifactAdapter(this._apiClient);

  Future<CodeGitStatus> getGitStatus(HoxiSessionTarget ref) async {
    final res = await _apiClient.get<dynamic>(
      '/api/hoxi/worktree/diff',
      queryParameters: {'sessionID': ref.hoxiId, 'directory': ref.directory},
    );
    final diffs = res.data is List ? (res.data as List) : const [];
    final files = <Map<String, dynamic>>[];
    var totalAdditions = 0;
    var totalDeletions = 0;
    for (final d in diffs.whereType<Map>()) {
      final map = d.cast<String, dynamic>();
      final file = (map['file'] ?? map['path'] ?? '').toString();
      if (file.isEmpty) continue;
      final additions = (map['additions'] as num?)?.toInt() ?? 0;
      final deletions = (map['deletions'] as num?)?.toInt() ?? 0;
      totalAdditions += additions;
      totalDeletions += deletions;
      files.add({
        'path': file,
        'oldPath': '',
        'workspacePath': file,
        'indexStatus': ' ',
        'worktreeStatus': 'M',
        'staged': false,
        'changed': true,
        'untracked': false,
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
          'isolated': true,
          'savedCommits': 0,
          'headCommit': '',
        },
      ],
    });
  }

  Future<CodeGitDiff> getGitDiff({
    required HoxiSessionTarget ref,
    required String path,
  }) async {
    final res = await _apiClient.get<dynamic>(
      '/api/hoxi/worktree/diff',
      queryParameters: {'sessionID': ref.hoxiId, 'directory': ref.directory},
    );
    final diffs = res.data is List ? (res.data as List) : const [];
    for (final d in diffs.whereType<Map>()) {
      final map = d.cast<String, dynamic>();
      final file = (map['file'] ?? map['path'] ?? '').toString();
      if (file == path) {
        final after = (map['after'] ?? '').toString();
        final patch = (map['patch'] ?? map['diff'] ?? '').toString();
        return CodeGitDiff.fromJson({
          'repositoryId': 'primary',
          'path': path,
          'kind': 'modified',
          'content': patch.isNotEmpty
              ? patch
              : '--- a/$path\n+++ b/$path\n$after',
          'truncated': false,
        });
      }
    }
    return CodeGitDiff.fromJson({'path': path, 'content': ''});
  }

  Future<CodeGitSaveResult> saveGitChanges(
    HoxiSessionTarget ref,
    String message,
  ) async {
    final res = await _apiClient.post<Map<String, dynamic>>(
      '/api/hoxi/worktree/commit',
      queryParameters: {'directory': ref.directory},
      data: {
        'sessionID': ref.hoxiId,
        'directory': ref.directory,
        'message': message.trim(),
        'push': false,
      },
    );
    final commitHash =
        (res.data?['commitHash'] ?? res.data?['commit'] ?? '').toString();
    return CodeGitSaveResult.fromJson({
      'status': 'saved',
      'commit': commitHash,
      'branch': ref.branch,
      'repositories': [
        if (commitHash.isNotEmpty) {'commit': commitHash},
      ],
    });
  }

  Future<CodeStructureResult> getSessionStructure(
    HoxiSessionTarget ref, {
    String path = '',
  }) async {
    final res = await _apiClient.get<dynamic>(
      '/file',
      queryParameters: {'path': path, 'directory': ref.directory},
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

  Future<CodeSessionFile> getSessionFile(
    HoxiSessionTarget ref,
    String path,
  ) async {
    final res = await _apiClient.get<Map<String, dynamic>>(
      '/file/content',
      queryParameters: {'path': path, 'directory': ref.directory},
    );
    final content = (res.data?['content'] ?? '').toString();
    return CodeSessionFile(
      path: path,
      content: content,
      extension: path.contains('.') ? path.split('.').last : '',
      size: utf8.encode(content).length,
      version: 'hoxi',
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
    await _apiClient.post<Map<String, dynamic>>(
      '/api/hoxi/worktree/apply',
      queryParameters: {'directory': ref.directory},
      data: {'sessionID': ref.hoxiId, 'mode': 'merge'},
    );
    return CodeDeliveryJob.fromJson({
      'id': sessionId,
      'sessionId': sessionId,
      'status': 'completed',
      'stage': 'completed',
      'progress': 100,
    });
  }

  Future<CodeSessionFlow> getSessionFlow() async =>
      CodeSessionFlow.fromJson(const {});

  Future<List<CodeQualityCheck>> getQualityChecks() async => const [];

  Future<CodeMemoryExtractionStatus> getSessionMemoryStatus() async =>
      CodeMemoryExtractionStatus.fromJson(const {'status': 'idle'});

  Future<List<CodeMemoryEntry>> getMemories() async => const [];

  Future<CodeExecutionRunDetail> getExecutionRun(int runId) async =>
      CodeExecutionRunDetail.fromJson({'id': runId, 'status': 'completed'});

  static String _basename(String path) {
    final parts = path.split(RegExp(r'[/\\]')).where((s) => s.isNotEmpty);
    return parts.isEmpty ? path : parts.last;
  }
}
