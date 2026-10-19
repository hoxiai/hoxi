import '../../../core/network/api_client.dart';
import '../../../core/storage/storage_service.dart';
import '../../ai_workspace/data/hoxi_workspace_adapter.dart';
import '../models/task_attention.dart';

const taskAttentionListPath = '/api/code/attention';

class TaskAttentionRepository {
  final ApiClient _apiClient;

  TaskAttentionRepository(this._apiClient);

  Future<List<TaskAttention>> list() async {
    if (StorageService.isActiveHoxi) {
      try {
        final res = await _apiClient.get<dynamic>('/api/hoxi/permission');
        final list = res.data is List ? (res.data as List) : const [];
        final items = <Map<String, dynamic>>[];
        for (final raw in list.whereType<Map>()) {
          final map = raw.cast<String, dynamic>();
          final permId = (map['id'] ?? '').toString().trim();
          if (permId.isEmpty) continue;
          final sessionStr =
              (map['sessionID'] ?? map['sessionId'] ?? '').toString();
          final sid = sessionStr.isNotEmpty
              ? HoxiWorkspaceAdapter.stablePositiveIntId(sessionStr)
              : 0;
          final title =
              (map['title'] ?? map['permission'] ?? '高风险操作待确认').toString();
          items.add({
            'id': 'hoxi_perm:$permId',
            'type': 'approval_pending',
            'severity': 'warning',
            'title': title,
            'summary': (map['pattern'] ?? map['command'] ?? title).toString(),
            'sessionId': sid,
            'taskId': sid,
            'actions': [
              {
                'type': 'approve',
                'label': '允许执行',
                'method': 'POST',
                'path': '/api/hoxi/permission/$permId/reply',
                'requiresConfirmation': false,
              },
              {
                'type': 'reject',
                'label': '拒绝',
                'method': 'POST',
                'path': '/api/hoxi/permission/$permId/reply?reject=1',
                'requiresConfirmation': false,
              },
            ],
          });
        }
        return parseTaskAttentionList({'items': items});
      } catch (_) {
        return const [];
      }
    }
    final response = await _apiClient.get<Map<String, dynamic>>(
      taskAttentionListPath,
      queryParameters: {'limit': 100},
    );
    return parseTaskAttentionList(response.data);
  }

  Future<void> execute(TaskAttentionAction action) async {
    if (StorageService.isActiveHoxi &&
        action.method == 'POST' &&
        action.path.startsWith('/api/hoxi/permission/')) {
      final isReject =
          action.path.contains('reject=1') || action.type == 'reject';
      final cleanPath = action.path.split('?').first;
      await _apiClient.post<dynamic>(
        cleanPath,
        data: {'reply': isReject ? 'reject' : 'once'},
      );
      return;
    }
    if (action.method != 'POST' || !action.path.startsWith('/api/code/')) {
      throw StateError('当前动作不能直接执行');
    }
    await _apiClient.post<Map<String, dynamic>>(
      action.path,
      data: const <String, dynamic>{},
    );
  }
}
