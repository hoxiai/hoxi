import 'dart:math';

import 'package:dio/dio.dart';

import '../../../core/models/node_action_outcome.dart';
import '../../../core/network/api_client.dart';
import '../../../core/network/api_exception.dart';
import '../models/code_git_review.dart';
import 'hoxi_git_artifact_adapter.dart';

class HoxiRequestInspectResult {
  final String kind;
  final String scope;
  final String clientRequestId;
  final String status;
  final int httpStatus;
  final Map<String, dynamic>? result;
  final String errorCode;
  final String errorMessage;
  final Map<String, dynamic> evidence;

  const HoxiRequestInspectResult({
    required this.kind,
    required this.scope,
    required this.clientRequestId,
    required this.status,
    this.httpStatus = 200,
    this.result,
    this.errorCode = '',
    this.errorMessage = '',
    this.evidence = const <String, dynamic>{},
  });

  bool get isCompleted => status == 'completed';
  bool get isOutcomeUnknown => status == 'outcome_unknown';
  bool get isAdmitted => status == 'admitted';
  bool get isNotAdmitted => status == 'not_admitted';

  NodeActionOutcome get actionOutcome {
    if (isCompleted) return NodeActionOutcome.completed;
    if (isOutcomeUnknown) return NodeActionOutcome.unknown;
    if (isAdmitted) return NodeActionOutcome.running;
    return NodeActionOutcome.submitted;
  }

  factory HoxiRequestInspectResult.fromJson(Map<String, dynamic> json) {
    final rawResult = json['result'];
    final rawEvidence = json['evidence'];
    return HoxiRequestInspectResult(
      kind: (json['kind'] ?? '').toString().trim(),
      scope: (json['scope'] ?? '').toString().trim(),
      clientRequestId: (json['clientRequestId'] ?? '').toString().trim(),
      status: (json['status'] ?? '').toString().trim(),
      httpStatus: (json['httpStatus'] as num?)?.toInt() ?? 200,
      result: rawResult is Map ? rawResult.cast<String, dynamic>() : null,
      errorCode: (json['errorCode'] ?? '').toString().trim(),
      errorMessage: (json['errorMessage'] ?? '').toString().trim(),
      evidence: rawEvidence is Map
          ? rawEvidence.cast<String, dynamic>()
          : const <String, dynamic>{},
    );
  }
}

class HoxiRequestOutcomeUnknownException implements Exception {
  final String kind;
  final String clientRequestId;
  final String message;
  final Map<String, dynamic> evidence;

  const HoxiRequestOutcomeUnknownException({
    required this.kind,
    required this.clientRequestId,
    required this.message,
    this.evidence = const <String, dynamic>{},
  });

  bool get headAdvanced => evidence['headAdvanced'] == true;
  bool get workingTreeClean => evidence['workingTreeClean'] == true;
  NodeActionOutcome get actionOutcome => NodeActionOutcome.unknown;

  @override
  String toString() {
    if (kind == 'worktree_commit') {
      if (headAdvanced && workingTreeClean) {
        return '提交结果待确认（未自动重试）：主机连接曾中断，检测到 Git HEAD 已推进且工作区已干净，请刷新核对最新提交。';
      }
      return '提交结果待确认（未自动重试）：请求发往主机后连接中断，请刷新工作树状态确认后再决定是否重新提交。';
    }
    if (kind == 'permission_reply') {
      if (evidence['auditRecorded'] == true || evidence['stillPending'] == false) {
        return '审批回复状态待确认（未自动重试）：连接中断，但主机侧该审批已不再挂起，请刷新列表确认。';
      }
      return '审批回复状态待确认（未自动重试）：连接中断，请刷新待办列表确认后再操作。';
    }
    return message.isNotEmpty ? message : '操作状态待确认（outcome_unknown），未自动重试。';
  }
}

/// 封装 Hoxi P0 请求准入三态账本（admitted / completed / outcome_unknown）的客户端生成、断线重连查询与幂等恢复。
class HoxiRequestLedgerHelper {
  static final Random _rng = Random();

  static String createClientRequestId([String prefix = 'mob']) {
    final ts = DateTime.now().millisecondsSinceEpoch.toRadixString(36);
    final rand = _rng.nextInt(0x7fffffff).toRadixString(36);
    return '$prefix-$ts-$rand';
  }

  static bool shouldInspectAfterError(Object error) {
    if (error is ApiException) {
      if (error.code == -1) {
        return error.msg != '请求已取消';
      }
      if (error.code == 502 || error.code == 503 || error.code == 504) {
        return true;
      }
      if (error.code == 409) {
        final data = error.data;
        if (data is Map) {
          final errCode = (data['code'] ?? '').toString().trim();
          return errCode == 'outcome_unknown' || errCode == 'request_in_progress';
        }
        final lower = error.msg.toLowerCase();
        return lower.contains('outcome is unknown') ||
            lower.contains('interrupted') ||
            lower.contains('still in progress');
      }
      return false;
    }
    if (error is DioException) {
      switch (error.type) {
        case DioExceptionType.connectionTimeout:
        case DioExceptionType.sendTimeout:
        case DioExceptionType.receiveTimeout:
        case DioExceptionType.connectionError:
          return true;
        case DioExceptionType.unknown:
          if (error.response == null) return true;
          break;
        default:
          break;
      }
      final status = error.response?.statusCode ?? 0;
      if (status == 502 || status == 503 || status == 504) return true;
      if (status == 409 && error.response?.data is Map) {
        final code = ((error.response!.data as Map)['code'] ?? '').toString().trim();
        return code == 'outcome_unknown' || code == 'request_in_progress';
      }
    }
    return false;
  }

  static Future<HoxiRequestInspectResult?> inspectWithRetry(
    ApiClient apiClient, {
    required String clientRequestId,
    required String kind,
    required String scope,
    String directory = '',
    Duration retryDelay = const Duration(milliseconds: 220),
  }) async {
    final cid = clientRequestId.trim();
    if (cid.isEmpty || scope.trim().isEmpty) return null;
    for (var attempt = 0; attempt < 2; attempt++) {
      try {
        final res = await apiClient.get<Map<String, dynamic>>(
          '/api/hoxi/requests/${Uri.encodeComponent(cid)}',
          queryParameters: {
            'kind': kind,
            'scope': scope.trim(),
            if (directory.trim().isNotEmpty) 'directory': directory.trim(),
          },
        );
        if (res.data != null) {
          final parsed = HoxiRequestInspectResult.fromJson(res.data!);
          if (parsed.isAdmitted && attempt == 0) {
            await Future<void>.delayed(retryDelay);
            continue;
          }
          return parsed;
        }
      } catch (_) {
        if (attempt == 0) {
          await Future<void>.delayed(retryDelay);
        }
      }
    }
    return null;
  }

  static String resolveCommitScope(HoxiSessionTarget ref) {
    final sid = ref.hoxiId.trim();
    if (sid.isNotEmpty && sid != '__root__') return sid;
    final dir = ref.directory.trim();
    return dir.isNotEmpty ? 'dir:$dir' : '__root__';
  }

  static CodeGitSaveResult _toSaveResult(
    HoxiSessionTarget ref,
    Map<String, dynamic>? payload,
  ) {
    final commitHash =
        (payload?['commitHash'] ?? payload?['commit'] ?? '').toString().trim();
    final branch = (payload?['branch'] ?? '').toString().trim();
    return CodeGitSaveResult.fromJson({
      'status': 'saved',
      'commit': commitHash,
      'branch': branch.isNotEmpty ? branch : ref.branch,
      'repositories': [
        if (commitHash.isNotEmpty) {'commit': commitHash},
      ],
    });
  }

  static Future<CodeGitSaveResult> commitWorktreeWithRecovery(
    ApiClient apiClient, {
    required HoxiSessionTarget ref,
    required String message,
    String? clientRequestId,
    Duration inspectRetryDelay = const Duration(milliseconds: 220),
  }) async {
    final reqId = (clientRequestId != null && clientRequestId.trim().isNotEmpty)
        ? clientRequestId.trim()
        : createClientRequestId('commit');
    try {
      final res = await apiClient.post<Map<String, dynamic>>(
        '/api/hoxi/worktree/commit',
        queryParameters: {'directory': ref.directory},
        options: Options(headers: {'X-Idempotency-Key': reqId}),
        data: {
          'sessionID': ref.hoxiId,
          'directory': ref.directory,
          'message': message.trim(),
          'push': false,
          'clientRequestId': reqId,
        },
      );
      return _toSaveResult(ref, res.data);
    } catch (error) {
      if (!shouldInspectAfterError(error)) rethrow;
      final inspected = await inspectWithRetry(
        apiClient,
        clientRequestId: reqId,
        kind: 'worktree_commit',
        scope: resolveCommitScope(ref),
        directory: ref.directory,
        retryDelay: inspectRetryDelay,
      );
      if (inspected != null) {
        if (inspected.isCompleted) {
          return _toSaveResult(ref, inspected.result);
        }
        if (inspected.isOutcomeUnknown) {
          throw HoxiRequestOutcomeUnknownException(
            kind: 'worktree_commit',
            clientRequestId: reqId,
            message: inspected.errorMessage,
            evidence: inspected.evidence,
          );
        }
      }
      rethrow;
    }
  }

  static Future<void> replyPermissionWithRecovery(
    ApiClient apiClient, {
    required String permissionId,
    required bool approve,
    String directory = '',
    String? reason,
    String? clientRequestId,
    Duration inspectRetryDelay = const Duration(milliseconds: 220),
  }) async {
    final replyDecision = approve ? 'once' : 'reject';
    final reqId = (clientRequestId != null && clientRequestId.trim().isNotEmpty)
        ? clientRequestId.trim()
        : 'perm-$permissionId-$replyDecision';
    try {
      await apiClient.post<dynamic>(
        '/api/hoxi/permission/$permissionId/reply',
        queryParameters: {
          if (directory.trim().isNotEmpty) 'directory': directory.trim(),
        },
        options: Options(headers: {'X-Idempotency-Key': reqId}),
        data: {
          'reply': replyDecision,
          'clientRequestId': reqId,
          if (reason != null && reason.isNotEmpty) 'message': reason,
        },
      );
    } catch (error) {
      if (!shouldInspectAfterError(error)) rethrow;
      final inspected = await inspectWithRetry(
        apiClient,
        clientRequestId: reqId,
        kind: 'permission_reply',
        scope: permissionId,
        directory: directory,
        retryDelay: inspectRetryDelay,
      );
      if (inspected != null) {
        if (inspected.isCompleted) return;
        if (inspected.isOutcomeUnknown) {
          throw HoxiRequestOutcomeUnknownException(
            kind: 'permission_reply',
            clientRequestId: reqId,
            message: inspected.errorMessage,
            evidence: inspected.evidence,
          );
        }
      }
      rethrow;
    }
  }
}
