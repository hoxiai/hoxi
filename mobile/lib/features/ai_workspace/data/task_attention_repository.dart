import 'package:dio/dio.dart';

import '../../../core/network/api_client.dart';
import '../../../core/storage/storage_service.dart';
import '../../../shared/models/task_attention.dart';
import 'hoxi_catalog_approval_adapter.dart';
import 'hoxi_workspace_adapter.dart';

const taskAttentionListPath = '/api/code/attention';

class TaskAttentionRepository {
  final ApiClient _apiClient;

  TaskAttentionRepository(this._apiClient);

  Future<List<TaskAttention>> list() async {
    if (!StorageService.activeNodeContext.isConnected ||
        StorageService.isActiveBusinessService) {
      return const [];
    }
    if (StorageService.isActiveHoxi) {
      final res = await _apiClient.get<dynamic>('/api/hoxi/permission');
      final list = res.data is List ? (res.data as List) : const [];
      final items = <Map<String, dynamic>>[];
      for (final raw in list.whereType<Map>()) {
        final map = raw.cast<String, dynamic>();
        final permId = (map['id'] ?? '').toString().trim();
        if (permId.isEmpty) continue;
        final sessionStr =
            (map['sessionID'] ?? map['sessionId'] ?? '').toString().trim();
        final approvalIntId = HoxiWorkspaceAdapter.stablePositiveIntId(permId);
        final sid = sessionStr.isNotEmpty
            ? HoxiWorkspaceAdapter.stablePositiveIntId(sessionStr)
            : approvalIntId;
        final parsed = HoxiCatalogApprovalAdapter.formatApprovalDetails(map);
        final meta = map['metadata'] is Map
            ? (map['metadata'] as Map).cast<String, dynamic>()
            : const <String, dynamic>{};
        final directory =
            (map['directory'] ?? meta['directory'] ?? '').toString().trim();
        final approveQuery = directory.isNotEmpty
            ? '?directory=${Uri.encodeQueryComponent(directory)}'
            : '';
        final rejectQuery = directory.isNotEmpty
            ? '?reject=1&directory=${Uri.encodeQueryComponent(directory)}'
            : '?reject=1';
        items.add({
          'id': 'hoxi_perm:$permId',
          'type': 'approval_pending',
          'severity': parsed.riskLevel == 'critical' ? 'error' : 'warning',
          'title': parsed.title,
          'summary': parsed.content,
          'sessionId': sid,
          'taskId': sid,
          'approvalId': approvalIntId,
          'actions': [
            {
              'type': 'approve',
              'label': '允许执行',
              'method': 'POST',
              'path': '/api/hoxi/permission/$permId/reply$approveQuery',
              'requiresConfirmation': false,
            },
            {
              'type': 'reject',
              'label': '拒绝',
              'method': 'POST',
              'path': '/api/hoxi/permission/$permId/reply$rejectQuery',
              'requiresConfirmation': false,
            },
            if (sessionStr.isNotEmpty)
              {
                'type': 'open_session',
                'label': '打开会话',
                'method': '',
                'path': '',
                'requiresConfirmation': false,
              },
          ],
        });
      }
      try {
        final qRes = await _apiClient.get<dynamic>('/api/hoxi/question');
        final qList = qRes.data is List ? (qRes.data as List) : const [];
        for (final raw in qList.whereType<Map>()) {
          final map = raw.cast<String, dynamic>();
          final qId = (map['id'] ?? '').toString().trim();
          if (qId.isEmpty) continue;
          final questions = HoxiCatalogApprovalAdapter.parseQuestionItems(map);
          if (questions.isEmpty) continue;
          final sessionStr =
              (map['sessionID'] ?? map['sessionId'] ?? '').toString().trim();
          final approvalIntId = HoxiWorkspaceAdapter.stablePositiveIntId(qId);
          final sid = sessionStr.isNotEmpty
              ? HoxiWorkspaceAdapter.stablePositiveIntId(sessionStr)
              : approvalIntId;
          final directory = (map['directory'] ?? '').toString().trim();
          final firstOptions = questions.first.options
              .map((o) => o.label.trim())
              .where((s) => s.isNotEmpty)
              .toList();
          final defaultAnswer =
              firstOptions.isNotEmpty ? firstOptions.first : '确认';
          final replyParams = <String>[
            if (directory.isNotEmpty)
              'directory=${Uri.encodeQueryComponent(directory)}',
            if (defaultAnswer.isNotEmpty)
              'answer=${Uri.encodeQueryComponent(defaultAnswer)}',
            'count=${questions.length}',
          ];
          final replyQuery =
              replyParams.isEmpty ? '' : '?${replyParams.join('&')}';
          final rejectQuery = directory.isNotEmpty
              ? '?directory=${Uri.encodeQueryComponent(directory)}'
              : '';
          items.add({
            'id': 'hoxi_question:$qId',
            'type': 'approval_pending',
            'severity': 'warning',
            'title': questions.length > 1
                ? '待回答问题 (${questions.length})'
                : '待回答问题',
            'summary': HoxiCatalogApprovalAdapter.formatQuestionSummary(
              questions,
            ),
            'sessionId': sid,
            'taskId': sid,
            'approvalId': approvalIntId,
            'questionOptions': firstOptions,
            'actions': [
              {
                'type': 'approve',
                'label': firstOptions.isNotEmpty
                    ? '选择「${firstOptions.first}」'
                    : '确认回答',
                'method': 'POST',
                'path': '/api/hoxi/question/$qId/reply$replyQuery',
                'requiresConfirmation': false,
              },
              if (sessionStr.isNotEmpty)
                {
                  'type': 'open_session',
                  'label': '打开会话选择',
                  'method': '',
                  'path': '',
                  'requiresConfirmation': false,
                },
              {
                'type': 'reject',
                'label': '跳过问题',
                'method': 'POST',
                'path': '/api/hoxi/question/$qId/reject$rejectQuery',
                'requiresConfirmation': false,
              },
            ],
          });
        }
      } catch (_) {}
      return parseTaskAttentionList({'items': items});
    }
    final response = await _apiClient.get<Map<String, dynamic>>(
      taskAttentionListPath,
      queryParameters: {'limit': 100},
    );
    return parseTaskAttentionList(response.data);
  }

  static TaskAttentionAction withQuestionAnswer(
    TaskAttentionAction action,
    String? answerOverride,
  ) {
    final clean = (answerOverride ?? '').trim();
    if (clean.isEmpty || !action.path.startsWith('/api/hoxi/question/')) {
      return action;
    }
    final uri = Uri.tryParse(action.path);
    if (uri == null) return action;
    final updatedQuery = <String, String>{
      ...uri.queryParameters,
      'answer': clean,
    };
    final updatedUri = uri.replace(queryParameters: updatedQuery);
    return TaskAttentionAction(
      type: action.type,
      label: action.label,
      method: action.method,
      path: updatedUri.toString(),
      requiresConfirmation: action.requiresConfirmation,
    );
  }

  Future<void> execute(
    TaskAttentionAction action, {
    String? clientRequestId,
  }) async {
    final reqId = clientRequestId?.trim() ?? '';
    final options = reqId.isEmpty
        ? null
        : Options(headers: {'X-Idempotency-Key': reqId});
    if (StorageService.isActiveHoxi &&
        action.method == 'POST' &&
        action.path.startsWith('/api/hoxi/permission/')) {
      final parsedUri = Uri.tryParse(action.path);
      final isReject =
          parsedUri?.queryParameters['reject'] == '1' ||
          action.path.contains('reject=1') ||
          action.type == 'reject';
      final directory = (parsedUri?.queryParameters['directory'] ?? '').trim();
      final cleanPath = action.path.split('?').first;
      final segments = cleanPath.split('/');
      final permId = segments.length > 4 ? segments[4] : 'perm';
      await HoxiCatalogApprovalAdapter.postPermissionReply(
        _apiClient,
        permissionId: permId,
        approve: !isReject,
        directory: directory,
        clientRequestId: reqId,
      );
      return;
    }
    if (StorageService.isActiveHoxi &&
        action.method == 'POST' &&
        action.path.startsWith('/api/hoxi/question/')) {
      final parsedUri = Uri.tryParse(action.path);
      final directory = (parsedUri?.queryParameters['directory'] ?? '').trim();
      final cleanPath = action.path.split('?').first;
      final segments = cleanPath.split('/');
      final qId = segments.length > 4 ? segments[4] : 'question';
      final isReject =
          cleanPath.endsWith('/reject') ||
          parsedUri?.queryParameters['reject'] == '1' ||
          action.type == 'reject';
      final count =
          int.tryParse(parsedUri?.queryParameters['count'] ?? '') ?? 1;
      final resolvedAnswer = (parsedUri?.queryParameters['answer'] ?? '').trim();
      await HoxiCatalogApprovalAdapter.postQuestionReply(
        _apiClient,
        questionId: qId,
        approve: !isReject,
        directory: directory,
        reason: resolvedAnswer,
        questionCount: count,
      );
      return;
    }
    if (action.method != 'POST' || !action.path.startsWith('/api/code/')) {
      throw StateError('当前动作不能直接执行');
    }
    await _apiClient.post<Map<String, dynamic>>(
      action.path,
      options: options,
      data: const <String, dynamic>{},
    );
  }
}
