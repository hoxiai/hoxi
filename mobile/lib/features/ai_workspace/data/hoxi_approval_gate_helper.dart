import 'dart:convert';

import '../../../core/network/api_client.dart';
import '../models/ai_dev_session.dart';
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

/// Hoxi 节点的权限审批与交互式问题（Question）门控解析及应答助手。
///
/// 从原 800 行 `HoxiCatalogApprovalAdapter` 中按独立职责拆出，保持目录发现与审批门控分离。
class HoxiApprovalGateHelper {
  const HoxiApprovalGateHelper._();

  static final RegExp _destructiveCmdRe = RegExp(
    r'(\b(rm|sudo|su|chmod|chown|chgrp|dd|mkfs|fdisk|diskutil|shutdown|reboot|kill|killall|pkill|kubectl\s+delete|terraform\s+(destroy|apply)|docker\s+(rm|rmi|system\s+prune)|(npm|pnpm|yarn|cargo)\s+publish)\b|git\s+(push\b.*(-f|--force)|reset\s+--hard|clean\b.*-f|checkout\s+--\s+\.|restore\b.*\s+\.|branch\b.*(-D|-d|--delete)|worktree\s+(remove|prune)))',
    caseSensitive: false,
  );

  static Future<List<AiApproval>> fetchApprovals(
    ApiClient apiClient, {
    required int Function(String raw) stableId,
    required Map<int, HoxiApprovalRef> approvalsById,
    required String Function(int sessionIntId) resolveSessionDir,
  }) async {
    final results = <AiApproval>[];
    try {
      final res = await apiClient.get<dynamic>('/api/hoxi/permission');
      final list = res.data is List ? (res.data as List) : const [];
      for (final raw in list.whereType<Map>()) {
        final map = raw.cast<String, dynamic>();
        final permId = (map['id'] ?? '').toString().trim();
        if (permId.isEmpty) continue;
        final hoxiSessionId =
            (map['sessionID'] ?? map['sessionId'] ?? '').toString().trim();
        final sessionIntId = hoxiSessionId.isNotEmpty
            ? stableId(hoxiSessionId)
            : 0;
        final approvalIntId = stableId(permId);
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
      final qRes = await apiClient.get<dynamic>('/api/hoxi/question');
      final qList = qRes.data is List ? (qRes.data as List) : const [];
      for (final raw in qList.whereType<Map>()) {
        final map = raw.cast<String, dynamic>();
        final qId = (map['id'] ?? '').toString().trim();
        final questions = parseQuestionItems(map);
        if (qId.isEmpty || questions.isEmpty) continue;
        final hoxiSessionId =
            (map['sessionID'] ?? map['sessionId'] ?? '').toString().trim();
        final sessionIntId =
            hoxiSessionId.isNotEmpty ? stableId(hoxiSessionId) : 0;
        final approvalIntId = stableId(qId);
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
      final opts = q.options
          .map((o) => o.label)
          .where((s) => s.isNotEmpty)
          .join(' / ');
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
        ? <String, dynamic>{
            'answers': decodeQuestionAnswers(reason, questionCount),
          }
        : const <String, dynamic>{};
    await apiClient.post<dynamic>(
      '/api/hoxi/question/$questionId/$action',
      queryParameters: query,
      data: data,
    );
  }

  static List<List<String>> decodeQuestionAnswers(
    String? rawReason,
    int questionCount,
  ) {
    final count = questionCount > 0 ? questionCount : 1;
    final trimmed = (rawReason ?? '').trim();
    if (trimmed.startsWith('[')) {
      try {
        final decoded = jsonDecode(trimmed);
        if (decoded is List) {
          final parsed = decoded.map<List<String>>((entry) {
            if (entry is List) {
              return entry
                  .map((e) => e.toString().trim())
                  .where((s) => s.isNotEmpty)
                  .toList();
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
    return List<List<String>>.generate(
      count,
      (idx) => idx == 0 ? fallback : const <String>[],
    );
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

  static Future<AiApproval> replyApproval(
    ApiClient apiClient,
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
        apiClient,
        questionId: ref.permissionId,
        approve: approve,
        directory: ref.directory,
        reason: reason,
        questionCount: ref.approval.questions.length,
      );
    } else {
      await postPermissionReply(
        apiClient,
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

  static ({String title, String content, String riskLevel})
  formatApprovalDetails(Map<String, dynamic> map) {
    final perm = (map['permission'] ?? '').toString().trim();
    final meta = map['metadata'] is Map
        ? (map['metadata'] as Map).cast<String, dynamic>()
        : const <String, dynamic>{};
    final patterns =
        (map['patterns'] is List ? map['patterns'] as List : const [])
            .map((e) => e.toString().trim())
            .where((s) => s.isNotEmpty)
            .toList();
    final cmd = (meta['command'] ?? map['command'] ?? '').toString().trim();
    final desc = (meta['description'] ?? '').toString().trim();
    final toolName =
        (meta['tool'] ?? (patterns.isNotEmpty ? patterns.first : perm))
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
    final isDestructive = _destructiveCmdRe.hasMatch(
      cmd.isNotEmpty ? cmd : body,
    );
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
}
